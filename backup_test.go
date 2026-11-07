//go:build linux || windows || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}

func fixtureRoots(t *testing.T) (string, string) {
	base := t.TempDir()
	desk := filepath.Join(base, "Claude")
	home := filepath.Join(base, "dot-claude")
	writeFile(t, filepath.Join(desk, "claude-code-sessions", "acct", "org", "local_1.json"), `{"sessionId":"local_1"}`)
	writeFile(t, filepath.Join(desk, "ccd-session-presets.json"), `{}`)
	writeFile(t, filepath.Join(desk, "Cookies"), "secret-cookie")
	writeFile(t, filepath.Join(desk, "config.json"), `{"token":"secret"}`)
	writeFile(t, filepath.Join(home, "projects", "-Users-me-app", "abc.jsonl"), "{\"type\":\"user\"}\n")
	writeFile(t, filepath.Join(home, "history.jsonl"), "{}\n")
	writeFile(t, filepath.Join(home, ".credentials.json"), `{"oauth":"secret"}`)
	writeFile(t, filepath.Join(home, "settings.json"), `{}`)
	return desk, home
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	desk, home := fixtureRoots(t)
	dest := t.TempDir()
	now := time.Date(2026, 11, 7, 9, 30, 0, 0, time.Local)
	dir, m, e := createBackup(dest, backupRoots{desk, home}, false, now)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(dir) != "claude-backup-20261107-093000" || m.Sessions != 1 || m.Chats != 1 {
		t.Fatal(dir, m.Sessions, m.Chats)
	}
	for _, f := range m.Files {
		p := f.Role + "/" + f.Path
		if strings.Contains(p, "Cookies") || strings.Contains(p, "config.json") || strings.Contains(p, "credentials") || strings.Contains(p, "settings.json") {
			t.Fatal("sensitive or opt-in file included:", p)
		}
	}
	if _, bad, e := verifyBackup(dir); e != nil || len(bad) != 0 {
		t.Fatal(e, bad)
	}
	list, e := listBackups(dest)
	if e != nil || len(list) != 1 || !list[0].Complete || list[0].Files != len(m.Files) {
		t.Fatal(e, list)
	}

	// Restore into empty roots, as after a fresh install.
	base := t.TempDir()
	targets := map[string]string{"desktop": filepath.Join(base, "Claude"), "claude": filepath.Join(base, ".claude")}
	rep, e := restoreBackup(dir, targets, false, false, now)
	if e != nil || len(rep.Create) != len(m.Files) || rep.Restored != 0 {
		t.Fatal(e, rep)
	}
	if _, e := os.Stat(targets["claude"]); !os.IsNotExist(e) {
		t.Fatal("dry run wrote to target")
	}
	rep, e = restoreBackup(dir, targets, true, false, now)
	if e != nil || rep.Restored != len(m.Files) {
		t.Fatal(e, rep)
	}
	got, _ := os.ReadFile(filepath.Join(targets["claude"], "projects", "-Users-me-app", "abc.jsonl"))
	if string(got) != "{\"type\":\"user\"}\n" {
		t.Fatal(string(got))
	}
	// Second restore is a no-op.
	rep, e = restoreBackup(dir, targets, true, false, now)
	if e != nil || rep.Restored != 0 || rep.Identical != len(m.Files) {
		t.Fatal(e, rep)
	}
}

func TestRestoreConflictsAreKeptUnlessOverwrite(t *testing.T) {
	desk, home := fixtureRoots(t)
	dest := t.TempDir()
	now := time.Now()
	dir, _, e := createBackup(dest, backupRoots{desk, home}, false, now)
	if e != nil {
		t.Fatal(e)
	}
	live := filepath.Join(home, "history.jsonl")
	writeFile(t, live, "{}\n{\"newer\":true}\n")
	targets := map[string]string{"desktop": desk, "claude": home}
	rep, e := restoreBackup(dir, targets, true, false, now)
	if e != nil || len(rep.Conflicts) != 1 || rep.Restored != 0 {
		t.Fatal(e, rep)
	}
	if b, _ := os.ReadFile(live); string(b) != "{}\n{\"newer\":true}\n" {
		t.Fatal("conflict overwritten without --overwrite")
	}
	rep, e = restoreBackup(dir, targets, true, true, now)
	if e != nil || rep.Restored != 1 || rep.SavedTo == "" {
		t.Fatal(e, rep)
	}
	if b, _ := os.ReadFile(live); string(b) != "{}\n" {
		t.Fatal("not restored")
	}
	if b, _ := os.ReadFile(filepath.Join(rep.SavedTo, "claude", "history.jsonl")); string(b) != "{}\n{\"newer\":true}\n" {
		t.Fatal("replaced version not saved")
	}
}

