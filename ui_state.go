package main

// ui_state.go — UI-only state and the small amount of command glue the
// dashboard needs. Rendering lives in ui.go, key handling in ui_input.go and
// pure drawing helpers in ui_kit.go.

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"
)

const (
	minTermW       = 60
	minTermH       = 17
	frameInterval  = 120 * time.Millisecond // spinner / timer refresh while something runs
	historyTTL     = 10 * time.Second       // refresh period for a *running* script's run list
	historyStale   = 30 * time.Second       // reload on selection if older than this
	maxHistoryKept = 200
	statusTTL      = 8 * time.Second
)

// Detail-pane tabs.
const (
	tabLogs = iota
	tabRuns
	tabConfig
	tabCount
)

var tabNames = [tabCount]string{"Logs", "Runs", "Config"}

// uiState holds everything the interface needs beyond the original model. Its
// zero value is valid (follow mode on, Logs tab, empty caches), so tests and
// code that build a bare model keep working.
type uiState struct {
	tab          int      // active detail tab
	paused       bool     // log auto-follow is paused (user scrolled up)
	logsDirty    bool     // new log text arrived; refresh viewport on next frame
	animating    bool     // fast render ticker is running
	tabScroll    int      // scroll offset for the Config tab
	revealEnv    bool     // show secret-looking env values in the Config tab
	viewing      *runView // an old run shown in the Logs tab instead of live output
	helpScroll   int
	helpReturn   string // view to return to when help closes
	cleanupArmed bool   // an "everything" cleanup needs a second Enter
	hist         map[string]*histEntry
}

// runView is a past run loaded from disk for reading in the Logs tab.
type runView struct {
	taskID int
	state  string
	when   time.Time
	logs   string
}

type histEntry struct {
	items   []TaskSummary // newest first
	loaded  time.Time
	loading bool
}

// historyLoadedMsg carries a run-history listing read off the UI goroutine.
type historyLoadedMsg struct {
	folder string
	items  []TaskSummary
}

// frameMsg drives smooth spinners/timers and throttles log re-rendering.
type frameMsg time.Time

