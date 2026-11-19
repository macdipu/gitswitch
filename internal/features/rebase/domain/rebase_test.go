package domain

import (
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func TestCheck(t *testing.T) {
	ok := refs.Head{Branch: "feature", Hash: "h"}
	cases := []struct {
		name string
		h    refs.Head
		want string // "" = allowed
	}{
		{"clean", ok, ""},
		{"new files are fine", refs.Head{Branch: "f", Hash: "h", Changes: refs.Changes{New: 3}}, ""},
		{"busy", refs.Head{Branch: "f", Hash: "h", Busy: "merge"}, "in the middle of a merge"},
		{"no commits", refs.Head{Branch: "main"}, "no commits yet"},
		{"detached", refs.Head{Hash: "h"}, "HEAD is detached"},
		{"conflicts", refs.Head{Branch: "f", Hash: "h", Changes: refs.Changes{Conflicts: 2}}, "2 file(s) still have conflicts"},
		{"staged", refs.Head{Branch: "f", Hash: "h", Changes: refs.Changes{Staged: 1}}, "commit, park or discard them first"},
		{"changed", refs.Head{Branch: "f", Hash: "h", Changes: refs.Changes{Changed: 1}}, "commit, park or discard them first"},
	}
	for _, c := range cases {
		err := Check(c.h)
		if (err == nil) != (c.want == "") || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: Check = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestCheckName(t *testing.T) {
	for name, bad := range map[string]bool{"main": false, "origin/main": false, "v1.0": false, "": true, "  ": true, "-x": true, "--onto": true} {
		if err := CheckName(name); (err != nil) != bad {
			t.Errorf("CheckName(%q) = %v", name, err)
		}
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		p    Plan
		want string
	}{
		{Plan{Branch: "f", Onto: "main", UpToDate: true}, "nothing to rebase"},
		{Plan{Branch: "f", Onto: "main", FastForward: true, IncomingCount: 1}, "just moves forward to main (1 commit come in)"},
		{Plan{Branch: "f", Onto: "main", ReplayedCount: 3, IncomingCount: 2}, "3 commits are replayed one by one, so they get new hashes"},
		{Plan{Branch: "f", Onto: "main", ReplayedCount: 1, IncomingCount: 2, BothChanged: []string{"a", "b"}}, "clashes are likely (an estimate): a, b"},
		{Plan{Branch: "f", Onto: "main", ReplayedCount: 1, BothChanged: []string{"1", "2", "3", "4", "5", "6", "7"}}, "1, 2, 3, 4, 5 and 2 more"},
		{Plan{Branch: "f", Onto: "main", ReplayedCount: 1}, "no clashes are expected"},
	}
	for _, c := range cases {
		if got := c.p.Describe(); !strings.Contains(got, c.want) {
			t.Errorf("Describe = %q, want %q", got, c.want)
		}
	}
}

func TestErrorsAndLabel(t *testing.T) {
	e := PushedError("f", []string{"1234567890", "abcdefghij"})
	if !strings.Contains(e.Error(), "2 of the commits on f (1234567, abcdefg)") || !strings.Contains(e.Error(), "merge instead") {
		t.Errorf("PushedError = %v", e)
	}
	if !strings.Contains(PushedError("f", []string{"aaaaaaaa", "bbbbbbbb", "cccccccc", "dddddddd"}).Error(), "and 1 more") {
		t.Error("long list not shortened")
	}
	if e := MergesError("f", 2); !strings.Contains(e.Error(), "2 merge commit(s)") || !strings.Contains(e.Error(), "flatten") {
		t.Errorf("MergesError = %v", e)
	}
	if got := Label(Rebased{Branch: "f", Onto: "main", Outcome: Done}); got != "Rebased f onto main" {
		t.Error(got)
	}
	if got := Label(Rebased{Branch: "f", Onto: "main", Outcome: FastForwards}); got != "Fast-forwarded f to main" {
		t.Error(got)
	}
}
