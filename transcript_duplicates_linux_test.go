//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeMetadataStubDoesNotBlockSync(t *testing.T) {
	for _, mode := range []string{"all", "chat"} {
		for _, dir := range []string{"aaa-worktree", "zzz-worktree"} {
			t.Run(mode+"/"+dir, func(t *testing.T) {
				noProcesses(t)
				f := setup(t)
				full := filepath.Join(f.projects, "project", cli+".jsonl")
				stub := filepath.Join(f.projects, dir, cli+".jsonl")
				fullBytes, err := os.ReadFile(full)
				if err != nil {
					t.Fatal(err)
				}
				stubBytes := []byte(`{"type":"custom-title","sessionId":"` + cli + `","customTitle":"Worktree"}` + "\n" + `{"type":"cost-state","sessionId":"` + cli + `"}` + "\n")
				put(t, stub, stubBytes)
				r := plan(t, f, mode)
				if len(r.Actions) != 1 || r.MissingTranscripts != 0 || len(r.Warnings) != 0 {
					t.Fatal(r)
				}
				id, err := apply(r)
				if err != nil || id == "" {
					t.Fatal(id, err)
				}
				if len(plan(t, f, mode).Actions) != 0 {
					t.Fatal("not idempotent")
				}
				if _, err = undo(f.data, id, true); err != nil {
					t.Fatal(err)
				}
				for path, want := range map[string][]byte{full: fullBytes, stub: stubBytes} {
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatal("transcript changed", path, err)
					}
				}
			})
		}
	}
}

func TestIdenticalTranscriptCopiesAndUniqueOrphans(t *testing.T) {
	f := setup(t)
	b, err := os.ReadFile(filepath.Join(f.projects, "project", cli+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), b)
	if r := plan(t, f, "all"); len(r.Actions) != 1 || len(r.Warnings) != 0 {
		t.Fatal(r)
	}
	if err := os.Remove(f.record); err != nil {
		t.Fatal(err)
	}
	r := plan(t, f, "all")
	if len(r.Orphans) != 1 || r.Orphans[0] != cli {
		t.Fatal("duplicate orphan", r.Orphans)
	}
}

func TestConflictingHistoriesSkipOnlyAffectedSession(t *testing.T) {
	f := setup(t)
	save(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), map[string]any{"sessionId": cli, "entrypoint": "claude-desktop", "type": "assistant", "uuid": "different-history"})
	save(t, filepath.Join(f.data, "claude-code-sessions", f.a, "local_"+parentInternal+".json"), map[string]any{"sessionId": "local_" + parentInternal, "cliSessionId": parentCLI, "cwd": t.TempDir()})
	save(t, filepath.Join(f.projects, "project", parentCLI+".jsonl"), map[string]any{"sessionId": parentCLI, "entrypoint": "claude-desktop"})
	r := plan(t, f, "all")
	if len(r.Actions) != 1 || childPlanned(r) || r.MissingTranscripts != 1 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "Conflicting Desktop histories") {
		t.Fatal(r)
	}
}

func TestDuplicateFilesCannotCombineForkProvenance(t *testing.T) {
	f := forkFixture(t)
	// One copy carries the inherited parent, another carries the child's ID.
	// Neither proves the Desktop fork on its own.
	save(t, filepath.Join(f.projects, "project", cli+".jsonl"), map[string]any{"sessionId": parentCLI, "entrypoint": "claude-desktop"})
	save(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), map[string]any{"sessionId": cli, "type": "custom-title"})
	if r := plan(t, f, "all"); childPlanned(r) {
		t.Fatal("provenance merged across separate files")
	}
}

func TestForkWithMetadataStub(t *testing.T) {
	f := forkFixture(t)
	save(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), map[string]any{"sessionId": cli, "type": "custom-title"})
	save(t, filepath.Join(f.projects, "worktree", parentCLI+".jsonl"), map[string]any{"sessionId": parentCLI, "type": "custom-title"})
	if r := plan(t, f, "all"); !childPlanned(r) || len(r.LineageVerified) != 1 {
		t.Fatal(r)
	}
}

func TestReleaseSidecarBesideAnyCopyBlocksSession(t *testing.T) {
	for _, kind := range []string{"stub", "identical"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			path := filepath.Join(f.projects, "worktree", cli+".jsonl")
			b := []byte(`{"type":"custom-title","sessionId":"` + cli + `"}`)
			if kind == "identical" {
				var err error
				b, err = os.ReadFile(filepath.Join(f.projects, "project", cli+".jsonl"))
				if err != nil {
					t.Fatal(err)
				}
			}
			put(t, path, b)
			put(t, strings.TrimSuffix(path, ".jsonl")+".desktop-released.json", []byte("{}"))
			r := plan(t, f, "all")
			if len(r.Actions) != 0 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "Released transcript") {
				t.Fatal(r)
			}
		})
	}
}

func TestNewConflictingCopyInvalidatesApply(t *testing.T) {
	noProcesses(t)
	f := setup(t)
	r := plan(t, f, "all")
	save(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), map[string]any{"sessionId": cli, "entrypoint": "claude-desktop", "type": "assistant"})
	if id, err := apply(r); err == nil || id != "" {
		t.Fatal("stale plan applied", id, err)
	}
	if _, err := os.Stat(filepath.Join(f.data, r.Actions[0].Target)); !os.IsNotExist(err) {
		t.Fatal("target written", err)
	}
	if _, err := os.Stat(filepath.Join(f.data, backupDir)); !os.IsNotExist(err) {
		t.Fatal("backup created", err)
	}
}

func TestCLIOnlyCopyCannotBecomeDesktopThroughStub(t *testing.T) {
	f := setup(t)
	save(t, filepath.Join(f.projects, "project", cli+".jsonl"), map[string]any{"sessionId": cli, "entrypoint": "cli"})
	save(t, filepath.Join(f.projects, "worktree", cli+".jsonl"), map[string]any{"sessionId": cli, "type": "custom-title"})
	if r := plan(t, f, "all"); len(r.Actions) != 0 || r.MissingTranscripts != 1 {
		t.Fatal(r)
	}
}
