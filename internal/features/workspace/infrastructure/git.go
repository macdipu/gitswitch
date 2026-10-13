// Package infrastructure makes commits with git. Import it as
// workspaceinfra.
package infrastructure

import (
	"fmt"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
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
