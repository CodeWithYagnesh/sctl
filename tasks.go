package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

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

// taskIDMutexes provides per-output-folder locking for atomic task ID allocation.
// Keyed by folder path, created on demand.
var taskIDMutexes sync.Map

func getFolderMutex(folderPath string) *sync.Mutex {
	mu, _ := taskIDMutexes.LoadOrStore(folderPath, &sync.Mutex{})
	return mu.(*sync.Mutex)
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
	// Hold per-folder mutex across ID allocation AND initial write to prevent races
	mu := getFolderMutex(folderPath)
	mu.Lock()
	taskID := getNextTaskID(folderPath)
	cmd := exec.Command("bash", "-c", command)
	prepareCmd(cmd)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("SCTL_TASK_ID=%d", taskID),
		"PYTHONUNBUFFERED=1",
		"FORCE_COLOR=1",
		"TERM=xterm-256color",
	)
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
		mu.Unlock()
		return nil, 0, err
	}
	mu.Unlock()

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
		var lineBuf strings.Builder
		var lastWasCR bool

		emitLine := func(s string) {
			s = strings.TrimSuffix(s, "\r")
			processLine(s)
		}

		for {
			b, err := reader.ReadByte()
			if err != nil {
				if lineBuf.Len() > 0 {
					emitLine(lineBuf.String())
					lineBuf.Reset()
				}
				break
			}

			if b == '\r' {
				if lineBuf.Len() > 0 {
					emitLine(lineBuf.String())
					lineBuf.Reset()
				}
				lastWasCR = true
			} else if b == '\n' {
				if !lastWasCR {
					emitLine(lineBuf.String())
					lineBuf.Reset()
				}
				lastWasCR = false
			} else {
				lastWasCR = false
				lineBuf.WriteByte(b)
			}
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
