package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
	// Host, if non-empty, causes the command to run remotely via SSH.
	// Format: user@hostname or hostname (uses your default SSH key/config).
	Host string `yaml:"host,omitempty"`
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

func GetConfigPath() string {
	path := os.Getenv("SCTL_CONFIG")
	if path == "" {
		path = "config.yaml"
	}
	return path
}

func LoadConfig() (*Config, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			defaultConfig := Config{
				Scripts: []ScriptConfig{
					{
						NameAlias:        "hello_world",
						Description:      "Print a friendly greeting",
						Command:          `echo "Hello, World!"`,
						OutputFolderPath: "./output",
					},
				},
			}
			err = SaveConfig(&defaultConfig)
			if err != nil {
				return nil, err
			}
			return &defaultConfig, nil
		}
		return nil, err
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	cfg.Theme = normalizeTheme(cfg.Theme)
	_ = SyncCrontab(&cfg)
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	cfg.Theme = normalizeTheme(cfg.Theme)
	path := GetConfigPath()
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	_ = SyncCrontab(cfg)
	return nil
}

func SyncCrontab(cfg *Config) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}

	cmd := exec.Command("crontab", "-l")
	var existingLines []string
	out, err := cmd.Output()
	if err == nil {
		rawLines := strings.Split(string(out), "\n")
		for _, line := range rawLines {
			existingLines = append(existingLines, strings.TrimRight(line, "\r\n"))
		}
		if len(existingLines) > 0 && existingLines[len(existingLines)-1] == "" {
			existingLines = existingLines[:len(existingLines)-1]
		}
	}

	var newLines []string
	skipNext := false
	for _, line := range existingLines {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(line, "# SCTL SCHEDULED JOB: ") {
			skipNext = true
			continue
		}
		newLines = append(newLines, line)
	}

	for _, script := range cfg.Scripts {
		cronExpr := strings.TrimSpace(script.Cron)
		if cronExpr != "" {
			newLines = append(newLines, fmt.Sprintf("# SCTL SCHEDULED JOB: %s", script.NameAlias))
			newLines = append(newLines, fmt.Sprintf("%s %s --run %q >/dev/null 2>&1", cronExpr, executable, script.NameAlias))
		}
	}

	if len(newLines) == 0 {
		rmCmd := exec.Command("crontab", "-r")
		_ = rmCmd.Run()
		return nil
	}

	tmpFile, err := os.CreateTemp("", "sctl_cron")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	writer := bufio.NewWriter(tmpFile)
	for _, line := range newLines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	installCmd := exec.Command("crontab", tmpFile.Name())
	return installCmd.Run()
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

func getTaskHistory(folderPath string) ([]TaskSummary, error) {
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		return nil, nil
	}
	files, err := os.ReadDir(folderPath)
	if err != nil {
		return nil, err
	}
	var history []TaskSummary
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasPrefix(name, "task_") && strings.HasSuffix(name, ".yaml") {
			var id int
			_, err := fmt.Sscanf(name, "task_%d.yaml", &id)
			if err == nil {
				filePath := filepath.Join(folderPath, name)
				info, err := file.Info()
				timestamp := time.Time{}
				if err == nil {
					timestamp = info.ModTime()
				}

				var state string
				data, err := os.ReadFile(filePath)
				if err == nil {
					var taskFile TaskYAML
					if err := yaml.Unmarshal(data, &taskFile); err == nil {
						state = taskFile.Task.State
					}
				}
				if state == "" {
					state = "Unknown"
				}

				history = append(history, TaskSummary{
					TaskID:    id,
					State:     state,
					Timestamp: timestamp,
					FilePath:  filePath,
				})
			}
		}
	}
	sort.Slice(history, func(i, j int) bool {
		return history[i].TaskID > history[j].TaskID
	})
	return history, nil
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

