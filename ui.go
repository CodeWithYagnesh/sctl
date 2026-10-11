package main

// ui.go — rendering of the dashboards (scripts and groups).
//
// Every panel is produced as a []string of exactly w×h cells by renderPanel,
// so layout can never overflow or wrap whatever the terminal size, theme or
// script output. Dialogs live in ui_dialogs.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func inGroupsFamily(v string) bool { return v == "groups" || strings.HasPrefix(v, "group_") }

// baseIsGroups reports which dashboard sits behind the current dialog.
func (m *model) baseIsGroups() bool {
	v := m.activeView
	if v == "help" || v == "quit_confirm" {
		v = m.ui.helpReturn
	}
	return inGroupsFamily(v)
}

func (m *model) View() string {
	w, h := m.width, m.height
	if w == 0 || h == 0 {
		return ""
	}
	lay := computeLayout(w, h)
	if lay.tooSmall {
		return m.renderTooSmall(w, h)
	}
	m.syncViewport(lay)

	var screen []string
	if m.baseIsGroups() {
		screen = m.renderGroupsScreen(lay)
	} else {
		screen = m.renderMainScreen(lay)
	}
	if panel, top, left, ok := m.activeDialog(w, h); ok {
		screen = overlay(screen, w, panel, top, left)
	}
	return finishScreen(screen, w, h)
}

// finishScreen forces the frame to exactly w×h cells.
func finishScreen(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		} else {
			out[i] = spaces(w)
		}
	}
	return strings.Join(out, "\n")
}

// syncViewport keeps the log viewport in step with the layout.
func (m *model) syncViewport(lay uiLayout) {
	if m.viewport.Width != lay.logW || m.viewport.Height != lay.logH {
		m.viewport.Width, m.viewport.Height = lay.logW, lay.logH
		m.updateViewport()
	}
}

func (m *model) renderTooSmall(w, h int) string {
	msg := []string{
		paintB("Terminal too small", pal.stopped),
		tMuted(fmt.Sprintf("%d×%d — sctl needs at least %d×%d", w, h, minTermW, minTermH)),
		tDim("Resize the window to continue"),
	}
	lines := make([]string, h)
	top := (h - len(msg)) / 2
	for i := range lines {
		lines[i] = ""
		if i >= top && i < top+len(msg) {
			lines[i] = centerText(msg[i-top], w)
		}
	}
	return finishScreen(lines, w, h)
}

// ─── screens ────────────────────────────────────────────────────────────────

func (m *model) renderMainScreen(lay uiLayout) []string {
	lines := m.renderHeader(lay.w)
	list := m.renderListPanel(lay.listW, lay.listH)
	ov := m.renderOverviewPanel(lay.detailW, lay.ovH)
	ct := m.renderContentPanel(lay.detailW, lay.contentH)
	if lay.stacked {
		lines = append(lines, list...)
		lines = append(lines, ov...)
		lines = append(lines, ct...)
	} else {
		lines = append(lines, hjoin(list, append(ov, ct...))...)
	}
	return append(lines, m.renderFooter(lay.w)...)
}

func (m *model) renderGroupsScreen(lay uiLayout) []string {
	lines := m.renderHeader(lay.w)
	if lay.stacked {
		lines = append(lines, m.renderGroupListPanel(lay.w, lay.listH)...)
		lines = append(lines, m.renderGroupDetailPanel(lay.w, lay.bodyH-lay.listH)...)
	} else {
		left := m.renderGroupListPanel(lay.listW, lay.bodyH)
		right := m.renderGroupDetailPanel(lay.detailW, lay.bodyH)
		lines = append(lines, hjoin(left, right)...)
	}
	return append(lines, m.renderFooter(lay.w)...)
}

// ─── header & footer ────────────────────────────────────────────────────────

func (m *model) renderHeader(w int) []string {
	left := chip("SCTL", pal.onAccent, pal.accent)
	if Version != "" && Version != "dev" {
		left += " " + tDim(Version)
	}
	if m.baseIsGroups() {
		left += " " + tDim("›") + " " + paintB("Groups", pal.text)
	}

	running, ok, failed := 0, 0, 0
	for i := range m.scripts {
		switch m.scripts[i].State {
		case "Running":
			running++
		case "Success":
			ok++
		case "Failed":
			failed++
		}
	}
	var stats []string
	if running > 0 {
		stats = append(stats, paintB(fmt.Sprintf("● %d running", running), pal.accent))
	}
	if ok > 0 {
		stats = append(stats, paint(fmt.Sprintf("✓ %d ok", ok), pal.success))
	}
	if failed > 0 {
		stats = append(stats, paintB(fmt.Sprintf("✕ %d failed", failed), pal.fail))
	}
	if len(stats) == 0 {
		stats = append(stats, tDim(fmt.Sprintf("%d scripts", len(m.scripts))))
	}
	right := strings.Join(stats, "  ")
	if !m.baseIsGroups() {
		mode := "SEQUENTIAL"
		if m.parallelMode {
			mode = "PARALLEL"
		}
		right += "   " + chip(mode, pal.muted, pal.surface)
	}
	right += "  " + tDim(time.Now().Format("15:04"))

	line := " " + spread(left, right, w-2)
	return []string{fit(line, w), tDim(strings.Repeat("─", w))}
}

