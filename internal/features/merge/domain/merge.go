// Package domain describes merging a branch into the current one, an
// operation git stopped in the middle of (merge, rebase, cherry-pick,
// revert), and a conflicted file as three versions (base, ours, theirs)
// plus the working file. It declares what those use cases need from git
// (ports). It is pure. Import it as merge.
package domain

import (
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// Mode is how a branch is merged.
type Mode string

const (
	// Auto fast-forwards when possible, else makes a merge commit (git's
	// default).
	Auto Mode = ""
	// FastForward only fast-forwards; refused when the branches have gone
	// apart.
	FastForward Mode = "ff"
	// Commit always makes a merge commit (--no-ff).
	Commit Mode = "merge"
	// Squash stages the branch's changes as one change, without
	// committing it.
	Squash Mode = "squash"
)

// ParseMode reads a mode as the API and the command line name it.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto", "default":
		return Auto, nil
	case "ff", "ff-only", "fast-forward":
		return FastForward, nil
	case "merge", "no-ff", "commit":
		return Commit, nil
	case "squash":
		return Squash, nil
	}
	return Auto, fmt.Errorf("unknown merge mode %q: use auto, ff, merge or squash", s)
}

// MaxCommits is how many commits a plan lists on each side.
const MaxCommits = 50

// CommitInfo is one commit a plan lists.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

// Plan says what merging a branch into the current one will do, before
// anything changes.
type Plan struct {
	Into string `json:"into"` // the current branch
	From string `json:"from"` // the branch (or commit) merged in, as given
	Hash string `json:"hash"` // the commit From points at
	Base string `json:"base"` // the last commit both share
	// UpToDate: everything in From is here already; nothing to merge.
	UpToDate bool `json:"upToDate"`
	// FastForward: nothing is only here, so the branch can simply move to
	// From without a merge commit.
	FastForward bool `json:"fastForward"`
	// Incoming lists the commits coming in (at most MaxCommits, newest
	// first), IncomingCount all of them.
	Incoming      []CommitInfo `json:"incoming"`
	IncomingCount int          `json:"incomingCount"`
	// Local lists the commits only on the current branch.
	Local      []CommitInfo `json:"local"`
	LocalCount int          `json:"localCount"`
	// BothChanged lists the files changed on both sides since Base.
	BothChanged []string `json:"bothChanged"`
	// Conflicts lists the files git predicts will clash; Predicted says
	// whether git could predict them (git 2.38 or newer). Without a
	// prediction, BothChanged is where clashes are likely.
	Conflicts []string `json:"conflicts"`
	Predicted bool     `json:"predicted"`
	// Blocked says why the merge can't start now; "" when it can.
	Blocked string `json:"blocked"`
	// Summary says in plain words what will happen.
	Summary string `json:"summary"`
}

