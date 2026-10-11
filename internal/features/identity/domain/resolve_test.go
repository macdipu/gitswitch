package domain

import (
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

func TestMatchesOwner(t *testing.T) {
	yes := [][2]string{{"acme/api.git", "acme"}, {"Acme/API.git", "acme"}, {"acme/api.git", "acme/api"}, {"acme/api.git", "acme/*"}, {"g/sub/p.git", "g/sub"}}
	no := [][2]string{{"acme-corp/api.git", "acme"}, {"acme/api.git", "acme/web"}, {"other/acme.git", "acme"}}
	for _, c := range yes {
		if !MatchesOwner(c[0], c[1]) {
			t.Errorf("%q should match %q", c[0], c[1])
		}
	}
	for _, c := range no {
		if MatchesOwner(c[0], c[1]) {
			t.Errorf("%q should not match %q", c[0], c[1])
		}
	}
}

func TestMatchesFolder(t *testing.T) {
	link := map[string]string{"/home/me/link": "/data/work"}
	real := func(p string) string {
		if r, ok := link[p]; ok {
			return r
		}
		return p
	}
	if !MatchesFolder("/data/work/api", "/home/me/link", real, false) || MatchesFolder("/data/work/api", "/home/me/link", nil, false) {
		t.Error("a symlinked rule folder should match only when symlinks are resolved")
	}
	if MatchesFolder("/src/Work/x", "/src/work", nil, false) || !MatchesFolder("/src/Work/x", "/src/work", nil, true) {
		t.Error("letter case should matter only with foldCase")
	}
	if MatchesFolder("/src/workshop", "/src/work", nil, true) {
		t.Error("a sibling folder with the same prefix must not match")
	}
}

func cfg() *account.Config {
	return &account.Config{
		Default:  "work",
		Accounts: []account.Account{{ID: "work"}, {ID: "client"}, {ID: "me"}},
		Rules: []account.Rule{
			{Kind: "owner", Pattern: "acme", Account: "client"},
			{Kind: "owner", Pattern: "acme/web", Account: "me"},
			{Kind: "folder", Pattern: "/src", Account: "work"},
			{Kind: "folder", Pattern: "/src/personal", Account: "me"},
		},
		Renamed: map[string]string{"old": "client"},
	}
}

func TestDecideOrder(t *testing.T) {
	c := cfg()
	wt := func(p string) func() string { return func() string { return p } }
	cases := []struct {
		name string
		in   Input
		want string
		why  string
	}{
		{"saved wins over rules", Input{SavedID: "me", RepoPath: "acme/api.git"}, "me", Saved},
		{"saved id follows a rename", Input{SavedID: "old"}, "client", Saved},
		{"owner rule", Input{RepoPath: "acme/api.git"}, "client", OwnerRule},
		{"most specific owner rule", Input{RepoPath: "acme/web.git"}, "me", OwnerRule},
		{"owner rule beats folder rule", Input{RepoPath: "acme/api.git", Worktree: wt("/src/personal/x")}, "client", OwnerRule},
		{"deepest folder rule", Input{RepoPath: "other/x.git", Worktree: wt("/src/personal/x")}, "me", FolderRule},
		{"folder rule", Input{RepoPath: "other/x.git", Worktree: wt("/src/x")}, "work", FolderRule},
		{"unknown saved id falls through", Input{SavedID: "ghost", RepoPath: "acme/api.git"}, "client", OwnerRule},
	}
	for _, tc := range cases {
		d := Decide(c, c.Accounts, tc.in)
		if d == nil || d.Account.ID != tc.want || d.Reason != tc.why {
			t.Errorf("%s: got %+v, want %s (%s)", tc.name, d, tc.want, tc.why)
		}
	}
	if d := Decide(c, c.Accounts, Input{RepoPath: "nobody/x.git"}); d != nil {
		t.Errorf("no match should ask, got %+v", d)
	}
	one := []account.Account{{ID: "solo"}}
	if d := Decide(c, one, Input{RepoPath: "nobody/x.git"}); d == nil || d.Reason != OnlyAccount {
		t.Errorf("single candidate should be chosen, got %+v", d)
	}
	// a rule pointing at an account for another host is ignored
	if d := Decide(c, []account.Account{{ID: "work"}, {ID: "me"}}, Input{RepoPath: "acme/api.git"}); d != nil {
		t.Errorf("rule for a non-candidate account must not apply, got %+v", d)
	}
}

func TestCandidatesAndDefault(t *testing.T) {
	c := &account.Config{Default: "b", Accounts: []account.Account{{ID: "a", Host: "github.com"}, {ID: "b", Host: "github.com"}, {ID: "g", Host: "gitlab.com"}}}
	if got := Candidates(c, "ssh.github.com"); len(got) != 2 {
		t.Fatalf("port-443 host candidates = %d", len(got))
	}
	if DefaultAmong(c, Candidates(c, "github.com")).ID != "b" {
		t.Fatal("default not chosen")
	}
	if DefaultAmong(c, Candidates(c, "gitlab.com")).ID != "g" {
		t.Fatal("first candidate should be used when the default is for another host")
	}
}
