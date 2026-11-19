package infrastructure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Paths are passed after --literal-pathspecs, so a file named "*.go" is
// that file and nothing else.

func (g *Git) Status(gitDir string) ([]workspace.File, error) {
	wt := g.Git.WorktreeOf(gitDir)
	if wt == gitDir {
		return nil, errors.New("this repo has no working folder (a bare repo)")
	}
	// --no-optional-locks: only reading, so git must not take the index
	// lock an editor or IDE may be using.
	out, err := g.Git.Run("--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatus(out), nil
}

// parseStatus reads `git status --porcelain=v2 -z`: "1 XY sub mH mI mW hH
// hI path", "2 XY sub mH mI mW hH hI Xscore path\0orig", "u XY sub m1 m2
// m3 mW h1 h2 h3 path", "? path". Paths may hold spaces, so each line is
// split into a fixed number of fields, the path last.
func parseStatus(out string) []workspace.File {
	files := []workspace.File{}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 3 {
			continue
		}
		switch e[0] {
		case '1':
			if f := strings.SplitN(e, " ", 9); len(f) == 9 && len(f[1]) == 2 {
				files = append(files, workspace.File{Path: f[8], Staged: f[1][:1], Worktree: f[1][1:]})
			}
		case '2':
			f := strings.SplitN(e, " ", 10)
			orig := ""
			if i+1 < len(entries) {
				i++
				orig = entries[i]
			}
			if len(f) == 10 && len(f[1]) == 2 {
				files = append(files, workspace.File{Path: f[9], Orig: orig, Staged: f[1][:1], Worktree: f[1][1:]})
			}
		case 'u':
			if f := strings.SplitN(e, " ", 11); len(f) == 11 && len(f[1]) == 2 {
				files = append(files, workspace.File{Path: f[10], Staged: f[1][:1], Worktree: f[1][1:], Conflict: true})
			}
		case '?':
			files = append(files, workspace.File{Path: e[2:], Staged: "?", Worktree: "?"})
		}
	}
	return files
}

func (g *Git) Diff(gitDir string, staged bool, paths []string) (string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	args := []string{"--literal-pathspecs", "-C", wt, "--git-dir=" + gitDir, "--work-tree=" + wt,
		"diff", "--no-color", "--no-ext-diff"}
	if staged {
		args = append(args, "--cached")
	}
	// Raw: a diff turned back into a patch must keep its last byte.
	out, err := g.Git.Output(append(append(args, "--"), paths...)...)
	if err != nil {
		return "", err
	}
	if staged {
		return out, nil
	}
	// git diff leaves out files it does not track: show each as added.
	files, err := g.Status(gitDir)
	if err != nil {
		return out, nil
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	var b strings.Builder
	b.WriteString(out)
	for _, f := range files {
		if !f.New() || (len(paths) > 0 && !want[f.Path]) {
			continue
		}
		if b.Len() > workspace.MaxDiffBytes {
			break
		}
		// --no-index exits 1 when the files differ, which they do.
		d, err := g.Git.Output("-C", wt, "diff", "--no-index", "--no-color", "--no-ext-diff", "--", "/dev/null", f.Path)
		if err != nil && gitinfra.ExitCode(err) != 1 {
			continue
		}
		if b.Len() > 0 && d != "" && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString(d)
	}
	return b.String(), nil
}

func (g *Git) Stage(wt string, paths []string) error {
	return g.Git.Change(wt, withPaths([]string{"add", "--all"}, paths)...)
}

func (g *Git) Unstage(wt string, paths []string) error {
	// reset works before the first commit too, unlike restore --staged.
	return g.Git.Change(wt, withPaths([]string{"reset", "-q"}, paths)...)
}

func (g *Git) Discard(wt, message string, paths []string) error {
	return g.Git.Change(wt, withPaths([]string{"stash", "push", "--include-untracked", "--quiet", "-m", message}, paths)...)
}

// withPaths adds paths to a command, taken literally. With no paths the
// flag is left out: git 2.47's stash push -u, given --literal-pathspecs
// and no path, stashes new files but leaves them in the folder.
func withPaths(args, paths []string) []string {
	if len(paths) == 0 {
		return args
	}
	return append(append(append([]string{"--literal-pathspecs"}, args...), "--"), paths...)
}

func (g *Git) Apply(wt, patch string, o workspace.ApplyOptions) error {
	args := []string{"apply", "--recount", "--whitespace=nowarn"}
	if o.Cached {
		args = append(args, "--cached")
	}
	if o.Reverse {
		args = append(args, "--reverse")
	}
	if o.Check {
		args = append(args, "--check")
	}
	_, err := g.Git.RunIn(wt, nil, patch, false, append(args, "-")...)
	if err != nil {
		return fmt.Errorf("the change could not be applied (the file may have changed): %v", err)
	}
	return nil
}

func (g *Git) IntentToAdd(wt, path string) error {
	return g.Git.Change(wt, "--literal-pathspecs", "add", "--intent-to-add", "--", path)
}

// Backup builds the stash entry with plumbing, on a scratch index, so the
// real index and the working folder are never touched: a commit whose tree
// is HEAD with this one file as it is now, on top of an "index" commit, the
// way git stash lays them out.
func (g *Git) Backup(wt, message, path string) error {
	gitDir, err := g.Git.RunIn(wt, nil, "", false, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(gitDir, "gitswitch-backup-index-*")
	if err != nil {
		return err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	env := []string{"GIT_INDEX_FILE=" + tmp.Name()}
	// Each step is a git call in the working folder, on the scratch index.
	step := func(stdin string, args ...string) (string, error) {
		return g.Git.RunIn(wt, env, stdin, false, args...)
	}
	if _, err := step("", "read-tree", "HEAD"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(wt, filepath.FromSlash(path))); err == nil {
		ls, err := g.Git.RunIn(wt, nil, "", false, "--literal-pathspecs", "ls-files", "--stage", "--", path)
		if err != nil {
			return err
		}
		mode := "100644"
		if f := strings.Fields(ls); len(f) > 0 {
			mode = f[0]
		}
		blob, err := g.Git.RunIn(wt, nil, "", false, "--literal-pathspecs", "hash-object", "-w", "--", path)
		if err != nil {
			return err
		}
		if _, err := step("", "update-index", "--add", "--cacheinfo", mode+","+blob+","+path); err != nil {
			return err
		}
	} else if _, err := step("", "--literal-pathspecs", "update-index", "--force-remove", "--", path); err != nil {
		return err
	}
	tree, err := step("", "write-tree")
	if err != nil {
		return err
	}
	headTree, err := g.Git.RunIn(wt, nil, "", false, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	head, err := g.Git.RunIn(wt, nil, "", false, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	idx, err := g.Git.RunIn(wt, nil, "", false, "commit-tree", headTree, "-p", head, "-m", "index on "+message)
	if err != nil {
		return err
	}
	wip, err := g.Git.RunIn(wt, nil, "", false, "commit-tree", tree, "-p", head, "-p", idx, "-m", message)
	if err != nil {
		return err
	}
	_, err = g.Git.RunIn(wt, nil, "", false, "stash", "store", "-m", message, wip)
	return err
}
