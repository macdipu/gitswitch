// Package domain describes looking at history without changing it:
// comparing any two commits, branches or tags (G22), the reflog (G25) and
// a guided bisect (G26). It declares what those use cases need from git
// (the Git port). It is pure and table-tested. Import it as inspect.
package domain

import (
	"fmt"
	"strconv"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// Limits: nothing here loads a whole history.
const (
	MaxCommits   = 50  // commits listed on each side of a comparison
	MaxFiles     = 500 // changed files listed
	MaxDiffBytes = history.MaxDiffBytes
)

// Working stands for the working folder (what is on disk now, tracked
// files only) as the second side of a comparison. It is not a valid ref
// name, so it can't be mistaken for a branch.
const Working = ":working"

// Mode says which changes a comparison shows.
type Mode string

const (
	// SinceFork is what B changed since A and B went apart (git A...B).
	SinceFork Mode = "since-fork"
	// Direct is the difference between the two as they are now (git A..B).
	Direct Mode = "direct"
)

// ParseMode reads a mode as the API and the command line name it; "" is
// SinceFork.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "since-fork", "three-dot", "fork":
		return SinceFork, nil
	case "direct", "two-dot":
		return Direct, nil
	}
	return SinceFork, fmt.Errorf("unknown comparison mode %q: use since-fork or direct", s)
}

// CheckSide refuses a side git would read as an option, or that is empty.
// The working folder is only allowed as the second side.
func CheckSide(ref string, second bool) error {
	if ref == Working {
		if !second {
			return fmt.Errorf("the working folder can only be the second side: compare a branch or commit with it, not the other way round")
		}
		return nil
	}
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("name a branch, tag or commit to compare")
	}
	return history.CheckRef(ref)
}

// Commit is one commit a comparison or the reflog lists.
type Commit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Time    int64  `json:"time"` // Unix seconds
	Subject string `json:"subject"`
}

// FileChange is one file that differs between the two sides.
type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"` // for a rename or copy
	Status  string `json:"status"`            // added, modified, deleted, renamed, copied, type changed
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
}

// Compared is a comparison of two sides.
type Compared struct {
	A       string `json:"a"`
	B       string `json:"b"`
	AHash   string `json:"aHash"`
	BHash   string `json:"bHash"` // HEAD's commit when B is the working folder
	Mode    Mode   `json:"mode"`
	Base    string `json:"base"`    // where they went apart ("" when they share no history)
	Working bool   `json:"working"` // B is the working folder
	Same    bool   `json:"same"`    // nothing differs

	OnlyA      []Commit `json:"onlyA"` // on A, not on B (git log B..A)
	OnlyACount int      `json:"onlyACount"`
	OnlyB      []Commit `json:"onlyB"` // on B, not on A (git log A..B)
	OnlyBCount int      `json:"onlyBCount"`

	Files     []FileChange `json:"files"`
	MoreFiles int          `json:"moreFiles"` // changed files left out after MaxFiles
	Added     int          `json:"added"`
	Deleted   int          `json:"deleted"`
	Summary   string       `json:"summary"`
}

// Diff is one file's change between the two sides.
type Diff struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Mode      Mode   `json:"mode"`
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"` // cut at MaxDiffBytes
}

// IsBinaryDiff reports whether git showed no text for a file.
func IsBinaryDiff(text string) bool {
	return strings.Contains(text, "\nBinary files ") || strings.HasPrefix(text, "Binary files ") ||
		strings.Contains(text, "\nGIT binary patch")
}

var statusWords = map[byte]string{'A': "added", 'M': "modified", 'D': "deleted", 'R': "renamed", 'C': "copied", 'T': "type changed"}

// ParseNameStatus reads `git diff --name-status -z`: "M\0path\0",
// "R100\0old\0new\0".
func ParseNameStatus(out string) []FileChange {
	f := strings.Split(out, "\x00")
	var files []FileChange
	for i := 0; i < len(f); i++ {
		code := f[i]
		if code == "" {
			continue
		}
		word, ok := statusWords[code[0]]
		if !ok {
			word = "modified"
		}
		switch code[0] {
		case 'R', 'C':
			if i+2 >= len(f) || f[i+2] == "" {
				return files
			}
			files = append(files, FileChange{Path: f[i+2], OldPath: f[i+1], Status: word})
			i += 2
		default:
			if i+1 >= len(f) {
				return files
			}
			files = append(files, FileChange{Path: f[i+1], Status: word})
			i++
		}
	}
	return files
}

