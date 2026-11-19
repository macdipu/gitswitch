package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// A real repo: read the last commit, amend its message, amend it with
// every change, and see when it is pushed.
func TestLastAndAmendWithGit(t *testing.T) {
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
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "me@example.com")
	run("config", "user.name", "Me")
	run("config", "commit.gpgsign", "false")
	write("a.txt", "one\n")
	run("add", "--all")
	run("commit", "-q", "-m", "First\n\nThe body.")
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if real, err := filepath.EvalSymlinks(gd); err == nil {
		gd = real
	}
	wt := filepath.Dir(gd)

	l, err := g.Last(gd)
	if err != nil || l.Hash != run("rev-parse", "HEAD") || l.Subject != "First" || l.Message != "First\n\nThe body." ||
		l.Parents != 0 || l.Files != 1 || l.Pushed {
		t.Fatalf("Last = %+v, %v", l, err)
	}

	// A new message, nothing staged: the files stay as they were.
	if err := g.Amend(wt, "Second", false); err != nil {
		t.Fatal(err)
	}
	if got := run("log", "--format=%s", "-n", "5"); got != "Second" {
		t.Errorf("after amending the message, log = %q", got)
	}

	// Every change, keeping the message.
	write("a.txt", "one\ntwo\n")
	write("b.txt", "new\n")
	if err := g.Amend(wt, "", true); err != nil {
		t.Fatal(err)
	}
	if got := run("log", "--format=%s", "-n", "5"); got != "Second" {
		t.Errorf("after amending with every change, log = %q", got)
	}
	if got := run("status", "--porcelain"); got != "" {
		t.Errorf("left uncommitted: %q", got)
	}
	if l, _ := g.Last(gd); l.Files != 2 {
		t.Errorf("files = %d, want 2", l.Files)
	}

	// On a remote branch: pushed.
	run("update-ref", "refs/remotes/origin/main", "HEAD")
	if l, err := g.Last(gd); err != nil || !l.Pushed {
		t.Errorf("pushed: %+v %v", l, err)
	}
}
