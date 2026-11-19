package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

type fakeWT struct {
	*fakeRefs
	list   []refs.Worktree
	ran    []string
	addErr error
}

func (f *fakeWT) Worktrees(string) ([]refs.Worktree, error) { return f.list, nil }
func (f *fakeWT) WorktreeAdd(_, path string, o refs.WorktreeAddOptions) error {
	f.ran = append(f.ran, "add "+path+" "+o.Branch+o.NewBranch+o.Detach)
	if f.addErr != nil {
		return f.addErr
	}
	br := o.Branch
	if o.NewBranch != "" {
		br = o.NewBranch
	}
	f.list = append(f.list, refs.Worktree{Path: path, Branch: br, Short: "abc1234"})
	return nil
}
func (f *fakeWT) WorktreeRemove(_, path string, force bool) error {
	f.ran = append(f.ran, "remove "+path)
	if force {
		f.ran = append(f.ran, "force")
	}
	return nil
}
func (f *fakeWT) WorktreePrune(string) error { f.ran = append(f.ran, "prune"); return nil }
func (f *fakeWT) WorktreeLock(_, p, r string) error {
	f.ran = append(f.ran, "lock "+p+" "+r)
	return nil
}
func (f *fakeWT) WorktreeUnlock(_, p string) error { f.ran = append(f.ran, "unlock "+p); return nil }
func (f *fakeWT) WorktreeRepair(string, ...string) (string, error) {
	f.ran = append(f.ran, "repair")
	return "", nil
}

type wtRepos struct {
	repo.Reader
	byDir map[string]string
}

func (r wtRepos) InfoFor(_ *account.Config, gd string) repo.Info {
	return repo.Info{Account: r.byDir[gd]}
}

type wtLocator struct{ locator }

func (wtLocator) GitDirFor(string) string { return "/wt-gitdir" }

func newWT(t *testing.T) (*Service, *fakeWT, string) {
	t.Helper()
	base := t.TempDir()
	main := filepath.Join(base, "repo")
	s, r := newService()
	f := &fakeWT{fakeRefs: r, list: []refs.Worktree{
		{Path: main, Branch: "main", Main: true, Changes: &refs.Changes{}},
		{Path: filepath.Join(base, "feat"), Branch: "feat", Changes: &refs.Changes{}},
	}}
	s.Refs = f
	s.Repos = wtRepos{byDir: map[string]string{main + "/.git": "work", "/wt-gitdir": "work"}}
	s.Accounts = noAccounts{}
	s.Locator = wtLocator{}
	return s, f, base
}

func TestWorktreesCarryTheReposAccount(t *testing.T) {
	s, f, base := newWT(t)
	all, err := s.Worktrees(filepath.Join(base, "repo") + "/.git")
	if err != nil || len(all) != 2 || all[0].Account != "work" || all[1].Account != "work" {
		t.Errorf("%v %+v", err, all)
	}
	// a Refs without worktree support says so instead of crashing
	s.Refs = f.fakeRefs
	if _, err := s.Worktrees("/x/.git"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("err = %v", err)
	}
}

func TestWorktreeAddRefusals(t *testing.T) {
	s, f, base := newWT(t)
	gd := filepath.Join(base, "repo") + "/.git"
	full := filepath.Join(base, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o644)
	cases := []struct {
		name, path string
		o          refs.WorktreeAddOptions
		want       string
	}{
		{"no mode", filepath.Join(base, "n"), refs.WorktreeAddOptions{}, "say what to check out"},
		{"two modes", filepath.Join(base, "n"), refs.WorktreeAddOptions{Branch: "a", Detach: "HEAD"}, "pick one"},
		{"no path", "", refs.WorktreeAddOptions{Branch: "a"}, "where should"},
		{"dash name", filepath.Join(base, "n"), refs.WorktreeAddOptions{Branch: "-f"}, "not a branch or commit"},
		{"from without -b", filepath.Join(base, "n"), refs.WorktreeAddOptions{Branch: "a", From: "x"}, "--from needs"},
		{"nested", filepath.Join(base, "repo", "sub", "wt"), refs.WorktreeAddOptions{Branch: "other"}, "inside this repo's own folder"},
		{"non-empty folder", full, refs.WorktreeAddOptions{Branch: "other"}, "not empty"},
		{"already a worktree", filepath.Join(base, "feat"), refs.WorktreeAddOptions{Branch: "other"}, "already a worktree"},
		{"branch elsewhere", filepath.Join(base, "n2"), refs.WorktreeAddOptions{Branch: "feat"}, "already checked out in " + filepath.Join(base, "feat")},
		{"branch in main", filepath.Join(base, "n3"), refs.WorktreeAddOptions{Branch: "main"}, "already checked out in " + filepath.Join(base, "repo")},
	}
	for _, c := range cases {
		if _, err := s.WorktreeAdd(gd, c.path, c.o); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if len(f.ran) != 0 {
		t.Errorf("a refused add still ran git: %v", f.ran)
	}
}

func TestWorktreeAddWorks(t *testing.T) {
	s, f, base := newWT(t)
	gd := filepath.Join(base, "repo") + "/.git"
	empty := filepath.Join(base, "empty")
	os.MkdirAll(empty, 0o755) // an empty folder is fine
	got, err := s.WorktreeAdd(gd, empty, refs.WorktreeAddOptions{Branch: "fix"})
	if err != nil || got.Path != empty || got.Branch != "fix" || got.Account != "work" || len(got.Warnings) != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if got, err := s.WorktreeAdd(gd, filepath.Join(base, "new"), refs.WorktreeAddOptions{NewBranch: "n", From: "main"}); err != nil || got.Branch != "n" {
		t.Errorf("%v %+v", err, got)
	}
	if len(f.ran) != 2 {
		t.Errorf("ran %v", f.ran)
	}
	// a worktree that would use another account is flagged
	s.Repos = wtRepos{byDir: map[string]string{gd: "work", "/wt-gitdir": "personal"}}
	got, _ = s.WorktreeAdd(gd, filepath.Join(base, "odd"), refs.WorktreeAddOptions{Detach: "HEAD"})
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "personal") {
		t.Errorf("warnings = %v", got.Warnings)
	}
	f.addErr = errors.New("git failed")
	if _, err := s.WorktreeAdd(gd, filepath.Join(base, "bad"), refs.WorktreeAddOptions{Detach: "HEAD"}); err == nil {
		t.Error("git's error was swallowed")
	}
}

