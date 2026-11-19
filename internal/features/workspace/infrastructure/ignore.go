package infrastructure

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Git implements the workspace domain's Ignorer too.

func (g *Git) IgnoreFile(gitDir string, local bool) (string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	if wt == gitDir {
		return "", errors.New("this repo has no working folder (a bare repo)")
	}
	if !local {
		return filepath.Join(wt, workspace.GitignoreName), nil
	}
	// info/exclude is in the common git dir, which a linked worktree
	// shares with its main one.
	out, err := g.Git.Run("-C", wt, "--git-dir="+gitDir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(wt, out)
	}
	return filepath.Clean(out), nil
}

func (g *Git) ReadIgnore(file string) (string, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func (g *Git) WriteIgnore(file, content string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(content), 0o644)
}

func (g *Git) IsDir(gitDir, path string) bool {
	fi, err := os.Stat(filepath.Join(g.Git.WorktreeOf(gitDir), filepath.FromSlash(path)))
	return err == nil && fi.IsDir()
}

func (g *Git) Tracked(gitDir, path string) ([]string, error) {
	wt := g.Git.WorktreeOf(gitDir)
	out, err := g.Git.Run("--literal-pathspecs", "--no-optional-locks", "-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"ls-files", "-z", "--cached", "--", path)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func (g *Git) Ignored(gitDir, path string) (bool, error) {
	// check-ignore skips tracked files, but finds them as a pathspec would
	// (a tracked a.txt makes "*.txt" not ignored) and refuses
	// --literal-pathspecs. So tracked is asked literally first, and
	// check-ignore then looks at the ignore files only (--no-index). A
	// leading ":" would be pathspec magic: "./" stops it.
	if tracked, err := g.Tracked(gitDir, path); err != nil || len(tracked) > 0 {
		return false, err
	}
	wt := g.Git.WorktreeOf(gitDir)
	// Exit 0: ignored, 1: not ignored.
	_, err := g.Git.Run("-C", wt, "--git-dir="+gitDir, "--work-tree="+wt,
		"check-ignore", "-q", "--no-index", "--", "./"+path)
	switch {
	case err == nil:
		return true, nil
	case gitinfra.ExitCode(err) == 1:
		return false, nil
	}
	return false, err
}

func (g *Git) Untrack(wt, path string) error {
	return g.Git.Change(wt, "--literal-pathspecs", "rm", "--cached", "-r", "-q", "--", path)
}
