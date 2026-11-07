//go:build linux || windows || darwin

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func projectFixture(t *testing.T, f fixture) map[string]any {
	t.Helper()
	p := map[string]any{"id": internal, "name": "Project", "target": map[string]any{"kind": "wsl", "cwd": "/home/test/project", "wslDistro": "archlinux"}, "settings": map[string]any{"modelId": "test-model", "effort": "high", "worktree": true}, "createdAt": 1, "lastUsedAt": 2}
	doc := map[string]any{"unknownRoot": "keep", "byAccount": map[string]any{accountA: map[string]any{"presets": []any{p}, "builtIns": map[string]any{"local": map[string]any{"settings": "source"}}}, accountB: map[string]any{"presets": []any{}, "builtIns": map[string]any{"local": map[string]any{"settings": "target"}}, "seededFromDraft": true}}}
	save(t, filepath.Join(f.data, presetsFile), doc)
	return doc
}

func TestSavedProjectsFullSyncAndExactUndo(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	projectFixture(t, f)
	path := filepath.Join(f.data, presetsFile)
	before, _ := os.ReadFile(path)
	rep := plan(t, f, "all")
	if rep.ProjectsAdded != 1 || rep.ProjectsUpdated != 0 {
		t.Fatal("saved WSL project not planned")
	}
	id, err := apply(rep)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	doc, accounts, err := presetAccounts(after)
	if err != nil {
		t.Fatal(err)
	}
	source := accounts[accountA].(map[string]any)["presets"].([]any)
	target := accounts[accountB].(map[string]any)["presets"].([]any)
	x, _ := json.Marshal(source)
	y, _ := json.Marshal(target)
	if !bytes.Equal(x, y) {
		t.Fatal("project target/settings were not preserved")
	}
	if doc["unknownRoot"] != "keep" || accounts[accountB].(map[string]any)["seededFromDraft"] != true {
		t.Fatal("unrelated metadata changed")
	}
	if rep := plan(t, f, "all"); len(rep.Actions) != 0 {
		t.Fatal("not idempotent")
	}
	if _, err := undo(f.data, id, true); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if !bytes.Equal(before, restored) {
		t.Fatal("project undo not byte exact")
	}
}

func TestChatOnlyDoesNotCopySavedProjects(t *testing.T) {
	f := setup(t)
	projectFixture(t, f)
	rep := plan(t, f, "chat")
	if rep.ProjectsAdded != 0 {
		t.Fatal("chat mode copied saved projects")
	}
	for _, a := range rep.Actions {
		if a.Target == presetsFile {
			t.Fatal("chat mode changes project registry")
		}
	}
}

func TestSavedProjectsNewestVersionAndStalePlan(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	doc := projectFixture(t, f)
	accounts := doc["byAccount"].(map[string]any)
	accounts[accountB].(map[string]any)["presets"] = []any{map[string]any{"id": internal, "name": "Updated", "target": map[string]any{"kind": "wsl", "cwd": "/home/test/project", "wslDistro": "archlinux"}, "settings": map[string]any{"modelId": "new"}, "lastUsedAt": 3}}
	save(t, filepath.Join(f.data, presetsFile), doc)
	rep := plan(t, f, "all")
	if rep.ProjectsUpdated != 1 {
		t.Fatal("newer project not propagated")
	}
	accounts[accountB].(map[string]any)["extra"] = "concurrent edit"
	save(t, filepath.Join(f.data, presetsFile), doc)
	if _, err := apply(rep); err == nil {
		t.Fatal("stale project plan applied")
	}
}

func TestProjectUndoRefusesModifiedRegistry(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	projectFixture(t, f)
	id, err := apply(plan(t, f, "all"))
	if err != nil {
		t.Fatal(err)
	}
	doc := object(t, filepath.Join(f.data, presetsFile))
	doc["concurrent"] = "keep"
	save(t, filepath.Join(f.data, presetsFile), doc)
	if _, err := undo(f.data, id, true); err == nil {
		t.Fatal("modified project registry overwritten")
	}
}

func TestProjectJournalRejectsTraversalAndMissingOriginal(t *testing.T) {
	for _, a := range []StoredAction{{Kind: "projects", Target: "../ccd-session-presets.json"}, {Kind: "projects", Target: presetsFile, Source: presetsFile}} {
		if err := validJournalAction(a); err == nil {
			t.Fatal("unsafe project manifest accepted")
		}
	}
}
