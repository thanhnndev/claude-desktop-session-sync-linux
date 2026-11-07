//go:build linux || windows || darwin

// Full, restorable backups of Claude Desktop Code sessions and Claude Code
// transcripts. Unlike the sync write-ahead journal, a session backup holds
// every chat transcript and session record, so work can resume after a
// reinstall or on a new machine.
package main

import (
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
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	backupFormat   = "claude-session-backup/1"
	backupPrefix   = "claude-backup-"
	manifestName   = "manifest.json"
	backupFilesDir = "files"
)

// Session data inside the Claude Desktop userData root. Cookies, Local
// Storage, caches and config.json (which can hold tokens) are never included.
var desktopItems = []string{
	"claude-code-sessions",      // Code tab sidebar records per account/org
	"ccd-session-presets.json",  // saved Code projects
	"ccd-ids.json",              // id salt used by the Code tab
	"git-worktrees.json",        // worktree registry referenced by sessions
	"local-agent-mode-sessions", // Cowork / agent-mode sessions
	"side-session-notes",        // notes shared between linked sessions
	"scratch-workspaces",        // scratch folders used as session cwd
}

// Session data inside ~/.claude. .credentials.json is never included.
var claudeItems = []string{"projects", "history.jsonl", "sessions", "file-history", "todos", "plans"}

// Opt-in user configuration that helps a resumed session behave the same.
var claudeConfigItems = []string{"settings.json", "CLAUDE.md", "rules", "skills", "agents", "commands"}

type BackupFile struct {
	Role       string `json:"role"`
	Path       string `json:"path"` // slash-separated, relative to the role root
	Size       int64  `json:"size"`
	Mode       uint32 `json:"mode"`
	ModifiedNS int64  `json:"modified_ns"`
	SHA256     string `json:"sha256"`
}

type BackupManifest struct {
	Format   string            `json:"format"`
	Tool     string            `json:"tool_version"`
	Created  string            `json:"created"`
	Host     string            `json:"host"`
	OS       string            `json:"os"`
	Roots    map[string]string `json:"roots"`
	Files    []BackupFile      `json:"files"`
	Skipped  []string          `json:"skipped"`
	Bytes    int64             `json:"total_bytes"`
	Sessions int               `json:"desktop_session_records"`
	Chats    int               `json:"transcripts"`
}

type backupRoots struct{ desktop, claude string }

func defaultClaudeHome() (string, error) {
	h, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(h, ".claude"), nil
}

func copyHashed(src, dst string, mode os.FileMode) (int64, string, error) {
	in, e := os.Open(src)
	if e != nil {
		return 0, "", e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return 0, "", e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(out, h), in)
	if e == nil {
		e = out.Sync()
	}
	if ce := out.Close(); e == nil {
		e = ce
	}
	return n, hex.EncodeToString(h.Sum(nil)), e
}

func hashFile(p string) (string, int64, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}

