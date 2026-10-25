//go:build linux

package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func runningFixture() error { return &claudeRunningError{Processes: []claudeProcess{{PID: 12345}}} }
func TestInteractiveRetry(t *testing.T) {
	checks := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("r\n")), &out, func() error {
		checks++
		if checks == 1 {
			return runningFixture()
		}
		return nil
	}, func() error { t.Fatal("retry force-quit processes"); return nil })
	if e != nil || checks != 2 || !strings.Contains(out.String(), "[F] Force quit") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveForceQuit(t *testing.T) {
	stopped := false
	calls := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("f\n")), &out, func() error {
		if !stopped {
			return runningFixture()
		}
		return nil
	}, func() error { calls++; stopped = true; return nil })
	if e != nil || calls != 1 || !strings.Contains(out.String(), "Stopping detected Claude processes") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveCancelAndEOF(t *testing.T) {
	for _, input := range []string{"c\n", ""} {
		var out bytes.Buffer
		e := waitForClaude(bufio.NewReader(strings.NewReader(input)), &out, runningFixture, func() error { t.Fatal("cancel force-quit processes"); return nil })
		if e == nil {
			t.Fatal("cancel accepted")
		}
	}
}
func TestInteractiveInvalidAndFailedForceThenRetry(t *testing.T) {
	checks := 0
	quitCalls := 0
	var out bytes.Buffer
	e := waitForClaude(bufio.NewReader(strings.NewReader("invalid\nf\nr\n")), &out, func() error {
		checks++
		if checks <= 3 {
			return runningFixture()
		}
		return nil
	}, func() error { quitCalls++; return errors.New("permission denied") })
	if e != nil || quitCalls != 1 || !strings.Contains(out.String(), "Invalid choice") || !strings.Contains(out.String(), "Force quit failed") {
		t.Fatal(e, out.String())
	}
}
func TestInteractiveInspectionFailureDoesNotOfferForce(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("cannot inspect")
	if e := waitForClaude(bufio.NewReader(strings.NewReader("f\n")), &out, func() error { return want }, func() error { t.Fatal("unexpected force"); return nil }); e != want || out.Len() != 0 {
		t.Fatal(e, out.String())
	}
}
func TestEnglishMenuRoutesOperations(t *testing.T) {
	var calls [][]string
	var out bytes.Buffer
	e := interactiveMenu(bufio.NewReader(strings.NewReader("1\n2\n3\n4\n5\n20261025T120000.000000000Z\nq\n")), &out, func(a []string) error { calls = append(calls, a); return nil })
	if e != nil || len(calls) != 5 {
		t.Fatal(e, calls)
	}
	if calls[0][0] != "plan" || calls[1][0] != "sync" || calls[3][2] != "chat" || calls[4][0] != "undo" {
		t.Fatal(calls)
	}
	if !strings.Contains(out.String(), "Back up and synchronize") || !strings.Contains(out.String(), "Backup ID:") {
		t.Fatal("English labels missing")
	}
}
func TestMenuRecoversFromErrorAndInvalidChoice(t *testing.T) {
	var out bytes.Buffer
	n := 0
	e := interactiveMenu(bufio.NewReader(strings.NewReader("wrong\n1\nq\n")), &out, func([]string) error { n++; return errors.New("test failure") })
	if e != nil || n != 1 || !strings.Contains(out.String(), "Error: test failure") {
		t.Fatal(e, out.String())
	}
}
func TestProcessClassifier(t *testing.T) {
	for _, c := range []struct {
		exe  string
		argv []byte
		want bool
	}{
		{"/usr/lib/claude-desktop/claude-desktop (deleted)", nil, true},
		{"/home/test/.config/Claude/claude-code/version/claude", nil, true},
		{"/usr/bin/claude-session-sync", nil, false},
		{"/tmp/claude-desktop-session-sync-linux.test", nil, false},
		{"/usr/bin/electron", []byte("electron\x00/usr/lib/claude-desktop/resources/app.asar\x00"), true},
		{"/usr/bin/electron", []byte("electron\x00/home/test/unrelated.js\x00--title=claude\x00"), false},
	} {
		if got := isClaudeExecutable(c.exe, c.argv); got != c.want {
			t.Fatal(c.exe, got)
		}
	}
}
func TestStopClaudeTermThenKill(t *testing.T) {
	initial := []claudeProcess{{PID: 123, StartTime: "1"}, {PID: 456, StartTime: "2"}}
	signals := []os.Signal{}
	killed := false
	e := stopClaudeProcesses(initial, func(_ claudeProcess, s os.Signal) error {
		signals = append(signals, s)
		if s == syscall.SIGKILL {
			killed = true
		}
		return nil
	}, func(p []claudeProcess) ([]claudeProcess, error) {
		if killed {
			return nil, nil
		}
		return p, nil
	}, func(time.Duration) {})
	if e != nil || !reflect.DeepEqual(signals, []os.Signal{syscall.SIGTERM, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGKILL}) {
		t.Fatal(e, signals)
	}
}
func TestStopClaudeDoesNotKillExitedProcesses(t *testing.T) {
	signals := []os.Signal{}
	e := stopClaudeProcesses([]claudeProcess{{PID: 123}}, func(_ claudeProcess, s os.Signal) error { signals = append(signals, s); return nil }, func([]claudeProcess) ([]claudeProcess, error) { return nil, nil }, func(time.Duration) {})
	if e != nil || !reflect.DeepEqual(signals, []os.Signal{syscall.SIGTERM}) {
		t.Fatal(e, signals)
	}
}
func TestStopClaudeFailure(t *testing.T) {
	want := errors.New("cannot stop")
	e := stopClaudeProcesses([]claudeProcess{{PID: 123}}, func(claudeProcess, os.Signal) error { return want }, func(p []claudeProcess) ([]claudeProcess, error) { return p, nil }, func(time.Duration) {})
	if e != want {
		t.Fatal(e)
	}
}
func TestCurrentProcessIdentity(t *testing.T) {
	_, start, e := processIdentity(os.Getpid())
	if e != nil || start == "" {
		t.Fatal(start, e)
	}
}
func TestMenuEOF(t *testing.T) {
	var out bytes.Buffer
	if e := interactiveMenu(bufio.NewReader(strings.NewReader("")), &out, func([]string) error { return io.EOF }); e != nil {
		t.Fatal(e)
	}
}
func TestForceQuitCannotRunOnDryRun(t *testing.T) {
	for _, a := range [][]string{{"plan", "--force-quit"}, {"sync", "--force-quit"}, {"sync", "--apply", "--force-quit"}} {
		if e := run(a); e == nil {
			t.Fatal("unsafe flags accepted", a)
		}
	}
}

