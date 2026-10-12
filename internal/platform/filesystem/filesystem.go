// Package filesystem holds file-system details: the home, config and cache
// folders, how this OS compares paths, and file changes that only print in
// dry-run mode. Layers above it never check runtime.GOOS for these concerns.
package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HomeDir is the user's home folder.
func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ExpandHome turns a leading ~ into the home folder.
func ExpandHome(p string) string {
	if p == "~" {
		return HomeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(HomeDir(), p[2:])
	}
	return p
}

// ConfigDir is gitswitch's own config folder.
func ConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = filepath.Join(HomeDir(), ".config")
	}
	return filepath.Join(d, "gitswitch")
}

// CacheDir is gitswitch's private cache folder.
func CacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "gitswitch")
}

// RealPath resolves symlinks, or returns p unchanged.
func RealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// CaseInsensitiveFS reports whether file names ignore case by default.
func CaseInsensitiveFS() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// SamePath compares two paths the way this OS does.
func SamePath(a, b string) bool {
	if CaseInsensitiveFS() {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// HasPath reports whether list holds folder p.
func HasPath(list []string, p string) bool {
	for _, v := range list {
		if SamePath(v, p) {
			return true
		}
	}
	return false
}

// FS makes file changes, or only prints them in dry-run mode.
type FS struct {
	DryRun bool
	Out    io.Writer
}

// Dry prints what would happen and reports true in dry-run mode.
func (f *FS) Dry(what string) bool {
	if f != nil && f.DryRun {
		out := f.Out
		if out == nil {
			out = os.Stdout
		}
		fmt.Fprintln(out, "[dry-run] "+what)
		return true
	}
	return false
}

func (f *FS) WriteFile(path string, data []byte, perm os.FileMode) error {
	if f.Dry("write " + path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return WriteAtomic(path, data, perm)
}

// WriteAtomic writes a file through a temporary file in the same folder
// and renames it into place, so readers (such as git reading an include
// file) never see it half written. An existing file keeps its mode.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (f *FS) Remove(path string) error {
	if f.Dry("delete " + path) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *FS) RemoveAll(path string) error {
	if f.Dry("delete " + path) {
		return nil
	}
	return os.RemoveAll(path)
}

func (f *FS) Rename(from, to string) error {
	if f.Dry("rename " + from + " -> " + to) {
		return nil
	}
	return os.Rename(from, to)
}
