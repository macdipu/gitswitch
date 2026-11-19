package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// twelve is a file of twelve short lines, so two edits far apart are two
// separate clashes (or hunks).
const twelve = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\n"

// mergeRepo is a repo on main with a.txt (twelve lines) committed, shown on
// History. git runs in it; write puts a file in it.
func mergeRepo(t *testing.T) (*tui, string, func(...string) string, func(string, string)) {
	t.Helper()
	x := testTUI(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example", "GIT_EDITOR": ":"} {
		t.Setenv(k, v)
	}
	dir := filepath.Join(t.TempDir(), "r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("a.txt", twelve)
	git("add", ".")
	git("commit", "-q", "-m", "base")
	gd, err := x.app.Repos.For(dir)
	if err != nil {
		t.Fatal(err)
	}
	x.histDir = gd
	x.switchTab(tabHistory)
	x.readHistory()
	return x, dir, git, write
}

// clashRepo: main and feature both change l2 and l11 of a.txt, so merging
// feature into main clashes twice in one file.
func clashRepo(t *testing.T) (*tui, string, func(...string) string, func(string, string)) {
	x, dir, git, write := mergeRepo(t)
	git("checkout", "-q", "-b", "feature")
	write("a.txt", strings.NewReplacer("l2\n", "feat2\n", "l11\n", "feat11\n").Replace(twelve))
	git("commit", "-q", "-am", "feature edits")
	git("checkout", "-q", "main")
	write("a.txt", strings.NewReplacer("l2\n", "main2\n", "l11\n", "main11\n").Replace(twelve))
	git("commit", "-q", "-am", "main edits")
	x.readHistory()
	return x, dir, git, write
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func plain(lines []string) string { return history.StripANSI(strings.Join(lines, "\n")) }

// startMerge presses e and picks the branch; the question comes up.
func startMerge(t *testing.T, x *tui, branch string) {
	t.Helper()
	x.handle("e")
	if x.picker == nil {
		t.Fatalf("e did not open the branch picker (msg %q)", x.msg)
	}
	typeKeys(x, branch)
	x.handle("enter")
}

// A fast-forward: the plan says so, f runs it, and U's undo hint is shown.
func TestMergeFastForward(t *testing.T) {
	x, dir, git, write := mergeRepo(t)
	git("checkout", "-q", "-b", "feature")
	write("f.txt", "new\n")
	git("add", ".")
	git("commit", "-q", "-m", "add f")
	git("checkout", "-q", "main")
	x.readHistory()
	startMerge(t, x, "feature")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "fast-forward") || !strings.Contains(x.confirm.text, "add f") {
		t.Fatalf("no plan in the question: %+v (msg %q)", x.confirm, x.msg)
	}
	if _, ok := x.confirm.actions["f"]; !ok {
		t.Fatal("f (fast-forward) is not offered")
	}
	x.handle("f")
	if _, err := os.Stat(filepath.Join(dir, "f.txt")); err != nil || x.msgErr || !strings.Contains(x.msg, "fast-forwarded") {
		t.Fatalf("not merged (msg %q): %v", x.msg, err)
	}
	if git("rev-parse", "main") != git("rev-parse", "feature") {
		t.Fatal("main did not move to feature")
	}
}

// Squash stages the changes without a commit; a merge commit is made on
// request even when it could fast-forward.
func TestMergeCommitAndSquash(t *testing.T) {
	x, _, git, write := mergeRepo(t)
	git("checkout", "-q", "-b", "feature")
	write("f.txt", "new\n")
	git("add", ".")
	git("commit", "-q", "-m", "add f")
	git("checkout", "-q", "main")
	x.readHistory()
	startMerge(t, x, "feature")
	x.handle("s")
	if st := git("status", "--porcelain"); !strings.Contains(st, "A  f.txt") || x.msgErr {
		t.Fatalf("squash did not stage f.txt (msg %q): %s", x.msg, st)
	}
	git("reset", "-q", "--hard")
	x.readHistory()
	startMerge(t, x, "feature")
	x.handle("m")
	if got := git("log", "-1", "--format=%p"); len(strings.Fields(got)) != 2 || !strings.Contains(x.msg, "merge commit") {
		t.Fatalf("no merge commit (parents %q, msg %q)", got, x.msg)
	}
}

// A clash stops the merge and opens the operation; History says so.
func TestMergeClashOpensOperation(t *testing.T) {
	x, dir, _, _ := clashRepo(t)
	startMerge(t, x, "feature")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "Likely clashes") || !strings.Contains(x.confirm.text, "a.txt") {
		t.Fatalf("the plan does not name the clash: %+v", x.confirm)
	}
	if _, ok := x.confirm.actions["f"]; ok {
		t.Fatal("f is offered although the branches have gone apart")
	}
	x.handle("m")
	if x.opv == nil || x.msgErr {
		t.Fatalf("the operation did not open (msg %q)", x.msg)
	}
	lines, _ := x.drawOperation()
	got := plain(lines)
	for _, want := range []string{"Operation in progress: merge", "Files in conflict (1)", "! a.txt", "c continue", "a abort"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "s skip") {
		t.Errorf("a merge cannot be skipped:\n%s", got)
	}
	if !strings.Contains(read(t, dir, "a.txt"), "<<<<<<<") {
		t.Fatal("no markers in the file")
	}
	x.handle("esc")
	x.readHistory()
	lines, _ = x.drawHistory()
	if got := plain(lines); !strings.Contains(got, "A merge is in progress") {
		t.Fatalf("no banner on History:\n%s", got)
	}
	x.handle("e") // opens the operation, not the picker
	if x.opv == nil || x.picker != nil {
		t.Fatal("e did not open the operation")
	}
}