func (m *model) renderFooter(w int) []string {
	var l1 string
	if status := m.freshStatus(); status != "" {
		g, c := statusColor(classifyStatus(status))
		l1 = " " + paintB(g, c) + " " + tText(oneLine(status))
	} else {
		l1 = m.contextLine()
	}
	return []string{fit(l1, w), renderHints(w, m.currentHints())}
}

func (m *model) contextLine() string {
	if m.baseIsGroups() {
		return " " + tDim("Pipelines run steps in order and stop at the first failure; parallel groups start everything at once.")
	}
	if n := m.hasChecked(); n > 0 {
		mode := "one after another"
		if m.parallelMode {
			mode = "all at once"
		}
		return " " + chip(fmt.Sprintf("%d selected", n), pal.onAccent, pal.accent) +
			tMuted(" R runs them "+mode+" · P changes mode · Space unselects")
	}
	return " " + tDim("Press ? for all shortcuts")
}

func (m *model) currentHints() []hint {
	if m.baseIsGroups() {
		return []hint{
			{"R", "run", 0}, {"S", "stop", 1}, {"↑↓", "move", 0}, {"Tab", "pane", 2},
			{"Enter", "open", 1}, {"A", "add", 3}, {"E", "edit", 4}, {"D", "delete", 5},
			{"O", "reports", 6}, {"Esc", "back", 0}, {"?", "help", 0}, {"Q", "quit", 0},
		}
	}
	if m.activePanel == panelLeft {
		return []hint{
			{"R", "run", 0}, {"S", "stop", 1}, {"G", "groups", 2}, {"/", "filter", 3},
			{"Space", "select", 4}, {"Tab", "detail", 2}, {"A", "add", 5}, {"E", "edit", 6},
			{"D", "delete", 8}, {"T", "theme", 9}, {"?", "help", 0}, {"Q", "quit", 0},
		}
	}
	hs := []hint{{"↑↓", "scroll", 0}, {"Tab", "list", 1}, {"1-3", "tabs", 2}}
	switch m.ui.tab {
	case tabLogs:
		hs = append(hs, hint{"F", "follow", 1}, hint{"C", "copy", 3}, hint{"O", "report", 4})
		if m.ui.viewing != nil {
			hs = append(hs, hint{"Esc", "live", 0})
		}
	case tabRuns:
		hs = append(hs, hint{"Enter", "open log", 0})
	case tabConfig:
		hs = append(hs, hint{"V", "reveal", 3}, hint{"Enter", "edit", 2})
	}
	return append(hs, hint{"?", "help", 0}, hint{"Q", "quit", 0})
}

// ─── shared visuals ─────────────────────────────────────────────────────────

func stateColors(state string) (bg, fg string) {
	switch state {
	case "Running":
		return pal.accent, pal.onAccent
	case "Success":
		return pal.success, pal.onSuccess
	case "Failed":
		return pal.fail, pal.onFail
	case "Stopped":
		return pal.stopped, pal.onStopped
	}
	return pal.surface, pal.muted
}

func stateChip(state string, queued bool) string {
	g, _, label := stateVisual(state, queued)
	bg, fg := stateColors(state)
	return chip(g+" "+label, fg, bg)
}

// scriptElapsed returns how long a script has run / ran for.
func scriptElapsed(s *ScriptState) (time.Duration, bool) {
	switch {
	case s.StartedAt.IsZero():
		return 0, false
	case s.State == "Running":
		return time.Since(s.StartedAt), true
	case !s.FinishedAt.IsZero():
		return s.FinishedAt.Sub(s.StartedAt), true
	}
	return 0, false
}

