package application

import (
	"errors"
	"fmt"
	"strings"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// RedoStatus is the action the last Undo took back and whether it can be
// done again now.
type RedoStatus = Status

// NextRedo reads the repo's undone action; undo.ErrNothingToRedo when
// there is none. Effect says what redoing does; Blocked why it cannot now.
func (s *Service) NextRedo(gitDir string) (RedoStatus, error) {
	rs, ok := s.Store.(undo.RedoStore)
	if !ok {
		return RedoStatus{}, undo.ErrNothingToRedo
	}
	r, err := rs.LoadRedo(gitDir)
	if err != nil {
		return RedoStatus{}, err
	}
	st := RedoStatus{Record: r, Effect: r.RedoEffect()}
	if err := s.checkRedo(gitDir, r); err != nil {
		st.Blocked = strings.TrimPrefix(err.Error(), "it can't be redone: ")
	}
	return st, nil
}

func (s *Service) checkRedo(gitDir string, r undo.Record) error {
	if err := r.CheckRedo(s.Git.Mark(gitDir)); err != nil {
		return err
	}
	rg, ok := s.Git.(undo.RedoGit)
	if !ok {
		return errors.New("it can't be redone here")
	}
	switch r.Action {
	case undo.Switch:
		if s.Git.Tip(gitDir, r.To) == "" {
			return fmt.Errorf("there is no branch %s any more, so there is nothing to switch to", r.To)
		}
		return nil
	case undo.Rename:
		if s.Git.Tip(gitDir, r.Branch) == "" {
			return fmt.Errorf("there is no branch %s any more (renamed or deleted since), so there is nothing to rename", r.Branch)
		}
		if s.Git.Tip(gitDir, r.To) != "" {
			return fmt.Errorf("there is a branch %s again, so %s can't get that name; rename or delete it, then redo", r.To, r.Branch)
		}
		return nil
	case undo.Prune:
		now := s.Git.Mark(gitDir)
		for _, d := range r.Deleted {
			if tip := s.Git.Tip(gitDir, d.Name); tip != d.Hash {
				return fmt.Errorf("the branch %s is not where the undo left it (moved, renamed or deleted since), so it is not deleted again", d.Name)
			}
			if now.Branch == d.Name {
				return fmt.Errorf("you are on %s, which cannot be deleted; switch to another branch first", d.Name)
			}
		}
		return nil
	case undo.StashDrop:
		if sg, ok := s.Git.(undo.StashGit); !ok || !sg.HasStash(gitDir, r.Before) {
			return errors.New("the stash is gone from the list (applied, popped or dropped since), so there is nothing to drop")
		}
		return nil
	}
	if !rg.HasCommit(gitDir, r.After) {
		return errors.New("the commit to go back to is no longer in the repo (git cleaned it up)")
	}
	if r.Action == undo.Move {
		if s.Git.Tip(gitDir, r.To) != "" {
			return fmt.Errorf("there is a branch %s again, so the commits can't move to it; rename or delete it, then redo", r.To)
		}
		return nil
	}
	// Commits the redo would take off the branch must not be pushed
	// (nothing is taken off by a commit or a merge).
	pushed, err := s.Git.Pushed(gitDir, r.After, r.Before)
	if err != nil {
		return err
	}
	if len(pushed) > 0 {
		return fmt.Errorf("the commits redoing would remove are already pushed (%s), so it would rewrite pushed history", short(pushed))
	}
	return nil
}

// Redo does the action the last Undo took back again, and makes it the
// repo's last action again (so it can be undone once more). It returns
// what was redone.
func (s *Service) Redo(gitDir string) (undo.Record, error) {
	rs, ok := s.Store.(undo.RedoStore)
	if !ok {
		return undo.Record{}, undo.ErrNothingToRedo
	}
	r, err := rs.LoadRedo(gitDir)
	if err != nil {
		return r, err
	}
	if err := s.checkRedo(gitDir, r); err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("redo %q in %s: %s", r.Label, r.Worktree, r.RedoEffect())) {
		return r, nil
	}
	switch r.Action {
	case undo.Switch:
		err = s.Git.Switch(r.Worktree, r.To)
	case undo.Rename:
		err = s.Git.Rename(gitDir, r.Branch, r.To)
	case undo.StashDrop:
		err = s.Git.(undo.RedoGit).DropStash(r.Worktree, r.Before)
	case undo.Prune:
		err = s.Git.(undo.RedoGit).DeleteBranches(gitDir, r.Deleted)
	case undo.Move:
		err = s.remove(gitDir, r)
	default:
		// Mixed: only the branch and the staging area move (as the undo
		// did); otherwise git reset --keep, which refuses when uncommitted
		// changes in the files it would change would be lost.
		err = s.Git.Reset(r.Worktree, r.After, !r.Mixed())
	}
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not uptodate") || strings.Contains(low, "would be overwritten") {
			return r, fmt.Errorf("nothing changed: you have uncommitted changes in files the redo would change. Commit or stash them first, then redo again.\n\n%v", err)
		}
		return r, fmt.Errorf("redo failed, nothing changed: %v", err)
	}
	// The action is done again: it is the last action, undoable once more.
	_ = rs.ClearRedo(gitDir)
	if err := s.Store.Save(gitDir, r); err != nil {
		return r, fmt.Errorf("redone, but it can't be undone again: %v", err)
	}
	return r, nil
}

// remove does a move again: the commits go to a new branch r.To, which is
// checked out, and the branch they were on goes back to r.After.
func (s *Service) remove(gitDir string, r undo.Record) error {
	if err := s.Git.MoveBranch(gitDir, r.To, r.Before, ""); err != nil {
		return err
	}
	if err := s.Git.MoveBranch(gitDir, r.Branch, r.After, r.Before); err != nil {
		_ = s.Git.MoveBranch(gitDir, r.To, "", r.Before)
		return err
	}
	if err := s.Git.Switch(r.Worktree, r.To); err != nil {
		_ = s.Git.MoveBranch(gitDir, r.Branch, r.Before, r.After)
		_ = s.Git.MoveBranch(gitDir, r.To, "", r.Before)
		return err
	}
	return nil
}