// stopped leaves the repo mid-merge with the operation open on a.txt.
func stopped(t *testing.T) (*tui, string, func(...string) string) {
	x, dir, git, _ := clashRepo(t)
	startMerge(t, x, "feature")
	x.handle("m")
	if x.opv == nil {
		t.Fatalf("not stopped (msg %q)", x.msg)
	}
	return x, dir, git
}

func TestConflictTakeOursThenContinue(t *testing.T) {
	x, dir, git := stopped(t)
	x.handle("c") // refused while a clash remains
	if !x.msgErr || !strings.Contains(x.msg, "still have conflicts") || x.opv == nil {
		t.Fatalf("continue with a clash left: msg %q err %v", x.msg, x.msgErr)
	}
	x.handle("enter")
	if x.conf == nil {
		t.Fatalf("the conflict did not open (msg %q)", x.msg)
	}
	lines, _ := x.drawConflict()
	got := plain(lines)
	for _, want := range []string{"Conflict  a.txt", "2 clash(es), 0 chosen", "<<<<<<<", "main2", "feat2", ">>>>>>>"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	x.handle("v") // ours
	if got := plain(func() []string { l, _ := x.drawConflict(); return l }()); !strings.Contains(got, "showing: ours") || !strings.Contains(got, "main11") || strings.Contains(got, "feat2") {
		t.Fatalf("ours pane:\n%s", got)
	}
	x.handle("v") // theirs
	if got := plain(func() []string { l, _ := x.drawConflict(); return l }()); !strings.Contains(got, "feat11") || strings.Contains(got, "main2") {
		t.Fatalf("theirs pane:\n%s", got)
	}
	x.handle("O")
	if x.confirm == nil {
		t.Fatal("keeping a whole side did not ask")
	}
	x.handle("y")
	if x.conf != nil || x.opv == nil || x.msgErr || !strings.Contains(x.msg, "Every conflict is fixed") {
		t.Fatalf("after ours: conf open %v, msg %q", x.conf != nil, x.msg)
	}
	if got := read(t, dir, "a.txt"); got != strings.NewReplacer("l2\n", "main2\n", "l11\n", "main11\n").Replace(twelve) {
		t.Fatalf("a.txt = %q", got)
	}
	x.handle("c")
	if x.opv != nil || x.msgErr || !strings.Contains(x.msg, "finished") {
		t.Fatalf("continue: msg %q", x.msg)
	}
	if len(strings.Fields(git("log", "-1", "--format=%p"))) != 2 {
		t.Fatal("no merge commit")
	}
}

func TestConflictTakeTheirs(t *testing.T) {
	x, dir, _ := stopped(t)
	x.handle("enter")
	x.handle("T")
	x.handle("y")
	if got := read(t, dir, "a.txt"); !strings.Contains(got, "feat2") || !strings.Contains(got, "feat11") || strings.Contains(got, "main") {
		t.Fatalf("a.txt = %q (msg %q)", got, x.msg)
	}
}

// Clash by clash: ours for the first, both for the second, then save.
func TestConflictPerRegion(t *testing.T) {
	x, dir, git := stopped(t)
	x.handle("enter")
	x.handle("o") // not on a clash yet
	if !x.msgErr || !strings.Contains(x.msg, "cursor on a clash") {
		t.Fatalf("o off a clash: msg %q", x.msg)
	}
	x.handle("w") // nothing chosen
	if !x.msgErr || !strings.Contains(x.msg, "2 clash(es) still need a choice") {
		t.Fatalf("w with no choice: msg %q", x.msg)
	}
	x.handle("d") // markers remain: refused with the reason
	if !x.msgErr || !strings.Contains(strings.ToLower(x.msg), "marker") || x.conf == nil {
		t.Fatalf("d with markers: msg %q", x.msg)
	}
	x.handle("n")
	x.handle("o")
	x.handle("b") // the cursor moved to the second clash
	if x.conf.choice[0] != pickOurs || x.conf.choice[1] != pickBoth {
		t.Fatalf("choices = %v (msg %q)", x.conf.choice, x.msg)
	}
	x.handle("w")
	want := strings.NewReplacer("l2\n", "main2\n", "l11\n", "main11\nfeat11\n").Replace(twelve)
	if got := read(t, dir, "a.txt"); got != want {
		t.Fatalf("a.txt = %q, want %q (msg %q)", got, want, x.msg)
	}
	if x.conf != nil || git("diff", "--name-only", "--diff-filter=U") != "" {
		t.Fatalf("still in conflict (msg %q)", x.msg)
	}
	x.handle("c")
	if x.opv != nil || git("status", "--porcelain") != "" {
		t.Fatalf("not finished (msg %q): %s", x.msg, git("status", "--porcelain"))
	}
}

// Open in the editor, then read again; the file is marked resolved after.
func TestConflictEditor(t *testing.T) {
	x, dir, _ := stopped(t)
	x.handle("enter")
	x.edit = func(path string) error { return os.WriteFile(path, []byte("edited by hand\n"), 0o644) }
	x.handle("e")
	if x.conf == nil || x.conf.c.Markers || !strings.Contains(x.msg, "no clash markers left") {
		t.Fatalf("after the editor: msg %q", x.msg)
	}
	x.handle("d")
	if x.conf != nil || x.msgErr || read(t, dir, "a.txt") != "edited by hand\n" {
		t.Fatalf("not resolved (msg %q)", x.msg)
	}
	x.edit = func(string) error { return errors.New("no editor") }
	x.openConflict("a.txt") // no longer in conflict
	if x.conf != nil && !x.msgErr {
		t.Fatalf("opened a file that is not in conflict: %q", x.msg)
	}
}

// Abort asks first; n keeps the merge, y goes back to before it.
func TestOperationAbort(t *testing.T) {
	x, dir, git := stopped(t)
	x.handle("s")
	if !x.msgErr || !strings.Contains(x.msg, "can't be skipped") {
		t.Fatalf("skip of a merge: msg %q", x.msg)
	}
	x.handle("a")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "Abort the merge") {
		t.Fatalf("abort did not ask: %+v", x.confirm)
	}
	x.handle("n")
	if x.opv == nil || !strings.Contains(read(t, dir, "a.txt"), "<<<<<<<") {
		t.Fatal("a cancelled abort changed the merge")
	}
	x.handle("a")
	x.handle("y")
	if x.opv != nil || x.msgErr || !strings.Contains(x.msg, "Aborted the merge") {
		t.Fatalf("after abort: msg %q", x.msg)
	}
	if got := read(t, dir, "a.txt"); strings.Contains(got, "<<<<<<<") || !strings.Contains(got, "main2") || git("status", "--porcelain") != "" {
		t.Fatalf("not back as before: %q", got)
	}
	lines, _ := x.drawHistory()
	if strings.Contains(plain(lines), "in progress") {
		t.Fatal("the banner stayed after the abort")
	}
}

