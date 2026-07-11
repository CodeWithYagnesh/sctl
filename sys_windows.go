//go:build windows

package main

import (
	"os/exec"
	"strconv"
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
