// Package domain describes a repo's branches and HEAD as the apps show
// them, and declares what the branch use cases need from git (ports). It
// is pure. Import it as refs.
package domain

import (
	"fmt"
	"strings"
)

// Head is where a repo is: its branch, latest commit and how it compares
// with the branch it pulls from.
type Head struct {
	Branch   string `json:"branch"` // "" when HEAD is detached
	Hash     string `json:"hash"`   // "" in a repo without commits
	Short    string `json:"short"`
	Subject  string `json:"subject"`
	Upstream string `json:"upstream"` // e.g. origin/main; "" when none
	// Gone is the remote branch the branch is set to pull from when this
	// clone has no copy of it (deleted on the server, or a single-branch
	// clone); Upstream is "" then.
	Gone   string `json:"gone"`
	Ahead  int    `json:"ahead"`  // commits not pushed to the upstream
	Behind int    `json:"behind"` // commits on the upstream not pulled yet
	Busy   string `json:"busy"`   // an operation git is in the middle of, e.g. "rebase"
	// Changes counts what is not committed yet in the working folder.
	Changes Changes `json:"changes"`
	// Parked counts the changes parked on this branch, waiting to be
	// restored (see Parked).
	Parked int `json:"parked"`
}

// Changes counts the files with uncommitted changes. A file changed again
// after it was staged counts as both staged and changed.
type Changes struct {
	Staged    int `json:"staged"`    // changes added to the next commit
	Changed   int `json:"changed"`   // tracked files changed but not staged
	New       int `json:"new"`       // files git does not track yet
	Conflicts int `json:"conflicts"` // files with unresolved conflicts
}

// Any reports whether there is anything not committed.
func (c Changes) Any() bool { return c.Staged+c.Changed+c.New+c.Conflicts > 0 }