// A rebase can be skipped; here a cherry-pick that clashes is.
func TestOperationSkipCherryPick(t *testing.T) {
	x, dir, git, _ := clashRepo(t)
	_ = exec.Command("git", "-C", dir, "cherry-pick", "feature").Run() // stops: a.txt clashes
	x.readHistory()
	x.handle("e")
	if x.opv == nil || x.opv.op.Kind != "cherry-pick" {
		t.Fatalf("no cherry-pick operation (msg %q)", x.msg)
	}
	lines, _ := x.drawOperation()
	if !strings.Contains(plain(lines), "s skip this commit") {
		t.Fatalf("skip not offered:\n%s", plain(lines))
	}
	x.handle("s")
	if x.opv != nil || x.msgErr || git("status", "--porcelain") != "" {
		t.Fatalf("not skipped (msg %q)", x.msg)
	}
}

// Merging is refused with uncommitted changes to tracked files, and a
// branch that is already merged says so.
func TestMergeRefusals(t *testing.T) {
	x, dir, git, write := mergeRepo(t)
	git("branch", "same")
	x.handle("e")
	typeKeys(x, "same")
	x.handle("enter")
	if x.confirm != nil || x.msgErr || !strings.Contains(x.msg, "up to date") {
		t.Fatalf("up-to-date: msg %q", x.msg)
	}
	git("checkout", "-q", "-b", "feature")
	write("f.txt", "new\n")
	git("add", ".")
	git("commit", "-q", "-m", "add f")
	git("checkout", "-q", "main")
	write("a.txt", "dirty\n")
	x.readHistory()
	startMerge(t, x, "feature")
	if x.view == nil || x.confirm != nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "Can't merge now") {
		t.Fatalf("a dirty tree was not refused: view %+v confirm %+v (msg %q)", x.view, x.confirm, x.msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "f.txt")); err == nil {
		t.Fatal("merged anyway")
	}
}

