// Package domain describes rebasing the current branch onto another
// branch, tag or commit: what a rebase would do (a plan), why it is
// refused, how it ended, the pending record that lets a rebase which
// stopped on clashes be undone once it is finished, and what these use
// cases need from git (ports). It is pure. Import it as rebase.
package domain

import (
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// MaxCommits is how many commits a plan lists on each side.
const MaxCommits = 50

// CommitInfo is one commit a plan lists.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// Plan says what rebasing the current branch onto a commit will do,
// before anything changes.
type Plan struct {
	Branch string `json:"branch"` // the current branch
	Onto   string `json:"onto"`   // what it is rebased onto, as given
	Hash   string `json:"hash"`   // the commit Onto points at
	Head   string `json:"head"`   // the commit the branch is at now
	Base   string `json:"base"`   // the last commit both share
	// UpToDate: Onto is already part of the branch; nothing to do.
	UpToDate bool `json:"upToDate"`
	// FastForward: the branch has no commits of its own, so it simply
	// moves to Onto.
	FastForward bool `json:"fastForward"`
	// Replayed lists the branch's own commits that are put on top of
	// Onto, oldest first (at most MaxCommits), ReplayedCount all of them.
	Replayed      []CommitInfo `json:"replayed"`
	ReplayedCount int          `json:"replayedCount"`
	// Incoming lists the commits on Onto that the branch does not have,
	// newest first (at most MaxCommits), IncomingCount all of them.
	Incoming      []CommitInfo `json:"incoming"`
	IncomingCount int          `json:"incomingCount"`
	// BothChanged lists the files changed on both sides since Base. It is
	// an estimate of where clashes are likely: git only knows when it
	// replays the commits.
	BothChanged []string `json:"bothChanged"`
	// Blocked says why the rebase can't start now; "" when it can.
	Blocked string `json:"blocked"`
	// Summary says in plain words what will happen.
	Summary string `json:"summary"`
}

// Describe sums up a plan in plain words.
func (p Plan) Describe() string {
	switch {
	case p.UpToDate:
		return fmt.Sprintf("%s already contains %s: there is nothing to rebase.", p.Branch, p.Onto)
	case p.FastForward:
		return fmt.Sprintf("%s has no commits of its own, so it just moves forward to %s (%s come in).", p.Branch, p.Onto, commits(p.IncomingCount))
	}
	s := fmt.Sprintf("%s will be put on top of %s: your %s are replayed one by one, so they get new hashes (%s come in). ", p.Branch, p.Onto, commits(p.ReplayedCount), commits(p.IncomingCount))
	if len(p.BothChanged) > 0 {
		s += fmt.Sprintf("%d file(s) changed on both sides, so clashes are likely (an estimate): %s. If it clashes, the rebase stops so you can fix them.", len(p.BothChanged), list(p.BothChanged))
	} else {
		s += "No file changed on both sides, so no clashes are expected."
	}
	return s
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

func list(files []string) string {
	if len(files) > 5 {
		return strings.Join(files[:5], ", ") + fmt.Sprintf(" and %d more", len(files)-5)
	}
	return strings.Join(files, ", ")
}

// Outcome is how a rebase ended.
type Outcome string

const (
	Done         Outcome = "done"         // the commits were replayed
	FastForwards Outcome = "fast-forward" // the branch just moved
	UpToDate     Outcome = "up-to-date"   // nothing to do, nothing changed
	Stopped      Outcome = "conflicts"    // git stopped with files to fix
)

// Rebased is the result of a rebase.
type Rebased struct {
	Branch  string  `json:"branch"`
	Onto    string  `json:"onto"`
	Outcome Outcome `json:"outcome"`
	Hash    string  `json:"hash"` // HEAD afterwards
	Short   string  `json:"short"`
	// Replayed is how many of the branch's commits were put on top.
	Replayed int `json:"replayed"`
	// Conflicts lists the files to fix when the rebase stopped.
	Conflicts []string `json:"conflicts"`
	// Message says what happened and what to do next.
	Message string `json:"message"`
}

// Check refuses to rebase when the repo is not ready: git is in the
// middle of something, there are no commits, HEAD is detached, or there
// are uncommitted changes to tracked files (a rebase would not carry
// them, and undoing it could lose them). New files that are not tracked
// are fine.
func Check(h refs.Head) error {
	c := h.Changes
	switch {
	case h.Busy != "":
		return remote.Busy(h.Busy)
	case h.Hash == "":
		return fmt.Errorf("this repo has no commits yet, so there is nothing to rebase; make the first commit")
	case h.Branch == "":
		return fmt.Errorf("HEAD is detached (not on a branch); switch to the branch to rebase first")
	case c.Conflicts > 0:
		return fmt.Errorf("%d file(s) still have conflicts; fix them first", c.Conflicts)
	case c.Staged+c.Changed > 0:
		return fmt.Errorf("you have uncommitted changes (%d staged, %d changed); commit, park or discard them first, then rebase", c.Staged, c.Changed)
	}
	return nil
}

// CheckName refuses a name that git would read as an option.
func CheckName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("name what to rebase onto, e.g. main or origin/main")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("%q is not a branch, tag or commit name", name)
	}
	return nil
}

