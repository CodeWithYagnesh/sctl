//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
)

func prepareCmd(cmd *exec.Cmd) {
	// No process group configuration needed on Windows
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// On Windows, use taskkill to kill the process and all of its descendants.
	// This is analogous to killing a Unix process group.
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	err := killCmd.Run()
	if err != nil {
		// Fallback to standard process kill if taskkill fails
		return cmd.Process.Kill()
	}
	return nil
}

func isSignaledStopped(err error) bool {
	// Signal concepts are not directly applicable on Windows in the same way.
	// Handled via stoppedPIDs map.
	return false
}

// sampleProcessMetrics on Windows shells out to WMIC to read working-set memory
// for the PID. CPU% sampling is not implemented here (WMIC's PercentProcessorTime
// requires a delta query pattern that is slow to shell out for every tick), so
// cpuPercent is always returned as 0 on Windows for now. memMB is real.
func sampleProcessMetrics(pid int) (cpuPercent float64, memMB float64, ok bool) {
	out, err := exec.Command("wmic", "process", "where", "ProcessId="+strconv.Itoa(pid), "get", "WorkingSetSize").Output()
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Fields(string(out))
	for _, tok := range lines {
		if v, perr := strconv.ParseFloat(strings.TrimSpace(tok), 64); perr == nil && v > 0 {
			return 0, v / (1024 * 1024), true
		}
	}
	return 0, 0, false
}

// clearProcessMetricsSample is a no-op on Windows since sampleProcessMetrics
// does not keep cross-call CPU sampling state here.
func clearProcessMetricsSample(pid int) {}
