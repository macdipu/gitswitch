// Package application holds the history use cases: the commit graph with
// the account behind each commit, and fixing not-pushed commits made with
// the wrong account. It depends only on domain ports. Import it as
// historyapp.
package application

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the history use cases.
type Service struct {
	Accounts account.Store
	Repos    repo.Reader
	Locator  repo.Locator
	Log      history.Log
	FS       *filesystem.FS // dry-run: rewrites are only printed
}

// Build loads a repo's latest commits with lanes, accounts and flags. With
// an active filter only matching commits are loaded, without graph lines
// (they would not connect).
func (s *Service) Build(gitDir string, limit int, f history.Filter) (*history.History, error) {
	return s.build(s.Accounts.Load(), gitDir, limit, f)
}

func (s *Service) build(c *account.Config, gitDir string, limit int, f history.Filter) (*history.History, error) {
	gf := f
	if f.Account != "" && f.Author == "" {
		// Ask git for the account's commits so older ones are found too;
		// Keep below drops commits of other accounts.
		if a := c.Account(f.Account); a != nil && a.Email != "" {
			gf.Author = a.Email
		}
	}
	gf, none, err := gf.ForGit(time.Now())
	if err != nil {
		return nil, err
	}
	var cs []history.Commit
	if !none {
		if cs, err = s.Log.Commits(gitDir, limit, gf); err != nil {
			return nil, err
		}
	}
	h := &history.History{Repo: s.Repos.InfoFor(c, gitDir)}
	if !f.Active() {
		h.Lanes = history.LayoutLanes(cs)
	}
	history.Annotate(c, h, cs, s.Log.Unpushed(gitDir))
	if f.Active() {
		h.Commits = f.Keep(h.Commits)
		h.Lanes = 1
		h.Flagged, h.Unpushed = 0, 0
		for _, cm := range h.Commits {
			if cm.Flagged {
				h.Flagged++
			}
			if cm.Unpushed {
				h.Unpushed++
			}
		}
	}
	if h.Commits == nil {
		h.Commits = []history.Commit{} // JSON [] rather than null
	}
	return h, nil
}

// Lines renders git's own graph with the account of each commit (used by
// `gitswitch log` and the terminal app). flaggedOnly lists only
// wrong-account commits.
func (s *Service) Lines(gitDir string, n int, flaggedOnly, color bool, f history.Filter) ([]string, *history.History, error) {
	rows, hist, err := s.Rows(gitDir, n, flaggedOnly, color, f)
	return history.Texts(rows), hist, err
}

