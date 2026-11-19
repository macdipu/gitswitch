// Package infrastructure rewrites history with git (rebase -i with the
// todo supplied without an editor) and keeps a stopped rewrite in the repo
// itself. Import it as rewriteinfra.
package infrastructure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	historyinfra "github.com/macdipu/gitswitch/internal/features/history/infrastructure"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// dirName is the folder in the git dir that holds the todo and the
// messages of a rewrite. It stays while the rewrite is stopped, because
// the amends that set the messages run after the rebase continues.
const dirName = "gitswitch-rewrite"

// ref keeps the branch's tip from before a stopped rewrite reachable.
const ref = "refs/gitswitch/rewrite-orig"

const section = "gitswitch.rewrite"

// Git implements the rewrite domain's Git.
type Git struct {
	Git *gitinfra.Runner
}

func (g *Git) Head(gitDir string) (refs.Head, error) {
	return (&refsinfra.Git{Git: g.Git}).Head(gitDir)
}

func (g *Git) Resolve(gitDir, rev string) (string, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "-q", "--verify", rev+"^{commit}")
	if err != nil || out == "" {
		return "", errors.New("there is no commit called " + rev)
	}
	return out, nil
}

func (g *Git) IsAncestor(gitDir, a, b string) bool {
	_, err := g.Git.Run("--git-dir="+gitDir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

const logFormat = "--format=%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s%x1f%B%x1d"

func (g *Git) log(gitDir string, args ...string) ([]rewrite.Commit, error) {
	a := append([]string{"--git-dir=" + gitDir, "log", "--no-show-signature", "--no-color", "--no-renames", "--name-only", logFormat}, args...)
	out, err := g.Git.Run(append(a, "--")...)
	if err != nil {
		return nil, err
	}
	return parse(out), nil
}

// parse reads `git log --name-only` with logFormat: records start at \x1e,
// the message ends at \x1d and the changed files follow, one a line.
func parse(out string) []rewrite.Commit {
	var cs []rewrite.Commit
	for _, rec := range strings.Split(out, "\x1e") {
		head, files, ok := strings.Cut(rec, "\x1d")
		if !ok {
			continue
		}
		f := strings.SplitN(head, "\x1f", 7)
		if len(f) < 7 {
			continue
		}
		c := rewrite.Commit{Hash: f[0], Parents: strings.Fields(f[1]), Author: f[2], Email: f[3], Subject: f[5],
			Message: rewrite.Tidy(f[6])}
		c.Merge = len(c.Parents) > 1
		fmt.Sscan(f[4], &c.Time)
		if len(c.Hash) >= 7 {
			c.Short = c.Hash[:7]
		}
		for _, l := range strings.Split(files, "\n") {
			if strings.TrimSpace(l) != "" {
				c.Files++
			}
		}
		cs = append(cs, c)
	}
	return cs
}

func (g *Git) Commit(gitDir, hash string) (rewrite.Commit, error) {
	cs, err := g.log(gitDir, "-1", hash)
	if err != nil || len(cs) == 0 {
		return rewrite.Commit{}, fmt.Errorf("no commit %q in this repo", hash)
	}
	return cs[0], nil
}

func (g *Git) Unpushed(gitDir string) ([]rewrite.Commit, error) {
	wt := g.Git.WorktreeOf(gitDir)
	// LocalCommits refuses when this clone cannot tell what is pushed.
	hs, err := (&historyinfra.Log{Git: g.Git}).LocalCommits(wt)
	if err != nil || len(hs) == 0 {
		return nil, err
	}
	return g.log(gitDir, "--topo-order", "--reverse", "HEAD", "--not", "--remotes")
}

func (g *Git) Range(gitDir, base string) ([]rewrite.Commit, error) {
	args := []string{"--topo-order", "--reverse", "HEAD"}
	if base != "" {
		args = append(args, "--not", base)
	}
	return g.log(gitDir, args...)
}

func (g *Git) Op(gitDir string) string { return gitinfra.InProgress(gitDir) }

func (g *Git) Conflicted(gitDir string) ([]string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	out, err := g.Git.Run("--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.Trim(f, "\r\n"); f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

func (g *Git) Abort(wt string) error { return g.Git.Change(wt, "rebase", "--abort") }

func (g *Git) CommitFixup(wt, hash string) (string, error) {
	// "fixup! <full hash>": the rewrite orders by hash, never by subject,
	// so a repeated subject can't fold the changes into the wrong commit.
	if err := g.Git.Change(wt, "commit", "-q", "-m", "fixup! "+hash); err != nil {
		return "", err
	}
	out, err := g.Git.Run("-C", wt, "rev-parse", "HEAD")
	return out, err
}

func (g *Git) SoftReset(wt, hash string) error {
	return g.Git.Change(wt, "reset", "-q", "--soft", hash)
}

// sh quotes a path for the shell git runs the editor and exec lines with
// (on Windows too: Git for Windows brings its own sh, which takes forward
// slashes).
func sh(p string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`) + "'"
}

// Todo renders a plan as git's todo text; dir holds the message files.
func Todo(p rewrite.Plan, dir string) string {
	var b strings.Builder
	for _, l := range p.Lines {
		if l.Pre >= 0 {
			fmt.Fprintf(&b, "exec git rev-parse HEAD > %s\n", sh(filepath.Join(dir, fmt.Sprintf("pre-%d", l.Pre))))
		}
		fmt.Fprintf(&b, "%s %s\n", l.Action, l.Hash)
		if l.Msg >= 0 {
			// Skipped when the group made no commit (it came out empty),
			// so the amend never lands on the commit before.
			fmt.Fprintf(&b, "exec test \"$(git rev-parse HEAD)\" = \"$(cat %s)\" || git commit -q --amend --allow-empty --no-verify -F %s\n",
				sh(filepath.Join(dir, fmt.Sprintf("pre-%d", l.Msg))), sh(filepath.Join(dir, fmt.Sprintf("msg-%d", l.Msg))))
		}
	}
	return b.String()
}

func (g *Git) Rebase(gitDir, wt, base string, p rewrite.Plan) error {
	dir := filepath.Join(gitDir, dirName)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, m := range p.Messages {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("msg-%d", i)), []byte(m+"\n"), 0o644); err != nil {
			return err
		}
	}
	todo := filepath.Join(dir, "todo")
	if err := os.WriteFile(todo, []byte(Todo(p, dir)), 0o644); err != nil {
		return err
	}
	// git runs the sequence editor through its shell with the todo's path
	// appended: `cp <our todo> <git's todo>`.
	env := []string{"GIT_SEQUENCE_EDITOR=cp " + sh(todo)}
	_, err := g.Git.RunIn(wt, env, "", false,
		"-c", "rebase.autoSquash=false", "-c", "rebase.updateRefs=false", "-c", "rebase.autoStash=false",
		"-c", "rebase.missingCommitsCheck=ignore", "-c", "rebase.instructionFormat=%s",
		"rebase", "-i", "--no-rebase-merges", "--empty=keep", base)
	return err
}

func (g *Git) SavePending(gitDir string, p rewrite.Pending) error {
	if _, err := g.Git.Run("--git-dir="+gitDir, "update-ref", "-m", "gitswitch: "+p.Label, ref, p.Head); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"head", p.Head}, {"branch", p.Branch}, {"worktree", p.Worktree},
		{"label", p.Label}, {"action", string(p.Action)}, {"fixup", p.Fixup}} {
		if err := g.Git.RepoSet(gitDir, section+"."+kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func (g *Git) Pending(gitDir string) (rewrite.Pending, bool) {
	out, err := g.Git.Run("--git-dir="+gitDir, "config", "--local", "-z", "--get-regexp", `^gitswitch\.rewrite\.`)
	if err != nil || out == "" {
		return rewrite.Pending{}, false
	}
	var p rewrite.Pending
	for _, entry := range strings.Split(out, "\x00") {
		k, v, _ := strings.Cut(entry, "\n")
		switch strings.TrimPrefix(k, section+".") {
		case "head":
			p.Head = v
		case "branch":
			p.Branch = v
		case "worktree":
			p.Worktree = v
		case "label":
			p.Label = v
		case "action":
			p.Action = undo.Action(v)
		case "fixup":
			p.Fixup = v
		}
	}
	return p, p.Head != "" && p.Branch != ""
}

func (g *Git) ClearPending(gitDir string) {
	// Both fail when there is nothing to remove; that is fine.
	_, _ = g.Git.Run("--git-dir="+gitDir, "config", "--local", "--remove-section", section)
	_, _ = g.Git.Run("--git-dir="+gitDir, "update-ref", "-d", ref)
	g.Cleanup(gitDir)
}

func (g *Git) Cleanup(gitDir string) { _ = os.RemoveAll(filepath.Join(gitDir, dirName)) }
