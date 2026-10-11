package main

// ui_dialogs.go — modal dialogs drawn over the dashboards: forms, confirmations,
// help, settings and cleanup.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// activeDialog returns the dialog (if any) that should be drawn on top of the
// dashboard, together with its screen position.
func (m *model) activeDialog(w, h int) (panel []string, top, left int, ok bool) {
	var spec modalSpec
	var body, footer []string
	switch {
	case m.cleanupMode:
		spec, body, footer = m.cleanupDialog(w, h)
	case m.settingsMode:
		spec, body, footer = m.settingsDialog(w, h)
	default:
		switch m.activeView {
		case "form":
			spec, body, footer = m.scriptFormDialog(w, h)
		case "env_form":
			spec, body, footer = m.envFormDialog(w, h)
		case "delete_confirm":
			if m.selected() == nil {
				return nil, 0, 0, false
			}
			spec, body, footer = m.deleteScriptDialog(w, h)
		case "group_form":
			spec, body, footer = m.groupFormDialog(w, h)
		case "group_members":
			spec, body, footer = m.groupMembersDialog(w, h)
		case "group_delete_confirm":
			if m.groupCursor < 0 || m.groupCursor >= len(m.groups) {
				return nil, 0, 0, false
			}
			spec, body, footer = m.deleteGroupDialog(w, h)
		case "help":
			spec, body, footer = m.helpDialog(w, h)
		case "quit_confirm":
			spec, body, footer = m.quitDialog(w, h)
		default:
			return nil, 0, 0, false
		}
	}
	panel, top, left = modalFrame(w, h, spec, body, footer)
	return panel, top, left, true
}

// ─── input helpers ──────────────────────────────────────────────────────────

// styleInput applies the active palette to a text input.
func styleInput(in *textinput.Model) {
	in.Prompt = ""
	in.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(pal.text))
	in.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(pal.dim))
	in.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(pal.accent))
}

func inputBg(focused bool) string {
	if focused {
		return mixHex(pal.surface, "#ffffff", 0.10)
	}
	return pal.surface
}

// inputBox draws an input as a shaded box of exactly w cells.
func inputBox(in *textinput.Model, focused bool, w int) string {
	styleInput(in)
	in.Width = max(3, w-3)
	return bgWrap(" "+in.View()+" ", w, inputBg(focused))
}

const formLabelW = 14

// fieldRow draws "▸ Label   [ input ]".
func fieldRow(label string, required bool, in *textinput.Model, focused bool, cw int) string {
	if required {
		label += " *"
	}
	mark, lab := "  ", tMuted(fit(label, formLabelW))
	if focused {
		mark, lab = paintB("▸ ", pal.accent), paintB(fit(label, formLabelW), pal.text)
	}
	return mark + lab + inputBox(in, focused, cw-2-formLabelW)
}

// toggleRow draws a yes/no choice as two options.
func toggleRow(label string, on bool, onText, offText string, focused bool, cw int) string {
	mark, lab := "  ", tMuted(fit(label, formLabelW))
	if focused {
		mark, lab = paintB("▸ ", pal.accent), paintB(fit(label, formLabelW), pal.text)
	}
	opt := func(sel bool, text string) string {
		if sel {
			return paintB("◉ "+text, pal.text)
		}
		return tDim("○ " + text)
	}
	row := mark + lab + opt(on, onText) + "   " + opt(!on, offText)
	if focused {
		row += tDim("   Space flips")
	}
	return fit(row, cw)
}

func buttonRow(focus int, labels []string, danger []bool, base int) string {
	var parts []string
	for i, l := range labels {
		d := danger != nil && danger[i]
		parts = append(parts, button(l, focus == base+i, d))
	}
	return "  " + strings.Join(parts, "  ")
}

func formHints(extra ...hint) []hint {
	hs := []hint{{"Tab", "next", 0}, {"Shift+Tab", "back", 3}}
	hs = append(hs, extra...)
	return append(hs, hint{"Ctrl+S", "save", 0}, hint{"Esc", "cancel", 0})
}

