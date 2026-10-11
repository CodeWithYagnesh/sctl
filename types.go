package main

import (
	"os/exec"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	Timeout          string                 `yaml:"timeout,omitempty"`
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

type ProgressBarConfig struct {
	Completed string `yaml:"completed,omitempty"`
	Pending   string `yaml:"pending,omitempty"`
}

type Config struct {
	Scripts     []ScriptConfig    `yaml:"scripts"`
	Groups      []GroupConfig     `yaml:"groups,omitempty"`
	Theme       ThemeConfig       `yaml:"theme,omitempty"`
	ProgressBar ProgressBarConfig `yaml:"progressbar,omitempty"`
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
	// CPUPercent / MemMB are the most recent process-metric sample for this
	// script's running command (0 when not running). CPUHistory / MemHistory
	// keep a short rolling window of recent samples for the sparkline in the
	// "progress + metrics" panel; both are capped at metricsHistoryLen entries.
	CPUPercent float64
	MemMB      float64
	CPUHistory []float64
	MemHistory []float64
}

// metricsHistoryLen bounds how many CPU/mem samples are kept per script (at
// one sample/second via TickMsg, 60 entries is a 1-minute rolling window).
const metricsHistoryLen = 60

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
	groupActivePanel   activePanel
	groupScriptCursor  int
	groupDetailScroll  int

	// Cleanup popup state
	cleanupMode      bool
	cleanupAlias     string
	cleanupOlderDays int
	cleanupInput     textinput.Model
	cleanupModeAll   bool

	// Settings popup state
	settingsMode   bool
	settingsInputs []textinput.Model

	// ui holds the state that exists only for the redesigned interface
	// (tabs, follow mode, async history cache, help scroll, ...). Its zero
	// value is valid.
	ui uiState
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
