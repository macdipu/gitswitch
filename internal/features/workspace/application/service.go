// Package application holds the working folder's use cases: list and diff
// the uncommitted changes, stage, unstage and discard them, and commit the
// staged changes, or every change, as the repo's account. It depends only on
// domain ports. Import it as workspaceapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the commit use case and the working folder's changes:
// list, diff, stage, unstage, discard.
type Service struct {
	Git      workspace.Git
	Changes  workspace.Changes
	Ignorer  workspace.Ignorer // edits .gitignore and info/exclude; may be nil
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
	if r.Account, err = s.account(gitDir); err != nil {
		return r, err
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

// account is the repo's gitswitch account ("" for none), refused when the
// repo's email is not the account's: a commit would show someone else.
func (s *Service) account(gitDir string) (string, error) {
	cfg := s.Accounts.Load()
	a, _ := s.Repos.AccountFor(cfg, gitDir)
	if a == nil {
		return "", nil
	}
	if email := s.Repos.InfoFor(cfg, gitDir).Email; a.Email != "" && email != "" && !strings.EqualFold(email, a.Email) {
		return a.ID, fmt.Errorf("this repo makes commits as %s, not as %s (%s), so the commit would show someone else. Use %s's name and email first: gitswitch use %s --once", email, a.ID, a.Email, a.ID, a.ID)
	}
	return a.ID, nil
}

// Amendable says whether the last commit may be amended now, with its
// message to start a new one from. Whether there is anything to amend
// depends on the new message, so Amend checks that.
func (s *Service) Amendable(gitDir string) workspace.Amendable {
	_, l, err := s.amendable(gitDir)
	r := workspace.Amendable{Can: err == nil, Short: l.Short, Subject: l.Subject, Message: l.Message}
	if err != nil {
		r.Why = err.Error()
	}
	return r
}

func (s *Service) amendable(gitDir string) (refs.Head, workspace.Last, error) {
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return h, workspace.Last{}, err
	}
	if h.Hash == "" {
		return h, workspace.Last{}, errors.New("there is no commit to amend yet; make the first commit instead")
	}
	l, err := s.Git.Last(gitDir)
	if err != nil {
		return h, l, err
	}
	switch {
	case h.Busy != "":
		return h, l, remote.Busy(h.Busy)
	case h.Changes.Conflicts > 0:
		return h, l, fmt.Errorf("%d file(s) still have conflicts; fix them first", h.Changes.Conflicts)
	case l.Parents > 1:
		return h, l, fmt.Errorf("the last commit (%s) is a merge commit; GitSwitch does not amend merges, make a new commit instead", l.Short)
	case l.Pushed:
		return h, l, fmt.Errorf("the last commit (%s) is already pushed, so amending it would rewrite pushed history; make a new commit instead", l.Short)
	case h.Gone != "":
		return h, l, fmt.Errorf("%s pushes to %s, which this clone has no copy of, so GitSwitch can't tell whether the last commit is pushed; fetch and try again, or make a new commit instead", h.Branch, h.Gone)
	}
	_, err = s.account(gitDir)
	return h, l, err
}

// Amend replaces the last commit with one that also holds the staged
// changes, or with all every change, new files included. Message ""
// keeps its message. It refuses when there is no commit yet, while git is
// in the middle of another operation, with conflicts, for a merge commit,
// for a commit that is already pushed, when the repo's email is not its
// account's, and when nothing would change: nothing to add and the same
// message. The amend can be undone, which puts the commit back as it was.
func (s *Service) Amend(gitDir, message string, all bool) (workspace.Committed, error) {
	r := workspace.Committed{All: all}
	message = workspace.Message(message)
	h, l, err := s.amendable(gitDir)
	r.Branch, r.Amended = h.Branch, l.Short
	if err != nil {
		return r, err
	}
	c := h.Changes
	adds := c.Staged > 0 || (all && c.Any())
	if !adds && (message == "" || message == l.Message) {
		if all {
			return r, errors.New("there is nothing to amend: no file has changed, and the message is the same")
		}
		return r, errors.New("there is nothing to amend: nothing is staged, and the message is the same. Stage changes, amend with every change (--all), or write a new message")
	}
	r.Account, _ = s.account(gitDir)
	wt := s.Locator.WorktreeOf(gitDir)
	what := "the message"
	switch {
	case all && adds:
		what = "every change"
	case adds:
		what = "the staged changes"
	}
	if s.FS.Dry(fmt.Sprintf("amend %s %s with %s in %s", l.Short, l.Subject, what, wt)) {
		return r, nil
	}
	var m undo.Mark
	if s.Undo != nil {
		m = s.Undo.Before(gitDir)
	}
	if err := s.Git.Amend(wt, message, all); err != nil {
		return r, err
	}
	if after, err := s.Git.Last(gitDir); err == nil {
		r.Hash, r.Short, r.Subject, r.Files = after.Hash, after.Short, after.Subject, after.Files
	}
	if s.Undo != nil {
		s.Undo.Done(gitDir, m, undo.Amend, "Amended "+l.Short+" into "+r.Short+" "+r.Subject)
	}
	return r, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
