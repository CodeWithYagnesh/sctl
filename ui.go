package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var progressCompletedChar = "▮"
var progressPendingChar = "▯"

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

	filled := strings.Repeat(progressCompletedChar, filledLen)
	empty := strings.Repeat(progressPendingChar, emptyLen)
	return filledStyle.Render(filled) + emptyStyle.Render(empty)
}
func (m *model) renderGroupDetailPanel(width, height int) string {
	borderStyle := unfocusedStyle
	if m.groupActivePanel == panelRight {
		borderStyle = focusedStyle
	}

	availW := width - 12
	if availW < 10 {
		availW = 10
	}

	if len(m.groups) == 0 || m.groupCursor >= len(m.groups) {
		return borderStyle.Width(width - 4).Height(height - 2).Render(
			lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
				Render("Select a group to view pipeline and script statuses."),
		)
	}

	group := m.groups[m.groupCursor]

	// Header
	groupChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#e2e8f0")).
		Bold(true).Padding(0, 1).
		Render(group.Name)

	modeBadge := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#818cf8")).
		Bold(true).Padding(0, 1).
		Render("PARALLEL")
	if group.Pipeline {
		modeBadge = lipgloss.NewStyle().
			Background(lipgloss.Color("#0f4c81")).
			Foreground(lipgloss.Color("#7dd3fc")).
			Bold(true).Padding(0, 1).
			Render("PIPELINE")
	}

	countBadge := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#64748b")).
		Padding(0, 1).
		Render(fmt.Sprintf("%d scripts", len(group.Scripts)))

	scriptMap := make(map[string]*ScriptState)
	for i := range m.scripts {
		scriptMap[m.scripts[i].Config.NameAlias] = &m.scripts[i]
	}
	hasRunning := false
	for _, alias := range group.Scripts {
		if sc, ok := scriptMap[alias]; ok && sc.State == "Running" {
			hasRunning = true
			break
		}
	}

	headerRight := modeBadge + "  " + countBadge
	if hasRunning {
		frame := spinnerFrames[int(time.Now().UnixNano()/200000000)%len(spinnerFrames)]
		liveChip := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" " + frame + " LIVE ")
		headerRight = liveChip + "  " + headerRight
	}

	breadcrumb := groupChip
	sp := availW - lipgloss.Width(breadcrumb) - lipgloss.Width(headerRight)
	if sp < 1 {
		sp = 1
	}
	topLine := breadcrumb + strings.Repeat(" ", sp) + headerRight

	if group.Description != "" {
		desc := group.Description
		if len(desc) > availW-4 {
			desc = desc[:availW-7] + "..."
		}
		topLine += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render("   " + desc)
	}

	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))

	aliasToIdx := make(map[string]int)
	for i, s := range m.scripts {
		aliasToIdx[s.Config.NameAlias] = i
	}

	var cardContent strings.Builder

	if len(group.Scripts) == 0 {
		cardContent.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render("  No scripts in this group. Press Enter to add members.") + "\n")
	} else {
		pipelineHalted := false
		haltIndex := -1
		if group.Pipeline {
			for i, alias := range group.Scripts {
				if idx, ok := aliasToIdx[alias]; ok {
					if m.scripts[idx].State == "Failed" || m.scripts[idx].State == "Stopped" {
						pipelineHalted = true
						haltIndex = i
						break
					}
				}
			}
		}

		for i, alias := range group.Scripts {
			idx, ok := aliasToIdx[alias]
			var script ScriptState
			if ok {
				script = m.scripts[idx]
			}

			if i > 0 {
				if group.Pipeline {
					prevAlias := group.Scripts[i-1]
					prevIdx, prevOk := aliasToIdx[prevAlias]
					connectorColor := "#334155"
					connectorChar := "│"
					if prevOk {
						prevState := m.scripts[prevIdx].State
						if prevState == "Success" {
							connectorColor = activeTheme.Success
						} else if prevState == "Running" {
							connectorColor = activeTheme.Accent
							connectorChar = "┃"
						} else if prevState == "Failed" || prevState == "Stopped" {
							connectorChar = "✕"
							connectorColor = activeTheme.Fail
						}
					}
					connector := lipgloss.NewStyle().Foreground(lipgloss.Color(connectorColor)).Bold(true).Render("   " + connectorChar)
					cardContent.WriteString(connector + "\n")
				} else {
					sepLine := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render("   ───")
					cardContent.WriteString(sepLine + "\n")
				}
			}

			isBlocked := false
			if group.Pipeline && pipelineHalted && haltIndex >= 0 && i > haltIndex {
				isBlocked = true
			}

			var badge string
			switch script.State {
			case "Running":
				frame := spinnerFrames[int(time.Now().UnixNano()/200000000)%len(spinnerFrames)]
				badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" " + frame + " RUNNING ")
			case "Success":
				badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Success)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" ✓ SUCCESS ")
			case "Failed":
				badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Fail)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" ✕ FAILED ")
			case "Stopped":
				badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Stopped)).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1).Render(" ⊘ STOPPED ")
			default:
				if !ok {
					badge = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#f43f5e")).Padding(0, 1).Render("  MISSING  ")
				} else if isBlocked {
					badge = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#64748b")).Padding(0, 1).Render(" ○ BLOCKED ")
				} else {
					badge = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#9ca3af")).Padding(0, 1).Render("   IDLE    ")
				}
			}

			seqNum := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(fmt.Sprintf("%d.", i+1))
			nameStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
			if !ok {
				nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f43f5e"))
			} else if isBlocked {
				nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#475569"))
			}
			name := nameStyle.Render(alias)

			row1 := " " + seqNum + " " + name
			r1sp := availW - lipgloss.Width(row1) - lipgloss.Width(badge) - 2
			if r1sp < 1 {
				r1sp = 1
			}
			row1 += strings.Repeat(" ", r1sp) + badge

			barW := availW - 14
			if barW < 4 {
				barW = 4
			}
			progress := 0
			if ok {
				progress = script.Progress
			}
			var pBar string
			if !ok || isBlocked {
				pBar = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("▯", barW))
			} else {
				pBar = drawProgressBar(barW, float64(progress)/100.0, script.State == "Running")
			}
			pctLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf(" %3d%%", progress))
			if script.State == "Idle" || !ok || isBlocked {
				pctLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("  --")
			}
			row2 := "     " + pBar + pctLabel

			row3 := ""
			if ok && !isBlocked {
				if script.State == "Running" && !script.StartedAt.IsZero() {
					elapsed := time.Since(script.StartedAt).Round(time.Second)
					row3 = "     " + lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render(fmt.Sprintf("⏱ %s", formatDuration(elapsed)))
				} else if (script.State == "Success" || script.State == "Failed" || script.State == "Stopped") && !script.StartedAt.IsZero() && !script.FinishedAt.IsZero() {
					dur := script.FinishedAt.Sub(script.StartedAt).Round(time.Second)
					startStr := script.StartedAt.Format("15:04:05")
					row3 = "     " + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf("%s  →  %s", startStr, formatDuration(dur)))
				}
			}

			row4 := ""
			if ok && script.State == "Running" && script.Logs != "" {
				logLines := strings.Split(script.Logs, "\n")
				lastLine := ""
				for j := len(logLines) - 1; j >= 0; j-- {
					if strings.TrimSpace(logLines[j]) != "" {
						lastLine = strings.TrimSpace(logLines[j])
						break
					}
				}
				if lastLine != "" {
					previewW := availW - 10
					if len(lastLine) > previewW {
						lastLine = lastLine[:previewW-3] + "..."
					}
					row4 = "     " + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("› " + lastLine)
				}
			}

			cardBorderColor := activeTheme.Idle
			if m.groupActivePanel == panelRight && m.groupScriptCursor == i {
				cardBorderColor = activeTheme.Accent
			} else if ok {
				switch script.State {
				case "Running":
					cardBorderColor = activeTheme.Accent
				case "Success":
					cardBorderColor = activeTheme.Success
				case "Failed", "Stopped":
					cardBorderColor = activeTheme.Fail
				}
			}

			cardStyle := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(cardBorderColor)).
				Padding(0, 1).
				Width(availW - 2)

			cardContentStr := row1 + "\n" + row2
			if row3 != "" {
				cardContentStr += "\n" + row3
			}
			if row4 != "" {
				cardContentStr += "\n" + row4
			}
			cardContent.WriteString(cardStyle.Render(cardContentStr) + "\n")
		}

		if pipelineHalted {
			haltMsg := lipgloss.NewStyle().
				Background(lipgloss.Color("#1e293b")).
				Foreground(lipgloss.Color("#f43f5e")).
				Bold(true).Padding(0, 1).
				Render("⛔ PIPELINE HALTED")
			haltDetail := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render("Downstream steps blocked")
			cardContent.WriteString("\n  " + haltMsg + "  " + haltDetail + "\n")
		}
	}

	// Setup viewport for cards
	headerLines := 2
	logSectionHeight := 4
	vpHeight := height - 2 - headerLines - 1 - logSectionHeight
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.groupViewport.Width = availW
	m.groupViewport.Height = vpHeight
	m.groupViewport.SetContent(cardContent.String())

	// Build log preview
	logPreview := m.renderGroupLogPreview(availW, group, aliasToIdx)

	logDivider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))

	fullContent := topLine + "\n" + divider + "\n" + m.groupViewport.View() + "\n" + logDivider + "\n" + logPreview

	return borderStyle.Width(width - 4).Height(height - 2).Render(fullContent)
}

