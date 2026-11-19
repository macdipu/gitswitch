package infrastructure_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

type env struct {
	t   *testing.T
	dir string
	gd  string
	app *bootstrap.App
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
	if err := os.WriteFile(filepath.Join(e.dir, name), []byte(text), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) commit(name, text, msg string) {
	e.t.Helper()
	e.write(name, text)
	e.git("add", "--all")
	e.git("commit", "-q", "-m", msg)
}

func (e *env) log() string { return e.git("log", "--format=%s", "--reverse") }

// newEnv makes main (base, m1) and feature (base, f1, f2, f3) that touch
// different files, so a rebase is clean; the repo is on feature.
func newEnv(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "Me", "GIT_AUTHOR_EMAIL": "me@example.com", "GIT_COMMITTER_NAME": "Me", "GIT_COMMITTER_EMAIL": "me@example.com"} {
		t.Setenv(k, v)
	}
	e := &env{t: t, dir: dir, gd: filepath.Join(dir, ".git"), app: bootstrap.New(false, io.Discard)}
	e.git("init", "-q", "-b", "main")
	e.commit("a.txt", "base\n", "base")
	e.git("switch", "-q", "-c", "feature")
	e.commit("f1.txt", "1\n", "f1")
	e.commit("f2.txt", "2\n", "f2")
	e.commit("f3.txt", "3\n", "f3")
	e.git("switch", "-q", "main")
	e.commit("m1.txt", "m\n", "m1")
	e.git("switch", "-q", "feature")
	return e
}

func TestPlan(t *testing.T) {
	e := newEnv(t)
	p, err := e.app.Rebase.Plan(e.gd, "main")
	if err != nil || p.Blocked != "" || p.Branch != "feature" || p.ReplayedCount != 3 || p.IncomingCount != 1 || p.FastForward || p.UpToDate {
		t.Fatalf("Plan = %+v, %v", p, err)
	}
	if p.Replayed[0].Subject != "f1" || p.Replayed[2].Subject != "f3" || p.Incoming[0].Subject != "m1" || len(p.BothChanged) != 0 {
		t.Fatalf("Plan lists = %+v", p)
	}
	if e.git("rev-parse", "main") != p.Hash {
		t.Fatalf("hash %s", p.Hash)
	}
	// Both sides change a.txt: likely clash.
	e.commit("a.txt", "feature\n", "f4")
	e.git("switch", "-q", "main")
	e.commit("a.txt", "main\n", "m2")
	e.git("switch", "-q", "feature")
	p, _ = e.app.Rebase.Plan(e.gd, "main")
	if strings.Join(p.BothChanged, ",") != "a.txt" || !strings.Contains(p.Summary, "estimate") {
		t.Fatalf("BothChanged = %+v", p)
	}
	for _, name := range []string{"nope", "feature", "-x", ""} {
		if _, err := e.app.Rebase.Plan(e.gd, name); err == nil {
			t.Errorf("Plan(%q) accepted", name)
		}
	}
}

func TestPlanCaps(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < rebase.MaxCommits+5; i++ {
		e.commit("f1.txt", strings.Repeat("x", i+2), "more")
	}
	p, err := e.app.Rebase.Plan(e.gd, "main")
	if err != nil || p.ReplayedCount != 3+rebase.MaxCommits+5 || len(p.Replayed) != rebase.MaxCommits || p.Replayed[0].Subject != "f1" {
		t.Fatalf("cap: %d/%d %v", len(p.Replayed), p.ReplayedCount, err)
	}
}

func TestRunClean(t *testing.T) {
	e := newEnv(t)
	orig := e.git("rev-parse", "HEAD")
	r, err := e.app.Rebase.Run(e.gd, "main")
	if err != nil || r.Outcome != rebase.Done || r.Replayed != 3 || r.Hash != e.git("rev-parse", "HEAD") || r.Hash == orig {
		t.Fatalf("Run = %+v, %v", r, err)
	}
	if got := e.log(); got != "base\nm1\nf1\nf2\nf3" {
		t.Fatalf("log = %q", got)
	}
	if e.git("rev-parse", "main") == r.Hash {
		t.Fatal("main moved")
	}
	// Undo puts the branch back exactly: same commits, same hashes.
	last, err := e.app.Undo.Last(e.gd)
	if err != nil || last.Action != undo.Rebase || last.Blocked != "" {
		t.Fatalf("Last = %+v, %v", last, err)
	}
	if _, err := e.app.Undo.Undo(e.gd); err != nil {
		t.Fatal(err)
	}
	if e.git("rev-parse", "HEAD") != orig || e.git("status", "--porcelain") != "" {
		t.Fatal("undo did not restore the branch")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "m1.txt")); err == nil {
		t.Fatal("m1.txt should be gone after undo")
	}
	// ... and redo does it again.
	if _, err := e.app.Undo.Redo(e.gd); err != nil || e.git("rev-parse", "HEAD") != r.Hash {
		t.Fatalf("redo: %v", err)
	}
	// A second run: up to date.
	r, err = e.app.Rebase.Run(e.gd, "main")
	if err != nil || r.Outcome != rebase.UpToDate {
		t.Fatalf("second run = %+v, %v", r, err)
	}
}

