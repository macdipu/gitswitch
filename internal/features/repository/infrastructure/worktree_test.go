package infrastructure

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// A linked worktree must resolve the same account as the repo it belongs
// to, also by folder rule and wherever the worktree folder is.
func TestAccountInsideLinkedWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(dir string, args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=a", "-c", "user.email=a@b.example", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	work := filepath.Join(home, "work")
	main := filepath.Join(work, "app")
	elsewhere := filepath.Join(home, "scratch", "app-fix")
	git(home, "init", "-q", "-b", "main", main)
	git(main, "commit", "-q", "--allow-empty", "-m", "i")
	git(main, "remote", "add", "origin", "git@github.com:someone/app.git")
	git(main, "worktree", "add", "-q", "-b", "fix", elsewhere)

	cfg := &account.Config{
		Accounts: []account.Account{{ID: "personal", Host: "github.com", Email: "p@x.example"}, {ID: "work", Host: "github.com", Email: "w@x.example"}},
		Rules:    []account.Rule{{Kind: "folder", Pattern: work, Account: "work"}},
	}
	r := &Reader{Git: &gitinfra.Runner{}}
	mgd, wgd := r.Git.GitDirFor(main), r.Git.GitDirFor(elsewhere)
	if mgd == wgd || !strings.Contains(filepath.ToSlash(wgd), "/worktrees/") {
		t.Fatalf("git dirs: %s %s", mgd, wgd)
	}
	ma, _ := r.AccountFor(cfg, mgd)
	wa, _ := r.AccountFor(cfg, wgd)
	if ma == nil || ma.ID != "work" {
		t.Fatalf("main account = %v", ma)
	}
	if wa == nil || wa.ID != "work" {
		t.Errorf("worktree account = %v want work like the main repo", wa)
	}
	// A saved account is shared too, and InfoFor reports the worktree's own folder.
	git(main, "config", "gitswitch.account", "personal")
	if a, _ := r.AccountFor(cfg, wgd); a == nil || a.ID != "personal" {
		t.Errorf("saved account in worktree = %v", a)
	}
	ri := r.InfoFor(cfg, wgd)
	if ri.Saved != "personal" || ri.Account != "personal" || ri.Remote != "git@github.com:someone/app.git" {
		t.Errorf("info: %+v", ri)
	}
	if got, _ := filepath.EvalSymlinks(ri.Path); got != realPathOf(t, elsewhere) {
		t.Errorf("info path = %s", ri.Path)
	}
	// The repo list finds the worktree folder as its own entry.
	cfg.Repos = []string{elsewhere}
	found := 0
	for _, i := range r.List(cfg) {
		if i.Added {
			found++
		}
	}
	if found != 1 {
		t.Errorf("list has %d added entries", found)
	}
}

func realPathOf(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
