//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func validTombstone(b []byte) bool {
	n, e := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return e == nil && n >= 0
}

type fileVersion struct {
	Path, Bucket string
	Data         []byte
	ModifiedNS   int64
}

func activity(r Record) int64 {
	n, _ := r.Data["lastActivityAt"].(json.Number)
	v, _ := n.Int64()
	return v
}
func newestRecord(records []Record, source string) (Record, error) {
	candidates := []Record{}
	for _, r := range records {
		if source == "" || r.Bucket == source {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return Record{}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.ModifiedNS != b.ModifiedNS {
			return a.ModifiedNS > b.ModifiedNS
		}
		if activity(a) != activity(b) {
			return activity(a) > activity(b)
		}
		return a.Path < b.Path
	})
	winner := candidates[0]
	for _, other := range candidates[1:] {
		if winner.ModifiedNS == other.ModifiedNS && activity(winner) == activity(other) && !bytes.Equal(winner.Payload, other.Payload) {
			return Record{}, errors.New("Conflicting versions with identical modification/activity times; choose --from explicitly")
		}
	}
	return winner, nil
}
func chatUpdate(source, target Record) []byte {
	var d map[string]any
	decode(target.Raw, &d)
	for _, k := range allowed {
		if v, ok := source.Data[k]; ok {
			d[k] = v
		} else {
			delete(d, k)
		}
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	return append(b, '\n')
}
func appendTombstoneActions(rep *Report, markers map[string][]fileVersion, target string) {
	keys := []string{}
	for key := range markers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		versions := markers[key]
		sort.Slice(versions, func(i, j int) bool {
			if versions[i].ModifiedNS != versions[j].ModifiedNS {
				return versions[i].ModifiedNS > versions[j].ModifiedNS
			}
			return versions[i].Path < versions[j].Path
		})
		winner := versions[0]
		for _, bucket := range rep.Buckets {
			if target != "" && bucket != target {
				continue
			}
			dst := filepath.Join("claude-code-sessions", bucket, key)
			var old *fileVersion
			for i := range versions {
				if versions[i].Bucket == bucket {
					old = &versions[i]
					break
				}
			}
			if old != nil && bytes.Equal(old.Data, winner.Data) {
				continue
			}
			a := Action{Kind: "tombstone", Source: winner.Path, Target: dst, Payload: json.RawMessage(winner.Data), SHA256: digest(winner.Data), ModifiedNS: winner.ModifiedNS}
			if old != nil {
				a.Before = old.Data
				a.BeforeSHA256 = digest(old.Data)
				a.BeforeModifiedNS = old.ModifiedNS
			}
			rep.Actions = append(rep.Actions, a)
		}
	}
}
func actionKind(k string) string {
	if k == "" {
		return "create"
	}
	return k
}
func snapshot(r *os.Root, path string) ([]byte, bool, error) {
	b, e := read(r, path, 10*1024*1024)
	if errors.Is(e, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, e == nil, e
}
func sameBefore(a Action, b []byte, exists bool) bool {
	if a.BeforeSHA256 == "" {
		return !exists
	}
	return exists && digest(b) == a.BeforeSHA256
}
func timestamp(r *os.Root, path string, ns int64) error {
	if ns == 0 {
		return nil
	}
	if e := safe(r, path, false); e != nil {
		return e
	}
	t := time.Unix(0, ns)
	if e := r.Chtimes(path, t, t); e != nil {
		return e
	}
	f, e := r.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func replace(r *os.Root, path string, b []byte, expected string) error {
	current, exists, e := snapshot(r, path)
	if e != nil {
		return e
	}
	if !exists || digest(current) != expected {
		return fmt.Errorf("target changed before replacement: %s", path)
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".session-sync-update-%d-%d", os.Getpid(), time.Now().UnixNano()))
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	current, exists, e = snapshot(r, path)
	if e != nil {
		return e
	}
	if !exists || digest(current) != expected {
		return fmt.Errorf("target changed during replacement: %s", path)
	}
	if e = r.Rename(tmp, path); e != nil {
		return e
	}
	return durableDir(r, dir)
}
func quarantine(r *os.Root, dir, path string, b []byte) error {
	h := sha256.Sum256([]byte(path))
	q := filepath.Join(dir, fmt.Sprintf("quarantine-%x.json", h[:8]))
	if e := add(r, q, b); e != nil {
		prior, e2 := read(r, q, 10*1024*1024)
		if e2 != nil || !bytes.Equal(prior, b) {
			return e
		}
	}
	return nil
}
func apply(rep Report) (string, error) {
	if e := guard(); e != nil {
		return "", e
	}
	r, e := realRoot(rep.DataDir)
	if e != nil {
		return "", e
	}
	defer r.Close()
	lock, e := lockRoot(r)
	if e != nil {
		return "", e
	}
	defer lock.Close()
	fresh, e := scan(rep.DataDir, rep.ProjectsDir, rep.SourceBucket, rep.TargetBucket, rep.CopyMode)
	if e != nil {
		return "", e
	}
	valid := map[string]Action{}
	for _, a := range fresh.Actions {
		valid[a.Target] = a
	}
	for _, a := range rep.Actions {
		v, ok := valid[a.Target]
		if !ok || v.Kind != a.Kind || v.SHA256 != a.SHA256 || v.BeforeSHA256 != a.BeforeSHA256 || v.ModifiedNS != a.ModifiedNS || v.BeforeModifiedNS != a.BeforeModifiedNS {
			return "", errors.New("plan became stale; run again")
		}
	}
	if len(rep.Actions) == 0 {
		return "", nil
	}
	if e = safe(r, backupDir, true); e != nil {
		return "", e
	}
	if e = r.MkdirAll(backupDir, 0700); e != nil {
		return "", e
	}
	info, e := r.Stat(backupDir)
	if e != nil {
		return "", e
	}
	if info.Mode().Perm() != 0700 {
		return "", errors.New("backup directory must have mode 0700")
	}
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join(backupDir, id)
	if e = r.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	j := Journal{Version: version, Created: time.Now().UTC().Format(time.RFC3339), Actions: []StoredAction{}}
	for _, a := range rep.Actions {
		j.Actions = append(j.Actions, StoredAction{Kind: a.Kind, Source: a.Source, Target: a.Target, SHA256: a.SHA256, Payload: []byte(a.Payload), Before: a.Before, BeforeSHA256: a.BeforeSHA256, ModifiedNS: a.ModifiedNS, BeforeModifiedNS: a.BeforeModifiedNS})
	}
	b, e := json.MarshalIndent(j, "", "  ")
	if e != nil {
		return id, e
	}
	if len(b) > 100*1024*1024 {
		return id, errors.New("backup manifest exceeds 100 MiB; limit the operation with --from/--to")
	}
	if e = add(r, filepath.Join(dir, "manifest.json"), b); e != nil {
		return id, e
	}
	if e = durableDir(r, backupDir); e != nil {
		return id, e
	}
	if e = durableDir(r, "."); e != nil {
		return id, e
	}
	for _, a := range rep.Actions {
		if e = guard(); e != nil {
			return id, e
		}
		current, exists, e := snapshot(r, a.Target)
		if e != nil {
			return id, e
		}
		if !sameBefore(a, current, exists) {
			return id, fmt.Errorf("target changed; use undo %s: %s", id, a.Target)
		}
		if a.BeforeSHA256 != "" {
			if e = quarantine(r, dir, a.Target, a.Before); e != nil {
				return id, e
			}
		}
		if a.Kind == "quarantine" {
			e = r.Remove(a.Target)
			if e == nil {
				e = durableDir(r, filepath.Dir(a.Target))
			}
		} else {
			if exists {
				e = replace(r, a.Target, a.Payload, a.BeforeSHA256)
			} else {
				e = add(r, a.Target, a.Payload)
			}
			if e == nil {
				e = timestamp(r, a.Target, a.ModifiedNS)
			}
		}
		if e != nil {
			return id, fmt.Errorf("partial operation; use undo %s: %w", id, e)
		}
	}
	return id, nil
}
func validJournalAction(a StoredAction) error {
	parts := strings.Split(a.Target, "/")
	if len(parts) != 4 || parts[0] != "claude-code-sessions" || !uuid.MatchString(parts[1]) || !uuid.MatchString(parts[2]) {
		return errors.New("invalid manifest target")
	}
	kind := actionKind(a.Kind)
	if kind != "create" && kind != "update" && kind != "tombstone" && kind != "quarantine" {
		return errors.New("unknown manifest action")
	}
	if kind == "tombstone" {
		id := strings.TrimSuffix(strings.TrimPrefix(parts[3], "deleted_"), ".json")
		if !strings.HasPrefix(parts[3], "deleted_") || !uuid.MatchString(id) {
			return errors.New("invalid deletion marker target")
		}
	} else {
		b := a.Payload
		if kind == "quarantine" {
			b = a.Before
		}
		if _, e := parseRecord(a.Target, filepath.Join(parts[1], parts[2]), b, "all"); e != nil {
			return e
		}
	}
	if kind != "quarantine" && digest(a.Payload) != a.SHA256 {
		return errors.New("invalid manifest checksum")
	}
	if a.BeforeSHA256 != "" && digest(a.Before) != a.BeforeSHA256 {
		return errors.New("invalid previous-version checksum")
	}
	if (kind == "update" || kind == "quarantine") && a.BeforeSHA256 == "" {
		return errors.New("missing previous version")
	}
	return nil
}
func undo(data, id string, write bool) ([]string, error) {
	if !regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z$`).MatchString(id) {
		return nil, errors.New("invalid backup ID")
	}
	r, e := realRoot(data)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if write {
		if e = guard(); e != nil {
			return nil, e
		}
		lock, e := lockRoot(r)
		if e != nil {
			return nil, e
		}
		defer lock.Close()
	}
	dir := filepath.Join(backupDir, id)
	b, e := read(r, filepath.Join(dir, "manifest.json"), 100*1024*1024)
	if e != nil {
		return nil, e
	}
	var j Journal
	if e = decode(b, &j); e != nil {
		return nil, e
	}
	switch j.Version {
	case "0.1.0", "0.1.1", "0.2.0", "0.3.0", version:
	default:
		return nil, errors.New("unsupported manifest version")
	}
	pending := []StoredAction{}
	paths := []string{}
	for _, a := range j.Actions {
		if e = validJournalAction(a); e != nil {
			return nil, e
		}
		cur, exists, e := snapshot(r, a.Target)
		if e != nil {
			return nil, e
		}
		kind := actionKind(a.Kind)
		if a.BeforeSHA256 != "" && exists && digest(cur) == a.BeforeSHA256 {
			continue
		}
		if kind == "quarantine" {
			if exists {
				return nil, fmt.Errorf("modified record retained; cannot undo %s", a.Target)
			}
		} else {
			if !exists {
				continue
			}
			if digest(cur) != a.SHA256 {
				return nil, fmt.Errorf("modified record retained; cannot undo %s", a.Target)
			}
		}
		pending = append(pending, a)
		paths = append(paths, a.Target)
	}
	if !write {
		return paths, nil
	}
	// Restore records before removing propagated tombstones by reversing apply order.
	for i := len(pending) - 1; i >= 0; i-- {
		a := pending[i]
		if e = guard(); e != nil {
			return paths, e
		}
		cur, exists, e := snapshot(r, a.Target)
		if e != nil {
			return paths, e
		}
		if actionKind(a.Kind) == "quarantine" {
			if exists {
				return paths, fmt.Errorf("target reappeared during undo: %s", a.Target)
			}
			if e = add(r, a.Target, a.Before); e != nil {
				return paths, e
			}
		} else {
			if !exists || digest(cur) != a.SHA256 {
				return paths, fmt.Errorf("record changed during undo: %s", a.Target)
			}
			// Keep the applied version separately from the original-version quarantine.
			undoDir := filepath.Join(dir, "undone")
			if e = safe(r, undoDir, true); e != nil {
				return paths, e
			}
			if e = r.MkdirAll(undoDir, 0700); e != nil {
				return paths, e
			}
			if e = quarantine(r, undoDir, a.Target, cur); e != nil {
				return paths, e
			}
			if a.BeforeSHA256 != "" {
				e = replace(r, a.Target, a.Before, a.SHA256)
			} else {
				e = r.Remove(a.Target)
				if e == nil {
					e = durableDir(r, filepath.Dir(a.Target))
				}
			}
			if e != nil {
				return paths, e
			}
		}
		if a.BeforeSHA256 != "" {
			if e = timestamp(r, a.Target, a.BeforeModifiedNS); e != nil {
				return paths, e
			}
		}
	}
	return paths, nil
}
