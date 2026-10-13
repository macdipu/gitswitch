package application

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeRefs struct {
	head     refs.Head
	branches []refs.Branch
	pullErr  error
	ran      []string
}

func (f *fakeRefs) Head(string) (refs.Head, error)         { return f.head, nil }
func (f *fakeRefs) Branches(string) ([]refs.Branch, error) { return f.branches, nil }
func (f *fakeRefs) Switch(_, b string) error               { f.ran = append(f.ran, "switch "+b); return nil }
func (f *fakeRefs) Track(_, l, r string) error {
	f.ran = append(f.ran, "track "+l+" "+r)
	return nil
}
func (f *fakeRefs) Pull(_ string, progress func(string)) error {
	progress("Fast-forward")
	f.ran = append(f.ran, "pull")
	if f.pullErr != nil {
		return f.pullErr
	}
	f.head.Behind = 0
	return nil
}

type locator struct{}

func (locator) GitDirFor(string) string         { return "" }
func (locator) WorktreeOf(gitDir string) string { return strings.TrimSuffix(gitDir, "/.git") }

func newService() (*Service, *fakeRefs) {
	r := &fakeRefs{
		head: refs.Head{Branch: "main", Hash: "abc", Short: "abc", Upstream: "origin/main", Behind: 2},
		branches: []refs.Branch{
			{Name: "main", Current: true, Upstream: "origin/main"}, {Name: "fix"},
			{Name: "origin/main", Remote: true}, {Name: "origin/fix", Remote: true}, {Name: "origin/feature/x", Remote: true},
		},
	}
	return &Service{Refs: r, Locator: locator{}, FS: &filesystem.FS{}}, r
}

func TestSwitch(t *testing.T) {
	s, r := newService()
	for name, want := range map[string]string{"": "not a branch", "-f": "not a branch", "nope": "no branch", "main": "already on main", "origin/main": "already on main"} {
		if _, err := s.Switch("/r/.git", name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Switch(%q) err = %v, want %q", name, err, want)
		}
	}
	for name, want := range map[string]string{"fix": "switch fix", "origin/fix": "switch fix", "origin/feature/x": "track feature/x origin/feature/x"} {
		r.ran = nil
		if _, err := s.Switch("/r/.git", name); err != nil || len(r.ran) != 1 || r.ran[0] != want {
			t.Errorf("Switch(%q): err=%v ran=%v, want %q", name, err, r.ran, want)
		}
	}
	// The local main pulls from origin/main, so upstream/main must not
	// quietly switch to it (nor report "already on main").
	r.branches = append(r.branches, refs.Branch{Name: "upstream/main", Remote: true})
	r.ran = nil
	if _, err := s.Switch("/r/.git", "upstream/main"); err == nil || !strings.Contains(err.Error(), "pulls from origin/main") ||
		!strings.Contains(err.Error(), "git switch -c upstream-main --track upstream/main") || len(r.ran) != 0 {
		t.Errorf("Switch(upstream/main): err=%v ran=%v", err, r.ran)
	}

	r.ran = nil
	r.head.Busy = "merge"
	if _, err := s.Switch("/r/.git", "fix"); err == nil || len(r.ran) != 0 {
		t.Errorf("switching mid-merge: err=%v ran=%v", err, r.ran)
	}
}

func TestUpdate(t *testing.T) {
	s, r := newService()
	var lines []string
	h, err := s.Update("/r/.git", func(l string) { lines = append(lines, l) })
	if err != nil || h.Behind != 0 || len(lines) != 1 {
		t.Fatalf("h=%+v err=%v lines=%v", h, err, lines)
	}

	r.pullErr = errors.New("fatal: Not possible to fast-forward, aborting.")
	if _, err := s.Update("/r/.git", func(string) {}); err == nil || !strings.Contains(err.Error(), "both have new commits") {
		t.Errorf("diverged: err=%v", err)
	}

	r.ran = nil
	for _, c := range []struct {
		head refs.Head
		want string
	}{
		{refs.Head{Hash: "abc"}, "detached"},
		{refs.Head{Branch: "fix", Hash: "abc"}, "not on a remote"},
		{refs.Head{Branch: "fix", Hash: "abc", Gone: "origin/fix"}, "git fetch origin fix:refs/remotes/origin/fix"},
		{refs.Head{Branch: "main", Upstream: "origin/main", Busy: "rebase"}, "middle of a rebase"},
	} {
		r.head = c.head
		if _, err := s.Update("/r/.git", func(string) {}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("head %+v: err=%v, want %q", c.head, err, c.want)
		}
	}
	if len(r.ran) != 0 {
		t.Errorf("nothing may run when refused: %v", r.ran)
	}

	r.head = refs.Head{Branch: "main", Upstream: "origin/main"}
	var out bytes.Buffer
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	if _, err := s.Update("/r/.git", func(string) {}); err != nil || len(r.ran) != 0 || !strings.Contains(out.String(), "git pull --ff-only") {
		t.Errorf("dry run: err=%v ran=%v out=%q", err, r.ran, out.String())
	}
}

type fakeRecorder struct{ done []string }

func (f *fakeRecorder) Before(string) undo.Mark { return undo.Mark{Branch: "main"} }
func (f *fakeRecorder) Done(_ string, m undo.Mark, a undo.Action, label string) {
	f.done = append(f.done, string(a)+" "+m.Branch+": "+label)
}

func TestSwitchIsRecordedForUndo(t *testing.T) {
	s, _ := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	if _, err := s.Switch("/r/.git", "origin/fix"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Switch("/r/.git", "main") // already there: nothing to record
	if strings.Join(rec.done, "|") != "switch main: Switched from main to fix" {
		t.Errorf("recorded %v", rec.done)
	}
}
