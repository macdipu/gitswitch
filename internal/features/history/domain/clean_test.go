package domain

import (
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"Fix login":                     "Fix login",
		"tab\there":                     "tab here",
		"evil\x1b]0;title\x07 subject":  "evil]0;title subject",
		"\x1b[2J\x1b[Hclear":            "[2J[Hclear",
		"c1\u009b31mred\u0085":          "c131mred",
		"del\x7f nul\x00 cr\r lf\n end": "del nul cr lf end",
		"héllo · 日本":                    "héllo · 日本",
	} {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// Rows from git never carry escape sequences of their own; only the colour
// added by paint.
func TestRowsAreCleaned(t *testing.T) {
	c := &account.Config{}
	h := &History{Commits: []Commit{{Hash: "abc", Short: "abc", Subject: "x\x1b[2Jy", Author: "a\x1b]8;;evil\x07", Email: "e\x1b@x", Refs: "HEAD -> m\x1b[0m", Flagged: true}}}
	var rows []Row
	rows = append(rows, ListRows(c, h, false)...)
	rows = append(rows, FlaggedRows(h, 1, false)...)
	rows = append(rows, GraphRows(c, h, "* \x1fabc", false)...)
	for _, r := range rows {
		if strings.ContainsRune(r.Text, 0x1b) || strings.ContainsRune(r.Text, 0x07) {
			t.Errorf("row has control characters: %q", r.Text)
		}
	}
}