// progressRow draws "bar  42%" in exactly w cells.
func progressRow(s *ScriptState, w int) string {
	barW := max(4, w-6)
	switch s.State {
	case "Running":
		if s.Progress <= 0 {
			return indeterminateBar(barW, pal.accent) + tDim("    …")
		}
		return progressBar(barW, s.Progress, pal.accent) + paintB(fmt.Sprintf(" %3d%%", s.Progress), pal.text)
	case "Success":
		return progressBar(barW, 100, pal.success) + paint(" 100%", pal.success)
	case "Failed", "Stopped":
		c, _ := stateColors(s.State)
		return progressBar(barW, s.Progress, c) + paint(fmt.Sprintf(" %3d%%", s.Progress), c)
	}
	return progressBar(barW, 0, pal.dim) + tDim("    —")
}

func lastLogLine(logs string) string {
	logs = strings.TrimRight(stripANSI(sanitizeLog(InterpretCarriageReturns(logs))), "\n ")
	if logs == "" {
		return ""
	}
	if i := strings.LastIndexByte(logs, '\n'); i >= 0 {
		logs = logs[i+1:]
	}
	return strings.TrimSpace(logs)
}

// ─── script list ────────────────────────────────────────────────────────────

func (m *model) renderListPanel(w, h int) []string {
	indices := m.filteredIndices()
	focused := m.activePanel == panelLeft
	cw := w - 4

	title := paintB("Scripts", pal.text)
	if focused {
		title = paintB("Scripts", pal.accent)
	}
	count := fmt.Sprintf("%d", len(m.scripts))
	if m.filterQuery != "" {
		count = fmt.Sprintf("%d of %d", len(indices), len(m.scripts))
	}

	var body []string
	if m.activeView == "filter" {
		styleInput(&m.filterInput)
		m.filterInput.Width = max(4, cw-4)
		body = append(body, bgWrap(" "+paintB("/", pal.accent)+" "+m.filterInput.View(), cw, pal.surface))
	} else if m.filterQuery != "" {
		body = append(body, paintB("/", pal.accent)+" "+tText(truncate(m.filterQuery, cw-14))+tDim("  Esc clears"))
	}

	switch {
	case len(m.scripts) == 0:
		body = append(body, "", centerText(tMuted("No scripts yet"), cw), centerText(tDim("Press ")+paintB("A", pal.accent)+tDim(" to add your first one"), cw))
		return renderPanel(w, h, panelSpec{title: title, right: tDim(count), focused: focused}, body)
	case len(indices) == 0:
		body = append(body, "", centerText(tMuted(fmt.Sprintf("Nothing matches “%s”", truncate(m.filterQuery, cw-20))), cw))
		return renderPanel(w, h, panelSpec{title: title, right: tDim(count), focused: focused}, body)
	}

	const rowsPer = 2
	capacity := max(1, (h-2-len(body))/rowsPer)
	cursorPos := 0
	for p, idx := range indices {
		if idx == m.cursor {
			cursorPos = p
			break
		}
	}
	m.listOffset = scrollOffset(m.listOffset, len(indices), capacity, cursorPos)
	end := min(len(indices), m.listOffset+capacity)

	anyChecked := m.hasChecked() > 0
	for p := m.listOffset; p < end; p++ {
		r1, r2 := m.scriptRows(indices[p], indices[p] == m.cursor, cw, anyChecked, focused)
		body = append(body, r1, r2)
	}

	right := tDim(count)
	if m.listOffset > 0 || end < len(indices) {
		right = tDim(fmt.Sprintf("%d/%d ", cursorPos+1, len(indices))) + tMuted(scrollArrows(m.listOffset > 0, end < len(indices)))
	}
	return renderPanel(w, h, panelSpec{title: title, right: right, focused: focused}, body)
}

func scrollArrows(up, down bool) string {
	switch {
	case up && down:
		return "▲▼"
	case up:
		return "▲"
	case down:
		return "▼"
	}
	return ""
}

// scriptRows renders one list entry as two rows of exactly cw cells.
func (m *model) scriptRows(idx int, selected bool, cw int, anyChecked, panelFocused bool) (string, string) {
	s := &m.scripts[idx]
	glyph, color, _ := stateVisual(s.State, m.isQueued(idx))

	bar := " "
	if selected {
		bar = paint("▌", pal.accent)
		if !panelFocused {
			bar = paint("▌", pal.dim)
		}
	}
	chk := ""
	if anyChecked {
		chk = tDim("○ ")
		if s.Checked {
			chk = paintB("◉ ", pal.accent)
		}
	}
	nameFg := pal.muted
	if selected {
		nameFg = pal.text
	}

	var tags []string
	if s.Config.Host != "" {
		tags = append(tags, tDim("ssh"))
	}
	if s.Config.Cron != "" {
		tags = append(tags, tDim("cron"))
	}
	if d, ok := scriptElapsed(s); ok {
		tags = append(tags, paint(formatDuration(d), color))
	}
	right := strings.Join(tags, " ")

	left := bar + " " + chk + paint(glyph, color) + " "
	nameW := cw - visibleWidth(left) - visibleWidth(right) - 1
	name := paintB(truncate(oneLine(s.Config.NameAlias), max(3, nameW)), nameFg)
	if !selected {
		name = paint(truncate(oneLine(s.Config.NameAlias), max(3, nameW)), nameFg)
	}
	row1 := spread(left+name, right, cw)

	var row2 string
	pad := "    "
	switch {
	case s.State == "Running":
		row2 = pad + progressRow(s, cw-len(pad))
	case s.Config.Description != "":
		row2 = pad + tDim(truncate(oneLine(s.Config.Description), cw-len(pad)))
	default:
		row2 = pad + tDim(truncate("$ "+oneLine(s.Config.Command), cw-len(pad)))
	}
	if selected {
		return bgWrap(row1, cw, pal.surface), bgWrap(row2, cw, pal.surface)
	}
	return fit(row1, cw), fit(row2, cw)
}

