//go:build linux

// An offline, bidirectional synchronizer for Claude Desktop Code metadata.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const version = "0.3.1"

var guard = processGuard

const backupDir = ".linux-session-sync-backups"

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var allowed = strings.Fields("sessionId cliSessionId unarchivedCliSessionId priorCliSessionIds forkedFromSessionId cwd originCwd worktreePath worktreeName folderWorktrees worktreeLazy worktreePinned sourceBranch branch gitAnchors gitAnchorsFolderRealpath gitAnchorsLookupOnly createdAt lastActivityAt lastFocusedAt title titleSource isArchived model effort")

type Record struct {
	Raw                []byte
	ModifiedNS         int64
	Parent             string
	Excluded           []string
	Path, Bucket, Name string
	Data               map[string]any
	IDs                []string
	Payload            []byte
}
type Action struct {
	Kind             string          `json:"kind"`
	Before           []byte          `json:"previous_bytes,omitempty"`
	BeforeSHA256     string          `json:"previous_sha256,omitempty"`
	ModifiedNS       int64           `json:"modified_ns"`
	BeforeModifiedNS int64           `json:"previous_modified_ns,omitempty"`
	Source           string          `json:"source"`
	Target           string          `json:"target"`
	SHA256           string          `json:"sha256"`
	Payload          json.RawMessage `json:"payload"`
}
type Report struct {
	SourceBucket       string   `json:"source_bucket,omitempty"`
	TargetBucket       string   `json:"target_bucket,omitempty"`
	Added              int      `json:"added"`
	Updated            int      `json:"updated"`
	Quarantined        int      `json:"quarantined"`
	Tombstones         int      `json:"tombstones"`
	LineageVerified    []string `json:"lineage_verified_sessions"`
	FullRecord         bool     `json:"full_record"`
	CopyMode           string   `json:"copy_mode"`
	Excluded           []string `json:"excluded_sensitive_fields"`
	Version            string   `json:"version"`
	DataDir            string   `json:"data_dir"`
	ProjectsDir        string   `json:"projects_dir"`
	Buckets            []string `json:"buckets"`
	Records            int      `json:"records"`
	Actions            []Action `json:"actions"`
	Warnings           []string `json:"warnings"`
	Orphans            []string `json:"desktop_transcripts_without_pointer"`
	MissingTranscripts int      `json:"missing_transcripts"`
}
type StoredAction struct {
	Kind             string `json:"kind,omitempty"`
	Before           []byte `json:"previous_bytes,omitempty"`
	BeforeSHA256     string `json:"previous_sha256,omitempty"`
	ModifiedNS       int64  `json:"modified_ns,omitempty"`
	BeforeModifiedNS int64  `json:"previous_modified_ns,omitempty"`
	Source           string `json:"source"`
	Target           string `json:"target"`
	SHA256           string `json:"sha256"`
	Payload          []byte `json:"payload_base64"`
}
type Journal struct {
	Version string         `json:"version"`
	Created string         `json:"created"`
	Actions []StoredAction `json:"actions"`
}

func digest(b []byte) string {
	var compact bytes.Buffer
	if json.Compact(&compact, b) == nil {
		b = compact.Bytes()
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func str(d map[string]any, k string) string { s, _ := d[k].(string); return s }
func realRoot(p string) (*os.Root, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	// Refuse every symlink component, including the supplied root.
	cur := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		cur = filepath.Join(cur, part)
		i, e := os.Lstat(cur)
		if e != nil {
			return nil, e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink refused: %s", cur)
		}
	}
	return os.OpenRoot(p)
}
func safe(r *os.Root, p string, missing bool) error {
	if !filepath.IsLocal(p) {
		return fmt.Errorf("non-local path refused: %s", p)
	}
	cur := ""
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		i, e := r.Lstat(cur)
		if missing && errors.Is(e, fs.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", cur)
		}
		if cur != p && !i.IsDir() {
			return fmt.Errorf("not a directory: %s", cur)
		}
	}
	return nil
}
func read(r *os.Root, p string, limit int64) ([]byte, error) {
	if e := safe(r, p, false); e != nil {
		return nil, e
	}
	i0, e := r.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !i0.Mode().IsRegular() {
		return nil, fmt.Errorf("non-regular file: %s", p)
	}
	f, e := r.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > limit {
		return nil, fmt.Errorf("non-regular or oversized file: %s", p)
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file grew: %s", p)
	}
	return b, e
}
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

