package application

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
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
	modes      map[string]string // gitDir ("" for every repo) -> saved switch mode
	createErr  error
	prefix     string
	target     [2]string // PushTarget: remote, merge
	remotes    []string
	pushErr    error
	defBranch  string
	moveErr    error
	merged     []string // MergedInto
	deleteErr  map[string]error
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
func (f *fakeRefs) PushTarget(string, string) (string, string) { return f.target[0], f.target[1] }
func (f *fakeRefs) RemoteNames(string) []string                { return f.remotes }
func (f *fakeRefs) NotPushed(string) int                       { return 3 }
func (f *fakeRefs) DefaultBranch(string, string) string        { return f.defBranch }
func (f *fakeRefs) Push(_, rem, src, dst string, up bool, progress func(string)) error {
	if f.pushErr != nil {
		return f.pushErr
	}
	progress("Writing objects")
	f.ran = append(f.ran, fmt.Sprintf("push %s %s:%s -u=%v", rem, src, dst, up))
	return nil
}
func (f *fakeRefs) Create(_, b, c string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.ran = append(f.ran, strings.TrimSpace("create "+b+" "+c))
	return nil
}
func (f *fakeRefs) CheckName(b string) error {
	if strings.ContainsAny(b, " ~^:?*[\\") || strings.Contains(b, "..") || strings.HasSuffix(b, "/") {
		return errors.New("fatal: not a valid branch name")
	}
	return nil
}
func (f *fakeRefs) Commit(_, rev string) (string, error) {
	if rev == "nope" {
		return "", errors.New("exit status 1")
	}
	return "hash-of-" + rev, nil
}
func (f *fakeRefs) MergedInto(_, rev string) ([]string, error) {
	f.ran = append(f.ran, "merged-into "+rev)
	return f.merged, nil
}
func (f *fakeRefs) DeleteBranch(_, b, was string) error {
	if err := f.deleteErr[b]; err != nil {
		return err
	}
	f.ran = append(f.ran, "delete "+b+" "+was)
	return nil
}
func (f *fakeRefs) MergeBase(_, a, b string) (string, error) { return "base-of-" + b, nil }
func (f *fakeRefs) MoveBranch(_, b, to, was, _ string) error {
	if f.moveErr != nil && to != "" {
		return f.moveErr
	}
	f.ran = append(f.ran, "move "+b+" "+was+"→"+to)
	return nil
}
func (f *fakeRefs) BranchPrefix() string            { return f.prefix }
func (f *fakeRefs) SaveBranchPrefix(p string) error { f.prefix = p; return nil }
func (f *fakeRefs) SwitchModes(gitDir string) (string, string) {
	if gitDir == "" {
		return f.modes[""], ""
	}
	return f.modes[""], f.modes[gitDir]
}
func (f *fakeRefs) SaveSwitchMode(gitDir, v string) error {
	if f.modes == nil {
		f.modes = map[string]string{}
	}
	f.modes[gitDir] = v
	return nil
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
func (f *fakeRecorder) Pruned(_ string, m undo.Mark, ds []undo.Deleted, label string) {
	var names []string
	for _, d := range ds {
		names = append(names, d.Name+"@"+d.Hash+"<"+d.Remote+" "+d.Merge)
	}
	f.done = append(f.done, "prune "+m.Branch+": "+label+" "+strings.Join(names, ","))
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

func TestSwitchSetting(t *testing.T) {
	s, _ := newService()
	if got := s.SwitchSetting("/r/.git"); got.Mode != refs.Ask {
		t.Errorf("default = %+v", got)
	}
	for _, c := range []struct{ gitDir, mode string }{{"", ""}, {"", "never"}, {"/r/.git", "never"}} {
		if err := s.SetSwitchSetting(c.gitDir, c.mode); err == nil || !strings.Contains(err.Error(), "ask, smart or park") {
			t.Errorf("SetSwitchSetting(%q, %q) = %v", c.gitDir, c.mode, err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SetSwitchSetting("", "Smart"))
	must(s.SetSwitchSetting("/r/.git", "park"))
	if got := s.SwitchSetting("/r/.git"); got.Mode != refs.Park || got.Global != refs.Smart || got.Repo != refs.Park {
		t.Errorf("repo over global = %+v", got)
	}
	if got := s.SwitchSetting(""); got.Mode != refs.Smart || got.Repo != "" {
		t.Errorf("global = %+v", got)
	}
	must(s.SetSwitchSetting("/r/.git", "")) // follow every repo's again
	if got := s.SwitchSetting("/r/.git"); got.Mode != refs.Smart || got.Repo != "" {
		t.Errorf("repo cleared = %+v", got)
	}
}

func TestNewBranch(t *testing.T) {
	s, r := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	r.head.Changes = refs.Changes{Changed: 1}
	got, err := s.NewBranch("/r/.git", " feature/login ", "")
	if err != nil || got.Branch != "feature/login" || !got.Carried || strings.Join(r.ran, ",") != "create feature/login" ||
		strings.Join(rec.done, "|") != "switch main: Started branch feature/login from main" {
		t.Fatalf("from HEAD: %+v %v %v %v", got, err, r.ran, rec.done)
	}

	r.ran = nil
	if _, err := s.NewBranch("/r/.git", "x", "v1.0"); err != nil || strings.Join(r.ran, ",") != "create x hash-of-v1.0" {
		t.Errorf("from a tag: %v %v", err, r.ran)
	}

	for _, c := range []struct{ name, from, want string }{
		{"", "", "can't be a branch name"},
		{"has space", "", "can't be a branch name"},
		{"-f", "", "can't be a branch name"},
		{"fix", "", "already a branch fix"},
		{"y", "nope", "no commit, branch or tag \"nope\""},
		{"y", "--all", "not a commit"},
	} {
		r.ran = nil
		if _, err := s.NewBranch("/r/.git", c.name, c.from); err == nil || !strings.Contains(err.Error(), c.want) || len(r.ran) != 0 {
			t.Errorf("NewBranch(%q, %q) = %v %v, want %q", c.name, c.from, err, r.ran, c.want)
		}
	}
	// A name that is only on the remote (origin/feature/x) is free here.
	if _, err := s.NewBranch("/r/.git", "feature/x", ""); err != nil {
		t.Errorf("a name only on the remote: %v", err)
	}

	r.createErr = overwrite
	if _, err := s.NewBranch("/r/.git", "z", "v1.0"); err == nil || !strings.Contains(err.Error(), "can't come along; nothing changed") {
		t.Errorf("changes clash: %v", err)
	}
}

func TestBranchPrefix(t *testing.T) {
	s, r := newService()
	for _, bad := range []string{"has space/", "-x", "a..b"} {
		if err := s.SetBranchPrefix(bad); err == nil || r.prefix != "" {
			t.Errorf("SetBranchPrefix(%q) = %v", bad, err)
		}
	}
	if err := s.SetBranchPrefix(" feature/ "); err != nil || s.BranchPrefix() != "feature/" {
		t.Errorf("prefix = %q %v", s.BranchPrefix(), err)
	}
	if err := s.SetBranchPrefix(""); err != nil || s.BranchPrefix() != "" {
		t.Errorf("cleared = %q %v", s.BranchPrefix(), err)
	}
}

type pushRepos struct{ repo.Reader }

func (pushRepos) InfoFor(*account.Config, string) repo.Info {
	return repo.Info{Account: "work", Remotes: []repo.Remote{{Name: "origin", URL: "git@github.com:acme/api.git"}}}
}

type noAccounts struct{ account.Store }

func (noAccounts) Load() *account.Config { return &account.Config{} }

func TestPush(t *testing.T) {
	push := func(setup func(r *fakeRefs)) (refs.Pushed, *fakeRefs, error) {
		s, r := newService()
		s.Repos, s.Accounts = pushRepos{}, noAccounts{}
		r.head.Behind = 0
		setup(r)
		p, err := s.Push("/r/.git", func(string) {})
		return p, r, err
	}
	// Tracking a remote branch: push the new commits there.
	p, r, err := push(func(r *fakeRefs) {
		r.head.Ahead, r.target, r.defBranch = 2, [2]string{"origin", "refs/heads/main"}, "main"
	})
	if err != nil || p.First || p.Commits != 2 || p.Account != "work" || p.PRURL != "" || strings.Join(r.ran, ",") != "push origin refs/heads/main:refs/heads/main -u=false" {
		t.Errorf("tracked: %+v %v %v", p, err, r.ran)
	}

	// Nothing new, or both sides moved: no push.
	if p, r, err := push(func(r *fakeRefs) { r.target = [2]string{"origin", "refs/heads/main"} }); err != nil || p.Commits != 0 || len(r.ran) != 0 {
		t.Errorf("nothing to push: %+v %v %v", p, err, r.ran)
	}
	if _, r, err := push(func(r *fakeRefs) { r.head.Ahead, r.head.Behind = 1, 2 }); err == nil || !strings.Contains(err.Error(), "never forces") || len(r.ran) != 0 {
		t.Errorf("diverged: %v %v", err, r.ran)
	}

	// First push: to origin, tracking it, with a pull request link.
	p, r, err = push(func(r *fakeRefs) {
		r.head.Branch, r.head.Upstream, r.remotes, r.defBranch = "feature/x", "", []string{"upstream", "origin"}, "main"
	})
	if err != nil || !p.First || p.Commits != 3 || p.Upstream != "origin/feature/x" || p.PRURL != "https://github.com/acme/api/pull/new/feature/x" ||
		strings.Join(r.ran, ",") != "push origin refs/heads/feature/x:refs/heads/feature/x -u=true" {
		t.Errorf("first: %+v %v %v", p, err, r.ran)
	}

	// Which remote, when there is no origin.
	for _, c := range []struct {
		remotes []string
		want    string
	}{{nil, "no remote to push to"}, {[]string{"a", "b"}, "several remotes (a, b)"}} {
		if _, _, err := push(func(r *fakeRefs) { r.head.Upstream, r.remotes = "", c.remotes }); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("remotes %v: %v", c.remotes, err)
		}
	}

	// Refused by the server: say why.
	if _, _, err := push(func(r *fakeRefs) {
		r.head.Ahead, r.target = 1, [2]string{"origin", "refs/heads/main"}
		r.pushErr = errors.New(" ! [rejected]        main -> main (fetch first)")
	}); err == nil || !strings.Contains(err.Error(), "Update first") {
		t.Errorf("rejected: %v", err)
	}

	if _, _, err := push(func(r *fakeRefs) { r.head.Branch = "" }); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Errorf("detached: %v", err)
	}
}

func TestMoveToNewBranch(t *testing.T) {
	s, r := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	r.head.Ahead, r.head.Behind, r.head.Changes = 2, 0, refs.Changes{New: 1}
	got, err := s.MoveToNewBranch("/r/.git", " login ")
	if err != nil || got.Branch != "login" || got.From != "main" || got.Commits != 2 || got.Base != "base-of-main@{upstream}" || !got.Carried ||
		strings.Join(r.ran, ",") != "create login,move main abc→base-of-main@{upstream}" ||
		strings.Join(rec.done, "|") != "move main: Moved 2 commits from main to login" {
		t.Fatalf("move: %+v %v %v %v", got, err, r.ran, rec.done)
	}

	// The old branch can't be moved: back on it, the new branch deleted.
	r.ran, r.moveErr = nil, errors.New("cannot lock ref")
	if _, err := s.MoveToNewBranch("/r/.git", "y"); err == nil || !strings.Contains(err.Error(), "nothing changed") ||
		strings.Join(r.ran, ",") != "create y,switch main,move y abc→" {
		t.Errorf("move fails: %v %v", err, r.ran)
	}
	r.moveErr = nil

	for name, c := range map[string]struct {
		head refs.Head
		want string
	}{
		"detached":     {refs.Head{Hash: "abc", Ahead: 1}, "HEAD is detached"},
		"no upstream":  {refs.Head{Branch: "main", Hash: "abc"}, "doesn't pull from a remote branch"},
		"all pushed":   {refs.Head{Branch: "main", Hash: "abc", Upstream: "origin/main"}, "nothing to move"},
		"taken name":   {refs.Head{Branch: "main", Hash: "abc", Upstream: "origin/main", Ahead: 1}, "already a branch fix"},
		"middle of it": {refs.Head{Branch: "main", Hash: "abc", Upstream: "origin/main", Ahead: 1, Busy: "rebase"}, "rebase"},
	} {
		r.ran, r.head = nil, c.head
		if _, err := s.MoveToNewBranch("/r/.git", "fix"); err == nil || !strings.Contains(err.Error(), c.want) || len(r.ran) != 0 {
			t.Errorf("%s: %v %v, want %q", name, err, r.ran, c.want)
		}
	}
}

func TestPrune(t *testing.T) {
	s, r := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	r.remotes, r.defBranch, r.target = []string{"origin"}, "main", [2]string{"origin", "refs/heads/x"}
	r.branches = []refs.Branch{
		{Name: "main", Hash: "m"}, {Name: "work", Current: true, Hash: "w"},
		{Name: "done", Hash: "d"}, {Name: "done2", Hash: "d2"}, {Name: "master", Hash: "m"},
		{Name: "other-tree", Elsewhere: true, Hash: "o"}, {Name: "squashed", Gone: true, Hash: "s"}, {Name: "open", Hash: "op"},
		{Name: "origin/main", Remote: true, Hash: "m"},
	}
	// MergedInto lists every branch on origin/main, those GitSwitch keeps too.
	r.merged = []string{"main", "work", "done", "done2", "master", "other-tree"}
	m, err := s.Merged("/r/.git")
	names := func(bs []refs.Branch) string {
		var n []string
		for _, b := range bs {
			n = append(n, b.Name)
		}
		return strings.Join(n, ",")
	}
	if err != nil || m.Into != "origin/main" || names(m.Branches) != "done,done2" || names(m.Kept) != "squashed" ||
		strings.Join(r.ran, ",") != "merged-into refs/remotes/origin/main" {
		t.Fatalf("merged: %+v %v %v", m, err, r.ran)
	}

	r.ran = nil
	if m, err := s.Prune("/r/.git"); err != nil || names(m.Branches) != "done,done2" ||
		strings.Join(r.ran, ",") != "merged-into refs/remotes/origin/main,delete done d,delete done2 d2" ||
		strings.Join(rec.done, "|") != "prune main: Deleted 2 merged branches done@d<origin refs/heads/x,done2@d2<origin refs/heads/x" {
		t.Fatalf("prune: %+v %v %v %v", m, err, r.ran, rec.done)
	}

	// The second can't be deleted: the first is, and is recorded.
	r.ran, rec.done, r.deleteErr = nil, nil, map[string]error{"done2": errors.New("cannot lock ref")}
	if m, err := s.Prune("/r/.git"); err == nil || !strings.Contains(err.Error(), "1 deleted") || names(m.Branches) != "done" ||
		strings.Join(rec.done, "|") != "prune main: Deleted the merged branch done done@d<origin refs/heads/x" {
		t.Fatalf("prune fails half way: %+v %v %v", m, err, rec.done)
	}
	r.deleteErr = nil

	// Nothing merged: nothing deleted, nothing recorded.
	r.ran, rec.done, r.merged = nil, nil, nil
	if m, err := s.Prune("/r/.git"); err != nil || len(m.Branches) != 0 || len(rec.done) != 0 {
		t.Fatalf("nothing to prune: %+v %v %v", m, err, rec.done)
	}

	// Without a known remote default branch, a local main is used.
	r.defBranch, r.ran = "", nil
	if m, err := s.Merged("/r/.git"); err != nil || m.Into != "main" || strings.Join(r.ran, ",") != "merged-into refs/heads/main" {
		t.Fatalf("local main: %+v %v %v", m, err, r.ran)
	}
	r.branches = []refs.Branch{{Name: "trunk", Current: true}}
	if _, err := s.Merged("/r/.git"); err == nil || !strings.Contains(err.Error(), "can't tell which branch") {
		t.Fatalf("no target: %v", err)
	}
}
