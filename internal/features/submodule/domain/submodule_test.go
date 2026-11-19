package domain

import (
	"reflect"
	"strings"
	"testing"
)

const h1 = "1111111111111111111111111111111111111111"
const h2 = "2222222222222222222222222222222222222222"
const zero = "0000000000000000000000000000000000000000"

func TestParseStatus(t *testing.T) {
	out := " " + h1 + " libs/core (v1.2-3-gabc)\n" +
		"-" + h2 + " docs\n" +
		"+" + h1 + " apps/web (heads/main)\n" +
		"U" + zero + " conflicted\n" +
		" " + h2 + " libs/core/nested one (remotes/origin/HEAD)\n" +
		"garbage\n\n"
	want := []Line{
		{' ', h1, "libs/core", "v1.2-3-gabc"},
		{'-', h2, "docs", ""},
		{'+', h1, "apps/web", "heads/main"},
		{'U', "", "conflicted", ""},
		{' ', h2, "libs/core/nested one", "remotes/origin/HEAD"},
	}
	if got := ParseStatus(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := ParseStatus(""); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

func TestStateOf(t *testing.T) {
	for p, want := range map[byte]string{' ': UpToDate, '-': NotInitialized, '+': OtherCommit, 'U': Conflicts} {
		if got := StateOf(p); got != want {
			t.Errorf("%q: %s, want %s", p, got, want)
		}
	}
}

func TestParseModules(t *testing.T) {
	out := "submodule.core.path\nlibs/core\x00submodule.core.url\ngit@github.com:acme/core.git\x00submodule.core.branch\ndevelop\x00" +
		"submodule.my.dotted.name.path\nvendor/x\x00submodule.my.dotted.name.url\n../x.git\x00" +
		"submodule.orphan.url\nhttps://h/o.git\x00"
	want := []Module{
		{"core", "libs/core", "git@github.com:acme/core.git", "develop"},
		{"my.dotted.name", "vendor/x", "../x.git", ""},
	}
	if got := ParseModules(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := ParseModules(""); len(got) != 0 {
		t.Fatalf("empty: %+v", got)
	}
}

func TestBuild(t *testing.T) {
	mods := []Module{{"a", "a", "u", "main"}, {"b", "b", "u", ""}, {"c", "c", "u", ""}, {"d", "d", "u", ""}}
	plain := []Line{{' ', h1, "a", ""}, {'-', h1, "b", ""}, {'+', h2, "c", ""}, {'U', "", "d", ""}}
	cached := []Line{{' ', h1, "a", ""}, {'-', h1, "b", ""}, {' ', h1, "c", ""}, {'U', "", "d", ""}}
	es := Build(mods, plain, cached)
	type row struct {
		state     string
		init      bool
		rec, have string
		dirty     int
	}
	want := []row{
		{UpToDate, true, h1, h1, -1},
		{NotInitialized, false, h1, "", 0},
		{OtherCommit, true, h1, h2, -1},
		{Conflicts, true, "", "", -1},
	}
	for i, e := range es {
		if got := (row{e.State, e.Initialized, e.Recorded, e.CheckedOut, e.Dirty}); got != want[i] {
			t.Errorf("%s: %+v, want %+v", e.Path, got, want[i])
		}
	}
	// A path .gitmodules lists but git does not know yet.
	if e := Build([]Module{{"x", "x", "u", ""}}, nil, nil)[0]; e.State != NotInitialized || e.Initialized {
		t.Errorf("unknown: %+v", e)
	}
}

func TestResolveURL(t *testing.T) {
	for _, c := range [][3]string{
		{"git@github.com:acme/sup.git", "../other.git", "git@github.com:acme/other.git"},
		{"git@github.com:acme/sup.git", "../../dipu/notes.git", "git@github.com:dipu/notes.git"},
		{"https://github.com/acme/sup.git", "../other.git", "https://github.com/acme/other.git"},
		{"https://github.com/acme/sup.git/", "./inner.git", "https://github.com/acme/sup.git/inner.git"},
		{"git@github.com:acme/sup.git", "git@github.com:x/y.git", "git@github.com:x/y.git"},
		{"", "../x.git", "../x.git"},
		{"/local/sup", "../x.git", "../x.git"},
	} {
		if got := ResolveURL(c[0], c[1]); got != c[2] {
			t.Errorf("ResolveURL(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestCheckPath(t *testing.T) {
	good := map[string]string{"libs/core": "libs/core", "a\\b": "a/b", "./x//y/": "x/y", " vendor/z ": "vendor/z"}
	for in, want := range good {
		if got, err := CheckPath(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-x", "/abs", "../up", "a/../../b", ".git", "a/.GIT/b", "C:\\x", "a\nb", "."} {
		if _, err := CheckPath(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestCheckBranchAndName(t *testing.T) {
	for _, b := range []string{"", "main", "release/1.2", "feat_x"} {
		if err := CheckBranch(b); err != nil {
			t.Errorf("%q: %v", b, err)
		}
	}
	for _, b := range []string{"-x", "a b", "a..b", "x/", "x.lock", "a:b", "a\\b"} {
		if CheckBranch(b) == nil {
			t.Errorf("%q accepted", b)
		}
	}
	for _, n := range []string{"core", "libs/core", "my.dotted"} {
		if err := CheckModuleName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "../x", "a/../b", "/abs", "a//b", "a\\b"} {
		if CheckModuleName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	clean := Entry{Path: "clean", Initialized: true, Recorded: h1, CheckedOut: h1}
	moved := Entry{Path: "moved", Initialized: true, Recorded: h1, CheckedOut: h2, Dirty: 3}
	dirtySame := Entry{Path: "same", Initialized: true, Recorded: h1, CheckedOut: h1, Dirty: 2}
	fresh := Entry{Path: "fresh", Dirty: 0}
	conf := Entry{Path: "conf", Initialized: true, State: Conflicts}
	unknown := Entry{Path: "unknown", Initialized: true, Recorded: h1, CheckedOut: h2, Dirty: -1}
	cases := []struct {
		name    string
		ts      []Entry
		remote  bool
		contain []string
	}{
		{"all clean", []Entry{clean, fresh}, false, nil},
		{"dirty and would move", []Entry{clean, moved}, false, []string{"moved has 3 changed file(s)"}},
		{"dirty but staying put", []Entry{dirtySame}, false, nil},
		{"remote moves them all", []Entry{dirtySame}, true, []string{"same has 2"}},
		{"conflicts", []Entry{conf}, false, []string{"conf has merge conflicts"}},
		{"unknown count does not block", []Entry{unknown}, false, nil},
		{"two reasons", []Entry{moved, conf}, false, []string{"moved", "conf"}},
	}
	for _, c := range cases {
		got := CheckUpdate(c.ts, c.remote)
		if len(c.contain) == 0 && got != "" {
			t.Errorf("%s: refused: %s", c.name, got)
		}
		for _, w := range c.contain {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
	}
}

func TestCheckRemove(t *testing.T) {
	e := Entry{Path: "libs/core", Name: "core", Initialized: true}
	if got := CheckRemove(e, 0, false); got != "" {
		t.Errorf("clean: %s", got)
	}
	e.Dirty = 2
	got := CheckRemove(e, 3, false)
	if !strings.Contains(got, "2 changed file(s) and 3 commit(s)") || !strings.Contains(got, "--force") {
		t.Errorf("got %q", got)
	}
	if CheckRemove(e, 3, true) != "" {
		t.Error("force still refused")
	}
	plan := strings.Join(RemovePlan(e, 3), "|")
	for _, w := range []string{"folder libs/core", ".gitmodules", ".git/modules/core", "2 changed", "3 commit"} {
		if !strings.Contains(plan, w) {
			t.Errorf("plan lacks %q: %s", w, plan)
		}
	}
	if p := strings.Join(RemovePlan(Entry{Path: "x", Name: "x"}, 0), "|"); strings.Contains(p, ".git/modules") {
		t.Errorf("uninitialized has no downloaded copy: %s", p)
	}
}

func TestAccountNote(t *testing.T) {
	if got := AccountNote("u", "work", "work"); got != "" {
		t.Errorf("same account: %q", got)
	}
	if got := AccountNote("u", "work", "me"); !strings.Contains(got, "account me") || !strings.Contains(got, "not this repo's work") {
		t.Errorf("different: %q", got)
	}
	if got := AccountNote("u", "work", ""); !strings.Contains(got, "will ask") {
		t.Errorf("none: %q", got)
	}
}

func TestTargets(t *testing.T) {
	es := []Entry{{Path: "a"}, {Path: "b"}}
	if ts, err := Targets(es, ""); err != nil || len(ts) != 2 {
		t.Fatal(ts, err)
	}
	if ts, err := Targets(es, "b"); err != nil || len(ts) != 1 || ts[0].Path != "b" {
		t.Fatal(ts, err)
	}
	if _, err := Targets(es, "c"); err == nil || !strings.Contains(err.Error(), "not a submodule") {
		t.Fatal(err)
	}
}
