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
)

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
