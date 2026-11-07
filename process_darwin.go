package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// processIdentity returns the parent PID and a start-time token that changes
// whenever a PID is reused.
func processIdentity(pid int) (parent int, start string, err error) {
	k, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if e != nil {
		return 0, "", e
	}
	if int(k.Proc.P_pid) != pid {
		return 0, "", os.ErrNotExist
	}
	t := k.Proc.P_starttime
	return int(k.Eproc.Ppid), fmt.Sprintf("%d.%06d", t.Sec, t.Usec), nil
}

// executablePath reads the exec path from kern.procargs2 (argc, then path).
func executablePath(pid int) (string, error) {
	b, e := unix.SysctlRaw("kern.procargs2", pid)
	if e != nil {
		return "", e
	}
	if len(b) < 5 {
		return "", errors.New("short procargs")
	}
	b = b[4:]
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}

func isClaudeExecutable(exe string, _ []byte) bool {
	if strings.Contains(exe, "/Claude.app/Contents/") {
		return true
	}
	name := strings.ToLower(filepath.Base(exe))
	if name == "claude" || name == "claude-desktop" || name == "claude_code" {
		return true
	}
	// Claude Code shipped inside Desktop: .../Application Support/Claude/claude-code/<ver>/.../claude
	return strings.Contains(exe, "/Application Support/Claude/claude-code/")
}

func listClaudeProcesses() ([]claudeProcess, error) {
	procs, e := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if e != nil {
		return nil, e
	}
	found := []claudeProcess{}
	for _, k := range procs {
		pid := int(k.Proc.P_pid)
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		exe, e := executablePath(pid)
		if e != nil {
			// Exited, zombie, or not inspectable: fall back to the short command name.
			if errors.Is(e, syscall.ESRCH) {
				continue
			}
			exe = unix.ByteSliceToString(k.Proc.P_comm[:])
		}
		if !isClaudeExecutable(exe, nil) {
			continue
		}
		t := k.Proc.P_starttime
		found = append(found, claudeProcess{PID: pid, Parent: int(k.Eproc.Ppid), Executable: exe, StartTime: fmt.Sprintf("%d.%06d", t.Sec, t.Usec)})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PID < found[j].PID })
	return found, nil
}
