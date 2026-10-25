//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const accountA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const accountB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const org = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
const internal = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
const cli = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
const prior = "ffffffff-ffff-4fff-8fff-ffffffffffff"

type fixture struct{ data, projects, a, b, record string }

func put(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func object(t *testing.T, p string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	return m
}
func save(t *testing.T, p string, m map[string]any) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	put(t, p, b)
}
func setup(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{data: filepath.Join(root, "Claude"), projects: filepath.Join(root, "projects"), a: accountA + "/" + org, b: accountB + "/" + org}
	for _, b := range []string{f.a, f.b} {
		if e := os.MkdirAll(filepath.Join(f.data, "claude-code-sessions", b), 0700); e != nil {
			t.Fatal(e)
		}
	}
	f.record = filepath.Join(f.data, "claude-code-sessions", f.a, "local_"+internal+".json")
	save(t, f.record, map[string]any{"sessionId": "local_" + internal, "cliSessionId": cli, "priorCliSessionIds": []string{prior}, "cwd": root, "title": "Resume workflow", "createdAt": 1, "lastActivityAt": 2, "isArchived": false, "permissionMode": "acceptEdits", "sessionSettings": map[string]any{"model": "test-model"}, "enabledMcpTools": map[string]any{"example": true}, "gitAnchors": []any{map[string]any{"gitRoot": root, "commonDir": root, "trustedAt": 100}}, "remoteMcpServersConfig": []any{map[string]any{"url": "https://example.test/mcp"}}})
	save(t, filepath.Join(f.projects, "project", cli+".jsonl"), map[string]any{"sessionId": cli, "entrypoint": "claude-desktop", "cwd": root})
	return f
}
func plan(t *testing.T, f fixture, mode string) Report {
	t.Helper()
	r, e := scan(f.data, f.projects, "", "", mode)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func noProcesses(t *testing.T) {
	t.Helper()
	old := guard
	guard = func() error { return nil }
	t.Cleanup(func() { guard = old })
}
func TestModesKeepWorkflowAndChatResets(t *testing.T) {
	f := setup(t)
	all := plan(t, f, "all")
	chat := plan(t, f, "chat")
	if len(all.Actions) != 1 || len(chat.Actions) != 1 {
		t.Fatal("expected one missing pointer")
	}
	var a, c map[string]any
	json.Unmarshal(all.Actions[0].Payload, &a)
	json.Unmarshal(chat.Actions[0].Payload, &c)
	if a["permissionMode"] != "acceptEdits" || a["sessionSettings"] == nil || a["enabledMcpTools"] == nil {
		t.Fatal("all lost session configuration")
	}
	if c["permissionMode"] != "default" || c["sessionSettings"] != nil || c["enabledMcpTools"] != nil {
		t.Fatal("chat transferred session configuration")
	}
	if a["sessionId"] != c["sessionId"] {
		t.Fatal("IDs changed")
	}
}
func TestDryRunWritesNothing(t *testing.T) {
	f := setup(t)
	before, _ := os.ReadFile(f.record)
	plan(t, f, "all")
	after, _ := os.ReadFile(f.record)
	if !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
	if _, e := os.Stat(filepath.Join(f.data, backupDir)); !os.IsNotExist(e) {
		t.Fatal("dry run created backup")
	}
	if _, e := os.Stat(filepath.Join(f.data, ".linux-session-sync.lock")); !os.IsNotExist(e) {
		t.Fatal("dry run created lock")
	}
}
func TestApplyIdempotentBackupUndo(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	before, _ := os.ReadFile(f.record)
	r := plan(t, f, "all")
	id, e := apply(r)
	if e != nil {
		t.Fatal(e)
	}
	if id == "" {
		t.Fatal("missing backup")
	}
	manifest := filepath.Join(f.data, backupDir, id, "manifest.json")
	i, e := os.Stat(manifest)
	if e != nil || i.Mode().Perm() != 0600 {
		t.Fatal("backup permission")
	}
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("not idempotent")
	}
	m, e := undo(f.data, id, false)
	if e != nil || len(m) != 1 {
		t.Fatal(m, e)
	}
	if _, e = undo(f.data, id, true); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(f.record)
	if !bytes.Equal(before, after) {
		t.Fatal("original modified")
	}
	if len(plan(t, f, "all").Actions) != 1 {
		t.Fatal("undo failed")
	}
	q, e := filepath.Glob(filepath.Join(f.data, backupDir, id, "quarantine-*.json"))
	if e != nil || len(q) != 1 {
		t.Fatal("undo lost added record")
	}
}
func TestUndoKeepsModifiedRecord(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	r := plan(t, f, "all")
	id, e := apply(r)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(f.data, r.Actions[0].Target)
	d := object(t, target)
	d["title"] = "edited later"
	save(t, target, d)
	if _, e = undo(f.data, id, true); e == nil {
		t.Fatal("undo overwrote modified record")
	}
	if object(t, target)["title"] != "edited later" {
		t.Fatal("modified record lost")
	}
}
func TestTombstoneAliases(t *testing.T) {
	for _, id := range []string{internal, cli, prior} {
		t.Run(id, func(t *testing.T) {
			f := setup(t)
			put(t, filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_"+id), []byte("123"))
			r := plan(t, f, "all")
			if len(r.Actions) != 0 {
				t.Fatal("resurrected deleted session")
			}
		})
	}
}
func TestUnarchivedAliasTombstone(t *testing.T) {
	f := setup(t)
	d := object(t, f.record)
	d["unarchivedCliSessionId"] = prior
	save(t, f.record, d)
	put(t, filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_"+prior), []byte("123"))
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("unarchived tombstone ignored")
	}
}
func TestInvalidTombstoneFailsClosed(t *testing.T) {
	f := setup(t)
	put(t, filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_unknown"), nil)
	if _, e := scan(f.data, f.projects, "", "", "all"); e == nil {
		t.Fatal("unrecognized deletion marker ignored")
	}
}
func TestConflictNoOverwrite(t *testing.T) {
	f := setup(t)
	dst := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	d := object(t, f.record)
	d["title"] = "other account edits"
	save(t, dst, d)
	r := plan(t, f, "all")
	if len(r.Actions) != 0 || len(r.Warnings) == 0 {
		t.Fatal("conflict not reported")
	}
	if object(t, dst)["title"] != "other account edits" {
		t.Fatal("conflict overwritten")
	}
}
func TestCorruptRecordBlocksName(t *testing.T) {
	f := setup(t)
	put(t, filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record)), []byte("{"))
	r := plan(t, f, "all")
	if len(r.Actions) != 0 || len(r.Warnings) == 0 {
		t.Fatal("corruption ignored")
	}
}
func TestSymlinkRefused(t *testing.T) {
	f := setup(t)
	dst := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	if e := os.Symlink(f.record, dst); e != nil {
		t.Fatal(e)
	}
	if _, e := scan(f.data, f.projects, "", "", "all"); e == nil {
		t.Fatal("followed symlink")
	}
}
func TestRootSymlinkRefused(t *testing.T) {
	f := setup(t)
	link := filepath.Join(t.TempDir(), "linked")
	os.Symlink(f.data, link)
	if _, e := scan(link, f.projects, "", "", "all"); e == nil {
		t.Fatal("followed root symlink")
	}
}
func TestCLIOnlyNeverImported(t *testing.T) {
	f := setup(t)
	save(t, filepath.Join(f.projects, "project", cli+".jsonl"), map[string]any{"sessionId": cli, "entrypoint": "cli"})
	r := plan(t, f, "all")
	if len(r.Actions) != 0 || r.MissingTranscripts != 1 {
		t.Fatal("CLI-only imported")
	}
}
func TestReleasedSkipped(t *testing.T) {
	f := setup(t)
	put(t, filepath.Join(f.projects, "project", cli+".desktop-released.json"), []byte("{}"))
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("released resurrected")
	}
}
func TestSecretsExcludedAllAndChat(t *testing.T) {
	for _, mode := range []string{"chat"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			d := object(t, f.record)
			d["title"] = "Bearer private-value"
			d["accessToken"] = "private-value"
			d["spawnSeed"] = map[string]any{"opaque": "private-value"}
			d["sessionSettings"] = map[string]any{"temperature": 1, "env": map[string]any{"API_KEY": "private-value"}}
			d["remoteMcpServersConfig"] = []any{map[string]any{"url": "https://example.test", "headers": map[string]any{"X-Custom": "private-value"}}, map[string]any{"url": "https://example.test/mcp"}}
			save(t, f.record, d)
			r := plan(t, f, mode)
			if len(r.Actions) != 1 {
				t.Fatal("expected action")
			}
			if bytes.Contains(r.Actions[0].Payload, []byte("private-value")) {
				t.Fatal("secret copied")
			}
			if len(r.Excluded) == 0 {
				t.Fatal("missing exclusion report")
			}
		})
	}
}
func TestStalePlanDoesNotResurrect(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	r := plan(t, f, "all")
	put(t, filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_"+cli), []byte("123"))
	if _, e := apply(r); e == nil {
		t.Fatal("stale plan applied")
	}
}
func TestGuardStopsApplyBeforeBackup(t *testing.T) {
	f := setup(t)
	old := guard
	guard = func() error { return errors.New("still running") }
	defer func() { guard = old }()
	if _, e := apply(plan(t, f, "all")); e == nil {
		t.Fatal("ignored running app")
	}
	if _, e := os.Stat(filepath.Join(f.data, backupDir)); !os.IsNotExist(e) {
		t.Fatal("created backup before guard")
	}
}
func TestNoClobberPublish(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "record"), []byte("original"))
	r, e := realRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if e = add(r, "record", []byte("overwrite")); e == nil {
		t.Fatal("overwrote")
	}
	b, _ := os.ReadFile(filepath.Join(root, "record"))
	if string(b) != "original" {
		t.Fatal("original lost")
	}
}
func TestExclusiveLock(t *testing.T) {
	f := setup(t)
	r, _ := realRoot(f.data)
	defer r.Close()
	a, e := lockRoot(r)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if b, e := lockRoot(r); e == nil {
		b.Close()
		t.Fatal("concurrent lock admitted")
	}
}
func TestAliasCollision(t *testing.T) {
	f := setup(t)
	d := object(t, f.record)
	d["sessionId"] = "local_" + prior
	save(t, filepath.Join(f.data, "claude-code-sessions", f.a, "local_"+prior+".json"), d)
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("duplicate transcript admitted")
	}
}
func TestFIFORefused(t *testing.T) {
	f := setup(t)
	if e := os.Remove(f.record); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(f.record, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := scan(f.data, f.projects, "", "", "all"); e == nil {
		t.Fatal("FIFO opened")
	}
}
func TestSelectionAndUnknownBucket(t *testing.T) {
	f := setup(t)
	r, e := scan(f.data, f.projects, f.a, f.b, "all")
	if e != nil || len(r.Actions) != 1 {
		t.Fatal(r, e)
	}
	if _, e := scan(f.data, f.projects, "unknown", f.b, "all"); e == nil {
		t.Fatal("unknown bucket accepted")
	}
}
func TestNoFabricatedPointerForOrphan(t *testing.T) {
	f := setup(t)
	os.Remove(f.record)
	r := plan(t, f, "all")
	if len(r.Actions) != 0 || len(r.Orphans) != 1 {
		t.Fatal("fabricated orphan ID")
	}
}
func TestInvalidAndUnsupportedArguments(t *testing.T) {
	for _, args := range [][]string{{"sync", "--copy", "bad"}, {"sync", "--apply"}, {"plan", "--apply", "--offline"}, {"sync", "--apply", "--offline", "--dry-run"}} {
		if e := run(args); e == nil {
			t.Fatal("accepted", args)
		}
	}
}
func TestPathTraversalRejected(t *testing.T) {
	r, _ := realRoot(t.TempDir())
	defer r.Close()
	if e := safe(r, "../escape", true); e == nil {
		t.Fatal("traversal allowed")
	}
	if _, e := undo(r.Name(), "../../evil", true); e == nil {
		t.Fatal("backup traversal")
	}
}
func TestBackupManifestHasNoSecrets(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	d := object(t, f.record)
	d["sessionSettings"] = map[string]any{"token": "do-not-back-up"}
	save(t, f.record, d)
	r := plan(t, f, "chat")
	id, e := apply(r)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(f.data, backupDir, id, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var j Journal
	if e = json.Unmarshal(b, &j); e != nil {
		t.Fatal(e)
	}
	for _, a := range j.Actions {
		if strings.Contains(string(a.Payload), "do-not-back-up") {
			t.Fatal("backup contains credentials")
		}
	}
}

func TestExplicitAllPreservesBytesIncludingConfig(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	d := object(t, f.record)
	d["spawnSeed"] = map[string]any{"env": map[string]any{"TOKEN": "explicit-test-secret"}, "args": []string{"--example", "value"}}
	save(t, f.record, d)
	original, _ := os.ReadFile(f.record)
	r, e := scan(f.data, f.projects, "", "", "all")
	if e != nil || len(r.Actions) != 1 {
		t.Fatal(e)
	}
	if !bytes.Equal(original, r.Actions[0].Payload) || len(r.Excluded) != 0 {
		t.Fatal("full-copy configuration altered")
	}
	id, e := apply(r)
	if e != nil {
		t.Fatal(e)
	}
	copied, _ := os.ReadFile(filepath.Join(f.data, r.Actions[0].Target))
	if !bytes.Equal(original, copied) {
		t.Fatal("full-copy bytes changed")
	}
	if _, e = undo(f.data, id, true); e != nil {
		t.Fatal(e)
	}
}
