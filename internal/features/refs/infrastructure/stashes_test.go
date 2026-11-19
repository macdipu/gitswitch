package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// stashRepo is a real repo with one commit (a.txt, b.txt) in a throwaway
// home, and helpers to run git and write files.
type stashRepo struct {
	t       *testing.T
	dir, gd string
	g       *Git
	run     func(args ...string) string
	write   func(name, text string)
}

func newStashRepo(t *testing.T) *stashRepo {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &stashRepo{t: t, dir: dir, gd: filepath.Join(dir, ".git"), g: &Git{Git: &gitinfra.Runner{}}}
	r.run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Me", "-c", "user.email=me@x.example", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	r.write = func(name, text string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.run("init", "-q", "-b", "main")
	r.write("a.txt", "one\ntwo\n")
	r.write("b.txt", "bee\n")
	r.run("add", ".")
	r.run("commit", "-q", "-m", "first")
	return r
}

func (r *stashRepo) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

func fileOf(s refs.Stash, path string) (refs.StashFile, bool) {
	for _, f := range s.Files {
		if f.Path == path {
			return f, true
		}
	}
	return refs.StashFile{}, false
}

func TestStashPushAndList(t *testing.T) {
	r := newStashRepo(t)
	// Staged + unstaged + untracked, and one file changed both ways.
	r.write("a.txt", "one\ntwo\nthree\n")
	r.run("add", "a.txt")
	r.write("a.txt", "one\ntwo\nthree\nfour\n")
	r.write("b.txt", "bee\nbuzz\n")
	r.write("new dir/new file.txt", "x\ny\n")

	h, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "my work", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.run("status", "--porcelain"); got != "" {
		t.Errorf("folder not clean after stash: %q", got)
	}
	all, err := r.g.Stashes(r.gd)
	if err != nil || len(all) != 1 {
		t.Fatalf("Stashes = %+v %v", all, err)
	}
	s := all[0]
	if s.Hash != h || s.Ref != "stash@{0}" || s.Index != 0 || s.Branch != "main" || s.Message != "my work" || s.Kind != refs.StashPlain || s.Time == 0 {
		t.Errorf("stash = %+v", s)
	}
	if s.Untracked != 1 || len(s.Files) != 3 {
		t.Fatalf("files = %+v", s.Files)
	}
	a, _ := fileOf(s, "a.txt")
	if a.Status != "M" || !a.Staged || a.Added != 2 || a.Deleted != 0 {
		t.Errorf("a.txt = %+v", a)
	}
	b, _ := fileOf(s, "b.txt")
	if b.Staged || b.Added != 1 {
		t.Errorf("b.txt = %+v", b)
	}
	n, ok := fileOf(s, "new dir/new file.txt")
	if !ok || !n.Untracked || n.Status != "?" || n.Added != 2 {
		t.Errorf("new file = %+v", n)
	}

	// Diffs: tracked, and the untracked file's content from ^3.
	d, err := r.g.StashDiff(r.gd, h, "a.txt")
	if err != nil || d.Untracked || !strings.Contains(d.Text, "+four") {
		t.Errorf("diff a.txt = %+v %v", d, err)
	}
	d, err = r.g.StashDiff(r.gd, h, "new dir/new file.txt")
	if err != nil || !d.Untracked || !strings.Contains(d.Text, "+x") || !strings.Contains(d.Text, "+y") {
		t.Errorf("diff new = %+v %v", d, err)
	}
	if _, err := r.g.StashDiff(r.gd, h, "nope.txt"); err == nil {
		t.Error("diff of a file not in the stash should fail")
	}
}

func TestStashKinds(t *testing.T) {
	r := newStashRepo(t)
	r.write("a.txt", "p\n")
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{Message: refs.ParkMessage("feature")}); err != nil {
		t.Fatal(err)
	}
	r.write("a.txt", "d\n")
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "gitswitch discard: main"}); err != nil {
		t.Fatal(err)
	}
	r.write("a.txt", "plain\n")
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{}); err != nil { // git names it
		t.Fatal(err)
	}
	all, _ := r.g.Stashes(r.gd)
	if len(all) != 3 || all[0].Kind != "stash" || all[1].Kind != "discard" || all[1].ParkedOn != "main" || all[2].Kind != "park" || all[2].ParkedOn != "feature" {
		t.Fatalf("kinds = %+v", all)
	}
	if !strings.HasPrefix(all[0].Message, "") || !strings.Contains(all[0].Message, "first") || all[0].Branch != "main" {
		t.Errorf("default message = %+v", all[0])
	}
	// Parked works the same as before.
	if p, _ := r.g.Parked(r.gd); len(p) != 1 || p[0].Branch != "feature" {
		t.Errorf("Parked = %+v", p)
	}
}

