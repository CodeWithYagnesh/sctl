package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func drawProgressBar(width int, percent float64, running bool) string {
	if width <= 0 {
		return ""
	}
	filledLen := int(float64(width) * percent)
	if filledLen > width {
		filledLen = width
	}
	emptyLen := width - filledLen

	var filledStyle lipgloss.Style
	if running {
		filledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent))
	} else {
		filledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Success))
	}
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Idle))

	filled := strings.Repeat("▮", filledLen)
	empty := strings.Repeat("▯", emptyLen)
	return filledStyle.Render(filled) + emptyStyle.Render(empty)
}

func (m *model) getHeaderHeight() int {
	return 3
}

func (m *model) renderLeftPanel(width, height int) string {
	var s strings.Builder
	borderStyle := unfocusedStyle
	if m.activePanel == panelLeft {
		borderStyle = focusedStyle
	}

	indices := m.filteredIndices()
	cursorPos := 0
	for pos, idx := range indices {
		if idx == m.cursor {
			cursorPos = pos
			break
		}
	}

	availW := width - 8
	if availW < 10 {
		availW = 10
	}

	titleTxt := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0")).Render("SCRIPTS")
	countStr := fmt.Sprintf("%d", len(m.scripts))
	if len(indices) > 0 {
		if m.filterQuery != "" {
			countStr = fmt.Sprintf("%d/%d", len(indices), len(m.scripts))
		} else if len(indices) > 1 {
			countStr = fmt.Sprintf("%d/%d", cursorPos+1, len(indices))
		}
	}
	countChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#64748b")).
		Padding(0, 1).
		Render(countStr)

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("Space·select  R·run")
	if m.filterQuery != "" {
		hint = lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Render("/") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(m.filterQuery) +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("  Esc·clear")
	}

	titleLeft := titleTxt + " " + countChip
	sp := availW - lipgloss.Width(titleLeft) - lipgloss.Width(hint)
	if sp < 1 {
		sp = 1
	}
	s.WriteString(titleLeft + strings.Repeat(" ", sp) + hint + "\n")

	headerLines := 2
	if m.activeView == "filter" {
		barStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#6366f1")).
			Width(availW - 2)
		prompt := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("/") + " "
		filterLines := strings.Split(barStyle.Render(prompt+m.filterInput.View()), "\n")
		for _, l := range filterLines {
			if l != "" {
				s.WriteString(l + "\n")
				headerLines++
			}
		}
	}

	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))
	s.WriteString(divider + "\n")

	if len(m.scripts) == 0 {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render("  No scripts configured. Press A to add one.") + "\n")
		return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
	} else if len(indices) == 0 && m.filterQuery != "" {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render(fmt.Sprintf("  No match for %q", m.filterQuery)) + "\n")
		return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
	}

	// Calculate max cards that can fit vertically
	availH := height - 2 // outer border consumes 2 lines
	cardsAreaHeight := availH - headerLines
	if cardsAreaHeight < 5 {
		cardsAreaHeight = 5
	}

	// Each card takes 5 lines + 1 line gap (6 lines total)
	cardCost := 5
	gapCost := 1
	cardTotalHeight := cardCost + gapCost

	// Reserve space for scroll hints if needed (2 lines total: 1 top, 1 bottom)
	usableHeight := cardsAreaHeight - 2
	if usableHeight < cardCost {
		usableHeight = cardCost
	}
	maxCards := usableHeight / cardTotalHeight
	if maxCards < 1 {
		maxCards = 1
	}

	// Adjust m.listOffset to ensure cursorPos is visible
	if cursorPos < m.listOffset {
		m.listOffset = cursorPos
	} else if cursorPos >= m.listOffset+maxCards {
		m.listOffset = cursorPos - maxCards + 1
	}
	if m.listOffset > len(indices)-maxCards {
		m.listOffset = len(indices) - maxCards
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}

	endIdx := m.listOffset + maxCards
	if endIdx > len(indices) {
		endIdx = len(indices)
	}

	// Top overflow indicator
	if m.listOffset > 0 {
		moreAbove := m.listOffset
		topHint := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Italic(true).
			Render(fmt.Sprintf("  ▲ %d script(s) above...", moreAbove))
		s.WriteString(topHint + "\n")
	}

	// Render visible script cards
	for pos := m.listOffset; pos < endIdx; pos++ {
		i := indices[pos]
		script := m.scripts[i]
		isSelected := i == m.cursor

		cursorGlyph := "  "
		if isSelected {
			cursorGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("▶ ")
		}
		selGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("○")
		if script.Checked {
			selGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("●")
		}
		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
		if isSelected {
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
		}
		name := nameStyle.Render(script.Config.NameAlias)

		var badge string
		switch script.State {
		case "Running":
			badge = badgeRunning.Render(" ● RUNNING ")
		case "Success":
			badge = badgeSuccess.Render(" ✓ SUCCESS ")
		case "Failed":
			badge = badgeFailed.Render(" ✕ FAILED  ")
		case "Stopped":
			badge = badgeStopped.Render(" ⊘ STOPPED ")
		default:
			badge = badgeIdle.Render("   IDLE    ")
		}

		row1left := cursorGlyph + selGlyph + " " + name
		r1sp := availW - lipgloss.Width(row1left) - lipgloss.Width(badge)
		if r1sp < 1 {
			r1sp = 1
		}
		row1 := row1left + strings.Repeat(" ", r1sp) + badge

		barW := availW - 12
		if barW < 4 {
			barW = 4
		}
		pBar := drawProgressBar(barW, float64(script.Progress)/100.0, script.State == "Running")
		pctLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf(" %3d%%", script.Progress))
		if script.State == "Idle" {
			pctLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("  --")
		}
		row2 := "   " + pBar + pctLabel

		descW := availW - 4
		desc := script.Config.Description
		var sshBadge string
		if script.Config.Host != "" {
			sshBadge = lipgloss.NewStyle().
				Background(lipgloss.Color("#0f4c81")).
				Foreground(lipgloss.Color("#7dd3fc")).
				Bold(true).Padding(0, 1).
				Render("SSH")
			descW -= lipgloss.Width(sshBadge) + 2
		}
		if descW > 3 && len(desc) > descW {
			desc = desc[:descW-3] + "..."
		}
		descRender := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Width(descW).Render(desc)
		row3 := "   " + descRender
		if sshBadge != "" {
			row3 += "  " + sshBadge
		}

		cardContent := row1 + "\n" + row2 + "\n" + row3

		cardBorderColor := lipgloss.Color(activeTheme.Idle)
		if isSelected {
			cardBorderColor = lipgloss.Color(activeTheme.Accent)
		}
		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cardBorderColor).
			Padding(0, 1).
			Width(availW)

		s.WriteString(cardStyle.Render(cardContent) + "\n")
	}

	// Bottom overflow indicator
	if endIdx < len(indices) {
		moreBelow := len(indices) - endIdx
		botHint := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Italic(true).
			Render(fmt.Sprintf("  ▼ %d script(s) below...", moreBelow))
		s.WriteString(botHint + "\n")
	}

	return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
}