func TestFastForwardAndUpToDate(t *testing.T) {
	e := newEnv(t)
	e.git("switch", "-q", "-c", "empty", "main~1")
	r, err := e.app.Rebase.Run(e.gd, "main")
	if err != nil || r.Outcome != rebase.FastForwards || e.git("rev-parse", "HEAD") != e.git("rev-parse", "main") {
		t.Fatalf("ff = %+v, %v", r, err)
	}
	if last, err := e.app.Undo.Last(e.gd); err != nil || last.Action != undo.Rebase {
		t.Fatalf("ff undo = %+v, %v", last, err)
	}
	// main is an ancestor of feature after a rebase: nothing to do.
	e.git("switch", "-q", "feature")
	if _, err := e.app.Rebase.Run(e.gd, "main"); err != nil {
		t.Fatal(err)
	}
	before := e.git("rev-parse", "HEAD")
	r, err = e.app.Rebase.Run(e.gd, "main~1")
	if err != nil || r.Outcome != rebase.UpToDate || e.git("rev-parse", "HEAD") != before {
		t.Fatalf("up to date = %+v, %v", r, err)
	}
}

func TestRefusals(t *testing.T) {
	refuse := func(e *env, want string) {
		t.Helper()
		head := e.git("rev-parse", "HEAD")
		p, err := e.app.Rebase.Plan(e.gd, "main")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p.Blocked, want) {
			t.Fatalf("Blocked = %q, want %q", p.Blocked, want)
		}
		if _, err := e.app.Rebase.Run(e.gd, "main"); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Run = %v, want %q", err, want)
		}
		if e.git("rev-parse", "HEAD") != head {
			t.Fatal("HEAD moved")
		}
	}
	t.Run("uncommitted", func(t *testing.T) {
		e := newEnv(t)
		e.write("f1.txt", "dirty\n")
		refuse(e, "commit, park or discard them first")
		if e.git("status", "--porcelain") != "M f1.txt" {
			t.Fatal("work lost")
		}
	})
	t.Run("busy", func(t *testing.T) {
		e := newEnv(t)
		e.git("switch", "-q", "main")
		e.commit("a.txt", "m\n", "m2")
		e.git("switch", "-q", "feature")
		e.git("cherry-pick", "main") // fine; then a stopped one:
		e.git("reset", "-q", "--hard", "HEAD~1")
		e.git("switch", "-q", "-c", "other", "main~1")
		e.commit("a.txt", "o\n", "o1")
		if out, err := exec.Command("git", "-C", e.dir, "cherry-pick", "main").CombinedOutput(); err == nil {
			t.Fatalf("expected a clash: %s", out)
		}
		refuse(e, "in the middle of a cherry-pick")
	})
	t.Run("detached", func(t *testing.T) {
		e := newEnv(t)
		e.git("switch", "-q", "--detach")
		refuse(e, "HEAD is detached")
	})
	t.Run("merge commit", func(t *testing.T) {
		e := newEnv(t)
		e.git("switch", "-q", "-c", "side", "main~1")
		e.commit("s.txt", "s\n", "s1")
		e.git("switch", "-q", "feature")
		e.git("merge", "-q", "--no-ff", "-m", "merge side", "side")
		refuse(e, "merge commit")
	})
	t.Run("unrelated", func(t *testing.T) {
		e := newEnv(t)
		e.git("switch", "-q", "--orphan", "lonely")
		e.commit("z.txt", "z\n", "z")
		e.git("switch", "-q", "feature")
		p, err := e.app.Rebase.Plan(e.gd, "lonely")
		if err != nil || !strings.Contains(p.Blocked, "share no history") {
			t.Fatalf("Plan = %+v, %v", p, err)
		}
		if _, err := e.app.Rebase.Run(e.gd, "lonely"); err == nil {
			t.Fatal("ran")
		}
	})
	t.Run("pushed", func(t *testing.T) {
		e := newEnv(t)
		bare := filepath.Join(t.TempDir(), "origin.git")
		if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
		e.git("remote", "add", "origin", bare)
		e.git("push", "-q", "origin", "feature:feature")
		refuse(e, "already pushed")
		// A new local commit on top is not enough: the older ones are pushed.
		e.commit("f4.txt", "4\n", "f4")
		refuse(e, "rewrite pushed commits")
	})
	t.Run("only some local commits unpushed are still refused if any is pushed", func(t *testing.T) {
		e := newEnv(t)
		bare := filepath.Join(t.TempDir(), "origin.git")
		_ = exec.Command("git", "init", "-q", "--bare", bare).Run()
		e.git("remote", "add", "origin", bare)
		e.git("push", "-q", "origin", "feature~2:refs/heads/feature")
		refuse(e, "already pushed")
	})
	t.Run("unpushed with a remote is fine", func(t *testing.T) {
		e := newEnv(t)
		bare := filepath.Join(t.TempDir(), "origin.git")
		_ = exec.Command("git", "init", "-q", "--bare", bare).Run()
		e.git("remote", "add", "origin", bare)
		e.git("push", "-q", "origin", "main:main")
		e.git("fetch", "-q", "origin")
		if r, err := e.app.Rebase.Run(e.gd, "origin/main"); err != nil || r.Outcome != rebase.Done {
			t.Fatalf("%+v %v", r, err)
		}
	})
}

