package domain

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	before := Mark{Worktree: "/r", Branch: "main", Head: "a"}
	if r, ok := New(Drop, "Dropped x", before, Mark{Worktree: "/r", Branch: "main", Head: "b"}, 9); !ok ||
		r.Before != "a" || r.After != "b" || r.Branch != "main" || r.To != "" || r.Time != 9 {
		t.Errorf("drop: %+v %v", r, ok)
	}
	if r, ok := New(Switch, "Switched", before, Mark{Worktree: "/r", Branch: "feat", Head: "a"}, 9); !ok || r.To != "feat" || r.Branch != "main" {
		t.Errorf("switch: %+v %v", r, ok)
	}
	for name, after := range map[string]Mark{
		"HEAD did not move":   before,
		"another branch":      {Worktree: "/r", Branch: "feat", Head: "b"},
		"detached after":      {Worktree: "/r", Head: "b"},
		"another worktree":    {Worktree: "/s", Branch: "main", Head: "b"},
		"switch to same name": {Worktree: "/r", Branch: "main", Head: "b"},
	} {
		a := Drop
		if name == "switch to same name" {
			a = Switch
		}
		if _, ok := New(a, "x", before, after, 0); ok {
			t.Errorf("%s: recorded", name)
		}
	}
	if _, ok := New(Revert, "x", Mark{Worktree: "/r", Head: "a"}, Mark{Worktree: "/r", Branch: "main", Head: "b"}, 0); ok {
		t.Error("detached before: recorded")
	}
}

func TestCheck(t *testing.T) {
	drop := Record{Action: Drop, Label: "Dropped x", Worktree: "/r", Branch: "main", Before: "a", After: "b"}
	sw := Record{Action: Switch, Label: "Switched", Worktree: "/r", Branch: "main", To: "feat", Before: "a", After: "c"}
	if err := drop.Check(Mark{Worktree: "/r", Branch: "main", Head: "b"}); err != nil {
		t.Errorf("drop ok: %v", err)
	}
	// After a switch, new commits on the branch switched to do not matter:
	// switching back loses nothing.
	if err := sw.Check(Mark{Worktree: "/r", Branch: "feat", Head: "d"}); err != nil {
		t.Errorf("switch ok: %v", err)
	}
	for want, c := range map[string]struct {
		r   Record
		now Mark
	}{
		"middle of a rebase":     {drop, Mark{Worktree: "/r", Branch: "main", Head: "b", Busy: "rebase"}},
		"another working folder": {drop, Mark{Worktree: "/s", Branch: "main", Head: "b"}},
		"you are on feat now":    {drop, Mark{Worktree: "/r", Branch: "feat", Head: "b"}},
		"on a detached HEAD now": {drop, Mark{Worktree: "/r", Head: "b"}},
		"main has changed since": {drop, Mark{Worktree: "/r", Branch: "main", Head: "z"}},
		"no longer on feat":      {sw, Mark{Worktree: "/r", Branch: "main", Head: "a"}},
	} {
		if err := c.r.Check(c.now); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check(%+v) = %v, want %q", c.now, err, want)
		}
	}
}

func TestEffect(t *testing.T) {
	for a, want := range map[Action]string{
		Switch: "Switches back to main", Rollback: "Your files are not changed", Revert: "Removes the new commit",
		CherryPick: "Removes the new commit", Drop: "back exactly as it was", FixAuthor: "back exactly as it was",
	} {
		r := Record{Action: a, Branch: "main"}
		if got := r.Effect(); !strings.Contains(got, want) {
			t.Errorf("%s: Effect() = %q, want %q", a, got, want)
		}
		if r.Rewinds() != (a != Switch) {
			t.Errorf("%s: Rewinds() = %v", a, r.Rewinds())
		}
	}
}