func (m *model) errorLine(cw int) string {
	if e := m.formError(); e != "" {
		return paintB("✕ ", pal.fail) + paint(truncate(oneLine(e), cw-3), pal.fail)
	}
	return ""
}

func dialogWidth(w, want int) int { return clampInt(want, 36, w-2) }

// ─── script form ────────────────────────────────────────────────────────────

var scriptFields = []struct {
	label    string
	hint     string
	required bool
}{
	{"Name", "Unique alias for this script, e.g. nightly_backup", true},
	{"Description", "Shown under the name in the list", false},
	{"Command", "Shell command to run — chain steps with &&", true},
	{"Output folder", "Run records are saved here. Must be unique for every script", true},
	{"Schedule", "Cron expression with 5 fields, e.g. */15 * * * * — empty = manual only", false},
	{"SSH host", "user@host runs the command remotely over SSH — empty = this machine", false},
	{"Timeout", "Stop the script after this long: 30s, 10m, 2h — empty = no limit", false},
}

func (m *model) scriptFormDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 80)
	cw := pw - 4
	title := "Add script"
	if m.editingAlias != "" {
		title = "Edit script"
	}
	spec := modalSpec{title: title, subtitle: m.editingAlias, wantW: pw}

	var body []string
	for i := range m.formInputs {
		f := scriptFields[i]
		body = append(body, fieldRow(f.label, f.required, &m.formInputs[i], m.focusedInput == i, cw))
	}
	body = append(body, "")
	if e := m.errorLine(cw); e != "" {
		body = append(body, e)
	} else if m.focusedInput < len(scriptFields) {
		body = append(body, tDim(truncate(scriptFields[m.focusedInput].hint, cw)))
	} else {
		body = append(body, tDim("* required"))
	}
	footer := []string{
		buttonRow(m.focusedInput, []string{"Save", "Cancel"}, nil, 7),
		renderHints(cw, formHints()),
	}
	return spec, body, footer
}

// ─── environment / schedule form ────────────────────────────────────────────

func (m *model) envFormDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 78)
	cw := pw - 4
	name := ""
	if s := m.selected(); s != nil {
		name = s.Config.NameAlias
	}
	spec := modalSpec{title: "Schedule & environment", subtitle: name, wantW: pw}
	if len(m.envInputs) < 22 {
		return spec, []string{tDim("loading…")}, nil
	}

	type row struct {
		text  string
		focus bool
	}
	var rows []row
	rows = append(rows, row{fieldRow("Schedule", false, &m.envInputs[0], m.focusedEnv == 0, cw), m.focusedEnv == 0})
	rows = append(rows, row{tDim("  " + fit("", formLabelW) + "cron, 5 fields — e.g. 0 2 * * *  (empty = manual only)"), false})
	rows = append(rows, row{"", false})
	rows = append(rows, row{paintB("  Environment variables", pal.accent) + tDim("   passed to the script, up to 10"), false})

	keyW := 24
	valW := cw - 2 - keyW - 3
	for pair := 0; pair < 10; pair++ {
		ki, vi := 1+pair*2, 2+pair*2
		kf, vf := m.focusedEnv == ki, m.focusedEnv == vi
		kb := inputBox(&m.envInputs[ki], kf, keyW)
		var vb string
		if !vf && isSecretKey(m.envInputs[ki].Value()) && m.envInputs[vi].Value() != "" {
			vb = bgWrap(" "+tText(strings.Repeat("•", min(12, len(m.envInputs[vi].Value()))))+" ", valW, inputBg(false))
		} else {
			vb = inputBox(&m.envInputs[vi], vf, valW)
		}
		mark := "  "
		if kf || vf {
			mark = paintB("▸ ", pal.accent)
		}
		rows = append(rows, row{mark + kb + tDim(" = ") + vb, kf || vf})
	}
	rows = append(rows, row{"", false})
	rows = append(rows, row{toggleRow("Notify", isYes(strings.ToLower(strings.TrimSpace(m.envInputs[21].Value()))),
		"Desktop notification when finished", "Off", m.focusedEnv == 21, cw), m.focusedEnv == 21})

	focusRow := 0
	for i, r := range rows {
		if r.focus {
			focusRow = i
		}
	}
	if m.focusedEnv >= 22 {
		focusRow = len(rows) - 1
	}

	footer := []string{
		buttonRow(m.focusedEnv, []string{"Save", "Cancel"}, nil, 22),
		renderHints(cw, formHints()),
	}
	extra := 1 // error line
	visible := max(3, h-2-len(footer)-1-extra)
	start, end := window(len(rows), visible, focusRow)
	var body []string
	for _, r := range rows[start:end] {
		body = append(body, r.text)
	}
	if e := m.errorLine(cw); e != "" {
		body = append(body, e)
	} else if len(rows) > visible {
		body = append(body, tDim(fmt.Sprintf("↑↓ more fields (%d/%d)", end, len(rows))))
	} else {
		body = append(body, "")
	}
	return spec, body, footer
}

