// Package domain describes making a commit from the working folder, and
// declares what it needs from git (ports). It is pure. Import it as
// workspace.
package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// Committed is the result of a commit.
type Committed struct {
	Branch  string `json:"branch"` // "" on a detached HEAD
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	All     bool   `json:"all"`     // every change was committed, new files too; otherwise what was staged
	Files   int    `json:"files"`   // how many files the commit holds
	Account string `json:"account"` // the gitswitch account the commit is made as; "" when the repo has none
}

// Message tidies a commit message: surrounding blank space goes, and a
// message with no text is "".
func Message(m string) string {
	return strings.TrimSpace(strings.ReplaceAll(m, "\r\n", "\n"))
}

// Files counts the files a commit takes: the staged ones, or with all
// every file with a change, new ones included. A file staged and changed
// again counts once with all.
func Files(c refs.Changes, all bool) int {
	if !all {
		return c.Staged
	}
	return max(c.Staged, c.Changed) + c.New
}

// Git makes commits.
type Git interface {
	// Head reads where the repo is, its uncommitted changes included.
	Head(gitDir string) (refs.Head, error)
	// Commit commits what is staged; with all, every change first, new
	// files included (git add --all).
	Commit(worktree, message string, all bool) error
}

// File is one path with uncommitted changes. Staged and Worktree are git's
// two status letters: what is in the next commit, and what changed in the
// working folder since ("." unchanged, M modified, A added, D deleted,
// R renamed, C copied, T type changed, U unmerged, ? new).
type File struct {
	Path     string `json:"path"`           // relative to the repo's root, with /
	Orig     string `json:"orig,omitempty"` // the old path of a rename or copy
	Staged   string `json:"staged"`
	Worktree string `json:"worktree"`
	Conflict bool   `json:"conflict,omitempty"`
}

// New reports a file git does not track yet.
func (f File) New() bool { return f.Staged == "?" }

// IsStaged reports a change in the next commit.
func (f File) IsStaged() bool { return !f.Conflict && !f.New() && f.Staged != "." }

// IsChanged reports a change not staged: in a tracked file, or a new file.
func (f File) IsChanged() bool { return !f.Conflict && (f.New() || f.Worktree != ".") }

// Describe is the change in words, e.g. "modified", "renamed from a.go",
// "new", "deleted, staged".
func (f File) Describe() string {
	if f.Conflict {
		return "conflict"
	}
	if f.New() {
		return "new"
	}
	word := func(l string) string {
		switch l {
		case "A":
			return "added"
		case "D":
			return "deleted"
		case "R":
			return "renamed from " + f.Orig
		case "C":
			return "copied from " + f.Orig
		case "T":
			return "type changed"
		}
		return "modified"
	}
	switch {
	case f.IsStaged() && f.Worktree != ".":
		return word(f.Staged) + ", staged; changed again"
	case f.IsStaged():
		return word(f.Staged) + ", staged"
	}
	return word(f.Worktree)
}

// Status is the working folder's uncommitted changes.
type Status struct {
	Branch string `json:"branch"` // "" on a detached HEAD
	Busy   string `json:"busy"`   // the operation git is in the middle of, or ""
	Files  []File `json:"files"`
}

// Staged lists the files with staged changes, Changed those with changes
// not staged (new files included), Conflicts the conflicted ones. A file
// staged and changed again is in both Staged and Changed.
func (s Status) Staged() []File    { return pick(s.Files, File.IsStaged) }
func (s Status) Changed() []File   { return pick(s.Files, File.IsChanged) }
func (s Status) Conflicts() []File { return pick(s.Files, func(f File) bool { return f.Conflict }) }

// Find returns the file with this path.
func (s Status) Find(path string) (File, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return File{}, false
}

func pick(files []File, keep func(File) bool) []File {
	out := []File{}
	for _, f := range files {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

// CleanPath turns a path as typed (./a/b, a\b on Windows, a//b) into the
// repo-relative form git prints; it refuses an empty path, one that leaves
// the repo, and one git would read as an option.
func CleanPath(p string) (string, error) {
	q := path.Clean(strings.ReplaceAll(strings.TrimSpace(p), "\\", "/"))
	switch {
	case p == "" || q == ".":
		return "", errors.New("name a file")
	case q == ".." || strings.HasPrefix(q, "../") || strings.HasPrefix(q, "/"):
		return "", fmt.Errorf("%q is outside the repo", p)
	case strings.HasPrefix(q, "-"):
		return "", fmt.Errorf("%q is not a file name", p)
	}
	return q, nil
}

// DiscardMessage names the stash that keeps discarded changes.
func DiscardMessage(branch string) string {
	if branch == "" {
		branch = "detached HEAD"
	}
	return "gitswitch discard: " + branch
}

// Diff is the uncommitted change of one file, or of every file when Path
// is "".
type Diff struct {
	Path      string `json:"path"`
	Staged    bool   `json:"staged"`    // the staged change; otherwise the one not staged
	Text      string `json:"text"`      // a unified diff; "" when there is no change
	Truncated bool   `json:"truncated"` // cut at MaxDiffBytes
}

// MaxDiffBytes is how much of a diff is returned, as for a commit's diff.
const MaxDiffBytes = 256 << 10

// Changes reads and changes the working folder and the index. Paths are
// repo-relative and taken literally (no wildcards).
type Changes interface {
	// Status lists the files with uncommitted changes.
	Status(gitDir string) ([]File, error)
	// Diff is the change not staged (new files against nothing), or with
	// staged the staged one, of paths; every file when paths is empty.
	Diff(gitDir string, staged bool, paths []string) (string, error)
	// Stage adds paths' changes, deletions included, to the index; every
	// change when paths is empty.
	Stage(worktree string, paths []string) error
	// Unstage takes paths' changes out of the index, keeping the files;
	// every staged change when paths is empty.
	Unstage(worktree string, paths []string) error
	// Discard puts paths back as in the last commit, new files removed,
	// keeping what it removes as a stash with this message; every change
	// when paths is empty.
	Discard(worktree, message string, paths []string) error
}
