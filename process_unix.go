//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

type claudeProcess struct {
	PID, Parent           int
	Executable, StartTime string
}
type claudeRunningError struct{ Processes []claudeProcess }

func (e *claudeRunningError) Error() string {
	return fmt.Sprintf("%d Claude process(es) are still running; fully quit Desktop and its workers (first PID: %d)", len(e.Processes), e.Processes[0].PID)
}
func processGuard() error {
	ps, e := listClaudeProcesses()
	if e != nil {
		return e
	}
	if len(ps) > 0 {
		return &claudeRunningError{Processes: ps}
	}
	return nil
}
func signalClaude(p claudeProcess, sig os.Signal) error {
	// FindProcess uses a pidfd on supported Linux kernels, pinning the process.
	handle, e := os.FindProcess(p.PID)
	if e != nil {
		return e
	}
	defer handle.Release()
	_, start, e := processIdentity(p.PID)
	if errors.Is(e, fs.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if start != p.StartTime {
		return fmt.Errorf("process identity changed for PID %d; refusing to signal it", p.PID)
	}
	e = handle.Signal(sig)
	if errors.Is(e, os.ErrProcessDone) || errors.Is(e, syscall.ESRCH) {
		return nil
	}
	return e
}

// Only the snapshot selected by the user is stopped. A newly started Claude
// process is never killed as part of a retry; the write guard will catch it.
func stopClaudeProcesses(initial []claudeProcess, send func(claudeProcess, os.Signal) error, remaining func([]claudeProcess) ([]claudeProcess, error), wait func(time.Duration)) error {
	for _, p := range initial {
		if e := send(p, syscall.SIGTERM); e != nil {
			return e
		}
	}
	active := initial
	for i := 0; i < 30; i++ {
		var e error
		active, e = remaining(active)
		if e != nil {
			return e
		}
		if len(active) == 0 {
			return nil
		}
		wait(200 * time.Millisecond)
	}
	for _, p := range active {
		if e := send(p, syscall.SIGKILL); e != nil {
			return e
		}
	}
	for i := 0; i < 20; i++ {
		var e error
		active, e = remaining(active)
		if e != nil {
			return e
		}
		if len(active) == 0 {
			return nil
		}
		wait(100 * time.Millisecond)
	}
	return errors.New("some selected Claude processes did not exit; no synchronization was started")
}
func remainingClaude(initial []claudeProcess) ([]claudeProcess, error) {
	current, e := listClaudeProcesses()
	if e != nil {
		return nil, e
	}
	byID := map[int]claudeProcess{}
	for _, p := range current {
		byID[p.PID] = p
	}
	active := []claudeProcess{}
	for _, p := range initial {
		if c, ok := byID[p.PID]; ok && c.StartTime == p.StartTime {
			active = append(active, p)
		}
	}
	return active, nil
}
func forceQuitClaude() error {
	ps, e := listClaudeProcesses()
	if e != nil {
		return e
	}
	if len(ps) == 0 {
		return nil
	}
	return stopClaudeProcesses(ps, signalClaude, remainingClaude, time.Sleep)
}