// Describe sums up a plan in plain words.
func (p Plan) Describe() string {
	switch {
	case p.UpToDate:
		return fmt.Sprintf("%s is up to date with %s: there is nothing to merge.", p.Into, p.From)
	case p.FastForward:
		return fmt.Sprintf("%s can fast-forward to %s: %s come in, no merge commit is needed.", p.Into, p.From, commits(p.IncomingCount))
	}
	s := fmt.Sprintf("%s and %s have gone apart: %s come in, %s only on %s. ", p.Into, p.From, commits(p.IncomingCount), commits(p.LocalCount), p.Into)
	switch {
	case p.Predicted && len(p.Conflicts) > 0:
		s += fmt.Sprintf("%d file(s) will clash: %s. The merge will stop so you can fix them.", len(p.Conflicts), list(p.Conflicts))
	case p.Predicted:
		s += "No clashes: git can merge them by itself."
	case len(p.BothChanged) > 0:
		s += fmt.Sprintf("%d file(s) changed on both sides, so clashes are likely: %s.", len(p.BothChanged), list(p.BothChanged))
	default:
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

// Outcome is how a merge ended.
type Outcome string

const (
	UpToDate     Outcome = "up-to-date"   // nothing to merge, nothing changed
	FastForwards Outcome = "fast-forward" // the branch moved to the merged one
	MergeCommit  Outcome = "merge-commit" // a merge commit was made
	Squashed     Outcome = "squashed"     // the changes are staged, not committed
	Stopped      Outcome = "conflicts"    // git stopped with files to fix
)

// Merged is the result of a merge.
type Merged struct {
	Into    string  `json:"into"`
	From    string  `json:"from"`
	Mode    Mode    `json:"mode"`
	Outcome Outcome `json:"outcome"`
	Hash    string  `json:"hash"` // HEAD afterwards
	Short   string  `json:"short"`
	// Conflicts lists the files to fix when the merge stopped.
	Conflicts []string `json:"conflicts"`
	// Message says what happened and what to do next.
	Message string `json:"message"`
}

// Check refuses to merge when the repo is not ready: git is in the middle
// of something, HEAD is detached, there are no commits, or there are
// uncommitted changes to tracked files (a merge could mix them with its
// own, and undoing it could lose them). New files that are not tracked
// are fine: git refuses by itself when the merge would overwrite one.
func Check(h refs.Head) error {
	c := h.Changes
	switch {
	case h.Busy != "":
		return remote.Busy(h.Busy)
	case h.Hash == "":
		return fmt.Errorf("this repo has no commits yet, so there is nothing to merge into; make the first commit")
	case h.Branch == "":
		return fmt.Errorf("HEAD is detached (not on a branch); switch to the branch to merge into first")
	case c.Conflicts > 0:
		return fmt.Errorf("%d file(s) still have conflicts; fix them first", c.Conflicts)
	case c.Staged+c.Changed > 0:
		return fmt.Errorf("you have uncommitted changes (%d staged, %d changed); commit, park or discard them first, then merge", c.Staged, c.Changed)
	}
	return nil
}

// CheckName refuses a branch name that git would read as an option.
func CheckName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("name the branch to merge, e.g. main or origin/main")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("%q is not a branch name", name)
	}
	return nil
}

// Label is what the undo list calls a merge.
func Label(m Merged) string {
	if m.Outcome == FastForwards {
		return fmt.Sprintf("Fast-forwarded %s to %s", m.Into, m.From)
	}
	return fmt.Sprintf("Merged %s into %s", m.From, m.Into)
}

// MergedName reads the branch a merge brings in from git's merge message
// ("Merge branch 'x'", "Merge remote-tracking branch 'origin/x'", "Merge
// tag 'v1'", "Merge commit 'abc'"); "" when it is not one of those.
func MergedName(msg string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	for _, p := range []string{"Merge branch '", "Merge remote-tracking branch '", "Merge tag '", "Merge commit '"} {
		if rest, ok := strings.CutPrefix(first, p); ok {
			if name, _, ok := strings.Cut(rest, "'"); ok {
				return name
			}
		}
	}
	return ""
}

// Operations git can stop in the middle of, as refs.Head.Busy names them.
const (
	OpMerge      = "merge"
	OpRebase     = "rebase"
	OpCherryPick = "cherry-pick"
	OpRevert     = "revert"
	OpBisect     = "bisect"
)

// Actions on a stopped operation.
const (
	Continue = "continue"
	Skip     = "skip"
	Abort    = "abort"
)

// Operation is what git is in the middle of, with the files still in
// conflict and what can be done.
type Operation struct {
	Kind   string `json:"kind"`   // merge, rebase, cherry-pick, revert, bisect, smart switch; "" for none
	Branch string `json:"branch"` // the branch it happens on; "" when detached (a rebase)
	// Conflicts lists the files still in conflict.
	Conflicts []string `json:"conflicts"`
	// Actions lists what can be done: continue, skip, abort.
	Actions []string `json:"actions"`
	// CanContinue: every conflict is fixed and staged; Why says otherwise.
	CanContinue bool   `json:"canContinue"`
	Why         string `json:"why"`
	// Ours and Theirs say in plain words what each side of a conflict is.
	Ours   string `json:"ours"`
	Theirs string `json:"theirs"`
	// Message says in plain words what is going on and what to do.
	Message string `json:"message"`
}

