package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

func initialModel() *model {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	scripts := make([]ScriptState, len(cfg.Scripts))
	for i, sc := range cfg.Scripts {
		state := "Idle"
		progress := 0
		logs := ""
		taskID := 0

		history, err := getTaskHistory(sc.OutputFolderPath)
		if err == nil && len(history) > 0 {
			latest := history[0]
			data, err := os.ReadFile(latest.FilePath)
			if err == nil {
				var taskFile TaskYAML
				if err := yaml.Unmarshal(data, &taskFile); err == nil {
					state = taskFile.Task.State
					progress = taskFile.Task.Progress
					logs = taskFile.Task.Logs.Value
					taskID = taskFile.Task.TaskID
				}
			}
		}

		scripts[i] = ScriptState{
			Config:   sc,
			TaskID:   taskID,
			State:    state,
			Progress: progress,
			Logs:     logs,
			Checked:  false,
		}
	}

	cfg.Theme = normalizeTheme(cfg.Theme)
	activeTheme = cfg.Theme

	inputs := make([]textinput.Model, 6)
	for i := range inputs {
		t := textinput.New()
		t.Width = 40
		switch i {
		case 0:
			t.Placeholder = "e.g., system_backup"
		case 1:
			t.Placeholder = "e.g., Run automated system backups"
		case 2:
			t.Placeholder = "e.g., bash scripts/backup.sh"
		case 3:
			t.Placeholder = "e.g., ./output/backup"
		case 4:
			t.Placeholder = "e.g., */5 * * * * (optional)"
		case 5:
			t.Placeholder = "e.g., user@192.168.1.1 (optional, blank = local)"
		}
		inputs[i] = t
	}
	inputs[0].Focus()

	// Initialize group form inputs
	groupInputs := make([]textinput.Model, 3)
	for i := range groupInputs {
		t := textinput.New()
		t.Width = 40
		switch i {
		case 0:
			t.Placeholder = "e.g., deploy_pipeline"
		case 1:
			t.Placeholder = "e.g., Deploy to production"
		case 2:
			t.Placeholder = "y / n (pipeline = sequential execution)"
		}
		groupInputs[i] = t
	}
	groupInputs[0].Focus()

	m := &model{
		config:            cfg,
		scripts:           scripts,
		cursor:            0,
		activePanel:       panelLeft,
		parallelMode:      false,
		viewport:          viewport.New(0, 0),
		runningIndex:      -1,
		activeView:        "main",
		formInputs:        inputs,
		focusedInput:      0,
		statusMsg:         "Welcome to sctl! Select a script and press 'R' to run. Press 'G' for Groups.",
		statusMsgTime:     time.Time{},
		theme:             cfg.Theme,
		groups:            cfg.Groups,
		groupCursor:       0,
		groupListOffset:   0,
		groupFormInputs:   groupInputs,
		groupFocusedInput: 0,
	}
	m.applyTheme()
	return m
}

