// Package infrastructure rebases with git, reads what a rebase touches and
// keeps the pending rebase in the repo itself. Import it as rebaseinfra.
package infrastructure

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
)

// Git implements the rebase domain's Git and Store.
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

func (g *Git) Commits(gitDir, tip, not string, limit int, oldestFirst bool) ([]rebase.CommitInfo, int, error) {
	const format = "--format=%H%x1f%h%x1f%an%x1f%s"
	var hashes []string
	count := 0
	if oldestFirst {
		out, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--reverse", tip, "--not", not, "--")
		if err != nil {
			return nil, 0, err
		}
		all := strings.Fields(out)
		count = len(all)
		hashes = all[:min(limit, count)]
	} else {
		n, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", tip, "--not", not, "--")
		if err != nil {
			return nil, 0, err
		}
		count, _ = strconv.Atoi(n)
		if count == 0 {
			return nil, 0, nil
		}
		out, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "-n", strconv.Itoa(limit), tip, "--not", not, "--")
		if err != nil {
			return nil, count, err
		}
		hashes = strings.Fields(out)
	}
	if len(hashes) == 0 {
		return nil, count, nil
	}
	args := append([]string{"--git-dir=" + gitDir, "log", "--no-show-signature", "--no-walk=unsorted", format}, hashes...)
	out, err := g.Git.Run(append(args, "--")...)
	if err != nil {
		return nil, count, err
	}
	var cs []rebase.CommitInfo
	for _, line := range strings.Split(out, "\n") {
		if f := strings.SplitN(line, "\x1f", 4); len(f) == 4 {
			cs = append(cs, rebase.CommitInfo{Hash: f[0], Short: f[1], Author: f[2], Subject: f[3]})
		}
	}
	return cs, count, nil
}

func (g *Git) Merges(gitDir, from, to string) int {
	out, _ := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", "--merges", to, "--not", from, "--")
	n, _ := strconv.Atoi(out)
	return n
}

func (g *Git) Count(gitDir, from, to string) int {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", to, "--not", from, "--")
	if err != nil {
		return 1 << 30 // unknown: never looks like "nothing"
	}
	n, _ := strconv.Atoi(out)
	return n
}

// Pushed lists the commits in from..to that some remote branch contains.
func (g *Git) Pushed(gitDir, from, to string) ([]string, error) {
	all, err := g.Git.Run("--git-dir="+gitDir, "rev-list", to, "--not", from, "--")
	if err != nil {
		return nil, err
	}
	local, err := g.Git.Run("--git-dir="+gitDir, "rev-list", to, "--not", from, "--remotes", "--")
	if err != nil {
		return nil, err
	}
	notPushed := map[string]bool{}
	for _, h := range strings.Fields(local) {
		notPushed[h] = true
	}
	var pushed []string
	for _, h := range strings.Fields(all) {
		if !notPushed[h] {
			pushed = append(pushed, h)
		}
	}
	return pushed, nil
}

func (g *Git) Changed(gitDir, from, to string) ([]string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "diff", "--name-only", "--no-renames", "-z", from, to, "--")
	if err != nil {
		return nil, err
	}
	var l []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.Trim(f, "\r\n"); f != "" {
			l = append(l, f)
		}
	}
	return l, nil
}

// Rebase never opens an editor (Change sets GIT_EDITOR), never autostashes
// and never moves other branches (rebase.updateRefs).
func (g *Git) Rebase(wt, onto string) error {
	return g.Git.Change(wt, "-c", "rebase.autoStash=false", "-c", "rebase.updateRefs=false", "rebase", "--no-autostash", onto)
}

func (g *Git) Abort(wt string) error { return g.Git.Change(wt, "rebase", "--abort") }

func (g *Git) Op(gitDir string) string { return gitinfra.InProgress(gitDir) }

func (g *Git) Conflicted(gitDir string) ([]string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	out, err := g.Git.Run("--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var l []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.Trim(f, "\r\n"); f != "" && !seen[f] {
			seen[f] = true
			l = append(l, f)
		}
	}
	return l, nil
}

func (g *Git) Stopped(gitDir string) (orig, onto string) {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		o, err1 := os.ReadFile(filepath.Join(gitDir, d, "orig-head"))
		n, err2 := os.ReadFile(filepath.Join(gitDir, d, "onto"))
		if err1 == nil && err2 == nil {
			return strings.TrimSpace(string(o)), strings.TrimSpace(string(n))
		}
	}
	return "", ""
}

// ref keeps the commit the branch was at, so git gc keeps it for as long
// as the rebase can still be undone.
const ref = "refs/gitswitch/rebase"

// section holds the pending rebase in the repo's own config.
const section = "gitswitch.rebase"

func (g *Git) Save(gitDir string, p rebase.Pending) error {
	if err := g.Clear(gitDir); err != nil {
		return err
	}
	if _, err := g.Git.Run("--git-dir="+gitDir, "update-ref", "-m", "gitswitch: rebase "+p.Branch, ref, p.Orig); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"branch", p.Branch}, {"orig", p.Orig}, {"onto", p.Onto}, {"ontoname", p.OntoName},
		{"worktree", p.Worktree}, {"count", strconv.Itoa(p.Count)}, {"time", strconv.FormatInt(p.Time, 10)},
	} {
		if err := g.Git.RepoSet(gitDir, section+"."+kv[0], kv[1]); err != nil {
			_ = g.Clear(gitDir)
			return err
		}
	}
	return nil
}

func (g *Git) Load(gitDir string) (rebase.Pending, bool) {
	out, err := g.Git.Run("--git-dir="+gitDir, "config", "--local", "-z", "--get-regexp", `^gitswitch\.rebase\.`)
	if err != nil || out == "" {
		return rebase.Pending{}, false
	}
	var p rebase.Pending
	for _, entry := range strings.Split(out, "\x00") {
		k, v, _ := strings.Cut(entry, "\n")
		switch strings.TrimPrefix(k, section+".") {
		case "branch":
			p.Branch = v
		case "orig":
			p.Orig = v
		case "onto":
			p.Onto = v
		case "ontoname":
			p.OntoName = v
		case "worktree":
			p.Worktree = v
		case "count":
			p.Count, _ = strconv.Atoi(v)
		case "time":
			p.Time, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	if p.Branch == "" || p.Orig == "" || p.Onto == "" {
		return rebase.Pending{}, false // half written: not usable
	}
	return p, true
}

func (g *Git) Clear(gitDir string) error {
	// Both fail when there is nothing to remove; that is fine.
	_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", section)
	_, _ = g.Git.Run("--git-dir="+gitDir, "update-ref", "-d", ref)
	return nil
}
