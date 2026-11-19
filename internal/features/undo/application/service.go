// Package application holds the undo use cases: remember the last action
// GitSwitch made in a repo, and undo it when nothing has happened since.
// It depends only on domain ports. Import it as undoapp.
package application

import (
	"errors"
	"fmt"
	"strings"
	"time"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the undo use cases. It is also the Recorder the history and
// branch use cases report their actions to.
type Service struct {
	Store undo.Store
	Git   undo.Git
	FS    *filesystem.FS // dry-run: an undo is only printed
	Now   func() time.Time
}

// Before reads where the repo is before an action.
func (s *Service) Before(gitDir string) undo.Mark { return s.Git.Mark(gitDir) }

// Done remembers an action that succeeded, replacing the repo's previous
// one. An action that cannot be undone (HEAD did not move) forgets the
// previous one, so Undo never goes back past it. Recording is best effort:
// the action itself already succeeded.
func (s *Service) Done(gitDir string, before undo.Mark, a undo.Action, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r, ok := undo.New(a, label, before, s.Git.Mark(gitDir), now().Unix())
	if ok && a == undo.Move {
		// The branch left now ends where the commits were taken off.
		r.After = s.Git.Tip(gitDir, r.Branch)
		ok = r.After != "" && r.After != r.Before
	}
	if !ok || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Updated remembers a pull with rebase onto the upstream commit base.
func (s *Service) Updated(gitDir string, before undo.Mark, base, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r, ok := undo.New(undo.Update, label, before, s.Git.Mark(gitDir), now().Unix())
	r.Base = base
	if !ok || base == "" || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Merged remembers a merge of the commit theirs (named from) into the
// current branch: a fast-forward or a merge commit. Undo moves the branch
// back while the merge commit is not pushed.
func (s *Service) Merged(gitDir string, before undo.Mark, theirs, from, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r, ok := undo.New(undo.Merge, label, before, s.Git.Mark(gitDir), now().Unix())
	r.To, r.Base = from, theirs
	if !ok || theirs == "" || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Pruned remembers deleted branches, so they can be brought back.
func (s *Service) Pruned(gitDir string, before undo.Mark, branches []undo.Deleted, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r := undo.Record{Action: undo.Prune, Label: label, Worktree: before.Worktree, Branch: before.Branch,
		Before: before.Head, After: before.Head, Time: now().Unix(), Deleted: branches}
	if len(branches) == 0 || r.Before == "" || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Renamed remembers a renamed branch, so undo can rename it back.
func (s *Service) Renamed(gitDir string, before undo.Mark, from, to, hash, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r := undo.Record{Action: undo.Rename, Label: label, Worktree: before.Worktree, Branch: from, To: to,
		Before: hash, After: hash, Time: now().Unix()}
	if from == "" || to == "" || hash == "" || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Stashed remembers a stash that was saved (undo.StashSave) or dropped
// (undo.StashDrop): hash is its commit and subject its stash list line.
func (s *Service) Stashed(gitDir string, before undo.Mark, a undo.Action, hash, subject, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r := undo.Record{Action: a, Label: label, Worktree: before.Worktree, Branch: before.Branch, To: subject,
		Before: hash, After: hash, Time: now().Unix()}
	if hash == "" || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Status is the last action and whether it can be undone now.
type Status struct {
	undo.Record
	Effect  string `json:"effect"`  // what undoing does, in plain words
	Blocked string `json:"blocked"` // why it cannot be undone now; "" when it can
}

// Last reads the repo's last action; undo.ErrNothing when there is none.
func (s *Service) Last(gitDir string) (Status, error) {
	r, err := s.Store.Load(gitDir)
	if err != nil {
		return Status{}, err
	}
	st := Status{Record: r, Effect: r.Effect()}
	if err := s.check(gitDir, r); err != nil {
		st.Blocked = err.Error()
	}
	return st, nil
}

func (s *Service) check(gitDir string, r undo.Record) error {
	if err := r.Check(s.Git.Mark(gitDir)); err != nil {
		return err
	}
	if r.Action == undo.Prune {
		for _, d := range r.Deleted {
			if tip := s.Git.Tip(gitDir, d.Name); tip != "" && tip != d.Hash {
				return fmt.Errorf("there is a branch %s again, at another commit, so it can't come back; rename or delete it, then undo again", d.Name)
			}
		}
	}
	if r.Action == undo.Rename {
		if s.Git.Tip(gitDir, r.To) == "" {
			return fmt.Errorf("there is no branch %s any more (renamed or deleted since), so there is nothing to rename back", r.To)
		}
		if s.Git.Tip(gitDir, r.Branch) != "" {
			return fmt.Errorf("there is a branch %s again, so %s can't get that name back; rename or delete it, then undo again", r.Branch, r.To)
		}
	}
	if r.Action == undo.StashSave || r.Action == undo.StashDrop {
		return s.checkStash(gitDir, r)
	}
	if r.Action == undo.Move && s.Git.Tip(gitDir, r.Branch) != r.After {
		return fmt.Errorf("%s has changed since the last action (%s): a pull, new commits or another git command. Putting the commits back now would lose that, so GitSwitch won't; merge %s into it instead", r.Branch, r.Label, r.To)
	}
	if !r.Rewinds() {
		return nil
	}
	if r.Action == undo.Update {
		// The commits from the remote stay there; only the rebased copies
		// of your commits would go, and only while they are not pushed.
		pushed, err := s.Git.Pushed(gitDir, r.Base, r.After)
		if err != nil {
			return err
		}
		if len(pushed) > 0 {
			return fmt.Errorf("your rebased commits are pushed already (%s); undoing the update now would rewrite pushed history", short(pushed))
		}
		return nil
	}
	if r.Action == undo.Merge {
		if r.FastForward() {
			// No commit was made: the commits stay on the merged branch,
			// only this branch moves back.
			return nil
		}
		// Only the merge commit goes; the merged commits stay where they
		// came from.
		pushed, err := s.Git.Pushed(gitDir, r.After+"^@", r.After)
		if err != nil {
			return err
		}
		if len(pushed) > 0 {
			return fmt.Errorf("the merge commit (%s) is already pushed, so undoing it would rewrite pushed history; revert it instead", short(pushed))
		}
		return nil
	}
	// Moving the branch back to Before removes the commits only After has.
	pushed, err := s.Git.Pushed(gitDir, r.Before, r.After)
	if err != nil {
		return err
	}
	if len(pushed) > 0 {
		return fmt.Errorf("the commits undoing would remove are already pushed (%s), so it would rewrite pushed history; revert them instead", short(pushed))
	}
	return nil
}

func (s *Service) checkStash(gitDir string, r undo.Record) error {
	g, ok := s.Git.(undo.StashGit)
	if !ok {
		return errors.New("stashes cannot be undone here")
	}
	has := g.HasStash(gitDir, r.Before)
	if r.Action == undo.StashDrop {
		if has {
			return errors.New("that stash is in the stash list again, so there is nothing to bring back")
		}
		return nil
	}
	if !has {
		return errors.New("the stash is gone from the list (applied, popped or dropped since), so it can't be put back")
	}
	if g.Dirty(r.Worktree) {
		return errors.New("there are uncommitted changes in the working folder now. Undoing would mix the stash with them, so GitSwitch won't; commit or stash them first")
	}
	return nil
}

// Undo undoes the repo's last action and forgets it. It returns what was
// undone.
func (s *Service) Undo(gitDir string) (undo.Record, error) {
	r, err := s.Store.Load(gitDir)
	if err != nil {
		return r, err
	}
	if err := s.check(gitDir, r); err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("undo %q in %s: %s", r.Label, r.Worktree, r.Effect())) {
		return r, nil
	}
	switch r.Action {
	case undo.Switch:
		err = s.Git.Switch(r.Worktree, r.Branch)
	case undo.Move:
		return r, s.unmove(gitDir, r)
	case undo.Rename:
		err = s.Git.Rename(gitDir, r.To, r.Branch)
	case undo.StashDrop:
		err = s.Git.(undo.StashGit).StashStore(gitDir, r.Before, r.To)
	case undo.StashSave:
		err = s.Git.(undo.StashGit).StashPopBack(r.Worktree, r.Before)
	case undo.Prune:
		var gone []undo.Deleted // those made again at the same commit stay
		for _, d := range r.Deleted {
			if s.Git.Tip(gitDir, d.Name) == "" {
				gone = append(gone, d)
			}
		}
		err = s.Git.Recreate(gitDir, gone)
	case undo.Rollback, undo.Commit, undo.Amend:
		// The files already hold the commits' changes; only the branch
		// moves. After an amend, what it added shows as uncommitted.
		err = s.Git.Reset(r.Worktree, r.Before, false)
	default:
		err = s.Git.Reset(r.Worktree, r.Before, true)
	}
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not uptodate") || strings.Contains(low, "would be overwritten") {
			return r, fmt.Errorf("nothing changed: you have uncommitted changes in files the undo would change. Commit or stash them first, then undo again.\n\n%v", err)
		}
		return r, fmt.Errorf("undo failed, nothing changed: %v", err)
	}
	if err := s.Store.Clear(gitDir); err != nil {
		return r, err
	}
	return r, nil
}

// unmove undoes a move: the branch left gets its commits back and is
// checked out again, and the new branch is deleted. Both branches are at
// the same commit then, so the files don't change.
func (s *Service) unmove(gitDir string, r undo.Record) error {
	if err := s.Git.MoveBranch(gitDir, r.Branch, r.Before, r.After); err != nil {
		return fmt.Errorf("undo failed, nothing changed: %v", err)
	}
	if err := s.Git.Switch(r.Worktree, r.Branch); err != nil {
		_ = s.Git.MoveBranch(gitDir, r.Branch, r.After, r.Before)
		return fmt.Errorf("undo failed, nothing changed: %v", err)
	}
	if err := s.Store.Clear(gitDir); err != nil {
		return err
	}
	if err := s.Git.MoveBranch(gitDir, r.To, "", r.Before); err != nil {
		return fmt.Errorf("the commits are back on %s, but %s could not be deleted (%v); delete it with: git branch -D %s", r.Branch, r.To, err, r.To)
	}
	return nil
}

// Forget drops the repo's last action without undoing it.
func (s *Service) Forget(gitDir string) error {
	if _, err := s.Store.Load(gitDir); errors.Is(err, undo.ErrNothing) {
		return err
	}
	return s.Store.Clear(gitDir)
}

func short(hashes []string) string {
	var out []string
	for i, h := range hashes {
		if i == 3 {
			out = append(out, fmt.Sprintf("and %d more", len(hashes)-3))
			break
		}
		out = append(out, h[:min(7, len(h))])
	}
	return strings.Join(out, ", ")
}