func formatDuration(d time.Duration) string {
	totalSeconds := int(d.Round(time.Second).Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

func formatExecutionStatus(state string, startedAt, finishedAt time.Time, progress int) string {
	stateLabel := strings.ToUpper(strings.TrimSpace(state))
	if stateLabel == "" {
		stateLabel = "IDLE"
	}

	var elapsed time.Duration
	if !finishedAt.IsZero() && !startedAt.IsZero() {
		elapsed = finishedAt.Sub(startedAt)
	} else if !startedAt.IsZero() {
		elapsed = time.Since(startedAt)
	}

	switch stateLabel {
	case "RUNNING":
		frame := spinnerFrames[int(time.Now().UnixNano()/200000000)%len(spinnerFrames)]
		if elapsed > 0 {
			return fmt.Sprintf("%s %s ⏱ %s", frame, stateLabel, formatDuration(elapsed))
		}
		return fmt.Sprintf("%s %s", frame, stateLabel)
	case "SUCCESS":
		if elapsed > 0 {
			return fmt.Sprintf("✓ %s ⏱ %s", stateLabel, formatDuration(elapsed))
		}
		return fmt.Sprintf("✓ %s", stateLabel)
	case "FAILED":
		if elapsed > 0 {
			return fmt.Sprintf("✕ %s ⏱ %s", stateLabel, formatDuration(elapsed))
		}
		return fmt.Sprintf("✕ %s", stateLabel)
	case "STOPPED":
		if elapsed > 0 {
			return fmt.Sprintf("⊘ %s ⏱ %s", stateLabel, formatDuration(elapsed))
		}
		return fmt.Sprintf("⊘ %s", stateLabel)
	default:
		return fmt.Sprintf("● %s", stateLabel)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func (m *model) Init() tea.Cmd {
	return tickCmd()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		if len(m.scripts) > 0 && m.cursor >= 0 && m.cursor < len(m.scripts) && m.scripts[m.cursor].State == "Running" {
			m.updateViewport()
		}
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftWidth := int(float64(m.width) * 0.42)
		rightWidth := m.width - leftWidth

		vWidth := rightWidth - 8
		if vWidth < 10 {
			vWidth = 10
		}
		m.viewport.Width = vWidth
		m.updateViewport()

	case TaskStartedMsg:
		for i := range m.scripts {
			if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
				m.scripts[i].Cmd = msg.Cmd
				m.scripts[i].TaskID = msg.TaskID
				m.scripts[i].State = "Running"
				m.scripts[i].StartedAt = time.Now()
				m.scripts[i].FinishedAt = time.Time{}
				m.scripts[i].Logs = ""
				m.scripts[i].Progress = 0
				if i == m.cursor {
					m.updateViewport()
				}
				break
			}
		}
		m.statusMsg = fmt.Sprintf("Started script: %s (Task %d)", msg.ScriptNameAlias, msg.TaskID)
		m.statusMsgTime = time.Now()
		return m, nil

	case TaskStartErrorMsg:
		for i := range m.scripts {
			if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
				m.scripts[i].State = "Failed"
				m.scripts[i].Logs = fmt.Sprintf("Execution failed to start: %v", msg.Error)
				if i == m.cursor {
					m.updateViewport()
				}
				if !m.parallelMode && i == m.runningIndex {
					m.runningIndex = -1
					return m, m.runNextSequentialCmd()
				}
				break
			}
		}
		m.statusMsg = fmt.Sprintf("Error starting %s: %v", msg.ScriptNameAlias, msg.Error)
		m.statusMsgTime = time.Now()
		return m, nil

	case TaskUpdateMsg:
		for i := range m.scripts {
			if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
				m.scripts[i].State = msg.State
				m.scripts[i].Progress = msg.Progress
				if msg.TaskID > 0 {
					m.scripts[i].TaskID = msg.TaskID
				}
				if msg.State == "Running" && m.scripts[i].StartedAt.IsZero() {
					m.scripts[i].StartedAt = time.Now()
				}
				if msg.Done {
					m.scripts[i].FinishedAt = time.Now()
				}
				if msg.LogLine != "" {
					if m.scripts[i].Logs == "" {
						m.scripts[i].Logs = msg.LogLine
					} else {
						m.scripts[i].Logs += "\n" + msg.LogLine
					}
				}
				if i == m.cursor {
					m.updateViewport()
				}
				if msg.Done {
					m.scripts[i].Cmd = nil
					var statusLine string
					if msg.Error != nil && msg.State != "Stopped" {
						statusLine = fmt.Sprintf("Script '%s' failed: %v", msg.ScriptNameAlias, msg.Error)
					} else if msg.State == "Stopped" {
						statusLine = fmt.Sprintf("Script '%s' was stopped.", msg.ScriptNameAlias)
					} else {
						statusLine = fmt.Sprintf("Script '%s' finished successfully.", msg.ScriptNameAlias)
					}
					m.statusMsg = statusLine
					m.statusMsgTime = time.Now()
					if m.scripts[i].Config.Notify {
						go sendNotification(msg.ScriptNameAlias, statusLine, msg.State)
					}
					if !m.parallelMode && i == m.runningIndex {
						m.runningIndex = -1
						return m, m.runNextSequentialCmd()
					}
				}
				break
			}
		}

	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			for i := range m.scripts {
				if m.scripts[i].Cmd != nil {
					_ = StopTask(m.scripts[i].Cmd)
				}
			}
			return m, tea.Quit
		}

		if m.activeView == "form" {
			return m.updateForm(msg)
		}

		if m.activeView == "env_form" {
			return m.updateEnvForm(msg)
		}

		if m.activeView == "delete_confirm" {
			return m.updateDeleteConfirm(msg)
		}

		if m.activeView == "history" {
			return m.updateHistoryView(msg)
		}

		if m.activeView == "filter" {
			return m.updateFilterView(msg)
		}

		// Group views
		if m.activeView == "groups" {
			return m.updateGroupView(msg)
		}
		if m.activeView == "group_form" {
			return m.updateGroupForm(msg)
		}
		if m.activeView == "group_members" {
			return m.updateGroupMembers(msg)
		}
		if m.activeView == "group_delete_confirm" {
			return m.updateGroupDeleteConfirm(msg)
		}

		switch key {
		case "q":
			for i := range m.scripts {
				if m.scripts[i].Cmd != nil {
					_ = StopTask(m.scripts[i].Cmd)
				}
			}
			return m, tea.Quit
		case "tab":
			if m.activePanel == panelLeft {
				m.activePanel = panelRight
			} else {
				m.activePanel = panelLeft
			}
			return m, nil
		case "p":
			m.parallelMode = !m.parallelMode
			m.statusMsg = fmt.Sprintf("Parallel execution toggled. Now: %t", m.parallelMode)
			m.statusMsgTime = time.Now()
			return m, nil
		case "t":
			m.cycleTheme()
			return m, nil
		case "a":
			m.editingAlias = ""
			m.activeView = "form"
			m.focusedInput = 0
			for i := range m.formInputs {
				m.formInputs[i].SetValue("")
			}
			m.formInputs[0].Focus()
			return m, nil
		case "e":
			if len(m.scripts) > 0 {
				sc := m.scripts[m.cursor].Config
				m.editingAlias = sc.NameAlias
				m.activeView = "form"
				m.focusedInput = 0
				m.formInputs[0].SetValue(sc.NameAlias)
				m.formInputs[1].SetValue(sc.Description)
				m.formInputs[2].SetValue(sc.Command)
				m.formInputs[3].SetValue(sc.OutputFolderPath)
				m.formInputs[4].SetValue(sc.Cron)
				m.formInputs[5].SetValue(sc.Host)
				m.formInputs[0].Focus()
			}
			return m, nil
		case "enter":
			if len(m.scripts) > 0 {
				m.activeView = "env_form"
				m.focusedEnv = 0
				m.initEnvForm()
			}
			return m, nil
		case "d", "delete":
			if len(m.scripts) > 0 {
				m.activeView = "delete_confirm"
				m.confirmDeleteFocused = 0
			}
			return m, nil
		case "/":
			fi := textinput.New()
			fi.Placeholder = "type to filter..."
			fi.CharLimit = 60
			fi.Width = 28
			fi.SetValue(m.filterQuery)
			fi.Focus()
			m.filterInput = fi
			m.activeView = "filter"
			return m, nil
		case "o":
			m.openHTMLOutput()
			return m, nil
		case "h", "H":
			if len(m.scripts) > 0 {
				focusedScript := m.scripts[m.cursor]
				history, err := getTaskHistory(focusedScript.Config.OutputFolderPath)
				if err != nil || len(history) == 0 {
					m.statusMsg = "No history found for script: " + focusedScript.Config.NameAlias
					m.statusMsgTime = time.Now()
				} else {
					m.historyItems = history
					m.historyCursor = 0
					m.activeView = "history"
				}
			}
			return m, nil
		case "s":
			m.stopSelected()
			return m, nil
		case "r":
			cmd := m.runSelected()
			return m, cmd
		case " ":
			if len(m.scripts) > 0 {
				m.scripts[m.cursor].Checked = !m.scripts[m.cursor].Checked
			}
			return m, nil
		case "pgup", "[":
			m.viewport.LineUp(3)
			return m, nil
		case "pgdn", "]":
			m.viewport.LineDown(3)
			return m, nil
		case "up", "k":
			if m.activePanel == panelLeft {
				indices := m.filteredIndices()
				for pos, idx := range indices {
					if idx == m.cursor && pos > 0 {
						m.cursor = indices[pos-1]
						m.updateViewport()
						break
					}
				}
			} else {
				m.viewport.LineUp(1)
			}
			return m, nil
		case "down", "j":
			if m.activePanel == panelLeft {
				indices := m.filteredIndices()
				for pos, idx := range indices {
					if idx == m.cursor && pos < len(indices)-1 {
						m.cursor = indices[pos+1]
						m.updateViewport()
						break
					}
				}
			} else {
				m.viewport.LineDown(1)
			}
			return m, nil
		case "ctrl+up":
			if len(m.scripts) > 1 && m.cursor > 0 {
				m.reorderScript(m.cursor, m.cursor-1)
				m.cursor--
				m.updateViewport()
			}
			return m, nil
		case "ctrl+down":
			if len(m.scripts) > 1 && m.cursor < len(m.scripts)-1 {
				m.reorderScript(m.cursor, m.cursor+1)
				m.cursor++
				m.updateViewport()
			}
			return m, nil
		case "g":
			m.activeView = "groups"
			m.groupCursor = 0
			m.groups = m.config.Groups
			return m, nil
		}
	}
	return m, nil
}

