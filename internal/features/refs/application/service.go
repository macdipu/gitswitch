// Package application holds the branch use cases: show where a repo is,
// list its branches, switch branch and update the branch from its remote.
// It depends only on domain ports. Import it as refsapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the branch use cases.
type Service struct {
	Refs    refs.Refs
	Locator repo.Locator
	FS      *filesystem.FS // dry-run: changes are only printed
	Undo    undo.Recorder  // remembers each switch so it can be undone; may be nil
}

// Head reads where a repo is: branch, HEAD commit and upstream.
func (s *Service) Head(gitDir string) (refs.Head, error) { return s.Refs.Head(gitDir) }

// Branches lists local branches, then remote ones.
func (s *Service) Branches(gitDir string) ([]refs.Branch, error) { return s.Refs.Branches(gitDir) }

// Switch checks out a branch. A remote branch (origin/x) checks out the
// local branch x, creating it to track the remote one when it does not
// exist; it is refused when x exists but pulls from another remote branch.
// Git refuses when uncommitted changes would be overwritten. It returns the
// local branch now checked out.
func (s *Service) Switch(gitDir, name string) (string, error) {
	p, err := s.plan(gitDir, name)
	if err != nil {
		return p.local, err
	}
	if s.FS.Dry(fmt.Sprintf("switch %s to branch %s", p.wt, p.local)) {
		return p.local, nil
	}
	m := s.before(gitDir)
	if err := s.checkout(p); err != nil {
		return p.local, err
	}
	s.done(gitDir, m, p)
	return p.local, nil
}

// SmartSwitch is Switch that takes the uncommitted changes along. When git
// can switch keeping them, it just switches. When it refuses because they
// would be overwritten, they are stashed, the branch is switched and they
// are put back. If some clash with the new branch, the switch stays done:
// those files have conflict markers to fix, and the original changes stay
// in the stash until ResolveDone (or ResolveAbort goes back). If they
// cannot be put back at all, it switches back and restores them.
func (s *Service) SmartSwitch(gitDir, name string) (refs.Switched, error) {
	p, err := s.plan(gitDir, name)
	r := refs.Switched{Branch: p.local, Carried: p.head.Changes.Any()}
	if err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("switch %s to branch %s, taking the uncommitted changes along", p.wt, p.local)) {
		return r, nil
	}
	m := s.before(gitDir)
	err = s.checkout(p)
	if err == nil || !refs.WouldOverwrite(err) {
		if err == nil {
			s.done(gitDir, m, p)
		}
		return r, err
	}
	stash, err := s.Refs.Stash(p.wt, "gitswitch smart switch: "+p.head.Branch+" → "+p.local)
	if err != nil {
		return r, fmt.Errorf("could not set your changes aside, nothing changed: %v", err)
	}
	if err := s.checkout(p); err != nil {
		return r, s.restore(p.wt, stash, err)
	}
	files, err := s.Refs.Apply(p.wt, stash)
	if err != nil {
		// Not a clash git could mark: go back to where they were made.
		if serr := s.Refs.Switch(p.wt, p.head.Branch); serr != nil {
			return r, fmt.Errorf("switched to %s, but your changes could not be put back (%v), nor could GitSwitch switch back (%v). They are safe in the stash: `git stash list`", p.local, err, serr)
		}
		return r, s.restore(p.wt, stash, fmt.Errorf("your changes could not be put back on %s, so GitSwitch stayed on %s: %v", p.local, p.head.Branch, err))
	}
	s.done(gitDir, m, p)
	if len(files) == 0 {
		return r, s.Refs.DropStash(p.wt, stash)
	}
	r.Clash = &refs.Clash{From: p.head.Branch, To: p.local, Stash: stash, Files: files}
	return r, s.Refs.SaveClash(gitDir, *r.Clash)
}

// restore puts stashed changes back where they were made after a failed
// switch, and returns why the switch failed.
func (s *Service) restore(wt, stash string, why error) error {
	if _, err := s.Refs.Apply(wt, stash); err != nil {
		return fmt.Errorf("%v. Your changes could not be put back either; they are safe in the stash: `git stash list`", why)
	}
	_ = s.Refs.DropStash(wt, stash)
	return why
}

// Clash is the repo's open smart-switch clash; nil when there is none.
func (s *Service) Clash(gitDir string) (*refs.Clash, error) { return s.Refs.LoadClash(gitDir) }

