package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

func (s *Service) worktreeGit() (refs.WorktreeGit, error) {
	g, ok := s.Refs.(refs.WorktreeGit)
	if !ok {
		return nil, errors.New("worktrees are not available")
	}
	return g, nil
}

// Worktrees lists the repo's worktrees, the main one first, each with the
// account the repo uses (linked worktrees share the repo's settings).
func (s *Service) Worktrees(gitDir string) ([]refs.Worktree, error) {
	g, err := s.worktreeGit()
	if err != nil {
		return nil, err
	}
	all, err := g.Worktrees(gitDir)
	if err != nil {
		return nil, err
	}
	acc := ""
	if s.Repos != nil && s.Accounts != nil {
		acc = s.Repos.InfoFor(s.Accounts.Load(), gitDir).Account
	}
	for i := range all {
		all[i].Account = acc
	}
	return all, nil
}

// samePlace compares folders after resolving links (git lists real paths).
func samePlace(a, b string) bool {
	return filesystem.SamePath(a, b) || filesystem.SamePath(filesystem.RealPath(a), filesystem.RealPath(b))
}

func findWorktree(all []refs.Worktree, path string) (refs.Worktree, error) {
	if strings.TrimSpace(path) == "" {
		return refs.Worktree{}, errors.New("which worktree? give its folder")
	}
	for _, w := range all {
		if samePlace(w.Path, path) {
			return w, nil
		}
	}
	return refs.Worktree{}, fmt.Errorf("%s is not a worktree of this repo (gitswitch worktree lists them)", path)
}

// inside reports whether path is dir or below it.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(filesystem.RealPath(dir), filesystem.RealPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WorktreeAdd makes a worktree at path (an absolute folder, "~" allowed).
// It is refused when the folder exists and is not empty, when it would sit
// inside the repo's own working folder (git would then show it as
// untracked files of the main repo), or when the branch is already checked
// out in another working folder. The worktree shares the repo's settings,
// so it uses the repo's account from the first commit; the caller may
// register it in the repo list.
func (s *Service) WorktreeAdd(gitDir, path string, o refs.WorktreeAddOptions) (refs.WorktreeAdded, error) {
	var res refs.WorktreeAdded
	g, err := s.worktreeGit()
	if err != nil {
		return res, err
	}
	path = filesystem.ExpandHome(strings.TrimSpace(path))
	if path == "" {
		return res, errors.New("where should the worktree go? give a folder")
	}
	if path, err = filepath.Abs(path); err != nil {
		return res, err
	}
	modes := 0
	for _, v := range []string{o.Branch, o.NewBranch, o.Detach} {
		if strings.TrimSpace(v) != "" {
			modes++
		}
	}
	switch {
	case modes == 0:
		return res, errors.New("say what to check out: a branch, a new branch, or --detach <commit>")
	case modes > 1:
		return res, errors.New("pick one: an existing branch, a new branch, or a detached commit")
	}
	for _, v := range []string{o.Branch, o.NewBranch, o.From, o.Detach} {
		if strings.HasPrefix(v, "-") {
			return res, fmt.Errorf("%q is not a branch or commit name", v)
		}
	}
	if o.From != "" && o.NewBranch == "" {
		return res, errors.New("--from needs a new branch (-b <name>)")
	}
	all, err := g.Worktrees(gitDir)
	if err != nil {
		return res, err
	}
	for _, w := range all {
		if samePlace(w.Path, path) {
			return res, fmt.Errorf("%s is already a worktree of this repo", path)
		}
	}
	if len(all) > 0 && !all[0].Bare && inside(all[0].Path, path) {
		return res, fmt.Errorf("%s is inside this repo's own folder (%s); git would show it as untracked files. Put it beside the repo, e.g. %s", path, all[0].Path, filepath.Join(filepath.Dir(all[0].Path), filepath.Base(all[0].Path)+"-"+filepath.Base(path)))
	}
	if ents, err := os.ReadDir(path); err == nil && len(ents) > 0 {
		return res, fmt.Errorf("%s already exists and is not empty; pick a new folder or an empty one", path)
	} else if err == nil {
		// an empty folder is fine, git uses it
	} else if st, serr := os.Stat(path); serr == nil && !st.IsDir() {
		return res, fmt.Errorf("%s is a file", path)
	}
	if o.Branch != "" {
		for _, w := range all {
			if w.Branch == o.Branch {
				return res, fmt.Errorf("%s is already checked out in %s; use another branch, or --detach it here", o.Branch, w.Path)
			}
		}
	}
	if s.FS.Dry(fmt.Sprintf("add a worktree at %s", path)) {
		res.Path = path
		return res, nil
	}
	if err := g.WorktreeAdd(gitDir, path, o); err != nil {
		return res, err
	}
	res.Path = path
	after, err := g.Worktrees(gitDir)
	if err != nil {
		return res, err
	}
	w, err := findWorktree(after, path)
	if err == nil {
		res.Path, res.Branch, res.Head = w.Path, w.Branch, w.Short
	}
	// Linked worktrees read the main repo's config, so the identity is the
	// repo's; check that the account resolves the same here.
	if s.Repos != nil && s.Accounts != nil && s.Locator != nil {
		c := s.Accounts.Load()
		res.Account = s.Repos.InfoFor(c, gitDir).Account
		if wd := s.Locator.GitDirFor(path); wd != "" {
			if got := s.Repos.InfoFor(c, wd).Account; got != res.Account {
				res.Warnings = append(res.Warnings, fmt.Sprintf("this worktree resolves to %q, the repo to %q; run gitswitch use in the worktree", got, res.Account))
			}
		}
	}
	return res, nil
}