// Secret containers are excluded as units so embedded MCP headers/env and runtime
// launch configuration never silently carry authentication into another account.
var secretKey = regexp.MustCompile(`(?i)(token|password|passwd|secret|credential|authorization|cookie|apikey|api_key|privatekey|private_key|clientsecret|client_secret)`)
var secretValue = regexp.MustCompile(`(?i)(sk-ant-|sk-proj-|gh[pousr]_[A-Za-z0-9]|github_pat_|bearer\s+|-----BEGIN.*PRIVATE KEY|[a-z][a-z0-9+.-]*://[^/\s:]+:[^/\s@]+@|--(?:api[-_]?key|token|password|secret)[=\s]|(?:api[_-]?key|access[_-]?token|password|secret)\s*[:=])`)

func scrub(v any, path string) (any, []string) {
	excluded := []string{}
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			lower := strings.ToLower(k)
			// Opaque snapshots may encode arbitrary environment and launch secrets.
			opaque := lower == "spawnseed" || lower == "promptappendsnapshot" || lower == "toolsurfacesnapshot" || lower == "env" || lower == "environment" || lower == "environmentvariables" || lower == "headers" || lower == "args" || lower == "command"
			if secretKey.MatchString(k) || opaque {
				excluded = append(excluded, p)
				continue
			}
			y, dropped := scrub(x[k], p)
			excluded = append(excluded, dropped...)
			if y != nil || x[k] == nil {
				out[k] = y
			}
		}
		return out, excluded
	case []any:
		out := []any{}
		for i, item := range x {
			y, drop := scrub(item, fmt.Sprintf("%s[%d]", path, i))
			if len(drop) > 0 { // Skip a whole config element rather than leave a broken authenticated endpoint.
				excluded = append(excluded, drop...)
				continue
			}
			out = append(out, y)
		}
		return out, excluded
	case string:
		if secretValue.MatchString(x) {
			return nil, []string{path}
		}
		return x, nil
	default:
		return v, nil
	}
}