// ResolveDone finishes a smart switch once the clashing files are fixed:
// it refuses while conflict markers remain, then makes every file a plain
// uncommitted change and drops the stashed copy.
func (s *Service) ResolveDone(gitDir string) error {
	c, wt, err := s.clash(gitDir)
	if err != nil {
		return err
	}
	if left := s.Refs.Markers(wt, c.Files); len(left) > 0 {
		return fmt.Errorf("these files still have conflict markers: %s. Keep the lines you want and delete the <<<<<<<, ======= and >>>>>>> lines, then try again", strings.Join(left, ", "))
	}
	if s.FS.Dry("finish the smart switch to " + c.To + ": keep the fixed files, drop the stashed copy") {
		return nil
	}
	if err := s.Refs.Unstage(wt); err != nil {
		return err
	}
	if err := s.Refs.DropStash(wt, c.Stash); err != nil {
		return err
	}
	return s.Refs.ClearClash(gitDir)
}

// ResolveAbort goes back to before a smart switch that clashed: the
// half-applied changes (and any fixes made since) are thrown away, the
// branch they were made on is checked out, and they are restored from the
// stashed copy exactly as they were.
func (s *Service) ResolveAbort(gitDir string) error {
	c, wt, err := s.clash(gitDir)
	if err != nil {
		return err
	}
	if s.FS.Dry("go back to " + c.From + " with your changes as they were before the switch") {
		return nil
	}
	if err := s.Refs.Discard(wt, c.Stash); err != nil {
		return err
	}
	if err := s.Refs.Switch(wt, c.From); err != nil {
		return fmt.Errorf("could not switch back to %s: %v. Your changes are safe in the stash: `git stash list`", c.From, err)
	}
	if files, err := s.Refs.Apply(wt, c.Stash); err != nil || len(files) > 0 {
		return fmt.Errorf("back on %s, but your changes did not apply cleanly (%v); they are still safe in the stash: `git stash list`", c.From, err)
	}
	if err := s.Refs.DropStash(wt, c.Stash); err != nil {
		return err
	}
	return s.Refs.ClearClash(gitDir)
}

func (s *Service) clash(gitDir string) (*refs.Clash, string, error) {
	c, err := s.Refs.LoadClash(gitDir)
	if err != nil {
		return nil, "", err
	}
	if c == nil {
		return nil, "", errors.New("there is no smart switch to finish in this repo")
	}
	return c, s.Locator.WorktreeOf(gitDir), nil
}

// switchPlan is a checked switch: what to check out and how.
type switchPlan struct {
	head   refs.Head
	wt     string
	local  string       // the local branch to check out
	remote *refs.Branch // the remote branch to track when local does not exist yet; nil otherwise
}

// plan checks a switch can be made.
func (s *Service) plan(gitDir, name string) (switchPlan, error) {
	var p switchPlan
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") {
		return p, fmt.Errorf("not a branch: %q", name)
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return p, err
	}
	p.head = h
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return p, err
	}
	b := refs.Find(all, name)
	if b == nil {
		return p, fmt.Errorf("no branch %q (see `gitswitch branch`)", name)
	}
	p.local = b.LocalName()
	l := refs.Find(all, p.local) // local branches are found first
	exists := l != nil && !l.Remote
	// upstream/main when the local main pulls from origin/main: switching
	// to main would quietly put you on another remote's branch.
	if b.Remote && exists && l.Upstream != "" && l.Upstream != b.Name {
		alt := strings.ReplaceAll(b.Name, "/", "-")
		return p, fmt.Errorf("the local branch %s pulls from %s, not %s, so gitswitch can't switch to %s through it. Switch to %s instead, or make a separate branch for %s in a terminal: git switch -c %s --track %s", p.local, l.Upstream, b.Name, b.Name, p.local, b.Name, alt, b.Name)
	}
	if p.local == h.Branch {
		return p, fmt.Errorf("already on %s", p.local)
	}
	if !exists {
		p.remote = b
	}
	p.wt = s.Locator.WorktreeOf(gitDir)
	return p, nil
}

func (s *Service) checkout(p switchPlan) error {
	if p.remote != nil {
		return s.Refs.Track(p.wt, p.local, p.remote.Name)
	}
	return s.Refs.Switch(p.wt, p.local)
}

func (s *Service) before(gitDir string) undo.Mark {
	if s.Undo == nil {
		return undo.Mark{}
	}
	return s.Undo.Before(gitDir)
}

func (s *Service) done(gitDir string, m undo.Mark, p switchPlan) {
	if s.Undo != nil {
		s.Undo.Done(gitDir, m, undo.Switch, "Switched from "+p.head.Branch+" to "+p.local)
	}
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
	if h.Upstream == "" && h.Gone != "" {
		rem, rb, _ := strings.Cut(h.Gone, "/")
		return h, fmt.Errorf("%s pulls from %s, but this clone has no copy of that remote branch: it was deleted on the server, or this is a single-branch clone. If it is still on the server, get it with: git fetch %s %s:refs/remotes/%s. If it was deleted, push the branch again (git push -u %s %s) or pull from another branch (git branch --set-upstream-to=<remote>/<branch>)", h.Branch, h.Gone, rem, rb, h.Gone, rem, h.Branch)
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
		return h, remote.Busy(h.Busy)
	}
	return h, nil
}
