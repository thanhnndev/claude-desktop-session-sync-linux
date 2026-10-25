//go:build linux

// An offline, additive synchronizer for Claude Desktop Code metadata.
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
	"strconv"
	"strings"
	"syscall"
	"time"
)

const version = "0.1.0"

var guard = processGuard

const backupDir = ".linux-session-sync-backups"

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var allowed = strings.Fields("sessionId cliSessionId unarchivedCliSessionId priorCliSessionIds cwd originCwd worktreePath worktreeName folderWorktrees worktreeLazy worktreePinned sourceBranch branch gitAnchors gitAnchorsFolderRealpath gitAnchorsLookupOnly createdAt lastActivityAt lastFocusedAt title titleSource isArchived model effort")

type Record struct {
	Excluded           []string
	Path, Bucket, Name string
	Data               map[string]any
	IDs                []string
	Payload            []byte
}
type Action struct {
	Source  string          `json:"source"`
	Target  string          `json:"target"`
	SHA256  string          `json:"sha256"`
	Payload json.RawMessage `json:"payload"`
}
type Report struct {
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
	Source  string `json:"source"`
	Target  string `json:"target"`
	SHA256  string `json:"sha256"`
	Payload []byte `json:"payload_base64"`
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
	r := Record{Path: path, Bucket: bucket, Name: filepath.Base(path)}
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
func transcriptIndex(p string) (map[string]string, []string, error) {
	r, e := realRoot(p)
	if e != nil {
		return nil, nil, e
	}
	defer r.Close()
	idx := map[string]string{}
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
			h, e := r.Open(path)
			if e != nil {
				return nil, nil, e
			}
			info, e := h.Stat()
			if e != nil || !info.Mode().IsRegular() {
				h.Close()
				return nil, nil, errors.New("non-regular transcript")
			}
			sc := bufio.NewScanner(io.LimitReader(h, 1024*1024))
			sc.Buffer(make([]byte, 4096), 1024*1024)
			proven := false
			for n := 0; n < 100 && sc.Scan(); n++ {
				var d map[string]any
				if json.Unmarshal(sc.Bytes(), &d) == nil && str(d, "sessionId") == id && str(d, "entrypoint") == "claude-desktop" {
					proven = true
					break
				}
			}
			h.Close()
			if proven {
				if _, ok := idx[id]; ok {
					return nil, nil, fmt.Errorf("ambiguous transcript ID %s", id)
				}
				idx[id] = path
				desktop = append(desktop, id)
			}
		}
	}
	sort.Strings(desktop)
	return idx, desktop, nil
}
func scan(data, projects, source, target, mode string) (Report, error) {
	rep := Report{Version: version, DataDir: data, ProjectsDir: projects, CopyMode: mode, FullRecord: mode == "all", Excluded: []string{}, Actions: []Action{}, Warnings: []string{}, Orphans: []string{}}
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
					tombs[id] = true
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
	for _, rec := range records {
		for _, id := range rec.IDs {
			known[id] = true
		}
		groups[rec.Name] = append(groups[rec.Name], rec)
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
		first := group[0]
		conflict := false
		deleted := false
		for _, rec := range group {
			if !bytes.Equal(first.Payload, rec.Payload) {
				conflict = true
			}
			for _, id := range rec.IDs {
				if tombs[id] {
					deleted = true
				}
			}
		}
		if deleted {
			rep.Warnings = append(rep.Warnings, "Tombstoned; no copy or automatic cleanup: "+name)
			continue
		}
		if conflict {
			rep.Warnings = append(rep.Warnings, "Conflicting versions; no winner selected: "+name)
			continue
		}
		if source != "" {
			found := false
			for _, rec := range group {
				if rec.Bucket == source {
					first = rec
					found = true
					break
				}
			}
			if !found {
				continue
			}
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
		sid := str(first.Data, "cliSessionId")
		if sid == "" {
			sid = str(first.Data, "unarchivedCliSessionId")
		}
		tp, ok := transcripts[sid]
		if !ok {
			rep.MissingTranscripts++
			rep.Warnings = append(rep.Warnings, "No proven Desktop transcript; skipped: "+name)
			continue
		}
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
			if names[dst] {
				continue
			}
			rep.Actions = append(rep.Actions, Action{Source: first.Path, Target: dst, SHA256: digest(first.Payload), Payload: first.Payload})
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
func processGuard() error {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return e
	}
	for _, p := range entries {
		pid, e := strconv.Atoi(p.Name())
		if e != nil || pid == os.Getpid() {
			continue
		}
		base := filepath.Join("/proc", p.Name())
		st, e := os.Stat(base)
		if e != nil {
			continue
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Uid != uint32(os.Getuid()) {
			continue
		}
		exe, e := os.Readlink(filepath.Join(base, "exe"))
		if e != nil {
			if errors.Is(e, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("cannot inspect process %d: %w", pid, e)
		}
		name := strings.ToLower(filepath.Base(exe))
		if name == "claude" || strings.Contains(name, "claude-desktop") || strings.Contains(name, "claude_code") {
			return fmt.Errorf("Claude process %d is still running; fully quit Desktop and its workers", pid)
		}
		if strings.Contains(name, "electron") {
			b, e := os.ReadFile(filepath.Join(base, "cmdline"))
			if e != nil {
				return e
			}
			if bytes.Contains(bytes.ToLower(b), []byte("claude")) {
				return fmt.Errorf("Claude Electron process %d is running", pid)
			}
		}
	}
	return nil
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
func apply(rep Report) (string, error) {
	if e := guard(); e != nil {
		return "", e
	}
	r, e := realRoot(rep.DataDir)
	if e != nil {
		return "", e
	}
	defer r.Close()
	lk, e := lockRoot(r)
	if e != nil {
		return "", e
	}
	defer lk.Close()
	// Refuse any change since the plan, including new deletion markers.
	fresh, e := scan(rep.DataDir, rep.ProjectsDir, "", "", rep.CopyMode)
	if e != nil {
		return "", e
	}
	valid := map[string]Action{}
	for _, a := range fresh.Actions {
		valid[a.Target] = a
	}
	for _, a := range rep.Actions {
		v, ok := valid[a.Target]
		if !ok || v.SHA256 != a.SHA256 {
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
	stored := []StoredAction{}
	for _, a := range rep.Actions {
		stored = append(stored, StoredAction{Source: a.Source, Target: a.Target, SHA256: a.SHA256, Payload: []byte(a.Payload)})
	}
	j := Journal{Version: version, Created: time.Now().UTC().Format(time.RFC3339), Actions: stored}
	b, _ := json.MarshalIndent(j, "", "  ")
	if e = add(r, filepath.Join(dir, "manifest.json"), b); e != nil {
		return id, e
	}
	if e = durableDir(r, backupDir); e != nil {
		return id, e
	}
	if e = durableDir(r, "."); e != nil {
		return id, e
	}
	// Write-ahead manifest holds every sanitized payload before the first app write.
	for _, a := range rep.Actions {
		if e = guard(); e != nil {
			return id, e
		}
		if e = add(r, a.Target, a.Payload); e != nil {
			return id, fmt.Errorf("partial operation; use undo %s: %w", id, e)
		}
	}
	return id, nil
}
func undo(data, id string, apply bool) ([]string, error) {
	if !regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z$`).MatchString(id) {
		return nil, errors.New("invalid backup ID")
	}
	r, e := realRoot(data)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var lk *os.File
	if apply {
		if e = guard(); e != nil {
			return nil, e
		}
		lk, e = lockRoot(r)
		if e != nil {
			return nil, e
		}
		defer lk.Close()
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
	if j.Version != version {
		return nil, errors.New("unsupported manifest version")
	}
	moves := []string{}
	expected := map[string]string{}
	for _, a := range j.Actions {
		parts := strings.Split(a.Target, "/")
		if len(parts) != 4 || parts[0] != "claude-code-sessions" || !uuid.MatchString(parts[1]) || !uuid.MatchString(parts[2]) {
			return nil, errors.New("invalid manifest target")
		}
		if _, e = parseRecord(a.Target, filepath.Join(parts[1], parts[2]), a.Payload, "all"); e != nil {
			return nil, e
		}
		if digest(a.Payload) != a.SHA256 {
			return nil, errors.New("invalid manifest checksum")
		}
		current, e := read(r, a.Target, 10*1024*1024)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if digest(current) != a.SHA256 {
			return nil, fmt.Errorf("modified record retained; cannot undo %s", a.Target)
		}
		moves = append(moves, a.Target)
		expected[a.Target] = a.SHA256
	}
	if !apply {
		return moves, nil
	}
	// Preflight all records before mutation. Copies remain in quarantine and manifest.
	for _, path := range moves {
		if e = guard(); e != nil {
			return moves, e
		}
		b, e := read(r, path, 10*1024*1024)
		if e != nil {
			return moves, e
		}
		if digest(b) != expected[path] {
			return moves, fmt.Errorf("record changed during undo: %s", path)
		}
		h := sha256.Sum256([]byte(path))
		q := filepath.Join(dir, fmt.Sprintf("quarantine-%x.json", h[:8]))
		if e = add(r, q, b); e != nil {
			prior, e2 := read(r, q, 10*1024*1024)
			if e2 != nil || digest(prior) != expected[path] {
				return moves, e
			}
		}
		if e = r.Remove(path); e != nil {
			return moves, e
		}
		if e = durableDir(r, filepath.Dir(path)); e != nil {
			return moves, e
		}
	}
	return moves, nil
}
func run(args []string) error {
	if len(args) == 0 {
		fmt.Println("Claude Desktop session sync " + version + " (Linux)\nCommands: doctor, plan, sync, undo, version\nAll operations are dry-run unless --apply --offline are both present.\nUse COMMAND --help for options.")
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
	write := f.Bool("apply", false, "apply reviewed additions or undo")
	dry := f.Bool("dry-run", false, "explicit dry-run (the default)")
	offline := f.Bool("offline", false, "confirm Desktop and all Claude workers stay stopped throughout")
	backup := f.String("backup", "", "backup ID for undo")
	mode := f.String("copy", "all", "all: exact session record with configuration and embedded secrets; chat: history pointers only")
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
	d, e := discover(*data)
	if e != nil {
		return e
	}
	if cmd == "undo" {
		m, e := undo(d, *backup, *write)
		if e != nil {
			return e
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
	if *summary || cmd == "doctor" {
		for i := range rep.Actions {
			rep.Actions[i].Payload = nil
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
