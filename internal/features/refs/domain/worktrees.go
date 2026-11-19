package domain

import "strings"

// Worktree is one working folder of a repo: the main one, or a linked one
// made with `git worktree add`. Linked worktrees share the repo's history,
// remotes and settings (the account too), and each has its own branch,
// index and uncommitted changes.
type Worktree struct {
	Path        string   `json:"path"`
	Head        string   `json:"head"`
	Short       string   `json:"short"`
	Branch      string   `json:"branch"` // "" when detached or bare
	Detached    bool     `json:"detached"`
	Bare        bool     `json:"bare"`
	Main        bool     `json:"main"` // the first one git lists: the repo's own folder
	Locked      bool     `json:"locked"`
	LockReason  string   `json:"lockReason"`
	Prunable    bool     `json:"prunable"` // its folder is gone; Prune forgets it
	PruneReason string   `json:"pruneReason"`
	Changes     *Changes `json:"changes"` // nil when unknown (bare, folder gone, or too slow)
	Account     string   `json:"account"` // the account the repo uses; the same for every worktree
}

// ParseWorktrees reads `git worktree list --porcelain`: blocks of
// "worktree <path>", "HEAD <hash>", "branch refs/heads/x" | "detached" |
// "bare", "locked [reason]", "prunable [reason]", split by blank lines.
func ParseWorktrees(out string) []Worktree {
	var all []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			all = append(all, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
			cur.Short = val
			if len(val) > 7 {
				cur.Short = val[:7]
			}
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		case "locked":
			cur.Locked, cur.LockReason = true, val
		case "prunable":
			cur.Prunable, cur.PruneReason = true, val
		}
	}
	flush()
	if len(all) > 0 {
		all[0].Main = true
	}
	return all
}

// WorktreeAddOptions says what a new worktree checks out: an existing
// Branch; a NewBranch started at From (HEAD when empty); or Detach, a
// commit (or branch or tag) without a branch.
type WorktreeAddOptions struct {
	Branch    string `json:"branch"`
	NewBranch string `json:"newBranch"`
	From      string `json:"from"`
	Detach    string `json:"detach"`
}

// WorktreeAdded is the result of adding a worktree.
type WorktreeAdded struct {
	Path     string   `json:"path"`
	Branch   string   `json:"branch"`
	Head     string   `json:"head"`
	Account  string   `json:"account"`
	Warnings []string `json:"warnings"`
}

// WorktreeGit is what worktrees need from git, beside the Refs methods.
// refsinfra.Git implements it; the service checks for it at run time so
// existing fakes of Refs keep compiling.
type WorktreeGit interface {
	// Worktrees lists the repo's worktrees, main first, with the uncommitted
	// counts of the checked-out ones (those that answered in time).
	Worktrees(gitDir string) ([]Worktree, error)
	// WorktreeAdd makes the worktree. An error means nothing was made.
	WorktreeAdd(gitDir, path string, o WorktreeAddOptions) error
	// WorktreeRemove deletes the worktree's folder. force also removes one
	// with uncommitted changes.
	WorktreeRemove(gitDir, path string, force bool) error
	// WorktreePrune forgets worktrees whose folder is gone.
	WorktreePrune(gitDir string) error
	WorktreeLock(gitDir, path, reason string) error
	WorktreeUnlock(gitDir, path string) error
	// WorktreeRepair fixes the links between the repo and moved worktrees.
	WorktreeRepair(gitDir string, paths ...string) (string, error)
}