func TestSignalOnlyOwnedTestProcess(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if e := child.Start(); e != nil {
		t.Fatal(e)
	}
	defer child.Process.Kill()
	_, start, e := processIdentity(child.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	p := claudeProcess{PID: child.Process.Pid, StartTime: start}
	stale := p
	stale.StartTime = "different-start-time"
	if e := signalClaude(stale, syscall.SIGTERM); e == nil {
		t.Fatal("stale identity was signalled")
	}
	if e := child.Process.Signal(syscall.Signal(0)); e != nil {
		t.Fatal("stale identity terminated child", e)
	}
	if e := signalClaude(p, syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	child.Wait()
	status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGTERM {
		t.Fatal("expected TERM on test child")
	}
}

func TestProtectedUnrelatedProcessDoesNotBlockSync(t *testing.T) {
	for _, name := range []string{"systemd\n", "gnome-keyring-d\n", "dbus-broker\n"} {
		if !ignorableInspectionError(os.ErrPermission, name) {
			t.Fatal("unrelated protected service blocked sync", name)
		}
	}
	for _, name := range []string{"claude\n", "claude-desktop\n", "electron\n", ""} {
		if ignorableInspectionError(os.ErrPermission, name) {
			t.Fatal("uninspectable Claude process was ignored", name)
		}
	}
	if ignorableInspectionError(errors.New("unexpected failure"), "systemd") {
		t.Fatal("unexpected inspection failure ignored")
	}
}