func (m *model) renderRightPanel(width, height int) string {
	borderStyle := unfocusedStyle
	if m.activePanel == panelRight {
		borderStyle = focusedStyle
	}

	if len(m.scripts) == 0 {
		return borderStyle.Width(width - 4).Height(height - 2).Render(
			lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("No scripts configured."),
		)
	}

	script := m.scripts[m.cursor]

	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("  /  ")
	titleLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0")).Render("OUTPUT LOG")
	scriptLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(activeTheme.Accent)).Render(script.Config.NameAlias)
	taskLabel := ""
	if script.TaskID > 0 {
		taskLabel = sep + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf("Task #%d", script.TaskID))
	}

	scrollPercent := m.viewport.ScrollPercent()
	scrollText := fmt.Sprintf("%d%%", int(scrollPercent*100))
	if scrollPercent <= 0 {
		scrollText = "Top"
	} else if scrollPercent >= 1.0 {
		scrollText = "End"
	}
	scrollChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#475569")).
		Padding(0, 1).
		Render(scrollText)

	statusLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748b")).
		Render(formatExecutionStatus(script.State, script.StartedAt, script.FinishedAt, script.Progress))
	breadcrumb := titleLabel + sep + scriptLabel + taskLabel
	availW := width - 8
	if availW < 10 {
		availW = 10
	}
	m.viewport.Width = availW

	sp := availW - lipgloss.Width(breadcrumb) - lipgloss.Width(statusLine) - lipgloss.Width(scrollChip)
	if sp < 1 {
		sp = 1
	}
	topLine := breadcrumb + strings.Repeat(" ", sp) + statusLine + " " + scrollChip
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))

	content := m.viewport.View()
	return borderStyle.Width(width - 4).Height(height - 2).Render(topLine + "\n" + divider + "\n" + content)
}

