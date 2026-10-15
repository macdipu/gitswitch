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

// Moved is the result of moving the commits not pushed to a new branch.
type Moved struct {
	Branch  string `json:"branch"`  // the new branch, now checked out
	From    string `json:"from"`    // the branch the commits were taken off
	Commits int    `json:"commits"` // how many commits moved
	Base    string `json:"base"`    // where From is now: the last commit it shares with its upstream
	Carried bool   `json:"carried"` // there were uncommitted changes; they stayed as they are
}

// WithPrefix puts the branch name prefix (e.g. "feature/") in front of a
// new branch's name, unless it is already there.
func WithPrefix(prefix, name string) string {
	if prefix == "" || strings.HasPrefix(name, prefix) {
		return name
	}
	return prefix + name
}

// SwitchMode is what switching branch does with uncommitted changes.
type SwitchMode string

const (
	Ask   SwitchMode = "ask"   // ask each time (the default)
	Smart SwitchMode = "smart" // take them along: smart switch
	Park  SwitchMode = "park"  // leave them parked on their branch
)

// ParseSwitchMode reads "ask", "smart" or "park" (any case); "" for
// anything else.
func ParseSwitchMode(s string) SwitchMode {
	switch m := SwitchMode(strings.ToLower(strings.TrimSpace(s))); m {
	case Ask, Smart, Park:
		return m
	}
	return ""
}

// SwitchSetting says what switching does with uncommitted changes in a
// repo: its own choice, else the one for every repo, else ask.
type SwitchSetting struct {
	Mode   SwitchMode `json:"mode"`   // in effect
	Repo   SwitchMode `json:"repo"`   // saved for this repo; "" follows Global
	Global SwitchMode `json:"global"` // for every repo
}

// NewSwitchSetting reads the saved values; an unknown one counts as unset.
func NewSwitchSetting(global, repo string) SwitchSetting {
	s := SwitchSetting{Global: ParseSwitchMode(global), Repo: ParseSwitchMode(repo)}
	if s.Global == "" {
		s.Global = Ask
	}
	s.Mode = s.Global
	if s.Repo != "" {
		s.Mode = s.Repo
	}
	return s
}

// PullMode is how Update brings a branch up to date.
type PullMode string

const (
	FFOnly PullMode = "ff-only" // only when the branch has no commits of its own (the default)
	Rebase PullMode = "rebase"  // also put your not-pushed commits on top of the new ones
)

// ParsePullMode reads a saved or typed mode; "" for an unknown one.
func ParsePullMode(s string) PullMode {
	switch PullMode(strings.ToLower(strings.TrimSpace(s))) {
	case FFOnly, "ff", "fast-forward":
		return FFOnly
	case Rebase:
		return Rebase
	}
	return ""
}

// PullSetting is the update mode in effect for a repo, and where it
// comes from (gitswitch.pull, in the repo's config before the global one).
type PullSetting struct {
	Mode   PullMode `json:"mode"`
	Repo   PullMode `json:"repo"`   // saved for this repo; "" follows Global
	Global PullMode `json:"global"` // for every repo
}

// NewPullSetting reads the saved values; an unknown one counts as unset.
func NewPullSetting(global, repo string) PullSetting {
	s := PullSetting{Global: ParsePullMode(global), Repo: ParsePullMode(repo)}
	if s.Global == "" {
		s.Global = FFOnly
	}
	s.Mode = s.Global
	if s.Repo != "" {
		s.Mode = s.Repo
	}
	return s
}

// Describe is e.g. "park (this repo)" or "ask (every repo)".
func (s SwitchSetting) Describe() string {
	if s.Repo != "" {
		return string(s.Mode) + " (this repo)"
	}
	return string(s.Mode) + " (every repo)"
}

// Pushed is the result of a push.
type Pushed struct {
	Branch   string `json:"branch"`
	Remote   string `json:"remote"`   // e.g. origin
	Upstream string `json:"upstream"` // the remote branch, e.g. origin/feature-x
	First    bool   `json:"first"`    // it was not on the remote: now the branch tracks it
	Commits  int    `json:"commits"`  // how many commits went; 0: nothing to push
	// Account is the gitswitch account that pushes; "" when git will ask
	// for one.
	Account string `json:"account"`
	// PRURL opens a pull request for the branch on the host; "" for the
	// remote's default branch or a host without one.
	PRURL string `json:"prUrl"`
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
	Hash     string `json:"hash"`     // the commit it points at
	// Gone: a local branch set to pull from a remote branch this clone no
	// longer has (deleted on the server, then fetched with --prune).
	Gone bool `json:"gone"`
	// Elsewhere: a local branch checked out in another working folder (git
	// worktree).
	Elsewhere bool `json:"elsewhere"`
}