// ─── confirmations ──────────────────────────────────────────────────────────

func (m *model) deleteScriptDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 58)
	cw := pw - 4
	s := m.selected()
	spec := modalSpec{title: "Delete script?", wantW: pw, danger: true}
	body := []string{
		paintB(truncate(oneLine(s.Config.NameAlias), cw), pal.text),
		tDim(truncate("$ "+oneLine(s.Config.Command), cw)),
		"",
		tMuted("Removes the script from the config, its groups and the crontab."),
		tDim("Run records already saved in the output folder are kept."),
	}
	if s.State == "Running" {
		body = append(body, "", paintB("⚠ It is running now and will be stopped.", pal.stopped))
	}
	footer := []string{
		buttonRow(m.confirmDeleteFocused, []string{"Cancel", "Delete"}, []bool{false, true}, 0),
		renderHints(cw, []hint{{"←→", "choose", 1}, {"Enter", "confirm", 0}, {"Y", "delete", 2}, {"Esc", "cancel", 0}}),
	}
	return spec, body, footer
}

func (m *model) deleteGroupDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 58)
	cw := pw - 4
	g := m.groups[m.groupCursor]
	spec := modalSpec{title: "Delete group?", wantW: pw, danger: true}
	body := []string{
		paintB(truncate(oneLine(g.Name), cw), pal.text),
		tDim(fmt.Sprintf("%d script(s) in this group", len(g.Scripts))),
		"",
		tMuted("Removes the group from the config."),
		tDim("The scripts themselves are not deleted."),
	}
	footer := []string{
		buttonRow(m.confirmDeleteFocused, []string{"Cancel", "Delete"}, []bool{false, true}, 0),
		renderHints(cw, []hint{{"←→", "choose", 1}, {"Enter", "confirm", 0}, {"Y", "delete", 2}, {"Esc", "cancel", 0}}),
	}
	return spec, body, footer
}

func (m *model) quitDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 60)
	cw := pw - 4
	spec := modalSpec{title: "Quit sctl?", wantW: pw, danger: true}
	n := m.runningCount()
	body := []string{paintB(fmt.Sprintf("%d script(s) are still running:", n), pal.stopped)}
	shown := 0
	for i := range m.scripts {
		s := &m.scripts[i]
		if s.Cmd == nil && s.State != "Running" {
			continue
		}
		if shown == 5 {
			body = append(body, tDim(fmt.Sprintf("  … and %d more", n-5)))
			break
		}
		body = append(body, "  "+paint(spinnerFrame(), pal.accent)+" "+tText(truncate(oneLine(s.Config.NameAlias), cw-6)))
		shown++
	}
	body = append(body, "", tMuted("Quitting stops them and discards unfinished work."))
	footer := []string{
		buttonRow(m.confirmDeleteFocused, []string{"Stay", "Quit & stop"}, []bool{false, true}, 0),
		renderHints(cw, []hint{{"←→", "choose", 1}, {"Enter", "confirm", 0}, {"Y", "quit", 2}, {"Esc", "stay", 0}}),
	}
	return spec, body, footer
}

// ─── groups ─────────────────────────────────────────────────────────────────

