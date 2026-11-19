package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

func TestParseKeys(t *testing.T) {
	got, rest := parseKeys([]byte("a\x1b[A\x1b[B\r\x7f\x1b[5~\x1b\x03é\x1b"), false)
	want := []string{"a", "up", "down", "enter", "backspace", "pgup", "esc", "ctrl-c", "é", "esc"}
	if strings.Join(got, ",") != strings.Join(want, ",") || rest != nil {
		t.Fatalf("parseKeys = %v %q, want %v", got, rest, want)
	}
}

// A key split across two reads is kept for the next read, not typed as
// separate characters.
func TestParseKeysSplitAcrossReads(t *testing.T) {
	for _, c := range []struct {
		first, second string
		full          bool
		want          string
	}{
		{"ab\x1b[", "5~c", false, "a,b,pgup,c"},
		{"x\x1b", "[Ay", true, "x,up,y"},
		{"\xc3", "\xa9!", false, "é,!"},
	} {
		keys, rest := parseKeys([]byte(c.first), c.full)
		more, rest := parseKeys(append(rest, c.second...), false)
		if got := strings.Join(append(keys, more...), ","); got != c.want || rest != nil {
			t.Errorf("%q + %q = %s (rest %q), want %s", c.first, c.second, got, rest, c.want)
		}
	}
}

func testTUI(t *testing.T) *tui {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": home + "/.gitconfig", "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		t.Setenv(k, v)
	}
	x := &tui{app: bootstrap.New(false, nil), w: 80, h: 24}
	x.reload()
	return x
}

// A selection past the end of a list that got shorter is moved back, so
// Enter on Settings cannot index past the health checks.
func TestSettingsSelectionStaysInRange(t *testing.T) {
	x := testTUI(t)
	x.switchTab(tabSettings)
	n := len(x.checks)
	x.sel[tabSettings] = n + 10
	x.reload()
	if last := settingRows + n - 1; x.sel[tabSettings] != last {
		t.Fatalf("selection = %d, want %d (last row)", x.sel[tabSettings], last)
	}
	x.sel[tabSettings] = n + 10
	x.checks = x.checks[:0] // fewer checks than before
	x.handle("enter")       // must not panic
	for _, s := range x.sel {
		if s < 0 {
			t.Fatalf("negative selection %v", x.sel)
		}
	}
}

func TestEscDoesNotQuit(t *testing.T) {
	x := testTUI(t)
	x.handle("a") // the add-account form
	x.handle("esc")
	x.handle("esc")
	if x.quit || x.form != nil {
		t.Fatalf("quit = %v, form open = %v after Esc Esc", x.quit, x.form != nil)
	}
	x.handle("q")
	if !x.quit {
		t.Fatal("q should quit")
	}
}

// Questions answer only to the keys they name; d is not D.
func TestConfirmNeedsItsOwnKeys(t *testing.T) {
	x := testTUI(t)
	ran := ""
	x.confirm = &confirmBox{"Remove?", map[string]func() error{
		"y": func() error { ran = "y"; return nil },
		"D": func() error { ran = "D"; return nil },
	}}
	for _, k := range []string{"d", "x", "enter"} {
		x.handle(k)
	}
	if x.confirm == nil || ran != "" {
		t.Fatalf("stray keys answered the question (ran %q)", ran)
	}
	x.handle("D")
	if x.confirm != nil || ran != "D" {
		t.Fatalf("D ran %q", ran)
	}
	x.confirm = &confirmBox{"Remove?", map[string]func() error{"y": func() error { ran = "y"; return nil }}}
	x.handle("esc")
	if x.confirm != nil || ran != "D" {
		t.Fatal("Esc should cancel")
	}
}

// A long picker list scrolls with the selection and narrows as you type.
func TestPickerScrollsAndFilters(t *testing.T) {
	x := testTUI(t)
	var opts []string
	for i := 0; i < 100; i++ {
		opts = append(opts, fmt.Sprintf("option %03d", i))
	}
	picked := -1
	x.picker = &picker{title: "Pick", options: opts, pick: func(i int) error { picked = i; return nil }}
	for i := 0; i < 60; i++ {
		x.handle("down")
	}
	box := strings.Join(x.overlay(), "\n")
	if !strings.Contains(box, "› option 060") || len(x.overlay()) > x.h-4 {
		t.Fatalf("selection not on screen, or box too tall (%d lines):\n%s", len(x.overlay()), history.StripANSI(box))
	}
	for _, k := range []string{"0", "4", "2"} {
		x.handle(k)
	}
	x.handle("enter")
	if picked != 42 {
		t.Fatalf("picked %d, want 42", picked)
	}
}

