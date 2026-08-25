package main

import (
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

var program *tea.Program
var stoppedPIDs sync.Map

var Version = "dev"

type activePanel int

const (
	panelLeft activePanel = iota
	panelRight
)

type ScriptConfig struct {
	NameAlias        string                 `yaml:"name_alias"`
	Description      string                 `yaml:"description"`
	Command          string                 `yaml:"command"`
	OutputFolderPath string                 `yaml:"output_folder_path"`
	Input            map[string]interface{} `yaml:"input,omitempty"`
	Cron             string                 `yaml:"cron,omitempty"`
	Notify           bool                   `yaml:"notify,omitempty"`
	Host             string                 `yaml:"host,omitempty"`
}

// GroupConfig defines a collection of scripts that can be run together.
// Pipeline=true means scripts run sequentially (one by one).
// Pipeline=false means scripts run in parallel.
type GroupConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Pipeline    bool     `yaml:"pipeline"`
	Scripts     []string `yaml:"scripts"`
}

type ThemeConfig struct {
	Name    string `yaml:"name,omitempty"`
	Accent  string `yaml:"accent,omitempty"`
	Success string `yaml:"success,omitempty"`
	Fail    string `yaml:"fail,omitempty"`
	Stopped string `yaml:"stopped,omitempty"`
	Idle    string `yaml:"idle,omitempty"`
}

type Config struct {
	Scripts []ScriptConfig `yaml:"scripts"`
	Groups  []GroupConfig  `yaml:"groups,omitempty"`
	Theme   ThemeConfig    `yaml:"theme,omitempty"`
}

type TaskYAML struct {
	Task struct {
		TaskID          int       `yaml:"task_id"`
		ScriptNameAlias string    `yaml:"script_name_alias"`
		State           string    `yaml:"state"`
		Progress        int       `yaml:"progress"`
		Logs            yaml.Node `yaml:"logs"`
	} `yaml:"task"`
}

type TaskSummary struct {
	TaskID    int
	State     string
	Timestamp time.Time
	FilePath  string
}

type TaskUpdateMsg struct {
	ScriptNameAlias string
	TaskID          int
	State           string
	Progress        int
	LogLine         string
	Done            bool
	Error           error
}

type ScriptState struct {
	Config     ScriptConfig
	TaskID     int
	State      string
	Progress   int
	Logs       string
	Cmd        *exec.Cmd
	Checked    bool
	StartedAt  time.Time
	FinishedAt time.Time
}

var activeTheme ThemeConfig

type model struct {
	config               *Config
	scripts              []ScriptState
	cursor               int
	activePanel          activePanel
	parallelMode         bool
	viewport             viewport.Model
	width                int
	height               int
	runQueue             []int
	runningIndex         int
	activeView           string
	formInputs           []textinput.Model
	focusedInput         int
	envInputs            []textinput.Model
	focusedEnv           int
	confirmDeleteFocused int
	statusMsg            string
	statusMsgTime        time.Time
	historyItems         []TaskSummary
	historyCursor        int
	editingAlias         string
	filterQuery          string
	filterInput          textinput.Model
	listOffset           int
	theme                ThemeConfig

	// Group management state
	groups             []GroupConfig
	groupCursor        int
	groupListOffset    int
	groupFormInputs    []textinput.Model
	groupFocusedInput  int
	editingGroupName   string
	groupMemberCursor  int
	groupMemberChecked []bool
}

type TaskStartedMsg struct {
	ScriptNameAlias string
	Cmd             *exec.Cmd
	TaskID          int
}

type TaskStartErrorMsg struct {
	ScriptNameAlias string
	Error           error
}

// TickMsg is fired every second to animate running scripts.
type TickMsg time.Time

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var (
	focusedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#6366f1")).
			Padding(0, 1)

	unfocusedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2d3748")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#6366f1"))

	badgeRunning = lipgloss.NewStyle().
			Background(lipgloss.Color("#10b981")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	badgeSuccess = lipgloss.NewStyle().
			Background(lipgloss.Color("#059669")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	badgeFailed = lipgloss.NewStyle().
			Background(lipgloss.Color("#f43f5e")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	badgeStopped = lipgloss.NewStyle().
			Background(lipgloss.Color("#f59e0b")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)

	badgeIdle = lipgloss.NewStyle().
			Background(lipgloss.Color("#374151")).
			Foreground(lipgloss.Color("#9ca3af")).
			Padding(0, 1)
)
