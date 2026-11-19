package application

import (
	"errors"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// fakeStashRefs adds the stash manager's port to the branch fake.
type fakeStashRefs struct {
	*fakeRefs
	stashes  []refs.Stash
	pushed   refs.StashOptions
	pushErr  error
	branched string
	diff     refs.StashDiff
}

func (f *fakeStashRefs) Stashes(string) ([]refs.Stash, error) { return f.stashes, nil }
func (f *fakeStashRefs) StashDiff(_, h, p string) (refs.StashDiff, error) {
	f.ran = append(f.ran, "diff "+h+" "+p)
	return f.diff, nil
}
func (f *fakeStashRefs) StashPush(_ string, o refs.StashOptions) (string, error) {
	f.ran = append(f.ran, "push")
	f.pushed = o
	if f.pushErr != nil {
		return "", f.pushErr
	}
	f.stashes = append([]refs.Stash{{Ref: "stash@{0}", Hash: "new", Subject: "On main: " + o.Message, Message: o.Message}}, f.stashes...)
	return "new", nil
}
func (f *fakeStashRefs) StashBranch(_, h, name string) error {
	f.ran = append(f.ran, "branch "+h+" "+name)
	f.branched = name
	return nil
}
func (f *fakeStashRefs) DropStash(_, h string) error {
	f.ran = append(f.ran, "drop "+h)
	return nil
}

type stashRec struct {
	a             undo.Action
	hash, subject string
	label         string
	calls         int
}

func (r *stashRec) Before(string) undo.Mark                                   { return undo.Mark{} }
func (r *stashRec) Done(string, undo.Mark, undo.Action, string)               {}
func (r *stashRec) Pruned(string, undo.Mark, []undo.Deleted, string)          {}
func (r *stashRec) Updated(string, undo.Mark, string, string)                 {}
func (r *stashRec) Renamed(string, undo.Mark, string, string, string, string) {}
func (r *stashRec) Stashed(_ string, _ undo.Mark, a undo.Action, h, subj, label string) {
	r.a, r.hash, r.subject, r.label = a, h, subj, label
	r.calls++
}

func newStashService() (*Service, *fakeStashRefs, *stashRec) {
	s, r := newService()
	f := &fakeStashRefs{fakeRefs: r, stashes: []refs.Stash{
		{Index: 0, Ref: "stash@{0}", Hash: "s0", Subject: "On main: wip", Message: "wip", Branch: "main", Kind: refs.StashPlain, Files: []refs.StashFile{{Path: "a.go", Status: "M"}}},
		{Index: 1, Ref: "stash@{1}", Hash: "s1", Subject: "On main: gitswitch park: fix", Kind: refs.StashPark, ParkedOn: "fix"},
	}}
	s.Refs = f
	rec := &stashRec{}
	s.Undo = rec
	return s, f, rec
}

func TestStashListShowDiff(t *testing.T) {
	s, f, _ := newStashService()
	all, err := s.Stashes("/r/.git")
	if err != nil || len(all) != 2 || all[1].Kind != refs.StashPark {
		t.Fatalf("Stashes = %+v %v", all, err)
	}
	if st, err := s.StashShow("/r/.git", "s0"); err != nil || st.Ref != "stash@{0}" {
		t.Errorf("show: %+v %v", st, err)
	}
	if _, err := s.StashShow("/r/.git", "gone"); !errors.Is(err, refs.ErrStashChanged) {
		t.Errorf("stale show: %v", err)
	}
	f.diff = refs.StashDiff{Path: "a.go", Text: strings.Repeat("x", refs.MaxStashDiffBytes+5)}
	d, err := s.StashDiff("/r/.git", "s0", "./a.go")
	if err != nil || !d.Truncated || len(d.Text) != refs.MaxStashDiffBytes {
		t.Errorf("diff: %v %v", d.Truncated, err)
	}
	if _, err := s.StashDiff("/r/.git", "s0", "other.go"); err == nil || !strings.Contains(err.Error(), "not in stash@{0}") {
		t.Errorf("file not in stash: %v", err)
	}
	if _, err := s.StashDiff("/r/.git", "s0", "../x"); err == nil {
		t.Error("path outside repo")
	}
}

func TestStashSave(t *testing.T) {
	s, f, rec := newStashService()
	if _, err := s.StashSave("/r/.git", refs.StashOptions{}); err == nil || !strings.Contains(err.Error(), "nothing to stash") {
		t.Errorf("clean: %v", err)
	}
	f.head.Changes = refs.Changes{Conflicts: 2}
	if _, err := s.StashSave("/r/.git", refs.StashOptions{}); err == nil || !strings.Contains(err.Error(), "2 files have unresolved conflicts") {
		t.Errorf("conflicts: %v", err)
	}
	f.head.Changes = refs.Changes{Changed: 1}
	f.head.Busy = "merge"
	if _, err := s.StashSave("/r/.git", refs.StashOptions{}); err == nil || !strings.Contains(err.Error(), "merge") || len(f.ran) != 0 {
		t.Errorf("busy: %v %v", err, f.ran)
	}
	f.head.Busy = ""
	if _, err := s.StashSave("/r/.git", refs.StashOptions{Message: "gitswitch park: x"}); err == nil {
		t.Error("reserved words")
	}
	if _, err := s.StashSave("/r/.git", refs.StashOptions{Paths: []string{"../x"}}); err == nil || len(f.ran) != 0 {
		t.Errorf("bad path: %v", err)
	}

	st, err := s.StashSave("/r/.git", refs.StashOptions{Message: " my work ", Untracked: true})
	if err != nil || st.Hash != "new" || f.pushed.Message != "my work" {
		t.Fatalf("save: %+v %v %+v", st, err, f.pushed)
	}
	if rec.a != undo.StashSave || rec.hash != "new" || rec.subject != "On main: my work" {
		t.Errorf("undo record: %+v", rec)
	}

	// Paths or keep-index: not undoable, so not recorded.
	*rec = stashRec{}
	if _, err := s.StashSave("/r/.git", refs.StashOptions{Paths: []string{"./a.go"}}); err != nil || f.pushed.Paths[0] != "a.go" || rec.calls != 0 {
		t.Errorf("paths: %v %+v %+v", err, f.pushed, rec)
	}
	if _, err := s.StashSave("/r/.git", refs.StashOptions{KeepIndex: true}); err != nil || rec.calls != 0 {
		t.Errorf("keep index: %v %+v", err, rec)
	}

	f.pushErr = errors.New("there was nothing to stash")
	if _, err := s.StashSave("/r/.git", refs.StashOptions{}); err == nil || rec.calls != 0 {
		t.Errorf("git refused: %v", err)
	}
}

func TestStashApplyPopSafety(t *testing.T) {
	s, f, rec := newStashService()
	if err := s.StashApply("/r/.git", "gone"); !errors.Is(err, refs.ErrStashChanged) || len(f.ran) != 0 {
		t.Errorf("stale: %v %v", err, f.ran)
	}
	f.head.Changes = refs.Changes{Changed: 1}
	if err := s.StashPop("/r/.git", "s0"); err == nil || !strings.Contains(err.Error(), "Commit or park them first") || len(f.ran) != 0 {
		t.Errorf("dirty: %v %v", err, f.ran)
	}
	f.head.Changes = refs.Changes{}
	f.head.Busy = "rebase"
	if err := s.StashApply("/r/.git", "s0"); err == nil || len(f.ran) != 0 {
		t.Errorf("busy: %v", err)
	}
	f.head.Busy = ""

	// A clash: discarded, stash kept (no drop).
	f.applyFiles = []string{"a.go"}
	if err := s.StashPop("/r/.git", "s0"); err == nil || !strings.Contains(err.Error(), "nothing changed and the stash stays") || strings.Join(f.ran, ",") != "apply s0,discard" {
		t.Errorf("clash: %v %v", err, f.ran)
	}
	// An apply that fails outright.
	f.ran, f.applyFiles, f.applyErr = nil, nil, errors.New("already exists, no checkout")
	if err := s.StashApply("/r/.git", "s0"); err == nil || !strings.Contains(err.Error(), "nothing changed") || strings.Join(f.ran, ",") != "apply s0" {
		t.Errorf("apply error: %v %v", err, f.ran)
	}
	f.ran, f.applyErr = nil, nil
	if err := s.StashApply("/r/.git", "s0"); err != nil || strings.Join(f.ran, ",") != "apply s0" {
		t.Errorf("apply: %v %v", err, f.ran)
	}
	f.ran = nil
	if err := s.StashPop("/r/.git", "s1"); err != nil || strings.Join(f.ran, ",") != "apply s1,drop s1" {
		t.Errorf("pop: %v %v", err, f.ran)
	}
	if rec.calls != 0 {
		t.Errorf("apply and pop are not undoable: %+v", rec)
	}
}

func TestStashDropUndoRecord(t *testing.T) {
	s, f, rec := newStashService()
	if err := s.StashDrop("/r/.git", "gone"); !errors.Is(err, refs.ErrStashChanged) {
		t.Errorf("stale: %v", err)
	}
	if err := s.StashDrop("/r/.git", "s0"); err != nil || strings.Join(f.ran, ",") != "drop s0" {
		t.Fatalf("drop: %v %v", err, f.ran)
	}
	if rec.a != undo.StashDrop || rec.hash != "s0" || rec.subject != "On main: wip" || rec.label != "Dropped stash@{0} (wip)" {
		t.Errorf("record: %+v", rec)
	}
}

func TestStashBranchRules(t *testing.T) {
	s, f, _ := newStashService()
	if err := s.StashBranch("/r/.git", "gone", "x"); !errors.Is(err, refs.ErrStashChanged) {
		t.Errorf("stale: %v", err)
	}
	if err := s.StashBranch("/r/.git", "s0", "fix"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("exists: %v", err)
	}
	f.head.Changes = refs.Changes{New: 1}
	if err := s.StashBranch("/r/.git", "s0", "new-one"); err == nil || !strings.Contains(err.Error(), "Commit or park") {
		t.Errorf("dirty: %v", err)
	}
	f.head.Changes = refs.Changes{}
	if err := s.StashBranch("/r/.git", "s0", " new-one "); err != nil || f.branched != "new-one" {
		t.Errorf("branch: %v %q", err, f.branched)
	}
}