func (m *model) renderGroupLogPreview(availW int, group GroupConfig, aliasToIdx map[string]int) string {
	var sb strings.Builder
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6366f1")).Render("  LOG PREVIEW")
	sb.WriteString(title)
	sb.WriteString("\n")

	if len(group.Scripts) == 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("   No scripts in group"))
		return sb.String()
	}

	for _, alias := range group.Scripts {
		idx, ok := aliasToIdx[alias]
		if !ok {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#f43f5e")).Render(fmt.Sprintf("   %s: MISSING", alias)))
			sb.WriteString("\n")
			continue
		}
		script := m.scripts[idx]
		lastLine := ""
		if script.Logs != "" {
			logLines := strings.Split(script.Logs, "\n")
			for j := len(logLines) - 1; j >= 0; j-- {
				if strings.TrimSpace(logLines[j]) != "" {
					lastLine = strings.TrimSpace(logLines[j])
					break
				}
			}
		}
		if lastLine == "" {
			lastLine = "— no output —"
		}
		previewW := availW - 12
		if len(lastLine) > previewW {
			lastLine = lastLine[:previewW-3] + "..."
		}
		aliasStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(alias + ":")
		logStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render(lastLine)
		sb.WriteString("   " + aliasStyle + " " + logStyle + "\n")
	}
	return sb.String()
}

