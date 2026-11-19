// Package infrastructure compares, reads the reflog and bisects with git.
// Import it as inspectinfra.
package infrastructure

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
)

// Git implements the inspect domain's Git.
type Git struct {
	Git *gitinfra.Runner
}

const commitFormat = "--format=%H%x1f%h%x1f%an%x1f%at%x1f%s"

func parseCommit(line string) (inspect.Commit, bool) {
	f := strings.SplitN(line, "\x1f", 5)
	if len(f) < 5 {
		return inspect.Commit{}, false
	}
	t, _ := strconv.ParseInt(f[3], 10, 64)
	return inspect.Commit{Hash: f[0], Short: f[1], Author: f[2], Time: t, Subject: f[4]}, true
}

func (g *Git) Head(gitDir string) (refs.Head, error) {
	h, err := (&refsinfra.Git{Git: g.Git}).Head(gitDir)
	if err != nil && strings.Contains(err.Error(), "bad revision") {
		// A repo without commits: git words it differently across versions.
		return refs.Head{Busy: gitinfra.InProgress(gitDir)}, nil
	}
	return h, err
}

func (g *Git) Resolve(gitDir, rev string) (string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil || out == "" {
		return "", errors.New("there is no branch, tag or commit called " + rev)
	}
	return out, nil
}

func (g *Git) MergeBase(gitDir, a, b string) string {
	out, _ := g.Git.Run("--git-dir="+gitDir, "merge-base", a, b)
	return out
}

func (g *Git) Commits(gitDir, tip, not string, limit int) ([]inspect.Commit, int, error) {
	n, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", tip, "--not", not, "--")
	if err != nil {
		return nil, 0, err
	}
	count, _ := strconv.Atoi(n)
	if count == 0 {
		return nil, 0, nil
	}
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "--no-show-signature", "-n", strconv.Itoa(limit),
		commitFormat, tip, "--not", not, "--")
	if err != nil {
		return nil, count, err
	}
	var cs []inspect.Commit
	for _, line := range strings.Split(out, "\n") {
		if c, ok := parseCommit(line); ok {
			cs = append(cs, c)
		}
	}
	return cs, count, nil
}

func (g *Git) Info(gitDir, hash string) (inspect.Commit, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "-1", "--no-show-signature", commitFormat, hash, "--")
	if err != nil {
		return inspect.Commit{}, err
	}
	c, ok := parseCommit(out)
	if !ok {
		return c, errors.New("no commit " + hash)
	}
	return c, nil
}

// diffArgs is the diff of from against to ("" is the working folder),
// with the options every call needs.
func (g *Git) diffArgs(gitDir, from, to string, opts ...string) []string {
	args := []string{"--git-dir=" + gitDir}
	if to == "" {
		// A diff against the working folder needs one.
		args = append(args, "--work-tree="+g.Git.WorktreeOf(gitDir))
	}
	args = append(args, "diff", "--no-color", "--no-ext-diff")
	args = append(args, opts...)
	args = append(args, "--end-of-options", from)
	if to != "" {
		args = append(args, to)
	}
	return args
}

func (g *Git) Changes(gitDir, from, to string) ([]inspect.FileChange, error) {
	names, err := g.Git.Run(append(g.diffArgs(gitDir, from, to, "--name-status", "-z", "-M"), "--")...)
	if err != nil {
		return nil, err
	}
	nums, err := g.Git.Run(append(g.diffArgs(gitDir, from, to, "--numstat", "-z", "-M"), "--")...)
	if err != nil {
		return nil, err
	}
	return inspect.JoinChanges(inspect.ParseNameStatus(names), inspect.ParseNumstat(nums)), nil
}

func (g *Git) FileDiff(gitDir, from, to, path string) (string, error) {
	// Both paths of a rename are needed to show it as one; a plain path
	// is taken literally.
	args := append([]string{"--literal-pathspecs"}, g.diffArgs(gitDir, from, to, "-M")...)
	args = append(args, "--", path)
	return g.Git.Output(args...)
}

// --- reflog ---

