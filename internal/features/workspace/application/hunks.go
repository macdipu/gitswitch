package application

import (
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// HunksChanged says what a hunk or line action did.
type HunksChanged struct {
	Path    string `json:"path"`
	Lines   int    `json:"lines"`             // changed lines moved (added and deleted)
	Stash   string `json:"stash,omitempty"`   // discard only: the stash that keeps what was removed
	Restore string `json:"restore,omitempty"` // discard only: the git command that brings the file back
}

// RestoreHint is the git command that puts a file back as it was before a
// discard of hunks or lines (the newest stash).
func RestoreHint(path string) string {
	if strings.ContainsAny(path, " '\"$&;()<>*?[]{}!\\") {
		path = "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}
	return "git restore --source='stash@{0}' -- " + path
}

// StageHunks adds the picked hunks or lines of one file's unstaged change
// to the next commit. A new file is added to git first (intent to add), so
// its lines can be staged one by one. fingerprint is the one the diff came
// with: when the file has changed since, nothing is done.
func (s *Service) StageHunks(gitDir, path, fingerprint string, sel workspace.Selection) (HunksChanged, error) {
	return s.hunkAction(gitDir, path, fingerprint, sel, false, "stage", func(f workspace.File) error {
		if !f.IsChanged() {
			return errors.New("has no change that is not staged")
		}
		return nil
	})
}

// UnstageHunks takes the picked hunks or lines of one file's staged change
// out of the next commit, keeping them in the file.
func (s *Service) UnstageHunks(gitDir, path, fingerprint string, sel workspace.Selection) (HunksChanged, error) {
	return s.hunkAction(gitDir, path, fingerprint, sel, true, "unstage", func(f workspace.File) error {
		if !f.IsStaged() {
			return errors.New("is not staged")
		}
		return nil
	})
}

// DiscardHunks removes the picked hunks or lines of one file's unstaged
// change from the working folder. Nothing is lost: the file as it was is
// kept first as a stash named "gitswitch discard: <branch>" that holds only
// that file; RestoreHint says how to bring it back. A new
// (untracked) file cannot be discarded in parts; discard the whole file.
func (s *Service) DiscardHunks(gitDir, path, fingerprint string, sel workspace.Selection) (HunksChanged, error) {
	return s.hunkAction(gitDir, path, fingerprint, sel, false, "discard", func(f workspace.File) error {
		switch {
		case f.New():
			return errors.New("is a new file: discard the whole file")
		case !f.IsChanged():
			return errors.New("has no change that is not staged")
		}
		return nil
	})
}

func (s *Service) hunkAction(gitDir, path, fingerprint string, sel workspace.Selection, staged bool, verb string, ok func(workspace.File) error) (HunksChanged, error) {
	var res HunksChanged
	p, err := workspace.CleanPath(path)
	if err != nil {
		return res, err
	}
	res.Path = p
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return res, err
	}
	if h.Busy != "" {
		return res, remote.Busy(h.Busy)
	}
	if verb == "discard" && h.Hash == "" {
		return res, errors.New("this repo has no commit yet, so there is nothing to go back to")
	}
	files, err := s.Changes.Status(gitDir)
	if err != nil {
		return res, err
	}
	st := workspace.Status{Files: files}
	f, found := st.Find(p)
	switch {
	case !found:
		return res, fmt.Errorf("%s has no uncommitted changes", p)
	case f.Conflict:
		return res, fmt.Errorf("%s has a conflict: fix it in your editor first", p)
	}
	if err := ok(f); err != nil {
		return res, fmt.Errorf("%s %v", p, err)
	}
	// The diff as it is now must be the one the person picked from.
	raw, err := s.Changes.Diff(gitDir, staged, []string{p})
	if err != nil {
		return res, err
	}
	if len(raw) > workspace.MaxDiffBytes {
		return res, errors.New("this diff is too big to pick lines from: use the whole file")
	}
	if fingerprint == "" || workspace.Fingerprint(raw) != fingerprint {
		return res, workspace.ErrStale
	}
	fd, err := workspace.ParseDiff(raw)
	if err != nil {
		return res, err
	}
	reverse := staged || verb == "discard"
	patch, whole, err := workspace.BuildPatch(fd, sel, reverse)
	if err != nil {
		return res, err
	}
	if verb == "discard" && fd.Deletes && !whole {
		return res, errors.New("this file is deleted: discard the whole file to bring it back")
	}
	res.Lines = changedLines(fd, sel)
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("%s %d line(s) of %s in %s", verb, res.Lines, p, wt)) {
		return res, nil
	}
	switch verb {
	case "stage":
		if f.New() {
			if err := s.Changes.IntentToAdd(wt, p); err != nil {
				return res, err
			}
		}
		err = s.Changes.Apply(wt, patch, workspace.ApplyOptions{Cached: true})
		if err != nil && f.New() { // leave the file as it was: new, nothing staged
			_ = s.Changes.Unstage(wt, []string{p})
		}
	case "unstage":
		err = s.Changes.Apply(wt, patch, workspace.ApplyOptions{Cached: true, Reverse: true})
	default:
		if err = s.Changes.Apply(wt, patch, workspace.ApplyOptions{Reverse: true, Check: true}); err != nil {
			return res, err
		}
		res.Stash = workspace.DiscardMessage(h.Branch)
		if err = s.Changes.Backup(wt, res.Stash, p); err != nil {
			return res, fmt.Errorf("could not keep a copy first, so nothing was discarded: %v", err)
		}
		err = s.Changes.Apply(wt, patch, workspace.ApplyOptions{Reverse: true})
	}
	if err != nil {
		res.Stash = ""
	} else if res.Stash != "" {
		res.Restore = RestoreHint(p)
	}
	return res, err
}

// changedLines counts the added and deleted lines a selection names.
func changedLines(fd workspace.FileDiff, sel workspace.Selection) int {
	picked := map[[2]int]bool{}
	for _, i := range sel.Hunks {
		if i >= 0 && i < len(fd.Hunks) {
			for j, l := range fd.Hunks[i].Lines {
				if l.Changed() {
					picked[[2]int{i, j}] = true
				}
			}
		}
	}
	for i, idx := range sel.Lines {
		for _, j := range idx {
			picked[[2]int{i, j}] = true
		}
	}
	return len(picked)
}