func getNextTaskID(folderPath string) int {
	if _, err := os.Stat(folderPath); os.IsNotExist(err) {
		return 1
	}
	files, err := os.ReadDir(folderPath)
	if err != nil {
		return 1
	}
	maxID := 0
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasPrefix(name, "task_") && strings.HasSuffix(name, ".yaml") {
			var id int
			_, err := fmt.Sscanf(name, "task_%d.yaml", &id)
			if err == nil && id > maxID {
				maxID = id
			}
		}
	}
	return maxID + 1
}

func writeTaskFile(folderPath string, taskID int, nameAlias string, state string, progress int, logs string) error {
	var taskFile TaskYAML
	taskFile.Task.TaskID = taskID
	taskFile.Task.ScriptNameAlias = nameAlias
	taskFile.Task.State = state
	taskFile.Task.Progress = progress
	taskFile.Task.Logs = yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: logs,
		Style: yaml.LiteralStyle,
	}
	data, err := yaml.Marshal(&taskFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		return err
	}
	filename := filepath.Join(folderPath, fmt.Sprintf("task_%d.yaml", taskID))
	return os.WriteFile(filename, data, 0644)
}

func StartTask(nameAlias string, command string, folderPath string, input map[string]interface{}) (*exec.Cmd, int, error) {
	taskID := getNextTaskID(folderPath)
	cmd := exec.Command("bash", "-c", command)
	prepareCmd(cmd)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("SCTL_TASK_ID=%d", taskID))
	for k, v := range input {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", k, v))
	}

	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w

	initialLogs := ""
	err := writeTaskFile(folderPath, taskID, nameAlias, "Running", 0, initialLogs)
	if err != nil {
		w.Close()
		r.Close()
		return nil, 0, err
	}

	if program != nil {
		program.Send(TaskUpdateMsg{
			ScriptNameAlias: nameAlias,
			TaskID:          taskID,
			State:           "Running",
			Progress:        0,
			LogLine:         "",
			Done:            false,
		})
	}

	var accumulatedLogs strings.Builder
	currentProgress := 0

	processLine := func(line string) {
		if strings.HasPrefix(line, "__PROGRESS__:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				var p int
				_, err := fmt.Sscanf(parts[1], "%d", &p)
				if err == nil {
					if p < 0 {
						p = 0
					}
					if p > 100 {
						p = 100
					}
					currentProgress = p
					_ = writeTaskFile(folderPath, taskID, nameAlias, "Running", currentProgress, accumulatedLogs.String())
					if program != nil {
						program.Send(TaskUpdateMsg{
							ScriptNameAlias: nameAlias,
							TaskID:          taskID,
							State:           "Running",
							Progress:        currentProgress,
							LogLine:         "",
							Done:            false,
						})
					}
				}
			}
			return
		}

		if accumulatedLogs.Len() > 0 {
			accumulatedLogs.WriteString("\n")
		}
		accumulatedLogs.WriteString(line)
		_ = writeTaskFile(folderPath, taskID, nameAlias, "Running", currentProgress, accumulatedLogs.String())
		if program != nil {
			program.Send(TaskUpdateMsg{
				ScriptNameAlias: nameAlias,
				TaskID:          taskID,
				State:           "Running",
				Progress:        currentProgress,
				LogLine:         line,
				Done:            false,
			})
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		reader := bufio.NewReader(r)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if len(line) > 0 {
					line = strings.TrimSuffix(line, "\n")
					line = strings.TrimSuffix(line, "\r")
					processLine(line)
				}
				break
			}
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			processLine(line)
		}
		r.Close()
	}()

	err = cmd.Start()
	if err != nil {
		w.Close()
		r.Close()
		return nil, 0, err
	}

	go func() {
		waitErr := cmd.Wait()
		w.Close()
		wg.Wait()

		state := "Success"
		var finalErr error
		if waitErr != nil {
			pid := cmd.Process.Pid
			if _, stopped := stoppedPIDs.LoadAndDelete(pid); stopped {
				state = "Stopped"
			} else if isSignaledStopped(waitErr) {
				state = "Stopped"
			} else {
				state = "Failed"
			}
			finalErr = waitErr
		} else {
			currentProgress = 100
		}

		_ = writeTaskFile(folderPath, taskID, nameAlias, state, currentProgress, accumulatedLogs.String())
		if program != nil {
			program.Send(TaskUpdateMsg{
				ScriptNameAlias: nameAlias,
				TaskID:          taskID,
				State:           state,
				Progress:        currentProgress,
				LogLine:         "",
				Done:            true,
				Error:           finalErr,
			})
		}
	}()

	return cmd, taskID, nil
}