// createBackup copies the selected session data into a new timestamped
// directory under dest. Sources are only read. The manifest is written last,
// so a directory without manifest.json is an incomplete backup.
func createBackup(dest string, roots backupRoots, withConfig bool, now time.Time) (string, BackupManifest, error) {
	m := BackupManifest{Format: backupFormat, Tool: version, Created: now.UTC().Format(time.RFC3339), OS: runtime.GOOS, Roots: map[string]string{}, Skipped: []string{}}
	m.Host, _ = os.Hostname()
	dest, e := filepath.Abs(dest)
	if e != nil {
		return "", m, e
	}
	if i, e := os.Stat(dest); e != nil || !i.IsDir() {
		return "", m, fmt.Errorf("backup destination must be an existing directory: %s", dest)
	}
	plan := map[string][]string{}
	if roots.desktop != "" {
		m.Roots["desktop"] = roots.desktop
		plan["desktop"] = desktopItems
	}
	if roots.claude != "" {
		m.Roots["claude"] = roots.claude
		plan["claude"] = append([]string{}, claudeItems...)
		if withConfig {
			plan["claude"] = append(plan["claude"], claudeConfigItems...)
		}
	}
	if len(plan) == 0 {
		return "", m, errors.New("nothing to back up: no Claude Desktop or ~/.claude data found")
	}
	for role, root := range m.Roots {
		if strings.HasPrefix(dest+string(filepath.Separator), filepath.Clean(root)+string(filepath.Separator)) {
			return "", m, fmt.Errorf("backup destination is inside the %s data root; choose another folder", role)
		}
	}
	dir := filepath.Join(dest, backupPrefix+now.Format("20060102-150405"))
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", m, e
	}
	for _, role := range []string{"desktop", "claude"} {
		root := m.Roots[role]
		for _, item := range plan[role] {
			src := filepath.Join(root, item)
			if _, e := os.Lstat(src); errors.Is(e, fs.ErrNotExist) {
				continue
			}
			e := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				rel, _ := filepath.Rel(root, p)
				slash := filepath.ToSlash(rel)
				if err != nil {
					m.Skipped = append(m.Skipped, role+"/"+slash+": "+err.Error())
					if d != nil && d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if d.Type()&fs.ModeSymlink != 0 {
					m.Skipped = append(m.Skipped, role+"/"+slash+": symlink not followed")
					return nil
				}
				target := filepath.Join(dir, backupFilesDir, role, rel)
				if d.IsDir() {
					return os.MkdirAll(target, 0700)
				}
				if !d.Type().IsRegular() {
					m.Skipped = append(m.Skipped, role+"/"+slash+": not a regular file")
					return nil
				}
				info, e := d.Info()
				if e != nil {
					m.Skipped = append(m.Skipped, role+"/"+slash+": "+e.Error())
					return nil
				}
				if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					return e
				}
				n, sum, e := copyHashed(p, target, 0600)
				if errors.Is(e, fs.ErrNotExist) {
					m.Skipped = append(m.Skipped, role+"/"+slash+": vanished during backup")
					os.Remove(target)
					return nil
				}
				if e != nil {
					return fmt.Errorf("copy %s: %w", p, e)
				}
				os.Chtimes(target, info.ModTime(), info.ModTime())
				m.Files = append(m.Files, BackupFile{Role: role, Path: slash, Size: n, Mode: uint32(info.Mode().Perm()), ModifiedNS: info.ModTime().UnixNano(), SHA256: sum})
				m.Bytes += n
				if role == "desktop" && strings.HasPrefix(slash, "claude-code-sessions/") && strings.HasPrefix(filepath.Base(slash), "local_") && strings.HasSuffix(slash, ".json") {
					m.Sessions++
				}
				if role == "claude" && strings.HasPrefix(slash, "projects/") && strings.HasSuffix(slash, ".jsonl") {
					m.Chats++
				}
				return nil
			})
			if e != nil {
				return dir, m, e
			}
		}
	}
	sort.Slice(m.Files, func(i, j int) bool {
		if m.Files[i].Role != m.Files[j].Role {
			return m.Files[i].Role < m.Files[j].Role
		}
		return m.Files[i].Path < m.Files[j].Path
	})
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return dir, m, e
	}
	tmp := filepath.Join(dir, manifestName+".tmp")
	if e := os.WriteFile(tmp, b, 0600); e != nil {
		return dir, m, e
	}
	if e := os.Rename(tmp, filepath.Join(dir, manifestName)); e != nil {
		return dir, m, e
	}
	os.WriteFile(filepath.Join(dir, "README-RESTORE.txt"), []byte(restoreHelp(dir)), 0600)
	return dir, m, nil
}

func restoreHelp(dir string) string {
	return `Claude session backup (` + backupFormat + `)

files/desktop/  -> Claude Desktop userData root
                   macOS:   ~/Library/Application Support/Claude
                   Linux:   ~/.config/Claude
                   Windows: %APPDATA%\Claude
files/claude/   -> ~/.claude (Claude Code transcripts in projects/)

Restore with the tool (dry run first, then apply with Claude fully quit):
  claude-session-sync restore --from "` + dir + `"
  claude-session-sync restore --from "` + dir + `" --apply --offline

Without the tool, copy files/desktop/* and files/claude/* into the folders
above while Claude is quit, without overwriting newer files.
This backup contains private chat history. Do not upload it.
`
}

