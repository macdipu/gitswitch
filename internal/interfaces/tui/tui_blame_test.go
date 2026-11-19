package tui

import (
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

func TestBlameAcrossRenameAndWalkBack(t *testing.T) {
	x, dir, git, commit := inspectRepo(t)
	commit("other.txt", "o\n", "init")
	c1 := commit("old.txt", "alpha\nbeta\n", "add old")
	git("mv", "old.txt", "new.txt")
	git("commit", "-q", "-m", "rename")
	c3 := commit("new.txt", "alpha\nbeta\ngamma\n", "add gamma")
	showRepo(t, x, dir)

	x.openBlame("new.txt", "", false)
	v := x.insp.bl
	if x.insp.kind != "blame" || v == nil || len(v.b.Lines) != 3 {
		t.Fatalf("blame not open (msg %q)", x.msg)
	}
	if v.b.Lines[0].Hash != c1 || v.b.Lines[0].OrigPath != "old.txt" || v.b.Lines[2].Hash != c3 {
		t.Fatalf("blame lines: %+v", v.b.Lines)
	}
	scr := page(x)
	for _, w := range []string{"Blame", "new.txt", c1[:7], c3[:7], "alpha", "gamma", "T"} {
		if !strings.Contains(scr, w) {
			t.Errorf("screen lacks %q:\n%s", w, scr)
		}
	}
	// repeated hashes are blanked: c1 appears once for its two lines
	if n := strings.Count(scr, c1[:7]); n != 1 {
		t.Errorf("hash %s shown %d times, want once:\n%s", c1[:7], n, scr)
	}
	// Enter shows the line's commit
	x.handle("G")
	x.handle("enter")
	if x.view == nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "add gamma") {
		t.Fatalf("Enter did not show the commit: %+v", x.view)
	}
	x.handle("esc")
	// b: before that commit, two lines; Backspace comes back
	x.handle("b")
	if len(v.b.Lines) != 2 || len(v.stack) != 1 || !strings.Contains(page(x), "just before") {
		t.Fatalf("blame before: %d lines, stack %d\n%s", len(v.b.Lines), len(v.stack), page(x))
	}
	x.handle("backspace")
	if len(v.b.Lines) != 3 || len(v.stack) != 0 {
		t.Fatalf("Backspace: %d lines, stack %d", len(v.b.Lines), len(v.stack))
	}
	// a line from before the file existed: says plainly, stays put
	x.handle("g")
	x.handle("b")
	if !x.msgErr || len(v.stack) != 0 || len(v.b.Lines) != 3 {
		t.Fatalf("blame before the first commit: msg %q err %v", x.msg, x.msgErr)
	}
	x.handle("esc")
	if x.inspecting() {
		t.Fatal("Esc should close")
	}
}

func TestBlameToggles(t *testing.T) {
	x, dir, _, commit := inspectRepo(t)
	c1 := commit("ws.txt", "a\nb\n", "first")
	c2 := commit("ws.txt", "a  \nb\n", "trailing space")
	showRepo(t, x, dir)
	x.openBlame("ws.txt", "", false)
	if x.insp.bl.b.Lines[0].Hash != c2 {
		t.Fatalf("line 1 from %s, want %s", x.insp.bl.b.Lines[0].Hash, c2)
	}
	x.handle("w")
	if !x.insp.bl.opt.IgnoreWhitespace || x.insp.bl.b.Lines[0].Hash != c1 || !strings.Contains(page(x), "ignoring whitespace") {
		t.Fatalf("w did not ignore whitespace:\n%s", page(x))
	}
	x.handle("m")
	if !x.insp.bl.opt.Moves || !strings.Contains(page(x), "moved lines") {
		t.Fatalf("m did not follow moves:\n%s", page(x))
	}
}

