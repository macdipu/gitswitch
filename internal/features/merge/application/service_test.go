package application

import (
	"errors"
	"strings"
	"testing"

	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// fakeGit: main at "h", side at "s", sharing "b".
type fakeGit struct {
	head      refs.Head
	op        string
	conflicts []string
	mergeErr  error
	runErr    error
	ran       []string
	files     map[string]string
	stages    map[int]string
}

func (f *fakeGit) Head(string) (refs.Head, error) { return f.head, nil }
func (f *fakeGit) Resolve(_, rev string) (string, error) {
	if rev == "side" {
		return "s", nil
	}
	return "", errors.New("there is no branch, tag or commit called " + rev)
}
func (f *fakeGit) MergeBase(_, a, b string) string { return "b" }
func (f *fakeGit) Commits(_, tip, not string, _ int) ([]merge.CommitInfo, int, error) {
	return []merge.CommitInfo{{Hash: tip, Subject: tip}}, 1, nil
}
func (f *fakeGit) Changed(_, from, to string) ([]string, error) {
	return []string{"a.txt", to + ".txt"}, nil
}
func (f *fakeGit) Predict(_, _, _ string) ([]string, bool) { return []string{"a.txt"}, true }
func (f *fakeGit) Merge(_, rev string, mode merge.Mode) error {
	f.ran = append(f.ran, "merge "+rev+" "+string(mode))
	if f.mergeErr != nil {
		f.op = merge.OpMerge
		if mode == merge.Squash {
			f.op = ""
		}
		f.conflicts = []string{"a.txt"}
		return f.mergeErr
	}
	f.head.Hash = "m"
	return nil
}
func (f *fakeGit) ResetMerge(string) error {
	f.ran = append(f.ran, "reset --merge")
	f.conflicts = nil
	return nil
}
func (f *fakeGit) Op(string) string                    { return f.op }
func (f *fakeGit) Merging(string) (string, string)     { return "s", "side" }
func (f *fakeGit) Conflicted(string) ([]string, error) { return f.conflicts, nil }
func (f *fakeGit) Run(_, op, action string) error {
	f.ran = append(f.ran, op+" --"+action)
	if f.runErr != nil {
		return f.runErr
	}
	f.op, f.conflicts = "", nil
	f.head.Hash = "m"
	return nil
}
func (f *fakeGit) Stage(_, _ string, n int) ([]byte, bool, error) {
	v, ok := f.stages[n]
	return []byte(v), ok, nil
}
func (f *fakeGit) ReadFile(_, p string) ([]byte, bool, error) {
	v, ok := f.files[p]
	return []byte(v), ok, nil
}
func (f *fakeGit) WriteFile(_, p string, b []byte) error { f.files[p] = string(b); return nil }
func (f *fakeGit) Take(_, p string, side merge.Side) error {
	f.ran = append(f.ran, "take "+string(side)+" "+p)
	return f.Add("", p)
}
func (f *fakeGit) Add(_, p string) error {
	f.ran = append(f.ran, "add "+p)
	var left []string
	for _, c := range f.conflicts {
		if c != p {
			left = append(left, c)
		}
	}
	f.conflicts = left
	return nil
}

type fakeLocator struct{}

func (fakeLocator) GitDirFor(string) string  { return "/r/.git" }
func (fakeLocator) WorktreeOf(string) string { return "/r" }

type fakeRecorder struct{ merged []string }

func (f *fakeRecorder) Before(string) undo.Mark { return undo.Mark{Head: "h", Branch: "main"} }
func (f *fakeRecorder) Merged(_ string, m undo.Mark, theirs, from, label string) {
	f.merged = append(f.merged, m.Head+" "+theirs+" "+from+" "+label)
}

func newService() (*Service, *fakeGit, *fakeRecorder) {
	g := &fakeGit{head: refs.Head{Branch: "main", Hash: "h", Short: "h"}, files: map[string]string{}, stages: map[int]string{}}
	rec := &fakeRecorder{}
	return &Service{Git: g, Locator: fakeLocator{}, FS: &filesystem.FS{}, Undo: rec}, g, rec
}

func TestPlan(t *testing.T) {
	s, g, _ := newService()
	p, err := s.Plan("/r/.git", "side")
	if err != nil || p.Into != "main" || p.Hash != "s" || p.FastForward || p.UpToDate || p.IncomingCount != 1 ||
		strings.Join(p.BothChanged, ",") != "a.txt" || !p.Predicted || strings.Join(p.Conflicts, ",") != "a.txt" || p.Blocked != "" || p.Summary == "" {
		t.Fatalf("Plan = %+v, %v", p, err)
	}
	for _, b := range []string{"nope", "main", "-x", ""} {
		if _, err := s.Plan("/r/.git", b); err == nil {
			t.Errorf("Plan(%q) was accepted", b)
		}
	}
	g.head.Changes.Changed = 1
	if p, err := s.Plan("/r/.git", "side"); err != nil || !strings.Contains(p.Blocked, "commit, park or discard them first") {
		t.Fatalf("dirty plan = %+v, %v", p, err)
	}
	if _, err := s.Merge("/r/.git", "side", merge.Auto); err == nil || len(g.ran) != 0 {
		t.Fatalf("merged with uncommitted changes: %v %v", err, g.ran)
	}
}

func TestMergeOutcomes(t *testing.T) {
	s, g, rec := newService()
	if _, err := s.Merge("/r/.git", "side", merge.FastForward); err == nil || !strings.Contains(err.Error(), "can't fast-forward") {
		t.Fatalf("ff-only when apart = %v", err)
	}
	r, err := s.Merge("/r/.git", "side", merge.Commit)
	if err != nil || r.Outcome != merge.MergeCommit || r.Hash != "m" || len(rec.merged) != 1 || rec.merged[0] != "h s side Merged side into main" {
		t.Fatalf("merge = %+v, %v, %v", r, err, rec.merged)
	}

	s, g, rec = newService()
	g.mergeErr = errors.New("CONFLICT")
	r, err = s.Merge("/r/.git", "side", merge.Auto)
	if err != nil || r.Outcome != merge.Stopped || strings.Join(r.Conflicts, ",") != "a.txt" || len(rec.merged) != 0 {
		t.Fatalf("stopped = %+v, %v", r, err)
	}

	s, g, _ = newService()
	g.mergeErr = errors.New("CONFLICT")
	if _, err := s.Merge("/r/.git", "side", merge.Squash); err == nil || !strings.Contains(err.Error(), "nothing changed") || g.ran[len(g.ran)-1] != "reset --merge" {
		t.Fatalf("clashing squash = %v, %v", err, g.ran)
	}
}

func TestOperationAndAct(t *testing.T) {
	s, g, rec := newService()
	if _, err := s.Act("/r/.git", merge.Continue); err == nil || !strings.Contains(err.Error(), "nothing is in progress") {
		t.Fatalf("nothing in progress = %v", err)
	}
	g.op, g.conflicts = merge.OpMerge, []string{"a.txt"}
	o, err := s.Operation("/r/.git")
	if err != nil || o.Kind != "merge" || o.CanContinue || strings.Join(o.Actions, ",") != "continue,abort" || o.Theirs != "side (being merged in)" {
		t.Fatalf("Operation = %+v, %v", o, err)
	}
	if _, err := s.Act("/r/.git", merge.Continue); err == nil || !strings.Contains(err.Error(), "still have conflicts: a.txt") {
		t.Fatalf("continue with conflicts = %v", err)
	}
	if _, err := s.Act("/r/.git", merge.Skip); err == nil || !strings.Contains(err.Error(), "can't be skipped") {
		t.Fatalf("skip a merge = %v", err)
	}
	if _, err := s.Act("/r/.git", "push"); err == nil {
		t.Fatal("unknown action accepted")
	}
	g.conflicts = nil
	o, err = s.Act("/r/.git", merge.Continue)
	if err != nil || o.Kind != "" || len(rec.merged) != 1 || !strings.Contains(rec.merged[0], "Merged side into main") {
		t.Fatalf("continue = %+v, %v, %v", o, err, rec.merged)
	}
	// A rebase that stops again on the next commit is not a failure.
	g.op, g.conflicts = merge.OpRebase, nil
	s.Git = &stopAgain{g}
	o, err = s.Act("/r/.git", merge.Continue)
	if err != nil || o.Kind != merge.OpRebase || !strings.Contains(o.Message, "stopped again") {
		t.Fatalf("stopped again = %+v, %v", o, err)
	}
	// Smart switch and bisect are finished elsewhere.
	g.op = "smart switch"
	if _, err := s.Act("/r/.git", merge.Abort); err == nil || !strings.Contains(err.Error(), "gitswitch resolve") {
		t.Fatalf("smart switch abort = %v", err)
	}
}

type stopAgain struct{ *fakeGit }

func (f *stopAgain) Run(_, op, action string) error {
	f.conflicts = []string{"b.txt"}
	return errors.New("could not apply")
}

func TestConflictAndResolve(t *testing.T) {
	s, g, _ := newService()
	g.op, g.conflicts = merge.OpMerge, []string{"a.txt", "b.txt"}
	g.stages = map[int]string{1: "base\n", 2: "main\n"}
	g.files["a.txt"] = "<<<<<<< HEAD\nmain\n=======\nside\n>>>>>>> side\n"
	c, err := s.Conflict("/r/.git", "a.txt")
	if err != nil || c.Base == nil || *c.Ours != "main\n" || c.Theirs != nil || c.Kind != "deleted by them" || !c.Markers || !c.Exists || c.OursLabel != "main (this branch)" {
		t.Fatalf("Conflict = %+v, %v", c, err)
	}
	if _, err := s.Conflict("/r/.git", "c.txt"); err == nil || !strings.Contains(err.Error(), "has no conflict") {
		t.Fatalf("not conflicted = %v", err)
	}
	g.stages[3] = "bin\x00ary"
	if _, err := s.Conflict("/r/.git", "a.txt"); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary = %v", err)
	}
	g.stages[3] = strings.Repeat("x", merge.MaxConflictBytes+1)
	if _, err := s.Conflict("/r/.git", "a.txt"); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("too big = %v", err)
	}
	if _, err := s.Resolve("/r/.git", "a.txt", "done", ""); err == nil || !strings.Contains(err.Error(), "still has conflict markers") {
		t.Fatalf("done with markers = %v", err)
	}
	if _, err := s.Resolve("/r/.git", "a.txt", "save", "<<<<<<< x\n>>>>>>> y\n"); err == nil || g.files["a.txt"] == "" {
		t.Fatalf("save with markers = %v", err)
	}
	o, err := s.Resolve("/r/.git", "a.txt", "save", "both\n")
	if err != nil || g.files["a.txt"] != "both\n" || strings.Join(o.Conflicts, ",") != "b.txt" || o.CanContinue {
		t.Fatalf("save = %+v, %v", o, err)
	}
	o, err = s.Resolve("/r/.git", "b.txt", "theirs", "")
	if err != nil || !o.CanContinue || g.ran[len(g.ran)-2] != "take theirs b.txt" {
		t.Fatalf("theirs = %+v, %v, %v", o, err, g.ran)
	}
	g.conflicts = []string{"a.txt"}
	if _, err := s.Resolve("/r/.git", "a.txt", "mine", ""); err == nil {
		t.Fatal("unknown action accepted")
	}
	g.op = "smart switch"
	if _, err := s.Resolve("/r/.git", "a.txt", "ours", ""); err == nil || !strings.Contains(err.Error(), "gitswitch resolve") {
		t.Fatalf("during a smart switch = %v", err)
	}
}
