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

var dashboardUpdateSignal = make(chan struct{}, 1)
var dashboardSubscribers = make(map[chan struct{}]struct{})
var dashboardSubscriberMu sync.Mutex

var Version = "dev"

type activePanel int

const (
	panelLeft activePanel = iota
	panelRight
)

type ScriptConfig struct {
	NameAlias        string                 `yaml:"name_alias" json:"name_alias"`
	Description      string                 `yaml:"description" json:"description"`
	Command          string                 `yaml:"command" json:"command"`
	OutputFolderPath string                 `yaml:"output_folder_path" json:"output_folder_path"`
	Input            map[string]interface{} `yaml:"input,omitempty" json:"input,omitempty"`
	Cron             string                 `yaml:"cron,omitempty" json:"cron,omitempty"`
	Notify           bool                   `yaml:"notify,omitempty" json:"notify,omitempty"`
	// Host, if non-empty, causes the command to run remotely via SSH.
	// Format: user@hostname or hostname (uses your default SSH key/config).
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
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
	runQueue             []string // stores NameAlias instead of slice indices to survive reordering
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
	// editingAlias holds the NameAlias of the script being edited.
	// Empty string means the form is in "Add" mode; non-empty means "Edit" mode.
	editingAlias string
	// filterQuery is the current live search string; empty means no filter.
	filterQuery string
	filterInput textinput.Model
	// listOffset is the top visible index for script card list windowing in renderLeftPanel.
	listOffset  int
	theme       ThemeConfig
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

// --- Professional Color Palette ---
// Accent: Indigo (#6366f1), Emerald (#10b981), Rose (#f43f5e), Amber (#f59e0b)
// Neutrals: Slate dark (#0f1117), border dim (#2d3748), border bright (#4a5568)
// Form field counts - defined once to avoid magic number duplication across model.go and ui.go
const (
	formFieldCount      = 6 // alias, description, command, output, cron, host
	formSubmitIdx       = 6 // submit button index
	formCancelIdx       = 7 // cancel button index
	formTotalFocusables = 8 // formFieldCount + submit + cancel

	envBaseFieldCount = 2 // cron + notify
	envMaxExtraPairs  = 10 // max extra key/value pairs (20 inputs)
	envFieldCount     = envBaseFieldCount + envMaxExtraPairs*2
	envSubmitIdx      = envFieldCount
	envCancelIdx      = envFieldCount + 1
	envTotalFocusables = envFieldCount + 2
)

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

	// Running: bright emerald-green
	badgeRunning = lipgloss.NewStyle().
			Background(lipgloss.Color("#10b981")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	// Success: muted green-teal
	badgeSuccess = lipgloss.NewStyle().
			Background(lipgloss.Color("#059669")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	// Failed: rose-red
	badgeFailed = lipgloss.NewStyle().
			Background(lipgloss.Color("#f43f5e")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	// Stopped: amber-orange
	badgeStopped = lipgloss.NewStyle().
			Background(lipgloss.Color("#f59e0b")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)

	// Idle: muted slate
	badgeIdle = lipgloss.NewStyle().
			Background(lipgloss.Color("#374151")).
			Foreground(lipgloss.Color("#9ca3af")).
			Padding(0, 1)
)