func TestViewerScrolls(t *testing.T) {
	x := testTUI(t)
	x.h = minH
	x.handle("?")
	first := history.StripANSI(strings.Join(x.overlay(), "\n"))
	x.handle("end")
	last := history.StripANSI(strings.Join(x.overlay(), "\n"))
	if first == last || !strings.Contains(last, "Questions") || len(x.overlay()) > x.h-4 {
		t.Fatalf("help does not scroll to its end:\n%s", last)
	}
}

func TestFitKeepsColoursAndWidth(t *testing.T) {
	s := "\x1b[31mhello\x1b[0m world"
	out := fit(s, 7)
	if visibleLen(out) != 7 || !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("fit = %q (visible %d)", out, visibleLen(out))
	}
	if visibleLen(padANSI("\x1b[1mab\x1b[0m", 5)) != 5 {
		t.Fatal("padANSI width")
	}
}

func TestAccountColumnsLineUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", home+"/.gitconfig")
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "1")
	x := &tui{app: bootstrap.New(false, nil), cfg: &account.Config{Default: "personal", Accounts: []account.Account{
		{ID: "personal", Email: "me@example.com", Host: "github.com"},
		{ID: "a-much-longer-account-id", Email: "a.very.long.address@company.example.com", Host: "gitlab.company.example"},
		{ID: "work", Email: "me@work.example", Host: "github.com"},
	}}}
	lines, _ := x.drawAccounts()
	col := -1
	for _, l := range lines {
		plain := history.StripANSI(l)
		i := strings.Index(plain, "no key file")
		if i < 0 {
			continue
		}
		i = utf8.RuneCountInString(plain[:i]) // screen columns, not bytes
		if col >= 0 && i != col {
			t.Fatalf("key column at %d, want %d:\n%s", i, col, strings.Join(lines, "\n"))
		}
		col = i
	}
}

func TestKnownErrorsAreExplained(t *testing.T) {
	x := testTUI(t)
	x.say(errors.New("git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository."), "")
	if x.view == nil || x.view.title != "What went wrong" || !strings.Contains(strings.Join(x.view.lines, "\n"), "What to do:") {
		t.Fatalf("view = %+v", x.view)
	}
	x.view = nil
	x.say(errors.New("something new"), "")
	if x.view != nil || x.msg != "something new" || !x.msgErr {
		t.Errorf("an unknown error stays a status line: view=%v msg=%q", x.view, x.msg)
	}
}

func TestWelcomeOnlyWithoutAccounts(t *testing.T) {
	x := testTUI(t)
	x.welcome()
	if x.view == nil || x.view.title != "Welcome to GitSwitch" {
		t.Fatalf("no accounts: view = %+v", x.view)
	}
	x.view = nil
	x.cfg.Accounts = []account.Account{{ID: "work"}}
	if x.welcome(); x.view != nil {
		t.Error("with an account there is nothing to welcome")
	}
}

