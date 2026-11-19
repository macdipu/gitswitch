// Package infrastructure merges, continues, skips and aborts with git, and
// reads and resolves conflicted files. Import it as mergeinfra.
package infrastructure

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Git implements the merge domain's Git.
type Git struct {
	Git *gitinfra.Runner
}

func (g *Git) Head(gitDir string) (refs.Head, error) {
	return (&refsinfra.Git{Git: g.Git}).Head(gitDir)
}

func (g *Git) Resolve(gitDir, rev string) (string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil || out == "" {
		return "", errors.New("there is no branch, tag or commit called " + rev)
	}
	return out, nil
}

func (g *Git) MergeBase(gitDir, a, b string) string {
	out, _ := g.Git.Run("--git-dir="+gitDir, "merge-base", a, b)
	return out
}

func (g *Git) Commits(gitDir, tip, not string, limit int) ([]merge.CommitInfo, int, error) {
	n, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", tip, "--not", not, "--")
	if err != nil {
		return nil, 0, err
	}
	count, _ := strconv.Atoi(n)
	if count == 0 {
		return nil, 0, nil
	}
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "--no-show-signature", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%h%x1f%an%x1f%s", tip, "--not", not, "--")
	if err != nil {
		return nil, count, err
	}
	var cs []merge.CommitInfo
	for _, line := range strings.Split(out, "\n") {
		if f := strings.SplitN(line, "\x1f", 4); len(f) == 4 {
			cs = append(cs, merge.CommitInfo{Hash: f[0], Short: f[1], Author: f[2], Subject: f[3]})
		}
	}
	return cs, count, nil
}

func (g *Git) Changed(gitDir, from, to string) ([]string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "diff", "--name-only", "--no-renames", "-z", from, to, "--")
	if err != nil {
		return nil, err
	}
	return nulList(out), nil
}

// Predict runs `git merge-tree --write-tree` (git 2.38+), which merges in
// memory: exit 0 is clean, exit 1 lists the conflicted files after the
// tree; anything else means this git does not know the option.
func (g *Git) Predict(gitDir, ours, theirs string) ([]string, bool) {
	out, err := g.Git.Run("--git-dir="+gitDir, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs)
	switch gitinfra.ExitCode(err) {
	case -1:
		if err != nil {
			return nil, false
		}
		return nil, true // exit 0
	case 1:
		files := nulList(out)
		if len(files) > 0 {
			files = files[1:] // the tree it wrote
		}
		return dedupe(files), true
	}
	return nil, false
}

func (g *Git) Merge(wt, rev string, mode merge.Mode) error {
	args := []string{"merge", "--no-edit"}
	switch mode {
	case merge.FastForward:
		args = append(args, "--ff-only")
	case merge.Commit:
		args = append(args, "--no-ff")
	case merge.Squash:
		args = append(args, "--squash")
	}
	return g.Git.Change(wt, append(args, rev)...)
}

func (g *Git) ResetMerge(wt string) error { return g.Git.Change(wt, "reset", "-q", "--merge") }

func (g *Git) Op(gitDir string) string {
	op := gitinfra.InProgress(gitDir)
	if op == merge.OpCherryPick && !exists(filepath.Join(gitDir, "CHERRY_PICK_HEAD")) {
		// A stopped series: the sequencer's next step says which.
		if b, err := os.ReadFile(filepath.Join(gitDir, "sequencer", "todo")); err == nil {
			if f := strings.Fields(string(b)); len(f) > 0 && (f[0] == "revert" || f[0] == "r") {
				return merge.OpRevert
			}
		}
	}
	return op
}

func (g *Git) Merging(gitDir string) (string, string) {
	hash, _ := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", "MERGE_HEAD^{commit}")
	msg, _ := os.ReadFile(filepath.Join(gitDir, "MERGE_MSG"))
	return hash, merge.MergedName(string(msg))
}

func (g *Git) Conflicted(gitDir string) ([]string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	out, err := g.Git.Run("--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	return dedupe(nulList(out)), nil
}

func (g *Git) Run(wt, op, action string) error {
	return g.Git.Change(wt, op, "--"+action)
}

func (g *Git) Stage(gitDir, path string, n int) ([]byte, bool, error) {
	spec := ":" + strconv.Itoa(n) + ":" + path
	if _, err := g.Git.Run("--git-dir="+gitDir, "cat-file", "-e", spec); err != nil {
		return nil, false, nil
	}
	b, err := g.raw("--git-dir="+gitDir, "cat-file", "blob", spec)
	return b, err == nil, err
}

// raw runs git and returns its standard output untouched (Run trims the
// last newline, which a file's content must keep).
func (g *Git) raw(args ...string) ([]byte, error) {
	cmd := exec.Command(gitinfra.Program(), args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	process.HideWindow(cmd)
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func (g *Git) ReadFile(wt, path string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(path)))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (g *Git) WriteFile(wt, path string, data []byte) error {
	p := filepath.Join(wt, filepath.FromSlash(path))
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, mode)
}

func (g *Git) Take(wt, path string, side merge.Side) error {
	n := 2
	if side == merge.Theirs {
		n = 3
	}
	gitDir := g.Git.GitDirFor(wt)
	if _, ok, _ := g.Stage(gitDir, path, n); !ok {
		// That side deleted the file: taking it deletes it.
		return g.Git.Change(wt, "--literal-pathspecs", "rm", "--quiet", "--", path)
	}
	if err := g.Git.Change(wt, "--literal-pathspecs", "checkout", "--"+string(side), "--", path); err != nil {
		return err
	}
	return g.Add(wt, path)
}

func (g *Git) Add(wt, path string) error {
	return g.Git.Change(wt, "--literal-pathspecs", "add", "--all", "--", path)
}

func nulList(out string) []string {
	var l []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.Trim(f, "\r\n"); f != "" {
			l = append(l, f)
		}
	}
	return l
}

func dedupe(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range l {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
