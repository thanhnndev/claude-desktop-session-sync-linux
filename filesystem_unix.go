//go:build linux || darwin

package main

import (
	"os"
	"syscall"
)

func durableDir(r *os.Root, p string) error {
	f, e := r.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func platformLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func privateBackup(info os.FileInfo) bool { return info.Mode().Perm() == 0700 }

func secureBackup(r *os.Root) error { return nil }

func unsafeLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