func TestParseRegions(t *testing.T) {
	text := "a\n<<<<<<< HEAD\nx\n||||||| base\nb\n=======\ny\nz\n>>>>>>> feature\nc"
	lines := splitLines(text)
	rs, err := parseRegions(lines)
	if err != nil || len(rs) != 1 {
		t.Fatalf("regions %v, err %v", rs, err)
	}
	r := rs[0]
	if r.start != 1 || r.end != 8 || !r.hasBase || len(r.ours) != 1 || len(r.theirs) != 2 || r.oursLabel != "HEAD" || r.theirsLabel != "feature" {
		t.Fatalf("region %+v", r)
	}
	got, err := buildResult(lines, rs, []int{pickBoth})
	if err != nil || got != "a\nx\ny\nz\nc" {
		t.Fatalf("both = %q, %v", got, err)
	}
	if got, _ := buildResult(lines, rs, []int{pickTheirs}); got != "a\ny\nz\nc" {
		t.Fatalf("theirs = %q", got)
	}
	if _, err := buildResult(lines, rs, []int{pickNone}); err == nil {
		t.Fatal("a clash with no choice was built")
	}
	if _, err := parseRegions(splitLines("<<<<<<< a\nx\n")); err == nil {
		t.Fatal("an unclosed clash was accepted")
	}
}
