package domain

import (
	"strconv"
	"strings"
	"testing"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

func TestLayoutLanes(t *testing.T) {
	// M merges B into A; A and B both come from R.
	cs := []Commit{
		{Hash: "M", Parents: []string{"A", "B"}},
		{Hash: "B", Parents: []string{"R"}},
		{Hash: "A", Parents: []string{"R"}},
		{Hash: "R"},
	}
	w := LayoutLanes(cs)
	if w != 2 {
		t.Fatalf("lanes = %d, want 2", w)
	}
	if cs[0].Col != 0 || cs[1].Col != 1 || cs[2].Col != 0 || cs[3].Col != 0 {
		t.Fatalf("columns %d %d %d %d", cs[0].Col, cs[1].Col, cs[2].Col, cs[3].Col)
	}
	// B (col 1) must connect down into R's column 0
	found := false
	for _, e := range cs[2].Edges {
		if e == [2]int{0, 0} {
			found = true
		}
	}
	if !found {
		t.Fatalf("A has no edge to R: %v", cs[2].Edges)
	}
	if LayoutLanes(nil) != 0 {
		t.Fatal("empty history")
	}
}

func TestWrongAccountRule(t *testing.T) {
	c := &account.Config{Accounts: []account.Account{{ID: "work", Email: "w@x"}, {ID: "me", Email: "m@x"}}}
	repo := &c.Accounts[0]
	if IsWrongAccount(c, repo, "w@x", true) {
		t.Error("own email flagged")
	}
	if !IsWrongAccount(c, repo, "m@x", false) {
		t.Error("other own account not flagged")
	}
	if IsWrongAccount(c, repo, "teammate@x", false) {
		t.Error("pushed teammate commit flagged")
	}
	if !IsWrongAccount(c, repo, "stray@x", true) {
		t.Error("unknown email on a local-only commit not flagged")
	}
}

func TestParseDateAndForGit(t *testing.T) {
	now := time.Date(2026, 10, 11, 15, 30, 0, 0, time.UTC)
	ok := map[string]string{
		"2024-05-01":       "2024-05-01 00:00:00",
		"2024-05-01 14:30": "2024-05-01 14:30:00",
		" Today ":          "2026-10-11 00:00:00",
		"yesterday":        "2026-10-10 00:00:00",
		"2 weeks ago":      "2026-09-27 15:30:00",
		"1 month ago":      "2026-09-11 15:30:00",
		"3 hours ago":      "2026-10-11 12:30:00",
	}
	for in, want := range ok {
		got, err := ParseDate(in, now, false)
		if err != nil || got.Format("2006-01-02 15:04:05") != want {
			t.Errorf("ParseDate(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	if got, _ := ParseDate("2024-05-01", now, true); got.Format("15:04:05") != "23:59:59" {
		t.Errorf("an end date covers the whole day, got %v", got)
	}
	for _, in := range []string{"bogus", "2024-13-45", "2999-01-01", "1960-01-01", "x weeks ago", "2 fortnights ago", "-1"} {
		if _, err := ParseDate(in, now, false); err == nil {
			t.Errorf("ParseDate(%q) should fail", in)
		}
	}
	g, none, err := Filter{Since: "2024-05-01", Until: "2024-05-31", Author: "ana"}.ForGit(now)
	if err != nil || none || g.Since != "@"+strconv.FormatInt(time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC).Unix(), 10) || g.Author != "ana" {
		t.Errorf("ForGit = %+v %v %v", g, none, err)
	}
	if _, _, err := (Filter{Since: "2024-06-01", Until: "2024-05-01"}).ForGit(now); err == nil {
		t.Error("a start after the end must be refused")
	}
	if _, none, _ := (Filter{Since: "2030-01-01"}).ForGit(now); !none {
		t.Error("a start in the future matches nothing")
	}
}

func TestParseDateEdges(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, ny)
	end := map[string]string{
		"2026-03-08":          "2026-03-08 23:59:59", // 23 hours long (clocks go forward)
		"2025-11-02":          "2025-11-02 23:59:59", // 25 hours long
		"2024-05-01 14:30":    "2024-05-01 14:30:59", // the whole minute
		"2024-05-01T14:30":    "2024-05-01 14:30:59",
		"2024-05-01 14:30:10": "2024-05-01 14:30:10",
	}
	for in, want := range end {
		got, err := ParseDate(in, now, true)
		if err != nil || got.Format("2006-01-02 15:04:05") != want {
			t.Errorf("ParseDate(%q, end) = %v, %v; want %s", in, got, err, want)
		}
	}
	ago := map[string]string{
		"1 month ago":   "2026-02-28 10:00:00", // not 3 March
		"13 months ago": "2025-02-28 10:00:00",
		"1 year ago":    "2025-03-31 10:00:00",
	}
	for in, want := range ago {
		got, err := ParseDate(in, now, false)
		if err != nil || got.Format("2006-01-02 15:04:05") != want {
			t.Errorf("ParseDate(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
	leap := time.Date(2024, 2, 29, 9, 0, 0, 0, time.UTC)
	if got, _ := ParseDate("1 year ago", leap, false); got.Format("2006-01-02") != "2023-02-28" {
		t.Errorf("a year before 29 February = %v, want 2023-02-28", got)
	}
}

func TestParseNumstat(t *testing.T) {
	var d Detail
	ParseNumstat(&d, "\n3\t1\tmain.go\n-\t-\tlogo.png\n10\t0\tdocs/{a.md => b.md}\r\n\n")
	want := []FileChange{{Path: "main.go", Added: 3, Deleted: 1}, {Path: "logo.png", Binary: true}, {Path: "docs/{a.md => b.md}", Added: 10}}
	if len(d.Files) != len(want) || d.Added != 13 || d.Deleted != 1 {
		t.Fatalf("got %+v", d)
	}
	for i := range want {
		if d.Files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, d.Files[i], want[i])
		}
	}
	var big Detail
	ParseNumstat(&big, strings.Repeat("1\t1\tf\n", MaxDetailFiles+3))
	if len(big.Files) != MaxDetailFiles || big.More != 3 || big.Added != MaxDetailFiles+3 {
		t.Errorf("big commit: %d files, %d more, %d added", len(big.Files), big.More, big.Added)
	}
	var none Detail
	if ParseNumstat(&none, ""); none.Files == nil {
		t.Error("no files should be [] in JSON, not null")
	}
}
