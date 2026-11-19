package infrastructure

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// Stashes lists every stash entry with its files (--git-dir: no working
// folder needed). Untracked files are in a stash's third parent.
func (g *Git) Stashes(gitDir string) ([]refs.Stash, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "stash", "list", "--format=%H%x1f%ct%x1f%gs")
	if err != nil {
		return nil, err
	}
	var all []refs.Stash
	for i, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\x1f", 3)
		if len(f) < 3 || f[0] == "" {
			continue
		}
		s := refs.Stash{Index: i, Ref: "stash@{" + strconv.Itoa(i) + "}", Hash: f[0]}
		s.Time, _ = strconv.ParseInt(f[1], 10, 64)
		s.Subject = f[2]
		s.Branch, s.Message = refs.StashSubject(f[2])
		s.Kind, s.ParkedOn = refs.StashKind(f[2])
		s.Files = g.stashEntryFiles(gitDir, s.Hash)
		for _, file := range s.Files {
			if file.Untracked {
				s.Untracked++
			}
		}
		all = append(all, s)
	}
	return all, nil
}

// stashEntryFiles lists a stash's files: the tracked ones (its first parent
// against its working-folder state), then the new ones (its third parent).
func (g *Git) stashEntryFiles(gitDir, hash string) []refs.StashFile {
	gd := "--git-dir=" + gitDir
	statuses := nulPairs(g.out(gd, "diff-tree", "-r", "--no-renames", "--name-status", "-z", hash+"^1", hash))
	nums := numstat(g.out(gd, "diff-tree", "-r", "--no-renames", "--numstat", "-z", hash+"^1", hash))
	staged := map[string]bool{}
	for _, p := range strings.Split(g.out(gd, "diff-tree", "-r", "--no-renames", "--name-only", "-z", hash+"^1", hash+"^2"), "\x00") {
		if p != "" {
			staged[p] = true
		}
	}
	var files []refs.StashFile
	for _, st := range statuses {
		n := nums[st[1]]
		files = append(files, refs.StashFile{Path: st[1], Status: st[0][:1], Staged: staged[st[1]], Added: n.added, Deleted: n.deleted, Binary: n.binary})
	}
	if _, err := g.Git.Run(gd, "rev-parse", "-q", "--verify", hash+"^3"); err == nil {
		un := nulPairs(g.out(gd, "diff-tree", "-r", "--root", "--no-commit-id", "--no-renames", "--name-status", "-z", hash+"^3"))
		unNums := numstat(g.out(gd, "diff-tree", "-r", "--root", "--no-commit-id", "--no-renames", "--numstat", "-z", hash+"^3"))
		for _, st := range un {
			n := unNums[st[1]]
			files = append(files, refs.StashFile{Path: st[1], Status: "?", Untracked: true, Added: n.added, Binary: n.binary})
		}
	}
	return files
}

// out is git's output, "" on failure.
func (g *Git) out(args ...string) string {
	s, err := g.Git.Run(args...)
	if err != nil {
		return ""
	}
	return s
}

// nulPairs reads `--name-status -z` output: status, path, status, path...
func nulPairs(s string) [][2]string {
	f := strings.Split(s, "\x00")
	var out [][2]string
	for i := 0; i+1 < len(f); i += 2 {
		if f[i] == "" {
			continue
		}
		out = append(out, [2]string{f[i], f[i+1]})
	}
	return out
}

type counts struct {
	added, deleted int
	binary         bool
}

// numstat reads `--numstat -z` output (no renames): added TAB deleted TAB path.
func numstat(s string) map[string]counts {
	m := map[string]counts{}
	for _, rec := range strings.Split(s, "\x00") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\t", 3)
		if len(f) < 3 {
			continue
		}
		c := counts{}
		if f[0] == "-" {
			c.binary = true
		} else {
			c.added, _ = strconv.Atoi(f[0])
			c.deleted, _ = strconv.Atoi(f[1])
		}
		m[f[2]] = c
	}
	return m
}