// changesRepo is a repo with one commit (a.txt, b.txt), then a.txt changed
// and staged, b.txt changed and new.txt added, shown on the History page.
func changesRepo(t *testing.T) (*tui, string, func(...string) string) {
	t.Helper()
	x := testTUI(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
		t.Setenv(k, v)
	}
	dir := filepath.Join(t.TempDir(), "r")
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	write("a.txt", "one\n")
	write("b.txt", "two\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	write("a.txt", "one more\n")
	git("add", "a.txt")
	write("b.txt", "two changed\n")
	write("new.txt", "fresh\n")
	gd, err := x.app.Repos.For(dir)
	if err != nil {
		t.Fatal(err)
	}
	x.histDir = gd
	x.switchTab(tabHistory)
	return x, dir, git
}

// screen is the History page as drawn, without colours.
func screen(x *tui) string {
	lines, _ := x.drawChanges()
	return history.StripANSI(strings.Join(lines, "\n"))
}

// selectPath moves the selection to path in a section.
func selectPath(t *testing.T, x *tui, path, sec string) {
	t.Helper()
	x.handle("g")
	for i := range x.changes.rows {
		if r := x.changes.rows[i]; r.f.Path == path && r.sec == sec {
			return
		}
		x.handle("j")
	}
	t.Fatalf("%s is not under %s:\n%s", path, sec, screen(x))
}

// s on History lists the changes in sections, a letter per file.
func TestChangesList(t *testing.T) {
	x, _, _ := changesRepo(t)
	x.handle("s")
	if x.changes == nil {
		t.Fatalf("s did not open the changes (msg %q)", x.msg)
	}
	got := screen(x)
	for _, want := range []string{"Uncommitted changes", "branch: main", "Staged (1)", "M a.txt", "Unstaged (2)", "M b.txt", "N new.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Conflicts") {
		t.Errorf("a Conflicts section with no conflict:\n%s", got)
	}
	if f := x.footer(); !strings.Contains(f, "s stage") {
		t.Errorf("footer = %q", f)
	}
	x.handle("q") // back to the commits, not quit
	if x.quit || x.changes != nil {
		t.Fatalf("q: quit = %v, changes open = %v", x.quit, x.changes != nil)
	}
}

func TestChangesStageAndUnstage(t *testing.T) {
	x, _, git := changesRepo(t)
	x.handle("s")
	selectPath(t, x, "new.txt", "changed")
	x.handle("s")
	if st := git("status", "--porcelain"); !strings.Contains(st, "A  new.txt") {
		t.Fatalf("new.txt not staged (msg %q):\n%s", x.msg, st)
	}
	if r := x.changes.rows[x.changes.sel]; x.msgErr || !strings.Contains(screen(x), "Staged (2)") {
		t.Fatalf("after staging: msg %q, selected %+v:\n%s", x.msg, r, screen(x))
	}
	selectPath(t, x, "a.txt", "staged")
	x.handle("u")
	if st := git("diff", "--cached", "--name-only"); strings.Contains(st, "a.txt") {
		t.Fatalf("a.txt still staged (msg %q):\n%s", x.msg, st)
	}
	x.handle("a")
	if got := screen(x); !strings.Contains(got, "Staged (3)") || strings.Contains(got, "Unstaged") {
		t.Fatalf("a did not stage all (msg %q):\n%s", x.msg, got)
	}
	x.handle("A")
	if got := screen(x); strings.Contains(got, "Staged") || !strings.Contains(got, "Changes (3)") {
		t.Fatalf("A did not unstage all (msg %q):\n%s", x.msg, got)
	}
}

// x asks first; n keeps the file, y puts it back and keeps the change in
// git stash.
func TestChangesDiscardAsks(t *testing.T) {
	x, dir, git := changesRepo(t)
	x.handle("s")
	selectPath(t, x, "b.txt", "changed")
	x.handle("x")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "git stash") {
		t.Fatalf("x did not ask: %+v", x.confirm)
	}
	x.handle("n")
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "two changed\n" || x.changes == nil {
		t.Fatalf("cancelled discard changed b.txt to %q", b)
	}
	x.handle("x")
	x.handle("y")
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "two\n" {
		t.Fatalf("b.txt = %q after discard (msg %q)", b, x.msg)
	}
	if s := git("stash", "list"); !strings.Contains(s, "gitswitch discard: main") {
		t.Fatalf("not kept in the stash: %q", s)
	}
	if got := screen(x); strings.Contains(got, "b.txt") || x.msgErr {
		t.Fatalf("b.txt still listed (msg %q):\n%s", x.msg, got)
	}
}

// Enter shows the file's diff, staged under Staged; Esc goes back to the
// list.
func TestChangesDiff(t *testing.T) {
	x, _, _ := changesRepo(t)
	x.handle("s")
	selectPath(t, x, "a.txt", "staged")
	x.handle("enter")
	if x.view == nil {
		t.Fatalf("no diff (msg %q)", x.msg)
	}
	text := history.StripANSI(strings.Join(x.view.lines, "\n"))
	if !strings.Contains(x.view.title, "a.txt · staged") || !strings.Contains(text, "-one") || !strings.Contains(text, "+one more") {
		t.Fatalf("diff %q:\n%s", x.view.title, text)
	}
	x.handle("esc")
	selectPath(t, x, "new.txt", "changed")
	x.handle("d")
	if x.view == nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "+fresh") {
		t.Fatalf("new file diff: %+v", x.view)
	}
	x.handle("esc")
	if x.view != nil || x.changes == nil {
		t.Fatal("Esc should close the diff and keep the changes")
	}
}

