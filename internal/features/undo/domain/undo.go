// Package domain describes the last action GitSwitch made in a repo and
// when it can be undone, and declares what undoing needs from git (ports).
// It is pure. Import it as undo.
package domain

import (
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
)

// Action is a change GitSwitch can undo.
type Action string

const (
	Revert     Action = "revert"
	Drop       Action = "drop"
	Rollback   Action = "rollback"
	CherryPick Action = "cherry-pick"
	FixAuthor  Action = "fix-author"
	Switch     Action = "switch"
	// Move: the commits not pushed were moved to a new branch (Record.To),
	// and the branch left (Record.Branch) moved back from Before to After.
	Move Action = "move"
	// Commit: a commit GitSwitch made.
	Commit Action = "commit"
	// Prune: merged branches were deleted (Record.Deleted).
	Prune Action = "prune"
)

// Deleted is a branch an action deleted: enough to bring it back.
type Deleted struct {
	Name   string `json:"name"`
	Hash   string `json:"hash"`   // the commit it pointed at
	Remote string `json:"remote"` // the remote it pulled from; "" for none
	Merge  string `json:"merge"`  // the remote branch it pulled from, e.g. refs/heads/x
}

// Mark is where a repo is: its working folder, branch and HEAD commit.
type Mark struct {
	Worktree string
	Branch   string // "" when HEAD is detached
	Head     string // "" in a repo without commits
	Busy     string // an operation git is in the middle of, e.g. "rebase"
}

// Record is the last action, with where the repo was before and after it.
type Record struct {
	Action   Action `json:"action"`
	Label    string `json:"label"`    // what was done, e.g. "Dropped 1a2b3c4 Fix login"
	Worktree string `json:"worktree"` // the working folder it was done in
	Branch   string `json:"branch"`   // the branch it changed; for a switch, the branch left
	To       string `json:"to"`       // for a switch or a move: the branch switched to
	Before   string `json:"before"`   // HEAD before
	After    string `json:"after"`    // HEAD after
	Time     int64  `json:"time"`     // when, in Unix seconds
	// Deleted lists the branches a prune deleted.
	Deleted []Deleted `json:"deleted"`
}

// New records an action from where the repo was before and after it. The
// second result is false when there is nothing to undo: HEAD did not
// move, or (except for a switch or a move) the branch is not the same
// after. A move's After is where the branch left is now, which only the
// caller knows: New leaves it "".
func New(a Action, label string, before, after Mark, now int64) (Record, bool) {
	r := Record{Action: a, Label: label, Worktree: before.Worktree, Branch: before.Branch,
		Before: before.Head, After: after.Head, Time: now}
	if before.Branch == "" || after.Branch == "" || before.Worktree != after.Worktree {
		return r, false
	}
	if a == Switch {
		r.To = after.Branch
		return r, r.To != r.Branch
	}
	if a == Move {
		r.To, r.After = after.Branch, ""
		return r, r.To != r.Branch && before.Head != "" && after.Head == before.Head
	}
	return r, after.Branch == before.Branch && before.Head != "" && after.Head != before.Head
}

// Effect says in plain words what undoing does.
func (r Record) Effect() string {
	switch r.Action {
	case Switch:
		return "Switches back to " + r.Branch + "."
	case Prune:
		names := make([]string, len(r.Deleted))
		for i, d := range r.Deleted {
			names[i] = d.Name
		}
		return "Brings back the deleted branches: " + strings.Join(names, ", ") + "."
	case Move:
		return "Puts the commits back on " + r.Branch + ", switches back to it and deletes " + r.To + ". Your uncommitted changes stay as they are."
	case Rollback:
		return "Puts the rolled-back commits back on " + r.Branch + ". Your files are not changed."
	case Commit:
		return "Takes the commit off " + r.Branch + ". Its changes stay in your files, uncommitted."
	case Revert, CherryPick:
		return "Removes the new commit(s) from " + r.Branch + ", as if they had never been made."
	default:
		return "Puts " + r.Branch + " back exactly as it was before."
	}
}

