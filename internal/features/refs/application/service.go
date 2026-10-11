// Package application holds the branch use cases: show where a repo is,
// list its branches, switch branch and update the branch from its remote.
// It depends only on domain ports. Import it as refsapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the branch use cases.
type Service struct {
	Refs    refs.Refs
	Locator repo.Locator
	FS      *filesystem.FS // dry-run: changes are only printed
}

// Head reads where a repo is: branch, HEAD commit and upstream.
func (s *Service) Head(gitDir string) (refs.Head, error) { return s.Refs.Head(gitDir) }

// Branches lists local branches, then remote ones.
func (s *Service) Branches(gitDir string) ([]refs.Branch, error) { return s.Refs.Branches(gitDir) }

// Switch checks out a branch. A remote branch (origin/x) checks out the
// local branch x, creating it to track the remote one when it does not
// exist. Git refuses when uncommitted changes would be overwritten. It
// returns the local branch now checked out.
func (s *Service) Switch(gitDir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("not a branch: %q", name)
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return "", err
	}
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return "", err
	}
	b := refs.Find(all, name)
	if b == nil {
		return "", fmt.Errorf("no branch %q (see `gitswitch branch`)", name)
	}
	local := b.LocalName()
	if local == h.Branch {
		return local, fmt.Errorf("already on %s", local)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	l := refs.Find(all, local) // local branches are found first
	exists := l != nil && !l.Remote
	if s.FS.Dry(fmt.Sprintf("switch %s to branch %s", wt, local)) {
		return local, nil
	}
	if exists {
		return local, s.Refs.Switch(wt, local)
	}
	return local, s.Refs.Track(wt, local, b.Name)
}

// Update brings the current branch up to date with its upstream (git pull
// --ff-only, through the repo's account). It never creates a merge commit:
// when both sides have new commits it stops and explains. Git's progress
// lines go to progress. It returns the new head.
func (s *Service) Update(gitDir string, progress func(string)) (refs.Head, error) {
	h, err := s.ready(gitDir)
	if err != nil {
		return h, err
	}
	if h.Branch == "" {
		return h, errors.New("HEAD is detached; switch to a branch first")
	}
	if h.Upstream == "" {
		return h, fmt.Errorf("%s is not on a remote yet; push it first: git push -u origin %s", h.Branch, h.Branch)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("update %s from %s (git pull --ff-only)", h.Branch, h.Upstream)) {
		return h, nil
	}
	if err := s.Refs.Pull(wt, progress); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not possible to fast-forward") || strings.Contains(low, "diverging branches") {
			return h, fmt.Errorf("%v\n\n%s and %s both have new commits, so they cannot be updated without a merge. Roll back or drop your not-pushed commits, or merge them in a terminal: git pull --rebase", err, h.Branch, h.Upstream)
		}
		return h, err
	}
	return s.Refs.Head(gitDir)
}

// ready reads the head and refuses while git is in the middle of another
// operation.
func (s *Service) ready(gitDir string) (refs.Head, error) {
	h, err := s.Refs.Head(gitDir)
	if err != nil {
		return h, err
	}
	if h.Busy != "" {
		return h, fmt.Errorf("git is in the middle of a %s in this repo; finish or abort it first", h.Busy)
	}
	return h, nil
}