func (m *model) renderBottomBar(width int) string {
	statusRow := ""
	if m.statusMsg != "" && (m.statusMsgTime.IsZero() || time.Since(m.statusMsgTime) < 8*time.Second) {
		chip := lipgloss.NewStyle().
			Background(lipgloss.Color("#6366f1")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1).
			Render("INFO")
		msg := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(m.statusMsg)
		statusRow = chip + "  " + msg + "\n"
	}

	border := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", width))

	type binding struct{ key, desc string }
	bindings := []binding{
		{"R", "Run"}, {"S", "Stop"}, {"Space", "Select"},
		{"A", "Add"}, {"E", "Edit"}, {"Enter", "Env/Cron"}, {"D", "Delete"},
		{"/", "Filter"}, {"H", "History"}, {"P", "Parallel"}, {"O", "HTML"},
		{"T", "Theme"}, {"C-↑/↓", "Reorder"}, {"Tab", "Switch pane"}, {"Q", "Quit"},
	}
	kStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#a5b4fc")).
		Bold(true).
		Padding(0, 1)
	dStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(" │ ")

	var parts []string
	for _, b := range bindings {
		parts = append(parts, kStyle.Render(b.key)+" "+dStyle.Render(b.desc))
	}
	legend := strings.Join(parts, sepStyle)

	return statusRow + border + "\n" + legend
}

func (m *model) renderFramedBox(titleText string, titleColor string, borderColor string, innerContent []string, boxWidth int) string {
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(1, 2).
		Width(boxWidth)

	tStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(titleColor))

	var sb strings.Builder
	if titleText != "" {
		sb.WriteString(tStyle.Render(titleText) + "\n")
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", boxWidth)) + "\n")
	}
	for _, line := range innerContent {
		sb.WriteString(line + "\n")
	}

	boxStr := boxStyle.Render(sb.String())
	boxHeight := strings.Count(boxStr, "\n")
	padTop := (m.height - boxHeight) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (m.width - lipgloss.Width(boxStr)) / 2
	if padLeft < 0 {
		padLeft = 0
	}

	var out strings.Builder
	for i := 0; i < padTop; i++ {
		out.WriteString("\n")
	}
	for _, line := range strings.Split(boxStr, "\n") {
		out.WriteString(strings.Repeat(" ", padLeft) + line + "\n")
	}
	return out.String()
}

