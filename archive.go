//go:build linux || windows || darwin

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const archiveExt = ".tar.gz"

// A tar.gz backup has the same layout as a folder backup:
// files/<role>/<path>, then manifest.json and README-RESTORE.txt at the end.
// It is written as <name>.tar.gz.partial and renamed only when complete.
type tarSink struct {
	final, partial string
	f              *os.File
	gz             *gzip.Writer
	tw             *tar.Writer
}

func newTarSink(final string) (*tarSink, error) {
	partial := final + ".partial"
	if _, e := os.Lstat(final); e == nil {
		return nil, fmt.Errorf("backup already exists: %s", final)
	}
	f, e := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	gz := gzip.NewWriter(f)
	return &tarSink{final: final, partial: partial, f: f, gz: gz, tw: tar.NewWriter(gz)}, nil
}

func (s *tarSink) path() string { return s.final }

func (s *tarSink) abort() {
	s.tw.Close()
	s.gz.Close()
	s.f.Close()
	os.Remove(s.partial)
}

func (s *tarSink) add(role, rel, src string, info fs.FileInfo) (int64, string, error) {
	// Open before writing the header so a vanished file can still be skipped.
	in, e := os.Open(src)
	if e != nil {
		return 0, "", e
	}
	defer in.Close()
	h := &tar.Header{Typeflag: tar.TypeReg, Name: path.Join(backupFilesDir, role, filepath.ToSlash(rel)), Mode: 0600, Size: info.Size(), ModTime: info.ModTime(), Format: tar.FormatPAX}
	if e := s.tw.WriteHeader(h); e != nil {
		return 0, "", e
	}
	sum := sha256.New()
	n, e := io.Copy(io.MultiWriter(s.tw, sum), io.LimitReader(in, info.Size()))
	if e == nil && n != info.Size() {
		e = fmt.Errorf("file shrank during backup (%d of %d bytes)", n, info.Size())
	}
	return n, hex.EncodeToString(sum.Sum(nil)), e
}

func (s *tarSink) writeEntry(name string, b []byte) error {
	if e := s.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0600, Size: int64(len(b)), ModTime: time.Now(), Format: tar.FormatPAX}); e != nil {
		return e
	}
	_, e := s.tw.Write(b)
	return e
}

func (s *tarSink) finish(m BackupManifest) error {
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if e := s.writeEntry(manifestName, b); e != nil {
		return e
	}
	if e := s.writeEntry("README-RESTORE.txt", []byte(restoreHelp(s.final))); e != nil {
		return e
	}
	if e := s.tw.Close(); e != nil {
		return e
	}
	if e := s.gz.Close(); e != nil {
		return e
	}
	if e := s.f.Sync(); e != nil {
		return e
	}
	if e := s.f.Close(); e != nil {
		return e
	}
	return os.Rename(s.partial, s.final)
}

func isArchive(p string) bool {
	if !strings.HasSuffix(p, archiveExt) {
		return false
	}
	i, e := os.Stat(p)
	return e == nil && i.Mode().IsRegular()
}

func openArchive(p string) (*tar.Reader, func(), error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, nil, e
	}
	gz, e := gzip.NewReader(f)
	if e != nil {
		f.Close()
		return nil, nil, fmt.Errorf("not a gzip backup: %w", e)
	}
	return tar.NewReader(gz), func() { gz.Close(); f.Close() }, nil
}

func readArchiveManifest(p string) (BackupManifest, error) {
	tr, done, e := openArchive(p)
	if e != nil {
		return BackupManifest{}, e
	}
	defer done()
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return BackupManifest{}, fmt.Errorf("not a complete backup (no %s in archive)", manifestName)
		}
		if e != nil {
			return BackupManifest{}, e
		}
		if h.Name == manifestName {
			b, e := io.ReadAll(io.LimitReader(tr, 256<<20))
			if e != nil {
				return BackupManifest{}, e
			}
			return parseManifest(b)
		}
	}
}

// scanArchive checks every file in the archive against the manifest. When
// into is set, the verified files are also written below into/files/.
// It returns the entries that are missing or fail their checksum.
func scanArchive(p string, m BackupManifest, into string) ([]string, error) {
	want := map[string]BackupFile{}
	for _, f := range m.Files {
		want[path.Join(backupFilesDir, f.Role, f.Path)] = f
	}
	seen := map[string]bool{}
	bad := []string{}
	tr, done, e := openArchive(p)
	if e != nil {
		return nil, e
	}
	defer done()
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if !strings.HasPrefix(h.Name, backupFilesDir+"/") {
			continue
		}
		f, ok := want[h.Name]
		if !ok || h.Typeflag != tar.TypeReg || seen[h.Name] {
			return nil, fmt.Errorf("unexpected archive entry %q", h.Name)
		}
		seen[h.Name] = true
		var w io.Writer = io.Discard
		var out *os.File
		if into != "" {
			target := filepath.Join(into, backupFilesDir, f.Role, filepath.FromSlash(f.Path))
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return nil, e
			}
			out, e = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return nil, e
			}
			w = out
		}
		sum := sha256.New()
		n, e := io.Copy(io.MultiWriter(w, sum), tr)
		if out != nil {
			if ce := out.Close(); e == nil {
				e = ce
			}
		}
		if e != nil {
			return nil, e
		}
		if n != f.Size || hex.EncodeToString(sum.Sum(nil)) != f.SHA256 {
			bad = append(bad, f.Role+"/"+f.Path)
		}
	}
	for name, f := range want {
		if !seen[name] {
			bad = append(bad, f.Role+"/"+f.Path)
		}
	}
	return bad, nil
}

// extractArchive verifies and unpacks an archive into a private temporary
// folder next to it, so large backups do not fill the system temp volume.
func extractArchive(p string, m BackupManifest) (string, []string, error) {
	tmp, e := os.MkdirTemp(filepath.Dir(p), ".claude-restore-")
	if e != nil {
		return "", nil, e
	}
	bad, e := scanArchive(p, m, tmp)
	if e != nil || len(bad) > 0 {
		os.RemoveAll(tmp)
		return "", bad, e
	}
	return tmp, nil, nil
}
