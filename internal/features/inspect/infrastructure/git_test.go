package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"os/exec"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	inspectapp "github.com/macdipu/gitswitch/internal/features/inspect/application"
	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type env struct {
	t   *testing.T
	dir string
	gd  string
	svc *inspectapp.Service
}

func (e *env) git(args ...string) string {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", e.dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *env) write(name, text string) {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) commit(name, text, msg string) string {
	e.t.Helper()
	e.write(name, text)
	e.git("add", "--all")
	e.git("commit", "-q", "-m", msg)
	return e.git("rev-parse", "HEAD")
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "Me", "GIT_AUTHOR_EMAIL": "me@example.com", "GIT_COMMITTER_NAME": "Me", "GIT_COMMITTER_EMAIL": "me@example.com"} {
		t.Setenv(k, v)
	}
	repo := filepath.Join(dir, "repo")
	_ = os.MkdirAll(repo, 0o755)
	e := &env{t: t, dir: repo}
	e.git("init", "-q", "-b", "main")
	run := &gitinfra.Runner{}
	e.gd = run.GitDirFor(repo)
	e.svc = &inspectapp.Service{Git: &Git{Git: run}, Locator: run, FS: &filesystem.FS{}}
	return e
}

func paths(fs []inspect.FileChange) string {
	var p []string
	for _, f := range fs {
		p = append(p, f.Status+":"+f.Path)
	}
	return strings.Join(p, " ")
}

// main and feature went apart at base: main added m.txt and changed
// shared.txt, feature added f.txt, renamed old.txt and added an image.
func TestCompareDivergedBranches(t *testing.T) {
	e := newEnv(t)
	e.commit("shared.txt", "one\n", "base shared")
	e.commit("old.txt", "some long enough text\nto be recognised\nas the same file\n", "base old")
	e.git("switch", "-q", "-c", "feature")
	e.commit("f.txt", "feature\n", "feature adds f")
	e.git("mv", "old.txt", "new.txt")
	e.git("commit", "-q", "-m", "feature renames old")
	e.write("img.bin", "a\x00b\x00")
	e.git("add", "--all")
	e.git("commit", "-q", "-m", "feature adds a binary")
	e.git("switch", "-q", "main")
	e.commit("m.txt", "main\n", "main adds m")
	e.commit("shared.txt", "one\ntwo\n", "main changes shared")

	c, err := e.svc.Compare(e.gd, "main", "feature", inspect.SinceFork)
	if err != nil {
		t.Fatal(err)
	}
	if c.OnlyACount != 2 || c.OnlyBCount != 3 || len(c.OnlyB) != 3 || c.OnlyB[0].Subject != "feature adds a binary" {
		t.Errorf("commits: only main %d, only feature %d: %+v", c.OnlyACount, c.OnlyBCount, c.OnlyB)
	}
	if got := paths(c.Files); got != "added:f.txt added:img.bin renamed:new.txt" {
		t.Errorf("since-fork files = %q", got)
	}
	for _, f := range c.Files {
		if f.Path == "new.txt" && f.OldPath != "old.txt" {
			t.Errorf("rename lost its old path: %+v", f)
		}
		if f.Path == "img.bin" && !f.Binary {
			t.Errorf("img.bin should be binary: %+v", f)
		}
	}
	if c.Base == "" || c.Base != e.git("merge-base", "main", "feature") {
		t.Errorf("base = %q", c.Base)
	}

	d, err := e.svc.Compare(e.gd, "main", "feature", inspect.Direct)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(d.Files); got != "added:f.txt added:img.bin deleted:m.txt renamed:new.txt modified:shared.txt" && got != "added:f.txt added:img.bin renamed:new.txt deleted:m.txt modified:shared.txt" {
		t.Errorf("direct files = %q", got)
	}
	if !strings.Contains(c.Summary, "Since they went apart") || strings.Contains(d.Summary, "Since they went apart") {
		t.Errorf("summaries: %q / %q", c.Summary, d.Summary)
	}

	// One file's diff in each mode.
	fd, err := e.svc.CompareFile(e.gd, "main", "feature", inspect.SinceFork, "f.txt")
	if err != nil || !strings.Contains(fd.Text, "+feature") {
		t.Errorf("file diff: %v %q", err, fd.Text)
	}
	dd, err := e.svc.CompareFile(e.gd, "main", "feature", inspect.Direct, "shared.txt")
	if err != nil || !strings.Contains(dd.Text, "-two") {
		t.Errorf("direct diff of shared.txt: %v %q", err, dd.Text)
	}
	sd, err := e.svc.CompareFile(e.gd, "main", "feature", inspect.SinceFork, "shared.txt")
	if err != nil || sd.Text != "" {
		t.Errorf("shared.txt did not change on feature since the fork: %v %q", err, sd.Text)
	}
	if _, err := e.svc.CompareFile(e.gd, "main", "feature", inspect.SinceFork, "img.bin"); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary diff error = %v", err)
	}

	// A big diff is cut.
	e.git("switch", "-q", "feature")
	e.commit("big.txt", strings.Repeat("0123456789abcdef\n", 40000), "big")
	bd, err := e.svc.CompareFile(e.gd, "main", "feature", inspect.SinceFork, "big.txt")
	if err != nil || !bd.Truncated || len(bd.Text) != inspect.MaxDiffBytes {
		t.Errorf("big diff: %v truncated=%v len=%d", err, bd.Truncated, len(bd.Text))
	}

	// Option-like refs, unknown refs.
	for _, bad := range []string{"--output=x", "no-such"} {
		if _, err := e.svc.Compare(e.gd, bad, "feature", inspect.Direct); err == nil {
			t.Errorf("compare %q should fail", bad)
		}
	}
}

