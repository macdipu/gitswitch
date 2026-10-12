package tui

import (
	"errors"
	"fmt"
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
	if x.sel[tabSettings] != n+1 {
		t.Fatalf("selection = %d, want %d (last row)", x.sel[tabSettings], n+1)
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
