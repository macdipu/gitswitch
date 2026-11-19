package infrastructure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func realDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorktreeLifecycleWithRealGit(t *testing.T) {
	r := newStashRepo(t)
	wt := filepath.Join(t.TempDir(), "feat")
	if err := r.g.WorktreeAdd(r.gd, wt, refs.WorktreeAddOptions{NewBranch: "feat"}); err != nil {
		t.Fatal(err)
	}
	all, err := r.g.Worktrees(r.gd)
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %v %+v", err, all)
	}
	if !all[0].Main || all[0].Branch != "main" || all[1].Main || all[1].Branch != "feat" || all[1].Path != realDir(t, wt) {
		t.Errorf("list: %+v", all)
	}
	if all[1].Changes == nil || all[1].Changes.Any() || all[0].Changes == nil {
		t.Errorf("counts: %+v %+v", all[0].Changes, all[1].Changes)
	}
	// Asking through the linked worktree's own git dir gives the same list.
	lgd := filepath.Join(r.gd, "worktrees", "feat")
	if again, err := r.g.Worktrees(lgd); err != nil || len(again) != 2 || !again[0].Main {
		t.Errorf("list from a linked git dir: %v %+v", err, again)
	}
	// Uncommitted and untracked files show in the counts.
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644)
	all, _ = r.g.Worktrees(r.gd)
	if c := all[1].Changes; c == nil || c.New != 1 || c.Changed != 1 {
		t.Errorf("dirty counts: %+v", c)
	}
	if all[0].Changes.Any() {
		t.Errorf("main is clean but shows %+v", all[0].Changes)
	}
	// git itself refuses a dirty removal without force.
	if err := r.g.WorktreeRemove(r.gd, wt, false); err == nil {
		t.Error("removed a dirty worktree without force")
	}
	if err := r.g.WorktreeLock(r.gd, wt, "usb"); err != nil {
		t.Fatal(err)
	}
	all, _ = r.g.Worktrees(r.gd)
	if !all[1].Locked || all[1].LockReason != "usb" {
		t.Errorf("lock: %+v", all[1])
	}
	if err := r.g.WorktreeUnlock(r.gd, wt); err != nil {
		t.Fatal(err)
	}
	if err := r.g.WorktreeRemove(r.gd, wt, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("folder is still there")
	}
	if all, _ = r.g.Worktrees(r.gd); len(all) != 1 {
		t.Errorf("after remove: %+v", all)
	}
	// the branch is kept
	if !strings.Contains(r.run("branch", "--list", "feat"), "feat") {
		t.Error("branch feat was deleted with its worktree")
	}
}

func TestWorktreePruneAndDetachedAndExistingBranch(t *testing.T) {
	r := newStashRepo(t)
	r.run("branch", "old")
	base := t.TempDir()
	a, d := filepath.Join(base, "a"), filepath.Join(base, "d")
	if err := r.g.WorktreeAdd(r.gd, a, refs.WorktreeAddOptions{Branch: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := r.g.WorktreeAdd(r.gd, d, refs.WorktreeAddOptions{Detach: "HEAD"}); err != nil {
		t.Fatal(err)
	}
	all, _ := r.g.Worktrees(r.gd)
	if len(all) != 3 || all[1].Branch != "old" || !all[2].Detached || all[2].Short == "" {
		t.Fatalf("%+v", all)
	}
	os.RemoveAll(a)
	all, _ = r.g.Worktrees(r.gd)
	if !all[1].Prunable || all[1].Changes != nil {
		t.Errorf("a missing folder is prunable with no counts: %+v", all[1])
	}
	if err := r.g.WorktreePrune(r.gd); err != nil {
		t.Fatal(err)
	}
	if all, _ = r.g.Worktrees(r.gd); len(all) != 2 || all[1].Detached != true {
		t.Errorf("after prune: %+v", all)
	}
	// from: a new branch at a given start
	r.write("c.txt", "c")
	r.run("add", ".")
	r.run("commit", "-q", "-m", "second")
	f := filepath.Join(base, "f")
	if err := r.g.WorktreeAdd(r.gd, f, refs.WorktreeAddOptions{NewBranch: "fromfirst", From: "HEAD~1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f, "c.txt")); err == nil {
		t.Error("new branch did not start at --from")
	}
	if out, err := r.g.WorktreeRepair(r.gd); err != nil {
		t.Errorf("repair: %v %s", err, out)
	}
}

// A linked worktree shares the repo's config: the saved account and the
// identity written in the main repo are what git reads in the worktree.
func TestLinkedWorktreeSharesRepoConfig(t *testing.T) {
	r := newStashRepo(t)
	r.run("config", "gitswitch.account", "work")
	r.run("config", "user.email", "me@work.example")
	wt := filepath.Join(t.TempDir(), "w")
	if err := r.g.WorktreeAdd(r.gd, wt, refs.WorktreeAddOptions{NewBranch: "w"}); err != nil {
		t.Fatal(err)
	}
	lgd := r.g.Git.GitDirFor(wt)
	if filepath.Base(filepath.Dir(lgd)) != "worktrees" {
		t.Fatalf("git dir of a worktree = %s", lgd)
	}
	if got := r.g.Git.RepoGet(lgd, "gitswitch.account"); got != "work" {
		t.Errorf("account in worktree = %q", got)
	}
	if got := r.g.Git.RepoGet(lgd, "user.email"); got != "me@work.example" {
		t.Errorf("email in worktree = %q", got)
	}
	// saving the account from the worktree lands in the one shared config
	if err := r.g.Git.RepoSet(lgd, "gitswitch.account", "other"); err != nil {
		t.Fatal(err)
	}
	if got := r.run("config", "--local", "gitswitch.account"); got != "other" {
		t.Errorf("main repo account = %q", got)
	}
	if got := r.g.Git.RuleFolderOf(lgd); realDir(t, got) != realDir(t, r.dir) {
		t.Errorf("rule folder = %s, want the main folder %s", got, r.dir)
	}
	if got := r.g.Git.WorktreeOf(lgd); realDir(t, got) != realDir(t, wt) {
		t.Errorf("worktree of = %s, want %s", got, wt)
	}
}
