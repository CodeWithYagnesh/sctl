package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"gopkg.in/yaml.v3"
)

func TestGetNextTaskID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sctl_test_task_id")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Case 1: Empty directory
	if next := getNextTaskID(tmpDir); next != 1 {
		t.Errorf("expected 1, got %d", next)
	}

	// Case 2: Some task files
	_ = os.WriteFile(filepath.Join(tmpDir, "task_1.yaml"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "task_5.yaml"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "task_3.yaml"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "other.txt"), []byte(""), 0644)

	if next := getNextTaskID(tmpDir); next != 6 {
		t.Errorf("expected 6, got %d", next)
	}
}

func TestWriteTaskFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sctl_test_write_task")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	err = writeTaskFile(tmpDir, 42, "test_script", "Running", 50, "line 1\nline 2")
	if err != nil {
		t.Fatalf("failed to write task file: %v", err)
	}

	taskFilePath := filepath.Join(tmpDir, "task_42.yaml")
	data, err := os.ReadFile(taskFilePath)
	if err != nil {
		t.Fatalf("failed to read task file: %v", err)
	}

	content := string(data)

	// Verify by unmarshalling back
	var parsed TaskYAML
	err = yaml.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("failed to unmarshal written task file: %v", err)
	}

	if parsed.Task.TaskID != 42 {
		t.Errorf("expected task_id 42, got %d", parsed.Task.TaskID)
	}
	if parsed.Task.ScriptNameAlias != "test_script" {
		t.Errorf("expected script_name_alias 'test_script', got %s", parsed.Task.ScriptNameAlias)
	}
	if parsed.Task.State != "Running" {
		t.Errorf("expected state 'Running', got %s", parsed.Task.State)
	}
	if parsed.Task.Progress != 50 {
		t.Errorf("expected progress 50, got %d", parsed.Task.Progress)
	}
	if parsed.Task.Logs.Value != "line 1\nline 2" {
		t.Errorf("expected logs 'line 1\nline 2', got %q", parsed.Task.Logs.Value)
	}

	// Verify it contains the block scalar marker in the raw string
	if !strings.Contains(content, "logs: |") {
		t.Errorf("expected logs to be serialized as a block scalar (logs: |), got: %s", content)
	}
}

func TestLoadConfigDefault(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sctl_test_config")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFilePath := filepath.Join(tmpDir, "config.yaml")
	os.Setenv("SCTL_CONFIG", configFilePath)
	defer os.Unsetenv("SCTL_CONFIG")

	// Verify file does not exist initially
	if _, err := os.Stat(configFilePath); !os.IsNotExist(err) {
		t.Fatalf("config file should not exist initially")
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		t.Fatalf("expected config file to be created, but it does not exist")
	}

	if len(cfg.Scripts) == 0 {
		t.Errorf("expected default scripts, got 0")
	}

	// Verify specific default script details
	found := false
	for _, sc := range cfg.Scripts {
		if sc.NameAlias == "hello_world" {
			found = true
			if sc.OutputFolderPath != "./output" {
				t.Errorf("expected output_folder_path './output', got %s", sc.OutputFolderPath)
			}
		}
	}
	if !found {
		t.Errorf("default script 'hello_world' not found")
	}
}

func TestStartTaskExecution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sctl_test_execution")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Command outputting logs and progress markers
	command := `echo "starting job"; echo "__PROGRESS__:30"; echo "working hard"; echo "__PROGRESS__:80"; echo "done!"`

	_, taskID, err := StartTask("test_run", command, tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to start task: %v", err)
	}

	if taskID != 1 {
		t.Errorf("expected taskID 1, got %d", taskID)
	}

	// Poll the state in the task file until complete
	var parsed TaskYAML
	taskFilePath := filepath.Join(tmpDir, "task_1.yaml")

	success := false
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(taskFilePath)
		if err != nil {
			continue
		}

		var current TaskYAML
		err = yaml.Unmarshal(data, &current)
		if err == nil {
			parsed = current
			state := current.Task.State
			if state == "Success" || state == "Failed" || state == "Stopped" {
				success = true
				break
			}
		}
	}

	if !success {
		t.Fatalf("task did not complete in time, last parsed state: %s", parsed.Task.State)
	}

	if parsed.Task.State != "Success" {
		t.Errorf("expected state 'Success', got %s", parsed.Task.State)
	}
	if parsed.Task.Progress != 100 {
		t.Errorf("expected progress 100, got %d", parsed.Task.Progress)
	}

	expectedLogs := "starting job\nworking hard\ndone!"
	if parsed.Task.Logs.Value != expectedLogs {
		t.Errorf("expected logs %q, got %q", expectedLogs, parsed.Task.Logs.Value)
	}
}

