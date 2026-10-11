package main

// ui_input.go — message routing and key handling for the dashboard views.
// Form/dialog handlers that predate the redesign still live in model.go.

import (
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Update is the single entry point for every message. It handles the UI-only
// messages itself and delegates the rest to update (model.go), then makes sure
// the animation ticker and the run-history cache are in the right state.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case frameMsg:
		return m, m.onFrame()
	case historyLoadedMsg:
		m.onHistoryLoaded(msg)
		return m, nil
	}

	_, cmd := m.update(msg)
	cmds := []tea.Cmd{cmd, m.ensureAnim()}

	switch msg := msg.(type) {
	case TickMsg:
		cmds = append(cmds, m.ensureHistory(true))
	case TaskUpdateMsg:
		if msg.Done {
			for i := range m.scripts {
				if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
					cmds = append(cmds, m.invalidateHistory(m.scripts[i].Config.OutputFolderPath))
					break
				}
			}
		}
	case TaskStartedMsg:
		cmds = append(cmds, m.ensureHistory(false))
	}
	return m, tea.Batch(cmds...)
}

// stopAll sends a stop signal to every running script.
func (m *model) stopAll() {
	for i := range m.scripts {
		if m.scripts[i].Cmd != nil {
			_ = StopTask(m.scripts[i].Cmd)
		}
	}
}

// requestQuit quits at once when idle, or asks first when scripts are running.
func (m *model) requestQuit() (tea.Model, tea.Cmd) {
	if n := m.runningCount(); n > 0 {
		m.ui.helpReturn = m.activeView
		m.activeView = "quit_confirm"
		m.confirmDeleteFocused = 0
		return m, nil
	}
	return m, tea.Quit
}

func (m *model) updateQuitConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	back := func() {
		m.activeView = m.ui.helpReturn
		if m.activeView == "" || m.activeView == "quit_confirm" {
			m.activeView = "main"
		}
	}
	switch msg.String() {
	case "esc", "n":
		back()
	case "y":
		m.stopAll()
		return m, tea.Quit
	case "left", "right", "tab", "shift+tab", "h", "l":
		m.confirmDeleteFocused = 1 - m.confirmDeleteFocused
	case "enter":
		if m.confirmDeleteFocused == 1 {
			m.stopAll()
			return m, tea.Quit
		}
		back()
	}
	return m, nil
}

// openAddForm shows an empty "add script" form.
func (m *model) openAddForm() {
	m.editingAlias = ""
	m.statusMsg = ""
	m.activeView = "form"
	m.focusedInput = 0
	for i := range m.formInputs {
		m.formInputs[i].SetValue("")
		m.formInputs[i].Blur()
	}
	m.formInputs[0].Focus()
}

// openEditForm shows the "edit script" form filled from the selected script.
func (m *model) openEditForm() {
	s := m.selected()
	if s == nil {
		return
	}
	sc := s.Config
	m.editingAlias = sc.NameAlias
	m.statusMsg = ""
	m.activeView = "form"
	m.focusedInput = 0
	vals := []string{sc.NameAlias, sc.Description, sc.Command, sc.OutputFolderPath, sc.Cron, sc.Host, sc.Timeout}
	for i := range m.formInputs {
		m.formInputs[i].SetValue(vals[i])
		m.formInputs[i].Blur()
	}
	m.formInputs[0].Focus()
}

func (m *model) openFilter() {
	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 60
	fi.Width = 28
	fi.SetValue(m.filterQuery)
	fi.CursorEnd()
	fi.Focus()
	m.filterInput = fi
	m.activeView = "filter"
}

func (m *model) openSettings() {
	m.settingsMode = true
	if len(m.settingsInputs) != 2 {
		m.settingsInputs = make([]textinput.Model, 2)
		for i := range m.settingsInputs {
			m.settingsInputs[i] = textinput.New()
		}
	}
	for i := range m.settingsInputs {
		m.settingsInputs[i].Width = 8
		m.settingsInputs[i].CharLimit = 2
		m.settingsInputs[i].Blur()
	}
	m.settingsInputs[0].SetValue(m.config.ProgressBar.Completed)
	m.settingsInputs[1].SetValue(m.config.ProgressBar.Pending)
	m.settingsInputs[0].Focus()
}

func (m *model) openCleanup() {
	m.cleanupMode = true
	m.cleanupModeAll = false
	m.cleanupAlias = ""
	if s := m.selected(); s != nil {
		m.cleanupAlias = s.Config.NameAlias
	} else {
		m.cleanupModeAll = true
	}
	m.cleanupOlderDays = -1
	m.ui.cleanupArmed = false
	m.cleanupInput.SetValue("")
	m.cleanupInput.Focus()
}

