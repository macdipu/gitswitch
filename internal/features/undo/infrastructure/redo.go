package infrastructure

import (
	"errors"

	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// redoRef points at the commit the undone action ended at, so git gc keeps
// it for as long as the action can be redone; redoSection holds the record.
const (
	redoRef     = "refs/gitswitch/redo"
	redoSection = "gitswitch.redo"
)

// SaveRedo keeps the action an Undo took back.
func (g *Git) SaveRedo(gitDir string, r undo.Record) error {
	_ = g.ClearRedo(gitDir)
	if err := g.save(gitDir, r, redoSection, redoRef, r.After); err != nil {
		_ = g.ClearRedo(gitDir)
		return err
	}
	return nil
}

// LoadRedo reads it; undo.ErrNothingToRedo when there is none.
func (g *Git) LoadRedo(gitDir string) (undo.Record, error) {
	r, err := g.load(gitDir, redoSection)
	if errors.Is(err, undo.ErrNothing) {
		return r, undo.ErrNothingToRedo
	}
	return r, err
}

// ClearRedo forgets it.
func (g *Git) ClearRedo(gitDir string) error {
	_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", redoSection)
	_, _ = g.Git.Run("--git-dir="+gitDir, "update-ref", "-d", redoRef)
	return nil
}

// HasCommit reports whether the repo has the commit.
func (g *Git) HasCommit(gitDir, hash string) bool {
	_, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", hash+"^{commit}")
	return err == nil
}

// DeleteBranches deletes each branch only while it is still at its commit,
// and forgets its upstream settings. Nothing is deleted when one moved.
func (g *Git) DeleteBranches(gitDir string, branches []undo.Deleted) error {
	for _, d := range branches {
		if g.Tip(gitDir, d.Name) != d.Hash {
			return errors.New("the branch " + d.Name + " has moved since the undo, so it is not deleted")
		}
	}
	for _, d := range branches {
		if err := g.MoveBranch(gitDir, d.Name, "", d.Hash); err != nil {
			return err
		}
		_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", "branch."+d.Name)
	}
	return nil
}

// DropStash removes the stash entry made of the commit.
func (g *Git) DropStash(worktree, hash string) error {
	return (&refsinfra.Git{Git: g.Git}).DropStash(worktree, hash)
}
