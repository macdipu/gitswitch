package domain

import (
	"path/filepath"
	"reflect"
	"testing"
)

func names(gs []Group) []string {
	var out []string
	for _, g := range gs {
		n := g.Name
		for _, r := range g.Repos {
			n += " " + filepath.Base(r.Path)
		}
		out = append(out, n)
	}
	return out
}

func TestGroupRepos(t *testing.T) {
	src, other := filepath.Join("home", "src"), filepath.Join("home", "other")
	list := []Info{ // pinned first, as the list comes
		{Path: filepath.Join(other, "web"), Account: "work", Remote: "git@github.com:Acme/web.git", Pinned: true},
		{Path: filepath.Join(src, "api"), Account: "work", Remote: "https://ssh.github.com/acme/api", Group: "Client"},
		{Path: filepath.Join(src, "notes"), Account: "personal", Remote: "git@gitlab.com:me/sub/notes.git", Group: "client"},
		{Path: filepath.Join(src, "scratch")},
	}
	cases := []struct {
		by   string
		want []string
	}{
		{ByNone, []string{" web api notes scratch"}},
		{ByAccount, []string{"personal notes", "work web api", "No account scratch"}},
		{ByOwner, []string{"github.com/Acme web api", "gitlab.com/me/sub notes", "No remote scratch"}},
		{ByFolder, []string{other + " web", src + " api notes scratch"}},
		{ByGroup, []string{"Client api notes", "Not in a group web scratch"}},
	}
	for _, c := range cases {
		if got := names(GroupRepos(list, c.by)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("by %s:\n got %q\nwant %q", c.by, got, c.want)
		}
	}
	if g := GroupRepos(list, ByAccount); !g[2].Other || g[0].Other {
		t.Errorf("only the last group fits none: %+v", g)
	}
}

func TestCheckGroupByAndName(t *testing.T) {
	for in, want := range map[string]string{"": ByNone, " Owner ": ByOwner, "group": ByGroup} {
		if got, err := CheckGroupBy(in); err != nil || got != want {
			t.Errorf("CheckGroupBy(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := CheckGroupBy("color"); err == nil {
		t.Error("an unknown way to group must be refused")
	}
	if n, err := CheckGroupName("  Client work "); err != nil || n != "Client work" {
		t.Errorf("CheckGroupName = %q, %v", n, err)
	}
	for _, bad := range []string{" ", "a=b", "two\nlines", string(make([]rune, 41))} {
		if _, err := CheckGroupName(bad); err == nil {
			t.Errorf("CheckGroupName(%q) must fail", bad)
		}
	}
}

func TestMarkClones(t *testing.T) {
	list := []Info{
		{Path: "/a/api", Remote: "git@github.com:acme/api.git"},
		{Path: "/b/api", Remote: "https://github.com/Acme/API"},
		{Path: "/c/api", Remote: "ssh://git@ssh.github.com:443/acme/api.git"},
		{Path: "/d/web", Remote: "git@github.com:acme/web.git"},
		{Path: "/e/local", Remote: "/srv/git/local"},
		{Path: "/f/none"},
	}
	MarkClones(list)
	if got := list[0].Clones; !reflect.DeepEqual(got, []string{"/b/api", "/c/api"}) {
		t.Errorf("clones of /a/api = %v", got)
	}
	if got := list[2].Clones; !reflect.DeepEqual(got, []string{"/a/api", "/b/api"}) {
		t.Errorf("clones of /c/api = %v", got)
	}
	for _, r := range list[3:] {
		if r.Clones != nil {
			t.Errorf("%s has no clones, got %v", r.Path, r.Clones)
		}
	}
}