func TestCompareWorkingFolderAndCommitList(t *testing.T) {
	e := newEnv(t)
	first := e.commit("a.txt", "a\n", "one")
	for i := 0; i < 60; i++ {
		e.commit("n.txt", fmt.Sprint(i, "\n"), fmt.Sprint("n", i))
	}
	c, err := e.svc.Compare(e.gd, first, "HEAD", inspect.SinceFork)
	if err != nil {
		t.Fatal(err)
	}
	if c.OnlyBCount != 60 || len(c.OnlyB) != inspect.MaxCommits || c.OnlyACount != 0 {
		t.Errorf("cap: %d listed of %d, only A %d", len(c.OnlyB), c.OnlyBCount, c.OnlyACount)
	}
	// B empty means HEAD; the working folder as B shows uncommitted edits.
	e.write("a.txt", "changed on disk\n")
	w, err := e.svc.Compare(e.gd, "HEAD", inspect.Working, inspect.Direct)
	if err != nil || !w.Working || paths(w.Files) != "modified:a.txt" {
		t.Errorf("working compare: %v %q", err, paths(w.Files))
	}
	wd, err := e.svc.CompareFile(e.gd, "HEAD", inspect.Working, inspect.Direct, "a.txt")
	if err != nil || !strings.Contains(wd.Text, "+changed on disk") {
		t.Errorf("working diff: %v %q", err, wd.Text)
	}
	if _, err := e.svc.Compare(e.gd, inspect.Working, "HEAD", inspect.Direct); err == nil {
		t.Error("the working folder as side A should be refused")
	}
	same, err := e.svc.Compare(e.gd, "HEAD", "", inspect.Direct)
	if err != nil || !same.Same {
		t.Errorf("HEAD vs HEAD: %v %+v", err, same.Summary)
	}
}

