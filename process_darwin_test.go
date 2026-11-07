package main

import "testing"

func TestDarwinProcessClassifier(t *testing.T) {
	for exe, want := range map[string]bool{
		"/Applications/Claude.app/Contents/MacOS/Claude":                                                                    true,
		"/Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/Contents/MacOS/Claude Helper (Renderer)": true,
		"/Users/me/Library/Application Support/Claude/claude-code/2.1.317/abc/claude.app/Contents/MacOS/claude":             true,
		"/Users/me/.local/bin/claude":                    true,
		"/Users/me/.local/bin/claude-session-sync":       false,
		"/Applications/Safari.app/Contents/MacOS/Safari": false,
	} {
		if got := isClaudeExecutable(exe, nil); got != want {
			t.Fatal(exe, got)
		}
	}
}

func TestDarwinListsRunningClaude(t *testing.T) {
	if _, e := listClaudeProcesses(); e != nil {
		t.Fatal(e)
	}
}
