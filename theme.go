package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var themePresets = []ThemeConfig{
	{Name: "default", Accent: "#6366f1", Success: "#10b981", Fail: "#f43f5e", Stopped: "#f59e0b", Idle: "#374151"},
	{Name: "monokai", Accent: "#66d9ef", Success: "#a6e22e", Fail: "#f92672", Stopped: "#fd971f", Idle: "#49483e"},
	{Name: "catppuccin", Accent: "#89b4fa", Success: "#a6e3a1", Fail: "#f38ba8", Stopped: "#fab387", Idle: "#585b70"},
	{Name: "nord", Accent: "#81a1c1", Success: "#a3be8c", Fail: "#bf616a", Stopped: "#ebcb8b", Idle: "#4c566a"},
	{Name: "cyberpunk", Accent: "#00f0ff", Success: "#39ff14", Fail: "#ff0055", Stopped: "#ffaa00", Idle: "#1a0b2e"},
	{Name: "matrix", Accent: "#00ff41", Success: "#00cc33", Fail: "#ff3333", Stopped: "#ffcc00", Idle: "#0d1117"},
	{Name: "synthwave", Accent: "#ff00cc", Success: "#00ff99", Fail: "#ff3366", Stopped: "#ffcc00", Idle: "#1a0a2e"},
	{Name: "midnight", Accent: "#7c83fd", Success: "#96f7d6", Fail: "#ff6b6b", Stopped: "#ffd93d", Idle: "#151b2e"},
	{Name: "obsidian", Accent: "#ff9f43", Success: "#2ed573", Fail: "#ff4757", Stopped: "#ffa502", Idle: "#1e272e"},
	{Name: "aurora", Accent: "#00d2d3", Success: "#55efc4", Fail: "#ff7675", Stopped: "#fdcb6e", Idle: "#1b2838"},
	{Name: "void", Accent: "#a855f7", Success: "#22c55e", Fail: "#ef4444", Stopped: "#f59e0b", Idle: "#09090b"},
	{Name: "tokyo", Accent: "#38bdf8", Success: "#34d399", Fail: "#fb7185", Stopped: "#fbbf24", Idle: "#0f172a"},
	{Name: "plasma", Accent: "#d946ef", Success: "#10b981", Fail: "#f43f5e", Stopped: "#f59e0b", Idle: "#18181b"},
	{Name: "horizon", Accent: "#f472b6", Success: "#34d399", Fail: "#f87171", Stopped: "#fbbf24", Idle: "#0f172a"},
	{Name: "circuit", Accent: "#fbbf24", Success: "#4ade80", Fail: "#f87171", Stopped: "#f59e0b", Idle: "#1c2f2b"},
	{Name: "nebula", Accent: "#c084fc", Success: "#6ee7b7", Fail: "#fca5a5", Stopped: "#fcd34d", Idle: "#0f0a1a"},
}

func normalizeTheme(theme ThemeConfig) ThemeConfig {
	if theme.Name == "" {
		theme.Name = "default"
	}
	var base ThemeConfig
	for _, preset := range themePresets {
		if preset.Name == theme.Name {
			base = preset
			break
		}
	}
	if base.Name == "" {
		base = themePresets[0]
		base.Name = theme.Name
	}
	if theme.Accent != "" {
		base.Accent = theme.Accent
	}
	if theme.Success != "" {
		base.Success = theme.Success
	}
	if theme.Fail != "" {
		base.Fail = theme.Fail
	}
	if theme.Stopped != "" {
		base.Stopped = theme.Stopped
	}
	if theme.Idle != "" {
		base.Idle = theme.Idle
	}
	return base
}

func (m *model) applyTheme() {
	m.config.Theme = normalizeTheme(m.config.Theme)
	activeTheme = m.config.Theme

	focusedStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(activeTheme.Accent)).
		Padding(0, 1)

	unfocusedStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(activeTheme.Idle)).
		Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(activeTheme.Accent))

	badgeRunning = lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Accent)).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true).
		Padding(0, 1)

	badgeSuccess = lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Success)).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true).
		Padding(0, 1)

	badgeFailed = lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Fail)).
		Foreground(lipgloss.Color("#ffffff")).
		Bold(true).
		Padding(0, 1)

	badgeStopped = lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Stopped)).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 1)

	badgeIdle = lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme.Idle)).
		Foreground(lipgloss.Color("#9ca3af")).
		Padding(0, 1)
}

func (m *model) cycleTheme() {
	currentName := strings.ToLower(m.config.Theme.Name)
	idx := 0
	for i, preset := range themePresets {
		if strings.ToLower(preset.Name) == currentName {
			idx = i
			break
		}
	}
	next := (idx + 1) % len(themePresets)
	m.config.Theme = themePresets[next]
	m.applyTheme()
	_ = SaveConfig(m.config)
	m.statusMsg = fmt.Sprintf("Theme switched to %s.", strings.Title(m.config.Theme.Name)) //nolint:staticcheck
	m.statusMsgTime = time.Now()
}