func (m *model) renderPipelineViz(group GroupConfig, availW int) []string {
	var lines []string

	if group.Pipeline {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("  PIPELINE SEQUENCE"))
	} else {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("  PARALLEL EXECUTION"))
	}
	lines = append(lines, "")

	scriptMap := make(map[string]*ScriptState)
	for i := range m.scripts {
		scriptMap[m.scripts[i].Config.NameAlias] = &m.scripts[i]
	}

	boxW := availW - 6
	if boxW > 40 {
		boxW = 40
	}
	if boxW < 20 {
		boxW = 20
	}

	for i, alias := range group.Scripts {
		sc, ok := scriptMap[alias]
		if !ok {
			continue
		}

		name := sc.Config.NameAlias
		if len(name) > boxW-4 {
			name = name[:boxW-7] + "..."
		}

		var statusBadge string
		var boxBorderColor string = activeTheme.Idle
		switch sc.State {
		case "Running":
			frame := spinnerFrames[int(time.Now().UnixNano()/200000000)%len(spinnerFrames)]
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Bold(true).Render(" " + frame + " RUNNING ")
			boxBorderColor = activeTheme.Accent
		case "Success":
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Success)).Render(" ✓ SUCCESS ")
			boxBorderColor = activeTheme.Success
		case "Failed":
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Fail)).Render(" ✕ FAILED ")
			boxBorderColor = activeTheme.Fail
		case "Stopped":
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Stopped)).Render(" ⊘ STOPPED ")
			boxBorderColor = activeTheme.Stopped
		default:
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Render("   IDLE   ")
		}

		barW := boxW - lipgloss.Width(statusBadge) - 6
		if barW < 4 {
			barW = 4
		}
		var pBar string
		if sc.State != "Idle" {
			pBar = drawProgressBar(barW, float64(sc.Progress)/100.0, sc.State == "Running")
		} else {
			pBar = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("▯", barW))
		}

		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(boxBorderColor)).
			Padding(0, 1).
			Width(boxW)

		boxContent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0")).Render(name) + "\n" +
			" " + pBar + " " + statusBadge

		boxRendered := boxStyle.Render(boxContent)
		boxLines := strings.Split(boxRendered, "\n")
		for _, bl := range boxLines {
			lines = append(lines, "  "+bl)
		}

		if group.Pipeline && i < len(group.Scripts)-1 {
			arrow := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("     ↓")
			lines = append(lines, arrow)
		} else if !group.Pipeline && i < len(group.Scripts)-1 {
			sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render("     ─")
			lines = append(lines, sep)
		}
	}

	return lines
}