func StopTask(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	stoppedPIDs.Store(cmd.Process.Pid, true)
	return killProcessGroup(cmd)
}

// sendNotification delivers a desktop notification when a task finishes.
// It always rings the terminal bell, then attempts a native OS notification.
func sendNotification(scriptName, body, state string) {
	// Terminal bell — works in every terminal emulator.
	fmt.Fprint(os.Stderr, "\a")

	title := "sctl — " + scriptName
	switch state {
	case "Success":
		title += " ✓"
	case "Failed":
		title += " ✕"
	case "Stopped":
		title += " ⊘"
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		cmd = exec.Command("osascript", "-e", script)
	case "windows":
		ps := fmt.Sprintf(
			`[System.Windows.Forms.MessageBox]::Show('%s','%s')`,
			strings.ReplaceAll(body, "'", ""),
			strings.ReplaceAll(title, "'", ""),
		)
		cmd = exec.Command("powershell", "-Command", ps)
	default: // Linux / BSD
		cmd = exec.Command("notify-send", "--app-name=sctl", title, body)
	}
	_ = cmd.Run()
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
	m.statusMsg = fmt.Sprintf("Theme switched to %s.", strings.Title(m.config.Theme.Name))
	m.statusMsgTime = time.Now()
}

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
	// editingAlias holds the NameAlias of the script being edited.
	// Empty string means the form is in "Add" mode; non-empty means "Edit" mode.
	editingAlias string
	// filterQuery is the current live search string; empty means no filter.
	filterQuery string
	filterInput textinput.Model
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

// --- Professional Color Palette ---
// Accent: Indigo (#6366f1), Emerald (#10b981), Rose (#f43f5e), Amber (#f59e0b)
// Neutrals: Slate dark (#0f1117), border dim (#2d3748), border bright (#4a5568)
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

	m := &model{
		config:        cfg,
		scripts:       scripts,
		cursor:        0,
		activePanel:   panelLeft,
		parallelMode:  false,
		viewport:      viewport.New(0, 0),
		runningIndex:  -1,
		activeView:    "main",
		formInputs:    inputs,
		focusedInput:  0,
		statusMsg:     "Welcome to sctl! Select a script and press 'R' to run.",
		statusMsgTime: time.Time{},
		theme:         cfg.Theme,
	}
	m.applyTheme()
	return m
}

type TickMsg time.Time

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
		leftWidth := int(float64(m.width) * 0.4)
		rightWidth := m.width - leftWidth

		m.viewport.Width = rightWidth - 4
		m.updateViewport()

	case TaskStartedMsg:
		for i := range m.scripts {
			if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
				m.scripts[i].Cmd = msg.Cmd
				m.scripts[i].TaskID = msg.TaskID
				m.scripts[i].State = "Running"
				m.scripts[i].StartedAt = time.Now()
				m.scripts[i].FinishedAt = time.Time{}
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
					// Feature #4: fire desktop notification if enabled for this script.
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

		// ── Filter mode: route all keys through the filter input ──────────
		if m.activeView == "filter" {
			return m.updateFilterView(msg)
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
			// Add mode — clear the form and open it.
			m.editingAlias = ""
			m.activeView = "form"
			m.focusedInput = 0
			for i := range m.formInputs {
				m.formInputs[i].SetValue("")
			}
			m.formInputs[0].Focus()
			return m, nil
		case "e":
			// Edit mode — pre-populate the form with the selected script's values.
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
			// Enter filter/search mode.
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
				// find position of current cursor in filtered list, move up
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
			// Move the selected script one position up in both lists and persist.
			if len(m.scripts) > 1 && m.cursor > 0 {
				m.reorderScript(m.cursor, m.cursor-1)
				m.cursor--
				m.updateViewport()
			}
			return m, nil
		case "ctrl+down":
			// Move the selected script one position down in both lists and persist.
			if len(m.scripts) > 1 && m.cursor < len(m.scripts)-1 {
				m.reorderScript(m.cursor, m.cursor+1)
				m.cursor++
				m.updateViewport()
			}
			return m, nil
		}
	}
	return m, nil
}