// ─── overview (selected script summary) ─────────────────────────────────────

func (m *model) renderOverviewPanel(w, h int) []string {
	cw := w - 4
	s := m.selected()
	if s == nil {
		return renderPanel(w, h, panelSpec{title: paintB("Overview", pal.text)},
			[]string{"", centerText(tDim("Select a script to see its status"), cw)})
	}

	title := paintB(truncate(oneLine(s.Config.NameAlias), max(6, cw-16)), pal.text)
	right := tDim("never run")
	if s.TaskID > 0 {
		right = tDim(fmt.Sprintf("run #%d", s.TaskID))
	}
	if items := m.histItems(s.Config.OutputFolderPath); s.TaskID == 0 && len(items) > 0 {
		right = tDim(fmt.Sprintf("run #%d", items[0].TaskID))
	}

	// Row 1: state chip and one-line description.
	desc := oneLine(s.Config.Description)
	if desc == "" {
		desc = "$ " + oneLine(s.Config.Command)
	}
	head := stateChip(s.State, m.isQueued(m.cursor)) + "  " + tMuted(truncate(desc, max(4, cw-22)))

	// Row 3: timing.
	var timing string
	if d, ok := scriptElapsed(s); ok {
		timing = tMuted("⏱ ") + paintB(formatDuration(d), pal.text)
		timing += tDim("   started " + s.StartedAt.Format("15:04:05"))
		if !s.FinishedAt.IsZero() && s.State != "Running" {
			timing += tDim("   finished " + humanAgo(s.FinishedAt))
		}
	} else if items := m.histItems(s.Config.OutputFolderPath); len(items) > 0 {
		timing = tDim("last run " + humanAgo(items[0].Timestamp) + " · " + items[0].Timestamp.Format("2006-01-02 15:04"))
	} else if m.histLoading(s.Config.OutputFolderPath) {
		timing = tDim("loading history…")
	} else {
		timing = tDim("never run — press R to start")
	}
	if s.Config.Cron != "" {
		timing = spread(timing, tDim("◷ "+s.Config.Cron), cw)
	}

	// Row 4: resource sparklines.
	var metrics []string
	if len(s.CPUHistory) == 0 && len(s.MemHistory) == 0 {
		metrics = []string{tDim("CPU and memory graphs appear while the script runs")}
	} else {
		cpu, mem := s.CPUPercent, s.MemMB
		if s.State != "Running" {
			cpu, mem = 0, 0
		}
		if cw >= 64 {
			half := (cw - 3) / 2
			cl := tMuted(fmt.Sprintf("CPU %5.1f%% ", cpu))
			ml := tMuted(fmt.Sprintf("MEM %5s ", formatMem(mem)))
			metrics = []string{
				cl + sparkline(s.CPUHistory, half-visibleWidth(cl), 100, pal.accent) + "   " +
					ml + sparkline(s.MemHistory, half-visibleWidth(ml), 0, pal.stopped),
			}
		} else {
			cl := tMuted(fmt.Sprintf("CPU %5.1f%% ", cpu))
			ml := tMuted(fmt.Sprintf("MEM %5s ", formatMem(mem)))
			metrics = []string{
				cl + sparkline(s.CPUHistory, cw-visibleWidth(cl), 100, pal.accent),
				ml + sparkline(s.MemHistory, cw-visibleWidth(ml), 0, pal.stopped),
			}
		}
	}

	body := []string{head, progressRow(s, cw), timing}
	body = append(body, metrics...)
	if s.State == "Running" || s.State == "Failed" {
		if ll := lastLogLine(s.Logs); ll != "" {
			body = append(body, tDim("› ")+tMuted(truncate(oneLine(ll), cw-2)))
		}
	}
	return renderPanel(w, h, panelSpec{title: title, right: right}, body)
}