// ─── Group View Handlers ─────────────────────────────────────────────────────

func (m *model) updateGroupView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc", "g":
		m.activeView = "main"
		return m, nil
	case "q":
		for i := range m.scripts {
			if m.scripts[i].Cmd != nil {
				_ = StopTask(m.scripts[i].Cmd)
			}
		}
		return m, tea.Quit
	case "up", "k":
		if m.groupCursor > 0 {
			m.groupCursor--
		}
		return m, nil
	case "down", "j":
		if m.groupCursor < len(m.groups)-1 {
			m.groupCursor++
		}
		return m, nil
	case "a":
		m.editingGroupName = ""
		m.activeView = "group_form"
		m.initGroupForm()
		return m, nil
	case "e":
		if len(m.groups) > 0 {
			g := m.groups[m.groupCursor]
			m.editingGroupName = g.Name
			m.activeView = "group_form"
			m.initGroupForm()
			m.groupFormInputs[0].SetValue(g.Name)
			m.groupFormInputs[1].SetValue(g.Description)
			if g.Pipeline {
				m.groupFormInputs[2].SetValue("y")
			} else {
				m.groupFormInputs[2].SetValue("n")
			}
			m.groupFocusedInput = 0
			m.groupFormInputs[0].Focus()
		}
		return m, nil
	case "enter":
		if len(m.groups) > 0 {
			m.activeView = "group_members"
			m.initGroupMembers()
		}
		return m, nil
	case "d", "delete":
		if len(m.groups) > 0 {
			m.activeView = "group_delete_confirm"
			m.confirmDeleteFocused = 0
		}
		return m, nil
	case "r":
		cmd := m.runGroup()
		return m, cmd
	}
	return m, nil
}

func (m *model) updateGroupForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "groups"
		return m, nil
	case "tab", "down":
		if m.groupFocusedInput < 4 {
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Blur()
			}
			m.groupFocusedInput++
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Focus()
			}
		} else {
			m.groupFocusedInput = 0
			m.groupFormInputs[0].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.groupFocusedInput > 0 {
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Blur()
			}
			m.groupFocusedInput--
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Focus()
			}
		} else {
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Blur()
			}
			m.groupFocusedInput = 4
		}
		return m, nil
	case "enter":
		if m.groupFocusedInput == 3 {
			m.submitGroupForm()
		} else if m.groupFocusedInput == 4 {
			m.activeView = "groups"
		} else {
			m.groupFormInputs[m.groupFocusedInput].Blur()
			m.groupFocusedInput++
			if m.groupFocusedInput < 3 {
				m.groupFormInputs[m.groupFocusedInput].Focus()
			}
		}
		return m, nil
	}
	if m.groupFocusedInput < 3 {
		var cmd tea.Cmd
		m.groupFormInputs[m.groupFocusedInput], cmd = m.groupFormInputs[m.groupFocusedInput].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) updateGroupMembers(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "groups"
		return m, nil
	case "up", "k":
		if m.groupMemberCursor > 0 {
			m.groupMemberCursor--
		}
		return m, nil
	case "down", "j":
		if m.groupMemberCursor < len(m.scripts)-1 {
			m.groupMemberCursor++
		}
		return m, nil
	case " ":
		if len(m.scripts) > 0 {
			m.groupMemberChecked[m.groupMemberCursor] = !m.groupMemberChecked[m.groupMemberCursor]
		}
		return m, nil
	case "enter", "s":
		m.submitGroupMembers()
		return m, nil
	}
	return m, nil
}

