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

type activePanel int

const (
	panelLeft activePanel = iota
	panelRight
)

// --- CONFIGURATION ---

type ScriptConfig struct {
	NameAlias        string                 `yaml:"name_alias"`
	Description      string                 `yaml:"description"`
	Command          string                 `yaml:"command"`
	OutputFolderPath string                 `yaml:"output_folder_path"`
	Input            map[string]interface{} `yaml:"input,omitempty"`
	Cron             string                 `yaml:"cron,omitempty"`
}

type Config struct {
	Scripts []ScriptConfig `yaml:"scripts"`
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
	_ = SyncCrontab(&cfg)
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
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

// --- TASK ENGINE ---

type TaskYAML struct {
	Task struct {
		TaskID          int       `yaml:"task_id"`
		ScriptNameAlias string    `yaml:"script_name_alias"`
		State           string    `yaml:"state"`
		Progress        int       `yaml:"progress"`
		Logs            yaml.Node `yaml:"logs"`
	} `yaml:"task"`
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

// --- TUI MODEL & VIEW ---

type ScriptState struct {
	Config   ScriptConfig
	TaskID   int
	State    string
	Progress int
	Logs     string
	Cmd      *exec.Cmd
	Checked  bool
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

var (
	focusedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#00ffd7")).
			Padding(0, 1)

	unfocusedStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3c3836")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ff007f"))

	badgeRunning = lipgloss.NewStyle().
			Background(lipgloss.Color("#00ffd7")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)

	badgeSuccess = lipgloss.NewStyle().
			Background(lipgloss.Color("#a6e22e")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)

	badgeFailed = lipgloss.NewStyle().
			Background(lipgloss.Color("#f92672")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1)

	badgeStopped = lipgloss.NewStyle().
			Background(lipgloss.Color("#f4bf75")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1)

	badgeIdle = lipgloss.NewStyle().
			Background(lipgloss.Color("#75715e")).
			Foreground(lipgloss.Color("#ffffff")).
			Padding(0, 1)
)

func initialModel() *model {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	scripts := make([]ScriptState, len(cfg.Scripts))
	for i, sc := range cfg.Scripts {
		scripts[i] = ScriptState{
			Config:   sc,
			State:    "Idle",
			Progress: 0,
			Logs:     "",
			Checked:  false,
		}
	}

	inputs := make([]textinput.Model, 5)
	for i := range inputs {
		t := textinput.New()
		t.CharLimit = 100
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
		}
		inputs[i] = t
	}
	inputs[0].Focus()

	return &model{
		config:       cfg,
		scripts:      scripts,
		cursor:       0,
		activePanel:  panelLeft,
		parallelMode: false,
		viewport:     viewport.New(0, 0),
		runningIndex: -1,
		activeView:   "main",
		formInputs:   inputs,
		focusedInput: 0,
		statusMsg:    "Welcome to sctl! Select a script and press 'R' to run.",
	}
}