// ParseNumstat reads `git diff --numstat -z` into line counts by path (the
// new path for a rename): "1\t2\tpath\0" and "1\t2\t\0old\0new\0".
func ParseNumstat(out string) map[string]FileChange {
	counts := map[string]FileChange{}
	f := strings.Split(out, "\x00")
	for i := 0; i < len(f); i++ {
		p := strings.SplitN(f[i], "\t", 3)
		if len(p) != 3 {
			continue
		}
		path := p[2]
		if path == "" { // a rename: the two paths follow
			if i+2 >= len(f) {
				break
			}
			path = f[i+2]
			i += 2
		}
		fc := FileChange{Path: path, Binary: p[0] == "-" && p[1] == "-"}
		fc.Added, _ = strconv.Atoi(p[0])
		fc.Deleted, _ = strconv.Atoi(p[1])
		counts[path] = fc
	}
	return counts
}

// JoinChanges puts the line counts on the files, in git's order.
func JoinChanges(files []FileChange, counts map[string]FileChange) []FileChange {
	for i, fc := range files {
		c := counts[fc.Path]
		files[i].Added, files[i].Deleted, files[i].Binary = c.Added, c.Deleted, c.Binary
	}
	return files
}

// Finish keeps MaxFiles files, adds up the lines and writes the summary.
func (c *Compared) Finish() {
	if len(c.Files) > MaxFiles {
		c.MoreFiles = len(c.Files) - MaxFiles
		c.Files = c.Files[:MaxFiles]
	}
	c.Added, c.Deleted = 0, 0
	for _, f := range c.Files {
		c.Added += f.Added
		c.Deleted += f.Deleted
	}
	if c.Files == nil {
		c.Files = []FileChange{}
	}
	if c.OnlyA == nil {
		c.OnlyA = []Commit{}
	}
	if c.OnlyB == nil {
		c.OnlyB = []Commit{}
	}
	c.Same = len(c.Files) == 0 && c.MoreFiles == 0 && c.OnlyACount == 0 && c.OnlyBCount == 0
	c.Summary = c.describe()
}

func (c Compared) describe() string {
	b := c.B
	if c.Working {
		b = "your working folder"
	}
	if c.Same {
		return fmt.Sprintf("%s and %s are the same.", c.A, b)
	}
	var parts []string
	if c.OnlyACount > 0 {
		parts = append(parts, fmt.Sprintf("%s has %s that %s doesn't", c.A, plural(c.OnlyACount, "commit"), b))
	}
	if c.OnlyBCount > 0 {
		parts = append(parts, fmt.Sprintf("%s has %s that %s doesn't", b, plural(c.OnlyBCount, "commit"), c.A))
	}
	s := strings.Join(parts, "; ")
	if s == "" {
		s = c.A + " and " + b + " have the same commits"
	}
	files := plural(len(c.Files)+c.MoreFiles, "file")
	switch {
	case len(c.Files)+c.MoreFiles == 0:
		return s + ", and the same files."
	case c.Mode == Direct:
		return fmt.Sprintf("%s. The two differ in %s (+%d -%d).", s, files, c.Added, c.Deleted)
	default:
		return fmt.Sprintf("%s. Since they went apart, %s changed %s (+%d -%d).", s, b, files, c.Added, c.Deleted)
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Git is everything the use cases need from git.
type Git interface {
	// Head is where the repo is: branch, commit, uncommitted changes and
	// the operation in progress.
	Head(gitDir string) (refs.Head, error)
	// Resolve returns the commit a branch, tag or commit name points at.
	Resolve(gitDir, rev string) (string, error)
	// MergeBase returns where two commits went apart, or "".
	MergeBase(gitDir, a, b string) string
	// Commits lists the commits on tip and not on not, newest first, at
	// most limit of them, and how many there are in all.
	Commits(gitDir, tip, not string, limit int) ([]Commit, int, error)
	// Changes lists the files that differ between from and to; with to ""
	// the second side is the working folder (tracked files).
	Changes(gitDir, from, to string) ([]FileChange, error)
	// FileDiff is the unified diff of one file between from and to.
	FileDiff(gitDir, from, to, path string) (string, error)
	// Info reads one commit.
	Info(gitDir, hash string) (Commit, error)

	ReflogGit
	BisectGit
}