func (m *model) updateGroupDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "groups"
		return m, nil
	case "left", "right", "tab", "shift+tab":
		if m.confirmDeleteFocused == 0 {
			m.confirmDeleteFocused = 1
		} else {
			m.confirmDeleteFocused = 0
		}
		return m, nil
	case "enter":
		if m.confirmDeleteFocused == 1 {
			m.deleteSelectedGroup()
		} else {
			m.activeView = "groups"
		}
		return m, nil
	}
	return m, nil
}

func (m *model) initGroupForm() {
	m.groupFormInputs = make([]textinput.Model, 3)
	for i := range m.groupFormInputs {
		t := textinput.New()
		t.Width = 40
		switch i {
		case 0:
			t.Placeholder = "e.g., deploy_pipeline"
		case 1:
			t.Placeholder = "e.g., Deploy to production"
		case 2:
			t.Placeholder = "y / n (pipeline = sequential execution)"
		}
		m.groupFormInputs[i] = t
	}
	m.groupFormInputs[0].Focus()
	m.groupFocusedInput = 0
}

func (m *model) initGroupMembers() {
	m.groupMemberCursor = 0
	m.groupMemberChecked = make([]bool, len(m.scripts))

	if m.groupCursor >= len(m.groups) {
		return
	}
	group := m.groups[m.groupCursor]
	memberSet := make(map[string]bool)
	for _, alias := range group.Scripts {
		memberSet[alias] = true
	}
	for i, s := range m.scripts {
		m.groupMemberChecked[i] = memberSet[s.Config.NameAlias]
	}
}

func (m *model) submitGroupForm() {
	name := strings.TrimSpace(m.groupFormInputs[0].Value())
	desc := strings.TrimSpace(m.groupFormInputs[1].Value())
	pipelineVal := strings.ToLower(strings.TrimSpace(m.groupFormInputs[2].Value()))
	pipeline := pipelineVal == "y" || pipelineVal == "yes"

	if name == "" {
		m.statusMsg = "Error: Group name is required."
		m.statusMsgTime = time.Now()
		return
	}

	if m.editingGroupName != "" {
		if name != m.editingGroupName {
			for _, g := range m.config.Groups {
				if g.Name == name {
					m.statusMsg = fmt.Sprintf("Error: Group '%s' already exists.", name)
					m.statusMsgTime = time.Now()
					return
				}
			}
		}
		for i, g := range m.config.Groups {
			if g.Name == m.editingGroupName {
				m.config.Groups[i].Name = name
				m.config.Groups[i].Description = desc
				m.config.Groups[i].Pipeline = pipeline
				break
			}
		}
	} else {
		for _, g := range m.config.Groups {
			if g.Name == name {
				m.statusMsg = fmt.Sprintf("Error: Group '%s' already exists.", name)
				m.statusMsgTime = time.Now()
				return
			}
		}
		m.config.Groups = append(m.config.Groups, GroupConfig{
			Name:        name,
			Description: desc,
			Pipeline:    pipeline,
			Scripts:     []string{},
		})
	}

	err := SaveConfig(m.config)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error saving config: %v", err)
		m.statusMsgTime = time.Now()
		return
	}

	m.groups = m.config.Groups
	m.statusMsg = fmt.Sprintf("Group '%s' saved successfully.", name)
	m.statusMsgTime = time.Now()
	m.editingGroupName = ""
	m.activeView = "groups"
}

func (m *model) deleteSelectedGroup() {
	if len(m.groups) == 0 {
		m.activeView = "groups"
		return
	}
	idx := m.groupCursor
	name := m.groups[idx].Name

	m.config.Groups = append(m.config.Groups[:idx], m.config.Groups[idx+1:]...)
	_ = SaveConfig(m.config)
	m.groups = m.config.Groups

	if m.groupCursor >= len(m.groups) {
		m.groupCursor = len(m.groups) - 1
	}
	if m.groupCursor < 0 {
		m.groupCursor = 0
	}

	m.activeView = "groups"
	m.statusMsg = fmt.Sprintf("Group '%s' deleted.", name)
	m.statusMsgTime = time.Now()
}

func (m *model) submitGroupMembers() {
	if m.groupCursor >= len(m.groups) {
		return
	}
	var newScripts []string
	for i, checked := range m.groupMemberChecked {
		if checked {
			newScripts = append(newScripts, m.scripts[i].Config.NameAlias)
		}
	}
	m.config.Groups[m.groupCursor].Scripts = newScripts
	err := SaveConfig(m.config)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error saving group: %v", err)
		m.statusMsgTime = time.Now()
		return
	}
	m.groups = m.config.Groups
	m.statusMsg = fmt.Sprintf("Group '%s' membership updated (%d scripts).", m.groups[m.groupCursor].Name, len(newScripts))
	m.statusMsgTime = time.Now()
	m.activeView = "groups"
}