func (m *model) groupFormDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 72)
	cw := pw - 4
	title := "New group"
	if m.editingGroupName != "" {
		title = "Edit group"
	}
	spec := modalSpec{title: title, subtitle: m.editingGroupName, wantW: pw}
	if len(m.groupFormInputs) < 3 {
		return spec, []string{tDim("loading…")}, nil
	}
	pipeline := isYes(strings.ToLower(strings.TrimSpace(m.groupFormInputs[2].Value())))
	body := []string{
		fieldRow("Name", true, &m.groupFormInputs[0], m.groupFocusedInput == 0, cw),
		fieldRow("Description", false, &m.groupFormInputs[1], m.groupFocusedInput == 1, cw),
		toggleRow("Run as", pipeline, "Pipeline", "Parallel", m.groupFocusedInput == 2, cw),
		"",
	}
	if e := m.errorLine(cw); e != "" {
		body = append(body, e)
	} else if pipeline {
		body = append(body, tDim("Pipeline: steps run one after another and stop at the first failure"))
	} else {
		body = append(body, tDim("Parallel: every script starts at the same time"))
	}
	footer := []string{
		buttonRow(m.groupFocusedInput, []string{"Save", "Cancel"}, nil, 3),
		renderHints(cw, formHints()),
	}
	return spec, body, footer
}

func (m *model) groupMembersDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 72)
	cw := pw - 4
	name := ""
	order := map[string]int{}
	pipeline := false
	if m.groupCursor >= 0 && m.groupCursor < len(m.groups) {
		g := m.groups[m.groupCursor]
		name, pipeline = g.Name, g.Pipeline
		for i, a := range g.Scripts {
			order[a] = i + 1
		}
	}
	spec := modalSpec{title: "Group members", subtitle: name, wantW: pw}
	footer := []string{renderHints(cw, []hint{{"Space", "toggle", 0}, {"↑↓", "move", 1}, {"Enter", "save", 0}, {"Esc", "cancel", 0}})}
	if len(m.scripts) == 0 {
		return spec, []string{tMuted("There are no scripts to add yet.")}, footer
	}

	visible := max(3, h-2-len(footer)-1-2)
	cur := clampInt(m.groupMemberCursor, 0, len(m.scripts)-1)
	start, end := window(len(m.scripts), visible, cur)
	var body []string
	for i := start; i < end; i++ {
		s := m.scripts[i]
		checked := i < len(m.groupMemberChecked) && m.groupMemberChecked[i]
		mark := "  "
		if i == cur {
			mark = paintB("▸ ", pal.accent)
		}
		box := tDim("○")
		if checked {
			box = paintB("◉", pal.accent)
		}
		pos := tDim("   ")
		if checked {
			if n, ok := order[s.Config.NameAlias]; ok && pipeline {
				pos = tDim(fit(fmt.Sprintf("%d.", n), 3))
			} else if !ok {
				pos = paint(" + ", pal.success)
			}
		}
		fg := pal.muted
		if i == cur || checked {
			fg = pal.text
		}
		desc := ""
		if s.Config.Description != "" {
			desc = "  " + tDim(truncate(oneLine(s.Config.Description), max(0, cw-visibleWidth(s.Config.NameAlias)-12)))
		}
		row := mark + box + " " + pos + " " + paint(truncate(oneLine(s.Config.NameAlias), max(4, cw-14)), fg) + desc
		if i == cur {
			row = bgWrap(row, cw, pal.surface)
		}
		body = append(body, row)
	}
	body = append(body, "")
	count := 0
	for _, c := range m.groupMemberChecked {
		if c {
			count++
		}
	}
	info := fmt.Sprintf("%d selected", count)
	if pipeline {
		info += " · existing steps keep their order, new ones are added at the end"
	}
	body = append(body, tDim(truncate(info, cw)))
	return spec, body, footer
}

// ─── cleanup & settings ─────────────────────────────────────────────────────

