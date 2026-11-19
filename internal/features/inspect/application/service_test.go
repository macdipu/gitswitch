package application

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// fake is a repo in memory: commits by name, a reflog and a bisect log.
type fake struct {
	head     refs.Head
	commits  map[string]string // rev -> hash
	mergeB   string
	changes  map[string][]inspect.FileChange
	diffText string
	diffArgs []string
	reflog   []inspect.Entry
	lost     map[string]bool
	branches map[string]string
	origin   [2]string
	log      string
	cands    int
	bisected []string
	reset    string
	started  [2]string
	nameErr  error
}

func (f *fake) Head(string) (refs.Head, error) { return f.head, nil }
func (f *fake) Resolve(_, rev string) (string, error) {
	if h, ok := f.commits[rev]; ok {
		return h, nil
	}
	return "", errors.New("no such " + rev)
}
func (f *fake) MergeBase(string, string, string) string { return f.mergeB }
func (f *fake) Commits(_, tip, not string, limit int) ([]inspect.Commit, int, error) {
	if tip == "bbb" && not == "aaa" {
		return []inspect.Commit{{Hash: "b1", Subject: "on b"}}, 1, nil
	}
	return nil, 0, nil
}
func (f *fake) Changes(_, from, to string) ([]inspect.FileChange, error) {
	return f.changes[from+">"+to], nil
}
func (f *fake) FileDiff(_, from, to, path string) (string, error) {
	f.diffArgs = []string{from, to, path}
	return f.diffText, nil
}
func (f *fake) Info(_, h string) (inspect.Commit, error) {
	return inspect.Commit{Hash: h, Short: h[:min(7, len(h))], Subject: "subject of " + h, Author: "Ana"}, nil
}
func (f *fake) Reflog(_, ref string, skip, limit int) ([]inspect.Entry, error) {
	if skip >= len(f.reflog) {
		return nil, nil
	}
	return f.reflog[skip:min(skip+limit, len(f.reflog))], nil
}
func (f *fake) Unreachable(_ string, hs []string) map[string]bool {
	m := map[string]bool{}
	for _, h := range hs {
		m[h] = f.lost[h]
	}
	return m
}
func (f *fake) CheckName(string) error { return f.nameErr }
func (f *fake) BranchExists(_, n string) bool {
	_, ok := f.branches[n]
	return ok
}
func (f *fake) CreateBranch(_, n, h string) error { f.branches[n] = h; return nil }
func (f *fake) Origin(string) (string, string)    { return f.origin[0], f.origin[1] }
func (f *fake) SaveOrigin(_, b, h string) error   { f.origin = [2]string{b, h}; return nil }
func (f *fake) ClearOrigin(string)                { f.origin = [2]string{} }
func (f *fake) IsAncestor(_, a, b string) bool    { return a < b }
func (f *fake) BisectStart(_, bad, good string) (string, error) {
	f.started = [2]string{bad, good}
	f.head.Busy = "bisect"
	f.head.Branch, f.head.Hash = "", "mid"
	f.log = "# bad: [" + strings.Repeat("b", 40) + "] bad one\n# good: [" + strings.Repeat("a", 40) + "] good one\n"
	f.cands = 7
	return "", nil
}
func (f *fake) BisectMark(_, m string) (string, error) {
	f.bisected = append(f.bisected, m)
	return "", nil
}
func (f *fake) BisectReset(_, to string) error { f.reset = to; f.head.Busy = ""; return nil }
func (f *fake) BisectLog(string) string        { return f.log }
func (f *fake) Candidates(string) int          { return f.cands }
func (f *fake) CommitFiles(string, string, int) ([]string, int, error) {
	return []string{"a.go"}, 0, nil
}

type loc struct{}

func (loc) GitDirFor(string) string  { return "/r/.git" }
func (loc) WorktreeOf(string) string { return "/r" }

func svc(f *fake) *Service { return &Service{Git: f, Locator: loc{}, FS: &filesystem.FS{}} }

