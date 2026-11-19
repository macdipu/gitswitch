package application

import (
	"errors"
	"strings"
	"testing"
	"time"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// redoStore is fakeStore that also keeps the undone record, and forgets it
// on a new action, like the real one.
type redoStore struct {
	fakeStore
	redo *undo.Record
}

func (f *redoStore) Save(g string, r undo.Record) error { f.redo = nil; return f.fakeStore.Save(g, r) }
func (f *redoStore) Clear(g string) error               { f.redo = nil; return f.fakeStore.Clear(g) }
func (f *redoStore) SaveRedo(_ string, r undo.Record) error {
	f.redo = &r
	return nil
}
func (f *redoStore) LoadRedo(string) (undo.Record, error) {
	if f.redo == nil {
		return undo.Record{}, undo.ErrNothingToRedo
	}
	return *f.redo, nil
}
func (f *redoStore) ClearRedo(string) error { f.redo = nil; return nil }

// redoGit is fakeGit with what redoing and stashes need.
type redoGit struct {
	*fakeGit
	gone    map[string]bool // commits git cleaned up
	stashes map[string]bool
}

func (f *redoGit) HasCommit(_, h string) bool { return !f.gone[h] }
func (f *redoGit) DeleteBranches(_ string, ds []undo.Deleted) error {
	for _, d := range ds {
		f.ran = append(f.ran, "delete "+d.Name)
		delete(f.tips, d.Name)
	}
	return nil
}
func (f *redoGit) DropStash(_, h string) error {
	f.ran = append(f.ran, "dropstash "+h)
	delete(f.stashes, h)
	return nil
}
func (f *redoGit) HasStash(_, h string) bool       { return f.stashes[h] }
func (f *redoGit) Dirty(string) bool               { return false }
func (f *redoGit) StashStore(_, h, _ string) error { f.stashes[h] = true; return nil }
func (f *redoGit) StashPopBack(_, h string) error  { delete(f.stashes, h); return nil }

func newRedoService() (*Service, *redoStore, *redoGit) {
	st := &redoStore{}
	g := &redoGit{fakeGit: &fakeGit{mark: undo.Mark{Worktree: "/r", Branch: "main", Head: "a"}, tips: map[string]string{}},
		gone: map[string]bool{}, stashes: map[string]bool{}}
	return &Service{Store: st, Git: g, FS: &filesystem.FS{}, Now: func() time.Time { return time.Unix(100, 0) }}, st, g
}

const gd = "/r/.git"

func TestRedoAfterUndoOfEachHeadMove(t *testing.T) {
	for _, c := range []struct {
		a    undo.Action
		want string // what redo runs
	}{
		{undo.Commit, "mixed b"}, {undo.Amend, "mixed b"}, {undo.Rollback, "mixed b"},
		{undo.Drop, "keep b"}, {undo.Revert, "keep b"}, {undo.CherryPick, "keep b"}, {undo.FixAuthor, "keep b"},
	} {
		s, st, g := newRedoService()
		if _, err := s.NextRedo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
			t.Fatalf("%s: before any undo: %v", c.a, err)
		}
		act(s, g.fakeGit, c.a, "b", "main")
		if _, err := s.Undo(gd); err != nil {
			t.Fatalf("%s: undo: %v", c.a, err)
		}
		if st.redo == nil {
			t.Fatalf("%s: undo kept nothing for redo", c.a)
		}
		g.ran = nil
		rs, err := s.NextRedo(gd)
		if err != nil || rs.Blocked != "" || rs.Effect == "" {
			t.Fatalf("%s: NextRedo = %+v, %v", c.a, rs, err)
		}
		r, err := s.Redo(gd)
		if err != nil || r.Action != c.a || strings.Join(g.ran, ",") != c.want {
			t.Errorf("%s: Redo = %+v, %v, ran %v", c.a, r, err, g.ran)
		}
		if st.redo != nil {
			t.Errorf("%s: redo record must be forgotten after redo", c.a)
		}
		// The action is the last one again: it can be undone once more.
		if last, err := s.Last(gd); err != nil || last.Blocked != "" || last.Action != c.a {
			t.Errorf("%s: Last after redo = %+v, %v", c.a, last, err)
		}
		if _, err := s.Redo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
			t.Errorf("%s: second redo: %v", c.a, err)
		}
	}
}