func (m *model) cleanupDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 64)
	cw := pw - 4
	spec := modalSpec{title: "Clean up run records", wantW: pw, danger: m.ui.cleanupArmed}
	scopeThis := "This script"
	if m.cleanupAlias != "" {
		scopeThis = "This script (" + truncate(m.cleanupAlias, 20) + ")"
	}
	opt := func(sel bool, text string) string {
		if sel {
			return paintB("◉ "+text, pal.text)
		}
		return tDim("○ " + text)
	}
	m.cleanupInput.Width = 8
	m.cleanupInput.CharLimit = 5
	body := []string{
		tMuted("Deletes saved run records (task_<id>.yaml files)."),
		"",
		tMuted(fit("Scope", formLabelW)) + opt(!m.cleanupModeAll, scopeThis) + "   " + opt(m.cleanupModeAll, "All scripts"),
		tMuted(fit("Older than", formLabelW)) + inputBox(&m.cleanupInput, true, 9) + tMuted("  days"),
		tDim(fit("", formLabelW) + "leave empty to delete every record"),
		"",
	}
	switch {
	case m.ui.cleanupArmed:
		what := "ALL run records"
		if !m.cleanupModeAll {
			what = "ALL run records of " + m.cleanupAlias
		}
		body = append(body, paintB("⚠ Press Enter again to permanently delete "+what, pal.fail))
	case m.formError() != "":
		body = append(body, m.errorLine(cw))
	default:
		body = append(body, tDim("This cannot be undone."))
	}
	footer := []string{renderHints(cw, []hint{{"Enter", "delete", 0}, {"Tab", "scope", 1}, {"Esc", "cancel", 0}})}
	return spec, body, footer
}

func (m *model) settingsDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 62)
	cw := pw - 4
	spec := modalSpec{title: "Settings", subtitle: "progress bar", wantW: pw}
	if len(m.settingsInputs) < 2 {
		return spec, []string{tDim("loading…")}, nil
	}
	fill := strings.TrimSpace(m.settingsInputs[0].Value())
	track := strings.TrimSpace(m.settingsInputs[1].Value())
	body := []string{
		tMuted("Characters used to draw progress bars."),
		"",
		fieldRow("Filled", false, &m.settingsInputs[0], m.settingsInputs[0].Focused(), min(cw, 30)),
		fieldRow("Empty", false, &m.settingsInputs[1], m.settingsInputs[1].Focused(), min(cw, 30)),
		"",
	}
	barW := max(10, cw-formLabelW-8)
	if fill != "" && track != "" {
		body = append(body,
			tMuted(fit("Preview", formLabelW))+progressBarWith(barW, 35, pal.accent, fill, track)+tDim("  35%"),
			tMuted(fit("", formLabelW))+progressBarWith(barW, 80, pal.success, fill, track)+tDim("  80%"))
	} else {
		body = append(body, tDim("Both characters are required"), "")
	}
	body = append(body, "")
	if e := m.errorLine(cw); e != "" {
		body = append(body, e)
	} else {
		body = append(body, tDim("▮ and ▯ together give the default smooth bar"))
	}
	footer := []string{renderHints(cw, []hint{{"Tab", "next", 0}, {"Enter", "save", 0}, {"Ctrl+R", "defaults", 1}, {"Esc", "cancel", 0}})}
	return spec, body, footer
}

// ─── help ───────────────────────────────────────────────────────────────────

func (m *model) helpDialog(w, h int) (modalSpec, []string, []string) {
	pw := dialogWidth(w, 76)
	cw := pw - 4
	spec := modalSpec{title: "Keyboard shortcuts", wantW: pw}

	var lines []string
	for i, sec := range helpContent {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, paintB(sec.title, pal.accent))
		for _, it := range sec.items {
			lines = append(lines, "  "+paintB(fit(truncate(it.keys, helpKeyCol), helpKeyCol), pal.text)+" "+tMuted(truncate(it.desc, max(4, cw-helpKeyCol-3))))
		}
	}
	rows := helpBodyRows(h)
	off := clampInt(m.ui.helpScroll, 0, max(0, len(lines)-rows))
	body := lines[off:min(len(lines), off+rows)]

	hs := []hint{{"Esc", "close", 0}}
	if len(lines) > rows {
		hs = append([]hint{{"↑↓", "scroll", 0}, {"PgUp/PgDn", "page", 1}}, hs...)
	}
	footer := []string{renderHints(cw, hs)}
	if len(lines) > rows {
		spec.subtitle = fmt.Sprintf("%d%%", scrollPct(off, len(lines)-rows))
	}
	return spec, body, footer
}