func (m *model) renderGroupScriptStatuses(group GroupConfig, availW int, maxLines int) []string {
	var lines []string

	scriptMap := make(map[string]*ScriptState)
	for i := range m.scripts {
		scriptMap[m.scripts[i].Config.NameAlias] = &m.scripts[i]
	}

	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Bold(true).Render("  LIVE SCRIPT STATUSES"))
	lines = append(lines, "")

	// Each item = 3 lines. Reserve 2 lines for header.
	itemHeight := 3
	maxItems := (maxLines - 2) / itemHeight
	if maxItems < 1 {
		maxItems = 1
	}

	if m.groupScriptCursor < 0 {
		m.groupScriptCursor = 0
	}
	if m.groupScriptCursor >= len(group.Scripts) {
		m.groupScriptCursor = len(group.Scripts) - 1
	}
	if m.groupScriptCursor < 0 {
		m.groupScriptCursor = 0
	}

	startIdx := 0
	if len(group.Scripts) > maxItems {
		startIdx = m.groupScriptCursor - maxItems/2
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx+maxItems > len(group.Scripts) {
			startIdx = len(group.Scripts) - maxItems
		}
	}
	endIdx := startIdx + maxItems
	if endIdx > len(group.Scripts) {
		endIdx = len(group.Scripts)
	}

	for i := startIdx; i < endIdx; i++ {
		alias := group.Scripts[i]
		sc, ok := scriptMap[alias]
		if !ok {
			continue
		}

		isSelected := i == m.groupScriptCursor && m.groupActivePanel == panelRight
		cursorGlyph := "  "
		if isSelected {
			cursorGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("▶ ")
		}

		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
		if isSelected {
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
		}
		name := nameStyle.Render(sc.Config.NameAlias)

		var badge string
		switch sc.State {
		case "Running":
			frame := spinnerFrames[int(time.Now().UnixNano()/200000000)%len(spinnerFrames)]
			badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" " + frame + " RUNNING ")
		case "Success":
			badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Success)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" ✓ SUCCESS ")
		case "Failed":
			badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Fail)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1).Render(" ✕ FAILED ")
		case "Stopped":
			badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Stopped)).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1).Render(" ⊘ STOPPED ")
		default:
			badge = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#9ca3af")).Padding(0, 1).Render("   IDLE   ")
		}

		row1 := cursorGlyph + name
		r1sp := availW - lipgloss.Width(row1) - lipgloss.Width(badge) - 2
		if r1sp < 1 {
			r1sp = 1
		}
		row1 += strings.Repeat(" ", r1sp) + badge

		barW := availW - 14
		if barW < 4 {
			barW = 4
		}
		var pBar string
		if sc.State != "Idle" {
			pBar = drawProgressBar(barW, float64(sc.Progress)/100.0, sc.State == "Running")
		} else {
			pBar = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render(strings.Repeat("▯", barW))
		}
		pctLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(fmt.Sprintf(" %3d%%", sc.Progress))
		if sc.State == "Idle" {
			pctLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("  --")
		}
		row2 := "   " + pBar + pctLabel

		logPreview := ""
		if sc.Logs != "" {
			logLines := strings.Split(sc.Logs, "\n")
			lastLine := ""
			for j := len(logLines) - 1; j >= 0; j-- {
				if strings.TrimSpace(logLines[j]) != "" {
					lastLine = strings.TrimSpace(logLines[j])
					break
				}
			}
			if lastLine != "" {
				previewW := availW - 8
				if len(lastLine) > previewW {
					lastLine = lastLine[:previewW-3] + "..."
				}
				logPreview = lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("   › " + lastLine)
			}
		}

		lines = append(lines, row1)
		lines = append(lines, row2)
		if logPreview != "" {
			lines = append(lines, logPreview)
		} else {
			lines = append(lines, "")
		}
	}

	if len(group.Scripts) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("  No scripts assigned."))
	}

	return lines
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

	availH := height - 2
	cardsAreaHeight := availH - headerLines
	if cardsAreaHeight < 5 {
		cardsAreaHeight = 5
	}

	cardCost := 5
	gapCost := 1
	cardTotalHeight := cardCost + gapCost

	usableHeight := cardsAreaHeight - 2
	if usableHeight < cardCost {
		usableHeight = cardCost
	}
	maxCards := usableHeight / cardTotalHeight
	if maxCards < 1 {
		maxCards = 1
	}

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

	if m.listOffset > 0 {
		moreAbove := m.listOffset
		topHint := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Italic(true).
			Render(fmt.Sprintf("  ▲ %d script(s) above...", moreAbove))
		s.WriteString(topHint + "\n")
	}

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
		var pBar string
		if script.State != "Idle" {
			pBar = drawProgressBar(barW, float64(script.Progress)/100.0, script.State == "Running")
		} else {
			pBar = ""
		}
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
		} else if script.State == "Success" {
			cardBorderColor = lipgloss.Color(activeTheme.Success)
		}
		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cardBorderColor).
			Padding(0, 1).
			Width(availW)

		s.WriteString(cardStyle.Render(cardContent) + "\n")
	}

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
	var bindings []binding
	if m.activePanel == panelLeft {
		bindings = []binding{
			{"R", "Run"}, {"S", "Stop"}, {"Space", "Select"},
			{"A", "Add"}, {"E", "Edit"}, {"D", "Delete"},
			{"/", "Filter"}, {"H", "History"}, {"P", "Parallel"}, {"G", "Groups"},
			{"Tab", "Switch"}, {"Q", "Quit"},
		}
	} else {
		bindings = []binding{
			{"[", "PgUp"}, {"]", "PgDn"}, {"O", "HTML"},
			{"T", "Theme"}, {"C", "CopyLog"}, {"Tab", "Switch"}, {"Q", "Quit"},
		}
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
		"Timeout  (optional, e.g. 10m)",
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
	if m.focusedInput == 6 {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.focusedInput == 7 {
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

	inner = append(inner, labelStyle.Render("Environment variables  (up to 10 key=value pairs)"))
	inner = append(inner, "")
	for i := 1; i < 21; i += 2 {
		pairNum := (i / 2) + 1
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

	inner = append(inner, labelStyle.Render("Desktop notification on complete  (y / n)"))
	notifyBorder := blurBorder
	if m.focusedEnv == 21 {
		notifyBorder = focusedBorder
	}
	styledNotify := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(notifyBorder).Width(10).Render(m.envInputs[21].View())
	for _, l := range strings.Split(styledNotify, "\n") {
		if l != "" {
			inner = append(inner, l)
		}
	}
	inner = append(inner, "")

	saveBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Save")
	cancelBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Cancel")
	if m.focusedEnv == 22 {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.focusedEnv == 23 {
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

// ─── Group Dashboard (split pane) ───────────────────────────────────────────

func (m *model) renderGroupDashboard() string {
	header := m.renderHeader(m.width)
	bottomBar := m.renderGroupBottomBar(m.width)
	bottomBarHeight := strings.Count(bottomBar, "\n") + 1
	headerHeight := m.getHeaderHeight()
	panelHeight := m.height - headerHeight - bottomBarHeight
	if panelHeight < 5 {
		panelHeight = 5
	}

	leftWidth := int(float64(m.width) * 0.38)
	rightWidth := m.width - leftWidth

	leftPanel := m.renderGroupListPanel(leftWidth, panelHeight)
	rightPanel := m.renderGroupDetailPanel(rightWidth, panelHeight)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	return lipgloss.JoinVertical(lipgloss.Left, header, panels, bottomBar)
}

func (m *model) renderGroupListPanel(width, height int) string {
	var s strings.Builder
	borderStyle := unfocusedStyle
	if m.groupActivePanel == panelLeft {
		borderStyle = focusedStyle
	}

	availW := width - 8
	if availW < 10 {
		availW = 10
	}

	titleTxt := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0")).Render("GROUPS")
	countStr := fmt.Sprintf("%d", len(m.groups))
	countChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#64748b")).
		Padding(0, 1).
		Render(countStr)

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("Tab·switch pane")
	titleLeft := titleTxt + " " + countChip
	sp := availW - lipgloss.Width(titleLeft) - lipgloss.Width(hint)
	if sp < 1 {
		sp = 1
	}
	s.WriteString(titleLeft + strings.Repeat(" ", sp) + hint + "\n")

	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))
	s.WriteString(divider + "\n")

	if len(m.groups) == 0 {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render("  No groups. Press A to add.") + "\n")
		return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
	}

	cardCost := 4
	gapCost := 1
	cardTotalHeight := cardCost + gapCost

	usableHeight := height - 4
	if usableHeight < cardCost {
		usableHeight = cardCost
	}
	maxCards := usableHeight / cardTotalHeight
	if maxCards < 1 {
		maxCards = 1
	}

	if m.groupCursor < m.groupListOffset {
		m.groupListOffset = m.groupCursor
	} else if m.groupCursor >= m.groupListOffset+maxCards {
		m.groupListOffset = m.groupCursor - maxCards + 1
	}
	if m.groupListOffset > len(m.groups)-maxCards {
		m.groupListOffset = len(m.groups) - maxCards
	}
	if m.groupListOffset < 0 {
		m.groupListOffset = 0
	}

	endIdx := m.groupListOffset + maxCards
	if endIdx > len(m.groups) {
		endIdx = len(m.groups)
	}

	if m.groupListOffset > 0 {
		moreAbove := m.groupListOffset
		topHint := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Italic(true).
			Render(fmt.Sprintf("  ▲ %d above...", moreAbove))
		s.WriteString(topHint + "\n")
	}

	for i := m.groupListOffset; i < endIdx; i++ {
		group := m.groups[i]
		isSelected := i == m.groupCursor

		cursorGlyph := "  "
		if isSelected {
			cursorGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("▶ ")
		}

		hasRunning := false
		for _, alias := range group.Scripts {
			for _, sc := range m.scripts {
				if sc.Config.NameAlias == alias && sc.State == "Running" {
					hasRunning = true
					break
				}
			}
			if hasRunning {
				break
			}
		}

		nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
		if isSelected {
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
		}
		name := nameStyle.Render(group.Name)
		if hasRunning {
			name = nameStyle.Render(group.Name) + " " + lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("●")
		}

		modeBadge := lipgloss.NewStyle().
			Background(lipgloss.Color("#1e293b")).
			Foreground(lipgloss.Color("#818cf8")).
			Bold(true).Padding(0, 1).
			Render("PARALLEL")
		if group.Pipeline {
			modeBadge = lipgloss.NewStyle().
				Background(lipgloss.Color("#0f4c81")).
				Foreground(lipgloss.Color("#7dd3fc")).
				Bold(true).Padding(0, 1).
				Render("PIPELINE")
		}

		countBadge := lipgloss.NewStyle().
			Background(lipgloss.Color("#1e293b")).
			Foreground(lipgloss.Color("#64748b")).
			Padding(0, 1).
			Render(fmt.Sprintf("%d scripts", len(group.Scripts)))

		row1 := cursorGlyph + name
		r1sp := availW - lipgloss.Width(row1) - lipgloss.Width(modeBadge)
		if r1sp < 1 {
			r1sp = 1
		}
		row1 += strings.Repeat(" ", r1sp) + modeBadge

		desc := group.Description
		if len(desc) > availW-6 {
			desc = desc[:availW-9] + "..."
		}
		row2 := "   " + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render(desc)
		row3 := "   " + countBadge

		cardBorderColor := lipgloss.Color(activeTheme.Idle)
		if isSelected {
			cardBorderColor = lipgloss.Color(activeTheme.Accent)
		}
		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cardBorderColor).
			Padding(0, 1).
			Width(availW)

		s.WriteString(cardStyle.Render(row1+"\n"+row2+"\n"+row3) + "\n")
	}

	if endIdx < len(m.groups) {
		moreBelow := len(m.groups) - endIdx
		botHint := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Italic(true).
			Render(fmt.Sprintf("  ▼ %d below...", moreBelow))
		s.WriteString(botHint + "\n")
	}

	return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
}

func (m *model) renderGroupBottomBar(width int) string {
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
		{"↑/↓", "Navigate"}, {"Tab", "Switch pane"}, {"R", "Run group"},
		{"S", "Stop"}, {"A", "Add group"}, {"E", "Edit group"},
		{"Enter", "Members"}, {"D", "Delete"}, {"G/Esc", "Back"}, {"Q", "Quit"},
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

func (m *model) renderGroupForm() string {
	var inner []string

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	focusedBorder := lipgloss.Color("#6366f1")
	blurBorder := lipgloss.Color("#1e293b")

	fields := []string{
		"Group name  (unique identifier)",
		"Description",
		"Pipeline mode  (y = sequential, n = parallel)",
	}

	for i, input := range m.groupFormInputs {
		inner = append(inner, labelStyle.Render(fields[i]))
		borderColor := blurBorder
		if i == m.groupFocusedInput {
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
	if m.groupFocusedInput == 3 {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.groupFocusedInput == 4 {
		cancelBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Fail)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Cancel")
	}
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("─────────────────────────────────────────────────────"))
	inner = append(inner, "  "+saveBg+"    "+cancelBg)

	title := "Add Group"
	if m.editingGroupName != "" {
		title = "Edit Group — " + m.editingGroupName
	}
	return m.renderFramedBox(title, "#e2e8f0", activeTheme.Accent, inner, 62)
}

func (m *model) renderGroupMembers() string {
	var inner []string

	if m.groupCursor < len(m.groups) {
		group := m.groups[m.groupCursor]
		groupChip := lipgloss.NewStyle().
			Background(lipgloss.Color("#1e293b")).
			Foreground(lipgloss.Color("#818cf8")).
			Bold(true).Padding(0, 1).
			Render(group.Name)
		inner = append(inner, "Editing members of  "+groupChip)
		inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 54)))
		inner = append(inner, "")
	}

	if len(m.scripts) == 0 {
		inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).Render("  No scripts available to add to group."))
	} else {
		maxVisible := 10
		startIdx := 0
		if len(m.scripts) > maxVisible {
			startIdx = m.groupMemberCursor - maxVisible/2
			if startIdx < 0 {
				startIdx = 0
			}
			if startIdx+maxVisible > len(m.scripts) {
				startIdx = len(m.scripts) - maxVisible
			}
		}
		endIdx := startIdx + maxVisible
		if endIdx > len(m.scripts) {
			endIdx = len(m.scripts)
		}

		for i := startIdx; i < endIdx; i++ {
			script := m.scripts[i]
			cursorGlyph := "  "
			if i == m.groupMemberCursor {
				cursorGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Accent)).Render("▶ ")
			}

			checkGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("○")
			if m.groupMemberChecked[i] {
				checkGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme.Success)).Render("●")
			}

			nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
			if i == m.groupMemberCursor {
				nameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0"))
			}

			line := cursorGlyph + checkGlyph + "  " + nameStyle.Render(script.Config.NameAlias)
			if script.Config.Description != "" {
				desc := script.Config.Description
				if len(desc) > 30 {
					desc = desc[:27] + "..."
				}
				line += lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render("  — " + desc)
			}
			inner = append(inner, line)
		}
	}

	inner = append(inner, "")
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#334155"))
	hintKey := lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(lipgloss.Color("#a5b4fc")).Bold(true).Padding(0, 1)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", 54)))
	inner = append(inner, hintKey.Render("Space")+hintStyle.Render(" toggle")+"  "+hintKey.Render("Enter/S")+hintStyle.Render(" save")+"  "+hintKey.Render("Esc")+hintStyle.Render(" cancel"))

	return m.renderFramedBox("Group Members", "#e2e8f0", activeTheme.Accent, inner, 60)
}