// currentLogs returns the log text the Logs tab is showing.
func (m *model) currentLogs() string {
	if m.ui.viewing != nil {
		return m.ui.viewing.logs
	}
	if s := m.selected(); s != nil {
		return s.Logs
	}
	return ""
}

func (m *model) copyLogs() {
	logs := m.currentLogs()
	if logs == "" {
		m.say("No logs to copy")
		return
	}
	text := stripANSI(sanitizeLog(InterpretCarriageReturns(logs)))
	if err := clipboard.WriteAll(text); err != nil {
		m.say("Error: cannot copy to clipboard: %v", err)
		return
	}
	m.say("Log copied to clipboard")
}

// updateMain handles keys on the main (scripts) dashboard.
func (m *model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	onList := m.activePanel == panelLeft

	switch key {
	case "q":
		return m.requestQuit()
	case "?":
		m.openHelp()
		return m, nil
	case ",":
		m.openSettings()
		return m, nil
	case "tab", "shift+tab":
		if onList {
			m.activePanel = panelRight
		} else {
			m.activePanel = panelLeft
		}
		return m, nil
	case "esc":
		switch {
		case m.ui.viewing != nil:
			m.ui.viewing = nil
			m.ui.paused = false
			m.updateViewport()
			m.say("Back to live output")
		case m.filterQuery != "":
			m.filterQuery = ""
			return m, m.afterSelectionChange()
		case !onList:
			m.activePanel = panelLeft
		}
		return m, nil
	case "1", "2", "3":
		m.setTab(int(key[0] - '1'))
		return m, nil
	case "]":
		m.setTab((m.ui.tab + 1) % tabCount)
		return m, nil
	case "[":
		m.setTab((m.ui.tab + tabCount - 1) % tabCount)
		return m, nil
	case "up", "k":
		if onList {
			return m, m.moveSelection(-1)
		}
		m.scrollDetail(-1)
		return m, nil
	case "down", "j":
		if onList {
			return m, m.moveSelection(1)
		}
		m.scrollDetail(1)
		return m, nil
	case "pgup":
		if onList {
			return m, m.moveSelection(-5)
		}
		m.scrollDetail(-m.detailPage())
		return m, nil
	case "pgdown", "pgdn":
		if onList {
			return m, m.moveSelection(5)
		}
		m.scrollDetail(m.detailPage())
		return m, nil
	case "home":
		if onList {
			return m, m.selectEdge(true)
		}
		m.scrollDetailEdge(true)
		return m, nil
	case "end":
		if onList {
			return m, m.selectEdge(false)
		}
		m.scrollDetailEdge(false)
		return m, nil
	case "f":
		m.toggleFollow()
		return m, nil
	case "v":
		m.ui.revealEnv = !m.ui.revealEnv
		return m, nil
	case "p":
		m.parallelMode = !m.parallelMode
		if m.parallelMode {
			m.say("Multi-run mode: PARALLEL — checked scripts start together")
		} else {
			m.say("Multi-run mode: SEQUENTIAL — checked scripts run one after another")
		}
		return m, nil
	case "t":
		m.cycleTheme()
		return m, nil
	case "a":
		m.openAddForm()
		return m, nil
	case "e":
		m.openEditForm()
		return m, nil
	case "enter":
		if !onList && m.ui.tab == tabRuns {
			m.openSelectedRun()
			return m, nil
		}
		if m.selected() != nil {
			m.activeView = "env_form"
			m.statusMsg = ""
			m.focusedEnv = 0
			m.initEnvForm()
		}
		return m, nil
	case "d", "delete":
		if m.selected() != nil {
			m.activeView = "delete_confirm"
			m.confirmDeleteFocused = 0
		}
		return m, nil
	case "/":
		m.openFilter()
		return m, nil
	case "o":
		m.openHTMLOutput()
		return m, nil
	case "h", "H":
		if m.selected() != nil {
			m.setTab(tabRuns)
			m.activePanel = panelRight
			return m, m.ensureHistory(false)
		}
		return m, nil
	case "s":
		m.stopSelected()
		return m, nil
	case "r":
		if s := m.selected(); s != nil && m.hasChecked() == 0 && s.State == "Running" {
			m.say("'%s' is already running — press S to stop it", s.Config.NameAlias)
			return m, nil
		}
		if len(m.scripts) == 0 {
			m.say("No scripts yet — press A to add one")
			return m, nil
		}
		m.ui.viewing = nil
		return m, m.runSelected()
	case " ":
		if s := m.selected(); s != nil {
			s.Checked = !s.Checked
		}
		return m, nil
	case "ctrl+up", "ctrl+down":
		if m.filterQuery != "" {
			m.say("Clear the filter (Esc) before reordering scripts")
			return m, nil
		}
		d := 1
		if key == "ctrl+up" {
			d = -1
		}
		if t := m.cursor + d; len(m.scripts) > 1 && t >= 0 && t < len(m.scripts) {
			m.reorderScript(m.cursor, t)
			m.cursor = t
			m.updateViewport()
		}
		return m, nil
	case "c":
		m.copyLogs()
		return m, nil
	case "x":
		m.openCleanup()
		return m, nil
	case "g":
		m.activeView = "groups"
		m.groupCursor = clampInt(m.groupCursor, 0, max(0, len(m.config.Groups)-1))
		m.groups = m.config.Groups
		return m, nil
	}
	return m, nil
}

