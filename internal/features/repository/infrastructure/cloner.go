package infrastructure

import (
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// Cloner runs git clone. It implements the repository domain's Cloner.
type Cloner struct {
	Git *gitinfra.Runner
}

// Clone runs from the parent folder, like a person typing `git clone`, so
// the ssh hook can find the new repo while it is being cloned.
func (c *Cloner) Clone(parent, url, folder, accountID string, progress func(string)) error {
	args := []string{"clone", "--progress"}
	if accountID != "" {
		args = append(args, "-c", "gitswitch.account="+accountID)
	}
	args = append(args, "--", url, folder)
	return c.Git.Stream(parent, progress, args...)
}
