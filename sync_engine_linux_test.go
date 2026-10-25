//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setTime(t *testing.T, p string, ns int64) {
	t.Helper()
	stamp := time.Unix(0, ns)
	if e := os.Chtimes(p, stamp, stamp); e != nil {
		t.Fatal(e)
	}
}
func synchronize(t *testing.T, f fixture, mode string) string {
	t.Helper()
	id, e := apply(plan(t, f, mode))
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestRoundTripUpdatesAndSharedMessages(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	a := f.record
	b := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(a))
	transcript := filepath.Join(f.projects, "project", cli+".jsonl")
	setTime(t, a, 1000000000)
	synchronize(t, f, "all")
	d := object(t, b)
	d["title"] = "Continued under B"
	d["sessionSettings"] = map[string]any{"effort": "high"}
	d["lastActivityAt"] = 100
	save(t, b, d)
	setTime(t, b, 2000000000)
	originalTranscript, _ := os.ReadFile(transcript)
	messages := append(originalTranscript, []byte("\n"+`{"type":"user","sessionId":"`+cli+`","entrypoint":"claude-desktop","message":"New message under B"}`+"\n")...)
	put(t, transcript, messages)
	r := plan(t, f, "all")
	if r.Updated != 1 || r.Added != 0 {
		t.Fatal("B edit not planned for A", r)
	}
	synchronize(t, f, "all")
	if object(t, a)["title"] != "Continued under B" || object(t, a)["sessionSettings"] == nil {
		t.Fatal("B workflow not available under A")
	}
	after, _ := os.ReadFile(transcript)
	if !bytes.Equal(messages, after) {
		t.Fatal("sync changed messages")
	}
	d = object(t, a)
	d["title"] = "Continued again under A"
	d["lastActivityAt"] = 200
	save(t, a, d)
	setTime(t, a, 3000000000)
	synchronize(t, f, "all")
	if object(t, b)["title"] != "Continued again under A" {
		t.Fatal("A edit not available under B")
	}
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("bidirectional sync not idempotent")
	}
	infoA, _ := os.Stat(a)
	infoB, _ := os.Stat(b)
	if !infoA.ModTime().Equal(infoB.ModTime()) {
		t.Fatal("mtime bounced")
	}
}
func TestUpdateBackupUndoPreservesBothVersions(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	old, _ := os.ReadFile(target)
	d := object(t, f.record)
	d["title"] = "New state"
	save(t, f.record, d)
	setTime(t, f.record, 2000000000)
	id := synchronize(t, f, "all")
	var j Journal
	manifest, _ := os.ReadFile(filepath.Join(f.data, backupDir, id, "manifest.json"))
	if e := json.Unmarshal(manifest, &j); e != nil {
		t.Fatal(e)
	}
	if len(j.Actions) != 1 || !bytes.Equal(j.Actions[0].Before, old) {
		t.Fatal("previous version not backed up")
	}
	newest, _ := os.ReadFile(target)
	if _, e := undo(f.data, id, true); e != nil {
		t.Fatal(e)
	}
	restored, _ := os.ReadFile(target)
	if !bytes.Equal(restored, old) {
		t.Fatal("undo did not restore original bytes")
	}
	q, _ := filepath.Glob(filepath.Join(f.data, backupDir, id, "undone", "quarantine-*.json"))
	if len(q) != 1 {
		t.Fatal("applied version lost")
	}
	b, _ := os.ReadFile(q[0])
	if !bytes.Equal(b, newest) {
		t.Fatal("newer bytes lost")
	}
	if _, e := undo(f.data, id, true); e != nil {
		t.Fatal("repeat undo failed", e)
	}
}
func TestDeletionPropagatesAndUndoRestores(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	synchronize(t, f, "all")
	before, _ := os.ReadFile(f.record)
	marker := filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_"+prior)
	put(t, marker, []byte("123"))
	r := plan(t, f, "all")
	if r.Quarantined != 2 || r.Tombstones != 1 || r.Added != 0 || r.Updated != 0 {
		t.Fatal("deletion plan incorrect", r)
	}
	id := synchronize(t, f, "all")
	for _, bucket := range []string{f.a, f.b} {
		if _, e := os.Stat(filepath.Join(f.data, "claude-code-sessions", bucket, filepath.Base(f.record))); !os.IsNotExist(e) {
			t.Fatal("deleted session still present")
		}
		if _, e := os.Stat(filepath.Join(f.data, "claude-code-sessions", bucket, "deleted_"+prior)); e != nil {
			t.Fatal("marker not propagated")
		}
	}
	if len(plan(t, f, "all").Actions) != 0 {
		t.Fatal("deleted session resurrected")
	}
	if _, e := undo(f.data, id, true); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(f.record)
	if !bytes.Equal(before, after) {
		t.Fatal("deleted original not restored")
	}
	if _, e := os.Stat(marker); e != nil {
		t.Fatal("user's original tombstone changed")
	}
}
func TestNewestTombstoneVersionPropagates(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	a := filepath.Join(f.data, "claude-code-sessions", f.a, "deleted_"+prior)
	b := filepath.Join(f.data, "claude-code-sessions", f.b, "deleted_"+prior)
	put(t, a, []byte("1"))
	put(t, b, []byte("2"))
	setTime(t, a, 1000000000)
	setTime(t, b, 2000000000)
	synchronize(t, f, "all")
	data, _ := os.ReadFile(a)
	if string(data) != "2" {
		t.Fatal("marker update not propagated")
	}
}
func TestSourceSelectionCanResolveTiedVersions(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	d := object(t, f.record)
	d["title"] = "Other version"
	save(t, target, d)
	setTime(t, f.record, 1000000000)
	setTime(t, target, 1000000000)
	r, e := scan(f.data, f.projects, f.a, f.b, "all")
	if e != nil || r.Updated != 1 {
		t.Fatal(e, r)
	}
	if _, e = apply(r); e != nil {
		t.Fatal(e)
	}
	if object(t, target)["title"] != "Resume workflow" {
		t.Fatal("selected source ignored")
	}
}
func TestChatUpdateKeepsTargetConfiguration(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	d := object(t, target)
	d["permissionMode"] = "plan"
	d["sessionSettings"] = map[string]any{"owner": "B"}
	save(t, target, d)
	setTime(t, target, 1500000000)
	d = object(t, f.record)
	d["title"] = "New history"
	d["sessionSettings"] = map[string]any{"owner": "A"}
	save(t, f.record, d)
	setTime(t, f.record, 2000000000)
	synchronize(t, f, "chat")
	got := object(t, target)
	if got["title"] != "New history" || got["permissionMode"] != "plan" || got["sessionSettings"].(map[string]any)["owner"] != "B" {
		t.Fatal("chat copied source configuration", got)
	}
	if len(plan(t, f, "chat").Actions) != 0 {
		t.Fatal("chat update not idempotent")
	}
}
func TestStaleTargetUpdateCannotBeApplied(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	d := object(t, f.record)
	d["title"] = "Source update"
	save(t, f.record, d)
	setTime(t, f.record, 2000000000)
	r := plan(t, f, "all")
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	d = object(t, target)
	d["title"] = "Concurrent edit"
	save(t, target, d)
	setTime(t, target, 3000000000)
	if _, e := apply(r); e == nil {
		t.Fatal("stale target overwritten")
	}
	if object(t, target)["title"] != "Concurrent edit" {
		t.Fatal("concurrent edit lost")
	}
}
func TestUpdateUndoRefusesModifiedTarget(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	d := object(t, f.record)
	d["title"] = "Source update"
	save(t, f.record, d)
	setTime(t, f.record, 2000000000)
	id := synchronize(t, f, "all")
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	d = object(t, target)
	d["title"] = "New user edit"
	save(t, target, d)
	if _, e := undo(f.data, id, true); e == nil {
		t.Fatal("undo overwrote edit")
	}
}

