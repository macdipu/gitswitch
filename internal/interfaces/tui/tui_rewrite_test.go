package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// rwRepo is a repo on the History page with two pushed base commits and n
// not-pushed commits c1..cn, each adding its own file.
func rwRepo(t *testing.T, n int) (*tui, string, func(...string) string) {
	t.Helper()
	x := testTUI(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
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
	git("init", "-q", "-b", "main")
	commit := func(file, msg string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", msg)
	}
	commit("base.txt", "base")
	commit("base2.txt", "base two")
	withRemote(t, git)
	for i := 1; i <= n; i++ {
		commit(string(rune('a'+i))+".txt", "c"+string(rune('0'+i)))
	}
	gd, err := x.app.Repos.For(dir)
	if err != nil {
		t.Fatal(err)
	}
	x.histDir = gd
	x.switchTab(tabHistory)
	return x, dir, git
}

// body is the text of the open overlay (view, confirm, picker, form), without colours.
func body(x *tui) string { return history.StripANSI(strings.Join(x.overlay(), "\n")) }

// pickOption opens the option of the open picker whose text contains want.
func pickOption(t *testing.T, x *tui, want string) {
	t.Helper()
	if x.picker == nil {
		t.Fatalf("no picker open; view %v form %v msg %q", x.view != nil, x.form != nil, x.msg)
	}
	for i, o := range x.picker.options {
		if strings.Contains(o, want) {
			for j, v := range x.picker.shown() {
				if v == i {
					x.picker.sel = j
				}
			}
			x.handle("enter")
			return
		}
	}
	t.Fatalf("picker has no %q: %q", want, x.picker.options)
}

func subjects(git func(...string) string, rng string) string {
	return strings.Join(strings.Split(git("log", "--reverse", "--format=%s", rng), "\n"), "|")
}

func openEditor(t *testing.T, x *tui) {
	t.Helper()
	x.handle("L")
	pickOption(t, x, "Interactive rebase")
	pickOption(t, x, "not pushed")
	if x.rwEd == nil {
		t.Fatalf("editor did not open: msg %q\n%s", x.msg, body(x))
	}
}

func TestRewriteKeysAreFree(t *testing.T) {
	x, _, _ := rwRepo(t, 1)
	x.handle("L")
	if x.picker == nil || !strings.Contains(x.picker.title, "Rewrite history") {
		t.Fatalf("L did not open the menu: %+v", x.picker)
	}
	for _, o := range []string{"Rebase onto", "Interactive rebase", "Reword a commit", "Fold staged"} {
		if !strings.Contains(strings.Join(x.picker.options, "\n"), o) {
			t.Errorf("menu lacks %q", o)
		}
	}
	x.handle("esc")
	x.handle("W")
	if x.wtView == nil {
		t.Fatal("W did not open the worktrees")
	}
	x.handle("esc")
	x.handle("V")
	if x.subView == nil {
		t.Fatal("V did not open the submodules")
	}
}

// Rebasing onto a branch: the plan is shown, then the commits are replayed.
func TestRebaseOntoBranch(t *testing.T) {
	x, _, git := rwRepo(t, 0)
	git("checkout", "-q", "-b", "feature")
	for _, f := range []string{"f1", "f2"} {
		os.WriteFile(filepath.Join(git("rev-parse", "--show-toplevel"), f+".txt"), []byte(f), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", f)
	}
	git("checkout", "-q", "main")
	os.WriteFile(filepath.Join(git("rev-parse", "--show-toplevel"), "m1.txt"), []byte("m"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "m1")
	git("checkout", "-q", "feature")
	x.loadHistory()

	x.handle("L")
	pickOption(t, x, "Rebase onto")
	pickOption(t, x, "main")
	b := body(x)
	for _, want := range []string{"Rebase feature onto main?", "Your commits, replayed in this order (2)", "f1", "Coming in from main (1)", "m1", "(y/n)"} {
		if !strings.Contains(b, want) {
			t.Fatalf("plan lacks %q:\n%s", want, b)
		}
	}
	x.handle("y")
	if got := subjects(git, "main..feature"); got != "f1|f2" {
		t.Fatalf("after the rebase: %q", got)
	}
	if git("merge-base", "main", "feature") != git("rev-parse", "main") {
		t.Fatal("feature is not on top of main")
	}
	if !strings.Contains(x.msg, "Rebased feature onto main") || !strings.Contains(x.msg, "U to undo") {
		t.Fatalf("msg %q", x.msg)
	}
}

// Both sides change a file: the rebase stops and says to press e.
func TestRebaseStopsOnClash(t *testing.T) {
	x, dir, git := rwRepo(t, 0)
	write := func(s string) {
		os.WriteFile(filepath.Join(dir, "base.txt"), []byte(s), 0o644)
		git("commit", "-q", "-am", s)
	}
	git("checkout", "-q", "-b", "feature")
	write("mine")
	git("checkout", "-q", "main")
	write("theirs")
	git("checkout", "-q", "feature")
	x.loadHistory()
	x.handle("L")
	pickOption(t, x, "Rebase onto")
	pickOption(t, x, "main")
	if !strings.Contains(body(x), "base.txt") || !strings.Contains(body(x), "clashes are likely") {
		t.Fatalf("plan should name the likely clash:\n%s", body(x))
	}
	x.handle("y")
	b := body(x)
	if x.view == nil || !strings.Contains(b, "Press e for the operation view") || !strings.Contains(b, "base.txt") {
		t.Fatalf("stop not explained:\n%s", b)
	}
	if !strings.Contains(x.msg, "press e") && !strings.Contains(x.msg, "Press e") {
		t.Fatalf("msg %q", x.msg)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "rebase-merge")); err != nil {
		t.Fatalf("the rebase is not left stopped: %v", err)
	}
	git("rebase", "--abort")
}

func TestRebaseRefusesDirtyTree(t *testing.T) {
	x, dir, git := rwRepo(t, 1)
	git("branch", "other")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed"), 0o644)
	x.handle("L")
	pickOption(t, x, "Rebase onto")
	pickOption(t, x, "other")
	if x.view == nil || !strings.Contains(body(x), "uncommitted changes") {
		t.Fatalf("dirty tree not refused:\n%s", body(x))
	}
}

// The editor: reorder, squash, reword and drop; the After list matches
// what git ends up with.
func TestInteractiveRewrite(t *testing.T) {
	x, _, git := rwRepo(t, 5)
	openEditor(t, x)
	if n := len(x.rwEd.steps); n != 5 {
		t.Fatalf("%d steps, want 5", n)
	}
	x.handle("j")
	x.handle("s") // c2 into c1
	x.handle("j")
	x.handle("r") // c3 reworded
	x.handle("enter")
	if x.form == nil {
		t.Fatal("Enter on a reword row should open the message form")
	}
	x.handle("ctrl-u")
	typeKeys(x, "c3 reworded")
	x.handle("enter")
	x.handle("j")
	x.handle("K") // c4 above c3
	x.handle("j")
	x.handle("j")
	x.handle("d") // c5 dropped
	if x.rwEd.sel != 4 {
		t.Fatalf("selection %d", x.rwEd.sel)
	}
	lines, _ := x.drawRewrite()
	scr := history.StripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{"5 commits become 3", "1 reworded", "folded into another", "1 dropped", "reordered", "After", "(reworded)", "(squash "} {
		if !strings.Contains(scr, want) {
			t.Fatalf("preview lacks %q:\n%s", want, scr)
		}
	}
	// the order: c1 squash c2, c4, c3
	var order []string
	for _, s := range x.rwEd.steps {
		order = append(order, x.rwEd.commit(s.Hash).Subject+":"+string(s.Action))
	}
	if got := strings.Join(order, " "); got != "c1:pick c2:squash c4:pick c3:reword c5:drop" {
		t.Fatalf("steps = %s", got)
	}
	x.handle("x")
	if x.form == nil || !strings.Contains(x.form.title, "squashed commit") {
		t.Fatalf("expected the squash message form, got %+v", x.form)
	}
	x.handle("enter") // keep the joined messages
	b := body(x)
	if x.confirm == nil || !strings.Contains(b, "Before (5") || !strings.Contains(b, "After (3") || !strings.Contains(b, "c3 reworded") {
		t.Fatalf("confirm lacks before/after:\n%s", b)
	}
	x.handle("y")
	if x.rwEd != nil {
		t.Fatalf("editor still open; msg %q\n%s", x.msg, body(x))
	}
	if got := subjects(git, "origin/main..HEAD"); got != "c1|c4|c3 reworded" {
		t.Fatalf("history = %q (preview was:\n%s)", got, scr)
	}
	if !strings.Contains(git("log", "--format=%B", "-n3", "--reverse", "origin/main..HEAD"), "c2") {
		t.Fatal("the squashed message should keep c2's text")
	}
	if !strings.Contains(x.msg, "U to undo") {
		t.Fatalf("msg %q", x.msg)
	}
	x.handle("U")
	x.handle("y")
	if got := subjects(git, "origin/main..HEAD"); got != "c1|c2|c3|c4|c5" {
		t.Fatalf("after undo: %q", got)
	}
}

// The first commit can't be squashed: shown inline, and x refuses.
func TestInteractiveRewriteValidation(t *testing.T) {
	x, _, _ := rwRepo(t, 2)
	openEditor(t, x)
	x.handle("s")
	lines, _ := x.drawRewrite()
	if scr := history.StripANSI(strings.Join(lines, "\n")); !strings.Contains(scr, "nothing before it is kept") {
		t.Fatalf("no inline error:\n%s", scr)
	}
	x.handle("x")
	if x.confirm != nil || x.form != nil || !x.msgErr {
		t.Fatalf("x should refuse; msg %q", x.msg)
	}
	x.handle("p")
	x.handle("x")
	if !strings.Contains(x.msg, "Nothing would change") {
		t.Fatalf("msg %q", x.msg)
	}
	x.handle("esc")
	if x.rwEd != nil {
		t.Fatal("Esc should close the editor")
	}
}

func TestInteractiveRewriteRefusals(t *testing.T) {
	x, dir, git := rwRepo(t, 2)
	pushed := git("rev-parse", "origin/main")
	x.handle("L")
	pickOption(t, x, "Interactive rebase")
	pickOption(t, x, "since a commit")
	x.form.fields[0].value = pushed
	for range x.form.fields {
		x.handle("enter")
	}
	if x.rwEd != nil || x.view == nil || !strings.Contains(body(x), "pushed") {
		t.Fatalf("a pushed commit was not refused:\n%s", body(x))
	}
	x.handle("esc")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("dirty"), 0o644)
	x.handle("L")
	pickOption(t, x, "Interactive rebase")
	pickOption(t, x, "not pushed")
	if x.rwEd != nil || x.view == nil || !strings.Contains(body(x), "uncommitted changes") {
		t.Fatalf("a dirty tree was not refused:\n%s", body(x))
	}
}

func TestRewordSelectedCommit(t *testing.T) {
	x, _, git := rwRepo(t, 3)
	c1 := strings.Split(git("rev-list", "--reverse", "origin/main..HEAD"), "\n")[0]
	for i, r := range x.histRows {
		if r.Hash == c1 {
			x.sel[tabHistory] = i
		}
	}
	if x.histRows[x.sel[tabHistory]].Hash != c1 {
		t.Fatalf("c1 not in History:\n%s", strings.Join(x.histLines, "\n"))
	}
	tip := git("rev-parse", "HEAD")
	x.handle("l")
	if x.form == nil {
		t.Fatalf("l opened no form: %q", x.msg)
	}
	x.handle("enter") // unchanged
	if !x.msgErr || !strings.Contains(x.msg, "same message") {
		t.Fatalf("unchanged message accepted: %q", x.msg)
	}
	x.handle("l")
	x.handle("ctrl-u")
	typeKeys(x, "c1 better")
	x.handle("enter")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "c1 better") || !strings.Contains(b, "2 commit(s) after it") || !strings.Contains(b, "new hashes") {
		t.Fatalf("confirm:\n%s", b)
	}
	x.handle("y")
	if got := subjects(git, "origin/main..HEAD"); got != "c1 better|c2|c3" {
		t.Fatalf("history %q", got)
	}
	if git("rev-parse", "HEAD") == tip {
		t.Fatal("later commits should have new hashes")
	}
}

