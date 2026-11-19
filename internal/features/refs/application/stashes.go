package application

import (
	"errors"
	"fmt"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// stashRecorder is what Undo needs to remember a stash drop or save; the
// undo service implements it (checked at run time, so test fakes of
// undo.Recorder need nothing).
type stashRecorder interface {
	Stashed(gitDir string, before undo.Mark, a undo.Action, hash, subject, label string)
}

func (s *Service) stashGit() (refs.StashGit, error) {
	g, ok := s.Refs.(refs.StashGit)
	if !ok {
		return nil, errors.New("stashes are not available")
	}
	return g, nil
}

func (s *Service) stashed(gitDir string, m undo.Mark, a undo.Action, st refs.Stash, label string) {
	if s.Undo == nil {
		return
	}
	if r, ok := s.Undo.(stashRecorder); ok {
		r.Stashed(gitDir, m, a, st.Hash, st.Subject, label)
	}
}

// Stashes lists every stash entry of the repo, newest first: parked and
// discarded changes too (see Kind), with each one's files.
func (s *Service) Stashes(gitDir string) ([]refs.Stash, error) {
	g, err := s.stashGit()
	if err != nil {
		return nil, err
	}
	return g.Stashes(gitDir)
}

// findStash finds a stash entry by its commit, as it is now in the list;
// refs.ErrStashChanged when the list no longer has it.
func (s *Service) findStash(gitDir, hash string) (refs.Stash, error) {
	all, err := s.Stashes(gitDir)
	if err != nil {
		return refs.Stash{}, err
	}
	return refs.FindStash(all, hash)
}

// StashShow reads one stash entry, with its files.
func (s *Service) StashShow(gitDir, hash string) (refs.Stash, error) {
	return s.findStash(gitDir, hash)
}

// StashDiff shows one file of a stash entry (a new file's whole content),
// cut at refs.MaxStashDiffBytes.
func (s *Service) StashDiff(gitDir, hash, path string) (refs.StashDiff, error) {
	st, err := s.findStash(gitDir, hash)
	if err != nil {
		return refs.StashDiff{}, err
	}
	p, err := refs.CleanStashPath(path)
	if err != nil {
		return refs.StashDiff{}, err
	}
	found := false
	for _, f := range st.Files {
		if f.Path == p {
			found = true
		}
	}
	if !found {
		return refs.StashDiff{}, fmt.Errorf("%s is not in %s", p, st.Ref)
	}
	g, _ := s.stashGit()
	d, err := g.StashDiff(gitDir, st.Hash, p)
	if err != nil {
		return d, err
	}
	if len(d.Text) > refs.MaxStashDiffBytes {
		d.Text, d.Truncated = d.Text[:refs.MaxStashDiffBytes], true
	}
	return d, nil
}

// StashSave stashes the working changes (all of them, or only the chosen
// paths, literally), with an optional message. Refused in the middle of a
// merge or rebase, with unresolved conflicts, and when there is nothing to
// stash. Undo takes it back when it was a plain stash of everything (no
// paths, no keep-index) and the working folder is still clean.
func (s *Service) StashSave(gitDir string, o refs.StashOptions) (refs.Stash, error) {
	g, err := s.stashGit()
	if err != nil {
		return refs.Stash{}, err
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return refs.Stash{}, err
	}
	o.Message = strings.TrimSpace(o.Message)
	if refs.ReservedStashMessage(o.Message) {
		return refs.Stash{}, errors.New(`start the message with other words: "gitswitch park:" and "gitswitch discard:" are kept for changes GitSwitch parks or keeps`)
	}
	if !h.Changes.Any() {
		return refs.Stash{}, errors.New("there is nothing to stash: no uncommitted changes")
	}
	if h.Changes.Conflicts > 0 {
		return refs.Stash{}, fmt.Errorf("%s have unresolved conflicts; fix them first", plural(h.Changes.Conflicts, "file"))
	}
	var paths []string
	for _, p := range o.Paths {
		c, err := refs.CleanStashPath(p)
		if err != nil {
			return refs.Stash{}, err
		}
		paths = append(paths, c)
	}
	o.Paths = paths
	if s.FS.Dry("stash the uncommitted changes of " + s.Locator.WorktreeOf(gitDir) + " (git stash push)") {
		return refs.Stash{}, nil
	}
	m := s.before(gitDir)
	hash, err := g.StashPush(s.Locator.WorktreeOf(gitDir), o)
	if err != nil {
		return refs.Stash{}, err
	}
	st, err := s.findStash(gitDir, hash)
	if err != nil {
		return refs.Stash{}, err
	}
	if len(o.Paths) == 0 && !o.KeepIndex {
		s.stashed(gitDir, m, undo.StashSave, st, "Stashed the changes on "+orDetachedHEAD(h.Branch))
	}
	return st, nil
}

func orDetachedHEAD(b string) string {
	if b == "" {
		return "the detached HEAD"
	}
	return b
}

// StashApply puts a stash's changes in the working folder and keeps the
// entry. It needs a clean working folder, like RestoreParked, so a clash can
// be undone completely: then nothing changes and the stash stays.
func (s *Service) StashApply(gitDir, hash string) error { return s.applyStash(gitDir, hash, false) }

// StashPop is StashApply that removes the entry once it went in.
func (s *Service) StashPop(gitDir, hash string) error { return s.applyStash(gitDir, hash, true) }

func (s *Service) applyStash(gitDir, hash string, drop bool) error {
	h, err := s.ready(gitDir)
	if err != nil {
		return err
	}
	st, err := s.findStash(gitDir, hash)
	if err != nil {
		return err
	}
	if h.Changes.Any() {
		return fmt.Errorf("there are uncommitted changes on %s (%s). Commit or park them first, so the stash doesn't mix with them", orDetachedHEAD(h.Branch), strings.TrimPrefix(h.Changes.Describe(), "uncommitted: "))
	}
	verb := "apply"
	if drop {
		verb = "pop"
	}
	if s.FS.Dry(fmt.Sprintf("%s %s (git stash %s)", verb, st.Ref, verb)) {
		return nil
	}
	wt := s.Locator.WorktreeOf(gitDir)
	files, err := s.Refs.Apply(wt, st.Hash)
	if err != nil {
		return fmt.Errorf("could not %s %s, so nothing changed: %v", verb, st.Ref, err)
	}
	if len(files) > 0 {
		if derr := s.Refs.Discard(wt, st.Hash); derr != nil {
			return fmt.Errorf("%s clashes with %s in %s, and the half-applied files could not be cleaned up (%v). The stash is untouched (`git stash list`); `git reset --hard` cleans up the files", st.Ref, orDetachedHEAD(h.Branch), strings.Join(files, ", "), derr)
		}
		return fmt.Errorf("%s clashes with %s in %s, so nothing changed and the stash stays", st.Ref, orDetachedHEAD(h.Branch), strings.Join(files, ", "))
	}
	if drop {
		return s.Refs.DropStash(wt, st.Hash)
	}
	return nil
}

// StashDrop deletes a stash entry. Undo brings it back (the commit stays
// reachable through the undo record).
func (s *Service) StashDrop(gitDir, hash string) error {
	st, err := s.findStash(gitDir, hash)
	if err != nil {
		return err
	}
	if s.FS.Dry(fmt.Sprintf("delete %s (git stash drop)", st.Ref)) {
		return nil
	}
	m := s.before(gitDir)
	if err := s.Refs.DropStash(s.Locator.WorktreeOf(gitDir), st.Hash); err != nil {
		return err
	}
	s.stashed(gitDir, m, undo.StashDrop, st, "Dropped "+st.Label())
	return nil
}

// StashBranch makes a new branch from a stash: at the commit it was made on,
// with its changes there, dropping the entry. Needs a clean working folder.
func (s *Service) StashBranch(gitDir, hash, name string) error {
	g, err := s.stashGit()
	if err != nil {
		return err
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return err
	}
	st, err := s.findStash(gitDir, hash)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if err := s.Refs.CheckName(name); err != nil {
		return err
	}
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return err
	}
	if b := refs.Find(all, name); b != nil && !b.Remote {
		return fmt.Errorf("there is a branch %s already", name)
	}
	if h.Changes.Any() {
		return fmt.Errorf("there are uncommitted changes on %s (%s). Commit or park them first, so the stash doesn't mix with them", orDetachedHEAD(h.Branch), strings.TrimPrefix(h.Changes.Describe(), "uncommitted: "))
	}
	if s.FS.Dry(fmt.Sprintf("make branch %s from %s (git stash branch)", name, st.Ref)) {
		return nil
	}
	return g.StashBranch(s.Locator.WorktreeOf(gitDir), st.Hash, name)
}
