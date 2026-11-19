package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// A real repo: the patterns built for odd names match exactly those names
// in git, tracked files are reported and untracked, and info/exclude is
// found.
func TestIgnoreWithGit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "me@example.com")
	run("config", "user.name", "Me")
	write("tracked.txt", "t\n")
	write("lib/keep.go", "k\n")
	run("add", "--all")
	run("commit", "-q", "-m", "first")
	for _, n := range []string{"*.txt", "a.txt", "#hash", "!bang", "trail ", ":colon", "dir/x.log", "dir/y.log", "q?.md", "qq.md", "lib/new.tmp"} {
		write(n, n)
	}

	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if real, err := filepath.EvalSymlinks(gd); err == nil {
		gd = real
	}
	wt := filepath.Dir(gd)

	gi, err := g.IgnoreFile(gd, false)
	if err != nil || gi != filepath.Join(wt, ".gitignore") {
		t.Fatalf("IgnoreFile = %q, %v", gi, err)
	}
	ex, err := g.IgnoreFile(gd, true)
	if err != nil || ex != filepath.Join(gd, "info", "exclude") {
		t.Fatalf("IgnoreFile local = %q, %v", ex, err)
	}
	add := func(file, p string, kind workspace.IgnoreKind) {
		t.Helper()
		pat, err := workspace.IgnorePattern(p, kind, g.IsDir(gd, p))
		if err != nil {
			t.Fatal(err)
		}
		c, err := g.ReadIgnore(file)
		if err != nil {
			t.Fatal(err)
		}
		c, _ = workspace.AddIgnoreLine(c, pat)
		if err := g.WriteIgnore(file, c); err != nil {
			t.Fatal(err)
		}
	}
	ignored := func(p string) bool {
		t.Helper()
		ok, err := g.Ignored(gd, p)
		if err != nil {
			t.Fatalf("Ignored(%s): %v", p, err)
		}
		return ok
	}

	add(gi, "*.txt", workspace.IgnoreFile)
	add(gi, "#hash", workspace.IgnoreFile)
	add(gi, "!bang", workspace.IgnoreFile)
	add(gi, "trail ", workspace.IgnoreFile)
	add(gi, "q?.md", workspace.IgnoreFile)
	add(gi, ":colon", workspace.IgnoreFile)
	add(gi, "dir/x.log", workspace.IgnoreFolder)
	add(ex, "lib/new.tmp", workspace.IgnoreExt)
	for p, want := range map[string]bool{
		"*.txt": true, "a.txt": false, "#hash": true, "!bang": true, "trail ": true, ":colon": true,
		"q?.md": true, "qq.md": false, "dir/x.log": true, "dir/y.log": true, "lib/new.tmp": true,
		"tracked.txt": false, // tracked: never ignored
	} {
		if got := ignored(p); got != want {
			b, _ := os.ReadFile(gi)
			t.Errorf("ignored(%q) = %v, want %v\n%s", p, got, want, b)
		}
	}
	if b, _ := os.ReadFile(ex); !strings.HasSuffix(string(b), "\n*.tmp\n") {
		t.Errorf("info/exclude = %q", b)
	}

	if !g.IsDir(gd, "lib") || g.IsDir(gd, "tracked.txt") {
		t.Error("IsDir is wrong")
	}
	if got, err := g.Tracked(gd, "lib"); err != nil || strings.Join(got, ",") != "lib/keep.go" {
		t.Errorf("Tracked(lib) = %v, %v", got, err)
	}
	if got, err := g.Tracked(gd, "*.txt"); err != nil || len(got) != 0 {
		t.Errorf("Tracked(*.txt) = %v, %v; want none (literal)", got, err)
	}
	add(gi, "tracked.txt", workspace.IgnoreFile)
	if err := g.Untrack(wt, "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Tracked(gd, "tracked.txt"); len(got) != 0 {
		t.Errorf("still tracked: %v", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "tracked.txt")); err != nil {
		t.Errorf("untrack removed the file: %v", err)
	}
	if !ignored("tracked.txt") {
		t.Error("tracked.txt is not ignored after untracking")
	}
	if got := run("status", "--porcelain", "--", "tracked.txt"); got != "D  tracked.txt" {
		t.Errorf("status = %q, want a staged deletion", got)
	}
}
