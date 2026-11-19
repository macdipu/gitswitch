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

// Output runs git and returns its standard output untouched, trailing
// newlines and carriage returns included: a diff that is turned back into a
// patch must keep every byte of its last line.
func (r *Runner) Output(args ...string) (string, error) {
	return r.RunIn("", nil, "", true, args...)
}

// RunIn runs git with extra environment variables (KEY=value) and text on
// standard input; dir "" is the current folder. The output is trimmed like
// Run's unless raw. A failure carries git's message and exit code.
func (r *Runner) RunIn(dir string, env []string, stdin string, raw bool, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command(Program(), args...)
	cmd.Env = append(append(os.Environ(), "GIT_EDITOR=:", "GIT_TERMINAL_PROMPT=0"), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	process.HideWindow(cmd)
	err := cmd.Run()
	text := out.String()
	if !raw {
		text = strings.TrimRight(text, "\r\n")
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return text, &Error{Msg: msg, Code: code}
	}
	return text, nil
}