func frameCmd() tea.Cmd {
	return tea.Tick(frameInterval, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// ─── status line ────────────────────────────────────────────────────────────

// say sets the status message shown in the footer.
func (m *model) say(format string, args ...any) {
	m.statusMsg = fmt.Sprintf(format, args...)
	m.statusMsgTime = time.Now()
}

// freshStatus returns the status message if it is still worth showing.
func (m *model) freshStatus() string {
	if m.statusMsg == "" {
		return ""
	}
	if m.statusMsgTime.IsZero() || time.Since(m.statusMsgTime) < statusTTL {
		return m.statusMsg
	}
	return ""
}

// formError returns a recent error message to show inside a dialog (dialogs
// hide the footer, so validation errors must be repeated there).
func (m *model) formError() string {
	if m.statusMsgTime.IsZero() || time.Since(m.statusMsgTime) > statusTTL {
		return ""
	}
	if classifyStatus(m.statusMsg) == levelError {
		return m.statusMsg
	}
	return ""
}

// ─── layout ─────────────────────────────────────────────────────────────────

// uiLayout is the geometry of the main dashboard. It is a pure function of the
// terminal size so it can be computed from WindowSizeMsg and from View alike.
type uiLayout struct {
	tooSmall     bool
	stacked      bool // narrow terminals: list on top, detail below
	w, h         int
	bodyH        int
	listW, listH int
	detailW      int
	ovH          int // overview panel outer height
	contentH     int // content (tabs) panel outer height
	logW, logH   int // log viewport size
}

func computeLayout(w, h int) uiLayout {
	l := uiLayout{w: w, h: h}
	if w < minTermW || h < minTermH {
		l.tooSmall = true
		return l
	}
	l.bodyH = h - 4 // header (2) + footer (2)
	if w >= 100 {
		l.listW = clampInt(w*28/100, 30, 42)
		l.listH = l.bodyH
		l.detailW = w - l.listW
		ovInner := clampInt((l.bodyH-4)/3, 4, 6)
		l.ovH = ovInner + 2
		l.contentH = l.bodyH - l.ovH
	} else {
		l.stacked = true
		l.listW, l.detailW = w, w
		l.listH = clampInt(l.bodyH*28/100, 5, 9)
		rest := l.bodyH - l.listH
		ovInner := clampInt((rest-5)/3, 3, 5)
		l.ovH = ovInner + 2
		l.contentH = rest - l.ovH
	}
	if l.contentH < 5 {
		l.tooSmall = true
		return l
	}
	l.logW = l.detailW - 4
	l.logH = l.contentH - 2
	return l
}

// applyLayout sizes the log viewport for the current terminal.
func (m *model) applyLayout() {
	lay := computeLayout(m.width, m.height)
	if lay.tooSmall {
		return
	}
	m.viewport.Width = lay.logW
	m.viewport.Height = lay.logH
	m.updateViewport()
}

// ─── animation & throttled log refresh ──────────────────────────────────────

func (m *model) anyRunning() bool {
	for i := range m.scripts {
		if m.scripts[i].Cmd != nil || m.scripts[i].State == "Running" {
			return true
		}
	}
	return false
}

func (m *model) runningCount() int {
	n := 0
	for i := range m.scripts {
		if m.scripts[i].Cmd != nil || m.scripts[i].State == "Running" {
			n++
		}
	}
	return n
}

// ensureAnim starts the fast ticker once when work begins.
func (m *model) ensureAnim() tea.Cmd {
	if m.ui.animating || !m.anyRunning() {
		return nil
	}
	m.ui.animating = true
	return frameCmd()
}

func (m *model) onFrame() tea.Cmd {
	m.flushLogs()
	if m.anyRunning() {
		return frameCmd()
	}
	m.ui.animating = false
	return nil
}

// flushLogs applies pending log text to the viewport. Log lines can arrive
// hundreds of times per second; re-wrapping the whole buffer for each one is
// wasteful, so updates are coalesced to the frame rate.
func (m *model) flushLogs() {
	if m.ui.logsDirty {
		m.ui.logsDirty = false
		m.updateViewport()
	}
}

// ─── selection, scrolling ───────────────────────────────────────────────────

func (m *model) isQueued(idx int) bool {
	for _, q := range m.runQueue {
		if q == idx {
			return true
		}
	}
	return false
}

func (m *model) hasChecked() int {
	n := 0
	for i := range m.scripts {
		if m.scripts[i].Checked {
			n++
		}
	}
	return n
}

// selected returns the highlighted script, or nil when the list is empty.
func (m *model) selected() *ScriptState {
	if m.cursor < 0 || m.cursor >= len(m.scripts) {
		return nil
	}
	return &m.scripts[m.cursor]
}

// afterSelectionChange resets per-script view state and kicks off a
// (non-blocking) history load for the newly selected script.
func (m *model) afterSelectionChange() tea.Cmd {
	m.ui.tabScroll = 0
	m.ui.paused = false
	m.ui.viewing = nil
	m.historyCursor = 0
	m.updateViewport()
	return m.ensureHistory(false)
}

// moveSelection moves the list cursor by delta rows within the filtered list.
func (m *model) moveSelection(delta int) tea.Cmd {
	indices := m.filteredIndices()
	if len(indices) == 0 {
		return nil
	}
	pos := 0
	for p, idx := range indices {
		if idx == m.cursor {
			pos = p
			break
		}
	}
	np := clampInt(pos+delta, 0, len(indices)-1)
	if indices[np] == m.cursor {
		return nil
	}
	m.cursor = indices[np]
	return m.afterSelectionChange()
}

// selectEdge jumps to the first or last visible script.
func (m *model) selectEdge(top bool) tea.Cmd {
	indices := m.filteredIndices()
	if len(indices) == 0 {
		return nil
	}
	target := indices[len(indices)-1]
	if top {
		target = indices[0]
	}
	if target == m.cursor {
		return nil
	}
	m.cursor = target
	return m.afterSelectionChange()
}

// scrollDetail scrolls the focused detail tab by delta lines (negative = up).
// Scrolling up in the log pauses auto-follow; reaching the end resumes it.
func (m *model) scrollDetail(delta int) {
	switch m.ui.tab {
	case tabLogs:
		if delta < 0 {
			m.viewport.LineUp(-delta)
		} else {
			m.viewport.LineDown(delta)
		}
		m.ui.paused = !m.viewport.AtBottom()
	case tabRuns:
		if s := m.selected(); s != nil {
			n := len(m.histItems(s.Config.OutputFolderPath))
			m.historyCursor = clampInt(m.historyCursor+delta, 0, max(0, n-1))
		}
	default:
		m.ui.tabScroll = clampInt(m.ui.tabScroll+delta, 0, m.tabMaxScroll())
	}
}

func (m *model) scrollDetailEdge(top bool) {
	switch m.ui.tab {
	case tabLogs:
		if top {
			m.viewport.GotoTop()
		} else {
			m.viewport.GotoBottom()
		}
		m.ui.paused = !m.viewport.AtBottom()
	case tabRuns:
		if s := m.selected(); s != nil {
			n := len(m.histItems(s.Config.OutputFolderPath))
			if top {
				m.historyCursor = 0
			} else {
				m.historyCursor = max(0, n-1)
			}
		}
	default:
		if top {
			m.ui.tabScroll = 0
		} else {
			m.ui.tabScroll = m.tabMaxScroll()
		}
	}
}

// detailPage is the number of rows a PgUp/PgDn moves in the detail pane.
func (m *model) detailPage() int {
	lay := computeLayout(m.width, m.height)
	if lay.tooSmall {
		return 5
	}
	return max(1, lay.contentH-3)
}

func (m *model) toggleFollow() {
	if m.ui.tab != tabLogs {
		return
	}
	if m.ui.paused {
		m.ui.paused = false
		m.viewport.GotoBottom()
		m.say("Following live output")
	} else {
		m.ui.paused = true
		m.say("Log paused — press F to follow again")
	}
}

func (m *model) setTab(t int) {
	if t < 0 || t >= tabCount {
		return
	}
	m.ui.tab = t
	m.ui.tabScroll = 0
}

// tabMaxScroll is the largest valid tabScroll for the Config tab.
func (m *model) tabMaxScroll() int {
	lay := computeLayout(m.width, m.height)
	s := m.selected()
	if lay.tooSmall || s == nil || m.ui.tab != tabConfig {
		return 0
	}
	return max(0, len(m.configLines(s, lay.logW))-(lay.contentH-2))
}

// ─── run-history cache (loaded asynchronously, never during render) ─────────

func (m *model) histItems(folder string) []TaskSummary {
	if e := m.ui.hist[folder]; e != nil {
		return e.items
	}
	return nil
}

func (m *model) histLoading(folder string) bool {
	e := m.ui.hist[folder]
	return e == nil || (e.loading && e.loaded.IsZero())
}

func (m *model) startHistoryLoad(folder string) tea.Cmd {
	if folder == "" {
		return nil
	}
	if m.ui.hist == nil {
		m.ui.hist = map[string]*histEntry{}
	}
	e := m.ui.hist[folder]
	if e == nil {
		e = &histEntry{}
		m.ui.hist[folder] = e
	}
	if e.loading {
		return nil
	}
	e.loading = true
	return func() tea.Msg {
		items, _ := getTaskHistory(folder)
		return historyLoadedMsg{folder: folder, items: items}
	}
}

// ensureHistory loads the selected script's run list when it is missing or
// stale. On ticks it only refreshes while the script is actually running.
func (m *model) ensureHistory(onTick bool) tea.Cmd {
	s := m.selected()
	if s == nil {
		return nil
	}
	folder := s.Config.OutputFolderPath
	e := m.ui.hist[folder]
	switch {
	case e == nil:
		return m.startHistoryLoad(folder)
	case e.loading:
		return nil
	case onTick:
		if s.State == "Running" && time.Since(e.loaded) > historyTTL {
			return m.startHistoryLoad(folder)
		}
	case time.Since(e.loaded) > historyStale:
		return m.startHistoryLoad(folder)
	}
	return nil
}

// invalidateHistory forces a reload (e.g. after a run finishes).
func (m *model) invalidateHistory(folder string) tea.Cmd {
	if e := m.ui.hist[folder]; e != nil {
		e.loading = false
	}
	return m.startHistoryLoad(folder)
}

func (m *model) onHistoryLoaded(msg historyLoadedMsg) {
	if m.ui.hist == nil {
		m.ui.hist = map[string]*histEntry{}
	}
	e := m.ui.hist[msg.folder]
	if e == nil {
		e = &histEntry{}
		m.ui.hist[msg.folder] = e
	}
	items := msg.items
	if len(items) > maxHistoryKept {
		items = items[:maxHistoryKept]
	}
	e.items = items
	e.loaded = time.Now()
	e.loading = false
}

// openSelectedRun loads the run under the Runs-tab cursor into the Logs tab.
// The live script state is left untouched, so a running script keeps running
// and "Esc" returns to its live output.
func (m *model) openSelectedRun() {
	s := m.selected()
	if s == nil {
		return
	}
	items := m.histItems(s.Config.OutputFolderPath)
	if m.historyCursor < 0 || m.historyCursor >= len(items) {
		return
	}
	it := items[m.historyCursor]
	data, err := os.ReadFile(it.FilePath)
	if err != nil {
		m.say("Error: cannot read run #%d: %v", it.TaskID, err)
		return
	}
	var tf TaskYAML
	if err := yaml.Unmarshal(data, &tf); err != nil {
		m.say("Error: cannot parse run #%d: %v", it.TaskID, err)
		return
	}
	m.ui.viewing = &runView{taskID: it.TaskID, state: tf.Task.State, when: it.Timestamp, logs: tf.Task.Logs.Value}
	m.ui.tab = tabLogs
	m.ui.paused = true
	m.updateViewport()
	m.viewport.GotoTop()
	m.say("Viewing run #%d — Esc returns to the live view", it.TaskID)
}

// ─── help screen ────────────────────────────────────────────────────────────

type helpItem struct{ keys, desc string }

type helpSection struct {
	title string
	items []helpItem
}

var helpContent = []helpSection{
	{"Navigate", []helpItem{
		{"↑ ↓  j k", "Move selection (list) or scroll (detail pane)"},
		{"Tab", "Switch focus: script list ↔ detail pane"},
		{"1  2  3", "Detail tabs: Logs · Runs · Config"},
		{"PgUp PgDn", "Scroll one page"},
		{"Home End", "Jump to top / bottom"},
		{"/", "Filter scripts by name or description (Esc clears)"},
	}},
	{"Run", []helpItem{
		{"r", "Run the selected script, or all checked scripts"},
		{"s", "Stop the running script (or all checked)"},
		{"Space", "Check / uncheck a script for multi-run"},
		{"p", "Toggle parallel / sequential mode for multi-run"},
		{"f", "Follow / pause live log output"},
	}},
	{"Inspect", []helpItem{
		{"h", "Open the Runs tab (past runs of this script)"},
		{"Enter", "In Runs: read that run's log · in list: edit config"},
		{"Esc", "Leave a past run and return to live output"},
		{"c", "Copy the visible log (detail pane)"},
		{"o", "Open the latest HTML report in your browser"},
		{"v", "Reveal / hide secret-looking values (Config tab)"},
	}},
	{"Manage", []helpItem{
		{"a  e", "Add a script / edit the selected one"},
		{"Enter", "Edit schedule, environment variables, notifications"},
		{"d", "Delete the selected script"},
		{"x", "Clean up old run records"},
		{"Ctrl+↑ Ctrl+↓", "Reorder scripts"},
	}},
	{"Groups & app", []helpItem{
		{"g", "Open Groups (parallel groups & sequential pipelines)"},
		{"t", "Cycle colour theme"},
		{",", "Settings (progress-bar glyphs)"},
		{"?", "This help"},
		{"q", "Quit (asks first if scripts are running)"},
		{"Ctrl+C", "Quit immediately (stops running scripts)"},
	}},
	{"Forms", []helpItem{
		{"Tab ↓ / Shift+Tab ↑", "Next / previous field"},
		{"Enter", "Next field, or press the focused button"},
		{"Space ← →", "Flip a yes/no field"},
		{"Ctrl+S", "Save from anywhere in the form"},
		{"Esc", "Cancel"},
	}},
	{"In Groups", []helpItem{
		{"↑ ↓", "Move between groups (left) or steps (right)"},
		{"Enter", "Left: edit members · right: open the step's logs"},
		{"r  s", "Run / stop the whole group"},
		{"a  e  d", "Add / edit / delete a group"},
		{"o", "Open HTML reports of all steps"},
		{"Esc  g", "Back to scripts"},
	}},
}

const helpKeyCol = 22

func helpBodyRows(termH int) int { return max(3, termH-8) }

func helpTotalLines() int {
	n := 0
	for i, s := range helpContent {
		if i > 0 {
			n++
		}
		n += 1 + len(s.items)
	}
	return n
}

func (m *model) helpMaxScroll() int {
	return max(0, helpTotalLines()-helpBodyRows(m.height))
}

func (m *model) openHelp() {
	m.ui.helpReturn = m.activeView
	if m.ui.helpReturn == "" || m.ui.helpReturn == "help" {
		m.ui.helpReturn = "main"
	}
	m.ui.helpScroll = 0
	m.activeView = "help"
}

func (m *model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := helpBodyRows(m.height) - 1
	switch msg.String() {
	case "esc", "q", "?", "enter":
		m.activeView = m.ui.helpReturn
		if m.activeView == "" || m.activeView == "help" {
			m.activeView = "main"
		}
	case "up", "k":
		m.ui.helpScroll = clampInt(m.ui.helpScroll-1, 0, m.helpMaxScroll())
	case "down", "j":
		m.ui.helpScroll = clampInt(m.ui.helpScroll+1, 0, m.helpMaxScroll())
	case "pgup":
		m.ui.helpScroll = clampInt(m.ui.helpScroll-page, 0, m.helpMaxScroll())
	case "pgdown", "pgdn", " ":
		m.ui.helpScroll = clampInt(m.ui.helpScroll+page, 0, m.helpMaxScroll())
	case "home":
		m.ui.helpScroll = 0
	case "end":
		m.ui.helpScroll = m.helpMaxScroll()
	}
	return m, nil
}
