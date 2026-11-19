// Package application holds the inspect use cases: compare two commits,
// branches or tags, read the reflog and recover a branch from it, and run
// a guided bisect. They change nothing except the branch a recovery makes
// and the checkouts of a bisect. They depend only on domain ports. Import
// it as inspectapp.
package application

import (
	"fmt"
	"strings"

	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the inspect use cases.
type Service struct {
	Git     inspect.Git
	Locator repo.Locator
	FS      *filesystem.FS // dry-run: changes are only printed
}

// sides resolves both sides of a comparison to commits. tip is what the
// commit lists use for B (HEAD's commit when B is the working folder).
type sides struct{ a, b, to string }

func (s *Service) resolve(gitDir, a, b string) (sides, error) {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if b == "" {
		b = "HEAD"
	}
	var r sides
	if err := inspect.CheckSide(a, false); err != nil {
		return r, err
	}
	if err := inspect.CheckSide(b, true); err != nil {
		return r, err
	}
	var err error
	if r.a, err = s.Git.Resolve(gitDir, a); err != nil {
		return r, err
	}
	if b == inspect.Working {
		r.b, err = s.Git.Resolve(gitDir, "HEAD")
		if err != nil {
			return r, fmt.Errorf("this repo has no commits yet, so there is nothing to compare")
		}
		return r, nil
	}
	if r.b, err = s.Git.Resolve(gitDir, b); err != nil {
		return r, err
	}
	r.to = r.b
	return r, nil
}

// from is the side the changed files are counted from: where the two went
// apart (since-fork) or A itself (direct).
func (s *Service) from(gitDir string, r sides, mode inspect.Mode, a, b string) (string, error) {
	if mode == inspect.Direct {
		return r.a, nil
	}
	base := s.Git.MergeBase(gitDir, r.a, r.b)
	if base == "" {
		return "", fmt.Errorf("%s and %s share no history, so there is no point where they went apart; compare them directly instead", a, b)
	}
	return base, nil
}

// Compare says how B differs from A: the commits only on each side (the
// newest MaxCommits, with counts of all) and the files that differ, since
// they went apart or directly. B defaults to HEAD; inspect.Working as B is
// the working folder.
func (s *Service) Compare(gitDir, a, b string, mode inspect.Mode) (inspect.Compared, error) {
	if b = strings.TrimSpace(b); b == "" {
		b = "HEAD"
	}
	c := inspect.Compared{A: strings.TrimSpace(a), B: b, Mode: mode, Working: b == inspect.Working}
	r, err := s.resolve(gitDir, a, b)
	if err != nil {
		return c, err
	}
	c.AHash, c.BHash = r.a, r.b
	c.Base = s.Git.MergeBase(gitDir, r.a, r.b)
	if c.OnlyB, c.OnlyBCount, err = s.Git.Commits(gitDir, r.b, r.a, inspect.MaxCommits); err != nil {
		return c, err
	}
	if c.OnlyA, c.OnlyACount, err = s.Git.Commits(gitDir, r.a, r.b, inspect.MaxCommits); err != nil {
		return c, err
	}
	from, err := s.from(gitDir, r, mode, a, b)
	if err != nil {
		return c, err
	}
	if c.Files, err = s.Git.Changes(gitDir, from, r.to); err != nil {
		return c, err
	}
	c.Finish()
	return c, nil
}

// CompareFile is one file's diff between the two sides, cut at
// MaxDiffBytes. A binary file is refused: there is no text to show.
func (s *Service) CompareFile(gitDir, a, b string, mode inspect.Mode, path string) (inspect.Diff, error) {
	if b = strings.TrimSpace(b); b == "" {
		b = "HEAD"
	}
	d := inspect.Diff{A: strings.TrimSpace(a), B: b, Mode: mode, Path: path}
	if err := checkPath(path); err != nil {
		return d, err
	}
	r, err := s.resolve(gitDir, a, b)
	if err != nil {
		return d, err
	}
	from, err := s.from(gitDir, r, mode, a, b)
	if err != nil {
		return d, err
	}
	text, err := s.Git.FileDiff(gitDir, from, r.to, path)
	if err != nil {
		return d, err
	}
	if inspect.IsBinaryDiff(text) {
		return d, fmt.Errorf("%s is a binary file, so there is no text to compare", path)
	}
	d.Text = text
	if len(d.Text) > inspect.MaxDiffBytes {
		d.Text, d.Truncated = d.Text[:inspect.MaxDiffBytes], true
	}
	return d, nil
}

// checkPath refuses an empty path, an absolute one or one leaving the repo.
func checkPath(p string) error {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("name the file to compare")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, ":") {
		return fmt.Errorf("%q must be a path inside the repo", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("%q must be a path inside the repo", p)
		}
	}
	return nil
}
