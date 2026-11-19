package domain

import (
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Auto, "auto": Auto, "ff": FastForward, "ff-only": FastForward, "merge": Commit, "no-ff": Commit, "squash": Squash} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMode("octopus"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestCheck(t *testing.T) {
	ok := refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{New: 2}}
	if err := Check(ok); err != nil {
		t.Fatalf("new files only: %v", err)
	}
	for want, h := range map[string]refs.Head{
		"middle of a rebase":      {Branch: "main", Hash: "abc", Busy: "rebase"},
		"detached":                {Hash: "abc"},
		"no commits":              {Branch: "main"},
		"commit, park or discard": {Branch: "main", Hash: "abc", Changes: refs.Changes{Changed: 1}},
		"still have conflicts":    {Branch: "main", Hash: "abc", Changes: refs.Changes{Conflicts: 1}},
	} {
		if err := Check(h); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want %q", h, err, want)
		}
	}
	if CheckName("-x") == nil || CheckName(" ") == nil || CheckName("origin/main") != nil {
		t.Error("CheckName")
	}
}

func TestPlanDescribe(t *testing.T) {
	cases := []struct {
		p    Plan
		want string
	}{
		{Plan{Into: "main", From: "x", UpToDate: true}, "nothing to merge"},
		{Plan{Into: "main", From: "x", FastForward: true, IncomingCount: 1}, "can fast-forward to x: 1 commit come in"},
		{Plan{Into: "main", From: "x", IncomingCount: 2, LocalCount: 1, Predicted: true, Conflicts: []string{"a.go"}}, "1 file(s) will clash: a.go"},
		{Plan{Into: "main", From: "x", IncomingCount: 2, LocalCount: 1, Predicted: true}, "No clashes"},
		{Plan{Into: "main", From: "x", IncomingCount: 2, LocalCount: 1, BothChanged: []string{"a", "b", "c", "d", "e", "f"}}, "clashes are likely: a, b, c, d, e and 1 more"},
	}
	for _, c := range cases {
		if got := c.p.Describe(); !strings.Contains(got, c.want) {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}

func TestMergedName(t *testing.T) {
	for msg, want := range map[string]string{
		"Merge branch 'feature/x'\n\n# Conflicts:":   "feature/x",
		"Merge remote-tracking branch 'origin/main'": "origin/main",
		"Merge branch 'side' into main":              "side",
		"Merge tag 'v1.0'":                           "v1.0",
		"Something else":                             "",
	} {
		if got := MergedName(msg); got != want {
			t.Errorf("MergedName(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestOperationActions(t *testing.T) {
	if a := ActionsFor(OpMerge); len(a) != 2 || a[0] != Continue || a[1] != Abort {
		t.Errorf("merge: %v", a)
	}
	for _, k := range []string{OpRebase, OpCherryPick, OpRevert} {
		if o := (Operation{Kind: k, Actions: ActionsFor(k)}); !o.Allowed(Skip) || !o.Allowed(Continue) || !o.Allowed(Abort) {
			t.Errorf("%s: %v", k, o.Actions)
		}
	}
	if ActionsFor(OpBisect) != nil || ActionsFor("smart switch") != nil || ActionsFor("") != nil {
		t.Error("bisect, smart switch and nothing have no actions here")
	}
	if !strings.Contains(Describe("smart switch", nil), "gitswitch resolve") || !strings.Contains(Describe("merge", []string{"a"}), "1 file(s) have conflicts: a") {
		t.Error("Describe")
	}
	if ours, theirs := Sides(OpRebase, "", ""); !strings.Contains(ours, "onto") || !strings.Contains(theirs, "your commit") {
		t.Errorf("rebase sides: %q %q", ours, theirs)
	}
	if ours, theirs := Sides(OpMerge, "main", "side"); ours != "main (this branch)" || theirs != "side (being merged in)" {
		t.Errorf("merge sides: %q %q", ours, theirs)
	}
}

func TestConflictHelpers(t *testing.T) {
	if KindOf(true, true, true) != "both changed" || KindOf(false, true, true) != "both added" ||
		KindOf(true, true, false) != "deleted by them" || KindOf(true, false, true) != "deleted by us" {
		t.Error("KindOf")
	}
	if !Binary([]byte("a\x00b")) || Binary([]byte("plain\n")) {
		t.Error("Binary")
	}
	if !HasMarkers("<<<<<<< HEAD\na\n=======\nb\n>>>>>>> side\n") || HasMarkers("a\n=======\nb\n") {
		t.Error("HasMarkers")
	}
	if Label(Merged{Into: "main", From: "x", Outcome: FastForwards}) != "Fast-forwarded main to x" || Label(Merged{Into: "main", From: "x", Outcome: MergeCommit}) != "Merged x into main" {
		t.Error("Label")
	}
}