func TestPrepareViewportLogs(t *testing.T) {
	logs := strings.Join([]string{"line 1", "line 2", "line 3"}, "\n")
	got := prepareViewportLogs(logs, 0)
	if got != logs {
		t.Fatalf("expected logs to stay unchanged, got %q", got)
	}

	var longLines []string
	for i := 0; i < 360; i++ {
		longLines = append(longLines, fmt.Sprintf("line %d", i))
	}
	longLogs := strings.Join(longLines, "\n")
	got = prepareViewportLogs(longLogs, 0)
	if !strings.Contains(got, "[truncated: showing the last 350 lines for performance]") {
		t.Fatalf("expected truncation notice, got %q", got)
	}
	if !strings.Contains(got, "line 359") {
		t.Fatalf("expected tail content to be preserved, got %q", got)
	}
}

func TestFormatExecutionStatus(t *testing.T) {
	startedAt := time.Now().Add(-125 * time.Second)
	got := formatExecutionStatus("Running", startedAt, time.Time{}, 3)
	if !strings.Contains(got, "RUNNING") {
		t.Fatalf("expected running status, got %q", got)
	}
	if !strings.Contains(got, "⏱") {
		t.Fatalf("expected elapsed-time marker, got %q", got)
	}
	if !strings.Contains(got, "02:05") {
		t.Fatalf("expected elapsed duration 02:05, got %q", got)
	}
}

func TestInterpretCarriageReturns(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "hello",
			expected: "hello",
		},
		{
			input:    "hello\rworld",
			expected: "world",
		},
		{
			input:    "hello\nworld",
			expected: "hello\nworld",
		},
		{
			input:    "hello\rworld\nfoo\rbar",
			expected: "world\nbar",
		},
		{
			input:    "abc\u001b[Kdef",
			expected: "abcdef",
		},
		{
			input:    "Progress: 10%\rProgress: 20%\rProgress: 30%",
			expected: "Progress: 30%",
		},
		{
			input:    "abc\r",
			expected: "abc",
		},
		{
			input:    "\rabc",
			expected: "abc",
		},
	}

	for _, tc := range tests {
		got := InterpretCarriageReturns(tc.input)
		if got != tc.expected {
			t.Errorf("InterpretCarriageReturns(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestStartTaskWithEnv(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sctl_test_env")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	command := `echo "COUNT_VAL=${count}"; echo "SOMETHING_VAL=${something_env}"`
	input := map[string]interface{}{
		"count":         2,
		"something_env": "something value",
	}

	_, taskID, err := StartTask("test_run_env", command, tmpDir, input)
	if err != nil {
		t.Fatalf("failed to start task: %v", err)
	}

	taskFilePath := filepath.Join(tmpDir, fmt.Sprintf("task_%d.yaml", taskID))

	success := false
	var parsed TaskYAML
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(taskFilePath)
		if err != nil {
			continue
		}

		var current TaskYAML
		err = yaml.Unmarshal(data, &current)
		if err == nil {
			parsed = current
			state := current.Task.State
			if state == "Success" || state == "Failed" || state == "Stopped" {
				success = true
				break
			}
		}
	}

	if !success {
		t.Fatalf("task did not complete in time, last parsed state: %s", parsed.Task.State)
	}

	if parsed.Task.State != "Success" {
		t.Errorf("expected state 'Success', got %s", parsed.Task.State)
	}

	expectedLogs := "COUNT_VAL=2\nSOMETHING_VAL=something value"
	if parsed.Task.Logs.Value != expectedLogs {
		t.Errorf("expected logs %q, got %q", expectedLogs, parsed.Task.Logs.Value)
	}
}