// ─── content panel (Logs / Runs / Config) ───────────────────────────────────

func (m *model) renderContentPanel(w, h int) []string {
	focused := m.activePanel == panelRight
	cw := w - 4
	s := m.selected()

	labels := make([]string, tabCount)
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if i == tabRuns && s != nil {
			if n := len(m.histItems(s.Config.OutputFolderPath)); n > 0 {
				label += fmt.Sprintf(" %d", n)
			}
		}
		if i == m.ui.tab {
			st := lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color(pal.text))
			if focused {
				st = st.Foreground(lipgloss.Color(pal.accent))
			}
			labels[i] = st.Render(label)
		} else {
			labels[i] = tDim(label)
		}
	}
	title := strings.Join(labels, "   ")

	if s == nil {
		return renderPanel(w, h, panelSpec{title: title, focused: focused},
			[]string{"", centerText(tDim("Nothing selected"), cw)})
	}

	var body []string
	var right string
	switch m.ui.tab {
	case tabRuns:
		body, right = m.runsBody(s, cw, h-2)
	case tabConfig:
		lines := m.configLines(s, cw)
		off := clampInt(m.ui.tabScroll, 0, max(0, len(lines)-(h-2)))
		body = lines[off:]
		if len(lines) > h-2 {
			right = tDim(fmt.Sprintf("%d%%", scrollPct(off, len(lines)-(h-2))))
		}
	default:
		body, right = m.logsBody(s, cw, h-2)
	}
	return renderPanel(w, h, panelSpec{title: title, right: right, focused: focused}, body)
}

func scrollPct(off, maxOff int) int {
	if maxOff <= 0 {
		return 100
	}
	return off * 100 / maxOff
}

func (m *model) logsBody(s *ScriptState, cw, rows int) ([]string, string) {
	var right string
	switch {
	case m.ui.viewing != nil:
		v := m.ui.viewing
		right = chip(fmt.Sprintf("RUN #%d · %s", v.taskID, strings.ToUpper(v.state)), pal.onStopped, pal.stopped) + tDim(" Esc → live")
	case s.State == "Running" && m.ui.paused:
		right = chip("⏸ PAUSED", pal.onStopped, pal.stopped) + tDim(" F follows")
	case s.State == "Running":
		right = paintB("● LIVE", pal.accent)
	}

	logs := s.Logs
	if m.ui.viewing != nil {
		logs = m.ui.viewing.logs
	}
	if strings.TrimSpace(logs) == "" {
		var msg, hint string
		switch {
		case s.State == "Running":
			msg, hint = spinnerFrame()+"  Waiting for output…", "The script is running but has not printed anything yet"
		case s.State == "Idle" || s.State == "":
			msg, hint = "No output yet", "Press R to run "+s.Config.NameAlias
		default:
			msg, hint = "This run produced no output", ""
		}
		lines := make([]string, rows)
		mid := max(0, rows/2-1)
		if mid < rows {
			lines[mid] = centerText(tMuted(msg), cw)
		}
		if hint != "" && mid+1 < rows {
			lines[mid+1] = centerText(tDim(hint), cw)
		}
		return lines, right
	}

	view := strings.Split(m.viewport.View(), "\n")
	if len(view) > rows {
		view = view[:rows]
	}
	if m.viewport.TotalLineCount() > m.viewport.Height {
		pct := int(m.viewport.ScrollPercent() * 100)
		if right != "" {
			right += " "
		}
		right += tDim(fmt.Sprintf("%d%%", pct))
	}
	return view, right
}

func (m *model) runsBody(s *ScriptState, cw, rows int) ([]string, string) {
	folder := s.Config.OutputFolderPath
	items := m.histItems(folder)
	if len(items) == 0 {
		msg := "No runs recorded yet"
		if m.histLoading(folder) {
			msg = spinnerFrame() + "  Loading runs…"
		}
		return []string{"", centerText(tMuted(msg), cw)}, ""
	}

	header := tDim(fit("  RUN      RESULT      STARTED", cw-12) + fit("WHEN", 12))
	body := []string{header}
	capacity := max(1, rows-1)
	cur := clampInt(m.historyCursor, 0, len(items)-1)
	start := scrollOffsetFor(len(items), capacity, cur)
	focused := m.activePanel == panelRight
	for i := start; i < min(len(items), start+capacity); i++ {
		it := items[i]
		g, c, label := stateVisual(it.State, false)
		mark := "  "
		if i == cur {
			mark = paint("▌ ", pal.accent)
		}
		extra := ""
		switch {
		case m.ui.viewing != nil && m.ui.viewing.taskID == it.TaskID:
			extra = "  " + paint("◀ viewing", pal.stopped)
		case s.State == "Running" && it.TaskID == s.TaskID:
			extra = "  " + paint("● live", pal.accent)
		}
		row := mark + tText(fit(fmt.Sprintf("#%d", it.TaskID), 7)) + " " +
			paint(g+" "+fit(label, 8), c) + "  " +
			tMuted(it.Timestamp.Format("2006-01-02 15:04")) + extra
		row = spread(row, tDim(humanAgo(it.Timestamp)), cw)
		if i == cur && focused {
			row = bgWrap(row, cw, pal.surface)
		}
		body = append(body, row)
	}
	right := tDim(fmt.Sprintf("%d/%d", cur+1, len(items)))
	return body, right
}