func TestBlameErrorsAndCap(t *testing.T) {
	x, dir, _, commit := inspectRepo(t)
	commit("bin.dat", "a\x00b\x00c\n", "binary")
	commit("big.txt", strings.Repeat("line\n", history.MaxBlameLines+3), "big")
	showRepo(t, x, dir)
	x.openBlame("bin.dat", "", false)
	if x.inspecting() || !x.msgErr || !strings.Contains(x.msg, "binary") {
		t.Fatalf("binary: msg %q err %v", x.msg, x.msgErr)
	}
	x.openBlame("nope.txt", "", false)
	if x.inspecting() || !x.msgErr || x.msg == "" {
		t.Fatalf("missing file: msg %q err %v", x.msg, x.msgErr)
	}
	x.openBlame("big.txt", "", false)
	if !x.insp.bl.b.Truncated || !strings.Contains(page(x), "first 5000 lines") {
		t.Fatal("the 5000-line cap is not shown")
	}
}

func TestFileHistoryFollowsRenamesAndPages(t *testing.T) {
	old := fileLogPage
	fileLogPage = 2
	t.Cleanup(func() { fileLogPage = old })
	x, dir, git, commit := inspectRepo(t)
	commit("f.txt", "1\n", "create")
	commit("f.txt", "1\n2\n", "second line")
	git("mv", "f.txt", "g.txt")
	git("commit", "-q", "-m", "rename to g")
	commit("g.txt", "1\n2\n3\n", "third line")
	showRepo(t, x, dir)

	x.handle("I")
	x.handle("down")
	x.handle("enter") // File history… (the selected commit's files are offered)
	if x.picker == nil {
		t.Fatalf("no file list (form %v)", x.form != nil)
	}
	x.handle("enter") // Type a path…
	if x.form == nil {
		t.Fatal("no path form")
	}
	typeKeys(x, "g.txt")
	x.handle("enter")
	v := x.insp.fl
	if x.insp.kind != "filelog" || v == nil || len(v.commits) != 2 || !v.more {
		t.Fatalf("file history: kind %q msg %q", x.insp.kind, x.msg)
	}
	if v.commits[0].Subject != "third line" || v.commits[1].Status != "R" || v.commits[1].OldPath != "f.txt" {
		t.Fatalf("page 1: %+v", v.commits)
	}
	if scr := page(x); !strings.Contains(scr, "renamed from f.txt") || !strings.Contains(scr, "n older page") {
		t.Fatalf("screen:\n%s", scr)
	}
	x.handle("n")
	if len(v.commits) != 2 || v.commits[0].Subject != "second line" || v.commits[1].Subject != "create" || v.more || v.skip != 2 {
		t.Fatalf("page 2: %+v skip %d more %v", v.commits, v.skip, v.more)
	}
	x.handle("n")
	if !strings.Contains(x.msg, "first commit") {
		t.Fatalf("msg %q", x.msg)
	}
	// the diff of the file in that commit, under its name then (f.txt)
	x.handle("enter")
	if x.view == nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "+2") || !strings.Contains(x.view.title, "f.txt") {
		t.Fatalf("diff view: %+v", x.view)
	}
	x.handle("esc")
	// blame at that commit, then Esc goes back to the history
	x.handle("b")
	if x.insp.kind != "blame" || x.insp.bl.path != "f.txt" || len(x.insp.bl.b.Lines) != 2 {
		t.Fatalf("blame at commit: kind %q msg %q", x.insp.kind, x.msg)
	}
	x.handle("esc")
	if x.insp.kind != "filelog" {
		t.Fatalf("Esc went to %q, want the file history", x.insp.kind)
	}
	x.handle("p")
	if v.skip != 0 || v.commits[0].Subject != "third line" {
		t.Fatalf("p: skip %d %+v", v.skip, v.commits)
	}
	// a path nothing touched
	x.handle("esc")
	x.openFileLog("missing.txt")
	if x.inspecting() || !x.msgErr {
		t.Fatalf("missing path: msg %q", x.msg)
	}
}