func TestRedoRefusesWhenTheRepoMovedOn(t *testing.T) {
	s, _, g := newRedoService()
	act(s, g.fakeGit, undo.Commit, "b", "main")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	// A new commit after the undo.
	g.mark.Head = "z"
	rs, _ := s.NextRedo(gd)
	if !strings.Contains(rs.Blocked, "has changed since the undo") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
	if _, err := s.Redo(gd); err == nil || len(g.ran) > 1 {
		t.Errorf("Redo must refuse and change nothing: %v %v", err, g.ran)
	}
	// Another branch.
	g.mark.Head, g.mark.Branch = "a", "other"
	if rs, _ = s.NextRedo(gd); !strings.Contains(rs.Blocked, "switch to main") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
	// Busy.
	g.mark.Branch, g.mark.Busy = "main", "rebase"
	if rs, _ = s.NextRedo(gd); rs.Blocked == "" {
		t.Error("a stopped operation must block redo")
	}
}

func TestRedoRefusesPushedAndCleanedUpCommits(t *testing.T) {
	s, _, g := newRedoService()
	act(s, g.fakeGit, undo.Drop, "b", "main")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	g.pushed = []string{"aaaaaaaaaa"} // the dropped commit was pushed since
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "pushed") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
	g.pushed = nil
	g.gone["b"] = true
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "no longer in the repo") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
}

func TestRedoSwitch(t *testing.T) {
	s, _, g := newRedoService()
	g.tips["feat"] = "c"
	act(s, g.fakeGit, undo.Switch, "c", "feat")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	g.ran = nil
	if _, err := s.Redo(gd); err != nil || strings.Join(g.ran, ",") != "switch feat" {
		t.Errorf("Redo: %v %v", err, g.ran)
	}
	// feat deleted before the redo.
	s, _, g = newRedoService()
	g.tips["feat"] = "c"
	act(s, g.fakeGit, undo.Switch, "c", "feat")
	_, _ = s.Undo(gd)
	delete(g.tips, "feat")
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "no branch feat") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
}

func TestRedoRename(t *testing.T) {
	s, _, g := newRedoService()
	g.tips["new"] = "a"
	s.Renamed(gd, s.Before(gd), "old", "new", "a", "Renamed old to new")
	if _, err := s.Undo(gd); err != nil { // renames new back to old
		t.Fatal(err)
	}
	g.ran = nil
	if _, err := s.Redo(gd); err != nil || strings.Join(g.ran, ",") != "rename old new" {
		t.Errorf("Redo: %v %v", err, g.ran)
	}
	// The name taken again.
	s, _, g = newRedoService()
	g.tips["new"] = "a"
	s.Renamed(gd, s.Before(gd), "old", "new", "a", "Renamed")
	_, _ = s.Undo(gd)
	g.tips["new"] = "q"
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "a branch new again") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
}

func TestRedoPruneDeletesBranchesAgain(t *testing.T) {
	s, _, g := newRedoService()
	ds := []undo.Deleted{{Name: "x", Hash: "h1"}, {Name: "y", Hash: "h2"}}
	s.Pruned(gd, s.Before(gd), ds, "Deleted 2 branches")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	if g.tips["x"] != "h1" || g.tips["y"] != "h2" {
		t.Fatalf("undo did not bring them back: %v", g.tips)
	}
	if _, err := s.Redo(gd); err != nil || len(g.tips) != 0 {
		t.Errorf("Redo: %v, tips %v", err, g.tips)
	}
	// Moved since the undo: not deleted.
	_, _ = s.Undo(gd)
	g.tips["x"] = "other"
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "not where the undo left it") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
	// Checked out now.
	g.tips["x"] = "h1"
	g.mark.Branch = "x"
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "cannot be deleted") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
}

