package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsDiscovery(t *testing.T) {
	local, roaming := t.TempDir(), t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", roaming)
	store := filepath.Join(local, "Claude-Data", "claude_test")
	if err := os.MkdirAll(filepath.Join(store, "claude-code-sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := discover("")
	if err != nil || !strings.EqualFold(got, store) {
		t.Fatalf("discovery: %s %v", got, err)
	}
	if err := os.MkdirAll(filepath.Join(roaming, "Claude", "claude-code-sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(""); err == nil {
		t.Fatal("ambiguous Windows roots accepted")
	}
}

func TestWindowsJunctionRootRefused(t *testing.T) {
	f := setup(t)
	link := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, f.data).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v %s", err, out)
	}
	for _, path := range []string{link, filepath.Join(link, "claude-code-sessions")} {
		if r, err := realRoot(path); err == nil {
			r.Close()
			t.Fatal("junction root/ancestor accepted")
		}
	}
}

func TestWindowsForwardSlashSelectionAndBackupACL(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	rep, err := scan(f.data, f.projects, accountA+"/"+org, accountB+"/"+org, "all")
	if err != nil || len(rep.Actions) != 1 {
		t.Fatalf("selectors: %v %v", rep.Actions, err)
	}
	id, err := apply(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(f.data, backupDir), filepath.Join(f.data, backupDir, id, "manifest.json")} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		text := sd.String()
		if strings.Contains(text, ";;;BU)") || strings.Contains(text, ";;;WD)") || strings.Contains(text, ";;;AU)") {
			t.Fatal("backup grants access to broad user groups")
		}
		if !strings.Contains(text, ";;;SY)") {
			t.Fatal("backup ACL missing SYSTEM")
		}
	}
	if _, err := undo(f.data, id, true); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsProcessHelper(t *testing.T) {
	if os.Getenv("CLAUDE_SYNC_PROCESS_HELPER") != "1" {
		return
	}
	time.Sleep(45 * time.Second)
	os.Exit(0)
}

func TestWindowsProcessGuardAndIdentity(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "claude.exe")
	if err := os.WriteFile(helper, b, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(helper, "-test.run=^TestWindowsProcessHelper$")
	cmd.Env = append(os.Environ(), "CLAUDE_SYNC_PROCESS_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	ps, err := listClaudeProcesses()
	if err != nil {
		t.Fatal(err)
	}
	var selected *claudeProcess
	for i := range ps {
		if ps[i].PID == cmd.Process.Pid {
			selected = &ps[i]
			break
		}
	}
	if selected == nil {
		t.Fatal("Claude helper not detected")
	}
	var running *claudeRunningError
	if !errors.As(processGuard(), &running) {
		t.Fatal("running Claude did not block writes")
	}
	stale := *selected
	stale.StartTime = "stale"
	if err := terminateClaude(stale); err == nil {
		t.Fatal("stale identity terminated")
	}
	if err := terminateClaude(*selected); err != nil {
		t.Fatal(err)
	}
	// Only this test's selected helper is terminated; real Claude is untouched.
}

func TestWindowsExecutableNames(t *testing.T) {
	for _, name := range []string{"Claude.exe", "claude-desktop.exe", "claude_code.exe", "claude-code.exe"} {
		if !isClaudeName(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"claude-session-sync.exe", "not-claude.exe", "electron.exe"} {
		if isClaudeName(name) {
			t.Fatal(name)
		}
	}
}
