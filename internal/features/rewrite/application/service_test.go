package application

import (
	"errors"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

func commit(h string, parent string) rewrite.Commit {
	return rewrite.Commit{Hash: strings.Repeat(h, 40), Short: strings.Repeat(h, 7), Subject: "s" + h, Message: "s" + h, Parents: []string{strings.Repeat(parent, 40)}}
}

type fakeGit struct {
	head       refs.Head
	unpushed   []rewrite.Commit
	rebaseErr  error
	op         string
	conflicts  []string
	rebased    int
	aborted    bool
	cleaned    bool
	pending    *rewrite.Pending
	headAfter  string
	softReset  string
	rangeCalls []string
}

func (g *fakeGit) Head(string) (refs.Head, error)        { return g.head, nil }
func (g *fakeGit) Resolve(_, rev string) (string, error) { return rev, nil }
func (g *fakeGit) Commit(_, hash string) (rewrite.Commit, error) {
	for _, c := range g.unpushed {
		if c.Hash == hash {
			return c, nil
		}
	}
	return rewrite.Commit{}, errors.New("no such commit")
}
func (g *fakeGit) IsAncestor(string, string, string) bool { return true }
func (g *fakeGit) Unpushed(string) ([]rewrite.Commit, error) {
	return g.unpushed, nil
}
func (g *fakeGit) Range(_, base string) ([]rewrite.Commit, error) {
	g.rangeCalls = append(g.rangeCalls, base)
	var out []rewrite.Commit
	on := false
	for _, c := range g.unpushed {
		if c.Parents[0] == base {
			on = true
		}
		if on {
			out = append(out, c)
		}
	}
	return out, nil
}
func (g *fakeGit) Rebase(_, _, _ string, _ rewrite.Plan) error {
	g.rebased++
	if g.rebaseErr != nil {
		g.op = map[bool]string{true: "rebase"}[len(g.conflicts) > 0 || g.op == "rebase"]
	}
	if g.rebaseErr == nil {
		g.head.Hash = g.headAfter
	}
	return g.rebaseErr
}
func (g *fakeGit) Op(string) string                    { return g.op }
func (g *fakeGit) Conflicted(string) ([]string, error) { return g.conflicts, nil }
func (g *fakeGit) Abort(string) error                  { g.aborted, g.op = true, ""; return nil }
func (g *fakeGit) CommitFixup(string, string) (string, error) {
	g.unpushed = append(g.unpushed, commit("f", "c"))
	return strings.Repeat("f", 40), nil
}
func (g *fakeGit) SoftReset(_, h string) error { g.softReset = h; return nil }
func (g *fakeGit) SavePending(_ string, p rewrite.Pending) error {
	g.pending = &p
	return nil
}
func (g *fakeGit) Pending(string) (rewrite.Pending, bool) {
	if g.pending == nil {
		return rewrite.Pending{}, false
	}
	return *g.pending, true
}
func (g *fakeGit) ClearPending(string) { g.pending = nil }
func (g *fakeGit) Cleanup(string)      { g.cleaned = true }

type fakeRec struct {
	done []undo.Action
	mark undo.Mark
}

func (r *fakeRec) Before(string) undo.Mark {
	return undo.Mark{Worktree: "/w", Branch: "main", Head: strings.Repeat("d", 40)}
}
func (r *fakeRec) Done(_ string, m undo.Mark, a undo.Action, _ string) {
	r.done, r.mark = append(r.done, a), m
}

type loc struct{}

func (loc) GitDirFor(string) string  { return "/g" }
func (loc) WorktreeOf(string) string { return "/w" }

func setup() (*Service, *fakeGit, *fakeRec) {
	g := &fakeGit{head: refs.Head{Branch: "main", Hash: strings.Repeat("c", 40)},
		unpushed: []rewrite.Commit{commit("b", "a"), commit("c", "b")}, headAfter: strings.Repeat("e", 40)}
	r := &fakeRec{}
	return &Service{Git: g, Locator: loc{}, FS: &filesystem.FS{}, Undo: r}, g, r
}

func swap(g *fakeGit) []rewrite.Step {
	return []rewrite.Step{{Hash: g.unpushed[1].Hash, Action: rewrite.Pick}, {Hash: g.unpushed[0].Hash, Action: rewrite.Pick}}
}

func TestTodo(t *testing.T) {
	s, g, _ := setup()
	td, err := s.Todo("/g", "")
	if err != nil || len(td.Commits) != 2 || td.Base != strings.Repeat("a", 40) || td.Blocked != "" {
		t.Fatalf("%+v %v", td, err)
	}
	g.head.Changes.Changed = 1
	if td, _ = s.Todo("/g", ""); !strings.Contains(td.Blocked, "uncommitted") || len(td.Commits) != 2 {
		t.Fatalf("%+v", td)
	}
	g.unpushed = nil
	if td, _ = s.Todo("/g", ""); len(td.Commits) != 0 {
		t.Fatalf("%+v", td)
	}
	// --since a pushed commit (not among the unpushed ones)
	s2, g2, _ := setup()
	pushed := commit("a", "z")
	g2.unpushed = append([]rewrite.Commit{pushed}, g2.unpushed...)
	s3 := *s2
	g3 := *g2
	g3.unpushed = g2.unpushed[1:]
	g3.rangeCalls = nil
	s3.Git = &fakeGitWithPushed{fakeGit: &g3, extra: pushed}
	if _, err := s3.Todo("/g", pushed.Hash); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Fatalf("pushed since: %v", err)
	}
}

