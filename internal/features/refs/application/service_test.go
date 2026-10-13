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
	head       refs.Head
	branches   []refs.Branch
	pullErr    error
	ran        []string
	switchErrs []error // returned by the next switches, in order
	applyFiles []string
	applyErr   error
	markers    []string
	clash      *refs.Clash
	stashMsg   string
	parked     []refs.Parked
}

func (f *fakeRefs) Head(string) (refs.Head, error)         { return f.head, nil }
func (f *fakeRefs) Branches(string) ([]refs.Branch, error) { return f.branches, nil }
func (f *fakeRefs) nextSwitchErr() error {
	if len(f.switchErrs) == 0 {
		return nil
	}
	err := f.switchErrs[0]
	f.switchErrs = f.switchErrs[1:]
	return err
}
func (f *fakeRefs) Switch(_, b string) error {
	if err := f.nextSwitchErr(); err != nil {
		return err
	}
	f.ran = append(f.ran, "switch "+b)
	return nil
}
func (f *fakeRefs) Track(_, l, r string) error {
	if err := f.nextSwitchErr(); err != nil {
		return err
	}
	f.ran = append(f.ran, "track "+l+" "+r)
	return nil
}
func (f *fakeRefs) Stash(_, msg string) (string, error) {
	f.ran = append(f.ran, "stash")
	f.stashMsg = msg
	return "st", nil
}
func (f *fakeRefs) Apply(_, st string) ([]string, error) {
	f.ran = append(f.ran, "apply "+st)
	files, err := f.applyFiles, f.applyErr
	f.applyFiles, f.applyErr = nil, nil // only the first apply clashes
	return files, err
}
func (f *fakeRefs) DropStash(_, st string) error { f.ran = append(f.ran, "drop "+st); return nil }
func (f *fakeRefs) Discard(_, st string) error   { f.ran = append(f.ran, "discard"); return nil }
func (f *fakeRefs) Unstage(string) error         { f.ran = append(f.ran, "unstage"); return nil }
func (f *fakeRefs) Markers(string, []string) []string {
	return f.markers
}
func (f *fakeRefs) Parked(string) ([]refs.Parked, error) { return f.parked, nil }
func (f *fakeRefs) ParkedPatch(_, st string) (string, error) {
	return "diff of " + st, nil
}
func (f *fakeRefs) SaveClash(_ string, c refs.Clash) error { f.clash = &c; return nil }
func (f *fakeRefs) LoadClash(string) (*refs.Clash, error)  { return f.clash, nil }
func (f *fakeRefs) ClearClash(string) error                { f.clash = nil; return nil }
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

var overwrite = errors.New("error: Your local changes to the following files would be overwritten by checkout:\n\ta.txt")

func TestSmartSwitch(t *testing.T) {
	// Git can keep the changes: a plain switch, no stash.
	s, r := newService()
	r.head.Changes = refs.Changes{Changed: 2}
	if got, err := s.SmartSwitch("/r/.git", "fix"); err != nil || !got.Carried || got.Clash != nil || strings.Join(r.ran, ",") != "switch fix" {
		t.Errorf("plain: %+v %v %v", got, err, r.ran)
	}

	// Git refuses: stash, switch, put back, drop the copy.
	s, r = newService()
	r.switchErrs = []error{overwrite}
	if got, err := s.SmartSwitch("/r/.git", "origin/feature/x"); err != nil || got.Clash != nil ||
		strings.Join(r.ran, ",") != "stash,track feature/x origin/feature/x,apply st,drop st" {
		t.Errorf("stashed: %+v %v %v", got, err, r.ran)
	}

	// Some files clash: the switch stays, the copy is kept, the clash saved.
	s, r = newService()
	r.switchErrs = []error{overwrite}
	r.applyFiles = []string{"a.txt"}
	got, err := s.SmartSwitch("/r/.git", "fix")
	if err != nil || got.Clash == nil || got.Clash.From != "main" || got.Clash.To != "fix" || got.Clash.Stash != "st" ||
		r.clash == nil || strings.Join(r.ran, ",") != "stash,switch fix,apply st" {
		t.Fatalf("clash: %+v %v %v", got, err, r.ran)
	}

	// The changes can't be put back at all: switch back and restore them.
	s, r = newService()
	r.switchErrs = []error{overwrite}
	r.applyErr = errors.New("already exists, no checkout")
	if _, err := s.SmartSwitch("/r/.git", "fix"); err == nil || !strings.Contains(err.Error(), "stayed on main") ||
		strings.Join(r.ran, ",") != "stash,switch fix,apply st,switch main,apply st,drop st" {
		t.Errorf("not applied: %v %v", err, r.ran)
	}

	// Any other git error: nothing is stashed.
	s, r = newService()
	r.switchErrs = []error{errors.New("fatal: something else")}
	if _, err := s.SmartSwitch("/r/.git", "fix"); err == nil || len(r.ran) != 0 {
		t.Errorf("other error: %v %v", err, r.ran)
	}
}