func (g *Git) Reflog(gitDir, ref string, skip, limit int) ([]inspect.Entry, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "reflog", "show", "--no-abbrev", "--skip="+strconv.Itoa(skip),
		"-n", strconv.Itoa(limit), "--format=%H%x1f%gd%x1f%gs%x1f%ct%x1f%s", "--end-of-options", ref, "--")
	if err != nil {
		if strings.Contains(err.Error(), "bad revision") || strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "ambiguous argument") {
			return nil, fmt.Errorf("there is no reflog for %s here", ref)
		}
		return nil, err
	}
	var es []inspect.Entry
	for _, line := range strings.Split(out, "\n") {
		if e, ok := inspect.ParseEntry(line); ok {
			es = append(es, e)
		}
	}
	return es, nil
}

func (g *Git) Unreachable(gitDir string, hashes []string) map[string]bool {
	lost := map[string]bool{}
	for _, h := range hashes {
		out, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--count=1", "--contains", h,
			"--format=%(refname)", "refs/heads", "refs/remotes", "refs/tags")
		if err == nil && out == "" {
			lost[h] = true
		}
	}
	return lost
}

func (g *Git) CheckName(name string) error {
	_, err := g.Git.Run("check-ref-format", "--branch", name)
	return err
}

func (g *Git) BranchExists(gitDir, name string) bool {
	_, err := g.Git.Run("--git-dir="+gitDir, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

func (g *Git) CreateBranch(gitDir, name, hash string) error {
	_, err := g.Git.Run("--git-dir="+gitDir, "branch", "--no-track", name, hash)
	return err
}

// --- bisect ---

const (
	keyBranch = "gitswitch.bisectbranch"
	keyHead   = "gitswitch.bisecthead"
)

func (g *Git) Origin(gitDir string) (string, string) {
	return g.Git.RepoGet(gitDir, keyBranch), g.Git.RepoGet(gitDir, keyHead)
}

func (g *Git) SaveOrigin(gitDir, branch, hash string) error {
	if err := g.Git.RepoSet(gitDir, keyBranch, branch); err != nil {
		return err
	}
	return g.Git.RepoSet(gitDir, keyHead, hash)
}

func (g *Git) ClearOrigin(gitDir string) {
	_ = g.Git.RepoUnset(gitDir, keyBranch)
	_ = g.Git.RepoUnset(gitDir, keyHead)
}

func (g *Git) IsAncestor(gitDir, a, b string) bool {
	_, err := g.Git.Run("--git-dir="+gitDir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// bisect runs git bisect in the working folder, in git's own words
// (LC_ALL=C) so the output can be read.
func (g *Git) bisect(wt string, args ...string) (string, error) {
	out, err := g.Git.RunIn(wt, []string{"LC_ALL=C", "LANGUAGE=C"}, "", false, append([]string{"bisect"}, args...)...)
	// Exit 2: the first bad commit was found; 3: only skipped commits are
	// left to test. Neither is a failure; the log says which.
	if c := gitinfra.ExitCode(err); c == 2 || c == 3 {
		return out, nil
	}
	return out, err
}

func (g *Git) BisectStart(wt, bad, good string) (string, error) {
	return g.bisect(wt, "start", bad, good)
}

func (g *Git) BisectMark(wt, mark string) (string, error) {
	return g.bisect(wt, mark)
}

func (g *Git) BisectReset(wt, to string) error {
	args := []string{"reset"}
	if to != "" {
		args = append(args, to)
	}
	_, err := g.bisect(wt, args...)
	return err
}

func (g *Git) BisectLog(gitDir string) string {
	out, _ := g.Git.RunIn("", []string{"LC_ALL=C"}, "", false, "--git-dir="+gitDir, "bisect", "log")
	return out
}

func (g *Git) Candidates(gitDir string) int {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", "refs/bisect/bad", "--not", "--glob=refs/bisect/good-*")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

func (g *Git) CommitFiles(gitDir, hash string, limit int) ([]string, int, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "-m", "--first-parent", "--root", hash)
	if err != nil {
		return nil, 0, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) > limit {
		return files[:limit], len(files) - limit, nil
	}
	return files, 0, nil
}