// fakeGitWithPushed knows one more commit (pushed: not among the unpushed).
type fakeGitWithPushed struct {
	*fakeGit
	extra rewrite.Commit
}

func (g *fakeGitWithPushed) Commit(_, h string) (rewrite.Commit, error) {
	if h == g.extra.Hash {
		return g.extra, nil
	}
	return g.fakeGit.Commit("", h)
}
func (g *fakeGitWithPushed) Range(_, _ string) ([]rewrite.Commit, error) {
	return append([]rewrite.Commit{g.extra}, g.unpushed...), nil
}

func TestRunDone(t *testing.T) {
	s, g, r := setup()
	res, err := s.Run("/g", "", swap(g))
	if err != nil || res.Outcome != rewrite.Done || res.Before != 2 || res.After != 2 || g.rebased != 1 || !g.cleaned {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.done) != 1 || r.done[0] != undo.Rewrite || r.mark.Head != strings.Repeat("d", 40) {
		t.Fatalf("recorded %+v %+v", r.done, r.mark)
	}
	// no change, and a bad step list, never reach git
	g.rebased = 0
	if _, err := s.Run("/g", "", []rewrite.Step{{Hash: g.unpushed[0].Hash}, {Hash: g.unpushed[1].Hash}}); err == nil || !strings.Contains(err.Error(), "change nothing") {
		t.Fatal(err)
	}
	if _, err := s.Run("/g", "", swap(g)[:1]); err == nil {
		t.Fatal("missing step accepted")
	}
	g.head.Busy = "merge"
	if _, err := s.Run("/g", "", swap(g)); err == nil {
		t.Fatal("busy accepted")
	}
	if g.rebased != 0 {
		t.Fatal("git ran for a refused rewrite")
	}
}

func TestRunStopsOnClash(t *testing.T) {
	s, g, r := setup()
	g.rebaseErr, g.conflicts = errors.New("could not apply"), []string{"f.txt"}
	res, err := s.Run("/g", "", swap(g))
	if err != nil || res.Outcome != rewrite.Stopped || len(res.Conflicts) != 1 || g.aborted || g.cleaned {
		t.Fatalf("%+v %v aborted=%v", res, err, g.aborted)
	}
	if g.pending == nil || g.pending.Head != strings.Repeat("d", 40) || g.pending.Action != undo.Rewrite || len(r.done) != 0 {
		t.Fatalf("pending %+v", g.pending)
	}
	// still stopped: Finish waits.
	if f, _ := s.Finish("/g"); !f.Pending || f.Recorded {
		t.Fatalf("%+v", f)
	}
	// continued: the branch moved, so it is recorded and forgotten.
	g.op, g.conflicts, g.rebaseErr = "", nil, nil
	g.head.Hash = strings.Repeat("e", 40)
	if f, _ := s.Finish("/g"); f.Pending || !f.Recorded || len(r.done) != 1 || g.pending != nil || r.mark.Head != strings.Repeat("d", 40) {
		t.Fatalf("%+v %+v", f, r.done)
	}
	// aborted: back at the original tip, nothing recorded.
	g.rebaseErr, g.conflicts = errors.New("x"), []string{"f.txt"}
	g.head.Hash = strings.Repeat("c", 40)
	s.Run("/g", "", swap(g))
	g.op, g.conflicts, g.rebaseErr = "", nil, nil
	g.pending.Head = strings.Repeat("c", 40)
	if f, _ := s.Finish("/g"); f.Recorded || len(r.done) != 1 || g.pending != nil {
		t.Fatalf("%+v", f)
	}
}

func TestRunFailureCleansUp(t *testing.T) {
	s, g, r := setup()
	g.rebaseErr, g.op = errors.New("hook failed"), "rebase"
	_, err := s.Run("/g", "", swap(g))
	if err == nil || !strings.Contains(err.Error(), "nothing changed") || !g.aborted || !g.cleaned || g.pending != nil || len(r.done) != 0 {
		t.Fatalf("%v aborted=%v", err, g.aborted)
	}
}

func TestRewordAndFixupInto(t *testing.T) {
	s, g, r := setup()
	if _, err := s.Reword("/g", g.unpushed[0].Hash, "  "); err == nil {
		t.Fatal("empty message")
	}
	res, err := s.Reword("/g", g.unpushed[0].Hash, "new")
	if err != nil || res.Outcome != rewrite.Done || len(r.done) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := s.Reword("/g", g.unpushed[0].Hash, "sb"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatal(err)
	}
	// fixup-into needs staged changes only
	if _, err := s.FixupInto("/g", g.unpushed[0].Hash); err == nil || !strings.Contains(err.Error(), "nothing is staged") {
		t.Fatal(err)
	}
	g.head.Changes = refs.Changes{Staged: 1}
	g.head.Hash = strings.Repeat("c", 40)
	r.done = nil
	res, err = s.FixupInto("/g", g.unpushed[0].Hash)
	if err != nil || res.Outcome != rewrite.Done || len(r.done) != 1 || r.done[0] != undo.Amend {
		t.Fatalf("%+v %v %v", res, err, r.done)
	}
	// a failing rewrite takes the fixup commit off again: staged as before.
	g.rebaseErr, g.unpushed = errors.New("boom"), g.unpushed[:2]
	if _, err := s.FixupInto("/g", g.unpushed[0].Hash); err == nil || g.softReset != strings.Repeat("d", 40) {
		t.Fatalf("%v softReset=%q", err, g.softReset)
	}
}