func parseRecord(path, bucket string, b []byte, mode string) (Record, error) {
	r := Record{Path: path, Bucket: bucket, Name: filepath.Base(path), Raw: append([]byte(nil), b...)}
	var d map[string]any
	if e := decode(b, &d); e != nil {
		return r, e
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.Name, "local_"), ".json")
	if !uuid.MatchString(id) || str(d, "sessionId") != "local_"+id {
		return r, errors.New("sessionId does not match filename")
	}
	if !uuid.MatchString(str(d, "cliSessionId")) && !uuid.MatchString(str(d, "unarchivedCliSessionId")) {
		return r, errors.New("no valid transcript ID")
	}
	if !filepath.IsAbs(str(d, "cwd")) {
		return r, errors.New("cwd must be absolute")
	}
	for _, k := range []string{"sshConfig", "wslConfig", "remoteHost", "remoteHostId"} {
		if v, ok := d[k]; ok && v != nil {
			return r, errors.New("non-local session refused")
		}
	}
	r.Parent = str(d, "forkedFromSessionId")
	if r.Parent != "" && (!strings.HasPrefix(r.Parent, "local_") || !uuid.MatchString(strings.TrimPrefix(r.Parent, "local_"))) {
		r.Parent = ""
	}
	if spawn, ok := d["spawnedFrom"].(map[string]any); ok && r.Parent != "" && str(spawn, "sessionId") != r.Parent {
		r.Parent = ""
	}
	r.IDs = []string{id}
	for _, k := range []string{"cliSessionId", "unarchivedCliSessionId"} {
		if s := str(d, k); s != "" {
			if !uuid.MatchString(s) {
				return r, errors.New("invalid alias")
			}
			r.IDs = append(r.IDs, s)
		}
	}
	if v, ok := d["priorCliSessionIds"]; ok {
		a, ok := v.([]any)
		if !ok {
			return r, errors.New("invalid priorCliSessionIds")
		}
		for _, v := range a {
			s, ok := v.(string)
			if !ok || !uuid.MatchString(s) {
				return r, errors.New("invalid prior alias")
			}
			r.IDs = append(r.IDs, s)
		}
	}
	clean := map[string]any{}
	for _, k := range allowed {
		if v, ok := d[k]; ok {
			clean[k] = v
		}
	}
	for _, k := range []string{"originCwd", "gitAnchorsFolderRealpath", "title", "titleSource", "model", "effort"} {
		if v, ok := clean[k]; ok {
			if _, ok := v.(string); !ok {
				return r, fmt.Errorf("invalid %s", k)
			}
		}
	}
	for _, k := range []string{"createdAt", "lastActivityAt", "lastFocusedAt"} {
		if v, ok := clean[k]; ok {
			n, ok := v.(json.Number)
			if !ok {
				return r, fmt.Errorf("invalid %s", k)
			}
			if _, e := n.Int64(); e != nil {
				return r, e
			}
		}
	}
	for _, k := range []string{"isArchived", "gitAnchorsLookupOnly"} {
		if v, ok := clean[k]; ok {
			if _, ok := v.(bool); !ok {
				return r, fmt.Errorf("invalid %s", k)
			}
		}
	}
	if v, ok := clean["gitAnchors"]; ok {
		a, ok := v.([]any)
		if !ok {
			return r, errors.New("invalid anchors")
		}
		sanitized := []any{}
		for _, v := range a {
			m, ok := v.(map[string]any)
			if !ok {
				return r, errors.New("invalid anchor")
			}
			c := map[string]any{}
			for _, k := range []string{"gitRoot", "commonDir", "gitDir"} {
				if v, ok := m[k]; ok {
					s, ok := v.(string)
					if !ok || !filepath.IsAbs(s) {
						return r, errors.New("invalid anchor path")
					}
					c[k] = s
				}
			}
			sanitized = append(sanitized, c)
		}
		clean["gitAnchors"] = sanitized
	}
	// All preserves the workflow state; chat deliberately resets session configuration.
	if mode == "all" {
		clean = d
	} else {
		clean["permissionMode"] = "default"
		sanitized, excluded := scrub(clean, "")
		clean = sanitized.(map[string]any)
		r.Excluded = excluded
	}

	r.Data = clean
	r.Payload, _ = json.MarshalIndent(clean, "", "  ")
	r.Payload = append(r.Payload, '\n')
	if mode == "all" {
		r.Data = d
		r.Payload = append([]byte(nil), b...)
		r.Excluded = nil
	}
	return r, nil
}
func list(r *os.Root, p string) ([]fs.DirEntry, error) {
	if e := safe(r, p, false); e != nil {
		return nil, e
	}
	return fs.ReadDir(r.FS(), p)
}

// A fork may retain its parent's transcript prefix before any child turns exist.
// Record only identity/provenance fields while streaming the complete JSONL file.
type Transcript struct {
	Path             string
	SessionIDs       map[string]bool
	DesktopIDs       map[string]bool
	OtherEntrypoints map[string]bool
}

