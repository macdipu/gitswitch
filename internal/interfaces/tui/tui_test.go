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
