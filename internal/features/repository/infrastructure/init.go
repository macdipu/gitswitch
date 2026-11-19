package infrastructure

import (
	"fmt"
	"os"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// Initializer runs git init and git --version. It implements the
// repository domain's Initializer.
type Initializer struct {
	Git *gitinfra.Runner
}

// Init makes dir (when needed) and runs git init in it.
func (i *Initializer) Init(dir, branch string) error {
	args := []string{"init", "-q"}
	if branch != "" {
		args = append(args, "--initial-branch="+branch)
	}
	if i.Git != nil && i.Git.DryRun {
		out := i.Git.Out
		if out == nil {
			out = os.Stdout
		}
		fmt.Fprintln(out, "[dry-run] git -C "+dir+" "+gitinfra.ShellQuote(args))
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_, err := i.Git.Run(append([]string{"-C", dir}, args...)...)
	return err
}

// Version is the output of git --version.
func (i *Initializer) Version() (string, error) { return i.Git.Run("--version") }