func TestForkProofSurvivesParentPointerAdvance(t *testing.T) {
	noProcesses(t)
	f := forkFixture(t)
	synchronize(t, f, "all")
	path := filepath.Join(f.data, "claude-code-sessions", f.a, "local_"+parentInternal+".json")
	d := object(t, path)
	next := "33333333-3333-4333-8333-333333333333"
	d["cliSessionId"] = next
	d["priorCliSessionIds"] = []string{parentCLI}
	save(t, path, d)
	setTime(t, path, time.Now().Add(time.Second).UnixNano())
	save(t, filepath.Join(f.projects, "project", next+".jsonl"), map[string]any{"sessionId": next, "entrypoint": "claude-desktop"})
	r := plan(t, f, "all")
	if r.MissingTranscripts != 0 {
		t.Fatal("inherited fork lost provenance after parent changed", r.Warnings)
	}
}

func TestTranscriptPointerAdvanceKeepsAllMessageFiles(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	target := filepath.Join(f.data, "claude-code-sessions", f.b, filepath.Base(f.record))
	oldPath := filepath.Join(f.projects, "project", cli+".jsonl")
	oldBytes, _ := os.ReadFile(oldPath)
	next := "44444444-4444-4444-8444-444444444444"
	nextBytes := append(append([]byte(nil), oldBytes...), []byte("\n"+`{"type":"assistant","sessionId":"`+next+`","entrypoint":"claude-desktop","message":"Continued session"}`+"\n")...)
	nextPath := filepath.Join(f.projects, "project", next+".jsonl")
	put(t, nextPath, nextBytes)
	d := object(t, target)
	d["cliSessionId"] = next
	d["priorCliSessionIds"] = []string{cli}
	d["lastActivityAt"] = 300
	save(t, target, d)
	setTime(t, target, 3000000000)
	synchronize(t, f, "all")
	if object(t, f.record)["cliSessionId"] != next {
		t.Fatal("old account still points to old transcript")
	}
	b, _ := os.ReadFile(oldPath)
	if !bytes.Equal(b, oldBytes) {
		t.Fatal("old messages changed")
	}
	b, _ = os.ReadFile(nextPath)
	if !bytes.Equal(b, nextBytes) {
		t.Fatal("new messages changed")
	}
}
func TestPartialUpdateUndoSkipsUnappliedActions(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	setTime(t, f.record, 1000000000)
	synchronize(t, f, "all")
	// Add a third existing account to make two update targets.
	third := "55555555-5555-4555-8555-555555555555/" + org
	os.MkdirAll(filepath.Join(f.data, "claude-code-sessions", third), 0700)
	synchronize(t, f, "all")
	d := object(t, f.record)
	d["title"] = "Source advanced"
	save(t, f.record, d)
	setTime(t, f.record, 3000000000)
	r := plan(t, f, "all")
	if r.Updated != 2 {
		t.Fatal(r)
	}
	calls := 0
	guard = func() error {
		calls++
		if calls >= 3 {
			return errors.New("test interruption")
		}
		return nil
	}
	id, e := apply(r)
	guard = func() error { return nil }
	if e == nil || id == "" {
		t.Fatal("expected recoverable partial apply", e, id)
	}
	if _, e := undo(f.data, id, true); e != nil {
		t.Fatal(e)
	}
	for _, bucket := range []string{f.b, third} {
		p := filepath.Join(f.data, "claude-code-sessions", bucket, filepath.Base(f.record))
		if object(t, p)["title"] != "Resume workflow" {
			t.Fatal("partial apply not reversed")
		}
	}
}
