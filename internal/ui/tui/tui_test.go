package tui

import (
	"strings"
	"testing"
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