// C commits from the changes; with nothing left it goes back to the commits.
func TestChangesCommit(t *testing.T) {
	x, _, git := changesRepo(t)
	x.handle("s")
	x.handle("a")
	x.handle("C")
	if x.form == nil || !strings.Contains(x.form.title, "3 staged") {
		t.Fatalf("commit form: %+v (msg %q)", x.form, x.msg)
	}
	for _, k := range []string{"F", "i", "x", "enter"} {
		x.handle(k)
	}
	if got := git("log", "-1", "--format=%s"); got != "Fix" || x.changes != nil {
		t.Fatalf("last commit %q, changes open = %v (msg %q)", got, x.changes != nil, x.msg)
	}
}

// typeKeys sends each rune of s as a key.
func typeKeys(x *tui, s string) {
	for _, r := range s {
		x.handle(string(r))
	}
}

// selectCommit puts the History selection on the newest commit line.
func selectCommit(t *testing.T, x *tui) string {
	t.Helper()
	for i, r := range x.histRows {
		if r.Hash != "" {
			x.sel[tabHistory] = i
			return r.Hash
		}
	}
	t.Fatalf("no commit line in:\n%s", strings.Join(x.histLines, "\n"))
	return ""
}

// withRemote gives the repo a bare origin with main pushed.
func withRemote(t *testing.T, git func(...string) string) {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	git("remote", "add", "origin", bare)
	git("push", "-q", "origin", "main")
}

// In the commit form, ↑ then → amends the last commit: its message is
// filled in, ← back to a new commit clears it, and Esc changes nothing.
func TestCommitFormAmends(t *testing.T) {
	x, _, git := changesRepo(t)
	x.handle("C")
	if x.form == nil || x.form.focus != 1 {
		t.Fatalf("commit form: %+v (msg %q)", x.form, x.msg)
	}
	x.handle("up")
	x.handle("right")
	if f := x.form; !strings.HasPrefix(f.title, "Amend ") || f.fields[1].value != "base" {
		t.Fatalf("amend chosen: title %q, message %q", f.title, f.fields[1].value)
	}
	x.handle("left")
	if f := x.form; f.fields[1].value != "" || !strings.HasPrefix(f.title, "Commit the 1 staged") {
		t.Fatalf("back to a new commit: title %q, message %q", f.title, f.fields[1].value)
	}
	x.handle("esc")
	if x.form != nil || git("rev-list", "--count", "HEAD") != "1" {
		t.Fatal("Esc should close the form and commit nothing")
	}

	x.handle("C")
	x.handle("up")
	x.handle("right")
	x.handle("down")
	x.handle("ctrl-u")
	typeKeys(x, "base, reworded")
	x.handle("enter")
	if n, s := git("rev-list", "--count", "HEAD"), git("log", "-1", "--format=%s"); n != "1" || s != "base, reworded" {
		t.Fatalf("after amend: %s commit(s), last %q (msg %q)", n, s, x.msg)
	}
	if got := git("show", "HEAD:a.txt"); got != "one more" || !strings.Contains(x.msg, "Amended") || !strings.Contains(x.msg, "U to undo") {
		t.Fatalf("a.txt in the commit = %q, msg %q", got, x.msg)
	}
}

// A pushed last commit can't be amended: the form says why and offers only
// a new commit.
func TestCommitFormSaysWhyNoAmend(t *testing.T) {
	x, _, git := changesRepo(t)
	withRemote(t, git)
	git("branch", "-u", "origin/main")
	x.loadHistory()
	x.handle("C")
	if x.form == nil {
		t.Fatalf("no commit form (msg %q)", x.msg)
	}
	k := x.form.fields[0]
	if len(k.options) != 1 || !strings.Contains(k.hint, "already pushed") {
		t.Fatalf("amend offered for a pushed commit: %+v", k)
	}
	x.handle("up")
	if box := history.StripANSI(strings.Join(x.overlay(), "\n")); !strings.Contains(box, "can't amend") {
		t.Fatalf("the reason is not shown:\n%s", box)
	}
}

