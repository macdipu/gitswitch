package infrastructure_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
)

type fx struct {
	t   *testing.T
	dir string
	gd  string
	app *bootstrap.App
}

func (f *fx) git(args ...string) string {
	f.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.dir}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fx) write(name, text string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commit writes a file and commits it with message m (author A <a@x.example>).
func (f *fx) commit(file, text, m string) string {
	f.t.Helper()
	f.write(file, text)
	f.git("add", file)
	f.git("-c", "user.name=A", "-c", "user.email=a@x.example", "commit", "-q", "--author=Orig <orig@x.example>", "-m", m)
	return f.git("rev-parse", "HEAD")
}

// subjects lists the subjects of the commits above base, oldest first.
func (f *fx) subjects() []string {
	out := f.git("log", "--reverse", "--format=%s", "main", "--not", "base")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (f *fx) body(rev string) string { return f.git("log", "-1", "--format=%B", rev) + "\n" }

func newFx(t *testing.T) *fx {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1",
		"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
		t.Setenv(k, v)
	}
	f := &fx{t: t, dir: filepath.Join(home, "r"), app: bootstrap.New(false, nil)}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git("init", "-q", "-b", "main")
	f.write("base.txt", "base\n")
	f.git("add", "base.txt")
	f.git("commit", "-q", "-m", "base")
	f.git("tag", "base")
	f.gd = f.git("rev-parse", "--absolute-git-dir")
	return f
}

// four makes commits one..four, each adding its own file.
func (f *fx) four() []string {
	var hs []string
	for _, n := range []string{"one", "two", "three", "four"} {
		hs = append(hs, f.commit(n+".txt", n+"\n", n+" subject\n\nbody of "+n))
	}
	return hs
}

func pick(h string) rewrite.Step { return rewrite.Step{Hash: h, Action: rewrite.Pick} }
func step(h string, a rewrite.Action, m string) rewrite.Step {
	return rewrite.Step{Hash: h, Action: a, Message: m}
}

func (f *fx) run(steps ...rewrite.Step) rewrite.Rewritten {
	f.t.Helper()
	r, err := f.app.Rewrite.Run(f.gd, "", steps)
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return r
}

func (f *fx) clean() {
	f.t.Helper()
	if s := f.git("status", "--porcelain"); s != "" {
		f.t.Fatalf("working folder is not clean:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(f.gd, "gitswitch-rewrite")); err == nil {
		// allowed only while a rewrite is pending
		if _, ok := f.app.Rewrite.Git.Pending(f.gd); !ok {
			f.t.Fatal("temp folder left behind")
		}
	}
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

func TestTodoDefaultsToNotPushed(t *testing.T) {
	f := newFx(t)
	f.four()
	td, err := f.app.Rewrite.Todo(f.gd, "")
	if err != nil || td.Blocked != "" || len(td.Commits) != 4 || td.Branch != "main" {
		t.Fatalf("Todo = %+v %v", td, err)
	}
	// Base is the root commit here: the first commit is left out, the rest listed.
	if td.Base != f.git("rev-parse", "base") || td.Commits[0].Subject != "one subject" || td.Commits[0].Files != 1 ||
		td.Commits[0].Author != "Orig" || td.Commits[3].Message != "four subject\n\nbody of four" {
		t.Fatalf("Todo = %+v", td)
	}
	// Pushed commits are not listed, and --since refuses them.
	bare := filepath.Join(t.TempDir(), "o.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	f.git("remote", "add", "origin", bare)
	f.git("push", "-q", "origin", "main")
	f.commit("five.txt", "5\n", "five subject")
	td, _ = f.app.Rewrite.Todo(f.gd, "")
	if len(td.Commits) != 1 || td.Commits[0].Subject != "five subject" {
		t.Fatalf("after push Todo = %+v", td)
	}
	if _, err := f.app.Rewrite.Todo(f.gd, "HEAD~2"); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Fatalf("pushed since = %v", err)
	}
	if _, err := f.app.Rewrite.Reword(f.gd, "HEAD~1", "x"); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Fatalf("reword pushed = %v", err)
	}
	if td, err = f.app.Rewrite.Todo(f.gd, "HEAD"); err != nil || len(td.Commits) != 1 {
		t.Fatalf("since HEAD = %+v %v", td, err)
	}
}

func TestReorderThenUndo(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	orig := hs[3]
	tree := f.git("rev-parse", "HEAD^{tree}")
	r := f.run(pick(hs[2]), pick(hs[0]), pick(hs[3]), pick(hs[1]))
	if r.Outcome != rewrite.Done || r.Before != 4 || r.After != 4 || len(r.Summary) != 4 {
		t.Fatalf("Run = %+v", r)
	}
	eq(t, "order", f.subjects(), []string{"three subject", "one subject", "four subject", "two subject"})
	if f.git("rev-parse", "HEAD^{tree}") != tree {
		t.Fatal("tree changed by a reorder of independent commits")
	}
	if a := f.git("log", "-1", "--format=%an <%ae>", "HEAD"); a != "Orig <orig@x.example>" {
		t.Fatalf("author = %s", a)
	}
	f.clean()
	last, err := f.app.Undo.Last(f.gd)
	if err != nil || last.Action != "rewrite" || last.Blocked != "" || last.Before != orig {
		t.Fatalf("Last = %+v %v", last, err)
	}
	if f.git("rev-parse", "refs/gitswitch/undo") != orig {
		t.Fatal("the original tip is not kept by refs/gitswitch/undo")
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil {
		t.Fatal(err)
	}
	if f.git("rev-parse", "HEAD") != orig {
		t.Fatal("undo did not restore the tip")
	}
	f.clean()
}

func TestSquashFixupDropReword(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	// squash two into one with the default (joined) message; drop four.
	r := f.run(pick(hs[0]), step(hs[1], rewrite.Squash, ""), pick(hs[2]), step(hs[3], rewrite.Drop, ""))
	if r.After != 2 {
		t.Fatalf("Run = %+v", r)
	}
	eq(t, "subjects", f.subjects(), []string{"one subject", "three subject"})
	if got := f.body("HEAD~1"); got != "one subject\n\nbody of one\n\ntwo subject\n\nbody of two\n" {
		t.Fatalf("joined message = %q", got)
	}
	if f.git("show", "HEAD~1:two.txt") != "two" || f.git("ls-tree", "--name-only", "HEAD") == "" || strings.Contains(f.git("ls-tree", "--name-only", "HEAD"), "four.txt") {
		t.Fatal("squash or drop lost or kept the wrong content")
	}
	f.clean()
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != hs[3] {
		t.Fatalf("undo: %v", err)
	}

	// fixup keeps the first message; an explicit squash message replaces it.
	r = f.run(pick(hs[0]), step(hs[1], rewrite.Fixup, ""), step(hs[2], rewrite.Squash, "combined\n\nthree and one"), pick(hs[3]))
	eq(t, "subjects", f.subjects(), []string{"combined", "four subject"})
	if got := f.body("HEAD~1"); got != "combined\n\nthree and one\n" {
		t.Fatalf("explicit squash message = %q", got)
	}
	if f.git("show", "HEAD~1:two.txt") != "two" {
		t.Fatal("fixup lost content")
	}
	f.app.Undo.Undo(f.gd)

	// reword the middle commit; the rest are kept (and keep their messages).
	f.run(pick(hs[0]), step(hs[1], rewrite.Reword, "  new second\n\n# kept: not a comment line\n"), pick(hs[2]), pick(hs[3]))
	eq(t, "subjects", f.subjects(), []string{"one subject", "new second", "three subject", "four subject"})
	if got := f.body("HEAD~2"); got != "new second\n\n# kept: not a comment line\n" {
		t.Fatalf("reworded message = %q", got)
	}
	if f.body("HEAD") != "four subject\n\nbody of four\n" {
		t.Fatal("a picked commit lost its message")
	}
	// the first commit was not rewritten at all (fast-forwarded): same hash.
	if f.git("rev-parse", "HEAD~3") != hs[0] {
		t.Fatal("an untouched leading commit got a new hash")
	}
	f.app.Undo.Undo(f.gd)

	// reword + squash combined: the reworded head, a squashed commit with no message.
	f.run(step(hs[0], rewrite.Reword, "head reworded"), step(hs[1], rewrite.Squash, ""), pick(hs[2]), pick(hs[3]))
	if got := f.body("HEAD~2"); got != "head reworded\n\ntwo subject\n\nbody of two\n" {
		t.Fatalf("reword+squash message = %q", got)
	}
	f.app.Undo.Undo(f.gd)

	// squash with a reworded head and an explicit combined message.
	f.run(step(hs[0], rewrite.Reword, "ignored head"), step(hs[1], rewrite.Squash, "final words"), pick(hs[2]), pick(hs[3]))
	if got := f.body("HEAD~2"); got != "final words\n" {
		t.Fatalf("final message = %q", got)
	}
	f.app.Undo.Undo(f.gd)

	// drop first, then a fixup after the next: dropped commit's file is gone.
	f.run(step(hs[0], rewrite.Drop, ""), pick(hs[1]), step(hs[2], rewrite.Fixup, ""), pick(hs[3]))
	eq(t, "subjects", f.subjects(), []string{"two subject", "four subject"})
	if _, err := os.Stat(filepath.Join(f.dir, "one.txt")); err == nil {
		t.Fatal("one.txt should be gone")
	}
	f.clean()
}

func TestRewordOlderCommit(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	r, err := f.app.Rewrite.Reword(f.gd, hs[1][:8], "Second, reworded")
	if err != nil || r.Outcome != rewrite.Done || r.After != 3 {
		t.Fatalf("Reword = %+v %v", r, err)
	}
	eq(t, "subjects", f.subjects(), []string{"one subject", "Second, reworded", "three subject", "four subject"})
	last, _ := f.app.Undo.Last(f.gd)
	if !strings.HasPrefix(last.Label, "Reworded") {
		t.Fatalf("label = %q", last.Label)
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != hs[3] {
		t.Fatalf("undo %v", err)
	}
	// the last commit, and a message that changes nothing.
	if _, err := f.app.Rewrite.Reword(f.gd, "HEAD", "four subject\n\nbody of four"); err == nil || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("same message = %v", err)
	}
	if _, err := f.app.Rewrite.Reword(f.gd, "HEAD", "last one"); err != nil || f.git("log", "-1", "--format=%s") != "last one" {
		t.Fatalf("reword HEAD: %v", err)
	}
	if _, err := f.app.Rewrite.Reword(f.gd, "HEAD", "  "); err == nil {
		t.Fatal("empty message accepted")
	}
}

func TestRefusals(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	before := f.git("rev-parse", "HEAD")
	bad := func(what, want string, steps ...rewrite.Step) {
		t.Helper()
		_, err := f.app.Rewrite.Run(f.gd, "", steps)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want %q", what, err, want)
		}
		if f.git("rev-parse", "HEAD") != before {
			t.Fatalf("%s: the branch moved", what)
		}
	}
	bad("missing", "is missing", pick(hs[0]), pick(hs[1]), pick(hs[2]))
	bad("duplicate", "twice", pick(hs[0]), pick(hs[0]), pick(hs[1]), pick(hs[2]), pick(hs[3]))
	bad("extra", "not one of the commits", pick(hs[0]), pick(hs[1]), pick(hs[2]), pick(hs[3]), pick(strings.Repeat("a", 40)))
	bad("squash first", "needs a commit to fold into", step(hs[0], rewrite.Squash, ""), pick(hs[1]), pick(hs[2]), pick(hs[3]))
	bad("reword no message", "needs a new message", step(hs[0], rewrite.Reword, " "), pick(hs[1]), pick(hs[2]), pick(hs[3]))
	bad("no change", "change nothing", pick(hs[0]), pick(hs[1]), pick(hs[2]), pick(hs[3]))
	bad("bad action", "unknown action", step(hs[0], "edit2", ""), pick(hs[1]), pick(hs[2]), pick(hs[3]))

	// uncommitted tracked changes
	f.write("one.txt", "changed\n")
	bad("dirty", "uncommitted changes", pick(hs[1]), pick(hs[0]), pick(hs[2]), pick(hs[3]))
	f.git("checkout", "--", "one.txt")

	// detached HEAD
	f.git("checkout", "-q", "--detach")
	bad("detached", "detached", pick(hs[1]), pick(hs[0]), pick(hs[2]), pick(hs[3]))
	f.git("checkout", "-q", "main")

	// another operation in progress
	f.git("checkout", "-q", "-b", "other", "base")
	f.write("one.txt", "x\n")
	f.git("add", "one.txt")
	f.git("commit", "-q", "-m", "clash")
	f.git("checkout", "-q", "main")
	_ = exec.Command("git", "-C", f.dir, "merge", "other").Run()
	bad("merging", "middle of a merge", pick(hs[1]), pick(hs[0]), pick(hs[2]), pick(hs[3]))
	f.git("merge", "--abort")
}

func TestMergeAndRootCommits(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	f.git("checkout", "-q", "-b", "side", "base")
	f.commit("side.txt", "s\n", "side commit")
	f.git("checkout", "-q", "main")
	f.git("merge", "-q", "--no-ff", "-m", "merge side", "side")
	f.commit("six.txt", "6\n", "six subject")
	f.commit("seven.txt", "7\n", "seven subject")
	// default: only what comes after the merge
	td, err := f.app.Rewrite.Todo(f.gd, "")
	if err != nil || len(td.Commits) != 2 || !strings.Contains(td.Note, "merge commit") {
		t.Fatalf("Todo = %+v %v", td, err)
	}
	if _, err := f.app.Rewrite.Todo(f.gd, hs[0]); err == nil || !strings.Contains(err.Error(), "merge commit") {
		t.Fatalf("since across a merge = %v", err)
	}
	root := f.git("rev-list", "--max-parents=0", "HEAD")
	if _, err := f.app.Rewrite.Todo(f.gd, root); err == nil || !strings.Contains(err.Error(), "first commit") {
		t.Fatalf("root = %v", err)
	}
	if _, err := f.app.Rewrite.Reword(f.gd, root, "x"); err == nil {
		t.Fatal("root reworded")
	}
}

// A clash stops the rebase; continue (the merge feature) finishes it, the
// reworded message still lands, and undo takes it all back. Abort leaves
// things as before.
func TestClashStopsThenContinueThenUndo(t *testing.T) {
	f := newFx(t)
	f.commit("f.txt", "1\n", "f one")
	a := f.commit("f.txt", "1\n2\n", "f two")
	b := f.commit("f.txt", "1\n2\n3\n", "f three")
	orig := b

	// Swapping the two edits of the same file clashes.
	r, err := f.app.Rewrite.Run(f.gd, "", []rewrite.Step{pick(f.git("rev-parse", "HEAD~2")), pick(b), step(a, rewrite.Reword, "f two, reworded")})
	if err != nil || r.Outcome != rewrite.Stopped || len(r.Conflicts) == 0 || r.Conflicts[0] != "f.txt" {
		t.Fatalf("Run = %+v %v", r, err)
	}
	if op, _ := f.app.Merge.Operation(f.gd); op.Kind != "rebase" || len(op.Conflicts) == 0 {
		t.Fatalf("Operation = %+v", op)
	}
	if td, _ := f.app.Rewrite.Todo(f.gd, ""); !td.Pending || td.Blocked == "" {
		t.Fatalf("Todo while stopped = %+v", td)
	}
	if _, ok := f.app.Rewrite.Git.Pending(f.gd); !ok || f.git("rev-parse", "refs/gitswitch/rewrite-orig") != orig {
		t.Fatal("the stop was not remembered")
	}
	if _, err := f.app.Undo.Last(f.gd); err == nil {
		t.Fatal("undo offered while stopped")
	}
	// Abort first: back as before, nothing recorded, nothing left.
	if _, err := f.app.Merge.Act(f.gd, "abort"); err != nil {
		t.Fatal(err)
	}
	if f.git("rev-parse", "HEAD") != orig {
		t.Fatal("abort did not restore")
	}
	if _, ok := f.app.Rewrite.Git.Pending(f.gd); ok {
		t.Fatal("pending left after abort")
	}
	if _, err := f.app.Undo.Last(f.gd); err == nil {
		t.Fatal("undo recorded for an aborted rewrite")
	}
	f.clean()

	// Again, and this time finish it.
	r, _ = f.app.Rewrite.Run(f.gd, "", []rewrite.Step{pick(f.git("rev-parse", "HEAD~2")), pick(b), step(a, rewrite.Reword, "f two, reworded")})
	if r.Outcome != rewrite.Stopped {
		t.Fatalf("Run = %+v", r)
	}
	if _, err := f.app.Merge.Resolve(f.gd, "f.txt", "save", "1\n3\n"); err != nil {
		t.Fatal(err)
	}
	op, err := f.app.Merge.Act(f.gd, "continue")
	for op.Kind != "" && err == nil && len(op.Conflicts) > 0 { // stops again on the next commit
		if _, err = f.app.Merge.Resolve(f.gd, "f.txt", "save", "1\n3\n2\n"); err == nil {
			op, err = f.app.Merge.Act(f.gd, "continue")
		}
	}
	if err != nil || op.Kind != "" {
		t.Fatalf("continue = %+v %v", op, err)
	}
	eq(t, "subjects", f.subjects()[len(f.subjects())-3:], []string{"f one", "f three", "f two, reworded"})
	if f.git("show", "HEAD:f.txt") != "1\n3\n2" {
		t.Fatalf("resolved file = %q", f.git("show", "HEAD:f.txt"))
	}
	f.clean()
	last, err := f.app.Undo.Last(f.gd)
	if err != nil || last.Action != "rewrite" || last.Blocked != "" || last.Before != orig {
		t.Fatalf("Last after continue = %+v %v", last, err)
	}
	if _, ok := f.app.Rewrite.Git.Pending(f.gd); ok {
		t.Fatal("pending left after continue")
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != orig {
		t.Fatalf("undo: %v", err)
	}
}

// The lazy path: finished with plain git, recorded by Finish.
func TestFinishAfterPlainGitContinue(t *testing.T) {
	f := newFx(t)
	f.commit("f.txt", "1\n", "f one")
	a := f.commit("f.txt", "1\n2\n", "f two")
	b := f.commit("f.txt", "1\n2\n3\n", "f three")
	r, _ := f.app.Rewrite.Run(f.gd, "", []rewrite.Step{pick(f.git("rev-parse", "HEAD~2")), pick(b), pick(a)})
	if r.Outcome != rewrite.Stopped {
		t.Fatalf("Run = %+v", r)
	}
	if fin, _ := f.app.Rewrite.Finish(f.gd); !fin.Pending || fin.Recorded {
		t.Fatalf("Finish while stopped = %+v", fin)
	}
	for i := 0; i < 3 && f.app.Rewrite.Git.Op(f.gd) == "rebase"; i++ {
		f.write("f.txt", []string{"1\n3\n", "1\n3\n2\n", "1\n3\n2\n"}[i])
		f.git("add", "f.txt")
		cmd := exec.Command("git", "-C", f.dir, "rebase", "--continue")
		cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
		_, _ = cmd.CombinedOutput() // may stop again on the next commit
	}
	fin, err := f.app.Rewrite.Finish(f.gd)
	if err != nil || !fin.Recorded {
		t.Fatalf("Finish = %+v %v", fin, err)
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != b {
		t.Fatalf("undo: %v", err)
	}
}

func TestFixupInto(t *testing.T) {
	f := newFx(t)
	hs := f.four()
	// nothing staged
	if _, err := f.app.Rewrite.FixupInto(f.gd, hs[1]); err == nil || !strings.Contains(err.Error(), "nothing is staged") {
		t.Fatalf("nothing staged = %v", err)
	}
	// staged plus an unstaged tracked change is refused
	f.write("two.txt", "two fixed\n")
	f.git("add", "two.txt")
	f.write("one.txt", "unstaged\n")
	if _, err := f.app.Rewrite.FixupInto(f.gd, hs[1]); err == nil || !strings.Contains(err.Error(), "not staged") {
		t.Fatalf("unstaged = %v", err)
	}
	if f.git("rev-parse", "HEAD") != hs[3] {
		t.Fatal("a refused fold moved the branch")
	}
	f.git("checkout", "--", "one.txt")
	// a repeated subject must not confuse it: two commits named alike
	f.git("commit", "-q", "--allow-empty", "-m", "two subject")
	f.git("reset", "-q", "--soft", "HEAD~1") // keep staged change, drop the empty commit again
	r, err := f.app.Rewrite.FixupInto(f.gd, hs[1])
	if err != nil || r.Outcome != rewrite.Done {
		t.Fatalf("FixupInto = %+v %v", r, err)
	}
	eq(t, "subjects", f.subjects(), []string{"one subject", "two subject", "three subject", "four subject"})
	if f.git("show", "HEAD~2:two.txt") != "two fixed" || f.git("show", "HEAD:two.txt") != "two fixed" {
		t.Fatal("the change is not in the older commit")
	}
	if f.git("show", "HEAD~3:one.txt") != "one" || f.git("rev-parse", "HEAD~3") != hs[0] {
		t.Fatal("an earlier commit changed")
	}
	f.clean()
	last, err := f.app.Undo.Last(f.gd)
	if err != nil || last.Blocked != "" || !strings.HasPrefix(last.Label, "Folded") {
		t.Fatalf("Last = %+v %v", last, err)
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != hs[3] {
		t.Fatalf("undo: %v", err)
	}
	// the change is back, uncommitted
	if f.git("status", "--porcelain") != "M two.txt" {
		t.Fatalf("status after undo = %q", f.git("status", "--porcelain"))
	}
}

// A fold that clashes stops; aborting it leaves the fixup commit, which the
// Finish call records so undo gives the staged changes back.
func TestFixupIntoClashAborted(t *testing.T) {
	f := newFx(t)
	a := f.commit("f.txt", "1\n", "f one")
	b := f.commit("f.txt", "1\n2\n", "f two")
	f.write("f.txt", "1\n2\n3\n")
	f.git("add", "f.txt")
	// fold the "3" change into the first commit: clashes with f two
	r, err := f.app.Rewrite.FixupInto(f.gd, a)
	if err != nil || r.Outcome != rewrite.Stopped {
		t.Fatalf("FixupInto = %+v %v", r, err)
	}
	if _, err := f.app.Merge.Act(f.gd, "abort"); err != nil {
		t.Fatal(err)
	}
	last, err := f.app.Undo.Last(f.gd)
	if err != nil || last.Before != b {
		t.Fatalf("Last = %+v %v", last, err)
	}
	if _, err := f.app.Undo.Undo(f.gd); err != nil || f.git("rev-parse", "HEAD") != b {
		t.Fatalf("undo: %v", err)
	}
}
