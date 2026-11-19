// Package infrastructure makes commits with git. Import it as
// workspaceinfra.
package infrastructure

import (
	"errors"
	"fmt"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Git implements the workspace domain's Git.
type Git struct {
	Git *gitinfra.Runner
}

func (g *Git) Head(gitDir string) (refs.Head, error) {
	return (&refsinfra.Git{Git: g.Git}).Head(gitDir)
}

func (g *Git) Commit(wt, message string, all bool) error {
	if all {
		if err := g.Git.Change(wt, "add", "--all"); err != nil {
			return err
		}
	}
	// git runs the repo's hooks and signs as its config says.
	if err := g.Git.Change(wt, "commit", "--quiet", "--cleanup=strip", "-m", message); err != nil {
		if all {
			return fmt.Errorf("%v\n\n(every change is staged now, ready to commit again)", err)
		}
		return err
	}
	return nil
}

func (g *Git) Last(gitDir string) (workspace.Last, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "-1", "--no-show-signature", "--format=%H%x00%h%x00%P%x00%s%x00%B", "HEAD", "--")
	if err != nil {
		return workspace.Last{}, err
	}
	f := strings.SplitN(out, "\x00", 5)
	if len(f) != 5 {
		return workspace.Last{}, errors.New("could not read the last commit")
	}
	l := workspace.Last{Hash: f[0], Short: f[1], Parents: len(strings.Fields(f[2])), Subject: f[3], Message: strings.TrimSpace(f[4])}
	if names, err := g.Git.Run("--git-dir="+gitDir, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", "-z", "HEAD"); err == nil {
		for _, n := range strings.Split(names, "\x00") {
			if n != "" {
				l.Files++
			}
		}
	}
	on, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", l.Hash, "refs/remotes")
	if err != nil {
		return l, err
	}
	l.Pushed = on != ""
	return l, nil
}

func (g *Git) Amend(wt, message string, all bool) error {
	if all {
		if err := g.Git.Change(wt, "add", "--all"); err != nil {
			return err
		}
	}
	args := []string{"commit", "--quiet", "--amend", "--cleanup=strip"}
	if message == "" {
		args = append(args, "--no-edit")
	} else {
		args = append(args, "-m", message)
	}
	if err := g.Git.Change(wt, args...); err != nil {
		if all {
			return fmt.Errorf("%v\n\n(every change is staged now, ready to amend again)", err)
		}
		return err
	}
	return nil
}