func TestRestoreRefusesCorruptBackup(t *testing.T) {
	desk, home := fixtureRoots(t)
	dir, _, e := createBackup(t.TempDir(), backupRoots{desk, home}, true, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	writeFile(t, filepath.Join(dir, "files", "claude", "history.jsonl"), "tampered")
	target := filepath.Join(t.TempDir(), ".claude")
	if _, e := restoreBackup(dir, map[string]string{"claude": target}, true, false, time.Now()); e == nil {
		t.Fatal("corrupt backup restored")
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("wrote despite corruption")
	}
}

func TestBackupWithConfigAndDestinationGuard(t *testing.T) {
	desk, home := fixtureRoots(t)
	_, m, e := createBackup(t.TempDir(), backupRoots{desk, home}, true, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range m.Files {
		if f.Role == "claude" && f.Path == "settings.json" {
			found = true
		}
		if strings.Contains(f.Path, "credentials") {
			t.Fatal("credentials included")
		}
	}
	if !found {
		t.Fatal("--with-config did not include settings.json")
	}
	if _, _, e := createBackup(home, backupRoots{desk, home}, false, time.Now()); e == nil {
		t.Fatal("backup inside a source root was allowed")
	}
}

func TestArchiveBackupRestore(t *testing.T) {
	desk, home := fixtureRoots(t)
	dest := t.TempDir()
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)
	p, m, e := createBackup(dest, backupRoots{desk, home}, false, now, true)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(p) != "claude-backup-20261107-100000.tar.gz" || !isArchive(p) {
		t.Fatal(p)
	}
	if _, e := os.Stat(p + ".partial"); !os.IsNotExist(e) {
		t.Fatal("partial file left behind")
	}
	if _, bad, e := verifyBackup(p); e != nil || len(bad) != 0 {
		t.Fatal(e, bad)
	}
	// Folders that are not tool backups are ignored; partial archives are incomplete.
	os.Mkdir(filepath.Join(dest, "claude-backup-quick-rsync-20261107-092503"), 0700)
	writeFile(t, filepath.Join(dest, "claude-backup-20261107-110000.tar.gz.partial"), "x")
	list, e := listBackups(dest)
	if e != nil || len(list) != 2 || !list[0].Complete || list[0].Files != len(m.Files) || list[0].Archive == 0 || list[1].Complete {
		t.Fatal(e, list)
	}

	base := t.TempDir()
	targets := map[string]string{"desktop": filepath.Join(base, "Claude"), "claude": filepath.Join(base, ".claude")}
	rep, e := restoreBackup(p, targets, false, false, now)
	if e != nil || len(rep.Create) != len(m.Files) {
		t.Fatal(e, rep)
	}
	rep, e = restoreBackup(p, targets, true, false, now)
	if e != nil || rep.Restored != len(m.Files) {
		t.Fatal(e, rep)
	}
	got, _ := os.ReadFile(filepath.Join(targets["desktop"], "claude-code-sessions", "acct", "org", "local_1.json"))
	if string(got) != `{"sessionId":"local_1"}` {
		t.Fatal(string(got))
	}
	// The temporary unpack folder is removed.
	entries, _ := os.ReadDir(dest)
	for _, d := range entries {
		if strings.HasPrefix(d.Name(), ".claude-restore-") {
			t.Fatal("temporary folder left:", d.Name())
		}
	}
	// Overwrite keeps the replaced version next to the archive.
	live := filepath.Join(targets["claude"], "history.jsonl")
	writeFile(t, live, "changed\n")
	rep, e = restoreBackup(p, targets, true, true, now)
	if e != nil || rep.Restored != 1 || !strings.HasPrefix(rep.SavedTo, strings.TrimSuffix(p, ".tar.gz")+"-replaced-") {
		t.Fatal(e, rep)
	}
	if b, _ := os.ReadFile(filepath.Join(rep.SavedTo, "claude", "history.jsonl")); string(b) != "changed\n" {
		t.Fatal("replaced version not saved")
	}
}

func TestArchiveRejectsTampering(t *testing.T) {
	desk, home := fixtureRoots(t)
	dest := t.TempDir()
	p, _, e := createBackup(dest, backupRoots{desk, home}, false, time.Now(), true)
	if e != nil {
		t.Fatal(e)
	}
	m, e := loadManifest(p)
	if e != nil {
		t.Fatal(e)
	}
	m.Files[0].SHA256 = strings.Repeat("0", 64)
	// Rebuild an archive whose manifest no longer matches its content.
	src := filepath.Join(t.TempDir(), "src")
	os.Mkdir(src, 0700)
	if _, e := scanArchive(p, BackupManifest{Files: m.Files}, src); e != nil {
		t.Fatal(e)
	}
	s, e := newTarSink(filepath.Join(dest, "claude-backup-20990101-000000.tar.gz"))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range m.Files {
		fp := filepath.Join(src, "files", f.Role, filepath.FromSlash(f.Path))
		info, _ := os.Stat(fp)
		if _, _, e := s.add(f.Role, filepath.FromSlash(f.Path), fp, info); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.finish(m); e != nil {
		t.Fatal(e)
	}
	bad := s.path()
	if _, list, e := verifyBackup(bad); e != nil || len(list) != 1 {
		t.Fatal(e, list)
	}
	target := filepath.Join(t.TempDir(), ".claude")
	if _, e := restoreBackup(bad, map[string]string{"claude": target}, true, false, time.Now()); e == nil {
		t.Fatal("tampered archive restored")
	}
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("wrote despite tampering")
	}
}
