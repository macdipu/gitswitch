package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxReflog is how many entries one read looks at when only the lost ones
// are wanted.
const MaxReflog = 2000

// Entry is one line of the reflog.
type Entry struct {
	Index   int    `json:"index"`   // 0 is the newest
	Ref     string `json:"ref"`     // HEAD@{3}
	Hash    string `json:"hash"`    // the commit HEAD pointed at after this step
	Short   string `json:"short"`   // abbreviated hash
	Action  string `json:"action"`  // commit, checkout, merge, rebase, reset, pull, ...
	What    string `json:"what"`    // the action in plain words
	Message string `json:"message"` // git's text: "moving from a to b", "fix login"
	Subject string `json:"subject"` // the commit's own subject
	Time    int64  `json:"time"`    // Unix seconds
	// Lost: the commit is on no branch, tag or remote branch; only the
	// reflog still leads to it. (git removes it for good after some weeks.)
	Lost bool `json:"lost"`
}

// Reflogged is a page of the reflog.
type Reflogged struct {
	Ref     string  `json:"ref"` // HEAD, or the branch
	Entries []Entry `json:"entries"`
	More    bool    `json:"more"` // there are older entries
	Next    int     `json:"next"` // the skip that reads the next page
}

// ParseEntry reads one line of
// `git reflog show --format=%H%x1f%gd%x1f%gs%x1f%ct%x1f%s`.
func ParseEntry(line string) (Entry, bool) {
	f := strings.SplitN(line, "\x1f", 5)
	if len(f) < 5 || len(f[0]) < 7 {
		return Entry{}, false
	}
	e := Entry{Hash: f[0], Short: f[0][:7], Ref: f[1], Subject: f[4]}
	if i := strings.LastIndex(f[1], "{"); i >= 0 && strings.HasSuffix(f[1], "}") {
		e.Index, _ = strconv.Atoi(f[1][i+1 : len(f[1])-1])
	}
	e.Time, _ = strconv.ParseInt(f[3], 10, 64)
	e.Action, e.Message = ParseAction(f[2])
	e.What = What(e.Action, f[2])
	return e, true
}

// ParseAction splits git's reflog text ("commit (amend): fix", "checkout:
// moving from a to b", "merge dev: Fast-forward") into the action and the
// rest. The action is one lower-case word.
func ParseAction(gs string) (action, message string) {
	head, rest, ok := strings.Cut(gs, ": ")
	if !ok {
		head, rest = gs, ""
		if strings.HasSuffix(gs, ":") {
			head = strings.TrimSuffix(gs, ":")
		}
	}
	if i := strings.IndexAny(head, " ("); i >= 0 {
		head = head[:i] // "merge dev", "commit (amend)"
	}
	return strings.ToLower(head), rest
}

// What says an action in plain words, using the full text for the
// variants ("commit (amend)", "rebase (finish)").
func What(action, gs string) string {
	switch action {
	case "commit":
		switch {
		case strings.HasPrefix(gs, "commit (amend)"):
			return "Amended a commit"
		case strings.HasPrefix(gs, "commit (initial)"):
			return "Made the first commit"
		case strings.HasPrefix(gs, "commit (merge)"):
			return "Made a merge commit"
		}
		return "Committed"
	case "checkout":
		return "Switched to another branch or commit"
	case "merge":
		return "Merged"
	case "rebase":
		return "Rebased"
	case "reset":
		return "Moved the branch (reset)"
	case "pull":
		return "Pulled"
	case "clone":
		return "Cloned"
	case "branch":
		return "Created a branch"
	case "cherry-pick":
		return "Cherry-picked"
	case "revert":
		return "Reverted"
	case "fetch":
		return "Fetched"
	case "update", "update by push":
		return "Updated"
	}
	if action == "" {
		return "Changed"
	}
	return strings.ToUpper(action[:1]) + action[1:]
}

// CheckReflogRef refuses a branch name git would read as an option; "" and
// HEAD mean HEAD.
func CheckReflogRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "HEAD" {
		return "HEAD", nil
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n") {
		return "", fmt.Errorf("%q is not a branch name", ref)
	}
	return ref, nil
}

// Recovered is a branch made at a reflog entry.
type Recovered struct {
	Name    string `json:"name"`
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Lost    bool   `json:"lost"` // the commit was on no branch before
	Message string `json:"message"`
}

// ReflogGit is the part of Git that reads the reflog and makes a branch.
type ReflogGit interface {
	// Reflog reads limit entries of ref's reflog after skip newer ones.
	// Lost is not set.
	Reflog(gitDir, ref string, skip, limit int) ([]Entry, error)
	// Unreachable says which of the commits are on no branch, tag or
	// remote branch.
	Unreachable(gitDir string, hashes []string) map[string]bool
	// CheckName refuses a name git doesn't accept for a branch.
	CheckName(name string) error
	// BranchExists reports whether a local branch has the name.
	BranchExists(gitDir, name string) bool
	// CreateBranch makes a branch at a commit without switching to it.
	CreateBranch(gitDir, name, hash string) error
}