// ─── groups view ────────────────────────────────────────────────────────────

// groupMembers returns the group's script indices (-1 for missing aliases).
func (m *model) groupMemberIndices(g GroupConfig) []int {
	byAlias := make(map[string]int, len(m.scripts))
	for i := range m.scripts {
		byAlias[m.scripts[i].Config.NameAlias] = i
	}
	out := make([]int, len(g.Scripts))
	for i, a := range g.Scripts {
		if idx, ok := byAlias[a]; ok {
			out[i] = idx
		} else {
			out[i] = -1
		}
	}
	return out
}

func (m *model) updateGroupView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	onList := m.groupActivePanel == panelLeft
	var g *GroupConfig
	if m.groupCursor >= 0 && m.groupCursor < len(m.groups) {
		g = &m.groups[m.groupCursor]
	}
	stepCount := 0
	if g != nil {
		stepCount = len(g.Scripts)
	}

	switch key {
	case "esc", "g":
		m.activeView = "main"
		m.groupActivePanel = panelLeft
	case "q":
		return m.requestQuit()
	case "?":
		m.openHelp()
	case "tab", "shift+tab":
		if onList {
			m.groupActivePanel = panelRight
		} else {
			m.groupActivePanel = panelLeft
		}
	case "up", "k":
		if onList {
			if m.groupCursor > 0 {
				m.groupCursor--
				m.groupScriptCursor = 0
			}
		} else {
			m.groupScriptCursor = clampInt(m.groupScriptCursor-1, 0, max(0, stepCount-1))
		}
	case "down", "j":
		if onList {
			if m.groupCursor < len(m.groups)-1 {
				m.groupCursor++
				m.groupScriptCursor = 0
			}
		} else {
			m.groupScriptCursor = clampInt(m.groupScriptCursor+1, 0, max(0, stepCount-1))
		}
	case "home":
		if onList {
			m.groupCursor, m.groupScriptCursor = 0, 0
		} else {
			m.groupScriptCursor = 0
		}
	case "end":
		if onList {
			m.groupCursor, m.groupScriptCursor = max(0, len(m.groups)-1), 0
		} else {
			m.groupScriptCursor = max(0, stepCount-1)
		}
	case "a":
		m.editingGroupName = ""
		m.statusMsg = ""
		m.activeView = "group_form"
		m.initGroupForm()
	case "e":
		if g != nil {
			m.editingGroupName = g.Name
			m.statusMsg = ""
			m.activeView = "group_form"
			m.initGroupForm()
			m.groupFormInputs[0].SetValue(g.Name)
			m.groupFormInputs[1].SetValue(g.Description)
			m.groupFormInputs[2].SetValue(yesNo(g.Pipeline))
		}
	case "enter":
		if g == nil {
			break
		}
		if onList {
			m.activeView = "group_members"
			m.initGroupMembers()
			break
		}
		if stepCount > 0 {
			if idx := m.groupMemberIndices(*g)[m.groupScriptCursor]; idx >= 0 {
				m.filterQuery = ""
				m.cursor = idx
				m.activeView = "main"
				m.activePanel = panelRight
				m.setTab(tabLogs)
				return m, m.afterSelectionChange()
			}
			m.say("Error: script '%s' no longer exists", g.Scripts[m.groupScriptCursor])
		}
	case "d", "delete":
		if g != nil {
			m.activeView = "group_delete_confirm"
			m.confirmDeleteFocused = 0
		}
	case "r":
		return m, m.runGroup()
	case "s":
		m.stopGroupScripts()
	case "o":
		m.openGroupHTMLOutputs()
	case "t":
		m.cycleTheme()
	}
	return m, nil
}

func yesNo(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

func isYes(s string) bool {
	switch s {
	case "y", "Y", "yes", "YES", "Yes", "true", "1":
		return true
	}
	return false
}
