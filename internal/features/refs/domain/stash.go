package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Kinds of stash entry: changes GitSwitch parked when switching branch,
// changes it kept when they were discarded, and every other stash.
const (
	StashPark    = "park"
	StashDiscard = "discard"
	StashPlain   = "stash"
)

const discardPrefix = "gitswitch discard: "

// MaxStashDiffBytes is how much of a stashed file's diff is returned, as
// for a commit's diff.
const MaxStashDiffBytes = 256 << 10

// ErrStashChanged is returned when a stash is no longer in the list as it
// was shown, because the list changed since (indexes shift, entries go).
var ErrStashChanged = errors.New("the stash list changed; reload")

// StashFile is one file a stash entry holds.
type StashFile struct {
	Path string `json:"path"`
	// Status is A (added), M (modified), D (deleted), T (type changed) for
	// tracked files, or ? for a new file git did not track (stored in the
	// stash's third parent when it was made with --include-untracked).
	Status    string `json:"status"`
	Untracked bool   `json:"untracked"`
	Staged    bool   `json:"staged"`  // the change was staged when stashed
	Added     int    `json:"added"`   // lines added
	Deleted   int    `json:"deleted"` // lines deleted
	Binary    bool   `json:"binary"`
}

// Stash is one entry of git's stash list.
type Stash struct {
	Index    int         `json:"index"`    // n of stash@{n}; shifts as entries come and go
	Ref      string      `json:"ref"`      // stash@{n}
	Hash     string      `json:"hash"`     // the stash commit: stable, use it to name the entry
	Subject  string      `json:"subject"`  // git's own line for it, e.g. "On main: fix the thing"
	Message  string      `json:"message"`  // what was typed, or git's "abc1234 subject" for none
	Branch   string      `json:"branch"`   // the branch it was made on; "" when HEAD was detached
	Time     int64       `json:"time"`     // when, in Unix seconds
	Kind     string      `json:"kind"`     // StashPark, StashDiscard or StashPlain
	ParkedOn string      `json:"parkedOn"` // the branch parked changes belong to; "" for other kinds
	Files    []StashFile `json:"files"`
	// Untracked counts the new files among Files.
	Untracked int `json:"untracked"`
}

// StashDiff is the diff of one file of a stash entry.
type StashDiff struct {
	Path      string `json:"path"`
	Untracked bool   `json:"untracked"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"` // cut at MaxStashDiffBytes
}

// StashOptions says what a new stash holds.
type StashOptions struct {
	Message   string   `json:"message"`   // "" lets git name it
	Paths     []string `json:"paths"`     // only these files (literal paths); none: every change
	Untracked bool     `json:"untracked"` // new files too
	KeepIndex bool     `json:"keepIndex"` // staged changes stay staged in the working folder
}

// StashGit is what the stash manager needs from git, beside the Refs
// methods Apply, DropStash and Discard. refsinfra.Git implements it.
type StashGit interface {
	// Stashes lists every stash entry, newest first, with its files.
	Stashes(gitDir string) ([]Stash, error)
	// StashDiff shows one file of a stash entry.
	StashDiff(gitDir, hash, path string) (StashDiff, error)
	// StashPush saves the working changes as a new stash entry and returns
	// its commit. An error means nothing was stashed.
	StashPush(worktree string, o StashOptions) (string, error)
	// StashBranch makes a branch at the commit the stash was made on, checks
	// it out, puts the stash's changes there and drops it. An error means
	// the branch was not made and nothing changed.
	StashBranch(worktree, hash, name string) error
}

// StashKind tells which kind of entry a stash list subject (git's %gs,
// e.g. "On main: gitswitch park: main") is, and for parked or discarded
// changes the branch they belong to.
func StashKind(subject string) (kind, branch string) {
	if b, ok := ParkedBranch(subject); ok {
		return StashPark, b
	}
	if i := strings.Index(subject, ": "+discardPrefix); strings.HasPrefix(subject, discardPrefix) || i >= 0 {
		if i >= 0 {
			subject = subject[i+2:]
		}
		if b := strings.TrimPrefix(subject, discardPrefix); b != "" {
			return StashDiscard, b
		}
	}
	return StashPlain, ""
}

// StashSubject splits a stash list subject as git writes it: "On main:
// text" (typed message) or "WIP on main: 1a2b3c4 subject" (none typed).
// The branch is "" when HEAD was detached ("(no branch)").
func StashSubject(subject string) (branch, message string) {
	rest := subject
	switch {
	case strings.HasPrefix(rest, "WIP on "):
		rest = rest[len("WIP on "):]
	case strings.HasPrefix(rest, "On "):
		rest = rest[len("On "):]
	default:
		return "", subject
	}
	b, msg, ok := strings.Cut(rest, ": ")
	if !ok {
		return "", subject
	}
	if b == "(no branch)" {
		b = ""
	}
	return b, msg
}

// Describe is e.g. "stash@{0} · main · 2 h ago · 3 files: a.go, b.go, c.go ·
// fix the thing", now being the Unix time.
func (s Stash) Describe(now int64) string {
	b := s.Branch
	if b == "" {
		b = "detached HEAD"
	}
	paths := make([]string, len(s.Files))
	for i, f := range s.Files {
		paths[i] = f.Path
	}
	out := fmt.Sprintf("%s · %s · %s · %s", s.Ref, b, Ago(now, s.Time), FileList(paths))
	if s.Message != "" {
		out += " · " + s.Message
	}
	return out
}

// FindStash returns the stash entry with this commit, or ErrStashChanged.
func FindStash(all []Stash, hash string) (Stash, error) {
	if hash != "" {
		for _, s := range all {
			if s.Hash == hash {
				return s, nil
			}
		}
	}
	return Stash{}, ErrStashChanged
}

// Label names a stash entry in words, e.g. "stash@{1} (fix the thing)".
func (s Stash) Label() string {
	if s.Kind == StashPark {
		return s.Ref + " (parked on " + s.ParkedOn + ")"
	}
	if s.Message != "" {
		return s.Ref + " (" + s.Message + ")"
	}
	return s.Ref
}

// CleanStashPath turns a path as typed (./a/b, a\b on Windows) into the
// repo-relative form git prints; it refuses an empty path, one that leaves
// the repo, and one git would read as an option.
func CleanStashPath(p string) (string, error) {
	q := path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
	switch {
	case strings.TrimSpace(p) == "" || q == ".":
		return "", errors.New("name a file")
	case q == ".." || strings.HasPrefix(q, "../") || strings.HasPrefix(q, "/"):
		return "", fmt.Errorf("%q is outside the repo", p)
	case strings.HasPrefix(q, "-"):
		return "", fmt.Errorf("%q is not a file name", p)
	}
	return q, nil
}

// ReservedStashMessage reports whether a message starts with the words
// GitSwitch uses for parked and discarded changes, which a stash of your own
// must not borrow.
func ReservedStashMessage(m string) bool {
	m = strings.TrimSpace(m)
	return strings.HasPrefix(m, parkPrefix) || strings.HasPrefix(m, discardPrefix)
}
