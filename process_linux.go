//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
func processIdentity(pid int) (parent int, start string, err error) {
	b, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if e != nil {
		return 0, "", e
	}
	// comm can contain spaces and parentheses; fields after its final ')' are stable.
	end := bytes.LastIndexByte(b, ')')
	if end < 0 {
		return 0, "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return 0, "", errors.New("short process stat")
	}
	parent, e = strconv.Atoi(fields[1])
	return parent, fields[19], e
}
func isClaudeExecutable(exe string, argv []byte) bool {
	exe = strings.TrimSuffix(exe, " (deleted)")
	name := strings.ToLower(filepath.Base(exe))
	if name == "claude" || name == "claude-desktop" || name == "claude_code" {
		return true
	}
	if strings.Contains(name, "electron") {
		for _, arg := range bytes.Split(argv, []byte{0}) {
			a := strings.ToLower(string(arg))
			if strings.Contains(a, "/claude-desktop/") || strings.Contains(a, "/claude/resources/") {
				return true
			}
		}
	}
	return false
}
func ignorableInspectionError(err error, comm string) bool {
	if !errors.Is(err, fs.ErrPermission) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(comm))
	return name != "" && !strings.Contains(name, "claude") && !strings.Contains(name, "electron")
}

func listClaudeProcesses() ([]claudeProcess, error) {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return nil, e
	}
	found := []claudeProcess{}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid == os.Getpid() {
			continue
		}
		base := filepath.Join("/proc", entry.Name())
		st, e := os.Stat(base)
		if e != nil {
			continue
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Uid != uint32(os.Getuid()) {
			continue
		}
		exe, e := os.Readlink(filepath.Join(base, "exe"))
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			comm, commErr := os.ReadFile(filepath.Join(base, "comm"))
			if commErr == nil && ignorableInspectionError(e, string(comm)) {
				continue
			}
			return nil, fmt.Errorf("cannot inspect process %d: %w", pid, e)
		}
		var argv []byte
		if strings.Contains(strings.ToLower(filepath.Base(exe)), "electron") {
			argv, e = os.ReadFile(filepath.Join(base, "cmdline"))
			if errors.Is(e, fs.ErrNotExist) {
				continue
			}
			if e != nil {
				return nil, e
			}
		}
		if !isClaudeExecutable(exe, argv) {
			continue
		}
		parent, start, e := processIdentity(pid)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		found = append(found, claudeProcess{PID: pid, Parent: parent, Executable: exe, StartTime: start})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PID < found[j].PID })
	return found, nil
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
