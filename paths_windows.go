package main

import (
	"os"
	"path/filepath"
)

func dataCandidates(home string) []string {
	var candidates []string
	if roaming := os.Getenv("APPDATA"); filepath.IsAbs(roaming) {
		candidates = append(candidates, filepath.Join(roaming, "Claude"))
	}
	if local := os.Getenv("LOCALAPPDATA"); filepath.IsAbs(local) {
		for _, pattern := range []string{
			filepath.Join(local, "Claude-Data", "*"),
			filepath.Join(local, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude"),
			filepath.Join(local, "Packages", "Claude_*", "LocalCache", "Local", "Claude"),
		} {
			matches, _ := filepath.Glob(pattern)
			candidates = append(candidates, matches...)
		}
	}
	return candidates
}

func systemAlias(p string) string { return p }
