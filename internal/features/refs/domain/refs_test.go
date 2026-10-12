package domain

import "testing"

func TestDescribe(t *testing.T) {
	cases := map[string]Head{
		"main → origin/main, 2 to push, 1 to pull · HEAD 1a2b3c4 Fix": {Branch: "main", Hash: "x", Short: "1a2b3c4", Subject: "Fix", Upstream: "origin/main", Ahead: 2, Behind: 1},
		"main → origin/main, up to date · HEAD 1a2b3c4 Fix":           {Branch: "main", Hash: "x", Short: "1a2b3c4", Subject: "Fix", Upstream: "origin/main"},
		"wip (not on a remote) · HEAD 1a2b3c4 Fix":                    {Branch: "wip", Hash: "x", Short: "1a2b3c4", Subject: "Fix"},
		"detached HEAD · HEAD 1a2b3c4 Fix · rebase in progress":       {Hash: "x", Short: "1a2b3c4", Subject: "Fix", Busy: "rebase"},
		"main (not on a remote) · no commits yet":                     {Branch: "main"},
		"feat → origin/feat (not in this clone) · HEAD 1a2b3c4 Fix":   {Branch: "feat", Hash: "x", Short: "1a2b3c4", Subject: "Fix", Gone: "origin/feat"},
	}
	for want, h := range cases {
		if got := h.Describe(); got != want {
			t.Errorf("Describe() = %q, want %q", got, want)
		}
	}
}

func TestFindAndLocalName(t *testing.T) {
	bs := []Branch{{Name: "origin/main", Remote: true}, {Name: "main"}, {Name: "origin/feature/x", Remote: true}}
	if b := Find(bs, "main"); b == nil || b.Remote {
		t.Errorf("a local branch must win: %+v", b)
	}
	if b := Find(bs, "origin/feature/x"); b == nil || b.LocalName() != "feature/x" {
		t.Errorf("remote branch: %+v", b)
	}
	if Find(bs, "nope") != nil {
		t.Error("unknown branch found")
	}
}