// clashEnv: feature's f2 changes a.txt, and so does main.
func clashEnv(t *testing.T) *env {
	e := newEnv(t)
	e.git("reset", "-q", "--hard", "feature~2") // base, f1
	e.commit("a.txt", "feature\n", "f2 changes a")
	e.commit("f3.txt", "3\n", "f3")
	e.git("switch", "-q", "main")
	e.commit("a.txt", "main\n", "m2 changes a")
	e.git("switch", "-q", "feature")
	return e
}

func TestStopContinueAndUndo(t *testing.T) {
	e := clashEnv(t)
	orig := e.git("rev-parse", "HEAD")
	r, err := e.app.Rebase.Run(e.gd, "main")
	if err != nil || r.Outcome != rebase.Stopped || strings.Join(r.Conflicts, ",") != "a.txt" {
		t.Fatalf("Run = %+v, %v", r, err)
	}
	// git's own state is left: the merge feature sees it.
	op, err := e.app.Merge.Operation(e.gd)
	if err != nil || op.Kind != merge.OpRebase || len(op.Conflicts) != 1 {
		t.Fatalf("Operation = %+v, %v", op, err)
	}
	if st, _ := e.app.Rebase.Status(e.gd); !st.Pending || st.Recorded {
		t.Fatalf("Status = %+v", st)
	}
	if e.git("rev-parse", "refs/gitswitch/rebase") != orig {
		t.Fatal("orig commit not kept reachable")
	}
	// Another rebase is refused meanwhile.
	if _, err := e.app.Rebase.Run(e.gd, "main"); err == nil || !strings.Contains(err.Error(), "in the middle of a rebase") {
		t.Fatalf("second run = %v", err)
	}
	// Continue before fixing is refused.
	if _, err := e.app.Merge.Act(e.gd, merge.Continue); err == nil {
		t.Fatal("continued with a clash")
	}
	e.write("a.txt", "fixed\n")
	if _, err := e.app.Merge.Resolve(e.gd, "a.txt", "done", ""); err != nil {
		t.Fatal(err)
	}
	after, err := e.app.Merge.Act(e.gd, merge.Continue)
	if err != nil || after.Kind != "" {
		t.Fatalf("continue = %+v, %v", after, err)
	}
	// The merge feature's hook recorded the rebase.
	last, err := e.app.Undo.Last(e.gd)
	if err != nil || last.Action != undo.Rebase || last.Before != orig || last.Blocked != "" {
		t.Fatalf("Last = %+v, %v", last, err)
	}
	if st, _ := e.app.Rebase.Status(e.gd); st.Pending {
		t.Fatalf("still pending: %+v", st)
	}
	if exec.Command("git", "-C", e.dir, "rev-parse", "-q", "--verify", "refs/gitswitch/rebase").Run() == nil {
		t.Fatal("pending ref left")
	}
	if got := e.log(); got != "base\nm1\nm2 changes a\nf1\nf2 changes a\nf3" {
		t.Fatalf("log = %q", got)
	}
	if _, err := e.app.Undo.Undo(e.gd); err != nil {
		t.Fatal(err)
	}
	if e.git("rev-parse", "HEAD") != orig || e.git("status", "--porcelain") != "" {
		t.Fatal("undo did not restore")
	}
	if b, _ := os.ReadFile(filepath.Join(e.dir, "a.txt")); string(b) != "feature\n" {
		t.Fatalf("a.txt = %q", b)
	}
}

