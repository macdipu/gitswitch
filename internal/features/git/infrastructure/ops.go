package infrastructure

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Change runs a git command that changes a repo (revert, rebase, reset,
// switch) in worktree. Git never opens an editor: commit messages are
// kept as git writes them. On failure the error holds git's message.
func (r *Runner) Change(worktree string, args ...string) error {
	cmd := exec.Command(Program(), append([]string{"-C", worktree}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_EDITOR=:", "GIT_SEQUENCE_EDITOR=:", "GIT_TERMINAL_PROMPT=0")
	process.HideWindow(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// ClashFile, in a git dir, holds a smart switch whose clashing files are
// not fixed yet (refs.Clash).
const ClashFile = "GITSWITCH_SWITCH"

// InProgress names the operation git is in the middle of in a repo
// ("rebase", "merge", "cherry-pick", "revert", "bisect", or GitSwitch's
// own "smart switch"), or "".
func InProgress(gitDir string) string {
	for _, c := range []struct{ file, name string }{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"},
		{"sequencer", "cherry-pick"}, // a series of picks or reverts, stopped
		{ClashFile, remote.SmartSwitch},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, c.file)); err == nil {
			return c.name
		}
	}
	return ""
}