// Rows is Lines with the commit on each line, for front ends that let you
// pick a commit.
func (s *Service) Rows(gitDir string, n int, flaggedOnly, color bool, f history.Filter) ([]history.Row, *history.History, error) {
	c := s.Accounts.Load()
	hist, err := s.build(c, gitDir, n, f)
	if err != nil {
		return nil, nil, err
	}
	if flaggedOnly {
		return history.FlaggedRows(hist, n, color), hist, nil
	}
	if f.Active() {
		return history.ListRows(c, hist, color), hist, nil
	}
	graph, err := s.Log.Graph(gitDir, n, color)
	if errors.Is(err, history.ErrNoCommits) {
		return []history.Row{{Text: "This repo has no commits yet."}}, hist, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return history.GraphRows(c, hist, graph, color), hist, nil
}

// FixAuthor rewrites the author of not-pushed commits on the current branch
// that used another account's email. Pushed commits are never touched. It
// returns how many commits were (or, in dry-run, would be) fixed.
func (s *Service) FixAuthor(gitDir string) (int, error) {
	c := s.Accounts.Load()
	a, _ := s.Repos.AccountFor(c, gitDir)
	if a == nil || a.Email == "" {
		return 0, fmt.Errorf("this repo has no account yet; run `gitswitch use <account>` first")
	}
	wt := s.Locator.WorktreeOf(gitDir)
	local, err := s.Log.LocalCommits(wt)
	if err != nil {
		return 0, err
	}
	if len(local) == 0 {
		return 0, nil
	}
	bad := 0
	for _, h := range local {
		if history.IsWrongAccount(c, a, s.Log.AuthorEmail(wt, h), true) {
			bad++
		}
	}
	if bad == 0 {
		return 0, nil
	}
	if s.FS.Dry(fmt.Sprintf("rewrite the author of %d not-pushed commit(s) in %s to %s", bad, wt, a.Email)) {
		return bad, nil
	}
	name := a.Name
	if name == "" {
		name = s.Log.HeadAuthorName(wt)
	}
	if err := s.Log.RewriteAuthor(wt, name, a.Email, local[0]); err != nil {
		return 0, err
	}
	return bad, nil
}

// Revert adds a commit that undoes a commit on the current branch. It is
// the safe way to undo a commit that is already pushed. A merge commit is
// undone against its first parent. It returns the reverted commit.
func (s *Service) Revert(gitDir, rev string) (history.Target, error) {
	wt, t, err := s.pick(gitDir, rev)
	if err != nil {
		return t, err
	}
	if s.FS.Dry("revert " + t.Label() + " in " + wt) {
		return t, nil
	}
	return t, s.Log.Revert(wt, t)
}

// Drop removes a not-pushed commit from the current branch; the commits
// after it are kept. Pushed commits are refused (revert them instead), and
// so are merge commits and the first commit.
func (s *Service) Drop(gitDir, rev string) (history.Target, error) {
	wt, t, err := s.pick(gitDir, rev)
	if err != nil {
		return t, err
	}
	switch {
	case t.IsMerge():
		return t, errors.New("a merge commit cannot be dropped; roll back to the commit before it, or revert it")
	case len(t.Parents) == 0:
		return t, errors.New("the first commit of a repo cannot be dropped")
	}
	if err := s.notPushed(wt, t); err != nil {
		return t, err
	}
	if s.FS.Dry("drop " + t.Label() + " from " + s.Log.Branch(wt)) {
		return t, nil
	}
	return t, s.Log.Drop(wt, t)
}

// Rollback moves the current branch back to a commit. The commits after
// it are undone, and their changes stay in the working folder as
// uncommitted changes, so no work is lost. Every undone commit must be
// not pushed. It returns how many commits were undone.
func (s *Service) Rollback(gitDir, rev string) (int, history.Target, error) {
	wt, t, err := s.pick(gitDir, rev)
	if err != nil {
		return 0, t, err
	}
	after, err := s.Log.After(wt, t.Hash)
	if err != nil {
		return 0, t, err
	}
	if len(after) == 0 {
		return 0, t, fmt.Errorf("%s is the latest commit; there is nothing to roll back", t.Short)
	}
	local := map[string]bool{}
	if l, err := s.Log.LocalCommits(wt); err == nil {
		for _, h := range l {
			local[h] = true
		}
	}
	for _, h := range after {
		if !local[h] {
			return 0, t, fmt.Errorf("commits after %s are already pushed, so rolling back would rewrite pushed history; revert the commits you want to undo instead", t.Short)
		}
	}
	if s.FS.Dry(fmt.Sprintf("roll %s back to %s, keeping the changes of %d commit(s) as uncommitted changes", s.Log.Branch(wt), t.Label(), len(after))) {
		return len(after), t, nil
	}
	return len(after), t, s.Log.Reset(wt, t.Hash)
}

// pick checks the repo can be changed and finds a commit on its current
// branch.
func (s *Service) pick(gitDir, rev string) (string, history.Target, error) {
	wt, err := s.ready(gitDir)
	if err != nil {
		return "", history.Target{}, err
	}
	t, err := s.resolve(wt, rev)
	if err != nil {
		return "", t, err
	}
	if !s.Log.OnBranch(wt, t.Hash) {
		return "", t, fmt.Errorf("%s is not on the current branch (%s); switch to its branch first", t.Short, s.Log.Branch(wt))
	}
	return wt, t, nil
}

// ready checks the repo can be changed: on a branch, and git is not in the
// middle of another operation. It returns the working folder.
func (s *Service) ready(gitDir string) (string, error) {
	if op := s.Log.InProgress(gitDir); op != "" {
		return "", fmt.Errorf("git is in the middle of a %s in this repo; finish or abort it first", op)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.Log.Branch(wt) == "" {
		return "", errors.New("HEAD is detached; switch to a branch first")
	}
	return wt, nil
}

func (s *Service) resolve(wt, rev string) (history.Target, error) {
	rev = strings.TrimSpace(rev)
	if rev == "" || strings.HasPrefix(rev, "-") {
		return history.Target{}, fmt.Errorf("not a commit: %q", rev)
	}
	return s.Log.Resolve(wt, rev)
}

// CherryPick copies commits from other branches onto the current branch,
// oldest first, as new commits. If any of them does not apply cleanly,
// nothing changes. Merge commits and commits already on the branch are
// refused.
func (s *Service) CherryPick(gitDir string, revs []string) ([]history.Target, error) {
	wt, err := s.ready(gitDir)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, errors.New("choose one or more commits to cherry-pick")
	}
	var ts []history.Target
	seen := map[string]bool{}
	for _, rev := range revs {
		t, err := s.resolve(wt, rev)
		if err != nil {
			return nil, err
		}
		switch {
		case seen[t.Hash]:
			continue
		case t.IsMerge():
			return nil, fmt.Errorf("%s is a merge commit; cherry-pick the commits it brought in instead", t.Short)
		case s.Log.OnBranch(wt, t.Hash):
			return nil, fmt.Errorf("%s is already on the current branch (%s)", t.Short, s.Log.Branch(wt))
		}
		seen[t.Hash] = true
		ts = append(ts, t)
	}
	hashes := make([]string, len(ts))
	for i, t := range ts {
		hashes[i] = t.Hash
	}
	order, err := s.Log.Order(wt, hashes)
	if err != nil {
		return nil, err
	}
	pos := map[string]int{}
	for i, h := range order {
		pos[h] = i
	}
	sort.SliceStable(ts, func(i, j int) bool { return pos[ts[i].Hash] < pos[ts[j].Hash] })
	labels := make([]string, len(ts))
	for i, t := range ts {
		labels[i] = t.Label()
	}
	if s.FS.Dry("cherry-pick onto " + s.Log.Branch(wt) + ": " + strings.Join(labels, ", ")) {
		return ts, nil
	}
	return ts, s.Log.CherryPick(wt, ts)
}

// WebURL is the page of a commit on the repo's host (GitHub, GitLab,
// ...), or of the repo itself when rev is "". It uses the origin remote,
// else the first one. A commit that is not pushed yet is refused: the
// host does not have it.
func (s *Service) WebURL(gitDir, rev string) (string, error) {
	info := s.Repos.InfoFor(s.Accounts.Load(), gitDir)
	remoteURL := ""
	for _, r := range info.Remotes {
		if remoteURL == "" || r.Name == "origin" {
			remoteURL = r.URL
		}
	}
	if remote.WebURL(remoteURL) == "" {
		return "", errors.New("this repo has no remote on a server to show it on")
	}
	if strings.TrimSpace(rev) == "" {
		return remote.WebURL(remoteURL), nil
	}
	t, err := s.resolve(s.Locator.WorktreeOf(gitDir), rev)
	if err != nil {
		return "", err
	}
	if s.Log.Unpushed(gitDir)[t.Hash] {
		return "", fmt.Errorf("%s is not pushed yet, so the server does not have it", t.Short)
	}
	return remote.CommitURL(remoteURL, t.Hash), nil
}

// notPushed refuses a commit that is on a remote.
func (s *Service) notPushed(wt string, t history.Target) error {
	local, err := s.Log.LocalCommits(wt)
	if err != nil {
		return err
	}
	for _, h := range local {
		if h == t.Hash {
			return nil
		}
	}
	return fmt.Errorf("%s is already pushed, so dropping it would rewrite pushed history; revert it instead", t.Short)
}
