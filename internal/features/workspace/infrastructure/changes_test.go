package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

func TestParseStatus(t *testing.T) {
	entries := []string{
		"1 M. N... 100644 100644 100644 aaa bbb staged.go",
		"1 .M N... 100644 100644 100644 aaa aaa dir/with space.go",
		"2 R. N... 100644 100644 100644 aaa aaa R100 new name.go", "old name.go",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc clash.go",
		"? notes.txt",
	}
	got := parseStatus(strings.Join(entries, "\x00") + "\x00")
	want := []workspace.File{
		{Path: "staged.go", Staged: "M", Worktree: "."},
		{Path: "dir/with space.go", Staged: ".", Worktree: "M"},
		{Path: "new name.go", Orig: "old name.go", Staged: "R", Worktree: "."},
		{Path: "clash.go", Staged: "U", Worktree: "U", Conflict: true},
		{Path: "notes.txt", Staged: "?", Worktree: "?"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseStatus =\n%+v\nwant\n%+v", got, want)
	}
	if got := parseStatus(""); len(got) != 0 {
		t.Errorf("empty status = %+v", got)
	}
}

// A real repo: list, diff, stage, unstage and discard, with paths git
// could read as patterns ("*.txt") or that hold spaces.
func TestChangesWithGit(t *testing.T) {
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
	write("a.txt", "one\n")
	write("*.txt", "star\n")
	write("keep.txt", "keep\n")
	run("add", "--all")
	run("commit", "-q", "-m", "first")

	write("a.txt", "one\ntwo\n")
	write("*.txt", "star changed\n")
	write("dir/new file.txt", "fresh\n")
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if real, err := filepath.EvalSymlinks(gd); err == nil {
		gd = real
	}
	wt := filepath.Dir(gd)

	files, err := g.Status(gd)
	if err != nil || len(files) != 3 {
		t.Fatalf("status: %+v %v", files, err)
	}
	d, err := g.Diff(gd, false, []string{"dir/new file.txt"})
	if err != nil || !strings.Contains(d, "+fresh") {
		t.Fatalf("diff of a new file: %q %v", d, err)
	}
	if d, _ := g.Diff(gd, false, nil); !strings.Contains(d, "+two") || !strings.Contains(d, "+fresh") || !strings.Contains(d, "+star changed") {
		t.Fatalf("diff of every file:\n%s", d)
	}

	// "*.txt" is that one file, not every .txt file.
	if err := g.Stage(wt, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := run("diff", "--cached", "--name-only"); got != "*.txt" {
		t.Fatalf("staged %q, want only *.txt", got)
	}
	if d, _ := g.Diff(gd, true, nil); !strings.Contains(d, "+star changed") || strings.Contains(d, "+two") {
		t.Fatalf("staged diff:\n%s", d)
	}
	if err := g.Unstage(wt, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := run("diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("still staged: %q", got)
	}

	// Discard a changed file and a new one; the other change stays, and
	// the discarded ones are in the stash.
	if err := g.Discard(wt, "gitswitch discard: main", []string{"a.txt", "dir/new file.txt"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Errorf("a.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "dir", "new file.txt")); !os.IsNotExist(err) {
		t.Errorf("new file still there: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "*.txt")); string(b) != "star changed\n" {
		t.Errorf("*.txt lost its change: %q", b)
	}
	if got := run("stash", "list"); !strings.Contains(got, "gitswitch discard: main") {
		t.Errorf("stash list: %q", got)
	}
	run("stash", "pop", "-q")
	if b, _ := os.ReadFile(filepath.Join(dir, "dir", "new file.txt")); string(b) != "fresh\n" {
		t.Errorf("the stash did not bring the new file back: %q", b)
	}

	// Discard everything: changed and new files go, all into one stash.
	write("a.txt", "again\n")
	if err := g.Discard(wt, "gitswitch discard: main", nil); err != nil {
		t.Fatal(err)
	}
	if got := run("status", "--porcelain"); got != "" {
		t.Errorf("left after discarding everything: %q", got)
	}
}