func inspectTranscript(h io.Reader, path string) (Transcript, error) {
	tr := Transcript{Path: path, SessionIDs: map[string]bool{}, DesktopIDs: map[string]bool{}, OtherEntrypoints: map[string]bool{}}
	rd := bufio.NewReader(h)
	for line := 1; ; line++ {
		b, e := rd.ReadBytes('\n')
		if e != nil && e != io.EOF {
			return tr, fmt.Errorf("read transcript %s at line %d: %w", path, line, e)
		}
		if len(bytes.TrimSpace(b)) > 0 {
			var d struct {
				SessionID  string `json:"sessionId"`
				Entrypoint string `json:"entrypoint"`
			}
			if err := json.Unmarshal(b, &d); err != nil {
				return tr, fmt.Errorf("invalid transcript JSON at %s line %d: %w", path, line, err)
			}
			if uuid.MatchString(d.SessionID) {
				tr.SessionIDs[d.SessionID] = true
				if d.Entrypoint == "claude-desktop" {
					tr.DesktopIDs[d.SessionID] = true
				} else if d.Entrypoint != "" {
					tr.OtherEntrypoints[d.SessionID] = true
				}
			}
		}
		if e == io.EOF {
			return tr, nil
		}
	}
}
func transcriptIndex(p string) (map[string]Transcript, []string, error) {
	r, e := realRoot(p)
	if e != nil {
		return nil, nil, e
	}
	defer r.Close()
	idx := map[string]Transcript{}
	desktop := []string{}
	dirs, e := list(r, ".")
	if e != nil {
		return nil, nil, e
	}
	for _, dir := range dirs {
		if dir.Type()&os.ModeSymlink != 0 {
			return nil, nil, errors.New("symlink in projects root")
		}
		if !dir.IsDir() {
			continue
		}
		files, e := list(r, dir.Name())
		if e != nil {
			return nil, nil, e
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".jsonl")
			if !uuid.MatchString(id) {
				continue
			}
			path := filepath.Join(dir.Name(), f.Name())
			if e := safe(r, path, false); e != nil {
				return nil, nil, e
			}
			info, e := r.Lstat(path)
			if e != nil {
				return nil, nil, e
			}
			if !info.Mode().IsRegular() {
				return nil, nil, errors.New("non-regular transcript")
			}
			h, e := r.Open(path)
			if e != nil {
				return nil, nil, e
			}
			tr, e := inspectTranscript(h, path)
			closeErr := h.Close()
			if e != nil {
				return nil, nil, e
			}
			if closeErr != nil {
				return nil, nil, closeErr
			}
			if _, ok := idx[id]; ok {
				return nil, nil, fmt.Errorf("ambiguous transcript ID %s", id)
			}
			idx[id] = tr
			if tr.DesktopIDs[id] && !tr.OtherEntrypoints[id] {
				desktop = append(desktop, id)
			}
		}
	}
	sort.Strings(desktop)
	return idx, desktop, nil
}
func transcriptID(rec Record) string {
	if s := str(rec.Data, "cliSessionId"); s != "" {
		return s
	}
	return str(rec.Data, "unarchivedCliSessionId")
}

// A bare Desktop marker from an unrelated inherited session is insufficient.
// Forks need a real parent pointer, a proven Desktop parent, inherited parent
// identity in the child's transcript, and a row carrying the child's own ID.
func transcriptProof(rec Record, index map[string]Transcript, parents map[string][]Record, visiting map[string]bool) string {
	sid := transcriptID(rec)
	tr, ok := index[sid]
	if !ok || !tr.SessionIDs[sid] || tr.OtherEntrypoints[sid] {
		return ""
	}
	if tr.DesktopIDs[sid] {
		return "direct"
	}
	if rec.Parent == "" || visiting[rec.Name] || len(visiting) >= 64 {
		return ""
	}
	candidates := parents[rec.Parent]
	if len(candidates) == 0 {
		return ""
	}
	parent, chooseErr := newestRecord(candidates, "")
	if chooseErr != nil {
		return ""
	}
	visiting[rec.Name] = true
	defer delete(visiting, rec.Name)
	if transcriptProof(parent, index, parents, visiting) == "" {
		return ""
	}
	for _, alias := range parent.IDs {
		if tr.DesktopIDs[alias] && !tr.OtherEntrypoints[alias] {
			return "fork"
		}
	}
	return ""
}

