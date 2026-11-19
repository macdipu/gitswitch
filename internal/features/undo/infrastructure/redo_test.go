package infrastructure

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// Real git: the undone record is kept with a ref that keeps its commit,
// a new action forgets it, and branches are deleted again only while they
// are where the undo left them.
func TestRedoRecordAndBranchesWithGit(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Me", "-c", "user.email=m@x.example", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	one := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "two")
	two := run("rev-parse", "HEAD")
	gd := filepath.Join(dir, ".git")
	g := &Git{Git: &gitinfra.Runner{}}

	if _, err := g.LoadRedo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
		t.Fatalf("empty: %v", err)
	}
	r := undo.Record{Action: undo.Commit, Label: "Committed two", Worktree: dir, Branch: "main", Before: one, After: two, Time: 5}
	if err := g.SaveRedo(gd, r); err != nil {
		t.Fatal(err)
	}
	got, err := g.LoadRedo(gd)
	if err != nil || got.Action != undo.Commit || got.Before != one || got.After != two || got.Label != "Committed two" || got.Time != 5 {
		t.Fatalf("LoadRedo = %+v, %v", got, err)
	}
	if run("rev-parse", "refs/gitswitch/redo") != two {
		t.Error("the redo ref must keep the commit")
	}
	// The undo record and the redo record are separate.
	if _, err := g.Load(gd); !errors.Is(err, undo.ErrNothing) {
		t.Errorf("Load sees the redo record: %v", err)
	}
	// A new action (Save) forgets the redo.
	if err := g.Save(gd, undo.Record{Action: undo.Commit, Label: "x", Worktree: dir, Branch: "main", Before: one, After: two}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LoadRedo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
		t.Errorf("redo after a new action: %v", err)
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "refs/gitswitch/redo").CombinedOutput(); err == nil {
		t.Errorf("redo ref left: %s", out)
	}
	if !g.HasCommit(gd, two) || g.HasCommit(gd, strings.Repeat("0", 40)) {
		t.Error("HasCommit")
	}

	// DeleteBranches: only where they were, with their upstream settings.
	run("branch", "x", one)
	run("branch", "y", two)
	run("config", "branch.x.remote", "origin")
	err = g.DeleteBranches(gd, []undo.Deleted{{Name: "x", Hash: one}, {Name: "y", Hash: one}})
	if err == nil || run("branch", "--list", "x") == "" {
		t.Fatalf("a moved branch must stop everything: %v", err)
	}
	if err := g.DeleteBranches(gd, []undo.Deleted{{Name: "x", Hash: one}, {Name: "y", Hash: two}}); err != nil {
		t.Fatal(err)
	}
	if run("branch", "--list", "x", "y") != "" {
		t.Error("branches left")
	}
	if out, _ := exec.Command("git", "-C", dir, "config", "--get", "branch.x.remote").Output(); len(out) != 0 {
		t.Errorf("upstream settings left: %s", out)
	}
}