func TestReflogLostCommitAndRecovery(t *testing.T) {
	e := newEnv(t)
	e.commit("a.txt", "a\n", "first")
	e.commit("a.txt", "b\n", "second")
	lost := e.commit("a.txt", "c\n", "third, to be lost")
	e.git("reset", "-q", "--hard", "HEAD~1")
	e.git("switch", "-q", "-c", "other")
	e.git("switch", "-q", "main")

	r, err := e.svc.Reflog(e.gd, "", 30, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != "HEAD" || len(r.Entries) != 6 || r.More {
		t.Fatalf("entries = %d more=%v", len(r.Entries), r.More)
	}
	top := r.Entries[0]
	if top.Index != 0 || top.Ref != "HEAD@{0}" || top.Action != "checkout" || top.Lost {
		t.Errorf("newest = %+v", top)
	}
	var found *inspect.Entry
	for i, en := range r.Entries {
		if en.Hash == lost {
			found = &r.Entries[i]
			if !en.Lost || en.Action != "commit" || en.Subject != "third, to be lost" {
				t.Errorf("lost entry = %+v", en)
			}
		} else if en.Lost {
			t.Errorf("entry %d should be reachable: %+v", en.Index, en)
		}
	}
	if found == nil {
		t.Fatal("the lost commit is not in the reflog")
	}
	actions := map[string]bool{}
	for _, en := range r.Entries {
		actions[en.Action] = true
	}
	if !actions["checkout"] || !actions["commit"] {
		t.Errorf("actions = %v", actions)
	}

	// Paging, and only the lost ones.
	p, err := e.svc.Reflog(e.gd, "HEAD", 2, 1, false)
	if err != nil || len(p.Entries) != 2 || p.Entries[0].Index != 1 || !p.More || p.Next != 3 {
		t.Errorf("page = %+v %v", p, err)
	}
	l, err := e.svc.Reflog(e.gd, "", 10, 0, true)
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Hash != lost {
		t.Errorf("lost only = %+v %v", l, err)
	}
	if b, err := e.svc.Reflog(e.gd, "main", 5, 0, false); err != nil || len(b.Entries) == 0 || b.Ref != "main" {
		t.Errorf("branch reflog: %+v %v", b, err)
	}
	if _, err := e.svc.Reflog(e.gd, "nope", 5, 0, false); err == nil {
		t.Error("a branch without a reflog should say so")
	}

	// Recover: a branch at the lost commit; main stays where it is.
	head := e.git("rev-parse", "HEAD")
	rec, err := e.svc.Recover(e.gd, "", found.Index, "", "rescued")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Lost || rec.Hash != lost || e.git("rev-parse", "rescued") != lost {
		t.Errorf("recovered = %+v", rec)
	}
	if e.git("rev-parse", "HEAD") != head || e.git("symbolic-ref", "--short", "HEAD") != "main" {
		t.Error("recovery moved the current branch")
	}
	// Now it is on a branch.
	if l, _ := e.svc.Reflog(e.gd, "", 10, 0, true); len(l.Entries) != 0 {
		t.Errorf("after recovery nothing is lost: %+v", l.Entries)
	}
	if _, err := e.svc.Recover(e.gd, "", found.Index, "", "rescued"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("same name again: %v", err)
	}
	if _, err := e.svc.Recover(e.gd, "", 999, "", "x"); err == nil {
		t.Error("an entry that does not exist should be refused")
	}
	if _, err := e.svc.Recover(e.gd, "", -1, lost, "by hash"); err == nil {
		t.Error("a bad branch name should be refused")
	}
	if rec, err := e.svc.Recover(e.gd, "", -1, lost, "by-hash"); err != nil || rec.Lost {
		t.Errorf("by hash: %+v %v", rec, err)
	}
}

// 16 commits, the bug appears in c9: files hold "ok" before, "broken" from there.
func bisectRepo(t *testing.T) (*env, []string) {
	e := newEnv(t)
	var hs []string
	for i := 1; i <= 16; i++ {
		state := "ok"
		if i >= 9 {
			state = "broken"
		}
		e.write("state.txt", state+"\n")
		hs = append(hs, e.commit("n.txt", fmt.Sprint(i, "\n"), fmt.Sprintf("c%d", i)))
	}
	return e, hs
}

// answer marks the checked-out commit by reading the file.
func answer(e *env) string {
	b, _ := os.ReadFile(filepath.Join(e.dir, "state.txt"))
	if strings.TrimSpace(string(b)) == "broken" {
		return inspect.Bad
	}
	return inspect.Good
}

func TestBisectFindsTheCulprit(t *testing.T) {
	e, hs := bisectRepo(t)
	if st, err := e.svc.BisectStatus(e.gd); err != nil || st.Running {
		t.Fatalf("no bisect yet: %+v %v", st, err)
	}
	st, err := e.svc.BisectStart(e.gd, "main", hs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Next == nil || st.Original != "main" || st.StepsLeft < 1 || len(st.Steps) != 2 {
		t.Fatalf("start = %+v", st)
	}
	if !strings.Contains(st.Message, "steps") && !strings.Contains(st.Message, "step") {
		t.Errorf("message = %q", st.Message)
	}
	if _, err := e.svc.BisectStart(e.gd, "main", hs[0]); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second start: %v", err)
	}
	// Refused while testing with an edit to a tracked file? That is the
	// user's business here; mark with a skip first.
	n, skipped := 0, false
	for st.Culprit == nil && n < 20 {
		n++
		mark := answer(e)
		// A commit that can't be tested is skipped once; not c8 or c9, or
		// the culprit could not be told apart.
		if !skipped && st.Next.Hash != hs[7] && st.Next.Hash != hs[8] {
			mark, skipped = inspect.Skip, true
		}
		if st, err = e.svc.BisectMark(e.gd, mark); err != nil {
			t.Fatal(err)
		}
	}
	if st.Culprit == nil || st.Culprit.Hash != hs[8] || st.Culprit.Subject != "c9" || st.Culprit.Author != "Me" {
		t.Fatalf("culprit = %+v after %d steps", st.Culprit, n)
	}
	if got := strings.Join(st.Culprit.Files, ","); got != "n.txt,state.txt" {
		t.Errorf("culprit files = %q", got)
	}
	if st.Next != nil || len(st.Steps) < 4 {
		t.Errorf("after the culprit: next=%v steps=%d", st.Next, len(st.Steps))
	}
	if _, err := e.svc.BisectMark(e.gd, inspect.Good); err == nil {
		t.Error("marking after the search is over should be refused")
	}
	// Status works from the repo alone: a restart.
	again, err := e.svc.BisectStatus(e.gd)
	if err != nil || again.Culprit == nil || again.Culprit.Hash != hs[8] || again.Original != "main" {
		t.Errorf("status = %+v %v", again, err)
	}
	// Reset returns to the branch.
	if st, err = e.svc.BisectReset(e.gd); err != nil || st.Running {
		t.Fatalf("reset: %+v %v", st, err)
	}
	if e.git("symbolic-ref", "--short", "HEAD") != "main" || e.git("rev-parse", "HEAD") != hs[15] {
		t.Error("reset did not return to main")
	}
	// exit status 1 with no output: no setting is left
	if out, _ := exec.Command("git", "-C", e.dir, "config", "--get-regexp", "^gitswitch\\.bisect").Output(); len(out) != 0 {
		t.Errorf("bisect settings left behind: %q", out)
	}
}

// Skipping the commit next to the culprit leaves it open: the answer is
// "one of these two", not a made-up culprit.
func TestBisectTooManySkips(t *testing.T) {
	e, hs := bisectRepo(t)
	st, err := e.svc.BisectStart(e.gd, "main", hs[0])
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 20 && len(st.Maybe) == 0 && st.Culprit == nil; n++ {
		mark := answer(e)
		if st.Next.Hash == hs[7] {
			mark = inspect.Skip
		}
		if st, err = e.svc.BisectMark(e.gd, mark); err != nil {
			t.Fatal(err)
		}
	}
	if st.Culprit != nil || len(st.Maybe) != 2 || st.Next != nil || !strings.Contains(st.Message, "skipped") {
		t.Fatalf("state = %+v", st)
	}
	if _, err := e.svc.BisectReset(e.gd); err != nil || e.git("symbolic-ref", "--short", "HEAD") != "main" {
		t.Errorf("reset: %v", err)
	}
}

func TestBisectRefusals(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.BisectStart(e.gd, "HEAD", "HEAD"); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Errorf("empty repo: %v", err)
	}
	var hs []string
	for i := 1; i <= 4; i++ {
		hs = append(hs, e.commit("f.txt", fmt.Sprint(i, "\n"), fmt.Sprint("c", i)))
	}
	e.write("f.txt", "edited\n")
	if _, err := e.svc.BisectStart(e.gd, "main", hs[0]); err == nil || !strings.Contains(err.Error(), "commit, park or discard") {
		t.Errorf("dirty tree: %v", err)
	}
	e.git("checkout", "--", "f.txt")
	e.write("untracked.txt", "fine\n")
	if _, err := e.svc.BisectStart(e.gd, hs[0], "main"); err == nil || !strings.Contains(err.Error(), "not older") {
		t.Errorf("good after bad: %v", err)
	}
	if _, err := e.svc.BisectStart(e.gd, "main", "main"); err == nil {
		t.Error("same commit twice")
	}
	if _, err := e.svc.BisectStart(e.gd, "--oops", hs[0]); err == nil {
		t.Error("option-like ref")
	}
	if _, err := e.svc.BisectMark(e.gd, inspect.Good); err == nil || !strings.Contains(err.Error(), "no bisect is running") {
		t.Errorf("mark without a bisect: %v", err)
	}
	if _, err := e.svc.BisectReset(e.gd); err == nil {
		t.Error("reset without a bisect")
	}
	if _, err := e.svc.BisectMark(e.gd, "maybe"); err == nil {
		t.Error("unknown mark")
	}
	// During a merge.
	e.git("switch", "-q", "-c", "x", hs[0])
	e.commit("f.txt", "x\n", "x")
	e.git("switch", "-q", "main")
	_ = exec.Command("git", "-C", e.dir, "merge", "x").Run()
	if _, err := e.svc.BisectStart(e.gd, "main", hs[0]); err == nil || !strings.Contains(err.Error(), "merge") {
		t.Errorf("during a merge: %v", err)
	}
}

// A repo started on a detached HEAD returns there; reset in a bisect that
// began on a branch while a tracked file is dirty by the user's testing
// is git's own error, passed on.
func TestBisectFromDetachedHead(t *testing.T) {
	e, hs := bisectRepo(t)
	e.git("checkout", "-q", "--detach", hs[12])
	st, err := e.svc.BisectStart(e.gd, hs[12], hs[0])
	if err != nil || st.Original != hs[12][:7] {
		t.Fatalf("start: %+v %v", st, err)
	}
	if st, err = e.svc.BisectReset(e.gd); err != nil || !strings.Contains(st.Message, hs[12][:7]) {
		t.Fatalf("reset: %+v %v", st, err)
	}
	if e.git("rev-parse", "HEAD") != hs[12] {
		t.Error("not back on the detached commit")
	}
}