func (m *model) renderForm() string {
	var inner []string

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	focusedBorder := lipgloss.Color("#6366f1")
	blurBorder := lipgloss.Color("#1e293b")

	fields := []string{
		"Name alias  (unique identifier)",
		"Description",
		"Command  (shell command to run)",
		"Output folder path",
		"Cron schedule  (optional, e.g. */5 * * * *)",
		"SSH host  (optional, e.g. user@server — blank = run locally)",
	}

	for i, input := range m.formInputs {
		inner = append(inner, labelStyle.Render(fields[i]))
		borderColor := blurBorder
		if i == m.focusedInput {
			borderColor = focusedBorder
		}
		styled := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(borderColor).
			Width(52).
			Render(input.View())
		for _, l := range strings.Split(styled, "\n") {
			if l != "" {
				inner = append(inner, l)
			}
		}
		inner = append(inner, "")
	}

	saveBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Save")
	cancelBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Cancel")
	if m.focusedInput == formSubmitIdx {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.focusedInput == formCancelIdx {
		cancelBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Fail)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Cancel")
	}
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("─────────────────────────────────────────────────────"))
	inner = append(inner, "  "+saveBg+"    "+cancelBg)

	title := "Add Script"
	if m.editingAlias != "" {
		title = "Edit Script — " + m.editingAlias
	}
	return m.renderFramedBox(title, "#e2e8f0", activeTheme.Accent, inner, 62)
}

func (m *model) renderEnvForm() string {
	var inner []string

	focusedScript := m.scripts[m.cursor]
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	scriptChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#818cf8")).
		Bold(true).Padding(0, 1).
		Render(focusedScript.Config.NameAlias)
	inner = append(inner, "Editing:  "+scriptChip)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 52)))
	inner = append(inner, "")

	focusedBorder := lipgloss.Color("#6366f1")
	blurBorder := lipgloss.Color("#1e293b")

	totalInputs := len(m.envInputs)
	if totalInputs == 0 {
		return m.renderFramedBox("Edit Script Config", "#e2e8f0", activeTheme.Accent, inner, 62)
	}

	notifyIdx := totalInputs - 1
	submitIdx := totalInputs
	cancelIdx := totalInputs + 1

	// Cron schedule (index 0)
	inner = append(inner, labelStyle.Render("Cron schedule  (e.g. */5 * * * *)"))
	cronBorder := blurBorder
	if m.focusedEnv == 0 {
		cronBorder = focusedBorder
	}
	styledCron := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(cronBorder).Width(52).Render(m.envInputs[0].View())
	for _, l := range strings.Split(styledCron, "\n") {
		if l != "" {
			inner = append(inner, l)
		}
	}
	inner = append(inner, "")

	// Host field (index 1) - new addition
	inner = append(inner, labelStyle.Render("SSH Host  (optional, e.g. user@host — blank = local)"))
	hostBorder := blurBorder
	if m.focusedEnv == 1 {
		hostBorder = focusedBorder
	}
	styledHost := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(hostBorder).Width(52).Render(m.envInputs[1].View())
	for _, l := range strings.Split(styledHost, "\n") {
		if l != "" {
			inner = append(inner, l)
		}
	}
	inner = append(inner, "")

	// Environment variables - start from index 2, go up to notifyIdx-1
	pairCount := (notifyIdx - 2) / 2
	inner = append(inner, labelStyle.Render(fmt.Sprintf("Environment variables  (up to %d key=value pairs)", pairCount)))
	inner = append(inner, "")
	for i := 2; i < notifyIdx; i += 2 {
		pairNum := ((i - 2) / 2) + 1
		inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(fmt.Sprintf("Pair %d", pairNum)))

		kBorder, vBorder := blurBorder, blurBorder
		if m.focusedEnv == i {
			kBorder = focusedBorder
		}
		if m.focusedEnv == i+1 {
			vBorder = focusedBorder
		}
		styledKey := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(kBorder).Width(23).Render(m.envInputs[i].View())
		styledVal := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(vBorder).Width(23).Render(m.envInputs[i+1].View())

		kLines := strings.Split(styledKey, "\n")
		vLines := strings.Split(styledVal, "\n")
		for j := 0; j < len(kLines); j++ {
			if j < len(vLines) && kLines[j] != "" && vLines[j] != "" {
				inner = append(inner, kLines[j]+"  "+vLines[j])
			}
		}
	}
	inner = append(inner, "")

	// Desktop notification (notifyIdx)
	inner = append(inner, labelStyle.Render("Desktop notification on complete  (y / n)"))
	notifyBorder := blurBorder
	if m.focusedEnv == notifyIdx {
		notifyBorder = focusedBorder
	}
	styledNotify := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(notifyBorder).Width(10).Render(m.envInputs[notifyIdx].View())
	for _, l := range strings.Split(styledNotify, "\n") {
		if l != "" {
			inner = append(inner, l)
		}
	}
	inner = append(inner, "")

	// Save/Cancel buttons
	saveBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Save")
	cancelBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Cancel")
	if m.focusedEnv == submitIdx {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.focusedEnv == cancelIdx {
		cancelBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Fail)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Cancel")
	}
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("─────────────────────────────────────────────────────"))
	inner = append(inner, "  "+saveBg+"    "+cancelBg)

	return m.renderFramedBox("Edit Script Config", "#e2e8f0", activeTheme.Accent, inner, 62)
}