func scan(data, projects, source, target, mode string) (Report, error) {
	rep := Report{Version: version, DataDir: data, ProjectsDir: projects, CopyMode: mode, SourceBucket: source, TargetBucket: target, FullRecord: mode == "all", Excluded: []string{}, Actions: []Action{}, Warnings: []string{}, Orphans: []string{}, LineageVerified: []string{}}
	r, e := realRoot(data)
	if e != nil {
		return rep, e
	}
	defer r.Close()
	accounts, e := list(r, "claude-code-sessions")
	if e != nil {
		return rep, e
	}
	records := []Record{}
	tombs := map[string]bool{}
	markers := map[string][]fileVersion{}
	blocked := map[string]bool{}
	names := map[string]bool{}
	for _, a := range accounts {
		if a.Type()&os.ModeSymlink != 0 {
			return rep, errors.New("symlink account refused")
		}
		if !a.IsDir() || !uuid.MatchString(a.Name()) {
			continue
		}
		orgs, e := list(r, filepath.Join("claude-code-sessions", a.Name()))
		if e != nil {
			return rep, e
		}
		for _, o := range orgs {
			if o.Type()&os.ModeSymlink != 0 {
				return rep, errors.New("symlink org refused")
			}
			if !o.IsDir() || !uuid.MatchString(o.Name()) {
				continue
			}
			bucket := filepath.Join(a.Name(), o.Name())
			rep.Buckets = append(rep.Buckets, bucket)
			dir := filepath.Join("claude-code-sessions", bucket)
			files, e := list(r, dir)
			if e != nil {
				return rep, e
			}
			for _, f := range files {
				name := f.Name()
				if !strings.HasPrefix(name, "local_") && !strings.HasPrefix(name, "deleted_") {
					continue
				}
				path := filepath.Join(dir, name)
				b, e := read(r, path, 10*1024*1024)
				if e != nil {
					return rep, e
				}
				if strings.HasPrefix(name, "deleted_") {
					id := strings.TrimSuffix(strings.TrimPrefix(name, "deleted_"), ".json")
					if !uuid.MatchString(id) {
						return rep, fmt.Errorf("unrecognized tombstone: %s", path)
					}
					if !validTombstone(b) {
						return rep, fmt.Errorf("invalid deletion marker timestamp: %s", path)
					}
					tombs[id] = true
					info, e := r.Lstat(path)
					if e != nil {
						return rep, e
					}
					markers[name] = append(markers[name], fileVersion{Path: path, Bucket: bucket, Data: b, ModifiedNS: info.ModTime().UnixNano()})
					continue
				}
				if !strings.HasSuffix(name, ".json") {
					continue
				}
				names[path] = true
				rec, e := parseRecord(path, bucket, b, mode)
				if e != nil {
					blocked[name] = true
					rep.Warnings = append(rep.Warnings, "Invalid record blocks this filename: "+path)
					continue
				}
				for _, field := range rec.Excluded {
					rep.Excluded = append(rep.Excluded, path+":"+field)
				}
				info, e := r.Lstat(path)
				if e != nil {
					return rep, e
				}
				rec.ModifiedNS = info.ModTime().UnixNano()
				records = append(records, rec)
			}
		}
	}
	if len(rep.Buckets) == 0 {
		return rep, errors.New("no existing account/org buckets; sign in in Desktop first")
	}
	for _, sel := range []string{source, target} {
		if sel != "" {
			found := false
			for _, b := range rep.Buckets {
				if sel == b {
					found = true
				}
			}
			if !found {
				return rep, fmt.Errorf("unknown bucket: %s", sel)
			}
		}
	}
	transcripts, desktop, e := transcriptIndex(projects)
	if e != nil {
		return rep, e
	}
	rep.Records = len(records)
	known := map[string]bool{}
	groups := map[string][]Record{}
	parents := map[string][]Record{}
	for _, rec := range records {
		for _, id := range rec.IDs {
			known[id] = true
		}
		groups[rec.Name] = append(groups[rec.Name], rec)
		parents[strings.TrimSuffix(rec.Name, ".json")] = append(parents[strings.TrimSuffix(rec.Name, ".json")], rec)
	}
	for _, id := range desktop {
		if !known[id] {
			rep.Orphans = append(rep.Orphans, id)
		}
	}
	keys := []string{}
	for name := range groups {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		group := groups[name]
		if blocked[name] {
			continue
		}
		deleted := false
		for _, rec := range group {
			for _, id := range rec.IDs {
				if tombs[id] {
					deleted = true
				}
			}
		}
		if deleted {
			for _, rec := range group {
				if target != "" && rec.Bucket != target {
					continue
				}
				rep.Actions = append(rep.Actions, Action{Kind: "quarantine", Source: rec.Path, Target: rec.Path, Before: rec.Raw, BeforeSHA256: digest(rec.Raw), BeforeModifiedNS: rec.ModifiedNS})
			}
			continue
		}
		first, chooseErr := newestRecord(group, source)
		if chooseErr != nil {
			rep.Warnings = append(rep.Warnings, chooseErr.Error()+": "+name)
			continue
		}
		if first.Name == "" {
			continue
		}
		// Alias collision under a different internal Desktop ID must never duplicate a conversation.
		collision := false
		for _, other := range records {
			if other.Name == name {
				continue
			}
			for _, id := range first.IDs {
				for _, oid := range other.IDs {
					if id == oid {
						collision = true
					}
				}
			}
		}
		if collision {
			rep.Warnings = append(rep.Warnings, "Alias collision; blocked: "+name)
			continue
		}
		sid := transcriptID(first)
		proof := transcriptProof(first, transcripts, parents, map[string]bool{})
		if proof == "" {
			rep.MissingTranscripts++
			rep.Warnings = append(rep.Warnings, "No proven Desktop transcript; skipped: "+name)
			continue
		}
		if proof == "fork" {
			rep.LineageVerified = append(rep.LineageVerified, first.Name)
		}
		tp := transcripts[sid].Path
		// A release sidecar means the transcript was explicitly released/deleted.
		tr, e := realRoot(projects)
		if e != nil {
			return rep, e
		}
		_, released := tr.Lstat(strings.TrimSuffix(tp, ".jsonl") + ".desktop-released.json")
		tr.Close()
		if released == nil {
			rep.Warnings = append(rep.Warnings, "Released transcript; skipped: "+name)
			continue
		}
		if !errors.Is(released, fs.ErrNotExist) {
			return rep, released
		}
		for _, bucket := range rep.Buckets {
			if target != "" && bucket != target {
				continue
			}
			dst := filepath.Join("claude-code-sessions", bucket, name)
			var old *Record
			for i := range group {
				if group[i].Bucket == bucket {
					old = &group[i]
					break
				}
			}
			desired := first.Payload
			if mode == "chat" && old != nil {
				if old.Path == first.Path {
					continue
				}
				desired = chatUpdate(first, *old)
			}
			if old != nil && bytes.Equal(old.Raw, desired) {
				continue
			}
			action := Action{Kind: "create", Source: first.Path, Target: dst, SHA256: digest(desired), Payload: desired, ModifiedNS: first.ModifiedNS}
			if old != nil {
				action.Kind = "update"
				action.Before = old.Raw
				action.BeforeSHA256 = digest(old.Raw)
				action.BeforeModifiedNS = old.ModifiedNS
			}
			rep.Actions = append(rep.Actions, action)
		}
	}
	appendTombstoneActions(&rep, markers, target)
	sort.SliceStable(rep.Actions, func(i, j int) bool {
		rank := func(k string) int {
			if k == "tombstone" {
				return 0
			}
			if k == "quarantine" {
				return 1
			}
			return 2
		}
		a, b := rank(rep.Actions[i].Kind), rank(rep.Actions[j].Kind)
		if a != b {
			return a < b
		}
		return rep.Actions[i].Target < rep.Actions[j].Target
	})
	for _, a := range rep.Actions {
		switch a.Kind {
		case "create":
			rep.Added++
		case "update":
			rep.Updated++
		case "quarantine":
			rep.Quarantined++
		case "tombstone":
			rep.Tombstones++
		}
	}
	sort.Strings(rep.Buckets)
	return rep, nil
}
func discover(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	candidates := []string{filepath.Join(home, ".config", "Claude")}
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		candidates = append(candidates, filepath.Join(x, "Claude"))
	}
	found := map[string]bool{}
	for _, p := range candidates {
		r, e := realRoot(p)
		if e != nil {
			continue
		}
		i, e := r.Lstat("claude-code-sessions")
		r.Close()
		if e == nil && i.IsDir() {
			found[p] = true
		}
	}
	if len(found) != 1 {
		return "", errors.New("no unique observed data root; pass --data-dir with an existing Claude userData directory")
	}
	for p := range found {
		return p, nil
	}
	panic("unreachable")
}
func durableDir(r *os.Root, p string) error {
	f, e := r.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func add(r *os.Root, path string, b []byte) error {
	if e := safe(r, path, true); e != nil {
		return e
	}
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".session-sync-%d-%d", os.Getpid(), time.Now().UnixNano()))
	f, e := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
	// link(2) publishes a complete file atomically and refuses any existing name.
	if e = r.Link(tmp, path); e != nil {
		return e
	}
	return durableDir(r, dir)
}
func lockRoot(r *os.Root) (*os.File, error) {
	if e := safe(r, ".linux-session-sync.lock", true); e != nil {
		return nil, e
	}
	f, e := r.OpenFile(".linux-session-sync.lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("another sync/undo is running")
	}
	return f, nil
}
func run(args []string) error {
	if len(args) == 0 && terminalInput() {
		return interactiveMenu(consoleInput, os.Stdout, run)
	}
	if len(args) == 1 && args[0] == "interactive" {
		return interactiveMenu(consoleInput, os.Stdout, run)
	}
	if len(args) == 0 {
		fmt.Println("Claude Desktop session sync " + version + " (Linux)\nCommands: interactive, doctor, plan, sync, undo, version\nAll operations are dry-run unless --apply --offline are both present.\nUse COMMAND --help for options.")
		return nil
	}
	cmd := args[0]
	if cmd == "version" {
		fmt.Println(version)
		return nil
	}
	if cmd != "doctor" && cmd != "plan" && cmd != "sync" && cmd != "undo" {
		return errors.New("unknown command")
	}
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	data := f.String("data-dir", "", "existing Claude userData root; discovered only if unambiguous")
	projects := f.String("projects-dir", "", "existing transcript projects root")
	source := f.String("from", "", "source account UUID/org UUID")
	target := f.String("to", "", "target account UUID/org UUID; omit for every existing bucket")
	write := f.Bool("apply", false, "apply reviewed synchronization changes or undo")
	dry := f.Bool("dry-run", false, "explicit dry-run (the default)")
	offline := f.Bool("offline", false, "confirm Desktop and all Claude workers stay stopped throughout")
	backup := f.String("backup", "", "backup ID for undo")
	mode := f.String("copy", "all", "all: exact session record with configuration and embedded secrets; chat: history pointers only")
	interactive := f.Bool("interactive", false, "show English Force quit / Retry / Cancel options when Claude blocks a write")
	force := f.Bool("force-quit", false, "explicitly stop detected Claude processes before applying; active work may be interrupted")
	summary := f.Bool("summary", false, "omit payloads from output")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *write && *dry {
		return errors.New("--dry-run and --apply cannot be combined")
	}
	if *write && (cmd != "sync" && cmd != "undo") {
		return errors.New("--apply is only valid for sync/undo")
	}
	if *write && !*offline {
		return errors.New("--apply requires --offline; fully quit Claude first")
	}
	if *mode != "all" && *mode != "chat" {
		return errors.New("--copy must be all or chat")
	}
	if *force && !*write {
		return errors.New("--force-quit requires --apply --offline")
	}
	d, e := discover(*data)
	if e != nil {
		return e
	}
	if *write {
		if *force {
			fmt.Fprintln(os.Stderr, "Stopping detected Claude processes...")
			if e := forceQuitClaude(); e != nil {
				return e
			}
		}
		if *interactive {
			if e := waitForClaude(consoleInput, os.Stdout, guard, forceQuitClaude); e != nil {
				return e
			}
		}
	}
	if cmd == "undo" {
		m, e := undo(d, *backup, *write)
		if e != nil {
			return e
		}
		if *interactive {
			fmt.Fprintf(os.Stdout, "Undo complete. Restored/reverted paths: %d\n", len(m))
			return nil
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"apply": *write, "quarantine": m})
	}
	if *projects == "" {
		h, e := os.UserHomeDir()
		if e != nil {
			return e
		}
		*projects = filepath.Join(h, ".claude", "projects")
	}
	*projects, e = filepath.Abs(*projects)
	if e != nil {
		return e
	}
	rep, e := scan(d, *projects, *source, *target, *mode)
	if e != nil {
		return e
	}
	if cmd == "doctor" {
		if e := guard(); e != nil {
			rep.Warnings = append(rep.Warnings, e.Error())
		}
	}
	if *write {
		id, e := apply(rep)
		if id != "" {
			fmt.Fprintln(os.Stderr, "Backup ID:", id)
		}
		if e != nil {
			return e
		}
	}
	if *interactive {
		label := "Dry run complete"
		if *write {
			label = "Synchronization complete"
		}
		fmt.Fprintf(os.Stdout, "%s. Added: %d. Updated: %d. Quarantined: %d. Deletion markers: %d. Session records: %d. Skipped/unproven: %d.\n", label, rep.Added, rep.Updated, rep.Quarantined, rep.Tombstones, rep.Records, rep.MissingTranscripts)
		for _, w := range rep.Warnings {
			fmt.Fprintln(os.Stdout, "Warning:", w)
		}
		return nil
	}
	if *summary || cmd == "doctor" {
		for i := range rep.Actions {
			rep.Actions[i].Payload = nil
			rep.Actions[i].Before = nil
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "Error:", e)
		os.Exit(1)
	}
}
