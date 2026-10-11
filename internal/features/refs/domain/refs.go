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
	Ahead    int    `json:"ahead"`    // commits not pushed to the upstream
	Behind   int    `json:"behind"`   // commits on the upstream not pulled yet
	Busy     string `json:"busy"`     // an operation git is in the middle of, e.g. "rebase"
}

// Describe is a one-line summary, e.g. "main → origin/main, 2 to push · HEAD 1a2b3c4 Fix login".
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
	} else if h.Branch != "" {
		b.WriteString(" (not on a remote)")
	}
	if h.Hash != "" {
		b.WriteString(" · HEAD " + h.Short + " " + h.Subject)
	} else {
		b.WriteString(" · no commits yet")
	}
	if h.Busy != "" {
		b.WriteString(" · " + h.Busy + " in progress")
	}
	return b.String()
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
	// Head reads where the repo is.
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
}
