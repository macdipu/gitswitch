package infrastructure

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// worktreeStatusWait is how long listing waits for the uncommitted counts;
// a worktree that is slower shows none rather than hanging the list.
const worktreeStatusWait = 4 * time.Second

// maxStatusWorktrees limits how many worktrees get their counts read.
const maxStatusWorktrees = 24

// commonDir is the repo's shared git folder: gitDir itself, or for a linked
// worktree's git dir (<repo>/.git/worktrees/<name>) the repo's.
func (g *Git) commonDir(gitDir string) string {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "--git-common-dir")
	if err != nil || out == "" {
		return gitDir
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(gitDir, out)
	}
	return filepath.Clean(out)
}

// wtArgs runs a `git worktree` command from the repo's main folder (or the
// bare repo itself).
func (g *Git) wtArgs(gitDir string, args ...string) []string {
	cd := g.commonDir(gitDir)
	wt := g.Git.WorktreeOf(cd)
	return append([]string{"-C", wt, "--git-dir=" + cd, "worktree"}, args...)
}

// Worktrees lists the repo's worktrees (main first) with the uncommitted
// counts of the ones whose folder is there.
func (g *Git) Worktrees(gitDir string) ([]refs.Worktree, error) {
	out, err := g.Git.Run(g.wtArgs(gitDir, "list", "--porcelain")...)
	if err != nil {
		return nil, err
	}
	all := refs.ParseWorktrees(out)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	var mu sync.Mutex
	for i := range all {
		w := all[i]
		if w.Bare || w.Prunable || i >= maxStatusWorktrees {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			c := g.worktreeChanges(all[i].Path)
			mu.Lock()
			all[i].Changes = c
			mu.Unlock()
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(worktreeStatusWait):
	}
	mu.Lock()
	defer mu.Unlock()
	res := make([]refs.Worktree, len(all))
	copy(res, all)
	return res, nil
}

// worktreeChanges counts the uncommitted changes of one worktree folder;
// nil when git can't say. --no-optional-locks: only reading.
func (g *Git) worktreeChanges(path string) *refs.Changes {
	out, err := g.Git.Run("--no-optional-locks", "-C", path, "status", "--porcelain=v2", "-z", "--untracked-files=normal")
	if err != nil {
		return nil
	}
	c := parseStatus(out)
	return &c
}

// WorktreeAdd runs `git worktree add`.
func (g *Git) WorktreeAdd(gitDir, path string, o refs.WorktreeAddOptions) error {
	args := []string{"add"}
	switch {
	case o.NewBranch != "":
		args = append(args, "-b", o.NewBranch, path)
		if o.From != "" {
			args = append(args, o.From)
		}
	case o.Detach != "":
		args = append(args, "--detach", path, o.Detach)
	default:
		args = append(args, path, o.Branch)
	}
	_, err := g.Git.Run(g.wtArgs(gitDir, args...)...)
	return err
}

// WorktreeRemove runs `git worktree remove`; force also drops uncommitted
// changes. A locked worktree is never forced here (the service refuses it).
func (g *Git) WorktreeRemove(gitDir, path string, force bool) error {
	args := []string{"remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := g.Git.Run(g.wtArgs(gitDir, append(args, path)...)...)
	return err
}

// WorktreePrune runs `git worktree prune`.
func (g *Git) WorktreePrune(gitDir string) error {
	_, err := g.Git.Run(g.wtArgs(gitDir, "prune", "-v")...)
	return err
}

func (g *Git) WorktreeLock(gitDir, path, reason string) error {
	args := []string{"lock"}
	if strings.TrimSpace(reason) != "" {
		args = append(args, "--reason", reason)
	}
	_, err := g.Git.Run(g.wtArgs(gitDir, append(args, path)...)...)
	return err
}

func (g *Git) WorktreeUnlock(gitDir, path string) error {
	_, err := g.Git.Run(g.wtArgs(gitDir, "unlock", path)...)
	return err
}

// WorktreeRepair runs `git worktree repair`.
func (g *Git) WorktreeRepair(gitDir string, paths ...string) (string, error) {
	return g.Git.Run(g.wtArgs(gitDir, append([]string{"repair"}, paths...)...)...)
}
