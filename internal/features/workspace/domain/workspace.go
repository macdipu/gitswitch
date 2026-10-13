// Package domain describes making a commit from the working folder, and
// declares what it needs from git (ports). It is pure. Import it as
// workspace.
package domain

import (
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