func TestSyncCrontab(t *testing.T) {
	cfg := &Config{
		Scripts: []ScriptConfig{
			{
				NameAlias: "cron_test_script",
				Command:   "echo hello",
				Cron:      "*/15 * * * *",
			},
		},
	}

	// Simply verify it executes without panics
	_ = SyncCrontab(cfg)

	// Clean up by passing empty config
	_ = SyncCrontab(&Config{})
}

func TestSubmitEnvForm(t *testing.T) {
	m := &model{
		cursor: 0,
		config: &Config{
			Scripts: []ScriptConfig{
				{
					NameAlias: "hello",
					Command:   "echo",
				},
			},
		},
		scripts: []ScriptState{
			{
				Config: ScriptConfig{
					NameAlias: "hello",
					Command:   "echo",
				},
			},
		},
		envInputs: make([]textinput.Model, 11),
	}

	for i := range m.envInputs {
		m.envInputs[i] = textinput.New()
	}

	m.envInputs[0].SetValue("*/10 * * * *")
	m.envInputs[1].SetValue("VAR_A")
	m.envInputs[2].SetValue("val_a")

	oldEnv := os.Getenv("SCTL_CONFIG")
	defer os.Setenv("SCTL_CONFIG", oldEnv)

	tmpFile, err := os.CreateTemp("", "sctl_test_config_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)
	os.Setenv("SCTL_CONFIG", tmpPath)

	m.submitEnvForm()

	if m.config.Scripts[0].Cron != "*/10 * * * *" {
		t.Errorf("expected Cron '*/10 * * * *', got %q", m.config.Scripts[0].Cron)
	}
	if m.config.Scripts[0].Input["VAR_A"] != "val_a" {
		t.Errorf("expected Input VAR_A = 'val_a', got %q", m.config.Scripts[0].Input["VAR_A"])
	}
}

func TestThemeCyclePersists(t *testing.T) {
	oldEnv := os.Getenv("SCTL_CONFIG")
	defer os.Setenv("SCTL_CONFIG", oldEnv)

	tmpFile, err := os.CreateTemp("", "sctl_test_theme_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)
	os.Setenv("SCTL_CONFIG", tmpPath)

	cfg := &Config{Scripts: []ScriptConfig{{NameAlias: "hello", Command: "echo hi"}}}
	m := &model{config: cfg, scripts: []ScriptState{{Config: cfg.Scripts[0]}}}

	m.cycleTheme()

	if m.config.Theme.Name != "monokai" {
		t.Fatalf("expected theme name 'monokai', got %q", m.config.Theme.Name)
	}
	if activeTheme.Accent != "#66d9ef" {
		t.Fatalf("expected accent color '#66d9ef', got %q", activeTheme.Accent)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read persisted config: %v", err)
	}
	if !strings.Contains(string(data), "name: monokai") {
		t.Fatalf("expected persisted config to include theme name, got:\n%s", string(data))
	}
}

func TestDeleteScript(t *testing.T) {
	m := &model{
		cursor: 1,
		config: &Config{
			Scripts: []ScriptConfig{
				{
					NameAlias: "hello_1",
					Command:   "echo 1",
				},
				{
					NameAlias: "hello_2",
					Command:   "echo 2",
				},
			},
		},
		scripts: []ScriptState{
			{
				Config: ScriptConfig{
					NameAlias: "hello_1",
					Command:   "echo 1",
				},
			},
			{
				Config: ScriptConfig{
					NameAlias: "hello_2",
					Command:   "echo 2",
				},
			},
		},
	}

	oldEnv := os.Getenv("SCTL_CONFIG")
	defer os.Setenv("SCTL_CONFIG", oldEnv)

	tmpFile, err := os.CreateTemp("", "sctl_test_delete_config_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)
	os.Setenv("SCTL_CONFIG", tmpPath)

	m.deleteSelectedScript()

	// Cursor should adjust down to 0
	if m.cursor != 0 {
		t.Errorf("expected cursor 0 after deleting index 1, got %d", m.cursor)
	}
	if len(m.scripts) != 1 {
		t.Errorf("expected scripts slice size 1, got %d", len(m.scripts))
	}
	if m.scripts[0].Config.NameAlias != "hello_1" {
		t.Errorf("expected remaining script to be hello_1, got %s", m.scripts[0].Config.NameAlias)
	}
}