type TickMsg time.Time

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
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftWidth := int(float64(m.width) * 0.4)
		rightWidth := m.width - leftWidth

		headerHeight := m.getHeaderHeight()
		panelHeight := m.height - (headerHeight + 4)
		if panelHeight < 5 {
			panelHeight = 5
		}

		m.viewport.Width = rightWidth - 4
		vHeight := panelHeight - 5
		if vHeight < 1 {
			vHeight = 1
		}
		m.viewport.Height = vHeight
		m.updateViewport()

	case TaskStartedMsg:
		for i := range m.scripts {
			if m.scripts[i].Config.NameAlias == msg.ScriptNameAlias {
				m.scripts[i].Cmd = msg.Cmd
				m.scripts[i].TaskID = msg.TaskID
				m.scripts[i].State = "Running"
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
					if msg.Error != nil && msg.State != "Stopped" {
						m.statusMsg = fmt.Sprintf("Script '%s' failed: %v", msg.ScriptNameAlias, msg.Error)
					} else if msg.State == "Stopped" {
						m.statusMsg = fmt.Sprintf("Script '%s' was stopped.", msg.ScriptNameAlias)
					} else {
						m.statusMsg = fmt.Sprintf("Script '%s' finished successfully.", msg.ScriptNameAlias)
					}
					m.statusMsgTime = time.Now()
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
		case "a":
			m.activeView = "form"
			m.focusedInput = 0
			for i := range m.formInputs {
				m.formInputs[i].SetValue("")
			}
			m.formInputs[0].Focus()
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
		case "o":
			m.openHTMLOutput()
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
				if m.cursor > 0 {
					m.cursor--
					m.updateViewport()
				}
			} else {
				m.viewport.LineUp(1)
			}
			return m, nil
		case "down", "j":
			if m.activePanel == panelLeft {
				if m.cursor < len(m.scripts)-1 {
					m.cursor++
					m.updateViewport()
				}
			} else {
				m.viewport.LineDown(1)
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.activeView = "main"
		return m, nil
	case "tab", "down":
		if m.focusedInput < 6 {
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput++
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Focus()
			}
		} else {
			m.focusedInput = 0
			m.formInputs[0].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.focusedInput > 0 {
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput--
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Focus()
			}
		} else {
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Blur()
			}
			m.focusedInput = 6
		}
		return m, nil
	case "enter":
		if m.focusedInput == 5 {
			m.submitForm()
		} else if m.focusedInput == 6 {
			m.activeView = "main"
		} else {
			m.formInputs[m.focusedInput].Blur()
			m.focusedInput++
			if m.focusedInput < 5 {
				m.formInputs[m.focusedInput].Focus()
			}
		}
		return m, nil
	}
	if m.focusedInput < 5 {
		var cmd tea.Cmd
		m.formInputs[m.focusedInput], cmd = m.formInputs[m.focusedInput].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) submitForm() {
	alias := strings.TrimSpace(m.formInputs[0].Value())
	desc := strings.TrimSpace(m.formInputs[1].Value())
	cmdStr := strings.TrimSpace(m.formInputs[2].Value())
	outputPath := strings.TrimSpace(m.formInputs[3].Value())
	cronStr := strings.TrimSpace(m.formInputs[4].Value())

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
	}

	m.config.Scripts = append(m.config.Scripts, newConfig)
	err := SaveConfig(m.config)
	if err != nil {
		m.statusMsg = fmt.Sprintf("Error saving config: %v", err)
	} else {
		m.statusMsg = fmt.Sprintf("Successfully added script '%s'.", alias)
	}
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
		if m.focusedEnv < 12 {
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv++
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Focus()
			}
		} else {
			m.focusedEnv = 0
			m.envInputs[0].Focus()
		}
		return m, nil
	case "shift+tab", "up":
		if m.focusedEnv > 0 {
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv--
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Focus()
			}
		} else {
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Blur()
			}
			m.focusedEnv = 12
		}
		return m, nil
	case "enter":
		if m.focusedEnv == 11 {
			m.submitEnvForm()
		} else if m.focusedEnv == 12 {
			m.activeView = "main"
		} else {
			m.envInputs[m.focusedEnv].Blur()
			m.focusedEnv++
			if m.focusedEnv < 11 {
				m.envInputs[m.focusedEnv].Focus()
			}
		}
		return m, nil
	}
	if m.focusedEnv < 11 {
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
	m.envInputs = make([]textinput.Model, 11)
	for i := range m.envInputs {
		m.envInputs[i] = textinput.New()
		m.envInputs[i].CharLimit = 100
		m.envInputs[i].Width = 40
	}
	m.envInputs[0].Placeholder = "e.g., */5 * * * *"

	focusedScript := m.scripts[m.cursor]
	m.envInputs[0].SetValue(focusedScript.Config.Cron)

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

	inputsMap := make(map[string]interface{})
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

	m.config.Scripts[m.cursor] = focusedScript.Config
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
	for _, p := range parts {
		for _, r := range p {
			if !((r >= '0' && r <= '9') || r == '*' || r == '/' || r == '-' || r == ',' || r == '?') {
				return false
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

	m.config.Scripts = append(m.config.Scripts[:idx], m.config.Scripts[idx+1:]...)
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

func (m *model) getHeaderHeight() int {
	if m.height >= 32 && m.width >= 105 {
		return 12
	}
	if m.height >= 25 && m.width >= 80 {
		return 5
	}
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
		logs = lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("No output logs. Run the script to see output.")
	} else {
		logs = InterpretCarriageReturns(logs)
	}
	m.viewport.SetContent(logs)
	if focusedScript.State == "Running" {
		m.viewport.GotoBottom()
	}
}

func (m *model) runTaskCmd(idx int) tea.Cmd {
	return func() tea.Msg {
		script := m.scripts[idx]
		cmd, taskID, err := StartTask(script.Config.NameAlias, script.Config.Command, script.Config.OutputFolderPath, script.Config.Input)
		if err != nil {
			return TaskStartErrorMsg{ScriptNameAlias: script.Config.NameAlias, Error: err}
		}
		return TaskStartedMsg{ScriptNameAlias: script.Config.NameAlias, Cmd: cmd, TaskID: taskID}
	}
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

	sort.Slice(htmlFiles, func(i, j int) bool {
		fi1, err1 := os.Stat(htmlFiles[i])
		fi2, err2 := os.Stat(htmlFiles[j])
		if err1 != nil || err2 != nil {
			return false
		}
		return fi1.ModTime().After(fi2.ModTime())
	})

	targetFile := htmlFiles[0]
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

	filledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7"))
	if !running {
		filledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e"))
	}
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2c2c2c"))

	filled := strings.Repeat("█", filledLen)
	empty := strings.Repeat("░", emptyLen)
	return filledStyle.Render(filled) + emptyStyle.Render(empty)
}

func (m *model) renderLeftPanel(width, height int) string {
	var s strings.Builder
	borderStyle := unfocusedStyle
	if m.activePanel == panelLeft {
		borderStyle = focusedStyle
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff007f")).Render(" 📑 SCRIPTS CONTROL ")
	s.WriteString(header + "\n\n")

	contentHeight := height - 5
	if contentHeight < 1 {
		contentHeight = 1
	}

	for i, script := range m.scripts {
		cursor := "  "
		if i == m.cursor {
			cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff007f")).Render("▶ ")
		}

		chk := "[ ]"
		if script.Checked {
			chk = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Render("[✔]")
		}

		nameStr := script.Config.NameAlias
		if i == m.cursor {
			nameStr = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Render(nameStr)
		} else {
			nameStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#c1c1c1")).Render(nameStr)
		}

		badge := ""
		switch script.State {
		case "Running":
			badge = badgeRunning.Render("RUNNING")
		case "Success":
			badge = badgeSuccess.Render("SUCCESS")
		case "Failed":
			badge = badgeFailed.Render("FAILED")
		case "Stopped":
			badge = badgeStopped.Render("STOPPED")
		default:
			badge = badgeIdle.Render("IDLE")
		}

		progPercent := ""
		if script.State != "Idle" {
			progPercent = fmt.Sprintf(" %d%%", script.Progress)
		}

		// Available width inside the card container (width - 8)
		availableWidth := width - 8
		if availableWidth < 10 {
			availableWidth = 10
		}

		line1 := fmt.Sprintf("%s%s %s", cursor, chk, nameStr)
		line1Len := lipgloss.Width(line1)
		badgeLen := lipgloss.Width(badge) + lipgloss.Width(progPercent)

		spaces := availableWidth - line1Len - badgeLen
		if spaces < 1 {
			spaces = 1
		}
		line1Final := line1 + strings.Repeat(" ", spaces) + badge + progPercent

		pBar := drawProgressBar(12, float64(script.Progress)/100.0, script.State == "Running")
		descStr := script.Config.Description
		descWidth := availableWidth - 16
		if descWidth < 0 {
			descWidth = 0
		}
		if descWidth > 3 && len(descStr) > descWidth {
			descStr = descStr[:descWidth-3] + "..."
		}
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Width(descWidth).MaxHeight(1)

		line2 := fmt.Sprintf("  %s  %s", pBar, descStyle.Render(descStr))

		cardContent := line1Final + "\n" + line2

		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			Width(width - 8)

		if i == m.cursor {
			cardStyle = cardStyle.BorderForeground(lipgloss.Color("#ff007f"))
		} else {
			cardStyle = cardStyle.BorderForeground(lipgloss.Color("#3c3836"))
		}

		s.WriteString(cardStyle.Render(cardContent) + "\n")
	}

	linesWritten := strings.Count(s.String(), "\n")
	for i := linesWritten; i < contentHeight; i++ {
		s.WriteString("\n")
	}
	return borderStyle.Width(width - 4).Height(height - 2).Render(s.String())
}

func (m *model) renderRightPanel(width, height int) string {
	borderStyle := unfocusedStyle
	if m.activePanel == panelRight {
		borderStyle = focusedStyle
	}

	if len(m.scripts) == 0 {
		return borderStyle.Width(width - 4).Height(height - 2).Render("No scripts configured.")
	}

	focusedScript := m.scripts[m.cursor]
	headerText := fmt.Sprintf(" 💻 OUTPUT: %s ", focusedScript.Config.NameAlias)
	if focusedScript.TaskID > 0 {
		headerText = fmt.Sprintf(" 💻 OUTPUT: %s (Task #%d) ", focusedScript.Config.NameAlias, focusedScript.TaskID)
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff007f")).Render(headerText)

	scrollPercent := m.viewport.ScrollPercent()
	scrollText := fmt.Sprintf(" %d%% ", int(scrollPercent*100))
	if scrollPercent <= 0 {
		scrollText = " Top "
	} else if scrollPercent >= 1.0 {
		scrollText = " Bottom "
	}
	scrollIndicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render(scrollText)

	topBar := header
	spaces := width - 4 - lipgloss.Width(header) - lipgloss.Width(scrollIndicator)
	if spaces > 0 {
		topBar += strings.Repeat(" ", spaces) + scrollIndicator
	}

	content := m.viewport.View()
	return borderStyle.Width(width - 4).Height(height - 2).Render(topBar + "\n\n" + content)
}

func (m *model) renderBottomBar(width int) string {
	statusText := ""
	if m.statusMsg != "" && (m.statusMsgTime.IsZero() || time.Since(m.statusMsgTime) < 8*time.Second) {
		statusText = lipgloss.NewStyle().
			Background(lipgloss.Color("#e6db74")).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 1).
			Render(" STATUS ") + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#e6db74")).Render(m.statusMsg)
	}

	parallelStr := ""
	if m.parallelMode {
		parallelStr = lipgloss.NewStyle().Background(lipgloss.Color("#ae81ff")).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1).Render("⚡ PARALLEL")
	} else {
		parallelStr = lipgloss.NewStyle().Background(lipgloss.Color("#3e3d32")).Foreground(lipgloss.Color("#f8f8f2")).Padding(0, 1).Render("⚙ SEQUENTIAL")
	}

	border := lipgloss.NewStyle().Foreground(lipgloss.Color("#2c2c2c")).Render(strings.Repeat("─", width))

	keyStyle := lipgloss.NewStyle().Background(lipgloss.Color("#3c3836")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Padding(0, 1)

	items := []string{
		keyStyle.Render("R") + descStyle.Render("Run"),
		keyStyle.Render("S") + descStyle.Render("Stop"),
		keyStyle.Render("A") + descStyle.Render("Add"),
		keyStyle.Render("Enter") + descStyle.Render("Edit"),
		keyStyle.Render("D/Del") + descStyle.Render("Delete"),
		keyStyle.Render("P") + descStyle.Render("Parallel"),
		keyStyle.Render("O") + descStyle.Render("Open HTML"),
		keyStyle.Render("Tab") + descStyle.Render("Switch"),
		keyStyle.Render("Q") + descStyle.Render("Quit"),
	}
	legend := strings.Join(items, " ")

	firstLine := lipgloss.JoinHorizontal(lipgloss.Center, parallelStr, "  ", statusText)
	return border + "\n" + firstLine + "\n" + legend
}

func (m *model) renderFramedBox(titleText string, titleColor string, borderColor string, innerContent []string, boxWidth int) string {
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(borderColor))

	titleLen := len(titleText)
	dashesCount := boxWidth - 3 - titleLen
	if dashesCount < 1 {
		dashesCount = 1
	}

	topBorder := "┌─"
	if titleText != "" {
		topBorder += lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(titleColor)).Render(titleText)
	}
	topBorder += strings.Repeat("─", dashesCount) + "┐"

	var s strings.Builder
	s.WriteString(borderStyle.Render(topBorder) + "\n")

	contentWidth := boxWidth - 4 // border + spacing
	for _, line := range innerContent {
		w := lipgloss.Width(line)
		var paddedLine string
		if w < contentWidth {
			paddedLine = "  " + line + strings.Repeat(" ", contentWidth-w) + "  "
		} else {
			paddedLine = "  " + line + "  "
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
	inner = append(inner, "")

	for i, input := range m.formInputs {
		label := ""
		switch i {
		case 0:
			label = "Script Name Alias (uniquely identifies the script):"
		case 1:
			label = "Description:"
		case 2:
			label = "Command (shell command to run):"
		case 3:
			label = "Output Folder Path:"
		case 4:
			label = "Cron Expression (e.g., */5 * * * * - optional):"
		}
		inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render(label))

		inputStr := input.View()
		var styledInput string
		if i == m.focusedInput {
			styledInput = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("#00ffd7")).
				Width(50).
				Render(inputStr)
		} else {
			styledInput = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("#3e3d32")).
				Width(50).
				Render(inputStr)
		}
		inputLines := strings.Split(styledInput, "\n")
		for _, line := range inputLines {
			if line != "" {
				inner = append(inner, line)
			}
		}
		inner = append(inner, "")
	}

	saveBtn := " [ Save ] "
	cancelBtn := " [ Cancel ] "
	if m.focusedInput == 5 {
		saveBtn = lipgloss.NewStyle().Background(lipgloss.Color("#00ffd7")).Foreground(lipgloss.Color("#000000")).Bold(true).Render(" [ Save ] ")
	} else if m.focusedInput == 6 {
		cancelBtn = lipgloss.NewStyle().Background(lipgloss.Color("#ff007f")).Foreground(lipgloss.Color("#000000")).Bold(true).Render(" [ Cancel ] ")
	}

	inner = append(inner, fmt.Sprintf("             %s     %s", saveBtn, cancelBtn))
	inner = append(inner, "")

	return m.renderFramedBox(" Add New Script ", "#ff007f", "#ff007f", inner, 60)
}

func (m *model) renderEnvForm() string {
	var inner []string
	inner = append(inner, "")

	focusedScript := m.scripts[m.cursor]
	titleLabel := fmt.Sprintf("Editing Config for: %s", focusedScript.Config.NameAlias)
	inner = append(inner, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00ffd7")).Render(titleLabel))
	inner = append(inner, "")

	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("Cron Expression (e.g., */5 * * * *):"))
	cronStr := m.envInputs[0].View()
	var styledCron string
	if m.focusedEnv == 0 {
		styledCron = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#00ffd7")).Width(50).Render(cronStr)
	} else {
		styledCron = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#3e3d32")).Width(50).Render(cronStr)
	}
	for _, line := range strings.Split(styledCron, "\n") {
		if line != "" {
			inner = append(inner, line)
		}
	}
	inner = append(inner, "")

	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("Environment Variables (up to 5 key-value pairs):"))

	for i := 1; i < 11; i += 2 {
		keyInput := m.envInputs[i].View()
		valInput := m.envInputs[i+1].View()

		var styledKey, styledVal string
		if m.focusedEnv == i {
			styledKey = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#00ffd7")).Width(22).Render(keyInput)
		} else {
			styledKey = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#3e3d32")).Width(22).Render(keyInput)
		}

		if m.focusedEnv == i+1 {
			styledVal = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#00ffd7")).Width(22).Render(valInput)
		} else {
			styledVal = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#3e3d32")).Width(22).Render(valInput)
		}

		keyLines := strings.Split(styledKey, "\n")
		valLines := strings.Split(styledVal, "\n")

		for j := 0; j < len(keyLines); j++ {
			if j < len(valLines) && keyLines[j] != "" && valLines[j] != "" {
				inner = append(inner, fmt.Sprintf("%s   %s", keyLines[j], valLines[j]))
			}
		}
	}
	inner = append(inner, "")

	saveBtn := " [ Save ] "
	cancelBtn := " [ Cancel ] "
	if m.focusedEnv == 11 {
		saveBtn = lipgloss.NewStyle().Background(lipgloss.Color("#00ffd7")).Foreground(lipgloss.Color("#000000")).Bold(true).Render(" [ Save ] ")
	} else if m.focusedEnv == 12 {
		cancelBtn = lipgloss.NewStyle().Background(lipgloss.Color("#ff007f")).Foreground(lipgloss.Color("#000000")).Bold(true).Render(" [ Cancel ] ")
	}

	inner = append(inner, fmt.Sprintf("             %s     %s", saveBtn, cancelBtn))
	inner = append(inner, "")

	return m.renderFramedBox(" Edit Script Environment & Schedule ", "#00ffd7", "#00ffd7", inner, 60)
}

