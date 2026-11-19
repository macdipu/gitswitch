package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// Real git: drop a stash, bring it back from the undo record's commit; pop
// a stash back into a clean folder, and refuse (changing nothing) on a clash.
func TestStashUndoWithGit(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Me", "-c", "user.email=m@x.example", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("a.txt", "one\n")
	run("add", ".")
	run("commit", "-qm", "first")
	gd := filepath.Join(dir, ".git")
	g := &Git{Git: &gitinfra.Runner{}}

	write("a.txt", "changed\n")
	write("new.txt", "new\n")
	run("stash", "push", "-q", "-u", "-m", "wip")
	hash := run("rev-parse", "refs/stash")
	subject := run("stash", "list", "--format=%gs")

	// Drop, remember it as the undo record (which keeps the commit reachable),
	// even after the commit would otherwise be unreachable.
	run("stash", "drop", "-q")
	if g.HasStash(gd, hash) {
		t.Fatal("still in the list")
	}
	if err := g.Save(gd, undo.Record{Action: undo.StashDrop, Label: "Dropped", Worktree: dir, Branch: "main", To: subject, Before: hash, After: hash}); err != nil {
		t.Fatal(err)
	}
	run("reflog", "expire", "--expire=now", "--all")
	run("gc", "-q", "--prune=now")
	if err := g.StashStore(gd, hash, subject); err != nil {
		t.Fatal(err)
	}
	if got := run("stash", "list", "--format=%H %gs"); got != hash+" "+subject {
		t.Errorf("stash list = %q", got)
	}
	if err := g.StashStore(gd, hash, subject); err != nil { // already there: fine
		t.Errorf("again: %v", err)
	}

	// Pop it back into the clean folder.
	if g.Dirty(dir) {
		t.Fatal("clean folder reported dirty")
	}
	if err := g.StashPopBack(dir, hash); err != nil {
		t.Fatal(err)
	}
	if !g.Dirty(dir) || g.HasStash(gd, hash) {
		t.Error("changes not back, or the entry is still listed")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new.txt")); string(b) != "new\n" {
		t.Errorf("new.txt = %q", b)
	}

	// A clash leaves the folder as it was and the stash in the list.
	run("stash", "push", "-q", "-u", "-m", "again")
	hash = run("rev-parse", "refs/stash")
	write("a.txt", "other line\n")
	run("commit", "-qam", "conflicting")
	if err := g.StashPopBack(dir, hash); err == nil {
		t.Fatal("expected a clash")
	}
	if g.Dirty(dir) || !g.HasStash(gd, hash) {
		t.Errorf("clash must change nothing: dirty=%v", g.Dirty(dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt left behind: %v", err)
	}
}
