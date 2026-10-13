// Package domain describes the last action GitSwitch made in a repo and
// when it can be undone, and declares what undoing needs from git (ports).
// It is pure. Import it as undo.
package domain

import (
	"errors"
	"fmt"
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
)

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
	To       string `json:"to"`       // for a switch: the branch switched to
	Before   string `json:"before"`   // HEAD before
	After    string `json:"after"`    // HEAD after
	Time     int64  `json:"time"`     // when, in Unix seconds
}

// New records an action from where the repo was before and after it. The
// second result is false when there is nothing to undo: HEAD did not
// move, or (except for a switch) the branch is not the same after.
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
	return r, after.Branch == before.Branch && before.Head != "" && after.Head != before.Head
}

// Effect says in plain words what undoing does.
func (r Record) Effect() string {
	switch r.Action {
	case Switch:
		return "Switches back to " + r.Branch + "."
	case Rollback:
		return "Puts the rolled-back commits back on " + r.Branch + ". Your files are not changed."
	case Revert, CherryPick:
		return "Removes the new commit(s) from " + r.Branch + ", as if they had never been made."
	default:
		return "Puts " + r.Branch + " back exactly as it was before."
	}
}

// Rewinds reports whether undoing moves the branch back to Before (every
// action but a switch).
func (r Record) Rewinds() bool { return r.Action != Switch }

// ErrNothing means there is no action to undo in the repo.
var ErrNothing = errors.New("nothing to undo in this repo")

// Check refuses to undo when the repo is no longer where the action left
// it: undoing then would lose work done since, or undo the wrong thing.
func (r Record) Check(now Mark) error {
	if now.Busy != "" {
		return fmt.Errorf("git is in the middle of a %s in this repo; finish or abort it first", now.Busy)
	}
	if now.Worktree != r.Worktree {
		return fmt.Errorf("the last action (%s) was made in another working folder, %s; undo it there", r.Label, r.Worktree)
	}
	if r.Action == Switch {
		if now.Branch != r.To {
			return fmt.Errorf("you are no longer on %s, so there is no switch to undo; switch to %s yourself", r.To, r.Branch)
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
}

// Recorder remembers actions so they can be undone. Use cases call Before
// first, and Done once the action succeeded.
type Recorder interface {
	Before(gitDir string) Mark
	Done(gitDir string, before Mark, a Action, label string)
}