func (m *model) renderDeleteConfirm() string {
	var inner []string
	inner = append(inner, "")

	focusedScript := m.scripts[m.cursor]
	warningMsg := fmt.Sprintf("Are you sure you want to delete script '%s'?", focusedScript.Config.NameAlias)
	inner = append(inner, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff007f")).Render(warningMsg))
	inner = append(inner, "")
	inner = append(inner, lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("This action cannot be undone and will update crontab."))
	inner = append(inner, "")

	cancelBtn := " [ Cancel ] "
	deleteBtn := " [ Delete ] "
	if m.confirmDeleteFocused == 0 {
		cancelBtn = lipgloss.NewStyle().Background(lipgloss.Color("#75715e")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Render(" [ Cancel ] ")
	} else if m.confirmDeleteFocused == 1 {
		deleteBtn = lipgloss.NewStyle().Background(lipgloss.Color("#f92672")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Render(" [ Delete ] ")
	}

	inner = append(inner, fmt.Sprintf("         %s         %s", cancelBtn, deleteBtn))
	inner = append(inner, "")

	return m.renderFramedBox(" Delete Confirmation ", "#f92672", "#f92672", inner, 50)
}

func getLargeLogo() string {
	logoLines := []string{
		"  ██████  ▄████▄  ▄▄▄█████▓ ██▓    ",
		"▒██    ▒ ▒██▀ ▀█  ▓  ██▒ ▓▒▓██▒    ",
		"░ ▓██▄   ▒▓█    ▄ ▒ ▓██░ ▒░▒██░    ",
		"  ▒   ██▒▒▓▓▄ ▄██▒░ ▓██▓ ░ ▒██░    ",
		"▒██████▒▒▒ ▓███▀ ░  ▒██▒ ░ ░██████▒",
		"▒ ▒▓▒ ▒ ░░ ░▒ ▒  ░  ▒ ░░   ░ ▒░▓  ░",
		"░ ░▒  ░ ░  ░  ▒       ░    ░ ░ ▒  ░",
		"░  ░  ░  ░          ░        ░ ░   ",
		"      ░  ░ ░                   ░  ░",
		"         ░                         ",
	}
	colors := []string{
		"#ff007f", // Neon Pink
		"#eb1097",
		"#d720af",
		"#c330c7",
		"#af40df",
		"#9b50f7", // Purple
		"#707eff", // Blue-Purple
		"#45acff", // Blue
		"#1ad9ff", // Cyan
		"#00ffd7", // Neon Teal
	}

	var sb strings.Builder
	for i, line := range logoLines {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors[i%len(colors)]))
		sb.WriteString(style.Render(line) + "\n")
	}
	return sb.String()
}

func (m *model) renderStats(width, height int) string {
	runningCount := 0
	successCount := 0
	failedCount := 0
	stoppedCount := 0
	idleCount := 0
	for _, s := range m.scripts {
		switch s.State {
		case "Running":
			runningCount++
		case "Success":
			successCount++
		case "Failed":
			failedCount++
		case "Stopped":
			stoppedCount++
		default:
			idleCount++
		}
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00ffd7"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Bold(true)

	runBadge := badgeRunning.Render(fmt.Sprintf("%d RUNNING", runningCount))
	successBadge := badgeSuccess.Render(fmt.Sprintf("%d OK", successCount))
	failBadge := badgeFailed.Render(fmt.Sprintf("%d FAIL", failedCount))

	var modeStr string
	if m.parallelMode {
		modeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#ae81ff")).Bold(true).Render("PARALLEL")
	} else {
		modeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#f4bf75")).Bold(true).Render("SEQUENTIAL")
	}

	configPath := GetConfigPath()
	if len(configPath) > 20 {
		configPath = "..." + configPath[len(configPath)-17:]
	}

	content := fmt.Sprintf(
		"  %s\n\n"+
			"  %s %s\n"+
			"  %s %s\n"+
			"  %s %s\n"+
			"  %s %s %s\n"+
			"  %s %s",
		headerStyle.Render("📊 SCRIPT DASHBOARD"),
		labelStyle.Render("Mode:   "), modeStr,
		labelStyle.Render("Config: "), valueStyle.Render(configPath),
		labelStyle.Render("Scripts:"), valueStyle.Render(fmt.Sprintf("%d loaded", len(m.scripts))),
		labelStyle.Render("Tasks:  "), runBadge, successBadge,
		labelStyle.Render("        "), failBadge,
	)

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3c3836")).
		Padding(0, 1).
		Width(width).
		Height(height)

	return borderStyle.Render(content)
}

func (m *model) renderMediumHeader(width int) string {
	logo := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff007f")).Render("█▀█ █▀▄ █▀ █▀▀ ▀█▀ █  ") + "\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Render("█▄█ █▄▀ ▄█ █▄▄  █  █▄▄")

	runningCount := 0
	for _, s := range m.scripts {
		if s.State == "Running" {
			runningCount++
		}
	}

	activeStr := ""
	if runningCount > 0 {
		activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Bold(true).Render(fmt.Sprintf("● RUNNING: %d", runningCount))
	} else {
		activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("○ IDLE")
	}

	modeStr := ""
	if m.parallelMode {
		modeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#ae81ff")).Bold(true).Render("PARALLEL")
	} else {
		modeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#f4bf75")).Bold(true).Render("SEQUENTIAL")
	}

	info := fmt.Sprintf(
		"⚡ %s  |  Mode: %s  |  %s",
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")).Render("SCRIPT RUNNER"),
		modeStr,
		activeStr,
	)

	spaces := width - 22 - lipgloss.Width(info) - 4
	if spaces < 1 {
		spaces = 1
	}

	rightSide := strings.Repeat(" ", spaces) + info

	logoLines := strings.Split(logo, "\n")

	headerLine1 := logoLines[0] + rightSide
	headerLine2 := logoLines[1] + strings.Repeat(" ", spaces) + lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("Config: "+GetConfigPath())

	border := lipgloss.NewStyle().Foreground(lipgloss.Color("#2c2c2c")).Render(strings.Repeat("━", width))

	return "\n  " + headerLine1 + "\n  " + headerLine2 + "\n" + border
}

func (m *model) renderCompactHeader(width int) string {
	titleStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#ff007f")).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 1)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00ffd7")).
		Bold(true)

	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#75715e"))

	leftText := titleStyle.Render("⚡ SCTL") + " " + subtitleStyle.Render("SCRIPT RUNNER")

	runningCount := 0
	for _, s := range m.scripts {
		if s.State == "Running" {
			runningCount++
		}
	}

	activeStr := ""
	if runningCount > 0 {
		activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Bold(true).Render(fmt.Sprintf("● RUNNING: %d", runningCount))
	} else {
		activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("#75715e")).Render("○ IDLE")
	}

	rightText := fmt.Sprintf("%s | %s", time.Now().Format("15:04:05"), activeStr)
	rightTextFormatted := metaStyle.Render(rightText)

	leftLen := lipgloss.Width(leftText)
	rightLen := lipgloss.Width(rightTextFormatted)

	spaces := width - leftLen - rightLen - 4
	if spaces < 1 {
		spaces = 1
	}

	headerContent := "  " + leftText + strings.Repeat(" ", spaces) + rightTextFormatted + "  "
	border := lipgloss.NewStyle().Foreground(lipgloss.Color("#2c2c2c")).Render(strings.Repeat("─", width))

	return "\n" + headerContent + "\n" + border
}

func (m *model) renderHeader(width int) string {
	if m.height >= 32 && m.width >= 105 {
		logo := getLargeLogo()
		stats := m.renderStats(45, 9)

		combined := lipgloss.JoinHorizontal(lipgloss.Top, logo, "      ", stats)
		border := lipgloss.NewStyle().Foreground(lipgloss.Color("#2c2c2c")).Render(strings.Repeat("━", width))
		return "\n" + combined + "\n" + border
	}
	if m.height >= 25 && m.width >= 80 {
		return m.renderMediumHeader(width)
	}
	return m.renderCompactHeader(width)
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

	header := m.renderHeader(m.width)

	leftWidth := int(float64(m.width) * 0.4)
	rightWidth := m.width - leftWidth

	headerHeight := m.getHeaderHeight()
	panelHeight := m.height - (headerHeight + 4)

	leftPanel := m.renderLeftPanel(leftWidth, panelHeight)
	rightPanel := m.renderRightPanel(rightWidth, panelHeight)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	bottomBar := m.renderBottomBar(m.width)
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
			target = &s
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

	for {
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

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Wait()
	}

	if !success {
		return fmt.Errorf("task finished with state: %s", lastState)
	}
	return nil
}

func main() {
	if len(os.Args) >= 3 && (os.Args[1] == "--run" || os.Args[1] == "-run") {
		alias := os.Args[2]
		if err := runHeadless(alias); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	program = p
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running program: %v", err)
	}
}