// Compared is what a branch would push to and pull from its upstream,
// newest commits first. Long lists are cut: MorePush and MorePull count
// the commits left out.
type Compared struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
	ToPush   []Line `json:"toPush"`
	ToPull   []Line `json:"toPull"`
	MorePush int    `json:"morePush"`
	MorePull int    `json:"morePull"`
}

// Line is one commit in a list.
type Line struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Time    int64  `json:"time"` // Unix seconds
}

// Merged is what pruning branches does: the local branches merged into
// Into, deleted by Prune, and the ones it keeps although their remote
// branch is gone.
type Merged struct {
	Into     string   `json:"into"`     // e.g. origin/main
	Branches []Branch `json:"branches"` // merged: safe to delete
	// Kept: their remote branch was deleted, but their commits are not on
	// Into (squash-merged or rebased on the server, or never merged).
	Kept []Branch `json:"kept"`
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
	// Create starts a branch at a commit ("" for HEAD) and checks it out,
	// keeping the uncommitted changes.
	Create(worktree, branch, commit string) error
	// CheckName refuses what git does not accept as a branch name.
	CheckName(branch string) error
	// Commit resolves a commit, branch or tag to its commit hash.
	Commit(gitDir, rev string) (string, error)
	// Commits lists up to limit commits on to that are not on from,
	// newest first.
	Commits(gitDir, from, to string, limit int) ([]Line, error)
	// MergedInto lists the local branches whose commits are all on rev.
	MergedInto(gitDir, rev string) ([]string, error)
	// DeleteBranch deletes a branch that is not checked out, only while it
	// is still at was, and forgets the remote branch it pulled from.
	DeleteBranch(gitDir, branch, was string) error
	// MergeBase is the last commit two commits share.
	MergeBase(gitDir, a, b string) (string, error)
	// MoveBranch points a branch that is not checked out at the commit to
	// ("" deletes it), only while it is still at was; why goes in the
	// reflog.
	MoveBranch(gitDir, branch, to, was, why string) error
	// BranchPrefix and SaveBranchPrefix keep the prefix of new branch
	// names, for every repo ("" for none).
	BranchPrefix() string
	SaveBranchPrefix(prefix string) error
	// Pull fast-forwards the current branch from its upstream, through the
	// repo's account. Each progress line goes to progress.
	Pull(worktree string, progress func(string)) error
	// PullRebase fetches the upstream and puts the branch's own commits on
	// top of it. When that fails it aborts the rebase, so nothing changes.
	PullRebase(worktree string, progress func(string)) error
	// PullModes reads gitswitch.pull globally and for the repo ("" each
	// when unset); SavePullMode saves it like SaveSwitchMode.
	PullModes(gitDir string) (global, repo string)
	SavePullMode(gitDir, value string) error
	// Merges counts the merge commits in from..to.
	Merges(gitDir, from, to string) int
	// Resolve returns the commit a revision names, or "".
	Resolve(gitDir, rev string) string
	// PushTarget reads where a branch pushes from its config: the remote
	// and the remote branch (refs/heads/x); "" when it has none.
	PushTarget(gitDir, branch string) (remote, merge string)
	// RemoteNames lists the repo's remotes.
	RemoteNames(gitDir string) []string
	// NotPushed counts HEAD's commits that are on no remote branch.
	NotPushed(gitDir string) int
	// DefaultBranch is a remote's default branch, e.g. main; "" when this
	// clone does not know it.
	DefaultBranch(gitDir, remote string) string
	// Push sends the local ref src to the remote's ref dst through the
	// repo's account, never forcing; setUpstream makes the branch track
	// it. Each progress line goes to progress.
	Push(worktree, remote, src, dst string, setUpstream bool, progress func(string)) error

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
	// SwitchModes reads the saved switch-changes values: for every repo,
	// and the repo's own ("" when unset, or when gitDir is "").
	SwitchModes(gitDir string) (global, repo string)
	// SaveSwitchMode saves the value for every repo (gitDir "") or for one;
	// "" removes it.
	SaveSwitchMode(gitDir, value string) error
	// SaveClash, LoadClash and ClearClash keep an open clash in the git
	// dir; LoadClash returns nil when there is none.
	SaveClash(gitDir string, c Clash) error
	LoadClash(gitDir string) (*Clash, error)
	ClearClash(gitDir string) error
}