// B lists the branches; r renames one (Undo is mentioned), Esc in the form
// keeps the name.
func TestBranchRename(t *testing.T) {
	x, _, git := changesRepo(t)
	git("branch", "feature")
	x.handle("B")
	if x.picker == nil {
		t.Fatalf("B: no branch list (msg %q)", x.msg)
	}
	typeKeys(x, "feature")
	x.handle("enter")
	if x.confirm == nil || x.confirm.actions["r"] == nil || x.confirm.actions["s"] == nil {
		t.Fatalf("no branch actions: %+v", x.confirm)
	}
	x.handle("r")
	if x.form == nil || x.form.fields[0].value != "feature" || x.msg == "Done." {
		t.Fatalf("rename form: %+v (msg %q)", x.form, x.msg)
	}
	x.handle("esc")
	if !strings.Contains(git("branch"), "feature") {
		t.Fatal("cancelled rename renamed")
	}
	x.handle("B")
	typeKeys(x, "feature")
	x.handle("enter")
	x.handle("r")
	x.handle("ctrl-u")
	typeKeys(x, "feat2")
	x.handle("enter")
	if b := git("branch", "--list", "feat2"); b == "" || !strings.Contains(x.msg, "U to undo") {
		t.Fatalf("not renamed: branches %q, msg %q", git("branch"), x.msg)
	}
}