func TestRedoMove(t *testing.T) {
	s, _, g := newRedoService()
	g.tips["main"] = "a"
	g.mark = undo.Mark{Worktree: "/r", Branch: "main", Head: "a"}
	// The move: commits (head a) went to feat, main went back to z.
	m := s.Before(gd)
	g.mark.Branch = "feat"
	g.tips["feat"], g.tips["main"] = "a", "z"
	s.Done(gd, m, undo.Move, "Moved")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	if g.mark.Branch != "main" || g.tips["main"] != "a" || g.tips["feat"] != "" {
		t.Fatalf("undo: %+v %v", g.mark, g.tips)
	}
	g.ran = nil
	if _, err := s.Redo(gd); err != nil {
		t.Fatal(err)
	}
	if g.mark.Branch != "feat" || g.tips["feat"] != "a" || g.tips["main"] != "z" {
		t.Errorf("redo: %+v %v (ran %v)", g.mark, g.tips, g.ran)
	}
	// feat exists again: refused.
	_, _ = s.Undo(gd)
	g.tips["feat"] = "q"
	if rs, _ := s.NextRedo(gd); !strings.Contains(rs.Blocked, "a branch feat again") {
		t.Errorf("Blocked = %q", rs.Blocked)
	}
}

func TestRedoStashDropAndStashSave(t *testing.T) {
	s, _, g := newRedoService()
	g.stashes["s1"] = true
	s.Stashed(gd, s.Before(gd), undo.StashDrop, "s1", "On main: wip", "Dropped stash")
	delete(g.stashes, "s1")
	if _, err := s.Undo(gd); err != nil || !g.stashes["s1"] {
		t.Fatalf("undo: %v", err)
	}
	if _, err := s.Redo(gd); err != nil || g.stashes["s1"] {
		t.Errorf("redo drop: %v %v", err, g.stashes)
	}
	// A stash save cannot be redone, and says why.
	s, _, g = newRedoService()
	g.stashes["s2"] = true
	s.Stashed(gd, s.Before(gd), undo.StashSave, "s2", "On main: wip", "Stashed")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	rs, err := s.NextRedo(gd)
	if err != nil || !strings.Contains(rs.Blocked, "not remembered") {
		t.Errorf("NextRedo = %+v, %v", rs, err)
	}
	if _, err := s.Redo(gd); err == nil || !strings.Contains(err.Error(), "can't be redone") {
		t.Errorf("Redo = %v", err)
	}
}

func TestNewActionForgetsTheRedo(t *testing.T) {
	s, st, g := newRedoService()
	act(s, g.fakeGit, undo.Commit, "b", "main")
	_, _ = s.Undo(gd)
	act(s, g.fakeGit, undo.Commit, "c", "main")
	if st.redo != nil {
		t.Error("a new action must forget the redo")
	}
}

func TestRedoDryRunChangesNothing(t *testing.T) {
	s, st, g := newRedoService()
	var out strings.Builder
	act(s, g.fakeGit, undo.Commit, "b", "main")
	_, _ = s.Undo(gd)
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	g.ran = nil
	if _, err := s.Redo(gd); err != nil || len(g.ran) != 0 || st.redo == nil || !strings.Contains(out.String(), "redo") {
		t.Errorf("dry run: %v %v %v %q", err, g.ran, st.redo, out.String())
	}
	// And an undo in dry-run keeps no redo.
	s2, st2, g2 := newRedoService()
	act(s2, g2.fakeGit, undo.Commit, "b", "main")
	s2.FS = &filesystem.FS{DryRun: true, Out: &out}
	_, _ = s2.Undo(gd)
	if st2.redo != nil {
		t.Error("a dry-run undo must not keep a redo")
	}
}

func TestRedoWithoutRedoStoreIsNothing(t *testing.T) {
	s, _, g := newService() // the plain fakes
	act(s, g, undo.Commit, "b", "main")
	if _, err := s.Undo(gd); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NextRedo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
		t.Errorf("NextRedo: %v", err)
	}
	if _, err := s.Redo(gd); !errors.Is(err, undo.ErrNothingToRedo) {
		t.Errorf("Redo: %v", err)
	}
}