// ActionsFor lists the actions git offers for an operation: merges
// continue or abort; rebases, cherry-picks and reverts can also skip the
// commit they stopped at. Bisects and GitSwitch's smart switch are
// finished in their own way (none here).
func ActionsFor(kind string) []string {
	switch kind {
	case OpMerge:
		return []string{Continue, Abort}
	case OpRebase, OpCherryPick, OpRevert:
		return []string{Continue, Skip, Abort}
	}
	return nil
}

// Allowed reports whether action is one of the operation's actions.
func (o Operation) Allowed(action string) bool {
	for _, a := range o.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// Sides says what "ours" and "theirs" are during an operation. In a
// rebase they are swapped from what people expect: ours is the branch
// being rebased onto, theirs is your commit being replayed.
func Sides(kind, branch, merging string) (ours, theirs string) {
	here := branch
	if here == "" {
		here = "the current commit"
	}
	switch kind {
	case OpMerge:
		if merging == "" {
			merging = "the branch being merged"
		}
		return here + " (this branch)", merging + " (being merged in)"
	case OpRebase:
		return "the branch you are rebasing onto", "your commit being replayed"
	case OpCherryPick:
		return here + " (this branch)", "the commit being copied"
	case OpRevert:
		return here + " (this branch)", "the undo of the commit being reverted"
	}
	return here, "the other side"
}

// Describe explains the operation in plain words.
func Describe(kind string, conflicts []string) string {
	switch kind {
	case "":
		return "Nothing is in progress."
	case remote.SmartSwitch:
		return remote.Busy(kind).Error()
	case OpBisect:
		return "git is in the middle of a bisect; finish it with `git bisect reset`."
	}
	if len(conflicts) > 0 {
		return fmt.Sprintf("The %s stopped: %d file(s) have conflicts: %s. Fix each one (keep ours, keep theirs, or edit it), then continue; or abort to go back to how it was before.", kind, len(conflicts), list(conflicts))
	}
	return fmt.Sprintf("The %s stopped and every conflict is fixed: continue to finish it, or abort to go back to how it was before.", kind)
}

// Unresolved is the refusal to continue while files still have conflicts.
func Unresolved(conflicts []string) error {
	return fmt.Errorf("%d file(s) still have conflicts: %s. Fix them (gitswitch conflict <path> --ours|--theirs|--done) and continue again", len(conflicts), list(conflicts))
}

// MaxConflictBytes is the largest version of a conflicted file returned
// or saved; bigger files are fixed in an editor.
const MaxConflictBytes = 512 << 10

// Side is a version of a conflicted file.
type Side string

const (
	Ours   Side = "ours"
	Theirs Side = "theirs"
)

// Conflict is a conflicted file: the three versions git keeps in the
// index (absent ones are nil: a file added on one side has no base, one
// deleted on a side has no version there) and the working file with
// conflict markers.
type Conflict struct {
	Path   string  `json:"path"`
	Kind   string  `json:"kind"`   // e.g. "both changed", "deleted by them"
	Op     string  `json:"op"`     // the operation in progress, "" for none
	Base   *string `json:"base"`   // stage 1: the last version both shared
	Ours   *string `json:"ours"`   // stage 2: this side
	Theirs *string `json:"theirs"` // stage 3: the side coming in
	// Text is the working file, with git's conflict markers; "" when it
	// is deleted (Exists false).
	Text    string `json:"text"`
	Exists  bool   `json:"exists"`
	Markers bool   `json:"markers"` // Text still holds conflict markers
	// OursLabel and TheirsLabel say in plain words what each side is.
	OursLabel   string `json:"oursLabel"`
	TheirsLabel string `json:"theirsLabel"`
}

// KindOf names a conflict from the versions it has.
func KindOf(base, ours, theirs bool) string {
	switch {
	case ours && theirs && base:
		return "both changed"
	case ours && theirs:
		return "both added"
	case ours:
		return "deleted by them"
	case theirs:
		return "deleted by us"
	}
	return "deleted on both sides"
}

// Binary reports whether a file's content is binary (holds a NUL byte in
// its first 8000 bytes, as git decides).
func Binary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for _, c := range b[:n] {
		if c == 0 {
			return true
		}
	}
	return false
}