// StashDiff shows one file of a stash entry: a tracked file's change, or a
// new file's whole content (from the third parent).
func (g *Git) StashDiff(gitDir, hash, path string) (refs.StashDiff, error) {
	gd := "--git-dir=" + gitDir
	d := refs.StashDiff{Path: path}
	if _, err := g.Git.Run(gd, "rev-parse", "-q", "--verify", hash+"^3:"+path); err == nil {
		d.Untracked = true
		out, err := g.Git.Run(gd, "--literal-pathspecs", "diff-tree", "-p", "-r", "--root", "--no-commit-id", "--no-renames", hash+"^3", "--", path)
		d.Text = out
		return d, err
	}
	out, err := g.Git.Run(gd, "--literal-pathspecs", "diff-tree", "-p", "-r", "--no-renames", hash+"^1", hash, "--", path)
	if err != nil {
		return d, err
	}
	if out == "" {
		return d, fmt.Errorf("%s is not in that stash", path)
	}
	d.Text = out
	return d, nil
}

// StashPush saves the working changes as a stash entry. git 2.47 breaks
// `stash push -u` when --literal-pathspecs is given without paths, so the
// option goes only with paths.
func (g *Git) StashPush(wt string, o refs.StashOptions) (string, error) {
	before, _ := g.Git.Run("-C", wt, "rev-parse", "-q", "--verify", "refs/stash")
	var args []string
	if len(o.Paths) > 0 {
		args = append(args, "--literal-pathspecs")
	}
	args = append(args, "stash", "push", "-q")
	if o.Untracked {
		args = append(args, "--include-untracked")
	}
	if o.KeepIndex {
		args = append(args, "--keep-index")
	}
	if o.Message != "" {
		args = append(args, "-m", o.Message)
	}
	if len(o.Paths) > 0 {
		args = append(args, "--")
		args = append(args, o.Paths...)
	}
	if err := g.Git.Change(wt, args...); err != nil {
		return "", err
	}
	after, _ := g.Git.Run("-C", wt, "rev-parse", "-q", "--verify", "refs/stash")
	if after == "" || after == before {
		return "", errors.New("there was nothing to stash")
	}
	return after, nil
}

// StashBranch is git stash branch, by commit. When the stash does not apply
// after the branch was made, the branch is removed again and the folder put
// back, so that nothing changes and the stash stays.
func (g *Git) StashBranch(wt, hash, name string) error {
	list, err := g.Git.Run("-C", wt, "stash", "list", "--format=%H")
	if err != nil {
		return err
	}
	idx := -1
	for i, h := range strings.Fields(list) {
		if h == hash {
			idx = i
			break
		}
	}
	if idx < 0 {
		return refs.ErrStashChanged
	}
	prevBranch, _ := g.Git.Run("-C", wt, "symbolic-ref", "-q", "--short", "HEAD")
	prevHead, _ := g.Git.Run("-C", wt, "rev-parse", "-q", "--verify", "HEAD")
	err = g.Git.Change(wt, "stash", "branch", name, "stash@{"+strconv.Itoa(idx)+"}")
	if err == nil {
		return nil
	}
	if cur, _ := g.Git.Run("-C", wt, "symbolic-ref", "-q", "--short", "HEAD"); cur == name && prevHead != "" {
		// The branch was made and checked out, but the changes did not go on.
		back := []string{"switch", "-q", prevBranch}
		if prevBranch == "" {
			back = []string{"switch", "-q", "--detach", prevHead}
		}
		if derr := g.Discard(wt, hash); derr == nil {
			if berr := g.Git.Change(wt, back...); berr == nil {
				_ = g.Git.Change(wt, "branch", "-q", "-D", name)
				return err
			}
		}
		return fmt.Errorf("%v. GitSwitch could not go back to where you were; you are on the new branch %s, and the stash is still in the list", err, name)
	}
	return err
}
