package history

import (
	"testing"

	"github.com/macdipu/gitswitch/internal/account"
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