// HasMarkers reports whether text still holds a conflict git marked (the
// same check as finishing a smart switch).
func HasMarkers(text string) bool { return refs.HasConflictMarkers(text) }

// MarkersLeft is the refusal to mark a file resolved while it still has
// conflict markers.
func MarkersLeft(path string) error {
	return fmt.Errorf("%s still has conflict markers. Keep the lines you want and delete the <<<<<<<, ======= and >>>>>>> lines, then try again", path)
}

// Git reads and changes the repo for merging and for stopped operations.
type Git interface {
	// Head reads where the repo is.
	Head(gitDir string) (refs.Head, error)
	// Resolve is the commit a branch, tag or commit names; an error when
	// it names none.
	Resolve(gitDir, rev string) (string, error)
	// MergeBase is the last commit both share; "" when none.
	MergeBase(gitDir, a, b string) string
	// Commits lists the commits reachable from tip and not from not,
	// newest first, at most limit, with how many there are in all.
	Commits(gitDir, tip, not string, limit int) ([]CommitInfo, int, error)
	// Changed lists the files that differ between two commits.
	Changed(gitDir, from, to string) ([]string, error)
	// Predict lists the files merging theirs into ours would leave in
	// conflict, without touching the work tree; ok is false when this git
	// can't tell (older than 2.38).
	Predict(gitDir, ours, theirs string) (conflicts []string, ok bool)
	// Merge merges rev into the current branch in worktree.
	Merge(worktree, rev string, mode Mode) error
	// ResetMerge puts the work tree and index back as at HEAD after a
	// merge that left no state (git reset --merge).
	ResetMerge(worktree string) error
	// Op is the operation in progress ("" for none), told apart more
	// finely than Head.Busy (a stopped series of reverts is a revert).
	Op(gitDir string) string
	// Merging is the commit being merged in and the name in git's merge
	// message, during a merge.
	Merging(gitDir string) (hash, name string)
	// Conflicted lists the files in conflict.
	Conflicted(gitDir string) ([]string, error)
	// Run runs `git <op> --<action>` (continue, skip, abort) in worktree,
	// keeping the message git wrote.
	Run(worktree, op, action string) error
	// Stage reads a version of a file from the index (1 base, 2 ours, 3
	// theirs); ok is false when it has none.
	Stage(gitDir, path string, n int) (data []byte, ok bool, err error)
	// ReadFile reads a working file; ok is false when it does not exist.
	ReadFile(worktree, path string) (data []byte, ok bool, err error)
	// WriteFile replaces a working file's content.
	WriteFile(worktree, path string, data []byte) error
	// Take checks out one side's version of a conflicted file.
	Take(worktree, path string, side Side) error
	// Add stages a file as it is in the work tree (a deleted file is
	// staged as deleted), which marks it resolved.
	Add(worktree, path string) error
}

// Recorder remembers a merge so it can be undone.
type Recorder interface {
	Before(gitDir string) undo.Mark
	// Merged remembers a merge of theirs (the commit merged in, named
	// from) that moved the branch.
	Merged(gitDir string, before undo.Mark, theirs, from, label string)
}
