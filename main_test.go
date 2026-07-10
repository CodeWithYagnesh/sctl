package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestGetNextTaskID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "obsctl_test_task_id")
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
	tmpDir, err := os.MkdirTemp("", "obsctl_test_write_task")
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
	tmpDir, err := os.MkdirTemp("", "obsctl_test_config")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configFilePath := filepath.Join(tmpDir, "config.yaml")
	os.Setenv("OBSCTL_CONFIG", configFilePath)
	defer os.Unsetenv("OBSCTL_CONFIG")

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
		if sc.NameAlias == "tls_verification" {
			found = true
			if sc.OutputFolderPath != "/home/yagnesh/work/kube/project/cronjob/tlsoutput" {
				t.Errorf("expected output_folder_path '/home/yagnesh/work/kube/project/cronjob/tlsoutput', got %s", sc.OutputFolderPath)
			}
		}
	}
	if !found {
		t.Errorf("default script 'tls_verification' not found")
	}
}

func TestStartTaskExecution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "obsctl_test_execution")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Command outputting logs and progress markers
	command := `echo "starting job"; echo "__PROGRESS__:30"; echo "working hard"; echo "__PROGRESS__:80"; echo "done!"`
	
	_, taskID, err := StartTask("test_run", command, tmpDir)
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