func newFake() *fake {
	return &fake{
		head:     refs.Head{Branch: "main", Hash: "head1"},
		commits:  map[string]string{"A": "aaa", "B": "bbb", "HEAD": "bbb", "C": "ccc"},
		mergeB:   "base",
		changes:  map[string][]inspect.FileChange{"base>bbb": {{Path: "x", Status: "added"}}, "aaa>bbb": {{Path: "x", Status: "added"}, {Path: "y", Status: "deleted"}}, "base>": {{Path: "w", Status: "modified"}}, "aaa>": {{Path: "w", Status: "modified"}, {Path: "v", Status: "added"}}},
		lost:     map[string]bool{},
		branches: map[string]string{"main": "x"},
	}
}

func TestCompareModes(t *testing.T) {
	f := newFake()
	s := svc(f)
	c, err := s.Compare("g", "A", "B", inspect.SinceFork)
	if err != nil || len(c.Files) != 1 || c.OnlyBCount != 1 || c.OnlyACount != 0 || c.Base != "base" || c.AHash != "aaa" {
		t.Fatalf("since-fork = %+v, %v", c, err)
	}
	if c, _ = s.Compare("g", "A", "B", inspect.Direct); len(c.Files) != 2 {
		t.Errorf("direct files = %+v", c.Files)
	}
	// B defaults to HEAD; the working folder counts against HEAD's commit.
	if c, err = s.Compare("g", "A", "", inspect.Direct); err != nil || c.B != "HEAD" || c.BHash != "bbb" {
		t.Errorf("default B = %+v %v", c, err)
	}
	if c, err = s.Compare("g", "A", inspect.Working, inspect.SinceFork); err != nil || !c.Working || c.Files[0].Path != "w" {
		t.Errorf("working since-fork = %+v %v", c, err)
	}
	if c, _ = s.Compare("g", "A", inspect.Working, inspect.Direct); len(c.Files) != 2 {
		t.Errorf("working direct = %+v", c.Files)
	}
	// Unrelated histories: only a direct comparison makes sense.
	f.mergeB = ""
	if _, err = s.Compare("g", "A", "B", inspect.SinceFork); err == nil || !strings.Contains(err.Error(), "share no history") {
		t.Errorf("unrelated since-fork: %v", err)
	}
	if _, err = s.Compare("g", "A", "B", inspect.Direct); err != nil {
		t.Errorf("unrelated direct: %v", err)
	}
	for _, c := range [][2]string{{"--x", "B"}, {"A", "-y"}, {"", "B"}, {"nope", "B"}, {"A", "nope"}, {inspect.Working, "B"}} {
		if _, err := s.Compare("g", c[0], c[1], inspect.Direct); err == nil {
			t.Errorf("Compare(%q, %q) should fail", c[0], c[1])
		}
	}
}