func TestResolve(t *testing.T) {
	s, r := newService()
	if err := s.ResolveDone("/r/.git"); err == nil || !strings.Contains(err.Error(), "no smart switch") {
		t.Errorf("nothing open: %v", err)
	}
	r.clash = &refs.Clash{From: "main", To: "fix", Stash: "st", Files: []string{"a.txt", "b.txt"}}
	r.markers = []string{"b.txt"}
	if err := s.ResolveDone("/r/.git"); err == nil || !strings.Contains(err.Error(), "still have conflict markers: b.txt") || r.clash == nil || len(r.ran) != 0 {
		t.Errorf("markers left: %v %v", err, r.ran)
	}
	r.markers = nil
	if err := s.ResolveDone("/r/.git"); err != nil || r.clash != nil || strings.Join(r.ran, ",") != "unstage,drop st" {
		t.Errorf("done: %v %v", err, r.ran)
	}

	s, r = newService()
	r.clash = &refs.Clash{From: "main", To: "fix", Stash: "st", Files: []string{"a.txt"}}
	if err := s.ResolveAbort("/r/.git"); err != nil || r.clash != nil || strings.Join(r.ran, ",") != "discard,switch main,apply st,drop st" {
		t.Errorf("abort: %v %v", err, r.ran)
	}
}

func TestPark(t *testing.T) {
	// Changes: stash them with the branch in the message, then switch.
	s, r := newService()
	r.head.Changes = refs.Changes{Changed: 1, New: 1}
	got, err := s.Park("/r/.git", "fix")
	if err != nil || !got.Parked || r.stashMsg != "gitswitch park: main" || strings.Join(r.ran, ",") != "stash,switch fix" {
		t.Errorf("park: %+v %v %q %v", got, err, r.stashMsg, r.ran)
	}

	// The switch fails: the changes are put back and the copy dropped.
	s, r = newService()
	r.head.Changes = refs.Changes{Changed: 1}
	r.switchErrs = []error{errors.New("fatal: invalid reference")}
	if _, err := s.Park("/r/.git", "fix"); err == nil || strings.Join(r.ran, ",") != "stash,apply st,drop st" {
		t.Errorf("failed switch: %v %v", err, r.ran)
	}

	// Nothing to park: a plain switch, and changes parked on the branch
	// switched to are offered (the fake stays "on" main).
	s, r = newService()
	r.head.Parked = 1
	r.parked = []refs.Parked{{Stash: "p1", Branch: "main"}, {Stash: "p2", Branch: "other"}}
	got, err = s.Park("/r/.git", "fix")
	if err != nil || got.Parked || strings.Join(r.ran, ",") != "switch fix" || len(got.Waiting) != 1 || got.Waiting[0].Stash != "p1" {
		t.Errorf("clean: %+v %v %v", got, err, r.ran)
	}

	// A detached HEAD has no branch to park on.
	s, r = newService()
	r.head.Branch, r.head.Changes = "", refs.Changes{Changed: 1}
	if _, err := s.Park("/r/.git", "fix"); err == nil || !strings.Contains(err.Error(), "detached") || len(r.ran) != 0 {
		t.Errorf("detached: %v %v", err, r.ran)
	}
}

func TestRestoreAndDropParked(t *testing.T) {
	s, r := newService()
	r.parked = []refs.Parked{{Ref: "stash@{0}", Stash: "p1", Branch: "main"}}
	if err := s.RestoreParked("/r/.git", "gone"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("unknown: %v", err)
	}

	// Uncommitted changes: refused, nothing runs.
	r.head.Changes = refs.Changes{Changed: 1}
	if err := s.RestoreParked("/r/.git", "p1"); err == nil || !strings.Contains(err.Error(), "Commit or park them first") || len(r.ran) != 0 {
		t.Errorf("dirty: %v %v", err, r.ran)
	}
	r.head.Changes = refs.Changes{}

	// A clash: the half-restored files are discarded and the entry kept.
	r.applyFiles = []string{"a.txt"}
	if err := s.RestoreParked("/r/.git", "p1"); err == nil || !strings.Contains(err.Error(), "stay parked") || strings.Join(r.ran, ",") != "apply p1,discard" {
		t.Errorf("clash: %v %v", err, r.ran)
	}

	r.ran = nil
	if err := s.RestoreParked("/r/.git", "p1"); err != nil || strings.Join(r.ran, ",") != "apply p1,drop p1" {
		t.Errorf("restore: %v %v", err, r.ran)
	}

	r.ran = nil
	if err := s.DropParked("/r/.git", "p1"); err != nil || strings.Join(r.ran, ",") != "drop p1" {
		t.Errorf("drop: %v %v", err, r.ran)
	}
	if out, err := s.ParkedPatch("/r/.git", "p1"); err != nil || out != "diff of p1" {
		t.Errorf("patch: %q %v", out, err)
	}
}

func TestResolvePark(t *testing.T) {
	s, r := newService()
	r.clash = &refs.Clash{From: "main", To: "fix", Stash: "st", Files: []string{"a.txt"}}
	r.head.Changes = refs.Changes{Changed: 1}
	got, err := s.ResolvePark("/r/.git")
	if err != nil || !got.Parked || r.clash != nil || r.stashMsg != "gitswitch park: main" ||
		strings.Join(r.ran, ",") != "discard,switch main,apply st,drop st,stash,switch fix" {
		t.Errorf("park instead: %+v %v %v", got, err, r.ran)
	}
}
