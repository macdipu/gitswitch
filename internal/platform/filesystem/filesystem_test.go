package filesystem

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLockFileIsExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache", "prompt.lock")
	unlock, ok := LockFile(p, time.Second)
	if !ok {
		t.Fatal("first lock not taken")
	}
	if _, ok := LockFile(p, 120*time.Millisecond); ok {
		t.Fatal("second lock taken while the first is held")
	}
	unlock()
	unlock2, ok := LockFile(p, time.Second)
	if !ok {
		t.Fatal("lock not taken after release")
	}
	unlock2()
}

func TestWriteAtomicReplacesAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "work.gitconfig")
	if err := WriteAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Errorf("content = %q", b)
	}
	if st, _ := os.Stat(p); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the existing 0600", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*tmp*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