func TestWorktreeRemoveRefusals(t *testing.T) {
	s, f, base := newWT(t)
	gd := filepath.Join(base, "repo") + "/.git"
	feat := filepath.Join(base, "feat")
	f.list = append(f.list,
		refs.Worktree{Path: filepath.Join(base, "lk"), Branch: "lk", Locked: true, LockReason: "usb"},
		refs.Worktree{Path: filepath.Join(base, "gone"), Branch: "gone", Prunable: true})
	for path, want := range map[string]string{
		filepath.Join(base, "repo"): "own folder",
		filepath.Join(base, "lk"):   "locked (usb); unlock it first",
		filepath.Join(base, "gone"): "prune",
		filepath.Join(base, "nope"): "not a worktree",
		"":                          "which worktree",
	} {
		if err := s.WorktreeRemove(gd, path, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("remove %q: %v, want %q", path, err, want)
		}
	}
	f.list[1].Changes = &refs.Changes{Changed: 2, New: 1}
	err := s.WorktreeRemove(gd, feat, false)
	if err == nil || !strings.Contains(err.Error(), "would be lost") || !strings.Contains(err.Error(), "2 changed") || !strings.Contains(err.Error(), "1 new") {
		t.Errorf("dirty: %v", err)
	}
	if len(f.ran) != 0 {
		t.Fatalf("ran %v", f.ran)
	}
	if err := s.WorktreeRemove(gd, feat, true); err != nil || len(f.ran) != 2 || f.ran[1] != "force" {
		t.Errorf("force: %v %v", err, f.ran)
	}
	f.ran = nil
	f.list[1].Changes = &refs.Changes{}
	if err := s.WorktreeRemove(gd, feat, false); err != nil || len(f.ran) != 1 {
		t.Errorf("clean: %v %v", err, f.ran)
	}
}

func TestWorktreePruneAndLock(t *testing.T) {
	s, f, base := newWT(t)
	gd := filepath.Join(base, "repo") + "/.git"
	if plan, err := s.WorktreePrune(gd); err != nil || len(plan) != 0 || len(f.ran) != 0 {
		t.Errorf("nothing to prune: %v %v %v", err, plan, f.ran)
	}
	f.list = append(f.list,
		refs.Worktree{Path: "/gone1", Prunable: true},
		refs.Worktree{Path: "/gone2", Prunable: true, Locked: true})
	plan, err := s.WorktreePrunePlan(gd)
	if err != nil || len(plan) != 1 || plan[0].Path != "/gone1" || len(f.ran) != 0 {
		t.Fatalf("plan: %v %+v %v", err, plan, f.ran)
	}
	if plan, err = s.WorktreePrune(gd); err != nil || len(plan) != 1 || len(f.ran) != 1 || f.ran[0] != "prune" {
		t.Errorf("prune: %v %v %v", err, plan, f.ran)
	}
	feat := filepath.Join(base, "feat")
	if err := s.WorktreeLock(gd, filepath.Join(base, "repo"), ""); err == nil {
		t.Error("locked the main folder")
	}
	if err := s.WorktreeUnlock(gd, feat); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Errorf("unlock unlocked: %v", err)
	}
	if err := s.WorktreeLock(gd, feat, "usb"); err != nil {
		t.Fatal(err)
	}
	f.list[1].Locked = true
	if err := s.WorktreeLock(gd, feat, ""); err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Errorf("double lock: %v", err)
	}
	if err := s.WorktreeUnlock(gd, feat); err != nil {
		t.Error(err)
	}
	if _, err := s.WorktreeRepair(gd); err != nil {
		t.Error(err)
	}
}