// d asks; a branch with commits on no other branch asks again before it
// goes.
func TestBranchDelete(t *testing.T) {
	x, _, git := changesRepo(t)
	git("branch", "merged")
	side := git("commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "only here")
	git("update-ref", "refs/heads/side", side)
	open := func(name string) {
		t.Helper()
		x.handle("B")
		typeKeys(x, name)
		x.handle("enter")
		x.handle("d")
		if x.confirm == nil || !strings.Contains(x.confirm.text, "Delete the branch "+name) {
			t.Fatalf("d did not ask: %+v (msg %q)", x.confirm, x.msg)
		}
	}
	open("merged")
	x.handle("n")
	if git("branch", "--list", "merged") == "" {
		t.Fatal("n deleted the branch")
	}
	open("merged")
	x.handle("y")
	if git("branch", "--list", "merged") != "" || !strings.Contains(x.msg, "U to undo") {
		t.Fatalf("merged not deleted (msg %q)", x.msg)
	}

	open("side")
	x.handle("y")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "anyway") {
		t.Fatalf("no second question for commits on no other branch: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if git("branch", "--list", "side") == "" {
		t.Fatal("n at the second question deleted the branch")
	}
	open("side")
	x.handle("y")
	x.handle("y")
	if git("branch", "--list", "side") != "" || !strings.Contains(x.msg, "Deleted the branch side") {
		t.Fatalf("side not deleted (msg %q)", x.msg)
	}
}

// u sets the remote branch a branch pulls from, or none.
func TestBranchSetUpstream(t *testing.T) {
	x, dir, git := changesRepo(t)
	withRemote(t, git)
	x.handle("B")
	typeKeys(x, "main")
	x.handle("enter")
	x.handle("u")
	if x.picker == nil {
		t.Fatalf("u: no remote branch list (msg %q)", x.msg)
	}
	x.handle("esc")
	if x.picker != nil {
		t.Fatal("Esc should close the list")
	}
	if out, _ := exec.Command("git", "-C", dir, "config", "branch.main.merge").Output(); len(out) > 0 {
		t.Fatal("Esc set an upstream")
	}
	x.handle("B")
	typeKeys(x, "main")
	x.handle("enter")
	x.handle("u")
	typeKeys(x, "origin/main")
	x.handle("enter")
	if got := git("rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" || x.msgErr {
		t.Fatalf("upstream %q (msg %q)", got, x.msg)
	}
	x.handle("B")
	typeKeys(x, "main")
	x.handle("enter")
	x.handle("u")
	typeKeys(x, "none")
	x.handle("enter")
	if out, _ := exec.Command("git", "-C", dir, "config", "branch.main.merge").Output(); len(out) > 0 || !strings.Contains(x.msg, "no remote branch") {
		t.Fatalf("upstream not unset: %q (msg %q)", out, x.msg)
	}
}

// t tags the selected commit (a message makes it annotated); T lists the
// tags, to push or delete one (asks first).
func TestTags(t *testing.T) {
	x, _, git := changesRepo(t)
	x.handle("T")
	if x.picker != nil || !strings.Contains(x.msg, "no tags") {
		t.Fatalf("T with no tags: picker %v, msg %q", x.picker != nil, x.msg)
	}
	hash := selectCommit(t, x)
	x.handle("t")
	if x.form == nil {
		t.Fatalf("t: no tag form (msg %q)", x.msg)
	}
	typeKeys(x, "v0")
	x.handle("esc")
	if git("tag") != "" {
		t.Fatal("Esc made a tag")
	}
	x.handle("t")
	typeKeys(x, "v1")
	x.handle("enter")
	x.handle("enter")
	if git("tag") != "v1" || git("cat-file", "-t", "v1") != "commit" || git("rev-parse", "v1") != hash {
		t.Fatalf("lightweight v1: tags %q (msg %q)", git("tag"), x.msg)
	}
	selectCommit(t, x)
	x.handle("t")
	typeKeys(x, "v2")
	x.handle("enter")
	typeKeys(x, "Release")
	x.handle("enter")
	if git("cat-file", "-t", "v2") != "tag" || !strings.Contains(x.msg, "annotated") {
		t.Fatalf("v2 not annotated (msg %q)", x.msg)
	}

	x.handle("T")
	typeKeys(x, "v1")
	x.handle("enter")
	if x.confirm == nil || x.confirm.actions["p"] == nil || x.confirm.actions["d"] == nil {
		t.Fatalf("no tag actions: %+v", x.confirm)
	}
	x.handle("d")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "Delete the tag v1") {
		t.Fatalf("d did not ask: %+v", x.confirm)
	}
	x.handle("n")
	if !strings.Contains(git("tag"), "v1") {
		t.Fatal("n deleted the tag")
	}
	x.handle("T")
	typeKeys(x, "v1")
	x.handle("enter")
	x.handle("d")
	x.handle("y")
	if git("tag") != "v2" || x.msgErr {
		t.Fatalf("tags %q after deleting v1 (msg %q)", git("tag"), x.msg)
	}
}

// i in the changes ignores the selected file, its extension or its folder,
// in .gitignore or only for me; a tracked file is untracked only on yes.
func TestChangesIgnore(t *testing.T) {
	x, dir, git := changesRepo(t)
	x.handle("s")
	selectPath(t, x, "new.txt", "changed")
	x.handle("i")
	if x.form == nil || len(x.form.fields[0].options) != 2 { // the file, *.txt; no folder at the top
		t.Fatalf("ignore form: %+v (msg %q)", x.form, x.msg)
	}
	x.handle("esc")
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Fatal("Esc wrote .gitignore")
	}
	x.handle("i")
	x.handle("enter")
	x.handle("enter")
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(b), "/new.txt") {
		t.Fatalf(".gitignore = %q (msg %q)", b, x.msg)
	}
	if got := screen(x); strings.Contains(got, "new.txt") {
		t.Fatalf("new.txt still listed:\n%s", got)
	}

	ignoreExtLocal := func() {
		t.Helper()
		selectPath(t, x, "b.txt", "changed")
		x.handle("i")
		x.handle("right") // *.txt
		x.handle("down")
		x.handle("right") // only for me
		x.handle("enter")
		if x.confirm == nil || !strings.Contains(x.confirm.text, "b.txt is tracked") {
			t.Fatalf("no untrack question: %+v (msg %q)", x.confirm, x.msg)
		}
	}
	ignoreExtLocal()
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); !strings.Contains(string(b), "*.txt") {
		t.Fatalf("exclude = %q", b)
	}
	x.handle("n")
	if !strings.Contains(git("ls-files"), "b.txt") {
		t.Fatal("n untracked b.txt")
	}
	ignoreExtLocal()
	x.handle("y")
	if strings.Contains(git("ls-files"), "b.txt") || !strings.Contains(x.msg, "untracked") {
		t.Fatalf("b.txt still tracked (msg %q): %s", x.msg, git("ls-files"))
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal("untracking removed the file")
	}
}
