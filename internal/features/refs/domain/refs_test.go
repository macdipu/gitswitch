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

		// Uncommitted changes come last, before an operation in progress.
		"main (not on a remote) · no commits yet · uncommitted: 2 new": {Branch: "main", Changes: Changes{New: 2}},
		"main → origin/main, up to date · HEAD 1a2b3c4 Fix · uncommitted: 1 conflict, 3 changed · merge in progress": {
			Branch: "main", Hash: "x", Short: "1a2b3c4", Subject: "Fix", Upstream: "origin/main", Changes: Changes{Changed: 3, Conflicts: 1}, Busy: "merge"},
	}
	for want, h := range cases {
		if got := h.Describe(); got != want {
			t.Errorf("Describe() = %q, want %q", got, want)
		}
	}
}

func TestChangesDescribe(t *testing.T) {
	cases := map[string]Changes{
		"": {},
		"uncommitted: 2 staged, 3 changed, 1 new":             {Staged: 2, Changed: 3, New: 1},
		"uncommitted: 2 conflicts":                            {Conflicts: 2},
		"uncommitted: 1 conflict, 1 staged, 1 changed, 1 new": {Conflicts: 1, Staged: 1, Changed: 1, New: 1},
	}
	for want, c := range cases {
		if got := c.Describe(); got != want {
			t.Errorf("%+v.Describe() = %q, want %q", c, got, want)
		}
		if c.Any() != (want != "") {
			t.Errorf("%+v.Any() = %v", c, c.Any())
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
