package infrastructure

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

type tmpRepo struct {
	t   *testing.T
	dir string
}

func newTmpRepo(t *testing.T) *tmpRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	r := &tmpRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *tmpRepo) git(args ...string) {
	r.t.Helper()
	r.as("A", "a@x", args...)
}

func (r *tmpRepo) as(name, email string, args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(r.dir, "gc"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *tmpRepo) write(path, text string) {
	r.t.Helper()
	p := filepath.Join(r.dir, path)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *tmpRepo) commit(name, email, msg string) {
	r.as(name, email, "add", "-A")
	r.as(name, email, "commit", "-q", "-m", msg)
}

func (r *tmpRepo) gd() string { return filepath.Join(r.dir, ".git") }

func logFor() *Log { return &Log{Git: &gitinfra.Runner{}} }

func TestBlameAcrossRename(t *testing.T) {
	r := newTmpRepo(t)
	r.write("old.txt", "alpha\nbeta\ngamma\n")
	r.commit("Ana", "ana@x.example", "create old")
	r.as("A", "a@x", "mv", "old.txt", "dir-new.txt")
	r.commit("Ana", "ana@x.example", "rename")
	r.write("dir-new.txt", "alpha\nBETA\ngamma\ndelta\r\n")
	r.commit("Bob", "bob@x.example", "edit")

	b, err := logFor().Blame(r.gd(), "", "dir-new.txt", history.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Lines) != 4 || b.Truncated {
		t.Fatalf("%+v", b.Lines)
	}
	if l := b.Lines[0]; l.Author != "Ana" || l.Email != "ana@x.example" || l.OrigPath != "old.txt" || l.Summary != "create old" || !l.Boundary || l.Text != "alpha" {
		t.Errorf("line 1: %+v", l)
	}
	if l := b.Lines[1]; l.Author != "Bob" || l.OrigPath != "dir-new.txt" || l.Boundary || l.Text != "BETA" {
		t.Errorf("line 2: %+v", l)
	}
	if l := b.Lines[3]; l.Text != "delta" || l.Author != "Bob" {
		t.Errorf("CRLF line: %+v", l)
	}
	// An older revision, a rev that is no ref, a missing file and a folder.
	if b, err := logFor().Blame(r.gd(), "HEAD~1", "dir-new.txt", history.BlameOptions{}); err != nil || b.Lines[1].Text != "beta" {
		t.Errorf("at HEAD~1: %v %+v", err, b.Lines)
	}
	if _, err := logFor().Blame(r.gd(), "HEAD", "nope.txt", history.BlameOptions{}); err == nil || !strings.Contains(err.Error(), "no file") {
		t.Errorf("missing file: %v", err)
	}
	if _, err := logFor().Blame(r.gd(), "nobranch", "old.txt", history.BlameOptions{}); err == nil {
		t.Error("unknown rev must fail")
	}
}

func TestBlameOptionsAndLiteralPath(t *testing.T) {
	r := newTmpRepo(t)
	r.write("f.txt", "one\ntwo\n")
	r.commit("Ana", "ana@x.example", "create")
	r.write("f.txt", "one\n  two\n") // whitespace only
	r.commit("Bob", "bob@x.example", "indent")
	b, _ := logFor().Blame(r.gd(), "HEAD", "f.txt", history.BlameOptions{})
	if b.Lines[1].Author != "Bob" {
		t.Errorf("without -w: %+v", b.Lines[1])
	}
	b, _ = logFor().Blame(r.gd(), "HEAD", "f.txt", history.BlameOptions{IgnoreWhitespace: true})
	if b.Lines[1].Author != "Ana" {
		t.Errorf("with -w: %+v", b.Lines[1])
	}
	// Lines moved from another file are followed with -M -C.
	r.write("g.txt", "moved line number one of it\nmoved line number two of it\nmoved line number three of it\n")
	r.write("h.txt", "x\n")
	r.commit("Ana", "ana@x.example", "add g")
	r.write("g.txt", "")
	r.write("h.txt", "moved line number one of it\nmoved line number two of it\nmoved line number three of it\n")
	r.commit("Bob", "bob@x.example", "move")
	b, err := logFor().Blame(r.gd(), "HEAD", "h.txt", history.BlameOptions{Moves: true})
	if err != nil {
		t.Fatal(err)
	}
	if b.Lines[0].Author != "Ana" || b.Lines[0].OrigPath != "g.txt" {
		t.Errorf("with -M -C: %+v", b.Lines[0])
	}
	// A path is literal: a glob is not expanded.
	if _, err := logFor().Blame(r.gd(), "HEAD", "*.txt", history.BlameOptions{}); err == nil {
		t.Error("a glob is a file name, and there is none")
	}
}

func TestBlameBinaryAndTruncation(t *testing.T) {
	r := newTmpRepo(t)
	r.write("bin.dat", "abc\x00def\n")
	var big strings.Builder
	for i := 0; i < history.MaxBlameLines+25; i++ {
		big.WriteString("line\n")
	}
	r.write("big.txt", big.String())
	r.write("empty.txt", "")
	r.commit("Ana", "ana@x.example", "files")
	if _, err := logFor().Blame(r.gd(), "HEAD", "bin.dat", history.BlameOptions{}); !errors.Is(err, history.ErrBinaryFile) {
		t.Errorf("binary: %v", err)
	}
	b, err := logFor().Blame(r.gd(), "HEAD", "big.txt", history.BlameOptions{})
	if err != nil || !b.Truncated || len(b.Lines) != history.MaxBlameLines {
		t.Fatalf("big: %v truncated=%v n=%d", err, b.Truncated, len(b.Lines))
	}
	if b, err := logFor().Blame(r.gd(), "HEAD", "empty.txt", history.BlameOptions{}); err != nil || len(b.Lines) != 0 {
		t.Errorf("empty: %v %+v", err, b.Lines)
	}
}

func TestFileHistoryFollowsRenames(t *testing.T) {
	r := newTmpRepo(t)
	body := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\n"
	r.write("a.txt", body)
	r.write("other.txt", "x\n")
	r.commit("Ana", "ana@x.example", "create a")
	r.write("other.txt", "y\n")
	r.commit("Ana", "ana@x.example", "unrelated")
	r.write("sub/keep", "")
	r.as("A", "a@x", "mv", "a.txt", "sub/b.txt")
	r.commit("Ana", "ana@x.example", "move a to b")
	r.write("sub/b.txt", body+"line 7\n")
	r.commit("Bob", "bob@x.example", "extend b")

	cs, more, err := logFor().FileHistory(r.gd(), "sub/b.txt", 10, 0)
	if err != nil || more {
		t.Fatalf("%v more=%v", err, more)
	}
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
	if c := cs[0]; c.Subject != "extend b" || c.Status != "M" || c.Path != "sub/b.txt" || c.Email != "bob@x.example" {
		t.Errorf("%+v", c)
	}
	if c := cs[1]; c.Status != "R" || c.Similarity != 100 || c.OldPath != "a.txt" || c.Path != "sub/b.txt" {
		t.Errorf("%+v", c)
	}
	if c := cs[2]; c.Status != "A" || c.Path != "a.txt" || c.Subject != "create a" {
		t.Errorf("%+v", c)
	}
	// Paging with n and skip: the name is still followed across the rename.
	p1, more, _ := logFor().FileHistory(r.gd(), "sub/b.txt", 2, 0)
	p2, more2, _ := logFor().FileHistory(r.gd(), "sub/b.txt", 2, 2)
	if len(p1) != 2 || !more || len(p2) != 1 || more2 || p2[0].Path != "a.txt" {
		t.Errorf("pages: %+v %v / %+v %v", p1, more, p2, more2)
	}
	if cs, _, err := logFor().FileHistory(r.gd(), "never.txt", 10, 0); err != nil || len(cs) != 0 {
		t.Errorf("unknown file: %v %+v", err, cs)
	}
}

func TestPickaxe(t *testing.T) {
	r := newTmpRepo(t)
	r.write("f.txt", "start\n")
	r.commit("Ana", "ana@x.example", "one")
	r.write("f.txt", "start\nneedle_42 here\n")
	r.commit("Ana", "ana@x.example", "adds needle")
	r.write("f.txt", "start\n")
	r.commit("Ana", "ana@x.example", "removes needle")
	r.write("f.txt", "start\nNEEDLE_7\n")
	r.commit("Ana", "ana@x.example", "adds upper")

	subjects := func(f history.Filter) string {
		t.Helper()
		cs, err := logFor().Commits(r.gd(), 50, f)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, c := range cs {
			s = append(s, c.Subject)
		}
		return strings.Join(s, ",")
	}
	if got := subjects(history.Filter{Pickaxe: "needle_42"}); got != "removes needle,adds needle" {
		t.Errorf("-S: %q", got)
	}
	if got := subjects(history.Filter{Pickaxe: "needle_[0-9]+", PickaxeRegex: true}); got != "removes needle,adds needle" {
		t.Errorf("-G: %q", got)
	}
	if got := subjects(history.Filter{Pickaxe: "needle_[0-9]+", PickaxeRegex: true, PickaxeIgnoreCase: true}); got != "adds upper,removes needle,adds needle" {
		t.Errorf("-G -i: %q", got)
	}
	// -S is literal: no regex meaning, and a leading dash is no option.
	if got := subjects(history.Filter{Pickaxe: "needle_[0-9]+"}); got != "" {
		t.Errorf("-S is not a regex: %q", got)
	}
	if got := subjects(history.Filter{Pickaxe: "--all"}); got != "" {
		t.Errorf("leading dashes: %q", got)
	}
	if _, err := logFor().Commits(r.gd(), 50, history.Filter{Pickaxe: "(", PickaxeRegex: true}); err == nil {
		t.Error("a broken regex must fail with git's message")
	}
}
