package application

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeStore struct {
	rec   *undo.Record
	saves int
}

func (f *fakeStore) Save(_ string, r undo.Record) error { f.rec = &r; f.saves++; return nil }
func (f *fakeStore) Load(string) (undo.Record, error) {
	if f.rec == nil {
		return undo.Record{}, undo.ErrNothing
	}
	return *f.rec, nil
}
func (f *fakeStore) Clear(string) error { f.rec = nil; return nil }

type fakeGit struct {
	mark     undo.Mark
	pushed   []string
	resetErr error
	ran      []string
	tips     map[string]string // branch -> commit
}

func (f *fakeGit) Mark(string) undo.Mark                   { return f.mark }
func (f *fakeGit) Pushed(_, _, _ string) ([]string, error) { return f.pushed, nil }
func (f *fakeGit) Reset(_, hash string, keep bool) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.ran = append(f.ran, map[bool]string{true: "keep ", false: "mixed "}[keep]+hash)
	f.mark.Head = hash
	return nil
}
func (f *fakeGit) Switch(_, b string) error {
	f.ran = append(f.ran, "switch "+b)
	f.mark.Branch = b
	return nil
}

func (f *fakeGit) Tip(_, b string) string { return f.tips[b] }
func (f *fakeGit) MoveBranch(_, b, to, was string) error {
	if f.tips[b] != was {
		return errors.New("cannot lock ref: is at " + f.tips[b] + " but expected " + was)
	}
	f.ran = append(f.ran, "move "+b+" "+was+"→"+to)
	if to == "" {
		delete(f.tips, b)
	} else {
		f.tips[b] = to
	}
	return nil
}

func newService() (*Service, *fakeStore, *fakeGit) {
	st, g := &fakeStore{}, &fakeGit{mark: undo.Mark{Worktree: "/r", Branch: "main", Head: "a"}}
	return &Service{Store: st, Git: g, FS: &filesystem.FS{}, Now: func() time.Time { return time.Unix(100, 0) }}, st, g
}

// act runs an action the way the history use cases do: Before, change, Done.
func act(s *Service, g *fakeGit, a undo.Action, head, branch string) {
	m := s.Before("/r/.git")
	g.mark.Head, g.mark.Branch = head, branch
	s.Done("/r/.git", m, a, string(a)+" label")
}

func TestUndoEachAction(t *testing.T) {
	for _, c := range []struct {
		a    undo.Action
		head string
		to   string
		want string
	}{
		{undo.Drop, "b", "main", "keep a"},
		{undo.Revert, "b", "main", "keep a"},
		{undo.CherryPick, "b", "main", "keep a"},
		{undo.FixAuthor, "b", "main", "keep a"},
		{undo.Rollback, "b", "main", "mixed a"},
		{undo.Switch, "c", "feat", "switch main"},
	} {
		s, st, g := newService()
		act(s, g, c.a, c.head, c.to)
		last, err := s.Last("/r/.git")
		if err != nil || last.Blocked != "" || last.Time != 100 || last.Effect == "" {
			t.Fatalf("%s: Last = %+v, %v", c.a, last, err)
		}
		r, err := s.Undo("/r/.git")
		if err != nil || r.Action != c.a || strings.Join(g.ran, ",") != c.want {
			t.Errorf("%s: Undo = %+v, %v, ran %v", c.a, r, err, g.ran)
		}
		if st.rec != nil {
			t.Errorf("%s: the record must be forgotten after undo", c.a)
		}
		if _, err := s.Undo("/r/.git"); !errors.Is(err, undo.ErrNothing) {
			t.Errorf("%s: second undo = %v", c.a, err)
		}
	}
}

func TestNotRecordedForgetsThePrevious(t *testing.T) {
	s, st, g := newService()
	act(s, g, undo.Drop, "b", "main")
	act(s, g, undo.Revert, "b", "main") // HEAD did not move: nothing to undo
	if st.rec != nil {
		t.Fatalf("an action that cannot be undone must forget the previous one: %+v", st.rec)
	}
}

func TestUndoRefusals(t *testing.T) {
	s, _, g := newService()
	act(s, g, undo.Drop, "b", "main")
	g.mark.Head = "z" // a commit made since
	if last, _ := s.Last("/r/.git"); !strings.Contains(last.Blocked, "changed since") {
		t.Errorf("Last after a new commit: %+v", last)
	}
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "changed since") || len(g.ran) > 0 {
		t.Errorf("undo after a new commit: %v, ran %v", err, g.ran)
	}

	s, _, g = newService()
	act(s, g, undo.Revert, "b", "main")
	g.pushed = []string{"bbbbbbbbbb"}
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "already pushed (bbbbbbb)") || len(g.ran) > 0 {
		t.Errorf("undo of a pushed revert: %v, ran %v", err, g.ran)
	}

	s, st, g := newService()
	act(s, g, undo.CherryPick, "b", "main")
	g.resetErr = errors.New("error: Entry 'a.txt' not uptodate. Cannot merge.")
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "uncommitted changes in files the undo would change") {
		t.Errorf("reset refused: %v", err)
	}
	if st.rec == nil {
		t.Error("a failed undo must keep the record, so it can be tried again")
	}
}

func TestUndoDryRun(t *testing.T) {
	s, st, g := newService()
	var out bytes.Buffer
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	act(s, g, undo.Drop, "b", "main")
	if _, err := s.Undo("/r/.git"); err != nil || len(g.ran) > 0 || st.rec == nil || !strings.Contains(out.String(), "undo") {
		t.Errorf("dry run: %v, ran %v, out %q", err, g.ran, out.String())
	}
}

// A move took the commits not pushed off main onto login: undo puts them
// back on main, switches back and deletes login.
func TestUndoMove(t *testing.T) {
	s, st, g := newService()
	g.tips = map[string]string{"main": "a"}
	m := s.Before("/r/.git")
	g.mark.Branch, g.tips["login"], g.tips["main"] = "login", "a", "base"
	s.Done("/r/.git", m, undo.Move, "Moved 2 commits from main to login")
	if st.rec == nil || st.rec.To != "login" || st.rec.Before != "a" || st.rec.After != "base" {
		t.Fatalf("recorded %+v", st.rec)
	}
	last, err := s.Last("/r/.git")
	if err != nil || last.Blocked != "" || !strings.Contains(last.Effect, "deletes login") {
		t.Fatalf("last = %+v %v", last, err)
	}

	// main moved since (a pull): blocked, nothing changes.
	g.tips["main"] = "pulled"
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "main has changed since") || len(g.ran) != 0 {
		t.Fatalf("main moved: %v %v", err, g.ran)
	}
	g.tips["main"] = "base"
	// A new commit on login: blocked too.
	g.mark.Head = "new"
	if _, err := s.Undo("/r/.git"); err == nil || !strings.Contains(err.Error(), "login has changed since") {
		t.Fatalf("login moved: %v", err)
	}
	g.mark.Head = "a"

	if _, err := s.Undo("/r/.git"); err != nil || strings.Join(g.ran, ",") != "move main base→a,switch main,move login a→" ||
		g.tips["main"] != "a" || g.tips["login"] != "" || st.rec != nil {
		t.Fatalf("undo: %v %v %v %+v", err, g.ran, g.tips, st.rec)
	}

	// Nothing moved (the old branch is where it was): nothing to undo.
	s, st, g = newService()
	g.tips = map[string]string{"main": "a"}
	m = s.Before("/r/.git")
	g.mark.Branch = "login"
	s.Done("/r/.git", m, undo.Move, "x")
	if st.rec != nil {
		t.Errorf("recorded a move that moved nothing: %+v", st.rec)
	}
}
