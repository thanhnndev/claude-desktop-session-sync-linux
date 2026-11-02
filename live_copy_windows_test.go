package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func treeHashes(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular file in live copy")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	return out, err
}

// Opt-in integration test: writes are confined to t.TempDir(), never the live
// profile. No actual account or process is created/changed/terminated.
func TestWindowsLiveDataCopyRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDE_SYNC_TEST_LIVE_COPY") != "1" {
		t.Skip("opt-in live-copy integration")
	}
	data, err := discover("")
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(os.Getenv("USERPROFILE"), ".claude", "projects")
	liveSessions := filepath.Join(data, "claude-code-sessions")
	before, err := treeHashes(liveSessions)
	if err != nil {
		t.Fatal(err)
	}
	beforeProjects, err := treeHashes(projects)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	if err := secureDirectory(temp); err != nil {
		t.Fatal(err)
	}
	copyData := filepath.Join(temp, "data")
	copyProjects := filepath.Join(temp, "projects")
	if err := os.Mkdir(copyData, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(copyData, "claude-code-sessions"), os.DirFS(liveSessions)); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copyProjects, os.DirFS(projects)); err != nil {
		t.Fatal(err)
	}
	originalCopy, err := treeHashes(filepath.Join(copyData, "claude-code-sessions"))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := scan(copyData, copyProjects, "", "", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Buckets) != 1 {
		t.Skip("this integration expects exactly one live bucket")
	}
	target := filepath.Join(accountB, org)
	if err := os.MkdirAll(filepath.Join(copyData, "claude-code-sessions", target), 0700); err != nil {
		t.Fatal(err)
	}
	noProcesses(t) // only the disposable copy is writable by this test
	for _, mode := range []string{"all", "chat"} {
		rep, err := scan(copyData, copyProjects, initial.Buckets[0], target, mode)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Actions) == 0 {
			t.Fatal("no proven live sessions to replay")
		}
		id, err := apply(rep)
		if err != nil {
			t.Fatal(err)
		}
		second, err := scan(copyData, copyProjects, initial.Buckets[0], target, mode)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Actions) != 0 {
			t.Fatal("live-copy sync not idempotent")
		}
		if _, err := undo(copyData, id, true); err != nil {
			t.Fatal(err)
		}
		restored, err := treeHashes(filepath.Join(copyData, "claude-code-sessions"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(originalCopy, restored) {
			t.Fatal("undo did not restore exact session bytes")
		}
		t.Logf("mode=%s replayed=%d undo=exact", mode, len(rep.Actions))
	}
	after, err := treeHashes(liveSessions)
	if err != nil {
		t.Fatal(err)
	}
	afterProjects, err := treeHashes(projects)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeProjects, afterProjects) {
		t.Fatal("live profile changed during integration test")
	}
	t.Log("live session and transcript hashes unchanged")
}