func (m *model) runGroup() tea.Cmd {
	if len(m.groups) == 0 || m.groupCursor >= len(m.groups) {
		m.statusMsg = "No group selected."
		m.statusMsgTime = time.Now()
		return nil
	}
	group := m.groups[m.groupCursor]

	var indicesToRun []int
	for _, alias := range group.Scripts {
		for i, s := range m.scripts {
			if s.Config.NameAlias == alias {
				indicesToRun = append(indicesToRun, i)
				break
			}
		}
	}

	if len(indicesToRun) == 0 {
		m.statusMsg = fmt.Sprintf("Group '%s' has no scripts to run.", group.Name)
		m.statusMsgTime = time.Now()
		return nil
	}

	mode := "parallel"
	if group.Pipeline {
		mode = "pipeline"
	}
	m.statusMsg = fmt.Sprintf("Running group '%s' (%s, %d script(s))...", group.Name, mode, len(indicesToRun))
	m.statusMsgTime = time.Now()

	if group.Pipeline {
		for _, idx := range indicesToRun {
			inQueue := false
			for _, q := range m.runQueue {
				if q == idx {
					inQueue = true
					break
				}
			}
			if !inQueue && m.runningIndex != idx && m.scripts[idx].State != "Running" {
				m.scripts[idx].Logs = ""
				m.scripts[idx].Progress = 0
				m.scripts[idx].State = "Idle"
				m.runQueue = append(m.runQueue, idx)
			}
		}
		if m.runningIndex == -1 {
			return m.runNextSequentialCmd()
		}
	} else {
		var cmds []tea.Cmd
		for _, idx := range indicesToRun {
			m.scripts[idx].Logs = ""
			m.scripts[idx].Progress = 0
			m.scripts[idx].State = "Running"
			cmds = append(cmds, m.runTaskCmd(idx))
		}
		return tea.Batch(cmds...)
	}
	return nil
}

// ─── Existing helpers ────────────────────────────────────────────────────────

func (m *model) reorderScript(i, j int) {
	if i < 0 || j < 0 || i >= len(m.scripts) || j >= len(m.scripts) {
		return
	}
	m.scripts[i], m.scripts[j] = m.scripts[j], m.scripts[i]
	aliasI := m.scripts[j].Config.NameAlias
	aliasJ := m.scripts[i].Config.NameAlias
	ciI, ciJ := -1, -1
	for ci, sc := range m.config.Scripts {
		if sc.NameAlias == aliasI {
			ciI = ci
		}
		if sc.NameAlias == aliasJ {
			ciJ = ci
		}
	}
	if ciI >= 0 && ciJ >= 0 {
		m.config.Scripts[ciI], m.config.Scripts[ciJ] = m.config.Scripts[ciJ], m.config.Scripts[ciI]
	}
	_ = SaveConfig(m.config)
}

func (m *model) filteredIndices() []int {
	var out []int
	q := strings.ToLower(m.filterQuery)
	for i, s := range m.scripts {
		if q == "" ||
			strings.Contains(strings.ToLower(s.Config.NameAlias), q) ||
			strings.Contains(strings.ToLower(s.Config.Description), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m *model) updateFilterView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterQuery = ""
		m.activeView = "main"
		return m, nil
	case "enter":
		m.activeView = "main"
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterQuery = m.filterInput.Value()
	indices := m.filteredIndices()
	if len(indices) > 0 {
		found := false
		for _, idx := range indices {
			if idx == m.cursor {
				found = true
				break
			}
		}
		if !found {
			m.cursor = indices[0]
			m.updateViewport()
		}
	}
	return m, cmd
}

func (m *model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "main"
		return m, nil
	case "tab", "down":
		if m.focusedInput < 7 {
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput++
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Focus()
			}
		} else {
			m.focusedInput = 0
			m.formInputs[0].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.focusedInput > 0 {
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput--
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Focus()
			}
		} else {
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput = 7
		}
		return m, nil
	case "enter":
		if m.focusedInput == 6 {
			m.submitForm()
		} else if m.focusedInput == 7 {
			m.activeView = "main"
		} else {
			m.formInputs[m.focusedInput].Blur()
			m.focusedInput++
			if m.focusedInput < 6 {
				m.formInputs[m.focusedInput].Focus()
			}
		}
		return m, nil
	}
	if m.focusedInput < 6 {
		var cmd tea.Cmd
		m.formInputs[m.focusedInput], cmd = m.formInputs[m.focusedInput].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) updateHistoryView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc", "q":
		m.activeView = "main"
		return m, nil
	case "up", "k":
		if m.historyCursor > 0 {
			m.historyCursor--
		}
		return m, nil
	case "down", "j":
		if m.historyCursor < len(m.historyItems)-1 {
			m.historyCursor++
		}
		return m, nil
	case "enter":
		if len(m.historyItems) > 0 {
			selected := m.historyItems[m.historyCursor]
			data, err := os.ReadFile(selected.FilePath)
			if err == nil {
				var taskFile TaskYAML
				if err := yaml.Unmarshal(data, &taskFile); err == nil {
					m.scripts[m.cursor].Logs = taskFile.Task.Logs.Value
					m.scripts[m.cursor].TaskID = taskFile.Task.TaskID
					m.scripts[m.cursor].State = taskFile.Task.State
					m.scripts[m.cursor].Progress = taskFile.Task.Progress
					m.updateViewport()
					m.statusMsg = fmt.Sprintf("Loaded logs for Task #%d", taskFile.Task.TaskID)
					m.statusMsgTime = time.Now()
				} else {
					m.statusMsg = fmt.Sprintf("Error parsing task file: %v", err)
					m.statusMsgTime = time.Now()
				}
			} else {
				m.statusMsg = fmt.Sprintf("Error reading task file: %v", err)
				m.statusMsgTime = time.Now()
			}
		}
		m.activeView = "main"
		return m, nil
	}
	return m, nil
}