// PushedError explains why pushed commits stop a rebase.
func PushedError(branch string, pushed []string) error {
	return fmt.Errorf("%d of the commits on %s (%s) are already pushed, and rebasing would rewrite pushed commits; merge instead. GitSwitch never offers a rebase of pushed commits, since it would need a force-push", len(pushed), branch, short(pushed))
}

// MergesError explains why merge commits stop a rebase.
func MergesError(branch string, n int) error {
	return fmt.Errorf("%s has %d merge commit(s) of its own, which a rebase would flatten; merge instead", branch, n)
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

// Label is what the undo list calls a rebase.
func Label(r Rebased) string {
	if r.Outcome == FastForwards {
		return fmt.Sprintf("Fast-forwarded %s to %s", r.Branch, r.Onto)
	}
	return fmt.Sprintf("Rebased %s onto %s", r.Branch, r.Onto)
}

// Pending is a rebase that started and is not recorded for undo yet: it
// is kept from the start so a rebase that stops on clashes and is
// finished later (gitswitch continue, in any front end, or git itself)
// can still be undone.
type Pending struct {
	Branch   string `json:"branch"`
	Orig     string `json:"orig"`     // the branch's commit before the rebase
	Onto     string `json:"onto"`     // the commit it is rebased onto
	OntoName string `json:"ontoName"` // what the user called it
	Worktree string `json:"worktree"`
	Count    int    `json:"count"` // how many commits are replayed
	Time     int64  `json:"time"`
}

// Status says whether a rebase is waiting to be finished.
type Status struct {
	// Pending: a rebase GitSwitch started is not finished yet.
	Pending bool   `json:"pending"`
	Branch  string `json:"branch"`
	Onto    string `json:"onto"`
	// Recorded: asking finished a rebase and recorded it for undo.
	Recorded bool `json:"recorded"`
}

// Git is what the rebase use cases need from git.
type Git interface {
	Head(gitDir string) (refs.Head, error)
	// Resolve turns a branch, tag or commit name into a commit hash.
	Resolve(gitDir, rev string) (string, error)
	MergeBase(gitDir, a, b string) string
	// Commits lists the commits reachable from tip and not from not, at
	// most limit (the oldest ones when oldestFirst, else the newest), in
	// that order, with how many there are in all.
	Commits(gitDir, tip, not string, limit int, oldestFirst bool) ([]CommitInfo, int, error)
	// Merges counts the merge commits reachable from to and not from from.
	Merges(gitDir, from, to string) int
	// Pushed lists the commits reachable from to and not from from that
	// are on a remote branch.
	Pushed(gitDir, from, to string) ([]string, error)
	// Changed lists the files that differ between two commits.
	Changed(gitDir, from, to string) ([]string, error)
	// Rebase runs git rebase onto in worktree: never an editor, never an
	// autostash.
	Rebase(worktree, onto string) error
	// Abort runs git rebase --abort.
	Abort(worktree string) error
	// Op is the operation in progress ("" for none).
	Op(gitDir string) string
	// Conflicted lists the files in conflict.
	Conflicted(gitDir string) ([]string, error)
	// Stopped reads the rebase git has stopped in the middle of: the
	// commit the branch was at and the commit it is rebased onto.
	Stopped(gitDir string) (orig, onto string)
	// Count counts the commits reachable from to and not from from.
	Count(gitDir, from, to string) int
}

// Store keeps the pending rebase of a repo, and the commit it started
// from reachable.
type Store interface {
	Save(gitDir string, p Pending) error
	// Load reads it; ok is false when there is none.
	Load(gitDir string) (p Pending, ok bool)
	Clear(gitDir string) error
}

// Recorder remembers a finished rebase so it can be undone.
type Recorder interface {
	Before(gitDir string) undo.Mark
	// Rebased remembers a rebase of the branch in before onto the commit
	// onto, which moved it.
	Rebased(gitDir string, before undo.Mark, onto, label string)
}