// WorktreeRemove deletes a worktree's folder and forgets it; its branch and
// commits stay. The main worktree and a locked one (unlock it first) are
// refused; one with uncommitted or untracked files is refused unless force,
// and the message says what would be lost.
func (s *Service) WorktreeRemove(gitDir, path string, force bool) error {
	g, err := s.worktreeGit()
	if err != nil {
		return err
	}
	all, err := g.Worktrees(gitDir)
	if err != nil {
		return err
	}
	w, err := findWorktree(all, path)
	if err != nil {
		return err
	}
	switch {
	case w.Main:
		return errors.New("that is the repo's own folder, not a linked worktree; it can't be removed here")
	case w.Locked:
		return fmt.Errorf("%s is locked%s; unlock it first (gitswitch worktree unlock)", w.Path, reasonNote(w.LockReason))
	case w.Prunable:
		return fmt.Errorf("the folder of %s is gone; `gitswitch worktree prune` forgets it", w.Path)
	}
	if w.Changes != nil && w.Changes.Any() && !force {
		return fmt.Errorf("%s has uncommitted work that would be lost (%s); commit or stash it, or remove with --force", w.Path, w.Changes.Describe())
	}
	if s.FS.Dry("remove the worktree " + w.Path) {
		return nil
	}
	return g.WorktreeRemove(gitDir, w.Path, force)
}

func reasonNote(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

// WorktreePrunePlan lists the worktrees Prune would forget: those whose
// folder is gone and that are not locked.
func (s *Service) WorktreePrunePlan(gitDir string) ([]refs.Worktree, error) {
	g, err := s.worktreeGit()
	if err != nil {
		return nil, err
	}
	all, err := g.Worktrees(gitDir)
	if err != nil {
		return nil, err
	}
	plan := []refs.Worktree{}
	for _, w := range all {
		if w.Prunable && !w.Locked && !w.Main {
			plan = append(plan, w)
		}
	}
	return plan, nil
}

// WorktreePrune forgets the worktrees WorktreePrunePlan lists, and returns
// them. Their folders are already gone; no files are touched.
func (s *Service) WorktreePrune(gitDir string) ([]refs.Worktree, error) {
	plan, err := s.WorktreePrunePlan(gitDir)
	if err != nil || len(plan) == 0 {
		return plan, err
	}
	g, _ := s.worktreeGit()
	if s.FS.Dry(fmt.Sprintf("forget %d worktree(s) whose folder is gone", len(plan))) {
		return plan, nil
	}
	return plan, g.WorktreePrune(gitDir)
}

// WorktreeLock protects a linked worktree from being pruned or removed
// (for one on a drive that is not always connected).
func (s *Service) WorktreeLock(gitDir, path, reason string) error {
	g, w, err := s.worktreeFor(gitDir, path)
	if err != nil {
		return err
	}
	if w.Main {
		return errors.New("the repo's own folder can't be locked")
	}
	if w.Locked {
		return fmt.Errorf("%s is already locked%s", w.Path, reasonNote(w.LockReason))
	}
	if s.FS.Dry("lock the worktree " + w.Path) {
		return nil
	}
	return g.WorktreeLock(gitDir, w.Path, reason)
}

// WorktreeUnlock undoes WorktreeLock.
func (s *Service) WorktreeUnlock(gitDir, path string) error {
	g, w, err := s.worktreeFor(gitDir, path)
	if err != nil {
		return err
	}
	if !w.Locked {
		return fmt.Errorf("%s is not locked", w.Path)
	}
	if s.FS.Dry("unlock the worktree " + w.Path) {
		return nil
	}
	return g.WorktreeUnlock(gitDir, w.Path)
}

func (s *Service) worktreeFor(gitDir, path string) (refs.WorktreeGit, refs.Worktree, error) {
	g, err := s.worktreeGit()
	if err != nil {
		return nil, refs.Worktree{}, err
	}
	all, err := g.Worktrees(gitDir)
	if err != nil {
		return nil, refs.Worktree{}, err
	}
	w, err := findWorktree(all, path)
	return g, w, err
}

// WorktreeRepair fixes the links between the repo and worktrees that were
// moved by hand (given their new folders; none means the repo's own check).
func (s *Service) WorktreeRepair(gitDir string, paths ...string) (string, error) {
	g, err := s.worktreeGit()
	if err != nil {
		return "", err
	}
	if s.FS.Dry("repair worktree links") {
		return "", nil
	}
	return g.WorktreeRepair(gitDir, paths...)
}

// ruleFolder is the folder folder rules look at: the repo's own working
// folder, also for a linked worktree (see gitinfra.Runner.RuleFolderOf).
func (s *Service) ruleFolder(gitDir string) string {
	if l, ok := s.Locator.(interface{ RuleFolderOf(string) string }); ok {
		return l.RuleFolderOf(gitDir)
	}
	return s.Locator.WorktreeOf(gitDir)
}
