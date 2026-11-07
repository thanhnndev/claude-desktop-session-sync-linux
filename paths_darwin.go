package main

import (
	"path/filepath"
	"strings"
)

// Claude Desktop (Electron) keeps its userData under Application Support on macOS.
func dataCandidates(home string) []string {
	return []string{filepath.Join(home, "Library", "Application Support", "Claude")}
}

// /tmp, /var and /etc are root-owned system symlinks into /private on macOS.
// Resolving only these well-known aliases keeps the "no symlink component"
// rule intact for every user-controlled path.
func systemAlias(p string) string {
	for _, a := range []string{"/tmp", "/var", "/etc"} {
		if p == a || strings.HasPrefix(p, a+"/") {
			return "/private" + p
		}
	}
	return p
}