// Describe is e.g. "uncommitted: 1 conflict, 2 staged, 3 changed, 1 new";
// "" when there is nothing to commit.
func (c Changes) Describe() string {
	if !c.Any() {
		return ""
	}
	var parts []string
	if c.Conflicts > 0 {
		parts = append(parts, plural(c.Conflicts, "conflict"))
	}
	for _, p := range []struct {
		n    int
		what string
	}{{c.Staged, "staged"}, {c.Changed, "changed"}, {c.New, "new"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return "uncommitted: " + strings.Join(parts, ", ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// Describe is a one-line summary, e.g. "main → origin/main, 2 to push ·
// HEAD 1a2b3c4 Fix login · uncommitted: 3 changed".
func (h Head) Describe() string {
	var b strings.Builder
	switch {
	case h.Branch != "":
		b.WriteString(h.Branch)
	case h.Hash != "":
		b.WriteString("detached HEAD")
	default:
		b.WriteString("no branch")
	}
	if h.Upstream != "" {
		b.WriteString(" → " + h.Upstream)
		var counts []string
		if h.Ahead > 0 {
			counts = append(counts, fmt.Sprintf("%d to push", h.Ahead))
		}
		if h.Behind > 0 {
			counts = append(counts, fmt.Sprintf("%d to pull", h.Behind))
		}
		if len(counts) == 0 {
			counts = append(counts, "up to date")
		}
		b.WriteString(", " + strings.Join(counts, ", "))
	} else if h.Gone != "" {
		b.WriteString(" → " + h.Gone + " (not in this clone)")
	} else if h.Branch != "" {
		b.WriteString(" (not on a remote)")
	}
	if h.Hash != "" {
		b.WriteString(" · HEAD " + h.Short + " " + h.Subject)
	} else {
		b.WriteString(" · no commits yet")
	}
	if c := h.Changes.Describe(); c != "" {
		b.WriteString(" · " + c)
	}
	if h.Parked > 0 {
		fmt.Fprintf(&b, " · %d parked here", h.Parked)
	}
	if h.Busy != "" {
		b.WriteString(" · " + h.Busy + " in progress")
	}
	return b.String()
}

// Switched is the result of a switch.
type Switched struct {
	Branch  string `json:"branch"`  // the local branch now checked out
	Carried bool   `json:"carried"` // uncommitted changes came along
	// Clash is set when some of the changes clash with the new branch:
	// they have conflict markers to fix.
	Clash *Clash `json:"clash"`
	// Parked: the uncommitted changes were parked on the branch left.
	Parked bool `json:"parked"`
	// Waiting lists the changes parked earlier on the branch switched to,
	// newest first, when they can be restored now (nothing uncommitted).
	Waiting []Parked `json:"waiting"`
}

// Parked is a set of uncommitted changes left on a branch: an ordinary
// stash entry with a message that names the branch (ParkMessage).
type Parked struct {
	Ref    string   `json:"ref"`    // stash@{n}, as git stash list shows it
	Stash  string   `json:"stash"`  // the stash commit
	Branch string   `json:"branch"` // the branch they were parked on
	Time   int64    `json:"time"`   // when, in Unix seconds
	Files  []string `json:"files"`  // the files they change, new files included
}

// Describe is e.g. "main · 2 h ago · 3 files: a.go, b.go, c.go", now
// being the Unix time.
func (p Parked) Describe(now int64) string {
	return p.Branch + " · " + Ago(now, p.Time) + " · " + FileList(p.Files)
}

// FileList is e.g. "3 files: a.go, b.go, c.go", with at most 5 named.
func FileList(files []string) string {
	n := len(files)
	switch {
	case n == 0:
		return "no files"
	case n == 1:
		return "1 file: " + files[0]
	case n > 5:
		return fmt.Sprintf("%d files: %s and %d more", n, strings.Join(files[:5], ", "), n-5)
	}
	return fmt.Sprintf("%d files: %s", n, strings.Join(files, ", "))
}

// Ago says roughly how long before now a Unix time was.
func Ago(now, then int64) string {
	d := now - then
	switch {
	case d < 60:
		return "just now"
	case d < 3600:
		return fmt.Sprintf("%d min ago", d/60)
	case d < 48*3600:
		return fmt.Sprintf("%d h ago", d/3600)
	default:
		return fmt.Sprintf("%d days ago", d/86400)
	}
}

const parkPrefix = "gitswitch park: "

// ParkMessage is the stash message of changes parked on branch.
func ParkMessage(branch string) string { return parkPrefix + branch }

// ParkedBranch reads the branch from a stash entry's subject, as git
// stash list shows it ("On main: gitswitch park: main"). ok is false for
// a stash entry that holds no parked changes.
func ParkedBranch(subject string) (branch string, ok bool) {
	if !strings.HasPrefix(subject, parkPrefix) {
		i := strings.Index(subject, ": "+parkPrefix)
		if i < 0 {
			return "", false
		}
		subject = subject[i+2:]
	}
	branch = strings.TrimPrefix(subject, parkPrefix)
	return branch, branch != ""
}

// ParkedOn lists the parked changes of one branch, in the same order.
func ParkedOn(all []Parked, branch string) []Parked {
	var out []Parked
	for _, p := range all {
		if p.Branch == branch {
			out = append(out, p)
		}
	}
	return out
}

// Clash is a smart switch that left files with conflicts. Until they are
// fixed (or the switch is undone), the original uncommitted changes stay
// safe in a stash entry.
type Clash struct {
	From  string   `json:"from"`  // the branch the changes were made on
	To    string   `json:"to"`    // the branch switched to
	Stash string   `json:"stash"` // the stash commit holding the original changes
	Files []string `json:"files"` // the files that had conflicts
}

// WouldOverwrite reports whether git refused a switch because it would
// overwrite uncommitted changes (or new files).
func WouldOverwrite(err error) bool {
	return err != nil && strings.Contains(err.Error(), "would be overwritten")
}

// HasConflictMarkers reports whether text still holds a conflict git
// marked: a line starting with <<<<<<< followed later by one starting
// with >>>>>>>.
func HasConflictMarkers(text string) bool {
	open := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<< ") || line == "<<<<<<<":
			open = true
		case open && (strings.HasPrefix(line, ">>>>>>> ") || line == ">>>>>>>"):
			return true
		}
	}
	return false
}

// Branch is a local branch or a branch on a remote.
type Branch struct {
	Name     string `json:"name"`     // main, or origin/main for a remote branch
	Remote   bool   `json:"remote"`   // a remote branch (switching creates a local one)
	Current  bool   `json:"current"`  // checked out
	Upstream string `json:"upstream"` // for a local branch: the branch it pulls from
}

// LocalName is the local branch to switch to for a branch: the same name
// for a local branch, and the name without the remote for a remote one
// (origin/feature/x → feature/x).
func (b Branch) LocalName() string {
	if !b.Remote {
		return b.Name
	}
	if _, rest, ok := strings.Cut(b.Name, "/"); ok {
		return rest
	}
	return b.Name
}

// Find returns the branch with this name: a local branch first, then a
// remote one. nil when there is none.
func Find(branches []Branch, name string) *Branch {
	for _, remote := range []bool{false, true} {
		for i := range branches {
			if branches[i].Remote == remote && branches[i].Name == name {
				return &branches[i]
			}
		}
	}
	return nil
}

// Refs reads and moves branches with git.
type Refs interface {
	// Head reads where the repo is, its uncommitted changes included.
	Head(gitDir string) (Head, error)
	// Branches lists local branches, then remote ones.
	Branches(gitDir string) ([]Branch, error)
	// Switch checks out a local branch.
	Switch(worktree, branch string) error
	// Track creates a local branch from a remote one and checks it out.
	Track(worktree, local, remoteBranch string) error
	// Pull fast-forwards the current branch from its upstream, through the
	// repo's account. Each progress line goes to progress.
	Pull(worktree string, progress func(string)) error

	// Stash saves every uncommitted change, new files included, as a
	// stash entry and returns its commit.
	Stash(worktree, message string) (string, error)
	// Apply puts a stash entry's changes back without removing the entry,
	// staged changes staged when that applies cleanly. It returns the
	// files left with conflict markers; an error means nothing changed.
	Apply(worktree, stash string) ([]string, error)
	// DropStash removes a stash entry.
	DropStash(worktree, stash string) error
	// Discard throws away the uncommitted changes to tracked files and
	// deletes the new files the stash entry holds.
	Discard(worktree, stash string) error
	// Unstage makes staged and conflicting files plain uncommitted changes.
	Unstage(worktree string) error
	// Markers lists the files that still hold conflict markers.
	Markers(worktree string, files []string) []string
	// Parked lists the parked changes, newest first.
	Parked(gitDir string) ([]Parked, error)
	// ParkedPatch shows what a stash entry changes: a diff, then the new
	// files it holds.
	ParkedPatch(worktree, stash string) (string, error)
	// SaveClash, LoadClash and ClearClash keep an open clash in the git
	// dir; LoadClash returns nil when there is none.
	SaveClash(gitDir string, c Clash) error
	LoadClash(gitDir string) (*Clash, error)
	ClearClash(gitDir string) error
}