// scrollOffsetFor centres the cursor in a window without needing state.
func scrollOffsetFor(total, visible, cursor int) int {
	start, _ := window(total, visible, cursor)
	return start
}

// configLines lists the script's settings and environment as styled lines.
func (m *model) configLines(s *ScriptState, cw int) []string {
	label := func(k string) string { return tMuted(fit(k, 11)) }
	valW := max(10, cw-11)
	var out []string

	kv := func(k, v string) {
		if v == "" {
			out = append(out, label(k)+tDim("—"))
			return
		}
		wrapped := strings.Split(lipgloss.NewStyle().Width(valW).Render(oneLine(v)), "\n")
		for i, l := range wrapped {
			l = strings.TrimRight(l, " ")
			if i == 0 {
				out = append(out, label(k)+tText(l))
			} else {
				out = append(out, spaces(11)+tText(l))
			}
		}
	}

	out = append(out, paintB("Script", pal.accent))
	kv("Command", s.Config.Command)
	kv("Output", s.Config.OutputFolderPath)
	kv("Schedule", s.Config.Cron)
	if s.Config.Host != "" {
		kv("Host", "ssh "+s.Config.Host)
	} else {
		kv("Host", "this machine")
	}
	if s.Config.Timeout != "" {
		kv("Timeout", s.Config.Timeout)
	} else {
		kv("Timeout", "none")
	}
	if s.Config.Notify {
		kv("Notify", "desktop notification when finished")
	} else {
		kv("Notify", "off")
	}

	keys := sortedInputKeys(s.Config.Input)
	out = append(out, "", paintB("Environment", pal.accent)+tDim(fmt.Sprintf("  %d variable(s)", len(keys))))
	if len(keys) == 0 {
		out = append(out, tDim("None. Press Enter to add variables, a schedule or notifications."))
	}
	kw := 0
	for _, k := range keys {
		kw = max(kw, min(visibleWidth(k), 24))
	}
	for _, k := range keys {
		v := fmt.Sprintf("%v", s.Config.Input[k])
		if isSecretKey(k) && !m.ui.revealEnv {
			v = "••••••••"
		}
		out = append(out, tMuted(fit(truncate(k, 24), kw))+tDim(" = ")+tText(truncate(oneLine(v), max(4, cw-kw-3))))
	}
	for _, k := range keys {
		if isSecretKey(k) {
			hint := "Secret-looking values are hidden — press V to reveal"
			if m.ui.revealEnv {
				hint = "Secrets are visible — press V to hide"
			}
			out = append(out, "", tDim(hint))
			break
		}
	}
	return out
}

// ─── groups ─────────────────────────────────────────────────────────────────

// groupSummary counts member states of a group.
type groupSummary struct {
	total, running, ok, failed, stopped, idle, missing int
}

func (m *model) summarizeGroup(g GroupConfig) groupSummary {
	sum := groupSummary{total: len(g.Scripts)}
	for _, idx := range m.groupMemberIndices(g) {
		if idx < 0 {
			sum.missing++
			continue
		}
		switch m.scripts[idx].State {
		case "Running":
			sum.running++
		case "Success":
			sum.ok++
		case "Failed":
			sum.failed++
		case "Stopped":
			sum.stopped++
		default:
			sum.idle++
		}
	}
	return sum
}