// Rewinds reports whether undoing moves the branch back to Before, taking
// commits off it (every action but a switch, a move or a prune).
func (r Record) Rewinds() bool { return r.Action != Switch && r.Action != Move && r.Action != Prune }

// ErrNothing means there is no action to undo in the repo.
var ErrNothing = errors.New("nothing to undo in this repo")

// Check refuses to undo when the repo is no longer where the action left
// it: undoing then would lose work done since, or undo the wrong thing.
func (r Record) Check(now Mark) error {
	if now.Busy != "" {
		return remote.Busy(now.Busy)
	}
	if now.Worktree != r.Worktree {
		return fmt.Errorf("the last action (%s) was made in another working folder, %s; undo it there", r.Label, r.Worktree)
	}
	if r.Action == Prune {
		return nil // any branch may be checked out: the deleted ones only come back
	}
	if r.Action == Switch {
		if now.Branch != r.To {
			return fmt.Errorf("you are no longer on %s, so there is no switch to undo; switch to %s yourself", r.To, r.Branch)
		}
		return nil
	}
	if r.Action == Move {
		if now.Branch != r.To {
			return fmt.Errorf("the last action (%s) left you on %s, but you are on %s now; switch to %s to undo it", r.Label, r.To, orDetached(now.Branch), r.To)
		}
		if now.Head != r.Before {
			return fmt.Errorf("%s has changed since the last action (%s): new commits or another git command. Undoing it now would delete that work, so GitSwitch won't", r.To, r.Label)
		}
		return nil
	}
	if now.Branch != r.Branch {
		return fmt.Errorf("the last action (%s) was on %s, but you are on %s now; switch to %s to undo it", r.Label, r.Branch, orDetached(now.Branch), r.Branch)
	}
	if now.Head != r.After {
		return fmt.Errorf("%s has changed since the last action (%s): new commits, a pull or another git command. Undoing it now would lose that work, so GitSwitch won't. To go back anyway, find the commit in `git reflog`", r.Branch, r.Label)
	}
	return nil
}

func orDetached(b string) string {
	if b == "" {
		return "a detached HEAD"
	}
	return b
}

// Store keeps the last action of each repo.
type Store interface {
	// Save replaces the repo's last action.
	Save(gitDir string, r Record) error
	// Load reads it; ErrNothing when there is none.
	Load(gitDir string) (Record, error)
	// Clear forgets it.
	Clear(gitDir string) error
}

// Git reads and moves the repo for an undo.
type Git interface {
	// Mark reads where the repo is.
	Mark(gitDir string) Mark
	// Pushed lists the commits reachable from to but not from from that
	// are on a remote branch.
	Pushed(gitDir, from, to string) ([]string, error)
	// Reset moves the current branch to hash. keep: uncommitted changes
	// are kept and the files are updated, refused when the two would
	// clash (git reset --keep); otherwise only the branch and the staging
	// area move and the files are not touched (git reset --mixed).
	Reset(worktree, hash string, keep bool) error
	// Switch checks out a local branch.
	Switch(worktree, branch string) error
	// Tip is the commit a local branch points at; "" when there is none.
	Tip(gitDir, branch string) string
	// MoveBranch points a branch that is not checked out at the commit to
	// ("" deletes it), only while it is still at was.
	MoveBranch(gitDir, branch, to, was string) error
	// Recreate brings back deleted branches, with the remote branch each
	// pulled from; refused, and nothing made, when one exists again.
	Recreate(gitDir string, branches []Deleted) error
}

// Recorder remembers actions so they can be undone. Use cases call Before
// first, and Done once the action succeeded.
type Recorder interface {
	Before(gitDir string) Mark
	Done(gitDir string, before Mark, a Action, label string)
	// Pruned remembers branches that were deleted.
	Pruned(gitDir string, before Mark, branches []Deleted, label string)
}
