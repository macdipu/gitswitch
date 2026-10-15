package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// With first parent, a merge keeps only its first parent, so the graph
// draws no line to commits that are not listed.
func TestCommitsFirstParent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, "gc"), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	run("switch", "-q", "-c", "side")
	run("commit", "-q", "--allow-empty", "-m", "side work")
	run("switch", "-q", "main")
	run("commit", "-q", "--allow-empty", "-m", "two")
	run("merge", "-q", "--no-ff", "-m", "merge side", "side")

	l := &Log{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	cs, err := l.Commits(gd, 50, history.Filter{Ref: "main", FirstParent: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 || !cs[0].Merge || len(cs[0].Parents) != 1 {
		t.Fatalf("first-parent history: %+v", cs)
	}
	if lanes := history.LayoutLanes(cs); lanes != 1 {
		t.Errorf("one lane without the side branch, got %d", lanes)
	}
	cs, _ = l.Commits(gd, 50, history.Filter{Ref: "main"})
	if len(cs) != 4 || len(cs[0].Parents) != 2 {
		t.Fatalf("full history keeps both parents: %+v", cs)
	}
}

// A branch's commits are marked when no copy of them is on HEAD.
func TestNotPicked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, "gc"), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=A", "GIT_COMMITTER_EMAIL=a@x")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name string) { must(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644)) }
	run("init", "-q", "-b", "main")
	write("a")
	run("add", "a")
	run("commit", "-q", "-m", "one")
	run("switch", "-q", "-c", "side")
	write("b")
	run("add", "b")
	run("commit", "-q", "-m", "side b")
	picked := run("rev-parse", "HEAD")
	write("c")
	run("add", "c")
	run("commit", "-q", "-m", "side c")
	open := run("rev-parse", "HEAD")
	run("switch", "-q", "main")
	run("cherry-pick", picked)

	l := &Log{Git: &gitinfra.Runner{}}
	got := l.NotPicked(filepath.Join(dir, ".git"), "side")
	if len(got) != 1 || !got[open] || got[picked] {
		t.Fatalf("NotPicked = %v, want only %s", got, open)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