func (m *model) renderGroupListPanel(w, h int) []string {
	focused := m.groupActivePanel == panelLeft
	cw := w - 4
	title := paintB("Groups", pal.text)
	if focused {
		title = paintB("Groups", pal.accent)
	}
	count := tDim(fmt.Sprintf("%d", len(m.groups)))

	if len(m.groups) == 0 {
		body := []string{"", centerText(tMuted("No groups yet"), cw),
			centerText(tDim("Press ")+paintB("A", pal.accent)+tDim(" to create one"), cw), "",
			centerText(tDim("A group runs several scripts together,"), cw),
			centerText(tDim("in parallel or as an ordered pipeline."), cw)}
		return renderPanel(w, h, panelSpec{title: title, right: count, focused: focused}, body)
	}

	capacity := max(1, (h-2)/2)
	m.groupListOffset = scrollOffset(m.groupListOffset, len(m.groups), capacity, m.groupCursor)
	end := min(len(m.groups), m.groupListOffset+capacity)

	var body []string
	for i := m.groupListOffset; i < end; i++ {
		g := m.groups[i]
		sel := i == m.groupCursor
		sum := m.summarizeGroup(g)

		bar := " "
		if sel {
			bar = paint("▌", pal.accent)
			if !focused {
				bar = paint("▌", pal.dim)
			}
		}
		glyph, gc := "○", pal.dim
		switch {
		case sum.running > 0:
			glyph, gc = spinnerFrame(), pal.accent
		case sum.failed > 0 || sum.stopped > 0:
			glyph, gc = "✕", pal.fail
		case sum.ok > 0 && sum.ok == sum.total:
			glyph, gc = "✓", pal.success
		}
		mode := "parallel"
		if g.Pipeline {
			mode = "pipeline"
		}
		fg := pal.muted
		if sel {
			fg = pal.text
		}
		left := bar + " " + paint(glyph, gc) + " "
		nameW := cw - visibleWidth(left) - len(mode) - 1
		name := paintB(truncate(oneLine(g.Name), max(3, nameW)), fg)
		r1 := spread(left+name, tDim(mode), cw)

		var detail string
		switch {
		case g.Description != "":
			detail = tDim(truncate(oneLine(g.Description), cw-4))
		default:
			detail = tDim(fmt.Sprintf("%d script(s)", sum.total))
		}
		r2 := "    " + detail
		if sum.total > 0 && g.Description != "" {
			r2 = "    " + tDim(truncate(oneLine(g.Description), cw-4-12)) + " "
			r2 = spread(r2, tDim(fmt.Sprintf("%d scripts", sum.total)), cw)
		}
		if sel {
			r1, r2 = bgWrap(r1, cw, pal.surface), bgWrap(r2, cw, pal.surface)
		}
		body = append(body, r1, r2)
	}
	right := count
	if m.groupListOffset > 0 || end < len(m.groups) {
		right = tDim(fmt.Sprintf("%d/%d ", m.groupCursor+1, len(m.groups))) + tMuted(scrollArrows(m.groupListOffset > 0, end < len(m.groups)))
	}
	return renderPanel(w, h, panelSpec{title: title, right: right, focused: focused}, body)
}

func (m *model) renderGroupDetailPanel(w, h int) []string {
	focused := m.groupActivePanel == panelRight
	cw := w - 4
	if m.groupCursor < 0 || m.groupCursor >= len(m.groups) {
		return renderPanel(w, h, panelSpec{title: paintB("Details", pal.text), focused: focused},
			[]string{"", centerText(tDim("Select or create a group to see its steps"), cw)})
	}
	g := m.groups[m.groupCursor]
	sum := m.summarizeGroup(g)
	members := m.groupMemberIndices(g)

	mode := chip("PARALLEL", pal.muted, pal.surface)
	if g.Pipeline {
		mode = chip("PIPELINE", pal.onAccent, pal.accent)
	}
	title := paintB(truncate(oneLine(g.Name), max(6, cw-24)), pal.text)
	if focused {
		title = paintB(truncate(oneLine(g.Name), max(6, cw-24)), pal.accent)
	}

	var body []string
	if g.Description != "" {
		body = append(body, tMuted(truncate(oneLine(g.Description), cw)))
	}
	var parts []string
	if sum.running > 0 {
		parts = append(parts, paintB(fmt.Sprintf("● %d running", sum.running), pal.accent))
	}
	if sum.ok > 0 {
		parts = append(parts, paint(fmt.Sprintf("✓ %d ok", sum.ok), pal.success))
	}
	if sum.failed > 0 {
		parts = append(parts, paintB(fmt.Sprintf("✕ %d failed", sum.failed), pal.fail))
	}
	if sum.stopped > 0 {
		parts = append(parts, paint(fmt.Sprintf("⊘ %d stopped", sum.stopped), pal.stopped))
	}
	if sum.idle > 0 {
		parts = append(parts, tDim(fmt.Sprintf("○ %d idle", sum.idle)))
	}
	if sum.missing > 0 {
		parts = append(parts, paint(fmt.Sprintf("? %d missing", sum.missing), pal.fail))
	}
	if len(parts) > 0 {
		body = append(body, strings.Join(parts, tDim("  ·  ")))
	}

	// Pipeline halt detection: the first failed/stopped step blocks the rest.
	haltAt := -1
	if g.Pipeline {
		for i, idx := range members {
			if idx >= 0 && (m.scripts[idx].State == "Failed" || m.scripts[idx].State == "Stopped") {
				haltAt = i
				break
			}
		}
	}
	if haltAt >= 0 {
		body = append(body, paintB("⛔ Pipeline halted", pal.fail)+tDim(fmt.Sprintf(" at step %d — later steps are blocked", haltAt+1)))
	}
	if len(body) > 0 {
		body = append(body, tDim(strings.Repeat("─", cw)))
	}

	if len(g.Scripts) == 0 {
		body = append(body, "", centerText(tMuted("This group has no scripts"), cw),
			centerText(tDim("Press ")+paintB("Enter", pal.accent)+tDim(" on the group list to choose members"), cw))
		return renderPanel(w, h, panelSpec{title: title, right: mode, focused: focused}, body)
	}

	const stepRows = 3
	capacity := max(1, (h-2-len(body))/stepRows)
	cur := clampInt(m.groupScriptCursor, 0, len(g.Scripts)-1)
	start, end := window(len(g.Scripts), capacity, cur)
	for i := start; i < end; i++ {
		body = append(body, m.groupStepRows(g, i, members[i], haltAt, cw, focused && i == cur)...)
	}
	right := mode
	if len(g.Scripts) > capacity {
		right = tDim(fmt.Sprintf("%d/%d ", cur+1, len(g.Scripts))) + mode
	}
	return renderPanel(w, h, panelSpec{title: title, right: right, focused: focused}, body)
}

