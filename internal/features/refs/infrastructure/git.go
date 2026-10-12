// Package infrastructure reads and moves branches with git. Import it as
// refsinfra.
package infrastructure

import (
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// Git implements the refs domain's Refs.
type Git struct {
	Git *gitinfra.Runner
}

func (g *Git) Head(gitDir string) (refs.Head, error) {
	h := refs.Head{Busy: gitinfra.InProgress(gitDir)}
	// The branch name works in a repo without commits too.
	if b, err := g.Git.Run("--git-dir="+gitDir, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		h.Branch = b
	}
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "-1", "--format=%H%x1f%s", "HEAD", "--")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") || strings.Contains(err.Error(), "unknown revision") {
			return h, nil
		}
		return h, err
	}
	h.Hash, h.Subject, _ = strings.Cut(out, "\x1f")
	if len(h.Hash) >= 7 {
		h.Short = h.Hash[:7]
	}
	if h.Branch == "" {
		return h, nil
	}
	if up, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		h.Upstream = up
		if c, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
			if f := strings.Fields(c); len(f) == 2 {
				h.Ahead, _ = strconv.Atoi(f[0])
				h.Behind, _ = strconv.Atoi(f[1])
			}
		}
	} else {
		h.Gone = g.goneUpstream(gitDir, h.Branch)
	}
	return h, nil
}

// goneUpstream is the remote branch a branch is set to pull from when git
// has no copy of it, e.g. origin/feat; "" when none is set.
func (g *Git) goneUpstream(gitDir, branch string) string {
	r, _ := g.Git.Run("--git-dir="+gitDir, "config", "--get", "branch."+branch+".remote")
	if r == "" {
		return ""
	}
	// %(upstream) comes from the config, so it is known even when gone.
	if up, _ := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch); up != "" {
		return up
	}
	m, _ := g.Git.Run("--git-dir="+gitDir, "config", "--get", "branch."+branch+".merge")
	if m == "" {
		return r
	}
	return r + "/" + strings.TrimPrefix(m, "refs/heads/")
}

func (g *Git) Branches(gitDir string) ([]refs.Branch, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--format=%(refname)%1f%(HEAD)%1f%(upstream:short)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var local, remote []refs.Branch
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) < 3 {
			continue
		}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			local = append(local, refs.Branch{Name: strings.TrimPrefix(f[0], "refs/heads/"), Current: f[1] == "*", Upstream: f[2]})
		case strings.HasPrefix(f[0], "refs/remotes/") && !strings.HasSuffix(f[0], "/HEAD"):
			remote = append(remote, refs.Branch{Name: strings.TrimPrefix(f[0], "refs/remotes/"), Remote: true})
		}
	}
	return append(local, remote...), nil
}

func (g *Git) Switch(wt, branch string) error {
	return g.Git.Change(wt, "switch", "--no-guess", branch)
}

func (g *Git) Track(wt, local, remoteBranch string) error {
	return g.Git.Change(wt, "switch", "--no-guess", "--create", local, "--track", remoteBranch)
}

func (g *Git) Pull(wt string, progress func(string)) error {
	return g.Git.Stream(wt, progress, "pull", "--ff-only", "--progress")
}
