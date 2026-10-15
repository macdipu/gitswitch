package domain

import (
	"testing"

	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

func TestFilterAndFacets(t *testing.T) {
	rs := []RemoteRepo{
		{Account: "work", FullName: "acme/api", Owner: "acme", Description: "The billing API", Private: true, Language: "Go", Cloned: []string{"/src/api"}},
		{Account: "work", FullName: "acme/web", Owner: "acme", Language: "TypeScript", Cloned: []string{}},
		{Account: "home", FullName: "dipu/notes", Owner: "dipu", Language: "go", Cloned: []string{}},
		{Account: "home", FullName: "dipu/old", Owner: "dipu", Archived: true, Cloned: []string{}},
	}
	names := func(f Filter) string {
		s := ""
		for _, r := range f.Apply(rs) {
			s += r.FullName + " "
		}
		return s
	}
	cases := map[string]Filter{
		"acme/api acme/web dipu/notes ":          {},
		"acme/api acme/web dipu/notes dipu/old ": {Archived: true},
		"acme/api ":                              {Text: "billing API"},
		"acme/web dipu/notes ":                   {Visibility: "public"},
		"acme/api dipu/notes ":                   {Language: "GO"},
		"dipu/notes ":                            {Account: "HOME"},
		"acme/web ":                              {Owner: "acme", Cloned: "no"},
		"":                                       {Text: "acme nothing"},
	}
	for want, f := range cases {
		if got := names(f); got != want {
			t.Errorf("%+v: got %q, want %q", f, got, want)
		}
	}
	owners, langs := Facets(rs)
	if len(owners) != 2 || owners[0] != "acme" || len(langs) != 2 || langs[0] != "Go" {
		t.Errorf("facets = %v %v", owners, langs)
	}
}

func TestMergeMarkAndFind(t *testing.T) {
	rs := Merge([]RepoList{
		{Repos: []RemoteRepo{{Account: "work", FullName: "Acme/API", SSHURL: "git@github.com:Acme/API.git", HTTPSURL: "https://github.com/Acme/API.git"}}},
		{Repos: []RemoteRepo{{Account: "home", FullName: "acme/api"}, {Account: "home", FullName: "a/b"}}},
	})
	if len(rs) != 2 || rs[0].FullName != "a/b" || rs[1].Account != "work" {
		t.Fatalf("Merge = %+v", rs)
	}
	MarkCloned(rs, []repo.Info{
		{Path: "/x/api", Remote: "ssh://git@ssh.github.com:443/acme/api"},
		{Path: "/gone/api", Remote: "git@github.com:acme/api.git", Missing: true},
	})
	if c := rs[1].Cloned; len(c) != 1 || c[0] != "/x/api" {
		t.Errorf("Cloned = %v", c)
	}
	for _, q := range []string{"acme/api", "ACME/API.git", "https://github.com/acme/api", "git@github.com:acme/api.git"} {
		if r, ok := Find(rs, q, ""); !ok || r.FullName != "Acme/API" {
			t.Errorf("Find(%q) = %+v, %v", q, r, ok)
		}
	}
	if _, ok := Find(rs, "acme/nothing", ""); ok {
		t.Error("Find of an unknown repo must fail")
	}
}