func TestCompareFile(t *testing.T) {
	f := newFake()
	s := svc(f)
	f.diffText = "diff --git a/x b/x\n+hi\n"
	d, err := s.CompareFile("g", "A", "B", inspect.SinceFork, "x")
	if err != nil || d.Text != f.diffText || d.Truncated || f.diffArgs[0] != "base" || f.diffArgs[1] != "bbb" {
		t.Errorf("diff = %+v %v args %v", d, err, f.diffArgs)
	}
	if _, err = s.CompareFile("g", "A", "B", inspect.Direct, "x"); err != nil || f.diffArgs[0] != "aaa" {
		t.Errorf("direct args %v %v", f.diffArgs, err)
	}
	f.diffText = "diff --git a/i b/i\nBinary files a/i and b/i differ\n"
	if _, err = s.CompareFile("g", "A", "B", inspect.Direct, "i"); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary: %v", err)
	}
	f.diffText = strings.Repeat("x", inspect.MaxDiffBytes+10)
	if d, _ = s.CompareFile("g", "A", "B", inspect.Direct, "big"); !d.Truncated || len(d.Text) != inspect.MaxDiffBytes {
		t.Errorf("cut: %v %d", d.Truncated, len(d.Text))
	}
	for _, p := range []string{"", "../x", "/etc/passwd", "a/../../x", ":(top)x"} {
		if _, err := s.CompareFile("g", "A", "B", inspect.Direct, p); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func entries(n int) []inspect.Entry {
	var es []inspect.Entry
	for i := 0; i < n; i++ {
		es = append(es, inspect.Entry{Index: i, Ref: fmt.Sprintf("HEAD@{%d}", i), Hash: fmt.Sprintf("h%d", i), Action: "commit"})
	}
	return es
}

func TestReflogPaging(t *testing.T) {
	f := newFake()
	f.reflog = entries(25)
	f.lost = map[string]bool{"h3": true, "h20": true}
	s := svc(f)
	r, err := s.Reflog("g", "", 10, 0, false)
	if err != nil || len(r.Entries) != 10 || !r.More || r.Next != 10 || !r.Entries[3].Lost || r.Entries[4].Lost {
		t.Fatalf("page 1 = %+v %v", r, err)
	}
	r, _ = s.Reflog("g", "HEAD", 10, 20, false)
	if len(r.Entries) != 5 || r.More || r.Entries[0].Index != 20 || !r.Entries[0].Lost {
		t.Errorf("last page = %+v", r)
	}
	if r, _ = s.Reflog("g", "", 10, 99, false); len(r.Entries) != 0 || r.More || r.Entries == nil {
		t.Errorf("past the end = %+v", r)
	}
	r, _ = s.Reflog("g", "", 1, 0, true)
	if len(r.Entries) != 1 || r.Entries[0].Index != 3 || !r.More || r.Next != 4 {
		t.Errorf("lost only, first = %+v", r)
	}
	r, _ = s.Reflog("g", "", 5, r.Next, true)
	if len(r.Entries) != 1 || r.Entries[0].Index != 20 || r.More {
		t.Errorf("lost only, rest = %+v", r)
	}
	if r, _ = s.Reflog("g", "", 0, -5, false); len(r.Entries) != 25 || r.Entries[0].Index != 0 {
		t.Errorf("defaults = %d entries", len(r.Entries))
	}
	if _, err = s.Reflog("g", "--all", 5, 0, false); err == nil {
		t.Error("option-like ref accepted")
	}
}

func TestRecover(t *testing.T) {
	f := newFake()
	f.reflog = entries(5)
	f.reflog[2].Hash = "lost2222"
	f.lost = map[string]bool{"lost2222": true}
	f.commits["lost2222"] = "lost2222"
	f.commits["ccc"] = "ccc"
	s := svc(f)
	r, err := s.Recover("g", "", 2, "", "rescued")
	if err != nil || f.branches["rescued"] != "lost2222" || !r.Lost || r.Subject != "subject of lost2222" || !strings.Contains(r.Message, "still where you were") {
		t.Fatalf("recover = %+v %v branches %v", r, err, f.branches)
	}
	if f.head.Branch != "main" {
		t.Error("the current branch moved")
	}
	if r, err = s.Recover("g", "", -1, "ccc", "by-hash"); err != nil || f.branches["by-hash"] != "ccc" || r.Lost {
		t.Errorf("by hash = %+v %v", r, err)
	}
	for name, c := range map[string]struct {
		idx  int
		hash string
		to   string
	}{
		"taken name": {2, "", "main"}, "empty name": {2, "", " "}, "option name": {2, "", "-x"},
		"no entry": {99, "", "n1"}, "no target": {-1, "", "n2"}, "option hash": {-1, "-x", "n3"}, "unknown hash": {-1, "zzz", "n4"},
	} {
		if _, err := s.Recover("g", "", c.idx, c.hash, c.to); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	f.nameErr = errors.New("bad")
	if _, err := s.Recover("g", "", 2, "", "has space"); err == nil || !strings.Contains(err.Error(), "can't be a branch name") {
		t.Errorf("git's name check: %v", err)
	}
}

func TestBisectFlow(t *testing.T) {
	f := newFake()
	f.commits = map[string]string{"bad": "bbb", "good": "aaa"}
	s := svc(f)
	if st, err := s.BisectStatus("g"); err != nil || st.Running || st.Message != "No bisect is running." {
		t.Fatalf("idle = %+v %v", st, err)
	}
	st, err := s.BisectStart("g", "bad", "good")
	if err != nil || !st.Running || st.Next == nil || st.Next.Hash != "mid" || st.StepsLeft != 2 || st.Left != 7 || len(st.Steps) != 2 || st.Original != "main" {
		t.Fatalf("start = %+v %v", st, err)
	}
	if f.started != [2]string{"bbb", "aaa"} || f.origin != [2]string{"main", "head1"} {
		t.Errorf("started %v origin %v", f.started, f.origin)
	}
	if _, err := s.BisectStart("g", "bad", "good"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("start twice: %v", err)
	}
	if _, err := s.BisectMark("g", "sideways"); err == nil {
		t.Error("bad mark accepted")
	}
	f.cands = 3
	if st, err = s.BisectMark("g", "good"); err != nil || f.bisected[0] != "good" || st.StepsLeft != 1 {
		t.Errorf("mark = %+v %v %v", st, err, f.bisected)
	}
	// The log now names the culprit.
	f.log += "# first bad commit: [" + strings.Repeat("c", 40) + "] culprit\n"
	f.cands = 1
	st, err = s.BisectStatus("g")
	if err != nil || st.Culprit == nil || st.Culprit.Hash != strings.Repeat("c", 40) || st.Culprit.Files[0] != "a.go" || st.Next != nil || st.StepsLeft != 0 {
		t.Fatalf("culprit = %+v %v", st, err)
	}
	if _, err := s.BisectMark("g", "bad"); err == nil || !strings.Contains(err.Error(), "search is over") {
		t.Errorf("mark after the end: %v", err)
	}
	st, err = s.BisectReset("g")
	if err != nil || f.reset != "main" || st.Running || !strings.Contains(st.Message, "back on main") || f.origin != [2]string{} {
		t.Errorf("reset = %+v %v to %q", st, err, f.reset)
	}
	if _, err := s.BisectReset("g"); err == nil {
		t.Error("reset twice")
	}
}

func TestBisectStartRefusals(t *testing.T) {
	f := newFake()
	f.commits = map[string]string{"bad": "bbb", "good": "aaa", "same": "bbb"}
	s := svc(f)
	f.head.Changes = refs.Changes{Changed: 2}
	if _, err := s.BisectStart("g", "bad", "good"); err == nil || !strings.Contains(err.Error(), "commit, park or discard") {
		t.Errorf("dirty: %v", err)
	}
	f.head.Changes = refs.Changes{}
	f.head.Busy = "rebase"
	if _, err := s.BisectStart("g", "bad", "good"); err == nil {
		t.Error("during a rebase")
	}
	f.head.Busy = ""
	for name, c := range map[string][2]string{"same": {"bad", "same"}, "order": {"good", "bad"}, "option": {"-x", "good"}, "unknown": {"bad", "zzz"}, "empty": {"", "good"}} {
		if _, err := s.BisectStart("g", c[0], c[1]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if f.started != [2]string{} || f.origin != [2]string{} {
		t.Error("a refused start must change nothing")
	}
	f.head = refs.Head{}
	if _, err := s.BisectStart("g", "bad", "good"); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Errorf("no commits: %v", err)
	}
}

func TestBisectDetachedOriginAndSkipped(t *testing.T) {
	f := newFake()
	f.head = refs.Head{Hash: strings.Repeat("d", 40)}
	f.commits = map[string]string{"bad": "bbb", "good": "aaa"}
	s := svc(f)
	st, err := s.BisectStart("g", "bad", "good")
	if err != nil || st.Original != "ddddddd" {
		t.Fatalf("start = %+v %v", st, err)
	}
	f.log += "# only skipped commits left to test\n# possible first bad commit: [" + strings.Repeat("e", 40) + "] e\n# possible first bad commit: [" + strings.Repeat("f", 40) + "] f\n"
	if st, _ = s.BisectStatus("g"); len(st.Maybe) != 2 || st.Culprit != nil || st.Next != nil {
		t.Errorf("skipped = %+v", st)
	}
	if st, err = s.BisectReset("g"); err != nil || f.reset != strings.Repeat("d", 40) || !strings.Contains(st.Message, "ddddddd") {
		t.Errorf("reset = %+v %v to %q", st, err, f.reset)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFake()
	f.commits = map[string]string{"bad": "bbb", "good": "aaa"}
	s := &Service{Git: f, Locator: loc{}, FS: &filesystem.FS{DryRun: true, Out: &strings.Builder{}}}
	if _, err := s.BisectStart("g", "bad", "good"); err != nil || f.started != [2]string{} || f.origin != [2]string{} {
		t.Errorf("dry start: %v %v", err, f.started)
	}
	f.commits["x"] = "xxx"
	if _, err := s.Recover("g", "", -1, "x", "n"); err != nil || len(f.branches) != 1 {
		t.Errorf("dry recover: %v %v", err, f.branches)
	}
}
