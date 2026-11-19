package application

import (
	"errors"
	"fmt"
	"strings"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Ignore adds a path (kind file), every file with its extension (ext) or
// its folder (folder) to the repo's root .gitignore, or with local to
// .git/info/exclude, which is for this clone only. A line already there is
// not added twice. Git keeps following a file it already tracks whatever
// the ignore files say: the result lists such files, and with untrack they
// are taken out of git (git rm --cached), the files kept. It refuses paths
// outside the repo and inside .git.
func (s *Service) Ignore(gitDir, path string, kind workspace.IgnoreKind, local, untrack bool) (workspace.Ignored, error) {
	r := workspace.Ignored{Kind: string(kind), Tracked: []string{}, Untracked: []string{}}
	if s.Ignorer == nil {
		return r, errors.New("ignoring files is not available here")
	}
	typedDir := strings.HasSuffix(strings.TrimSpace(path), "/") || strings.HasSuffix(strings.TrimSpace(path), `\`)
	p, err := workspace.CleanPath(path)
	if err != nil {
		return r, err
	}
	if p == ".git" || strings.HasPrefix(p, ".git/") {
		return r, errors.New("files in .git are git's own; there is nothing to ignore there")
	}
	r.Path = p
	isDir := typedDir || s.Ignorer.IsDir(gitDir, p)
	if r.Pattern, err = workspace.IgnorePattern(p, kind, isDir); err != nil {
		return r, err
	}
	target, err := workspace.IgnoreTarget(p, kind, isDir)
	if err != nil {
		return r, err
	}
	r.Where = workspace.GitignoreName
	if local {
		r.Where = workspace.ExcludeName
	}
	if r.File, err = s.Ignorer.IgnoreFile(gitDir, local); err != nil {
		return r, err
	}
	content, err := s.Ignorer.ReadIgnore(r.File)
	if err != nil {
		return r, err
	}
	var updated string
	updated, r.Added = workspace.AddIgnoreLine(content, r.Pattern)
	tracked, err := s.Ignorer.Tracked(gitDir, target)
	if err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("add %q to %s", r.Pattern, r.File)) {
		if untrack && len(tracked) > 0 {
			s.FS.Dry(fmt.Sprintf("git rm --cached -r -- %s (the files stay)", target))
			r.Untracked = tracked
		} else {
			r.Tracked = tracked
		}
		return r, nil
	}
	if r.Added {
		if err := s.Ignorer.WriteIgnore(r.File, updated); err != nil {
			return r, err
		}
	}
	if untrack && len(tracked) > 0 {
		if err := s.Ignorer.Untrack(s.Locator.WorktreeOf(gitDir), target); err != nil {
			return r, err
		}
		r.Untracked = tracked
		if tracked, err = s.Ignorer.Tracked(gitDir, target); err != nil {
			return r, err
		}
	}
	r.Tracked = tracked
	if r.Ignored, err = s.Ignorer.Ignored(gitDir, p); err != nil {
		return r, err
	}
	r.Note = ignoreNote(r, target)
	return r, nil
}

// ignoreNote says what to know about an ignore: tracked files it can't
// ignore, files taken out of git, a line already there.
func ignoreNote(r workspace.Ignored, target string) string {
	var notes []string
	if !r.Added {
		notes = append(notes, fmt.Sprintf("%s already had the line %s.", r.Where, r.Pattern))
	}
	switch n := len(r.Tracked); {
	case n == 1 && r.Tracked[0] == target:
		notes = append(notes, fmt.Sprintf("%s is tracked by git, so ignoring it changes nothing: git keeps following it. Untrack it to stop (git rm --cached; the file stays in your folder).", target))
	case n > 0:
		notes = append(notes, fmt.Sprintf("%d file(s) in %s are tracked by git, so ignoring them changes nothing: git keeps following them. Untrack them to stop (git rm --cached; the files stay in your folder).", n, target))
	}
	if n := len(r.Untracked); n > 0 {
		notes = append(notes, fmt.Sprintf("Took %d file(s) out of git; they stay in your folder. Commit to record it: when others pull that commit, git deletes the file(s) from their folders.", n))
	}
	if !r.Ignored && len(r.Tracked) == 0 {
		notes = append(notes, fmt.Sprintf("git still does not ignore %s: a later line starting with ! may bring it back.", r.Path))
	}
	return strings.Join(notes, " ")
}
