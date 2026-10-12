package infrastructure

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsConfigWrite(t *testing.T) {
	writes := [][]string{{"--global", "user.email", "x"}, {"--unset", "a.b"}, {"--global", "--add", "a.b", "c"}, {"--remove-section", "x"}, {"--rename-section", "a", "b"}}
	reads := [][]string{{"--get", "a.b"}, {"--global", "--get-regexp", "^x"}, {"user.email"}, {"--list"}, {"-z", "--global", "--get-regexp", "x"}}
	for _, w := range writes {
		if !IsConfigWrite(w) {
			t.Errorf("%v should be a write", w)
		}
	}
	for _, r := range reads {
		if IsConfigWrite(r) {
			t.Errorf("%v should be a read", r)
		}
	}
	if ca := ConfigArgs([]string{"--git-dir=/x", "config", "--local", "a.b", "c"}); len(ca) != 3 {
		t.Errorf("configArgs = %v", ca)
	}
	if ca := ConfigArgs([]string{"-C", "/x", "log"}); ca != nil {
		t.Errorf("non-config command detected as config: %v", ca)
	}
}

// A linked worktree's folder must come from the worktree itself, never
// from the folder gitswitch happens to run in.
func TestWorktreeOfLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	r := &Runner{}
	main, feat := filepath.Join(dir, "main"), filepath.Join(dir, "feat")
	for _, args := range [][]string{
		{"init", "-q", main},
		{"-C", main, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "i"},
		{"-C", main, "worktree", "add", "-q", feat, "-b", "feat"},
	} {
		if _, err := r.Run(args...); err != nil {
			t.Fatal(err)
		}
	}
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	if got := r.WorktreeOf(filepath.Join(main, ".git", "worktrees", "feat")); got != feat {
		t.Errorf("linked worktree = %q, want %q", got, feat)
	}
	if got := r.WorktreeOf(filepath.Join(main, ".git")); got != main {
		t.Errorf("main = %q, want %q", got, main)
	}
	bare := filepath.Join(dir, "b.git")
	if _, err := r.Run("init", "-q", "--bare", bare); err != nil {
		t.Fatal(err)
	}
	if got := r.WorktreeOf(bare); got != bare {
		t.Errorf("bare repo = %q, want the git dir itself, not %q", got, elsewhere)
	}
}
