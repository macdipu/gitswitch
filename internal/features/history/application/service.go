// Package application holds the history use cases: the commit graph with
// the account behind each commit, and fixing not-pushed commits made with
// the wrong account. It depends only on domain ports. Import it as
// historyapp.
package application

import (
	"errors"
	"fmt"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
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

// Build loads a repo's latest commits with lanes, accounts and flags.
func (s *Service) Build(gitDir string, limit int) (*history.History, error) {
	return s.build(s.Accounts.Load(), gitDir, limit)
}

func (s *Service) build(c *account.Config, gitDir string, limit int) (*history.History, error) {
	cs, err := s.Log.Commits(gitDir, limit)
	if err != nil {
		return nil, err
	}
	h := &history.History{Repo: s.Repos.InfoFor(c, gitDir)}
	h.Lanes = history.LayoutLanes(cs)
	history.Annotate(c, h, cs, s.Log.Unpushed(gitDir))
	return h, nil
}

// Lines renders git's own graph with the account of each commit (used by
// `gitswitch log` and the terminal app). flaggedOnly lists only
// wrong-account commits.
func (s *Service) Lines(gitDir string, n int, flaggedOnly, color bool) ([]string, *history.History, error) {
	c := s.Accounts.Load()
	hist, err := s.build(c, gitDir, n)
	if err != nil {
		return nil, nil, err
	}
	if flaggedOnly {
		return history.FlaggedLines(hist, n, color), hist, nil
	}
	graph, err := s.Log.Graph(gitDir, n, color)
	if errors.Is(err, history.ErrNoCommits) {
		return []string{"This repo has no commits yet."}, hist, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return history.GraphLines(c, hist, graph, color), hist, nil
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