func loadManifest(dir string) (BackupManifest, error) {
	var m BackupManifest
	b, e := os.ReadFile(filepath.Join(dir, manifestName))
	if e != nil {
		return m, fmt.Errorf("not a complete backup (no %s): %w", manifestName, e)
	}
	if e := json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.Format != backupFormat {
		return m, fmt.Errorf("unsupported backup format %q", m.Format)
	}
	for _, f := range m.Files {
		if (f.Role != "desktop" && f.Role != "claude") || !filepath.IsLocal(filepath.FromSlash(f.Path)) {
			return m, fmt.Errorf("invalid manifest entry %s/%s", f.Role, f.Path)
		}
	}
	return m, nil
}

// verifyBackup rechecks every stored file against its manifest checksum.
func verifyBackup(dir string) (BackupManifest, []string, error) {
	m, e := loadManifest(dir)
	if e != nil {
		return m, nil, e
	}
	bad := []string{}
	for _, f := range m.Files {
		sum, n, e := hashFile(filepath.Join(dir, backupFilesDir, f.Role, filepath.FromSlash(f.Path)))
		if e != nil || sum != f.SHA256 || n != f.Size {
			bad = append(bad, f.Role+"/"+f.Path)
		}
	}
	return m, bad, nil
}

type RestoreReport struct {
	Backup     string            `json:"backup"`
	Apply      bool              `json:"apply"`
	Roots      map[string]string `json:"target_roots"`
	Create     []string          `json:"create"`
	Identical  int               `json:"identical"`
	Conflicts  []string          `json:"conflicts"`
	Replaced   []string          `json:"replaced,omitempty"`
	SavedTo    string            `json:"replaced_versions_saved_to,omitempty"`
	Corrupt    []string          `json:"corrupt_in_backup,omitempty"`
	Restored   int               `json:"restored"`
	Overwrite  bool              `json:"overwrite"`
	SourceOS   string            `json:"source_os"`
	SourceTime string            `json:"source_created"`
}

// restoreBackup copies files from a backup into the target roots. Files that
// already exist with identical content are left alone. Files that differ are
// conflicts: they are skipped unless overwrite is set, and then the current
// version is first saved inside the backup directory.
func restoreBackup(dir string, targets map[string]string, apply, overwrite bool, now time.Time) (RestoreReport, error) {
	rep := RestoreReport{Backup: dir, Apply: apply, Overwrite: overwrite, Roots: targets, Create: []string{}, Conflicts: []string{}}
	m, bad, e := verifyBackup(dir)
	if e != nil {
		return rep, e
	}
	rep.SourceOS, rep.SourceTime = m.OS, m.Created
	if len(bad) > 0 {
		rep.Corrupt = bad
		return rep, fmt.Errorf("%d file(s) in the backup fail their checksum; nothing restored", len(bad))
	}
	opened := map[string]*os.Root{}
	defer func() {
		for _, r := range opened {
			r.Close()
		}
	}()
	for role, root := range targets {
		if apply {
			if e := os.MkdirAll(root, 0700); e != nil {
				return rep, e
			}
		}
		if _, e := os.Stat(root); e != nil {
			if errors.Is(e, fs.ErrNotExist) && !apply {
				continue
			}
			return rep, e
		}
		r, e := realRoot(root)
		if e != nil {
			return rep, e
		}
		opened[role] = r
	}
	type job struct {
		f      BackupFile
		exists bool
	}
	jobs := []job{}
	for _, f := range m.Files {
		if _, ok := targets[f.Role]; !ok {
			continue
		}
		rel := filepath.FromSlash(f.Path)
		label := f.Role + "/" + f.Path
		r := opened[f.Role]
		if r == nil {
			rep.Create = append(rep.Create, label)
			jobs = append(jobs, job{f, false})
			continue
		}
		if e := safe(r, rel, true); e != nil {
			return rep, e
		}
		info, e := r.Lstat(rel)
		if errors.Is(e, fs.ErrNotExist) {
			rep.Create = append(rep.Create, label)
			jobs = append(jobs, job{f, false})
			continue
		}
		if e != nil {
			return rep, e
		}
		if !info.Mode().IsRegular() {
			rep.Conflicts = append(rep.Conflicts, label+" (not a regular file)")
			continue
		}
		cur, e := r.Open(rel)
		if e != nil {
			return rep, e
		}
		h := sha256.New()
		_, e = io.Copy(h, cur)
		cur.Close()
		if e != nil {
			return rep, e
		}
		if hex.EncodeToString(h.Sum(nil)) == f.SHA256 {
			rep.Identical++
			continue
		}
		rep.Conflicts = append(rep.Conflicts, label)
		if overwrite {
			jobs = append(jobs, job{f, true})
		}
	}
	if !apply {
		return rep, nil
	}
	saved := filepath.Join(dir, "replaced-"+now.Format("20060102-150405"))
	for _, j := range jobs {
		r := opened[j.f.Role]
		rel := filepath.FromSlash(j.f.Path)
		if j.exists {
			// Keep the version being replaced before touching it.
			keep := filepath.Join(saved, j.f.Role, rel)
			if e := os.MkdirAll(filepath.Dir(keep), 0700); e != nil {
				return rep, e
			}
			old, e := r.ReadFile(rel)
			if e != nil {
				return rep, e
			}
			if e := os.WriteFile(keep, old, 0600); e != nil {
				return rep, e
			}
			rep.SavedTo = saved
		}
		if parent := filepath.Dir(rel); parent != "." {
			if e := r.MkdirAll(parent, 0700); e != nil {
				return rep, e
			}
			if e := safe(r, parent, false); e != nil {
				return rep, e
			}
		}
		b, e := os.ReadFile(filepath.Join(dir, backupFilesDir, j.f.Role, rel))
		if e != nil {
			return rep, e
		}
		tmp := filepath.Join(filepath.Dir(rel), fmt.Sprintf(".restore-%d-%d", os.Getpid(), time.Now().UnixNano()))
		mode := os.FileMode(j.f.Mode) & 0777
		if mode == 0 {
			mode = 0600
		}
		if e := r.WriteFile(tmp, b, mode); e != nil {
			return rep, e
		}
		mt := time.Unix(0, j.f.ModifiedNS)
		r.Chtimes(tmp, mt, mt)
		if e := r.Rename(tmp, rel); e != nil {
			r.Remove(tmp)
			return rep, e
		}
		rep.Restored++
		if j.exists {
			rep.Replaced = append(rep.Replaced, j.f.Role+"/"+j.f.Path)
		}
	}
	return rep, nil
}