func (m *model) submitForm() {
	alias := strings.TrimSpace(m.formInputs[0].Value())
	desc := strings.TrimSpace(m.formInputs[1].Value())
	cmdStr := strings.TrimSpace(m.formInputs[2].Value())
	outputPath := strings.TrimSpace(m.formInputs[3].Value())
	cronStr := strings.TrimSpace(m.formInputs[4].Value())
	hostStr := strings.TrimSpace(m.formInputs[5].Value())

	if alias == "" || cmdStr == "" || outputPath == "" {
		m.statusMsg = "Error: Name, Command, and Output Path are required."
		m.statusMsgTime = time.Now()
		return
	}
	if cronStr != "" && !isValidCron(cronStr) {
		m.statusMsg = "Error: Invalid cron format. Expected 5 space-separated fields."
		m.statusMsgTime = time.Now()
		return
	}

	if m.editingAlias != "" {
		if alias != m.editingAlias {
			for _, s := range m.scripts {
				if s.Config.NameAlias == alias {
					m.statusMsg = fmt.Sprintf("Error: Alias '%s' is already used by another script.", alias)
					m.statusMsgTime = time.Now()
					return
				}
			}
			// Update group references when alias changes
			for gi := range m.config.Groups {
				for si, a := range m.config.Groups[gi].Scripts {
					if a == m.editingAlias {
						m.config.Groups[gi].Scripts[si] = alias
					}
				}
			}
		}
		updatedConfig := ScriptConfig{
			NameAlias:        alias,
			Description:      desc,
			Command:          cmdStr,
			OutputFolderPath: outputPath,
			Cron:             cronStr,
			Host:             hostStr,
		}
		for ci, sc := range m.config.Scripts {
			if sc.NameAlias == m.editingAlias {
				updatedConfig.Input = sc.Input
				m.config.Scripts[ci] = updatedConfig
				break
			}
		}
		err := SaveConfig(m.config)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Error saving config: %v", err)
			m.statusMsgTime = time.Now()
			return
		}
		for si := range m.scripts {
			if m.scripts[si].Config.NameAlias == m.editingAlias {
				m.scripts[si].Config = updatedConfig
				break
			}
		}
		m.statusMsg = fmt.Sprintf("Script '%s' updated successfully.", alias)
		m.statusMsgTime = time.Now()
		m.editingAlias = ""
		m.activeView = "main"
		return
	}

	for _, s := range m.scripts {
		if s.Config.NameAlias == alias {
			m.statusMsg = fmt.Sprintf("Error: Script alias '%s' already exists.", alias)
			m.statusMsgTime = time.Now()
			return
		}
	}

	newConfig := ScriptConfig{
		NameAlias:        alias,
		Description:      desc,
		Command:          cmdStr,
		OutputFolderPath: outputPath,
		Cron:             cronStr,
		Host:             hostStr,
	}

	m.config.Scripts = append(m.config.Scripts, newConfig)
	err := SaveConfig(m.config)
	if err != nil {
		m.config.Scripts = m.config.Scripts[:len(m.config.Scripts)-1]
		m.statusMsg = fmt.Sprintf("Error saving config: %v", err)
		m.statusMsgTime = time.Now()
		return
	}
	m.statusMsg = fmt.Sprintf("Successfully added script '%s'.", alias)
	m.statusMsgTime = time.Now()

	m.scripts = append(m.scripts, ScriptState{
		Config:   newConfig,
		State:    "Idle",
		Progress: 0,
		Logs:     "",
		Checked:  false,
	})
	m.activeView = "main"
}

func (m *model) updateEnvForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "main"
		return m, nil
	case "tab", "down":
		if m.focusedEnv < 13 {
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv++
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Focus()
			}
		} else {
			m.focusedEnv = 0
			m.envInputs[0].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.focusedEnv > 0 {
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv--
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Focus()
			}
		} else {
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv = 13
		}
		return m, nil
	case "enter":
		if m.focusedEnv == 12 {
			m.submitEnvForm()
		} else if m.focusedEnv == 13 {
			m.activeView = "main"
		} else {
			m.envInputs[m.focusedEnv].Blur()
			m.focusedEnv++
			if m.focusedEnv < 12 {
				m.envInputs[m.focusedEnv].Focus()
			}
		}
		return m, nil
	}
	if m.focusedEnv < 12 {
		var cmd tea.Cmd
		m.envInputs[m.focusedEnv], cmd = m.envInputs[m.focusedEnv].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "main"
		return m, nil
	case "left", "right", "tab", "shift+tab":
		if m.confirmDeleteFocused == 0 {
			m.confirmDeleteFocused = 1
		} else {
			m.confirmDeleteFocused = 0
		}
		return m, nil
	case "enter":
		if m.confirmDeleteFocused == 1 {
			m.deleteSelectedScript()
		} else {
			m.activeView = "main"
		}
		return m, nil
	}
	return m, nil
}

