//go:build linux || windows || darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const presetsFile = "ccd-session-presets.json"

func presetAccounts(b []byte) (map[string]any, map[string]any, error) {
	var doc map[string]any
	if err := decode(b, &doc); err != nil {
		return nil, nil, err
	}
	accounts, ok := doc["byAccount"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("project presets: missing byAccount object")
	}
	for id, raw := range accounts {
		if !uuid.MatchString(id) {
			return nil, nil, errors.New("project presets: invalid account ID")
		}
		account, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, errors.New("project presets: invalid account object")
		}
		if raw, exists := account["presets"]; exists {
			list, ok := raw.([]any)
			if !ok {
				return nil, nil, errors.New("project presets: invalid presets array")
			}
			seen := map[string]bool{}
			for _, raw := range list {
				p, ok := raw.(map[string]any)
				if !ok {
					return nil, nil, errors.New("project presets: invalid preset")
				}
				id := str(p, "id")
				if !uuid.MatchString(id) || seen[id] {
					return nil, nil, errors.New("project presets: invalid/duplicate preset ID")
				}
				seen[id] = true
				target, ok := p["target"].(map[string]any)
				if !ok || str(target, "kind") == "" || str(target, "cwd") == "" {
					return nil, nil, errors.New("project presets: missing target kind/cwd")
				}
			}
		}
	}
	return doc, accounts, nil
}

func presetTime(p map[string]any) int64 {
	var newest int64
	for _, key := range []string{"updatedAt", "lastUsedAt", "createdAt"} {
		if n, ok := p[key].(json.Number); ok {
			v, _ := n.Int64()
			if v > newest {
				newest = v
			}
		}
	}
	return newest
}
func presetLocation(p map[string]any) string {
	t := p["target"].(map[string]any)
	// Keep WSL distro and Linux path case intact. Local Windows paths use
	// case-insensitive comparison so one folder is not listed twice.
	kind, cwd := str(t, "kind"), str(t, "cwd")
	if strings.Contains(cwd, ":\\") {
		cwd = strings.ToLower(cwd)
	}
	return kind + "\x00" + str(t, "wslDistro") + "\x00" + cwd
}

// Merge saved Code projects between existing account identities. Presets include
// the full target (local/WSL), folder, model, effort and worktree settings. This
// does not copy source trees, rewrite paths, or create an account identity.
func appendProjectActions(rep *Report, r *os.Root, source, target string) error {
	b, err := read(r, presetsFile, 10*1024*1024)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, accounts, err := presetAccounts(b)
	if err != nil {
		return err
	}
	ids := []string{}
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	accountOf := func(bucket string) string { return strings.Split(filepath.ToSlash(bucket), "/")[0] }
	sourceAccount, targetAccount := accountOf(source), accountOf(target)
	winners := map[string]map[string]any{}
	conflicts := map[string]bool{}
	for _, accountID := range ids {
		if source != "" && accountID != sourceAccount {
			continue
		}
		account := accounts[accountID].(map[string]any)
		list, _ := account["presets"].([]any)
		for _, raw := range list {
			p := raw.(map[string]any)
			id := str(p, "id")
			old, exists := winners[id]
			if !exists || presetTime(p) > presetTime(old) {
				winners[id] = p
				conflicts[id] = false
			} else if presetTime(p) == presetTime(old) {
				x, _ := json.Marshal(p)
				y, _ := json.Marshal(old)
				if digest(x) != digest(y) {
					conflicts[id] = true
				}
			}
		}
	}
	presetIDs := []string{}
	for id := range winners {
		presetIDs = append(presetIDs, id)
	}
	sort.Strings(presetIDs)
	changed := false
	for _, accountID := range ids {
		if target != "" && accountID != targetAccount {
			continue
		}
		account := accounts[accountID].(map[string]any)
		list, _ := account["presets"].([]any)
		byID := map[string]int{}
		locations := map[string]string{}
		for i, raw := range list {
			p := raw.(map[string]any)
			byID[str(p, "id")] = i
			locations[presetLocation(p)] = str(p, "id")
		}
		for _, id := range presetIDs {
			if conflicts[id] {
				rep.Warnings = append(rep.Warnings, "Conflicting saved project versions; choose --from: "+id)
				continue
			}
			p := winners[id]
			if other, exists := locations[presetLocation(p)]; exists && other != id {
				rep.Warnings = append(rep.Warnings, "Saved project already exists with another ID; retained destination project: "+id)
				continue
			}
			if i, exists := byID[id]; exists {
				old := list[i].(map[string]any)
				x, _ := json.Marshal(old)
				y, _ := json.Marshal(p)
				if digest(x) == digest(y) {
					continue
				}
				if source == "" && presetTime(old) >= presetTime(p) {
					continue
				}
				list[i] = p
				rep.ProjectsUpdated++
			} else {
				list = append(list, p)
				byID[id] = len(list) - 1
				rep.ProjectsAdded++
			}
			locations[presetLocation(p)] = id
			changed = true
		}
		if changed && len(list) > 0 {
			account["presets"] = list
		}
	}
	if !changed {
		return nil
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	info, err := r.Stat(presetsFile)
	if err != nil {
		return err
	}
	rep.Actions = append(rep.Actions, Action{Kind: "projects", Source: presetsFile, Target: presetsFile, Before: b, BeforeSHA256: digest(b), Payload: payload, SHA256: digest(payload), ModifiedNS: info.ModTime().UnixNano(), BeforeModifiedNS: info.ModTime().UnixNano()})
	return nil
}

func validProjectJournal(a StoredAction) error {
	if a.Kind != "projects" || a.Target != presetsFile || a.Source != presetsFile || a.BeforeSHA256 == "" {
		return errors.New("invalid project manifest action")
	}
	if digest(a.Payload) != a.SHA256 || digest(a.Before) != a.BeforeSHA256 {
		return errors.New("invalid project manifest checksum")
	}
	if _, _, err := presetAccounts(a.Payload); err != nil {
		return fmt.Errorf("invalid project replacement: %w", err)
	}
	if _, _, err := presetAccounts(a.Before); err != nil {
		return fmt.Errorf("invalid project original: %w", err)
	}
	return nil
}