func (m *model) renderGroupDeleteConfirm() string {
	var inner []string

	group := m.groups[m.groupCursor]
	groupChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color("#f43f5e")).
		Bold(true).Padding(0, 1).
		Render(group.Name)
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#f43f5e")).Bold(true).Render("⚠  Delete group:"))
	inner = append(inner, "   "+groupChip)
	inner = append(inner, "")
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Render("This will remove the group configuration."))
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#334155")).Render("Scripts themselves will not be deleted."))
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

	return m.renderFramedBox("Confirm Delete Group", "#f43f5e", "#f43f5e", inner, 50)
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
	if len(m.groups) > 0 {
		chips = append(chips, lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Render(fmt.Sprintf("%d groups", len(m.groups))))
	}
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

	if m.settingsMode {
		return m.renderSettings()
	}
	if m.cleanupMode {
		return m.renderCleanup()
	}
	if m.activeView == "groups" {
		return m.renderGroupDashboard()
	}
	if m.activeView == "group_form" {
		return m.renderGroupForm()
	}
	if m.activeView == "group_members" {
		return m.renderGroupMembers()
	}
	if m.activeView == "group_delete_confirm" {
		return m.renderGroupDeleteConfirm()
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

func (m *model) renderSettings() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6366f1")).Render(" Progress Bar Settings ")
	completed := m.settingsInputs[0].View()
	pending := m.settingsInputs[1].View()
	content := fmt.Sprintf("%s\n\nCompleted char:\n%s\n\nPending char:\n%s\n\nPress Enter to save, Esc to cancel, Tab to switch", title, completed, pending)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6366f1")).
		Padding(1, 2).
		Width(60).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m *model) renderCleanup() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6366f1")).Render(" Cleanup Task Files ")
	mode := "All scripts"
	if !m.cleanupModeAll {
		mode = "Specific alias"
	}
	content := fmt.Sprintf("%s\n\nMode: %s\n\nOlder than days: %s\n\nPress Enter to confirm, Esc to cancel, Tab to toggle mode", title, mode, m.cleanupInput.Value())
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6366f1")).
		Padding(1, 2).
		Width(60).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
