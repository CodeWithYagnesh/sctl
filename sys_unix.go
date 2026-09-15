//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func isSignaledStopped(err error) bool {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signaled() && status.Signal() == syscall.SIGKILL
		}
	}
	return false
}

// ─── process metrics (Linux, via /proc) ─────────────────────────────────────
// prevCPUSample tracks the last (utime+stime, wallclock) reading per PID so we
// can compute a CPU percentage between two samples, the same way `top` does.
var prevCPUSample sync.Map // map[int]cpuSample

type cpuSample struct {
	totalTicks uint64
	at         time.Time
}

var clockTicksPerSec = float64(100) // overridden lazily via getconf if available

func init() {
	if out, err := exec.Command("getconf", "CLK_TCK").Output(); err == nil {
		if v, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); perr == nil && v > 0 {
			clockTicksPerSec = v
		}
	}
}

// sampleProcessMetrics reads /proc/<pid>/stat and /proc/<pid>/status to compute
// approximate CPU% (relative to one core, may exceed 100 for multi-threaded
// work) and resident memory in MB for the given PID. Returns ok=false if the
// process no longer exists or cannot be read (e.g. already exited).
func sampleProcessMetrics(pid int) (cpuPercent float64, memMB float64, ok bool) {
	statPath := "/proc/" + strconv.Itoa(pid) + "/stat"
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, 0, false
	}
	// Fields are space-separated; the comm field (2nd) is parenthesized and may
	// itself contain spaces, so split after the last ')'.
	s := string(data)
	closeParen := strings.LastIndex(s, ")")
	if closeParen == -1 || closeParen+2 >= len(s) {
		return 0, 0, false
	}
	fields := strings.Fields(s[closeParen+2:])
	// After splitting off "pid (comm) ", fields[0] is state (3rd overall field),
	// so utime is fields[11] and stime is fields[12] (0-indexed from state).
	if len(fields) < 15 {
		return 0, 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	totalTicks := utime + stime
	now := time.Now()

	cpuPercent = 0
	if prev, exists := prevCPUSample.Load(pid); exists {
		p := prev.(cpuSample)
		elapsed := now.Sub(p.at).Seconds()
		if elapsed > 0 && totalTicks >= p.totalTicks {
			deltaTicks := float64(totalTicks - p.totalTicks)
			cpuPercent = (deltaTicks / clockTicksPerSec) / elapsed * 100.0
		}
	}
	prevCPUSample.Store(pid, cpuSample{totalTicks: totalTicks, at: now})

	statusPath := "/proc/" + strconv.Itoa(pid) + "/status"
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		return cpuPercent, 0, true
	}
	for _, line := range strings.Split(string(statusData), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				kb, perr := strconv.ParseFloat(parts[1], 64)
				if perr == nil {
					memMB = kb / 1024.0
				}
			}
			break
		}
	}
	return cpuPercent, memMB, true
}

// clearProcessMetricsSample removes stored CPU-sampling state for a PID once
// its task finishes, so a future PID reuse doesn't inherit stale tick counts.
func clearProcessMetricsSample(pid int) {
	prevCPUSample.Delete(pid)
}
