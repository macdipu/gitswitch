package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

func TestParseKeys(t *testing.T) {
	got := parseKeys([]byte("a\x1b[A\x1b[B\r\x7f\x1b[5~\x1b\x03é"))
	want := []string{"a", "up", "down", "enter", "backspace", "pgup", "esc", "ctrl-c", "é"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseKeys = %v, want %v", got, want)
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