// groupStepRows draws one step of a group as three rows.
func (m *model) groupStepRows(g GroupConfig, i, idx, haltAt, cw int, selected bool) []string {
	alias := g.Scripts[i]
	last := i == len(g.Scripts)-1
	blocked := g.Pipeline && haltAt >= 0 && i > haltAt

	state, color := "", pal.dim
	var s *ScriptState
	if idx >= 0 {
		s = &m.scripts[idx]
		state = s.State
		if !blocked {
			_, color, _ = stateVisual(state, m.isQueued(idx))
		}
	}

	t1, t2, t3 := "├", "│", "│"
	switch {
	case len(g.Scripts) == 1:
		t1, t2, t3 = "─", " ", " "
	case i == 0:
		t1 = "╭"
	case last:
		t1, t2, t3 = "╰", " ", " "
	}
	rail := func(c string) string { return paint(c, color) }

	glyph := "?"
	label := "MISSING"
	switch {
	case s == nil:
		color = pal.fail
	case blocked:
		glyph, label = "◌", "BLOCKED"
	default:
		glyph, _, label = stateVisual(state, m.isQueued(idx))
	}
	nameFg := pal.text
	if blocked {
		nameFg = pal.dim
	}
	num := ""
	if g.Pipeline {
		num = tDim(fmt.Sprintf("%d. ", i+1))
	}
	badge := paintB(glyph+" "+label, color)
	if s != nil && !blocked {
		if d, ok := scriptElapsed(s); ok {
			badge += tDim("  " + formatDuration(d))
		}
	}
	left := rail(t1) + " " + num + paintB(truncate(oneLine(alias), max(4, cw-30)), nameFg)
	if s == nil {
		left = rail(t1) + " " + num + paintB(truncate(oneLine(alias), max(4, cw-30)), pal.fail)
	}
	r1 := spread(left, badge, cw)

	var r2, r3 string
	switch {
	case s == nil:
		r2 = rail(t2) + "  " + tDim("script no longer exists — remove it from the group")
		r3 = rail(t3)
	case blocked:
		r2 = rail(t2) + "  " + progressBar(max(4, cw-9), 0, pal.dim) + tDim("    —")
		r3 = rail(t3) + "  " + tDim("waiting for earlier steps")
	default:
		r2 = rail(t2) + "  " + progressRow(s, cw-3)
		ll := lastLogLine(s.Logs)
		if ll == "" {
			ll = "no output"
			r3 = rail(t3) + "  " + tDim("› "+ll)
		} else {
			r3 = rail(t3) + "  " + tDim("› ") + tMuted(truncate(oneLine(ll), max(4, cw-6)))
		}
	}
	rows := []string{r1, r2, r3}
	for k := range rows {
		if selected {
			rows[k] = bgWrap(rows[k], cw, pal.surface)
		} else {
			rows[k] = fit(rows[k], cw)
		}
	}
	return rows
}
