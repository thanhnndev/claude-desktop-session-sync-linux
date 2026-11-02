package main

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"
)

type claudeProcess struct {
	PID, Parent           int
	Executable, StartTime string
}
type claudeRunningError struct{ Processes []claudeProcess }

func (e *claudeRunningError) Error() string {
	return fmt.Sprintf("%d Claude process(es) are still running; fully quit Desktop and its workers (first PID: %d)", len(e.Processes), e.Processes[0].PID)
}

func isClaudeName(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case "claude.exe", "claude-desktop.exe", "claude_code.exe", "claude-code.exe":
		return true
	}
	return false
}

func handleIdentity(h windows.Handle) (string, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x%08x", created.HighDateTime, created.LowDateTime), nil
}

func sameUser(h windows.Handle) (bool, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	return windows.EqualSid(user.User.Sid, current.User.Sid), nil
}

func listClaudeProcesses() ([]claudeProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	found := []claudeProcess{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if !isClaudeName(name) || int(entry.ProcessID) == os.Getpid() {
			continue
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if errors.Is(e, windows.ERROR_INVALID_PARAMETER) {
			continue
		} // already exited
		if e != nil {
			return nil, fmt.Errorf("cannot inspect Claude PID %d: %w", entry.ProcessID, e)
		}
		own, e := sameUser(h)
		if e != nil {
			windows.CloseHandle(h)
			return nil, e
		}
		if !own {
			windows.CloseHandle(h)
			continue
		}
		start, e := handleIdentity(h)
		windows.CloseHandle(h)
		if e != nil {
			return nil, e
		}
		found = append(found, claudeProcess{PID: int(entry.ProcessID), Parent: int(entry.ParentProcessID), Executable: name, StartTime: start})
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool { return found[i].PID < found[j].PID })
	return found, nil
}
func processGuard() error {
	ps, err := listClaudeProcesses()
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return &claudeRunningError{Processes: ps}
	}
	return nil
}

// Identity and ownership are checked on the same handle used for termination;
// a recycled PID can never select an unrelated process.
func terminateClaude(p claudeProcess) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(p.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	start, err := handleIdentity(h)
	if err != nil {
		return err
	}
	if start != p.StartTime {
		return fmt.Errorf("process identity changed for PID %d; refusing to terminate it", p.PID)
	}
	own, err := sameUser(h)
	if err != nil {
		return err
	}
	if !own {
		return errors.New("refusing to terminate another user's process")
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		state, waitErr := windows.WaitForSingleObject(h, 0)
		if waitErr == nil && state == windows.WAIT_OBJECT_0 {
			return nil
		}
		return err
	}
	state, err := windows.WaitForSingleObject(h, uint32((6*time.Second)/time.Millisecond))
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return errors.New("selected Claude process did not exit")
	}
	return nil
}
func forceQuitClaude() error {
	initial, err := listClaudeProcesses()
	if err != nil {
		return err
	}
	for _, p := range initial {
		if err := terminateClaude(p); err != nil {
			return err
		}
	}
	return processGuard()
}