// reorderScript swaps scripts at positions i and j in both m.scripts and
// m.config.Scripts, then persists the new order to config.yaml immediately.
func (m *model) reorderScript(i, j int) {
	if i < 0 || j < 0 || i >= len(m.scripts) || j >= len(m.scripts) {
		return
	}
	m.scripts[i], m.scripts[j] = m.scripts[j], m.scripts[i]
	// Keep m.config.Scripts in sync by alias — safer than assuming same index.
	aliasI := m.scripts[j].Config.NameAlias // after swap, j now holds the original i
	aliasJ := m.scripts[i].Config.NameAlias // after swap, i now holds the original j
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

// filteredIndices returns the list of m.scripts indices that match filterQuery.
// If filterQuery is empty every index is returned (no filtering).
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

// updateFilterView handles key events while the inline filter bar is open.
func (m *model) updateFilterView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Clear filter and return to main view.
		m.filterQuery = ""
		m.activeView = "main"
		return m, nil
	case "enter":
		// Confirm filter — stay on main with filter applied.
		m.activeView = "main"
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterQuery = m.filterInput.Value()
	// Snap cursor to first matching script.
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
	// Slots 0-5 are text inputs; 6=Save, 7=Cancel
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

	// ── EDIT mode ──────────────────────────────────────────────────────────
	if m.editingAlias != "" {
		// Alias rename: ensure the new alias isn't taken by a different script.
		if alias != m.editingAlias {
			for _, s := range m.scripts {
				if s.Config.NameAlias == alias {
					m.statusMsg = fmt.Sprintf("Error: Alias '%s' is already used by another script.", alias)
					m.statusMsgTime = time.Now()
					return
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
		// Update m.config.Scripts by the original alias.
		for ci, sc := range m.config.Scripts {
			if sc.NameAlias == m.editingAlias {
				updatedConfig.Input = sc.Input // preserve existing env vars
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
		// Update m.scripts in place.
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

	// ── ADD mode ───────────────────────────────────────────────────────────
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
	// Focus slots: 0=cron, 1-10=env pairs, 11=notify, 12=Save, 13=Cancel
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
	// Slot 0: cron. Slots 1-10: 5 key/value pairs. Slot 11 (new): notify toggle.
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

	// Notify toggle (slot 11): 'y' enables, anything else disables.
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

	// Sync by alias to stay safe if indices drift.
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

func isValidCron(cronStr string) bool {
	if cronStr == "" {
		return true
	}
	parts := strings.Fields(cronStr)
	if len(parts) != 5 {
		return false
	}

	maxVals := []int{59, 23, 31, 12, 7}
	for fi, p := range parts {
		if p == "*" {
			continue
		}

		base := p
		if idx := strings.Index(p, "/"); idx >= 0 {
			stepStr := p[idx+1:]
			base = p[:idx]
			for _, r := range stepStr {
				if r < '0' || r > '9' {
					return false
				}
			}
		}
		if base == "*" || base == "" {
			continue
		}

		for _, seg := range strings.Split(base, ",") {
			bounds := strings.Split(seg, "-")
			if len(bounds) > 2 {
				return false
			}
			for _, b := range bounds {
				var n int
				if _, err := fmt.Sscanf(b, "%d", &n); err != nil {
					return false
				}
				if n < 0 || n > maxVals[fi] {
					return false
				}
			}
		}
	}
	return true
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

func (m *model) getHeaderHeight() int {
	return 3
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
			// Feature #10: wrap the command for remote SSH execution.
			// Inline env vars as a shell prefix so the remote shell picks them up.
			var envParts []string
			for k, v := range input {
				envParts = append(envParts, fmt.Sprintf("%s=%s", k, fmt.Sprintf("%v", v)))
			}
			sort.Strings(envParts) // deterministic order
			envPrefix := ""
			if len(envParts) > 0 {
				envPrefix = strings.Join(envParts, " ") + " "
			}
			// Run via ssh; BatchMode=yes fails fast if key auth is unavailable.
			command = fmt.Sprintf("ssh -o BatchMode=yes -o StrictHostKeyChecking=no %s %s",
				script.Config.Host,
				shellQuote(envPrefix+"bash -c "+shellQuote(command)))
			input = nil // env vars already inlined above
		}

		cmd, taskID, err := StartTask(script.Config.NameAlias, command, script.Config.OutputFolderPath, input)
		if err != nil {
			return TaskStartErrorMsg{ScriptNameAlias: script.Config.NameAlias, Error: err}
		}
		return TaskStartedMsg{ScriptNameAlias: script.Config.NameAlias, Cmd: cmd, TaskID: taskID}
	}
}

// shellQuote wraps s in single quotes, escaping any single quotes within.
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

func (m *model) renderLeftPanel(width, height int) string {
	var s strings.Builder
	borderStyle := unfocusedStyle
	if m.activePanel == panelLeft {
		borderStyle = focusedStyle
	}

	// ── Section header ───────────────────────────────────────────
	titleTxt := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#e2e8f0")).Render("SCRIPTS")
	indices := m.filteredIndices()
	countStr := fmt.Sprintf("%d", len(m.scripts))
	if m.filterQuery != "" {
		countStr = fmt.Sprintf("%d/%d", len(indices), len(m.scripts))
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
	availW := width - 8
	sp := availW - lipgloss.Width(titleLeft) - lipgloss.Width(hint)
	if sp < 1 {
		sp = 1
	}
	s.WriteString(titleLeft + strings.Repeat(" ", sp) + hint + "\n")

	// ── Filter input bar (only shown while activeView == "filter") ─
	if m.activeView == "filter" {
		barStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#6366f1")).
			Width(availW - 2)
		prompt := lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("/") + " "
		for _, l := range strings.Split(barStyle.Render(prompt+m.filterInput.View()), "\n") {
			if l != "" {
				s.WriteString(l + "\n")
			}
		}
	}

	divider := lipgloss.NewStyle().Foreground(lipgloss.Color("#1e293b")).Render(strings.Repeat("─", availW))
	s.WriteString(divider + "\n")

	// ── Script cards ─────────────────────────────────────────────
	if len(m.scripts) == 0 {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render("  No scripts configured. Press A to add one.") + "\n")
	} else if len(indices) == 0 && m.filterQuery != "" {
		s.WriteString("\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#475569")).Italic(true).
			Render(fmt.Sprintf("  No match for %q", m.filterQuery)) + "\n")
	}

	// Build a quick lookup so we can skip non-matching scripts.
	visible := make(map[int]bool, len(indices))
	for _, idx := range indices {
		visible[idx] = true
	}

	for i, script := range m.scripts {
		if !visible[i] {
			continue
		}
		isSelected := i == m.cursor

		// Row 1 — name + status badge
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

		// Row 2 — progress bar with % label
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

		// Row 3 — description + optional SSH badge
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

	// ── Breadcrumb header ──────────────────────────────────────────
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
	availW := width - 4
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
	// ── Status row (only shown when a message is active) ──────────────────
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

	// ── Keybinding legend row ──────────────────────────────────────────
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
	// Center-render a modal box with rounded border
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(1, 2).
		Width(boxWidth)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(titleColor))

	var sb strings.Builder
	if titleText != "" {
		sb.WriteString(titleStyle.Render(titleText) + "\n")
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

func (m *model) renderCustomFramedBox(titleText string, titleColor string, borderColor string, innerContent []string, boxWidth int) string {
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor))

	titleLen := lipgloss.Width(titleText)
	dashesCount := boxWidth - 3 - titleLen
	if dashesCount < 1 {
		dashesCount = 1
	}

	topBorderStart := borderStyle.Render("┌─")
	var topBorderTitle string
	if titleText != "" {
		topBorderTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(titleColor)).Render(titleText)
	}
	topBorderEnd := borderStyle.Render(strings.Repeat("─", dashesCount) + "┐")
	topBorderLine := topBorderStart + topBorderTitle + topBorderEnd

	var s strings.Builder
	s.WriteString(topBorderLine + "\n")

	contentWidth := boxWidth - 4
	for _, line := range innerContent {
		w := lipgloss.Width(line)
		var paddedLine string
		if w < contentWidth {
			paddedLine = " " + line + strings.Repeat(" ", contentWidth-w) + " "
		} else {
			paddedLine = " " + line + " "
		}
		s.WriteString(borderStyle.Render("│") + paddedLine + borderStyle.Render("│") + "\n")
	}

	bottomBorder := "└" + strings.Repeat("─", boxWidth-2) + "┘"
	s.WriteString(borderStyle.Render(bottomBorder) + "\n")

	boxStr := s.String()
	boxHeight := strings.Count(boxStr, "\n")
	padTop := (m.height - boxHeight) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (m.width - boxWidth) / 2
	if padLeft < 0 {
		padLeft = 0
	}

	var output strings.Builder
	for i := 0; i < padTop; i++ {
		output.WriteString("\n")
	}
	for _, line := range strings.Split(boxStr, "\n") {
		if line == "" {
			continue
		}
		output.WriteString(strings.Repeat(" ", padLeft) + line + "\n")
	}
	return output.String()
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

	// Cron field
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

	// Env vars
	inner = append(inner, labelStyle.Render("Environment variables  (up to 5 key=value pairs)"))
	inner = append(inner, "")
	for i := 1; i < 11; i += 2 {
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

	// Notify toggle (slot 11)
	inner = append(inner, labelStyle.Render("Desktop notification on complete  (y / n)"))
	notifyBorder := blurBorder
	if m.focusedEnv == 11 {
		notifyBorder = focusedBorder
	}
	styledNotify := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(notifyBorder).Width(10).Render(m.envInputs[11].View())
	for _, l := range strings.Split(styledNotify, "\n") {
		if l != "" {
			inner = append(inner, l)
		}
	}
	inner = append(inner, "")

	saveBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Save")
	cancelBg := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Idle)).Foreground(lipgloss.Color("#475569")).Padding(0, 2).Render("Cancel")
	if m.focusedEnv == 12 {
		saveBg = lipgloss.NewStyle().Background(lipgloss.Color(activeTheme.Accent)).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 2).Render("Save")
	}
	if m.focusedEnv == 13 {
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
	// Brand pill
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

	// Live stats
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

	// Mode chip
	modeTxt, modeFg := "SEQUENTIAL", "#f59e0b"
	if m.parallelMode {
		modeTxt, modeFg = "PARALLEL", "#818cf8"
	}
	modeChip := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e293b")).
		Foreground(lipgloss.Color(modeFg)).
		Bold(true).Padding(0, 1).Render(modeTxt)

	// Config + clock
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

	// Sync viewport height to the correct inner height
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

func runHeadless(alias string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %v", err)
	}

	var target *ScriptConfig
	for _, s := range cfg.Scripts {
		if s.NameAlias == alias {

			sc := s
			target = &sc
			break
		}
	}
	if target == nil {
		return fmt.Errorf("script alias %q not found", alias)
	}

	cmd, taskID, err := StartTask(target.NameAlias, target.Command, target.OutputFolderPath, target.Input)
	if err != nil {
		return fmt.Errorf("failed to start task: %v", err)
	}

	taskFilePath := filepath.Join(target.OutputFolderPath, fmt.Sprintf("task_%d.yaml", taskID))

	success := false
	var lastState string
	printedOffset := 0

	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		data, err := os.ReadFile(taskFilePath)
		if err != nil {
			continue
		}

		var current TaskYAML
		err = yaml.Unmarshal(data, &current)
		if err != nil {
			continue
		}

		lastState = current.Task.State

		logsVal := current.Task.Logs.Value
		if len(logsVal) > printedOffset {
			fmt.Print(logsVal[printedOffset:])
			printedOffset = len(logsVal)
		}

		if lastState == "Success" {
			success = true
			break
		}
		if lastState == "Failed" || lastState == "Stopped" {
			break
		}
	}
	if time.Now().After(deadline) {
		return fmt.Errorf("timed out waiting for task '%s' to complete", alias)
	}

	_ = cmd

	if !success {
		return fmt.Errorf("task finished with state: %s", lastState)
	}
	return nil
}

func printHelp() {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff007f")).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ae81ff")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	boldStyle := lipgloss.NewStyle().Bold(true)

	versionBadge := lipgloss.NewStyle().
		Background(lipgloss.Color("#00ffd7")).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 1).
		Render(Version)

	authorLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#75715e")).
		Render("by github/codewithyagnesh")

	fmt.Println()
	fmt.Print(lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("  SCTL — Script Controller") + "\n")
	fmt.Println()
	fmt.Printf("  %s  %s  %s\n", titleStyle.Render("sctl (Script Controller)"), versionBadge, authorLabel)
	fmt.Printf("  %s\n", descStyle.Render("Modern, elegant task automation dashboard & scheduler"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" USAGE:"))
	fmt.Println("   sctl [flags]")
	fmt.Println()

	fmt.Println(headerStyle.Render(" FLAGS:"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--run, -run <alias>"), descStyle.Render("Execute the specified script in headless mode with real-time logging"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--version, -v"), descStyle.Render("Print version and author information"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--help, -h"), descStyle.Render("Show this help message and exit"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" TUI KEYBOARD SHORTCUTS:"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("k / ↑"), descStyle.Render("Move selection up"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("j / ↓"), descStyle.Render("Move selection down"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Tab"), descStyle.Render("Switch focus between Left (Scripts) and Right (Logs) panels"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Space"), descStyle.Render("Select / check script for multi-selection execution"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("r"), descStyle.Render("Run the selected (or checked) script(s)"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("s"), descStyle.Render("Force stop the currently running process"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("a"), descStyle.Render("Create a new script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Enter"), descStyle.Render("Edit schedule and environment variables for the selected script"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("d / Delete"), descStyle.Render("Remove the selected script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("h / H"), descStyle.Render("View task execution history and load past logs"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("p"), descStyle.Render("Toggle parallel execution mode (concurrently or sequentially)"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("o"), descStyle.Render("Open the latest HTML report/output in system default browser"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("[ / ] or PgUp/PgDn"), descStyle.Render("Scroll logs viewport up / down"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("q / Ctrl+C"), descStyle.Render("Quit the application"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" ENVIRONMENT VARIABLES:"))
	fmt.Printf("   %-25s %s\n", boldStyle.Render("SCTL_CONFIG"), descStyle.Render("Custom path to config.yaml (defaults to current directory)"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" REAL-TIME PROGRESS INTEGRATION:"))
	fmt.Println(descStyle.Render("   To update the TUI progress bar from your custom script, print a line prefixed with"))
	fmt.Println("   " + boldStyle.Render("__PROGRESS__:<integer_0_to_100>") + descStyle.Render(" to standard output."))
	fmt.Println()
}

func main() {
	if len(os.Args) != 1 {
		if len(os.Args) >= 3 && (os.Args[1] == "--run" || os.Args[1] == "-run") {
			alias := os.Args[2]
			if err := runHeadless(alias); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		if len(os.Args) >= 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
			fmt.Printf("sctl version: %s\n", Version)
			fmt.Printf("GitHub: github/codewithyagnesh\n")
			os.Exit(0)
		}
		if len(os.Args) >= 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			printHelp()
			os.Exit(0)
		}
		printHelp()
		os.Exit(0)
	} else {
		p := tea.NewProgram(initialModel(), tea.WithAltScreen())
		program = p
		if _, err := p.Run(); err != nil {
			log.Fatalf("Error running program: %v", err)
		}
	}
}
