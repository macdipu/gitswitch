package application

import (
	"strings"
	"testing"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

type fakeStashGit struct {
	*fakeGit
	has, dirty bool
}

func (f *fakeStashGit) HasStash(string, string) bool { return f.has }
func (f *fakeStashGit) Dirty(string) bool            { return f.dirty }
func (f *fakeStashGit) StashStore(_, h, subj string) error {
	f.ran = append(f.ran, "store "+h+" "+subj)
	f.has = true
	return nil
}
func (f *fakeStashGit) StashPopBack(_, h string) error {
	f.ran = append(f.ran, "popback "+h)
	f.has = false
	return nil
}

func TestUndoStashDrop(t *testing.T) {
	s, st, g := newService()
	sg := &fakeStashGit{fakeGit: g}
	s.Git = sg
	s.Stashed("/r/.git", s.Before("/r/.git"), undo.StashDrop, "h1", "On main: wip", "Dropped stash@{0} (wip)")
	if st.rec == nil || st.rec.Before != "h1" || st.rec.After != "h1" || st.rec.To != "On main: wip" {
		t.Fatalf("record: %+v", st.rec)
	}
	// Still undoable after switching branch: only the list changes.
	g.mark.Branch = "other"
	last, err := s.Last("/r/.git")
	if err != nil || last.Blocked != "" || !strings.Contains(last.Effect, "stash list") {
		t.Fatalf("Last: %+v %v", last, err)
	}
	if _, err := s.Undo("/r/.git"); err != nil || strings.Join(g.ran, ",") != "store h1 On main: wip" {
		t.Fatalf("Undo: %v %v", err, g.ran)
	}
	if st.rec != nil {
		t.Error("record not cleared")
	}

	// The stash is in the list already: blocked.
	s.Stashed("/r/.git", s.Before("/r/.git"), undo.StashDrop, "h1", "On main: wip", "x")
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "in the stash list again") {
		t.Errorf("already there: %v", err)
	}
}

func TestUndoStashSave(t *testing.T) {
	s, st, g := newService()
	sg := &fakeStashGit{fakeGit: g, has: true}
	s.Git = sg
	s.Stashed("/r/.git", s.Before("/r/.git"), undo.StashSave, "h2", "On main: x", "Stashed")
	sg.dirty = true
	if last, _ := s.Last("/r/.git"); !strings.Contains(last.Blocked, "uncommitted changes") {
		t.Errorf("dirty: %+v", last)
	}
	if _, err := s.Undo("/r/.git"); err == nil || len(g.ran) != 0 || st.rec == nil {
		t.Errorf("dirty undo: %v %v", err, g.ran)
	}
	sg.dirty, sg.has = false, false
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "gone from the list") {
		t.Errorf("gone: %v", err)
	}
	sg.has = true
	if _, err := s.Undo("/r/.git"); err != nil || strings.Join(g.ran, ",") != "popback h2" {
		t.Errorf("undo: %v %v", err, g.ran)
	}
}

func TestStashedWithoutHash(t *testing.T) {
	s, st, _ := newService()
	s.Stashed("/r/.git", s.Before("/r/.git"), undo.StashDrop, "", "", "x")
	if st.rec != nil {
		t.Errorf("%+v", st.rec)
	}
}