func (m *model) renderDeleteConfirm() string {
	var inner []string

	script := m.scripts[m.cursor]
	scriptChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#f43f5e")).
		Bold(true).Padding(0, 1).
		Render(script.Config.NameAlias)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#f43f5e")).Bold(true).Render("⚠  Delete script:"))
	inner = append(inner, "   "+scriptChip)
	inner = append(inner, "")
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render("This will remove the script from config and crontab."))
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("This action cannot be undone."))
	inner = append(inner, "")

	cancelBg := lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#94a3b8")).Padding(0, 2).Render("Cancel")
	deleteBg := lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Delete")
	if m.confirmDeleteFocused == 0 {
		cancelBg = lipgloss.NewStyle().Background(lipgloss.Color("#334155")).Foreground(lipgloss.Color("#e2e8f0")).Bold(true).Padding(0, 2).Render("Cancel")
	} else if m.confirmDeleteFocused == 1 {
		deleteBg = lipgloss.NewStyle().Background(lipgloss.Color("#f43f5e")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Delete")
	}
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 42)))
	inner = append(inner, "  "+cancelBg+"    "+deleteBg)

	return m.renderFramedBox("Confirm Delete", "#f43f5e", "#f43f5e", inner, 50)
}

func (m *model) renderHistory() string {
	var inner []string

	script := m.scripts[m.cursor]
	scriptChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#818cf8")).
		Bold(true).Padding(0, 1).
		Render(script.Config.NameAlias)
	inner = append(inner, "Run history for  "+scriptChip)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 54)))
	inner = append(inner, "")

	if len(m.historyItems) == 0 {
		inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("  No past runs found in the output folder."))
		inner = append(inner, "")
	} else {
		maxVisible := 8
		startIdx := 0
		if len(m.historyItems) > maxVisible {
			startIdx = m.historyCursor - maxVisible/2
			if startIdx < 0 {
				startIdx = 0
			}
			if startIdx+maxVisible > len(m.historyItems) {
				startIdx = len(m.historyItems) - maxVisible
			}
		}
		endIdx := startIdx + maxVisible
		if endIdx > len(m.historyItems) {
			endIdx = len(m.historyItems)
		}

		for i := startIdx; i < endIdx; i++ {
			item := m.historyItems[i]
			cursorGlyph := "  "
			if i == m.historyCursor {
				cursorGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Render("▶ ")
			}

			var badge string
			switch item.State {
			case "Success":
				badge = badgeSuccess.Render(" ✓ SUCCESS ")
			case "Failed":
				badge = badgeFailed.Render(" ✕ FAILED  ")
			case "Stopped":
				badge = badgeStopped.Render(" ⊘ STOPPED ")
			case "Running":
				badge = badgeRunning.Render(" ● RUNNING ")
			default:
				badge = badgeIdle.Render("   " + item.State + "   ")
			}

			timeStr := item.Timestamp.Format("2006-01-02  15:04")
			taskNum := lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0")).Render(fmt.Sprintf("Task #%d", item.TaskID))
			ts := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(timeStr)
			itemText := cursorGlyph + taskNum + "   " + badge + "   " + ts
			inner = append(inner, itemText)
		}
		inner = append(inner, "")
	}

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155"))
	hintKey := lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#a5b4fc")).Bold(true).Padding(0, 1)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 54)))
	inner = append(inner, hintKey.Render("Enter")+hintStyle.Render(" load log")+"   "+hintKey.Render("Esc")+hintStyle.Render(" close"))

	return m.renderFramedBox("Execution History", "#e2e8f0", activeTheme.Accent, inner, 62)
}