// Continued outside GitSwitch (plain git), the next look at the repo still
// records the rebase.
func TestFinishLazily(t *testing.T) {
	e := clashEnv(t)
	orig := e.git("rev-parse", "HEAD")
	if r, err := e.app.Rebase.Run(e.gd, "main"); err != nil || r.Outcome != rebase.Stopped {
		t.Fatalf("%+v %v", r, err)
	}
	e.write("a.txt", "fixed\n")
	e.git("add", "a.txt")
	cmd := exec.Command("git", "-C", e.dir, "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=:")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	st, err := e.app.Rebase.Status(e.gd)
	if err != nil || !st.Recorded || st.Pending {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if last, err := e.app.Undo.Last(e.gd); err != nil || last.Before != orig || last.Action != undo.Rebase {
		t.Fatalf("Last = %+v, %v", last, err)
	}
	if rec, _ := e.app.Rebase.Finish(e.gd); rec {
		t.Fatal("recorded twice")
	}
}

func TestAbortLeavesRepoAsBefore(t *testing.T) {
	e := clashEnv(t)
	orig := e.git("rev-parse", "HEAD")
	if r, err := e.app.Rebase.Run(e.gd, "main"); err != nil || r.Outcome != rebase.Stopped {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := e.app.Merge.Act(e.gd, merge.Abort); err != nil {
		t.Fatal(err)
	}
	if e.git("rev-parse", "HEAD") != orig || e.git("symbolic-ref", "--short", "HEAD") != "feature" || e.git("status", "--porcelain") != "" {
		t.Fatal("abort did not restore")
	}
	if st, _ := e.app.Rebase.Status(e.gd); st.Pending || st.Recorded {
		t.Fatalf("pending after abort: %+v", st)
	}
	if _, err := e.app.Undo.Last(e.gd); err == nil {
		t.Fatal("an aborted rebase was recorded for undo")
	}
	if exec.Command("git", "-C", e.dir, "rev-parse", "-q", "--verify", "refs/gitswitch/rebase").Run() == nil {
		t.Fatal("pending ref left")
	}
	// The same rebase can be tried again.
	if r, err := e.app.Rebase.Run(e.gd, "main"); err != nil || r.Outcome != rebase.Stopped {
		t.Fatalf("again: %+v %v", r, err)
	}
}

func TestSkipFinishesAndRecords(t *testing.T) {
	e := clashEnv(t)
	orig := e.git("rev-parse", "HEAD")
	if r, err := e.app.Rebase.Run(e.gd, "main"); err != nil || r.Outcome != rebase.Stopped {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := e.app.Merge.Act(e.gd, merge.Skip); err != nil {
		t.Fatal(err)
	}
	last, err := e.app.Undo.Last(e.gd)
	if err != nil || last.Action != undo.Rebase || last.Before != orig {
		t.Fatalf("Last = %+v, %v", last, err)
	}
	if _, err := e.app.Undo.Undo(e.gd); err != nil || e.git("rev-parse", "HEAD") != orig {
		t.Fatalf("undo: %v", err)
	}
}

// Undo is refused once the rebased commits are pushed.
func TestUndoRefusedWhenPushed(t *testing.T) {
	e := newEnv(t)
	if _, err := e.app.Rebase.Run(e.gd, "main"); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "origin.git")
	_ = exec.Command("git", "init", "-q", "--bare", bare).Run()
	e.git("remote", "add", "origin", bare)
	e.git("push", "-q", "origin", "feature:feature")
	e.git("fetch", "-q", "origin")
	last, err := e.app.Undo.Last(e.gd)
	if err != nil || !strings.Contains(last.Blocked, "pushed") {
		t.Fatalf("Last = %+v, %v", last, err)
	}
}

// A rebase onto a tag and a plain commit hash.
func TestOntoTagAndHash(t *testing.T) {
	e := newEnv(t)
	e.git("tag", "v1", "main")
	if r, err := e.app.Rebase.Run(e.gd, "v1"); err != nil || r.Outcome != rebase.Done {
		t.Fatalf("%+v %v", r, err)
	}
	e.git("reset", "-q", "--hard", "ORIG_HEAD")
	if r, err := e.app.Rebase.Run(e.gd, e.git("rev-parse", "--short", "main")); err != nil || r.Outcome != rebase.Done {
		t.Fatalf("%+v %v", r, err)
	}
}

// rebase.updateRefs and autostash settings do not leak into the run.
func TestIgnoresUserRebaseConfig(t *testing.T) {
	e := newEnv(t)
	e.git("config", "rebase.updateRefs", "true")
	e.git("config", "rebase.autoStash", "true")
	e.git("branch", "mid", "feature~1")
	mid := e.git("rev-parse", "mid")
	if r, err := e.app.Rebase.Run(e.gd, "main"); err != nil || r.Outcome != rebase.Done {
		t.Fatalf("%+v %v", r, err)
	}
	if e.git("rev-parse", "mid") != mid {
		t.Fatal("another branch was moved")
	}
}
