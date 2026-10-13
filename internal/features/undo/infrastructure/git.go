// Package infrastructure keeps the last action in the repo itself and
// moves the repo back with git. Import it as undoinfra.
package infrastructure

import (
	"fmt"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// ref points at the commit an undo goes back to, so git gc keeps a dropped
// or rewritten commit for as long as it can be undone.
const ref = "refs/gitswitch/undo"

// section holds the record in the repo's own config ([gitswitch "undo"]).
const section = "gitswitch.undo"

// Git implements the undo domain's Store and Git.
type Git struct {
	Git *gitinfra.Runner
}

func (g *Git) Save(gitDir string, r undo.Record) error {
	if err := g.Clear(gitDir); err != nil {
		return err
	}
	if _, err := g.Git.Run("--git-dir="+gitDir, "update-ref", "-m", "gitswitch: "+r.Label, ref, r.Before); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"action", string(r.Action)}, {"label", r.Label}, {"worktree", r.Worktree}, {"branch", r.Branch},
		{"to", r.To}, {"before", r.Before}, {"after", r.After}, {"time", strconv.FormatInt(r.Time, 10)},
		{"deleted", joinDeleted(r.Deleted)},
	} {
		if err := g.Git.RepoSet(gitDir, section+"."+kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func (g *Git) Load(gitDir string) (undo.Record, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "config", "--local", "-z", "--get-regexp", `^gitswitch\.undo\.`)
	if err != nil || out == "" {
		return undo.Record{}, undo.ErrNothing // exit 1: no such keys
	}
	var r undo.Record
	for _, entry := range strings.Split(out, "\x00") {
		k, v, _ := strings.Cut(entry, "\n")
		switch strings.TrimPrefix(k, section+".") {
		case "action":
			r.Action = undo.Action(v)
		case "label":
			r.Label = v
		case "worktree":
			r.Worktree = v
		case "branch":
			r.Branch = v
		case "to":
			r.To = v
		case "before":
			r.Before = v
		case "after":
			r.After = v
		case "time":
			r.Time, _ = strconv.ParseInt(v, 10, 64)
		case "deleted":
			r.Deleted = splitDeleted(v)
		}
	}
	if r.Action == "" || r.Before == "" || r.After == "" {
		return undo.Record{}, undo.ErrNothing // half written: not usable
	}
	return r, nil
}

func (g *Git) Clear(gitDir string) error {
	// Both fail when there is nothing to remove; that is fine.
	_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", section)
	_, _ = g.Git.Run("--git-dir="+gitDir, "update-ref", "-d", ref)
	return nil
}

func (g *Git) Mark(gitDir string) undo.Mark {
	m := undo.Mark{Worktree: g.Git.WorktreeOf(gitDir), Busy: gitinfra.InProgress(gitDir)}
	m.Branch, _ = g.Git.Run("--git-dir="+gitDir, "symbolic-ref", "-q", "--short", "HEAD")
	m.Head, _ = g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	return m
}

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

func (g *Git) Reset(worktree, hash string, keep bool) error {
	mode := "--mixed"
	if keep {
		mode = "--keep"
	}
	return g.Git.Change(worktree, "reset", "-q", mode, hash)
}

func (g *Git) Switch(worktree, branch string) error {
	return g.Git.Change(worktree, "switch", "--no-guess", branch)
}

func (g *Git) Tip(gitDir, branch string) string {
	out, _ := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", "refs/heads/"+branch+"^{commit}")
	return out
}

func (g *Git) MoveBranch(gitDir, branch, to, was string) error {
	args := []string{"--git-dir=" + gitDir, "update-ref", "-m", "gitswitch: undo"}
	if to == "" {
		args = append(args, "-d", "refs/heads/"+branch, was)
	} else {
		args = append(args, "refs/heads/"+branch, to, was)
	}
	_, err := g.Git.Run(args...)
	return err
}

// joinDeleted writes deleted branches as one config value: hash:name:
// remote:merge for each, separated by spaces. Branch and remote names
// can't hold a space or a colon.
func joinDeleted(ds []undo.Deleted) string {
	var parts []string
	for _, d := range ds {
		parts = append(parts, d.Hash+":"+d.Name+":"+d.Remote+":"+d.Merge)
	}
	return strings.Join(parts, " ")
}

func splitDeleted(v string) []undo.Deleted {
	var ds []undo.Deleted
	for _, p := range strings.Fields(v) {
		if f := strings.SplitN(p, ":", 4); len(f) == 4 && f[0] != "" && f[1] != "" {
			ds = append(ds, undo.Deleted{Hash: f[0], Name: f[1], Remote: f[2], Merge: f[3]})
		}
	}
	return ds
}

func (g *Git) Recreate(gitDir string, branches []undo.Deleted) error {
	for _, d := range branches {
		if g.Tip(gitDir, d.Name) != "" {
			return fmt.Errorf("there is a branch %s again", d.Name)
		}
	}
	for _, d := range branches {
		// The all-zero old value: only when the branch does not exist.
		if _, err := g.Git.Run("--git-dir="+gitDir, "update-ref", "-m", "gitswitch: undo prune", "refs/heads/"+d.Name, d.Hash, ""); err != nil {
			return err
		}
		if d.Remote != "" && d.Merge != "" {
			_ = g.Git.RepoSet(gitDir, "branch."+d.Name+".remote", d.Remote)
			_ = g.Git.RepoSet(gitDir, "branch."+d.Name+".merge", d.Merge)
		}
	}
	return nil
}
