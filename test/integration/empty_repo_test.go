package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repo with no commit yet (right after init) must work everywhere: git 2.47
// words the unborn HEAD as "bad revision 'HEAD'", which used to fail branch,
// changes and fetch with a fatal error. Walk the whole first commit.
func TestFreshRepoWithoutCommits(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.root, "fresh")
	e.gs("init", dir, "--account", "me")

	if out := e.gsIn(dir, "branch"); !strings.Contains(out, "main") && !strings.Contains(out, "master") {
		t.Fatalf("branch on a fresh repo:\n%s", out)
	}
	e.gsIn(dir, "status")
	if out := e.gsIn(dir, "changes"); !strings.Contains(out, "No uncommitted changes") {
		t.Fatalf("changes on a fresh repo:\n%s", out)
	}
	if out := e.gsIn(dir, "log"); strings.Contains(out, "fatal") {
		t.Fatalf("log on a fresh repo:\n%s", out)
	}

	must(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644))
	if out := e.gsIn(dir, "changes"); !strings.Contains(out, "a.txt") {
		t.Fatalf("changes with a new file:\n%s", out)
	}
	if out := e.gsIn(dir, "diff", "a.txt"); !strings.Contains(out, "+hello") {
		t.Fatalf("diff of a new file in a fresh repo:\n%s", out)
	}
	e.gsIn(dir, "stage", "a.txt")
	if out := e.gsIn(dir, "changes"); !strings.Contains(out, "Staged") {
		t.Fatalf("changes after stage:\n%s", out)
	}
	e.gsIn(dir, "unstage", "a.txt")
	e.gsIn(dir, "stage", "a.txt")
	if out := e.gsIn(dir, "commit", "-m", "First commit"); !strings.Contains(out, "First commit") {
		t.Fatalf("first commit:\n%s", out)
	}
	if got := e.gitIn(dir, "log", "-1", "--format=%ae"); got != "dipu@me.example" {
		t.Errorf("first commit author = %q", got)
	}
	if out := e.gsIn(dir, "log"); !strings.Contains(out, "First commit") {
		t.Fatalf("log after the first commit:\n%s", out)
	}
}