func (m *model) initEnvForm() {
	m.envInputs = make([]textinput.Model, 12)
	for i := range m.envInputs {
		m.envInputs[i] = textinput.New()
		m.envInputs[i].CharLimit = 100
		m.envInputs[i].Width = 40
	}
	m.envInputs[0].Placeholder = "e.g., */5 * * * *"
	m.envInputs[11].Placeholder = "y / n"
	m.envInputs[11].CharLimit = 1

	focusedScript := m.scripts[m.cursor]
	m.envInputs[0].SetValue(focusedScript.Config.Cron)
	if focusedScript.Config.Notify {
		m.envInputs[11].SetValue("y")
	} else {
		m.envInputs[11].SetValue("n")
	}

	keys := make([]string, 0, len(focusedScript.Config.Input))
	for k := range focusedScript.Config.Input {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	idx := 1
	for _, k := range keys {
		if idx >= 11 {
			break
		}
		m.envInputs[idx].SetValue(k)
		m.envInputs[idx+1].SetValue(fmt.Sprintf("%v", focusedScript.Config.Input[k]))
		idx += 2
	}
	for i := 1; i < 11; i += 2 {
		m.envInputs[i].Placeholder = fmt.Sprintf("Key %d", (i/2)+1)
		m.envInputs[i+1].Placeholder = fmt.Sprintf("Value %d", (i/2)+1)
	}

	m.envInputs[0].Focus()
}

func (m *model) submitEnvForm() {
	focusedScript := &m.scripts[m.cursor]
	cronVal := strings.TrimSpace(m.envInputs[0].Value())
	focusedScript.Config.Cron = cronVal

	if len(m.envInputs) > 11 {
		notifyVal := strings.ToLower(strings.TrimSpace(m.envInputs[11].Value()))
		focusedScript.Config.Notify = notifyVal == "y" || notifyVal == "yes"
	}

	existingKeys := make([]string, 0, len(focusedScript.Config.Input))
	for k := range focusedScript.Config.Input {
		existingKeys = append(existingKeys, k)
	}
	sort.Strings(existingKeys)
	shownInForm := make(map[string]bool)
	for i, k := range existingKeys {
		if i >= 5 {
			break
		}
		shownInForm[k] = true
	}

	inputsMap := make(map[string]interface{})
	for k, v := range focusedScript.Config.Input {
		if !shownInForm[k] {
			inputsMap[k] = v
		}
	}
	for i := 1; i < 11; i += 2 {
		k := strings.TrimSpace(m.envInputs[i].Value())
		v := strings.TrimSpace(m.envInputs[i+1].Value())
		if k != "" {
			inputsMap[k] = v
		}
	}
	focusedScript.Config.Input = inputsMap

	if cronVal != "" && !isValidCron(cronVal) {
		m.statusMsg = "Error: Invalid cron format. Expected 5 space-separated fields."
		m.statusMsgTime = time.Now()
		return
	}

	for ci, sc := range m.config.Scripts {
		if sc.NameAlias == focusedScript.Config.NameAlias {
			m.config.Scripts[ci] = focusedScript.Config
			break
		}
	}
	err := SaveConfig(m.config)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error saving config: %v", err)
	} else {
		m.statusMsg = "Configuration updated successfully."
	}
	m.statusMsgTime = time.Now()
	m.activeView = "main"
}

func (m *model) deleteSelectedScript() {
	if len(m.scripts) == 0 {
		m.activeView = "main"
		return
	}
	idx := m.cursor
	script := m.scripts[idx]

	if script.Cmd != nil {
		_ = StopTask(script.Cmd)
	}

	aliasToDelete := script.Config.NameAlias
	configIdx := -1
	for ci, sc := range m.config.Scripts {
		if sc.NameAlias == aliasToDelete {
			configIdx = ci
			break
		}
	}
	if configIdx >= 0 {
		m.config.Scripts = append(m.config.Scripts[:configIdx], m.config.Scripts[configIdx+1:]...)
	}

	// Remove script from all groups
	for gi := range m.config.Groups {
		var newScripts []string
		for _, alias := range m.config.Groups[gi].Scripts {
			if alias != aliasToDelete {
				newScripts = append(newScripts, alias)
			}
		}
		m.config.Groups[gi].Scripts = newScripts
	}
	m.groups = m.config.Groups

	_ = SaveConfig(m.config)

	m.scripts = append(m.scripts[:idx], m.scripts[idx+1:]...)

	if m.cursor >= len(m.scripts) {
		m.cursor = len(m.scripts) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}

	m.activeView = "main"
	m.statusMsg = fmt.Sprintf("Successfully deleted script '%s'.", script.Config.NameAlias)
	m.statusMsgTime = time.Now()
	m.updateViewport()
}

func InterpretCarriageReturns(s string) string {
	s = strings.ReplaceAll(s, "\u001b[K", "")
	s = strings.ReplaceAll(s, "\x1b[K", "")

	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\r") {
			parts := strings.Split(line, "\r")
			lastPart := parts[len(parts)-1]
			if lastPart == "" && len(parts) > 1 {
				lastPart = parts[len(parts)-2]
			}
			lines[i] = lastPart
		}
	}
	return strings.Join(lines, "\n")
}

func prepareViewportLogs(logs string, width int) string {
	normalized := InterpretCarriageReturns(logs)
	lines := strings.Split(normalized, "\n")
	const maxViewportLines = 350
	if len(lines) > maxViewportLines {
		trimmed := lines[len(lines)-maxViewportLines:]
		trimmed = append([]string{"[truncated: showing the last 350 lines for performance]"}, trimmed...)
		lines = trimmed
		normalized = strings.Join(lines, "\n")
	}
	if width > 0 {
		normalized = lipgloss.NewStyle().Width(width).Render(normalized)
	}
	return normalized
}

func (m *model) updateViewport() {
	if len(m.scripts) == 0 {
		m.viewport.SetContent("No scripts configured.")
		return
	}
	focusedScript := m.scripts[m.cursor]
	logs := focusedScript.Logs
	if logs == "" {
		logs = lipgloss.NewStyle().Foreground(lipgloss.Color("#4a5568")).Italic(true).Render("No output yet — run the script to see logs here.")
	} else {
		logs = prepareViewportLogs(logs, m.viewport.Width)
	}
	m.viewport.SetContent(logs)
	if focusedScript.State == "Running" {
		m.viewport.GotoBottom()
	}
}

