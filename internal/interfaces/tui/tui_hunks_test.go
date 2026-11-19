package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hunksRepo has h.txt (twelve lines) with l2 and l11 changed, so it has two
// hunks, with the changes view open on it.
func hunksRepo(t *testing.T) (*tui, string, func(...string) string, func(string, string)) {
	x, dir, git, write := mergeRepo(t)
	write("h.txt", twelve)
	git("add", ".")
	git("commit", "-q", "-m", "h")
	write("h.txt", strings.NewReplacer("l2\n", "new2\n", "l11\n", "new11\n").Replace(twelve))
	x.handle("s")
	selectPath(t, x, "h.txt", "changed")
	x.handle("H")
	if x.hv == nil {
		t.Fatalf("H did not open the hunks (msg %q)", x.msg)
	}
	return x, dir, git, write
}

// moveTo puts the cursor on the line with that text.
func moveTo(t *testing.T, x *tui, text string) {
	t.Helper()
	v := x.hv
	for i, r := range v.rows {
		if r.l >= 0 && v.d.Hunks[r.h].Lines[r.l].Text == text && v.d.Hunks[r.h].Lines[r.l].Changed() {
			v.cur = i
			return
		}
	}
	t.Fatalf("no changed line %q in the hunks", text)
}

func TestHunksListAndStage(t *testing.T) {
	x, _, git, _ := hunksRepo(t)
	lines, _ := x.drawHunks()
	got := plain(lines)
	for _, want := range []string{"Hunks  h.txt", "not staged", "Hunk 1", "Hunk 2", "-l2", "+new2", "-l11", "+new11"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
	x.handle("u") // not staged: refused
	if !x.msgErr || !strings.Contains(x.msg, "not staged") {
		t.Fatalf("u on an unstaged change: msg %q", x.msg)
	}
	x.handle("s") // the cursor is on hunk 1
	if d := git("diff", "--cached"); !strings.Contains(d, "+new2") || strings.Contains(d, "new11") || x.msgErr {
		t.Fatalf("hunk 1 not staged alone (msg %q):\n%s", x.msg, d)
	}
	if got := plain(func() []string { l, _ := x.drawHunks(); return l }()); strings.Contains(got, "new2") || !strings.Contains(got, "+new11") {
		t.Fatalf("the view did not follow:\n%s", got)
	}
	// back in the changes, the staged part has its own row: unstage it there
	x.handle("esc")
	selectPath(t, x, "h.txt", "staged")
	x.handle("H")
	if x.hv == nil || !x.hv.staged {
		t.Fatalf("staged hunks did not open (msg %q)", x.msg)
	}
	x.handle("s")
	if !x.msgErr || !strings.Contains(x.msg, "already staged") {
		t.Fatalf("s on a staged change: msg %q", x.msg)
	}
	x.handle("u")
	if d := git("diff", "--cached"); d != "" || x.hv != nil {
		t.Fatalf("not unstaged (msg %q):\n%s", x.msg, d)
	}
}

// Single lines: Space picks, s stages only those.
func TestHunksPickLines(t *testing.T) {
	x, _, git, write := hunksRepo(t)
	write("h.txt", strings.NewReplacer("l5\n", "n5\n", "l6\n", "n6\n").Replace(twelve))
	x.handle("r")
	moveTo(t, x, "n5")
	x.handle(" ")
	moveTo(t, x, "l5")
	x.handle(" ")
	if len(x.hv.picked) != 2 {
		t.Fatalf("picked %v", x.hv.picked)
	}
	if got := plain(func() []string { l, _ := x.drawHunks(); return l }()); !strings.Contains(got, "2 line(s) picked") || !strings.Contains(got, "[x] +n5") {
		t.Fatalf("picks not shown:\n%s", got)
	}
	x.handle("s")
	d := git("diff", "--cached")
	if !strings.Contains(d, "+n5") || !strings.Contains(d, "-l5") || strings.Contains(d, "n6") || strings.Contains(d, "-l6") {
		t.Fatalf("only the picked lines should be staged (msg %q):\n%s", x.msg, d)
	}
	// a context line cannot be picked
	x.hv.cur = 1
	x.handle(" ")
	if !x.msgErr || !strings.Contains(x.msg, "context") {
		t.Fatalf("picking context: msg %q", x.msg)
	}
}

// Discard asks, says the change is kept in git stash and how to restore it.
func TestHunksDiscard(t *testing.T) {
	x, dir, git, _ := hunksRepo(t)
	x.handle("n") // hunk 2
	x.handle("x")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "hunk 2") || !strings.Contains(x.confirm.text, "git stash") || !strings.Contains(x.confirm.text, "git restore --source='stash@{0}' -- h.txt") {
		t.Fatalf("discard did not ask properly: %+v", x.confirm)
	}
	x.handle("n")
	if !strings.Contains(read(t, dir, "h.txt"), "new11") {
		t.Fatal("a cancelled discard changed the file")
	}
	x.handle("x")
	x.handle("y")
	got := read(t, dir, "h.txt")
	if strings.Contains(got, "new11") || !strings.Contains(got, "new2") || x.msgErr {
		t.Fatalf("h.txt = %q (msg %q)", got, x.msg)
	}
	if !strings.Contains(x.msg, "git stash") || !strings.Contains(git("stash", "list"), "gitswitch discard") {
		t.Fatalf("not kept in the stash (msg %q)", x.msg)
	}
}

// A file that changed after the hunks were read is refused, and the view
// is read again.
func TestHunksStaleFingerprint(t *testing.T) {
	x, dir, git, _ := hunksRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "h.txt"), []byte(strings.NewReplacer("l2\n", "other2\n").Replace(twelve)), 0o644); err != nil {
		t.Fatal(err)
	}
	x.handle("s")
	if !x.msgErr || !strings.Contains(x.msg, "the file changed since you looked: reload") {
		t.Fatalf("stale stage: msg %q", x.msg)
	}
	if d := git("diff", "--cached"); d != "" {
		t.Fatalf("staged from a stale diff:\n%s", d)
	}
	if got := plain(func() []string { l, _ := x.drawHunks(); return l }()); !strings.Contains(got, "other2") || strings.Contains(got, "new11") {
		t.Fatalf("the view was not read again:\n%s", got)
	}
}

func TestHunksRefuseConflictAndBinary(t *testing.T) {
	x, _, _, write := mergeRepo(t)
	write("bin.dat", "a\x00b\n")
	x.handle("s")
	selectPath(t, x, "bin.dat", "changed")
	x.handle("H")
	if x.hv != nil || !x.msgErr {
		t.Fatalf("hunks of a new binary file opened (msg %q)", x.msg)
	}
}
