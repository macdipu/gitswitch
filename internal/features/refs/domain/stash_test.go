package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestStashSubject(t *testing.T) {
	cases := []struct{ in, branch, msg string }{
		{"On main: fix the thing", "main", "fix the thing"},
		{"WIP on feature/x: 1a2b3c4 Subject: with colon", "feature/x", "1a2b3c4 Subject: with colon"},
		{"On (no branch): detached work", "", "detached work"},
		{"On main: gitswitch park: other", "main", "gitswitch park: other"},
		{"something else", "", "something else"},
	}
	for _, c := range cases {
		if b, m := StashSubject(c.in); b != c.branch || m != c.msg {
			t.Errorf("StashSubject(%q) = %q, %q; want %q, %q", c.in, b, m, c.branch, c.msg)
		}
	}
}

func TestStashKind(t *testing.T) {
	cases := []struct{ in, kind, branch string }{
		{"On main: gitswitch park: feature", StashPark, "feature"},
		{"On main: gitswitch discard: main", StashDiscard, "main"},
		{"On main: gitswitch discard: detached HEAD", StashDiscard, "detached HEAD"},
		{"On main: my own", StashPlain, ""},
		{"WIP on main: 1a2b3c4 gitswitch parking lot", StashPlain, ""},
		{"On main: gitswitch park: ", StashPlain, ""},
	}
	for _, c := range cases {
		if k, b := StashKind(c.in); k != c.kind || b != c.branch {
			t.Errorf("StashKind(%q) = %q, %q; want %q, %q", c.in, k, b, c.kind, c.branch)
		}
	}
}

func TestFindStashAndLabel(t *testing.T) {
	all := []Stash{{Hash: "h1", Ref: "stash@{0}", Message: "wip"}, {Hash: "h2", Ref: "stash@{1}", Kind: StashPark, ParkedOn: "x"}}
	if s, err := FindStash(all, "h2"); err != nil || s.Ref != "stash@{1}" {
		t.Errorf("FindStash = %+v %v", s, err)
	}
	for _, h := range []string{"h3", ""} {
		if _, err := FindStash(all, h); !errors.Is(err, ErrStashChanged) || err.Error() != "the stash list changed; reload" {
			t.Errorf("FindStash(%q) = %v", h, err)
		}
	}
	if all[0].Label() != "stash@{0} (wip)" || all[1].Label() != "stash@{1} (parked on x)" {
		t.Errorf("labels: %q %q", all[0].Label(), all[1].Label())
	}
}

func TestStashDescribe(t *testing.T) {
	s := Stash{Ref: "stash@{2}", Branch: "main", Time: 0, Message: "wip", Files: []StashFile{{Path: "a.go"}, {Path: "b.go"}}}
	if got := s.Describe(7200); got != "stash@{2} · main · 2 h ago · 2 files: a.go, b.go · wip" {
		t.Errorf("Describe = %q", got)
	}
	if got := (Stash{Ref: "stash@{0}"}).Describe(0); !strings.Contains(got, "detached HEAD") || !strings.Contains(got, "no files") {
		t.Errorf("Describe = %q", got)
	}
}

func TestCleanStashPathAndReserved(t *testing.T) {
	for in, want := range map[string]string{"a/b.go": "a/b.go", "./a//b.go": "a/b.go", `a\b.go`: "a/b.go"} {
		if got, err := CleanStashPath(in); err != nil || got != want {
			t.Errorf("CleanStashPath(%q) = %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", ".", "../x", "/etc/passwd", "-f"} {
		if _, err := CleanStashPath(in); err == nil {
			t.Errorf("CleanStashPath(%q) should fail", in)
		}
	}
	if !ReservedStashMessage(" gitswitch park: x") || !ReservedStashMessage("gitswitch discard: x") || ReservedStashMessage("park it") {
		t.Error("ReservedStashMessage")
	}
}
