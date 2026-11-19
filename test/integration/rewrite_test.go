package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Interactive rebase, reword and fixup-into from the command line with
// real git: --todo and --messages without a terminal, the plan, refusals
// for pushed commits, a clash that stops and is finished with continue,
// and undo after each.
func TestRewriteCommands(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	pushed := e.commitFile(api, "p.txt", "p\n", "pushed")
	e.gitIn(api, "push", "-q", "origin", "main")
	a := e.commitFile(api, "a.txt", "a\n", "add a")
	b := e.commitFile(api, "b.txt", "b\n", "add b\n\nbody b")
	c := e.commitFile(api, "c.txt", "c\n", "add c")
	head := func() string { return e.gitIn(api, "rev-parse", "HEAD") }
	short := func(h string) string { return h[:7] }
	write := func(name, text string) {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(e.root, name), []byte(text), 0o644))
	}

	// Not in a terminal: the todo must come from a file; a reword needs its message.
	e.gsErr(api, "--todo", "rebase", "-i", "-y")
	todo := filepath.Join(e.root, "todo.txt")
	write("todo.txt", "# reorder, reword the first, squash the last into it\npick "+short(b)+" add b\nreword "+short(a)+" add a\nsquash "+short(c)+" add c\n")
	write("rw.txt", "pick "+short(b)+"\nreword "+short(a)+"\npick "+short(c)+"\n")
	e.gsErr(api, "reword "+short(a)+" needs a message", "rebase", "-i", "--todo", filepath.Join(e.root, "rw.txt"), "-y")
	if head() != c {
		t.Fatal("a refused rewrite moved the branch")
	}
	write("msgs.json", `{"`+short(a)+`": "Add a (reworded)"}`)
	out := e.gsIn(api, "rebase", "-i", "--todo", todo, "--messages", filepath.Join(e.root, "msgs.json"), "-y")
	for _, want := range []string{"Before (3", "After (2", "reworded", "3 commits become 2", "gitswitch undo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if got := e.subjects(api, "HEAD", 3); got != "Add a (reworded),add b,pushed" {
		t.Fatalf("subjects = %q", got)
	}
	// reword + squash with no squash message: the messages are joined.
	if body := e.gitIn(api, "log", "-1", "--format=%B"); body != "Add a (reworded)\n\nadd c" {
		t.Fatalf("joined = %q", body)
	}
	if e.gitIn(api, "status", "--porcelain") != "" {
		t.Fatal("dirty after rewrite")
	}
	e.gsIn(api, "undo", "-y")
	if head() != c {
		t.Fatal("undo did not bring the old tip back")
	}

	// --plan shows, changes nothing; an unchanged todo changes nothing.
	out = e.gsIn(api, "rebase", "-i", "--todo", todo, "--messages", filepath.Join(e.root, "msgs.json"), "--plan")
	if !strings.Contains(out, "After (2") || head() != c {
		t.Fatalf("plan:\n%s", out)
	}
	write("same.txt", "pick "+short(a)+"\npick "+short(b)+"\npick "+short(c)+"\n")
	if out := e.gsIn(api, "rebase", "-i", "--todo", filepath.Join(e.root, "same.txt"), "--plan"); !strings.Contains(out, "Nothing would change") {
		t.Fatalf("same:\n%s", out)
	}
	// an empty todo cancels; a bad one is refused; drop and fixup work.
	write("empty.txt", "# nothing\n")
	if out := e.gsIn(api, "rebase", "-i", "--todo", filepath.Join(e.root, "empty.txt"), "-y"); !strings.Contains(out, "Cancelled") {
		t.Fatalf("cancel:\n%s", out)
	}
	write("bad.txt", "pick "+short(a)+"\npick "+short(b)+"\n")
	e.gsErr(api, "is missing", "rebase", "-i", "--todo", filepath.Join(e.root, "bad.txt"), "-y")
	write("df.txt", "pick "+short(a)+"\nfixup "+short(b)+"\ndrop "+short(c)+"\n")
	e.gsIn(api, "rebase", "-i", "--todo", filepath.Join(e.root, "df.txt"), "-y")
	if got := e.subjects(api, "HEAD", 2); got != "add a,pushed" || e.gitIn(api, "show", "HEAD:b.txt") != "b" {
		t.Fatalf("drop+fixup: %q", got)
	}
	e.gsIn(api, "undo", "-y")

	// reword an older commit; the pushed one is refused.
	e.gsErr(api, "already pushed", "reword", short(pushed), "-m", "nope", "-y")
	e.gsErr(api, "-m", "reword", short(b), "-y") // no terminal: needs -m
	e.gsIn(api, "reword", short(b), "-m", "Add b, better", "-y")
	if got := e.subjects(api, "HEAD", 3); got != "add c,Add b, better,add a" {
		t.Fatalf("reword = %q", got)
	}
	e.gsIn(api, "undo", "-y")
	if head() != c {
		t.Fatal("undo after reword")
	}

	// fixup-into: stage a change, fold it into "add a".
	e.gsErr(api, "nothing is staged", "fixup-into", short(a), "-y")
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("a fixed\n"), 0o644))
	e.gitIn(api, "add", "a.txt")
	out = e.gsIn(api, "fixup-into", short(a), "-y")
	if !strings.Contains(out, "part of") || e.gitIn(api, "show", "HEAD~2:a.txt") != "a fixed" || e.subjects(api, "HEAD", 3) != "add c,add b,add a" {
		t.Fatalf("fixup-into:\n%s\n%s", out, e.subjects(api, "HEAD", 3))
	}
	e.gsIn(api, "undo", "-y")
	if head() != c || e.gitIn(api, "status", "--porcelain") != "M a.txt" {
		t.Fatalf("undo fixup-into: %s", e.gitIn(api, "status", "--porcelain"))
	}
	e.gitIn(api, "checkout", "--", "a.txt")

	// a clash stops (exit status != 0), conflict + continue finish it, undo restores.
	x := e.commitFile(api, "f.txt", "1\n", "f one")
	y := e.commitFile(api, "f.txt", "1\n2\n", "f two")
	write("swap.txt", "pick "+short(y)+"\npick "+short(x)+"\n")
	e.gsErr(api, "the rewrite stopped", "rebase", "-i", "--since", short(x), "--todo", filepath.Join(e.root, "swap.txt"), "-y")
	if out := e.gsIn(api, "conflict"); !strings.Contains(out, "f.txt") {
		t.Fatalf("conflict:\n%s", out)
	}
	for i := 0; i < 3 && e.gitIn(api, "status", "--porcelain=v2", "--branch") != "" && fileExists(filepath.Join(api, ".git", "rebase-merge")); i++ {
		must(t, os.WriteFile(filepath.Join(api, "f.txt"), []byte("1\n2\n"), 0o644))
		e.gsIn(api, "conflict", "f.txt", "--done")
		e.gsMay(api, "continue")
	}
	if fileExists(filepath.Join(api, ".git", "rebase-merge")) {
		t.Fatal("rebase still in progress")
	}
	e.gsIn(api, "undo", "-y")
	if head() != y {
		t.Fatalf("undo after a stopped rewrite: HEAD %s, want %s", head(), y)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// gsMay runs gitswitch and ignores a failure (a continue that stops again).
func (e *env) gsMay(dir string, args ...string) { _, _ = e.run(dir, gsBin, args...) }
