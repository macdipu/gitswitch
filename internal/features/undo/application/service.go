// Package application holds the undo use cases: remember the last action
// GitSwitch made in a repo, and undo it when nothing has happened since.
// It depends only on domain ports. Import it as undoapp.
package application

import (
	"errors"
	"fmt"
	"strings"
	"time"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the undo use cases. It is also the Recorder the history and
// branch use cases report their actions to.
type Service struct {
	Store undo.Store
	Git   undo.Git
	FS    *filesystem.FS // dry-run: an undo is only printed
	Now   func() time.Time
}

// Before reads where the repo is before an action.
func (s *Service) Before(gitDir string) undo.Mark { return s.Git.Mark(gitDir) }

// Done remembers an action that succeeded, replacing the repo's previous
// one. An action that cannot be undone (HEAD did not move) forgets the
// previous one, so Undo never goes back past it. Recording is best effort:
// the action itself already succeeded.
func (s *Service) Done(gitDir string, before undo.Mark, a undo.Action, label string) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	r, ok := undo.New(a, label, before, s.Git.Mark(gitDir), now().Unix())
	if !ok || s.Store.Save(gitDir, r) != nil {
		_ = s.Store.Clear(gitDir)
	}
}

// Status is the last action and whether it can be undone now.
type Status struct {
	undo.Record
	Effect  string `json:"effect"`  // what undoing does, in plain words
	Blocked string `json:"blocked"` // why it cannot be undone now; "" when it can
}

// Last reads the repo's last action; undo.ErrNothing when there is none.
func (s *Service) Last(gitDir string) (Status, error) {
	r, err := s.Store.Load(gitDir)
	if err != nil {
		return Status{}, err
	}
	st := Status{Record: r, Effect: r.Effect()}
	if err := s.check(gitDir, r); err != nil {
		st.Blocked = err.Error()
	}
	return st, nil
}

func (s *Service) check(gitDir string, r undo.Record) error {
	if err := r.Check(s.Git.Mark(gitDir)); err != nil {
		return err
	}
	if !r.Rewinds() {
		return nil
	}
	// Moving the branch back to Before removes the commits only After has.
	pushed, err := s.Git.Pushed(gitDir, r.Before, r.After)
	if err != nil {
		return err
	}
	if len(pushed) > 0 {
		return fmt.Errorf("the commits undoing would remove are already pushed (%s), so it would rewrite pushed history; revert them instead", short(pushed))
	}
	return nil
}

// Undo undoes the repo's last action and forgets it. It returns what was
// undone.
func (s *Service) Undo(gitDir string) (undo.Record, error) {
	r, err := s.Store.Load(gitDir)
	if err != nil {
		return r, err
	}
	if err := s.check(gitDir, r); err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("undo %q in %s: %s", r.Label, r.Worktree, r.Effect())) {
		return r, nil
	}
	switch r.Action {
	case undo.Switch:
		err = s.Git.Switch(r.Worktree, r.Branch)
	case undo.Rollback:
		// The files already hold the rolled-back commits' changes; only the
		// branch moves.
		err = s.Git.Reset(r.Worktree, r.Before, false)
	default:
		err = s.Git.Reset(r.Worktree, r.Before, true)
	}
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not uptodate") || strings.Contains(low, "would be overwritten") {
			return r, fmt.Errorf("nothing changed: you have uncommitted changes in files the undo would change. Commit or stash them first, then undo again.\n\n%v", err)
		}
		return r, fmt.Errorf("undo failed, nothing changed: %v", err)
	}
	if err := s.Store.Clear(gitDir); err != nil {
		return r, err
	}
	return r, nil
}

// Forget drops the repo's last action without undoing it.
func (s *Service) Forget(gitDir string) error {
	if _, err := s.Store.Load(gitDir); errors.Is(err, undo.ErrNothing) {
		return err
	}
	return s.Store.Clear(gitDir)
}

func short(hashes []string) string {
	var out []string
	for i, h := range hashes {
		if i == 3 {
			out = append(out, fmt.Sprintf("and %d more", len(hashes)-3))
			break
		}
		out = append(out, h[:min(7, len(h))])
	}
	return strings.Join(out, ", ")
}
