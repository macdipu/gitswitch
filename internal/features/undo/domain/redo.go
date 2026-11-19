package domain

import (
	"errors"
	"fmt"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
)

// Redo is the inverse of Undo. An Undo keeps the Record it took back (the
// redo record, same fields); Redo does the change again, obeys the same
// kind of refusal rules (the repo must be exactly where the Undo left it,
// nothing pushed would be rewritten, git's own refusal when uncommitted
// changes would be lost) and then puts the Record back as the last action,
// so it can be undone again.
//
// Covered: commit, amend, rollback, revert, drop, cherry-pick, fix-author,
// update (rebase), merge, switch, move, rename, prune (branch deletes) and
// stash drop. Not covered: a stash save (see RedoUnsupported).

// ErrNothingToRedo means there is no undone action to do again.
var ErrNothingToRedo = errors.New("nothing to redo in this repo")

// RedoStore keeps the action the last Undo took back. The infrastructure
// Git offers it next to Store; the undo service looks for it at run time,
// so other Stores need nothing. A new action (Store.Save or Clear) forgets
// the redo record.
type RedoStore interface {
	SaveRedo(gitDir string, r Record) error
	// LoadRedo reads it; ErrNothingToRedo when there is none.
	LoadRedo(gitDir string) (Record, error)
	ClearRedo(gitDir string) error
}

// RedoGit is what redoing needs besides Git; the infrastructure Git
// offers it, and the undo service checks at run time.
type RedoGit interface {
	// HasCommit reports whether the repo still has the commit.
	HasCommit(gitDir, hash string) bool
	// DeleteBranches deletes branches, each only while it still points at
	// its Hash, and forgets their upstream settings.
	DeleteBranches(gitDir string, branches []Deleted) error
	// DropStash removes the stash entry made of the commit.
	DropStash(worktree, hash string) error
}

// Mixed reports whether undoing (and so redoing) only moves the branch and
// the staging area and leaves the files alone (a commit, an amend, a
// rollback), against moving the files with git reset --keep.
func (r Record) Mixed() bool {
	return r.Action == Commit || r.Action == Amend || r.Action == Rollback
}

// RedoUnsupported says why this action cannot be done again; "" when it can.
func (r Record) RedoUnsupported() string {
	switch r.Action {
	case Revert, Drop, Rollback, CherryPick, FixAuthor, Switch, Move, Commit, Amend, Prune, Update, Rename, Merge, StashDrop, Rebase, Rewrite:
		return ""
	case StashSave:
		return "a stash is saved with options (which files, whether staged changes stay) that are not remembered, so it cannot be repeated exactly. Stash again with `gitswitch stash save`"
	}
	return "this kind of change cannot be done again; make it again yourself"
}

// RedoEffect says in plain words what redoing does.
func (r Record) RedoEffect() string {
	switch r.Action {
	case Switch:
		return "Switches to " + r.To + " again."
	case Rename:
		return "Renames " + r.Branch + " to " + r.To + " again."
	case StashDrop:
		return "Drops the stash again."
	case Prune:
		names := make([]string, len(r.Deleted))
		for i, d := range r.Deleted {
			names[i] = d.Name
		}
		return "Deletes the branches again: " + joinNames(names) + "."
	case Move:
		return "Moves the commits to " + r.To + " again, switches to it and takes them off " + r.Branch + ". Your uncommitted changes stay as they are."
	case Commit:
		return "Puts the commit back on " + r.Branch + ". Your files are not changed."
	case Amend:
		return "Puts the amended commit back on " + r.Branch + ". Your files are not changed."
	case Rollback:
		return "Rolls " + r.Branch + " back again, to the commit before. Your files are not changed."
	case Merge:
		if r.FastForward() {
			return "Moves " + r.Branch + " forward to " + r.To + " again."
		}
		return "Puts the merge of " + r.To + " back on " + r.Branch + "."
	case Drop:
		return "Removes the commit from " + r.Branch + " again."
	case Revert, CherryPick:
		return "Puts the new commit(s) back on " + r.Branch + "."
	case Update, FixAuthor:
		return "Puts " + r.Branch + " back as it was after the change."
	case Rebase:
		return "Puts the rebased " + r.Branch + " back."
	case Rewrite:
		return "Puts the rewritten " + r.Branch + " back."
	}
	return "Does the change again."
}

func joinNames(n []string) string {
	s := ""
	for i, x := range n {
		if i > 0 {
			s += ", "
		}
		s += x
	}
	return s
}

// CheckRedo refuses to redo when the repo is no longer where the Undo left
// it: redoing then would lose work done since, or redo the wrong thing.
func (r Record) CheckRedo(now Mark) error {
	if why := r.RedoUnsupported(); why != "" {
		return errors.New("it can't be redone: " + why)
	}
	if now.Busy != "" {
		return remote.Busy(now.Busy)
	}
	if now.Worktree != r.Worktree {
		return fmt.Errorf("the undone action (%s) was made in another working folder, %s; redo it there", r.Label, r.Worktree)
	}
	switch r.Action {
	case Prune, Rename, StashDrop:
		return nil // any branch may be checked out
	case Switch:
		if now.Branch != r.Branch {
			return fmt.Errorf("you are not on %s any more, so there is no switch to redo; switch to %s yourself", r.Branch, r.To)
		}
		return nil
	}
	if now.Branch != r.Branch {
		return fmt.Errorf("the undone action (%s) was on %s, but you are on %s now; switch to %s to redo it", r.Label, r.Branch, orDetached(now.Branch), r.Branch)
	}
	if now.Head != r.Before {
		return fmt.Errorf("%s has changed since the undo: new commits, a pull or another git command. Redoing now would lose that work, so GitSwitch won't", r.Branch)
	}
	return nil
}