type BackupSummary struct {
	Path     string `json:"path"`
	Created  string `json:"created"`
	OS       string `json:"os"`
	Host     string `json:"host"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"total_bytes"`
	Sessions int    `json:"desktop_session_records"`
	Chats    int    `json:"transcripts"`
	Complete bool   `json:"complete"`
}

func listBackups(dest string) ([]BackupSummary, error) {
	entries, e := os.ReadDir(dest)
	if e != nil {
		return nil, e
	}
	out := []BackupSummary{}
	for _, d := range entries {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), backupPrefix) {
			continue
		}
		p := filepath.Join(dest, d.Name())
		s := BackupSummary{Path: p}
		if m, e := loadManifest(p); e == nil {
			s = BackupSummary{Path: p, Created: m.Created, OS: m.OS, Host: m.Host, Files: len(m.Files), Bytes: m.Bytes, Sessions: m.Sessions, Chats: m.Chats, Complete: true}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path > out[j].Path })
	return out, nil
}

func humanBytes(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < 1024 || u == "GB" {
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return ""
}

func runBackupCommand(cmd string, args []string) error {
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	dest := f.String("dest", "", "folder that holds timestamped backups")
	from := f.String("from", "", "backup directory (claude-backup-YYYYMMDD-HHMMSS)")
	data := f.String("data-dir", "", "Claude Desktop userData root")
	home := f.String("claude-home", "", "Claude Code home (default ~/.claude)")
	withConfig := f.Bool("with-config", false, "backup: also include ~/.claude settings.json, CLAUDE.md, rules, skills, agents, commands")
	only := f.String("only", "", "restore: desktop or claude (default both)")
	write := f.Bool("apply", false, "restore: write files (default is a dry run)")
	offline := f.Bool("offline", false, "restore: confirm Claude Desktop and Claude Code are fully quit")
	overwrite := f.Bool("overwrite", false, "restore: replace differing files; current versions are saved in the backup first")
	summary := f.Bool("summary", false, "print counts instead of file lists")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	claudeHome := *home
	if claudeHome == "" {
		h, e := defaultClaudeHome()
		if e != nil {
			return e
		}
		claudeHome = h
	}
	switch cmd {
	case "backup":
		if *dest == "" {
			return errors.New("--dest is required")
		}
		roots := backupRoots{}
		if d, e := discover(*data); e == nil {
			roots.desktop = d
		} else if *data != "" {
			return e
		} else {
			fmt.Fprintln(os.Stderr, "Warning: Claude Desktop data not found; backing up ~/.claude only")
		}
		if i, e := os.Stat(claudeHome); e == nil && i.IsDir() {
			roots.claude, _ = filepath.Abs(claudeHome)
		}
		if guard() != nil {
			fmt.Fprintln(os.Stderr, "Note: Claude is running. Backup continues; files written after this moment are not included.")
		}
		dir, m, e := createBackup(*dest, roots, *withConfig, time.Now())
		if e != nil {
			if dir != "" {
				fmt.Fprintln(os.Stderr, "Incomplete backup left at:", dir)
			}
			return e
		}
		fmt.Printf("Backup complete: %s\nFiles: %d (%s). Desktop session records: %d. Transcripts: %d. Skipped: %d.\n", dir, len(m.Files), humanBytes(m.Bytes), m.Sessions, m.Chats, len(m.Skipped))
		for _, s := range m.Skipped {
			fmt.Println("Skipped:", s)
		}
		return nil
	case "backups":
		if *dest == "" {
			return errors.New("--dest is required")
		}
		list, e := listBackups(*dest)
		if e != nil {
			return e
		}
		if len(list) == 0 {
			fmt.Println("No backups in", *dest)
			return nil
		}
		for _, b := range list {
			if !b.Complete {
				fmt.Printf("%s  INCOMPLETE (no manifest)\n", b.Path)
				continue
			}
			fmt.Printf("%s  %s  %s/%s  files=%d size=%s sessions=%d transcripts=%d\n", b.Path, b.Created, b.OS, b.Host, b.Files, humanBytes(b.Bytes), b.Sessions, b.Chats)
		}
		return nil
	case "verify":
		if *from == "" {
			return errors.New("--from is required")
		}
		m, bad, e := verifyBackup(*from)
		if e != nil {
			return e
		}
		if len(bad) > 0 {
			for _, b := range bad {
				fmt.Println("Corrupt or missing:", b)
			}
			return fmt.Errorf("%d of %d file(s) failed verification", len(bad), len(m.Files))
		}
		fmt.Printf("OK: %d file(s), %s, created %s on %s\n", len(m.Files), humanBytes(m.Bytes), m.Created, m.OS)
		return nil
	case "restore":
		if *from == "" {
			return errors.New("--from is required")
		}
		if *write && !*offline {
			return errors.New("--apply requires --offline; fully quit Claude Desktop and Claude Code first")
		}
		if *write {
			if e := guard(); e != nil {
				return e
			}
		}
		targets := map[string]string{}
		if *only == "" || *only == "desktop" {
			d, e := discover(*data)
			if e != nil {
				// A fresh install may not have a Code sessions folder yet.
				h, he := os.UserHomeDir()
				if he != nil {
					return he
				}
				d = dataCandidates(h)[0]
				if *data != "" {
					d, _ = filepath.Abs(*data)
				}
				if _, se := os.Stat(d); se != nil {
					return fmt.Errorf("Claude Desktop data folder not found (%s); open Claude Desktop once, sign in, quit it, or pass --data-dir", d)
				}
			}
			targets["desktop"] = d
		}
		if *only == "" || *only == "claude" {
			targets["claude"], _ = filepath.Abs(claudeHome)
		}
		if len(targets) == 0 {
			return errors.New("--only must be desktop or claude")
		}
		rep, e := restoreBackup(*from, targets, *write, *overwrite, time.Now())
		if e != nil {
			if len(rep.Corrupt) > 0 {
				enc.Encode(rep)
			}
			return e
		}
		if *summary {
			mode := "Dry run"
			if *write {
				mode = "Restore"
			}
			fmt.Printf("%s: to create %d, identical %d, conflicts %d, restored %d.\n", mode, len(rep.Create), rep.Identical, len(rep.Conflicts), rep.Restored)
			if len(rep.Conflicts) > 0 && !*overwrite {
				fmt.Println("Conflicting files were left as they are; add --overwrite to replace them (current versions are saved in the backup first).")
			}
			if rep.SavedTo != "" {
				fmt.Println("Replaced versions saved to:", rep.SavedTo)
			}
			return nil
		}
		return enc.Encode(rep)
	}
	return errors.New("unknown command")
}
