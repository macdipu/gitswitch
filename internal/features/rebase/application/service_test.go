package application

import (
	"errors"
	"strings"
	"testing"

	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// fakeGit: feature at "h" (3 own commits), main at "m" (2 new), base "b".
type fakeGit struct {
	head       refs.Head
	base       string
	merges     int
	pushed     []string
	op         string
	conflicts  []string
	rebaseErr  error
	ranRebase  []string
	aborted    bool
	stopOrig   string
	stopOnto   string
	counts     map[string]int // "from>to" -> n
	afterHead  string
	headAfter  bool
	unresolved bool
}

func (f *fakeGit) Head(string) (refs.Head, error) { return f.head, nil }
func (f *fakeGit) Resolve(_, rev string) (string, error) {
	if rev == "main" || rev == "m" {
		return "m", nil
	}
	return "", errors.New("there is no branch, tag or commit called " + rev)
}
func (f *fakeGit) MergeBase(string, string, string) string { return f.base }
func (f *fakeGit) Commits(_, tip, _ string, _ int, oldestFirst bool) ([]rebase.CommitInfo, int, error) {
	n := 3
	if tip == "m" {
		n = 2
	}
	return []rebase.CommitInfo{{Hash: tip, Subject: tip}}, n, nil
}
func (f *fakeGit) Merges(string, string, string) int               { return f.merges }
func (f *fakeGit) Pushed(string, string, string) ([]string, error) { return f.pushed, nil }
func (f *fakeGit) Changed(_, from, to string) ([]string, error) {
	return []string{"a.txt", to + ".txt"}, nil
}
func (f *fakeGit) Rebase(_, onto string) error {
	f.ranRebase = append(f.ranRebase, onto)
	if f.rebaseErr != nil {
		return f.rebaseErr
	}
	f.head.Hash = "n"
	return nil
}
func (f *fakeGit) Abort(string) error                  { f.aborted, f.op = true, ""; return nil }
func (f *fakeGit) Op(string) string                    { return f.op }
func (f *fakeGit) Conflicted(string) ([]string, error) { return f.conflicts, nil }
func (f *fakeGit) Stopped(string) (string, string)     { return f.stopOrig, f.stopOnto }
func (f *fakeGit) Count(_, from, to string) int        { return f.counts[from+">"+to] }

type fakeStore struct {
	p       *rebase.Pending
	cleared int
}

func (s *fakeStore) Save(_ string, p rebase.Pending) error { s.p = &p; return nil }
func (s *fakeStore) Load(string) (rebase.Pending, bool) {
	if s.p == nil {
		return rebase.Pending{}, false
	}
	return *s.p, true
}
func (s *fakeStore) Clear(string) error { s.p = nil; s.cleared++; return nil }

type fakeLocator struct{}

func (fakeLocator) GitDirFor(string) string  { return "/r/.git" }
func (fakeLocator) WorktreeOf(string) string { return "/r" }

type fakeRecorder struct{ got []string }

func (f *fakeRecorder) Before(string) undo.Mark {
	return undo.Mark{Head: "h", Branch: "feature", Worktree: "/r"}
}
func (f *fakeRecorder) Rebased(_ string, m undo.Mark, onto, label string) {
	f.got = append(f.got, m.Head+" "+m.Branch+" "+onto+" "+label)
}

func newService() (*Service, *fakeGit, *fakeStore, *fakeRecorder) {
	g := &fakeGit{head: refs.Head{Branch: "feature", Hash: "h", Short: "h"}, base: "b", counts: map[string]int{}}
	st, rec := &fakeStore{}, &fakeRecorder{}
	return &Service{Git: g, Store: st, Locator: fakeLocator{}, FS: &filesystem.FS{}, Undo: rec}, g, st, rec
}

func TestPlan(t *testing.T) {
	s, g, _, _ := newService()
	p, err := s.Plan("/r/.git", "main")
	if err != nil || p.Branch != "feature" || p.Hash != "m" || p.ReplayedCount != 3 || p.IncomingCount != 2 || p.UpToDate || p.FastForward ||
		strings.Join(p.BothChanged, ",") != "a.txt" || p.Blocked != "" || p.Summary == "" {
		t.Fatalf("Plan = %+v, %v", p, err)
	}
	for _, name := range []string{"nope", "feature", "-x", ""} {
		if _, err := s.Plan("/r/.git", name); err == nil {
			t.Errorf("Plan(%q) accepted", name)
		}
	}
	g.base = "m"
	if p, _ := s.Plan("/r/.git", "main"); !p.UpToDate || p.Blocked != "" || len(p.Replayed) != 0 {
		t.Fatalf("up to date = %+v", p)
	}
	g.base = "h"
	if p, _ := s.Plan("/r/.git", "main"); !p.FastForward || p.UpToDate || len(p.Replayed) != 0 {
		t.Fatalf("ff = %+v", p)
	}
	g.base = ""
	if p, err := s.Plan("/r/.git", "main"); err != nil || !strings.Contains(p.Blocked, "share no history") {
		t.Fatalf("unrelated = %+v, %v", p, err)
	}
	g.base = "b"
	g.merges = 1
	if p, _ := s.Plan("/r/.git", "main"); !strings.Contains(p.Blocked, "merge commit") {
		t.Fatalf("merges = %+v", p)
	}
	g.merges, g.pushed = 0, []string{"abcdefghi"}
	if p, _ := s.Plan("/r/.git", "main"); !strings.Contains(p.Blocked, "rewrite pushed commits") {
		t.Fatalf("pushed = %+v", p)
	}
	g.pushed, g.head.Changes.Changed = nil, 1
	if p, _ := s.Plan("/r/.git", "main"); !strings.Contains(p.Blocked, "commit, park or discard them first") {
		t.Fatalf("dirty = %+v", p)
	}
}

func TestRunRefusesBeforeRunning(t *testing.T) {
	s, g, st, _ := newService()
	g.pushed = []string{"abcdefghi"}
	if _, err := s.Run("/r/.git", "main"); err == nil || len(g.ranRebase) != 0 || st.p != nil {
		t.Fatalf("Run = %v, ran %v, pending %v", err, g.ranRebase, st.p)
	}
}

func TestRunOutcomes(t *testing.T) {
	s, g, st, rec := newService()
	r, err := s.Run("/r/.git", "main")
	if err != nil || r.Outcome != rebase.Done || r.Hash != "n" || r.Replayed != 3 || strings.Join(g.ranRebase, ",") != "m" ||
		len(rec.got) != 1 || rec.got[0] != "h feature m Rebased feature onto main" || st.p != nil {
		t.Fatalf("done = %+v, %v, %v, %v", r, err, rec.got, st.p)
	}

	s, g, st, rec = newService()
	g.base = "h"
	r, err = s.Run("/r/.git", "main")
	if err != nil || r.Outcome != rebase.FastForwards || len(rec.got) != 1 || !strings.Contains(rec.got[0], "Fast-forwarded feature to main") {
		t.Fatalf("ff = %+v, %v, %v", r, err, rec.got)
	}

	s, g, st, rec = newService()
	g.base = "m"
	r, err = s.Run("/r/.git", "main")
	if err != nil || r.Outcome != rebase.UpToDate || len(g.ranRebase) != 0 || len(rec.got) != 0 {
		t.Fatalf("up to date = %+v, %v", r, err)
	}

	// Stopped on clashes: not aborted, pending kept, nothing recorded.
	s, g, st, rec = newService()
	g.rebaseErr, g.op, g.conflicts = errors.New("CONFLICT"), "rebase", []string{"a.txt"}
	r, err = s.Run("/r/.git", "main")
	if err != nil || r.Outcome != rebase.Stopped || strings.Join(r.Conflicts, ",") != "a.txt" || g.aborted || st.p == nil || len(rec.got) != 0 {
		t.Fatalf("stopped = %+v, %v, aborted=%v pending=%v", r, err, g.aborted, st.p)
	}
	if st.p.Orig != "h" || st.p.Onto != "m" || st.p.Branch != "feature" || st.p.Count != 3 {
		t.Fatalf("pending = %+v", st.p)
	}

	// Fails before any stop (git refused): nothing half done.
	s, g, st, rec = newService()
	g.rebaseErr = errors.New("untracked file would be overwritten")
	if _, err = s.Run("/r/.git", "main"); err == nil || !strings.Contains(err.Error(), "nothing changed") || st.p != nil || len(rec.got) != 0 {
		t.Fatalf("failed = %v, %v", err, st.p)
	}
	// A rebase state without clashes is cleaned up with an abort.
	s, g, st, _ = newService()
	g.rebaseErr, g.op = errors.New("hook said no"), "rebase"
	if _, err = s.Run("/r/.git", "main"); err == nil || !g.aborted || st.p != nil {
		t.Fatalf("no-clash stop = %v aborted=%v", err, g.aborted)
	}
}

func TestFinish(t *testing.T) {
	pend := rebase.Pending{Branch: "feature", Orig: "h", Onto: "m", OntoName: "main", Worktree: "/r", Count: 3}
	setup := func() (*Service, *fakeGit, *fakeStore, *fakeRecorder) {
		s, g, st, rec := newService()
		p := pend
		st.p = &p
		g.head.Hash = "n"
		return s, g, st, rec
	}
	// Nothing pending.
	s, _, st, _ := newService()
	if ok, err := s.Finish("/r/.git"); ok || err != nil || st.cleared != 0 {
		t.Fatalf("nothing pending: %v %v", ok, err)
	}
	// Still stopped: nothing changes.
	s, g, st, rec := setup()
	g.op, g.stopOrig, g.stopOnto = "rebase", "h", "m"
	g.head.Busy = "rebase"
	if ok, _ := s.Finish("/r/.git"); ok || st.p == nil || len(rec.got) != 0 {
		t.Fatal("finished a stopped rebase")
	}
	// Finished: recorded once, pending gone.
	s, _, st, rec = setup()
	if ok, _ := s.Finish("/r/.git"); !ok || st.p != nil || len(rec.got) != 1 || rec.got[0] != "h feature m Rebased feature onto main" {
		t.Fatalf("finish: %v %v", st.p, rec.got)
	}
	if ok, _ := s.Finish("/r/.git"); ok || len(rec.got) != 1 {
		t.Fatal("recorded twice")
	}
	// Aborted: head back at orig.
	s, g, st, rec = setup()
	g.head.Hash = "h"
	if ok, _ := s.Finish("/r/.git"); ok || st.p != nil || len(rec.got) != 0 {
		t.Fatal("aborted rebase recorded")
	}
	// Another branch, another unrelated rebase, new commits since, onto not below: all dropped.
	for name, mut := range map[string]func(*fakeGit){
		"other branch":   func(g *fakeGit) { g.head.Branch = "other" },
		"other rebase":   func(g *fakeGit) { g.op, g.stopOrig, g.stopOnto, g.head.Busy = "rebase", "x", "y", "rebase" },
		"extra commits":  func(g *fakeGit) { g.counts["m>n"] = 4 },
		"onto not below": func(g *fakeGit) { g.counts["n>m"] = 1 },
	} {
		s, g, st, rec = setup()
		mut(g)
		if ok, _ := s.Finish("/r/.git"); ok || st.p != nil || len(rec.got) != 0 {
			t.Errorf("%s: recorded=%v pending=%v rec=%v", name, ok, st.p, rec.got)
		}
	}
	// A skipped commit means fewer replayed commits: still recorded.
	s, g, _, rec = setup()
	g.counts["m>n"] = 2
	if ok, _ := s.Finish("/r/.git"); !ok || len(rec.got) != 1 {
		t.Fatal("skip not recorded")
	}
}

func TestStatus(t *testing.T) {
	s, g, st, _ := newService()
	if got, _ := s.Status("/r/.git"); got.Pending {
		t.Fatalf("Status = %+v", got)
	}
	st.p = &rebase.Pending{Branch: "feature", Orig: "h", Onto: "m", OntoName: "main", Count: 3}
	g.op, g.stopOrig, g.stopOnto = "rebase", "h", "m"
	if got, _ := s.Status("/r/.git"); !got.Pending || got.Branch != "feature" || got.Onto != "main" || got.Recorded {
		t.Fatalf("Status = %+v", got)
	}
	g.op, g.head.Hash = "", "n"
	if got, _ := s.Status("/r/.git"); got.Pending || !got.Recorded {
		t.Fatalf("Status after finish = %+v", got)
	}
}
