package application

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Status lists the working folder's uncommitted changes.
func (s *Service) Status(gitDir string) (workspace.Status, error) {
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return workspace.Status{}, err
	}
	files, err := s.Changes.Status(gitDir)
	if err != nil {
		return workspace.Status{}, err
	}
	return workspace.Status{Branch: h.Branch, Busy: h.Busy, Files: files}, nil
}

// Diff is one file's uncommitted change, or every file's when path is "":
// the staged change with staged, otherwise the one not staged (a new file
// in full).
func (s *Service) Diff(gitDir, path string, staged bool) (workspace.Diff, error) {
	d := workspace.Diff{Staged: staged}
	var paths []string
	if path != "" {
		p, err := workspace.CleanPath(path)
		if err != nil {
			return d, err
		}
		d.Path, paths = p, []string{p}
	}
	text, err := s.Changes.Diff(gitDir, staged, paths)
	if err != nil {
		return d, err
	}
	d.Text = text
	if len(d.Text) > workspace.MaxDiffBytes {
		d.Text, d.Truncated = d.Text[:workspace.MaxDiffBytes], true
	}
	return d, nil
}

// Stage adds the changes of paths (new and deleted files too) to the next
// commit, or with all every change. It returns the paths staged.
func (s *Service) Stage(gitDir string, paths []string, all bool) ([]string, error) {
	_, picked, err := s.pick(gitDir, paths, all, workspace.File.IsChanged, "no change to stage", "has nothing to stage")
	if err != nil {
		return nil, err
	}
	return s.run(gitDir, "stage", picked, all, s.Changes.Stage)
}

// Unstage takes the changes of paths out of the next commit, keeping them
// in the files, or with all every staged change. It returns the paths
// unstaged.
func (s *Service) Unstage(gitDir string, paths []string, all bool) ([]string, error) {
	st, picked, err := s.pick(gitDir, paths, all, workspace.File.IsStaged, "nothing is staged", "is not staged")
	if err != nil {
		return nil, err
	}
	if all && st.Busy != "" { // a plain reset would end the merge or rebase
		return nil, fmt.Errorf("%s: unstage files by name", remote.Busy(st.Busy))
	}
	return s.run(gitDir, "unstage", picked, all, s.Changes.Unstage)
}

// Discarded says what Discard removed and where it is kept.
type Discarded struct {
	Files []string `json:"files"`
	Stash string   `json:"stash"` // the stash message that holds the changes
}

// Discard puts paths back as they are in the last commit (new files are
// removed), or with all every file. Nothing is lost: the changes are kept
// as a stash named "gitswitch discard: <branch>". Refused while git is in
// the middle of another operation, for conflicted files, and before the
// first commit.
func (s *Service) Discard(gitDir string, paths []string, all bool) (Discarded, error) {
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return Discarded{}, err
	}
	switch {
	case h.Busy != "":
		return Discarded{}, remote.Busy(h.Busy)
	case h.Hash == "":
		return Discarded{}, errors.New("this repo has no commit yet, so there is nothing to go back to")
	}
	keep := func(f workspace.File) bool { return !f.Conflict }
	_, picked, err := s.pick(gitDir, paths, all, keep, "there is nothing to discard", "has no changes")
	if err != nil {
		return Discarded{}, err
	}
	msg := workspace.DiscardMessage(h.Branch)
	files, err := s.run(gitDir, "discard", picked, all, func(wt string, ps []string) error {
		return s.Changes.Discard(wt, msg, ps)
	})
	return Discarded{Files: files, Stash: msg}, err
}

// pick checks paths against the working folder's changes: each must be a
// file with a change keep accepts, and none may be in conflict (fixing a
// conflict comes first). With all, every such file is picked, and any
// conflict refuses.
func (s *Service) pick(gitDir string, paths []string, all bool, keep func(workspace.File) bool, none, notThis string) (workspace.Status, []string, error) {
	st, err := s.Status(gitDir)
	if err != nil {
		return st, nil, err
	}
	if all {
		var picked []string
		for _, f := range st.Files {
			if keep(f) {
				picked = append(picked, f.Path)
			}
		}
		if n := len(st.Conflicts()); n > 0 {
			return st, nil, fmt.Errorf("%d file(s) have conflicts: fix them first, or name the other files", n)
		}
		if len(picked) == 0 {
			return st, nil, errors.New(none)
		}
		return st, picked, nil
	}
	if len(paths) == 0 {
		return st, nil, errors.New("name the files, or use --all")
	}
	seen := map[string]bool{}
	var picked []string
	for _, p := range paths {
		c, err := workspace.CleanPath(p)
		if err != nil {
			return st, nil, err
		}
		f, ok := st.Find(c)
		if !ok {
			return st, nil, fmt.Errorf("%s has no uncommitted changes", c)
		}
		if f.Conflict {
			return st, nil, fmt.Errorf("%s has a conflict: fix it in your editor first", c)
		}
		if !keep(f) {
			return st, nil, fmt.Errorf("%s %s", c, notThis)
		}
		if !seen[c] {
			seen[c] = true
			picked = append(picked, c)
		}
	}
	return st, picked, nil
}

// run applies a change to the picked paths, or to everything with all
// (git then finds the files itself, so none is missed between reading and
// changing). Dry run only prints it.
func (s *Service) run(gitDir, verb string, picked []string, all bool, do func(wt string, paths []string) error) ([]string, error) {
	wt := s.Locator.WorktreeOf(gitDir)
	what := strings.Join(picked, ", ")
	if all {
		what = fmt.Sprintf("every change (%d file(s))", len(picked))
	}
	if s.FS.Dry(fmt.Sprintf("%s in %s: %s", verb, wt, what)) {
		return picked, nil
	}
	paths := picked
	if all {
		paths = nil
	}
	if err := do(wt, paths); err != nil {
		return nil, err
	}
	return picked, nil
}

// RepoPath turns a path typed in folder from (relative to it, or absolute)
// into the repo-relative path git prints. A path outside the repo comes
// back starting with "..", which the use cases refuse.
func (s *Service) RepoPath(gitDir, from, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(resolved(from), p)
	}
	rel, err := filepath.Rel(resolved(s.Locator.WorktreeOf(gitDir)), p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

// resolved resolves symlinks in a folder (macOS: /var is /private/var), so a
// path typed under one name matches the repo found under the other.
func resolved(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}
