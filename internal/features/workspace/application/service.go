// Package application holds the commit use case: commit the staged
// changes, or every change, as the repo's account. It depends only on
// domain ports. Import it as workspaceapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the commit use case.
type Service struct {
	Git      workspace.Git
	Accounts account.Store
	Repos    repo.Reader
	Locator  repo.Locator
	FS       *filesystem.FS // dry-run: a commit is only printed
	Undo     undo.Recorder  // remembers each commit so it can be undone; may be nil
}

// Commit commits the staged changes, or with all every change, new files
// included. It refuses while git is in the middle of another operation,
// with conflicts, with nothing to commit, without all when nothing is
// staged, and when the repo's email is not its account's: the commit
// would show someone else.
func (s *Service) Commit(gitDir, message string, all bool) (workspace.Committed, error) {
	r := workspace.Committed{All: all}
	message = workspace.Message(message)
	if message == "" {
		return r, errors.New("write a commit message: what the change does, e.g. \"Fix the login form\"")
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return r, err
	}
	r.Branch = h.Branch
	c := h.Changes
	switch {
	case h.Busy != "":
		return r, remote.Busy(h.Busy)
	case c.Conflicts > 0:
		return r, fmt.Errorf("%d file(s) still have conflicts; fix them first", c.Conflicts)
	case !c.Any():
		return r, errors.New("there is nothing to commit: no file has changed")
	case !all && c.Staged == 0:
		return r, errors.New("nothing is staged: stage the files to commit in your editor, or commit every change (new files too) with --all")
	}
	r.Files = workspace.Files(c, all)
	cfg := s.Accounts.Load()
	if a, _ := s.Repos.AccountFor(cfg, gitDir); a != nil {
		r.Account = a.ID
		if email := s.Repos.InfoFor(cfg, gitDir).Email; a.Email != "" && email != "" && !strings.EqualFold(email, a.Email) {
			return r, fmt.Errorf("this repo makes commits as %s, not as %s (%s), so the commit would show someone else. Use %s's name and email first: gitswitch use %s --once", email, a.ID, a.Email, a.ID, a.ID)
		}
	}
	wt := s.Locator.WorktreeOf(gitDir)
	what := "the staged changes"
	if all {
		what = "every change"
	}
	if s.FS.Dry(fmt.Sprintf("commit %s in %s: %q", what, wt, firstLine(message))) {
		return r, nil
	}
	var m undo.Mark
	if s.Undo != nil {
		m = s.Undo.Before(gitDir)
	}
	if err := s.Git.Commit(wt, message, all); err != nil {
		return r, err
	}
	if after, err := s.Git.Head(gitDir); err == nil {
		r.Hash, r.Short, r.Subject = after.Hash, after.Short, after.Subject
	}
	if s.Undo != nil {
		s.Undo.Done(gitDir, m, undo.Commit, "Committed "+r.Short+" "+r.Subject)
	}
	return r, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
