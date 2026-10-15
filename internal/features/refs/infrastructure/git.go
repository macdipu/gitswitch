// Package infrastructure reads and moves branches with git. Import it as
// refsinfra.
package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
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
	h := refs.Head{Busy: gitinfra.InProgress(gitDir), Changes: g.changes(gitDir)}
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
	if ps, err := g.parked(gitDir, false); err == nil {
		h.Parked = len(refs.ParkedOn(ps, h.Branch))
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

// changes counts the uncommitted changes in the repo's working folder. A
// repo without one (bare) has none. --no-optional-locks: only reading, so
// git must not take the index lock an editor or IDE may be using.
func (g *Git) changes(gitDir string) refs.Changes {
	wt := g.Git.WorktreeOf(gitDir)
	if wt == gitDir { // bare: git would list the git dir itself as new files
		return refs.Changes{}
	}
	out, err := g.Git.Run("--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"status", "--porcelain=v2", "-z", "--untracked-files=normal")
	if err != nil {
		return refs.Changes{}
	}
	return parseStatus(out)
}

// parseStatus counts the entries of `git status --porcelain=v2 -z`:
// "1 XY …" changed, "2 XY … path\0orig" renamed or copied, "u XY …"
// unmerged, "? path" untracked. X is the staged state, Y the working
// folder's; "." means unchanged.
func parseStatus(out string) refs.Changes {
	var c refs.Changes
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '1', '2':
			if f[0] == '2' {
				i++ // the original path follows as its own field
			}
			if len(f) < 4 {
				continue
			}
			if f[2] != '.' {
				c.Staged++
			}
			if f[3] != '.' {
				c.Changed++
			}
		case 'u':
			c.Conflicts++
		case '?':
			c.New++
		}
	}
	return c
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
	out, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--format=%(refname)%1f%(HEAD)%1f%(upstream:short)%1f%(objectname)%1f%(upstream:track)%1f%(worktreepath)",
		"refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var local, remote []refs.Branch
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x1f")
		if len(f) < 6 {
			continue
		}
		switch {
		case strings.HasPrefix(f[0], "refs/heads/"):
			b := refs.Branch{Name: strings.TrimPrefix(f[0], "refs/heads/"), Current: f[1] == "*", Upstream: f[2], Hash: f[3],
				Gone: f[4] == "[gone]", Elsewhere: f[1] != "*" && f[5] != ""}
			local = append(local, b)
		case strings.HasPrefix(f[0], "refs/remotes/") && !strings.HasSuffix(f[0], "/HEAD"):
			remote = append(remote, refs.Branch{Name: strings.TrimPrefix(f[0], "refs/remotes/"), Remote: true, Hash: f[3]})
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

func (g *Git) Create(wt, branch, commit string) error {
	args := []string{"switch", "--no-guess", "--create", branch}
	if commit != "" {
		args = append(args, commit)
	}
	return g.Git.Change(wt, args...)
}

func (g *Git) CheckName(branch string) error {
	_, err := g.Git.Run("check-ref-format", "--branch", branch)
	return err
}

func (g *Git) Commit(gitDir, rev string) (string, error) {
	return g.Git.Run("--git-dir="+gitDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

func (g *Git) Commits(gitDir, from, to string, limit int) ([]refs.Line, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "-n", strconv.Itoa(limit), "--format=%H%x1f%h%x1f%an%x1f%at%x1f%s", from+".."+to, "--")
	if err != nil {
		return nil, err
	}
	var ls []refs.Line
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\x1f", 5)
		if len(f) < 5 {
			continue
		}
		t, _ := strconv.ParseInt(f[3], 10, 64)
		ls = append(ls, refs.Line{Hash: f[0], Short: f[1], Author: f[2], Time: t, Subject: f[4]})
	}
	return ls, nil
}

func (g *Git) MergedInto(gitDir, rev string) ([]string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--merged="+rev, "--format=%(refname:lstrip=2)", "refs/heads")
	return strings.Fields(out), err
}

func (g *Git) DeleteBranch(gitDir, branch, was string) error {
	if _, err := g.Git.Run("--git-dir="+gitDir, "update-ref", "-m", "gitswitch: delete merged branch", "-d", "refs/heads/"+branch, was); err != nil {
		return err
	}
	// git branch -d does this too: a new branch of the same name must not
	// pull from the old one's remote branch. Exit 128: no such section.
	_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", "branch."+branch)
	return nil
}

func (g *Git) MergeBase(gitDir, a, b string) (string, error) {
	return g.Git.Run("--git-dir="+gitDir, "merge-base", a, b)
}

func (g *Git) MoveBranch(gitDir, branch, to, was, why string) error {
	args := []string{"--git-dir=" + gitDir, "update-ref", "-m", why}
	if to == "" {
		args = append(args, "-d", "refs/heads/"+branch, was)
	} else {
		args = append(args, "refs/heads/"+branch, to, was)
	}
	_, err := g.Git.Run(args...)
	return err
}

// prefixKey holds the prefix of new branch names, in the global config.
const prefixKey = "gitswitch.branchPrefix"

func (g *Git) BranchPrefix() string { return g.Git.GlobalGet(prefixKey) }

func (g *Git) SaveBranchPrefix(prefix string) error {
	if prefix != "" {
		_, err := g.Git.Global(prefixKey, prefix)
		return err
	}
	if _, err := g.Git.Global("--unset", prefixKey); gitinfra.ExitCode(err) != 5 { // 5: was not set
		return err
	}
	return nil
}

func (g *Git) Pull(wt string, progress func(string)) error {
	return g.Git.Stream(wt, progress, "pull", "--ff-only", "--progress")
}

func (g *Git) PullRebase(wt string, progress func(string)) error {
	// --rebase=true: your merge commits are refused before this, never
	// flattened; --no-autostash: uncommitted changes are refused before
	// this too, never stashed behind your back.
	err := g.Git.Stream(wt, progress, "-c", "rebase.autoStash=false", "pull", "--rebase=true", "--no-autostash", "--progress")
	if err != nil && gitinfra.InProgress(g.Git.GitDirFor(wt)) == "rebase" { // not when the fetch failed
		if _, aerr := g.Git.Run("-C", wt, "rebase", "--abort"); aerr != nil {
			return fmt.Errorf("%v\n\nthe rebase could not be undone either (%v); finish or abort it in a terminal: git rebase --abort", err, aerr)
		}
	}
	return err
}

func (g *Git) Tags(gitDir string) ([]string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)", "refs/tags")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (g *Git) Merges(gitDir, from, to string) int {
	out, _ := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", "--merges", from+".."+to, "--")
	n, _ := strconv.Atoi(out)
	return n
}

func (g *Git) Resolve(gitDir, rev string) string {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return out
}

func (g *Git) PushTarget(gitDir, branch string) (string, string) {
	return g.Git.RepoGet(gitDir, "branch."+branch+".remote"), g.Git.RepoGet(gitDir, "branch."+branch+".merge")
}

func (g *Git) RemoteNames(gitDir string) []string {
	out, _ := g.Git.Run("--git-dir="+gitDir, "remote")
	return strings.Fields(out)
}

func (g *Git) NotPushed(gitDir string) int {
	out, _ := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", "HEAD", "--not", "--remotes")
	n, _ := strconv.Atoi(out)
	return n
}

func (g *Git) DefaultBranch(gitDir, remote string) string {
	out, err := g.Git.Run("--git-dir="+gitDir, "symbolic-ref", "-q", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(out, remote+"/")
}

func (g *Git) Push(wt, remote, src, dst string, setUpstream bool, progress func(string)) error {
	args := []string{"push", "--progress"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	return g.Git.Stream(wt, progress, append(args, remote, src+":"+dst)...)
}

func (g *Git) Stash(wt, message string) (string, error) {
	if err := g.Git.Change(wt, "stash", "push", "-q", "--include-untracked", "-m", message); err != nil {
		return "", err
	}
	return g.Git.Run("-C", wt, "rev-parse", "-q", "--verify", "refs/stash")
}

func (g *Git) Apply(wt, stash string) ([]string, error) {
	// --index fails without changing anything when the staged changes do
	// not apply (git 2.x: "conflicts in index"); then apply them unstaged.
	if err := g.Git.Change(wt, "stash", "apply", "-q", "--index", stash); err == nil {
		return nil, nil
	}
	err := g.Git.Change(wt, "stash", "apply", "-q", stash)
	if err == nil {
		return nil, nil
	}
	out, _ := g.Git.Run("-C", wt, "diff", "--name-only", "-z", "--diff-filter=U")
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, err
	}
	return files, nil
}

func (g *Git) DropStash(wt, stash string) error {
	out, err := g.Git.Run("-C", wt, "stash", "list", "--format=%H")
	if err != nil {
		return err
	}
	for i, h := range strings.Fields(out) {
		if h == stash {
			return g.Git.Change(wt, "stash", "drop", "-q", "stash@{"+strconv.Itoa(i)+"}")
		}
	}
	return nil // already gone
}

func (g *Git) Discard(wt, stash string) error {
	if err := g.Git.Change(wt, "reset", "-q", "--hard"); err != nil {
		return err
	}
	// The stash's third parent holds the new (untracked) files.
	if _, err := g.Git.Run("-C", wt, "rev-parse", "-q", "--verify", stash+"^3"); err != nil {
		return nil
	}
	out, err := g.Git.Run("-C", wt, "ls-tree", "-r", "-z", "--name-only", stash+"^3")
	if err != nil {
		return err
	}
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			if err := os.Remove(filepath.Join(wt, filepath.FromSlash(f))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (g *Git) Unstage(wt string) error { return g.Git.Change(wt, "reset", "-q") }

func (g *Git) Markers(wt string, files []string) []string {
	var left []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(f)))
		if err == nil && refs.HasConflictMarkers(string(b)) {
			left = append(left, f)
		}
	}
	return left
}

func (g *Git) Parked(gitDir string) ([]refs.Parked, error) { return g.parked(gitDir, true) }

// parked reads the stash list (--git-dir: it needs no working folder),
// with each entry's files when files is set.
func (g *Git) parked(gitDir string, files bool) ([]refs.Parked, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "stash", "list", "--format=%H%x1f%ct%x1f%gs")
	if err != nil {
		return nil, err
	}
	var ps []refs.Parked
	for i, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\x1f", 3)
		if len(f) < 3 {
			continue
		}
		branch, ok := refs.ParkedBranch(f[2])
		if !ok {
			continue
		}
		p := refs.Parked{Ref: "stash@{" + strconv.Itoa(i) + "}", Stash: f[0], Branch: branch}
		p.Time, _ = strconv.ParseInt(f[1], 10, 64)
		if files {
			p.Files = g.stashFiles(gitDir, p.Stash)
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// stashFiles lists the files a stash entry changes: the tracked ones (its
// first parent against its working-folder state), then the new ones (its
// third parent, when it has one).
func (g *Git) stashFiles(gitDir, stash string) []string {
	var files []string
	for _, args := range [][]string{{"diff", "--name-only", "-z", stash + "^1", stash, "--"}, {"ls-tree", "-r", "-z", "--name-only", stash + "^3"}} {
		out, err := g.Git.Run(append([]string{"--git-dir=" + gitDir}, args...)...)
		if err != nil {
			continue // no third parent: no new files
		}
		for _, f := range strings.Split(out, "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	return files
}

func (g *Git) ParkedPatch(wt, stash string) (string, error) {
	out, err := g.Git.Run("-C", wt, "stash", "show", "--stat", "-p", stash)
	if err != nil {
		return "", err
	}
	if n, err := g.Git.Run("-C", wt, "ls-tree", "-r", "--name-only", stash+"^3"); err == nil && n != "" {
		out += "\n\nNew files:\n  " + strings.ReplaceAll(n, "\n", "\n  ")
	}
	return out, nil
}

// switchKey holds what switching does with uncommitted changes, in the
// global config or a repo's own.
const switchKey = "gitswitch.switchChanges"

func (g *Git) SwitchModes(gitDir string) (global, repo string) {
	global = g.Git.GlobalGet(switchKey)
	if gitDir != "" {
		repo = g.Git.RepoGet(gitDir, switchKey)
	}
	return global, repo
}

func (g *Git) SaveSwitchMode(gitDir, value string) error { return g.save(switchKey, gitDir, value) }

// pullKey holds how Update brings a branch up to date: ff-only or rebase.
const pullKey = "gitswitch.pull"

func (g *Git) PullModes(gitDir string) (global, repo string) {
	global = g.Git.GlobalGet(pullKey)
	if gitDir != "" {
		repo = g.Git.RepoGet(gitDir, pullKey)
	}
	return global, repo
}

func (g *Git) SavePullMode(gitDir, value string) error { return g.save(pullKey, gitDir, value) }

// save sets key for every repo (gitDir "") or one; value "" removes it.
func (g *Git) save(key, gitDir, value string) error {
	var err error
	switch {
	case gitDir == "" && value == "":
		_, err = g.Git.Global("--unset", key)
	case gitDir == "":
		_, err = g.Git.Global(key, value)
	case value == "":
		err = g.Git.RepoUnset(gitDir, key)
	default:
		return g.Git.RepoSet(gitDir, key, value)
	}
	if value == "" && gitinfra.ExitCode(err) == 5 { // was not set
		return nil
	}
	return err
}

// The clash file holds one "key value" pair per line; files repeat.
func (g *Git) SaveClash(gitDir string, c refs.Clash) error {
	var b strings.Builder
	fmt.Fprintf(&b, "from %s\nto %s\nstash %s\n", c.From, c.To, c.Stash)
	for _, f := range c.Files {
		fmt.Fprintf(&b, "file %s\n", f)
	}
	return os.WriteFile(filepath.Join(gitDir, gitinfra.ClashFile), []byte(b.String()), 0o644)
}

func (g *Git) LoadClash(gitDir string) (*refs.Clash, error) {
	b, err := os.ReadFile(filepath.Join(gitDir, gitinfra.ClashFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c := &refs.Clash{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "from":
			c.From = v
		case "to":
			c.To = v
		case "stash":
			c.Stash = v
		case "file":
			c.Files = append(c.Files, v)
		}
	}
	return c, nil
}

func (g *Git) ClearClash(gitDir string) error {
	err := os.Remove(filepath.Join(gitDir, gitinfra.ClashFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