func TestRewordRefusesPushedCommit(t *testing.T) {
	x, _, git := rwRepo(t, 1)
	base := git("rev-parse", "origin/main")
	for i, r := range x.histRows {
		if r.Hash == base {
			x.sel[tabHistory] = i
		}
	}
	x.handle("l")
	if x.form != nil || x.view == nil || !strings.Contains(body(x), "pushed") {
		t.Fatalf("pushed commit not refused:\n%s", body(x))
	}
}

func TestFoldStagedIntoOlderCommit(t *testing.T) {
	x, dir, git := rwRepo(t, 3)
	// nothing staged: refused
	x.handle("L")
	pickOption(t, x, "Fold staged")
	pickOption(t, x, " c1")
	if !strings.Contains(body(x), "nothing is staged") {
		t.Fatalf("no refusal:\n%s", body(x))
	}
	x.handle("esc")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("c1\nextra\n"), 0o644) // c1 made b.txt
	git("add", "b.txt")
	x.handle("L")
	pickOption(t, x, "Fold staged")
	pickOption(t, x, " c1")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "1 staged file(s)") || !strings.Contains(b, "2 commit(s) after it") {
		t.Fatalf("confirm:\n%s", b)
	}
	x.handle("y")
	if got := subjects(git, "origin/main..HEAD"); got != "c1|c2|c3" {
		t.Fatalf("history %q (msg %q)", got, x.msg)
	}
	first := git("rev-list", "--reverse", "origin/main..HEAD")
	first = strings.Split(first, "\n")[0]
	if got := git("show", first+":b.txt"); !strings.Contains(got, "extra") {
		t.Fatalf("the change is not in c1: %q", got)
	}
	if git("status", "--porcelain") != "" {
		t.Fatalf("tree not clean: %s", git("status", "--porcelain"))
	}
}
