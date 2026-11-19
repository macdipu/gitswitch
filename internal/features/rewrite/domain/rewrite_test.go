package domain

import (
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func cs() []Commit {
	mk := func(h, m string) Commit {
		return Commit{Hash: strings.Repeat(h, 40), Short: strings.Repeat(h, 7), Subject: strings.SplitN(m, "\n", 2)[0], Message: m, Parents: []string{"p"}}
	}
	return []Commit{mk("a", "A\n\nbody a"), mk("b", "B"), mk("c", "C"), mk("d", "D")}
}

func h(c string) string { return strings.Repeat(c, 40) }

func st(c string, a Action, m string) Step { return Step{Hash: h(c), Action: a, Message: m} }

func TestBuild(t *testing.T) {
	cases := []struct {
		name  string
		steps []Step
		err   string
		check func(t *testing.T, p Plan)
	}{
		{"missing", []Step{st("a", Pick, ""), st("b", Pick, ""), st("c", Pick, "")}, "is missing", nil},
		{"duplicate", []Step{st("a", Pick, ""), st("a", Pick, ""), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "twice", nil},
		{"extra", []Step{st("a", Pick, ""), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, ""), st("e", Pick, "")}, "not one of", nil},
		{"unknown action", []Step{st("a", "weird", ""), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "unknown action", nil},
		{"squash first", []Step{st("a", Squash, ""), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "nothing before it is kept", nil},
		{"fixup after drop first", []Step{st("a", Drop, ""), st("b", Fixup, ""), st("c", Pick, ""), st("d", Pick, "")}, "nothing before it is kept", nil},
		{"reword without message", []Step{st("a", Reword, "  \n"), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "needs a new message", nil},
		{"no change", []Step{st("a", Pick, ""), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if !p.NoChange || p.After != 4 {
				t.Fatalf("%+v", p)
			}
		}},
		{"reword to the same text is no change", []Step{st("a", Reword, "A\n\nbody a\n"), st("b", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if !p.NoChange || p.Reworded != 0 {
				t.Fatalf("%+v", p)
			}
		}},
		{"reorder", []Step{st("b", Pick, ""), st("a", Pick, ""), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if !p.Moved || p.NoChange || len(p.Messages) != 0 || p.Lines[0].Hash != h("b") {
				t.Fatalf("%+v", p)
			}
		}},
		{"drop all but one", []Step{st("a", Drop, ""), st("b", Drop, ""), st("c", Pick, ""), st("d", Drop, "")}, "", func(t *testing.T, p Plan) {
			if p.Dropped != 3 || p.After != 1 || p.Summary() != "4 commits become 1 commit: 3 dropped" {
				t.Fatalf("%+v %s", p, p.Summary())
			}
		}},
		{"squash joins messages", []Step{st("a", Pick, ""), st("b", Squash, ""), st("c", Fixup, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if p.After != 2 || p.Squashed != 2 || len(p.Messages) != 1 || p.Messages[0] != "A\n\nbody a\n\nB" {
				t.Fatalf("%+v", p)
			}
			// the group's first line remembers HEAD, its last sets the message
			if p.Lines[0].Pre != 0 || p.Lines[2].Msg != 0 || p.Lines[1].Msg != -1 || p.Lines[2].Action != Fixup {
				t.Fatalf("%+v", p.Lines)
			}
		}},
		{"fixup keeps the message", []Step{st("a", Pick, ""), st("b", Fixup, ""), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if len(p.Messages) != 0 || p.Result[0].Subject != "A" {
				t.Fatalf("%+v", p)
			}
		}},
		{"explicit squash message wins", []Step{st("a", Reword, "head"), st("b", Squash, "final"), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if p.Messages[0] != "final" || p.Reworded != 1 {
				t.Fatalf("%+v", p)
			}
		}},
		{"reworded head joined with squashed", []Step{st("a", Reword, "head"), st("b", Squash, ""), st("c", Pick, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if p.Messages[0] != "head\n\nB" {
				t.Fatalf("%+v", p.Messages)
			}
		}},
		{"drop between group members", []Step{st("a", Pick, ""), st("b", Drop, ""), st("c", Squash, ""), st("d", Pick, "")}, "", func(t *testing.T, p Plan) {
			if p.After != 2 || p.Messages[0] != "A\n\nbody a\n\nC" || p.Lines[2].Msg != 0 {
				t.Fatalf("%+v", p)
			}
		}},
	}
	for _, c := range cases {
		p, err := Build(cs(), c.steps)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		c.check(t, p)
	}
}

func TestResolve(t *testing.T) {
	steps, err := Resolve(cs(), []Step{{Hash: "aaaa", Action: "p"}, {Hash: strings.ToUpper(h("b")), Action: "S"}, {Hash: "cccc"}, {Hash: "dddd", Action: Drop}})
	if err != nil || steps[0].Hash != h("a") || steps[0].Action != Pick || steps[1].Action != Squash || steps[2].Action != Pick {
		t.Fatalf("%+v %v", steps, err)
	}
	if _, err := Resolve(cs(), []Step{{Hash: "zzzz"}}); err == nil {
		t.Fatal("unknown hash resolved")
	}
	if _, err := Resolve(cs(), []Step{{Hash: "aa"}}); err == nil {
		t.Fatal("too short a prefix resolved")
	}
	dup := append(cs(), Commit{Hash: "aaaa" + strings.Repeat("0", 36)})
	if _, err := Resolve(dup, []Step{{Hash: "aaaa"}}); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := Resolve(cs(), []Step{{Hash: "aaaa", Action: "edit"}}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("edit: %v", err)
	}
}

func TestEligibleAndCheckSince(t *testing.T) {
	c := cs()
	if got, note := Eligible(c); len(got) != 4 || note != "" {
		t.Fatalf("%d %q", len(got), note)
	}
	c[1].Merge = true
	if got, note := Eligible(c); len(got) != 2 || got[0].Subject != "C" || !strings.Contains(note, "merge commit") {
		t.Fatalf("%v %q", got, note)
	}
	c = cs()
	c[0].Parents = nil
	if got, note := Eligible(c); len(got) != 3 || !strings.Contains(note, "first commit") {
		t.Fatalf("%v %q", got, note)
	}
	if err := CheckSince(c); err == nil || !strings.Contains(err.Error(), "first commit") {
		t.Fatal(err)
	}
	c = cs()
	c[2].Merge = true
	if err := CheckSince(c); err == nil || !strings.Contains(err.Error(), "merge commit") {
		t.Fatal(err)
	}
	if err := CheckSince(cs()); err != nil {
		t.Fatal(err)
	}
}

func TestChecks(t *testing.T) {
	ok := refs.Head{Branch: "main", Hash: "x"}
	for _, c := range []struct {
		name string
		h    refs.Head
		want string
	}{
		{"ready", ok, ""},
		{"busy", refs.Head{Branch: "main", Hash: "x", Busy: "merge"}, "middle of a merge"},
		{"detached", refs.Head{Hash: "x"}, "detached"},
		{"no commits", refs.Head{Branch: "main"}, "no commits"},
		{"staged", refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{Staged: 1}}, "uncommitted changes"},
		{"changed", refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{Changed: 1}}, "uncommitted changes"},
		{"untracked is fine", refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{New: 3}}, ""},
		{"conflicts", refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{Conflicts: 1}}, "conflicts"},
	} {
		err := Check(c.h)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	if err := CheckFold(refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{Staged: 1}}); err != nil {
		t.Error(err)
	}
	if err := CheckFold(ok); err == nil || !strings.Contains(err.Error(), "nothing is staged") {
		t.Error(err)
	}
	if err := CheckFold(refs.Head{Branch: "main", Hash: "x", Changes: refs.Changes{Staged: 1, Changed: 1}}); err == nil || !strings.Contains(err.Error(), "not staged") {
		t.Error(err)
	}
}

func TestParseTodoAndPrepare(t *testing.T) {
	text := "# comment\n\nr aaaaaaa subject\nsquash bbbb\nf cccc\n  drop dddd  \n"
	steps, err := ParseTodo(text, cs())
	if err != nil || len(steps) != 4 || steps[0].Action != Reword || steps[1].Action != Squash || steps[2].Action != Fixup || steps[3].Action != Drop || steps[3].Hash != h("d") {
		t.Fatalf("%+v %v", steps, err)
	}
	if s, err := ParseTodo("# only comments\n", cs()); err != nil || s != nil {
		t.Fatalf("empty = %v %v", s, err)
	}
	for _, bad := range []string{"pick\n", "melt aaaa\n", "pick zzzz\n"} {
		if _, err := ParseTodo(bad, cs()); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if got := FormatTodo(cs()); !strings.HasPrefix(got, "pick aaaaaaa A\npick bbbbbbb B\n") || !strings.Contains(got, "# d, drop") {
		t.Fatalf("FormatTodo = %q", got)
	}

	// reword head with a squash after it: one combined prompt; the head gets its own text.
	out, prompts := Prepare(cs(), steps)
	if len(prompts) != 1 || !prompts[0].Combine || prompts[0].Index != 2 || prompts[0].Initial != "A\n\nbody a\n\nB" || out[0].Message != "A\n\nbody a" {
		t.Fatalf("%+v %+v", prompts, out)
	}
	// plain reword asks for its message; given ones ask nothing.
	_, prompts = Prepare(cs(), []Step{st("a", Pick, ""), st("b", Reword, ""), st("c", Pick, ""), st("d", Pick, "")})
	if len(prompts) != 1 || prompts[0].Combine || prompts[0].Index != 1 || prompts[0].Initial != "B" {
		t.Fatalf("%+v", prompts)
	}
	_, prompts = Prepare(cs(), []Step{st("a", Pick, ""), st("b", Reword, "x"), st("c", Squash, "y"), st("d", Pick, "")})
	if len(prompts) != 0 {
		t.Fatalf("%+v", prompts)
	}
	if got := StripComments("  Title\n\nbody\n# ignored\n"); got != "Title\n\nbody" {
		t.Fatalf("StripComments = %q", got)
	}
}