func (m *model) renderHeader(width int) string {
	brand := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Accent)).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true).
		Padding(0, 2).
		Render("SCTL")
	tagline := lipgloss.NewStyle().
		Foreground(lipgloss.Color(activeTheme.Accent)).
		Bold(true).
		Render(" Script Controller")

	running, okCount, failed := 0, 0, 0
	for _, s := range m.scripts {
		switch s.State {
		case "Running":
			running++
		case "Success":
			okCount++
		case "Failed":
			failed++
		}
	}
	pipeSep := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render("  │  ")
	var chips []string
	chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf("%d scripts", len(m.scripts))))
	if running > 0 {
		chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Bold(true).Render(fmt.Sprintf("\u25cf %d running", running)))
	}
	if okCount > 0 {
		chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("#059669")).Render(fmt.Sprintf("\u2713 %d ok", okCount)))
	}
	if failed > 0 {
		chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("#f43f5e")).Bold(true).Render(fmt.Sprintf("\u2715 %d failed", failed)))
	}
	statsStr := strings.Join(chips, pipeSep)

	modeTxt, modeFg := "SEQUENTIAL", "#f59e0b"
	if m.parallelMode {
		modeTxt, modeFg = "PARALLEL", "#818cf8"
	}
	modeChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color(modeFg)).
		Bold(true).Padding(0, 1).Render(modeTxt)

	cfgPath := GetConfigPath()
	if len(cfgPath) > 28 {
		cfgPath = "\u2026" + cfgPath[len(cfgPath)-25:]
	}
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).
		Render(cfgPath + "  " + time.Now().Format("15:04"))

	leftSide := brand + tagline
	rightSide := statsStr + pipeSep + modeChip + pipeSep + meta

	sp := width - lipgloss.Width(leftSide) - lipgloss.Width(rightSide) - 4
	if sp < 1 {
		sp = 1
	}
	line := "  " + leftSide + strings.Repeat(" ", sp) + rightSide
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("\u2501", width))
	return "\n" + line + "\n" + rule
}

func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing TUI..."
	}
	if m.activeView == "form" {
		return m.renderForm()
	}
	if m.activeView == "env_form" {
		return m.renderEnvForm()
	}
	if m.activeView == "delete_confirm" {
		return m.renderDeleteConfirm()
	}
	if m.activeView == "history" {
		return m.renderHistory()
	}

	header := m.renderHeader(m.width)

	leftWidth := int(float64(m.width) * 0.4)
	rightWidth := m.width - leftWidth

	bottomBar := m.renderBottomBar(m.width)
	bottomBarHeight := strings.Count(bottomBar, "\n") + 1

	headerHeight := m.getHeaderHeight()
	panelHeight := m.height - headerHeight - bottomBarHeight
	if panelHeight < 5 {
		panelHeight = 5
	}

	vHeight := panelHeight - 5
	if vHeight < 1 {
		vHeight = 1
	}
	m.viewport.Height = vHeight

	leftPanel := m.renderLeftPanel(leftWidth, panelHeight)
	rightPanel := m.renderRightPanel(rightWidth, panelHeight)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	return lipgloss.JoinVertical(lipgloss.Left, header, panels, bottomBar)
}