func TestStashNothingAndPaths(t *testing.T) {
	r := newStashRepo(t)
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{Untracked: true}); err == nil || !strings.Contains(err.Error(), "nothing to stash") {
		t.Errorf("nothing: %v", err)
	}
	r.write("a.txt", "A\n")
	r.write("b.txt", "B\n")
	r.write("u1.txt", "1\n")
	r.write("u2.txt", "2\n")
	// Only a.txt and u1.txt (a new file), literal paths.
	h, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "some", Paths: []string{"a.txt", "u1.txt"}, Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	if st := r.run("status", "--porcelain"); !strings.Contains(st, "b.txt") || !strings.Contains(st, "u2.txt") || strings.Contains(st, "a.txt") || strings.Contains(st, "u1.txt") {
		t.Errorf("status after path stash:\n%s", st)
	}
	all, _ := r.g.Stashes(r.gd)
	if len(all) != 1 || all[0].Hash != h || len(all[0].Files) != 2 || all[0].Untracked != 1 {
		t.Errorf("stash = %+v", all)
	}
	// Without untracked files, only new files: nothing stashed.
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{Paths: []string{"u2.txt"}}); err == nil {
		t.Error("an untracked path without -u should stash nothing")
	}
	// A glob is not expanded: it is a literal name.
	r.write("g1.txt", "g\n")
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{Paths: []string{"g*.txt"}, Untracked: true}); err == nil {
		t.Error("glob should not match g1.txt")
	}
}

func TestStashKeepIndex(t *testing.T) {
	r := newStashRepo(t)
	r.write("a.txt", "staged\n")
	r.run("add", "a.txt")
	r.write("b.txt", "unstaged\n")
	if _, err := r.g.StashPush(r.dir, refs.StashOptions{KeepIndex: true}); err != nil {
		t.Fatal(err)
	}
	if st := r.run("status", "--porcelain"); st != "M  a.txt" {
		t.Errorf("status = %q", st)
	}
}

func TestStashApplyClashLeavesEverything(t *testing.T) {
	r := newStashRepo(t)
	r.write("a.txt", "one\nSTASHED\n")
	r.write("n.txt", "new\n")
	h, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "clash", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	// Another commit changes the same line.
	r.write("a.txt", "one\nCOMMITTED\n")
	r.run("commit", "-qam", "other")
	files, err := r.g.Apply(r.dir, h)
	if err == nil && len(files) == 0 {
		t.Fatal("expected a clash")
	}
	if len(files) > 0 {
		if err := r.g.Discard(r.dir, h); err != nil {
			t.Fatal(err)
		}
	}
	if st := r.run("status", "--porcelain"); st != "" {
		t.Errorf("folder not as before: %q", st)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "n.txt")); !os.IsNotExist(err) {
		t.Errorf("untracked file left behind: %v", err)
	}
	if all, _ := r.g.Stashes(r.gd); len(all) != 1 || all[0].Hash != h {
		t.Errorf("stash must stay: %+v", all)
	}
	if r.read("a.txt") != "one\nCOMMITTED\n" {
		t.Errorf("a.txt = %q", r.read("a.txt"))
	}
}

func TestStashDropAndStore(t *testing.T) {
	r := newStashRepo(t)
	r.write("a.txt", "x\n")
	h, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "keep me"})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := r.g.Stashes(r.gd)
	subject := all[0].Subject
	if err := r.g.DropStash(r.dir, h); err != nil {
		t.Fatal(err)
	}
	if all, _ := r.g.Stashes(r.gd); len(all) != 0 {
		t.Fatalf("dropped: %+v", all)
	}
	r.run("stash", "store", "-m", subject, h)
	all, _ = r.g.Stashes(r.gd)
	if len(all) != 1 || all[0].Hash != h || all[0].Message != "keep me" || all[0].Branch != "main" {
		t.Errorf("stored back: %+v", all)
	}
}

func TestStashBranch(t *testing.T) {
	r := newStashRepo(t)
	r.write("a.txt", "one\ntwo\nbranch\n")
	r.write("n.txt", "new\n")
	h, err := r.g.StashPush(r.dir, refs.StashOptions{Message: "to a branch", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	// Move main on, so the stash's base is an older commit.
	r.write("b.txt", "later\n")
	r.run("commit", "-qam", "later")
	if err := r.g.StashBranch(r.dir, h, "from-stash"); err != nil {
		t.Fatal(err)
	}
	if cur := r.run("symbolic-ref", "--short", "HEAD"); cur != "from-stash" {
		t.Errorf("on %s", cur)
	}
	if r.read("a.txt") != "one\ntwo\nbranch\n" || r.read("n.txt") != "new\n" {
		t.Errorf("changes missing: %q %q", r.read("a.txt"), r.read("n.txt"))
	}
	if all, _ := r.g.Stashes(r.gd); len(all) != 0 {
		t.Errorf("stash not dropped: %+v", all)
	}
	// A stale hash is refused.
	if err := r.g.StashBranch(r.dir, h, "again"); err != refs.ErrStashChanged {
		t.Errorf("stale: %v", err)
	}
}
