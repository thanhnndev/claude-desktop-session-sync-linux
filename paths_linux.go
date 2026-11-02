package main

import (
	"os"
	"path/filepath"
)

func dataCandidates(home string) []string {
	candidates := []string{filepath.Join(home, ".config", "Claude")}
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		candidates = append(candidates, filepath.Join(x, "Claude"))
	}
	return candidates
}