func (m *model) runTaskCmd(idx int) tea.Cmd {
	return func() tea.Msg {
		script := m.scripts[idx]
		command := script.Config.Command
		input := script.Config.Input

		if script.Config.Host != "" {
			var envParts []string
			for k, v := range input {
				envParts = append(envParts, fmt.Sprintf("%s=%s", k, fmt.Sprintf("%v", v)))
			}
			sort.Strings(envParts)
			envPrefix := ""
			if len(envParts) > 0 {
				envPrefix = strings.Join(envParts, " ") + " "
			}
			command = fmt.Sprintf("ssh -o BatchMode=yes -o StrictHostKeyChecking=no %s %s",
				script.Config.Host,
				shellQuote(envPrefix+"bash -c "+shellQuote(command)))
			input = nil
		}

		cmd, taskID, err := StartTask(script.Config.NameAlias, command, script.Config.OutputFolderPath, input)
		if err != nil {
			return TaskStartErrorMsg{ScriptNameAlias: script.Config.NameAlias, Error: err}
		}
		return TaskStartedMsg{ScriptNameAlias: script.Config.NameAlias, Cmd: cmd, TaskID: taskID}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m *model) runSelected() tea.Cmd {
	var indicesToRun []int
	for i, s := range m.scripts {
		if s.Checked {
			indicesToRun = append(indicesToRun, i)
		}
	}
	if len(indicesToRun) == 0 && len(m.scripts) > 0 {
		indicesToRun = append(indicesToRun, m.cursor)
	}
	if len(indicesToRun) == 0 {
		return nil
	}

	if m.parallelMode {
		var cmds []tea.Cmd
		for _, idx := range indicesToRun {
			m.scripts[idx].Logs = ""
			m.scripts[idx].Progress = 0
			m.scripts[idx].State = "Running"
			cmds = append(cmds, m.runTaskCmd(idx))
		}
		return tea.Batch(cmds...)
	} else {
		for _, idx := range indicesToRun {
			inQueue := false
			for _, q := range m.runQueue {
				if q == idx {
					inQueue = true
					break
				}
			}
			if !inQueue && m.runningIndex != idx && m.scripts[idx].State != "Running" {
				m.scripts[idx].Logs = ""
				m.scripts[idx].Progress = 0
				m.scripts[idx].State = "Idle"
				m.runQueue = append(m.runQueue, idx)
			}
		}
		if m.runningIndex == -1 {
			return m.runNextSequentialCmd()
		}
	}
	return nil
}

func (m *model) runNextSequentialCmd() tea.Cmd {
	if len(m.runQueue) == 0 {
		return nil
	}
	nextIdx := m.runQueue[0]
	m.runQueue = m.runQueue[1:]

	m.runningIndex = nextIdx
	m.scripts[nextIdx].Logs = ""
	m.scripts[nextIdx].Progress = 0
	m.scripts[nextIdx].State = "Running"

	return m.runTaskCmd(nextIdx)
}

func (m *model) stopSelected() {
	highlighted := &m.scripts[m.cursor]
	if highlighted.State == "Running" && highlighted.Cmd != nil {
		err := StopTask(highlighted.Cmd)
		if err != nil {
			m.statusMsg = fmt.Sprintf("Error stopping '%s': %v", highlighted.Config.NameAlias, err)
		} else {
			m.statusMsg = fmt.Sprintf("Stopped task for '%s'.", highlighted.Config.NameAlias)
		}
		m.statusMsgTime = time.Now()
		return
	}

	stoppedAny := false
	for i := range m.scripts {
		s := &m.scripts[i]
		if s.Checked && s.State == "Running" && s.Cmd != nil {
			_ = StopTask(s.Cmd)
			stoppedAny = true
		}
	}
	if stoppedAny {
		m.statusMsg = "Sent stop signal to selected running tasks."
	} else {
		m.statusMsg = "No running task found to stop."
	}
	m.statusMsgTime = time.Now()
}

func (m *model) openHTMLOutput() {
	if len(m.scripts) == 0 {
		m.statusMsg = "No scripts available to open HTML output."
		m.statusMsgTime = time.Now()
		return
	}
	script := m.scripts[m.cursor]
	folder := script.Config.OutputFolderPath

	if _, err := os.Stat(folder); os.IsNotExist(err) {
		m.statusMsg = fmt.Sprintf("Output folder '%s' does not exist.", folder)
		m.statusMsgTime = time.Now()
		return
	}

	var htmlFiles []string
	_ = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".html") {
			htmlFiles = append(htmlFiles, path)
		}
		return nil
	})

	if len(htmlFiles) == 0 {
		m.statusMsg = fmt.Sprintf("No HTML files found in '%s'.", folder)
		m.statusMsgTime = time.Now()
		return
	}

	var targetFile string
	if script.TaskID > 0 {
		taskSub1 := fmt.Sprintf("task_%d", script.TaskID)
		taskSub2 := fmt.Sprintf("_%d", script.TaskID)
		for _, path := range htmlFiles {
			base := filepath.Base(path)
			if strings.Contains(base, taskSub1) || strings.Contains(base, taskSub2) {
				targetFile = path
				break
			}
		}
	}

	if targetFile == "" {
		sort.Slice(htmlFiles, func(i, j int) bool {
			fi1, err1 := os.Stat(htmlFiles[i])
			fi2, err2 := os.Stat(htmlFiles[j])
			if err1 != nil || err2 != nil {
				return false
			}
			return fi1.ModTime().After(fi2.ModTime())
		})
		targetFile = htmlFiles[0]
	}
	absPath, err := filepath.Abs(targetFile)
	if err == nil {
		targetFile = absPath
	}

	m.statusMsg = fmt.Sprintf("Opening %s...", filepath.Base(targetFile))
	m.statusMsgTime = time.Now()

	go func() {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetFile)
		case "darwin":
			cmd = exec.Command("open", targetFile)
		default:
			cmd = exec.Command("xdg-open", targetFile)
		}
		_ = cmd.Start()
	}()
}