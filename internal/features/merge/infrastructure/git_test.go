package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
)

// repo makes main and side that both change a.txt (a clash) and each
// change a file of their own.
func repo(t *testing.T) (dir string, run func(...string) string) {
	t.Helper()
	dir = t.TempDir()
	// The code under test runs git with this process's environment.
	for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "Me", "GIT_AUTHOR_EMAIL": "me@example.com", "GIT_COMMITTER_NAME": "Me", "GIT_COMMITTER_EMAIL": "me@example.com"} {
		t.Setenv(k, v)
	}
	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Me", "GIT_AUTHOR_EMAIL=me@example.com", "GIT_COMMITTER_NAME=Me", "GIT_COMMITTER_EMAIL=me@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("a.txt", "base\n")
	write("gone.txt", "will be deleted on side\n")
	run("add", "--all")
	run("commit", "-q", "-m", "base")
	run("switch", "-q", "-c", "side")
	write("a.txt", "side\n")
	write("s.txt", "side only\n")
	run("rm", "-q", "gone.txt")
	run("add", "--all")
	run("commit", "-q", "-m", "side")
	run("switch", "-q", "main")
	write("a.txt", "main\n")
	write("gone.txt", "changed on main\n")
	write("m.txt", "main only\n")
	run("add", "--all")
	run("commit", "-q", "-m", "main")
	return dir, run
}

func TestPlanReadsWithGit(t *testing.T) {
	dir, run := repo(t)
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	side, err := g.Resolve(gd, "side")
	if err != nil || side != run("rev-parse", "side") {
		t.Fatalf("Resolve = %q, %v", side, err)
	}
	if _, err := g.Resolve(gd, "nope"); err == nil {
		t.Fatal("resolved a branch that does not exist")
	}
	head := run("rev-parse", "HEAD")
	base := g.MergeBase(gd, head, side)
	if base != run("rev-parse", "HEAD~1") {
		t.Fatalf("MergeBase = %q", base)
	}
	cs, n, err := g.Commits(gd, side, head, 10)
	if err != nil || n != 1 || len(cs) != 1 || cs[0].Subject != "side" || cs[0].Author != "Me" || len(cs[0].Short) < 7 {
		t.Fatalf("Commits = %+v %d %v", cs, n, err)
	}
	if files, err := g.Changed(gd, base, side); err != nil || strings.Join(files, ",") != "a.txt,gone.txt,s.txt" {
		t.Fatalf("Changed = %v %v", files, err)
	}
	conflicts, ok := g.Predict(gd, head, side)
	if !ok || strings.Join(conflicts, ",") != "a.txt,gone.txt" {
		t.Fatalf("Predict = %v %v", conflicts, ok)
	}
	if conflicts, ok := g.Predict(gd, base, side); !ok || len(conflicts) != 0 {
		t.Fatalf("Predict clean = %v %v", conflicts, ok)
	}
	if run("status", "--porcelain") != "" {
		t.Fatal("planning touched the work tree")
	}
}

func TestStopResolveContinueWithGit(t *testing.T) {
	dir, run := repo(t)
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if err := g.Merge(dir, "side", merge.Auto); err == nil {
		t.Fatal("a clashing merge succeeded")
	}
	if op := g.Op(gd); op != merge.OpMerge {
		t.Fatalf("Op = %q", op)
	}
	if h, name := g.Merging(gd); h != run("rev-parse", "side") || name != "side" {
		t.Fatalf("Merging = %q %q", h, name)
	}
	files, err := g.Conflicted(gd)
	if err != nil || strings.Join(files, ",") != "a.txt,gone.txt" {
		t.Fatalf("Conflicted = %v %v", files, err)
	}
	for n, want := range map[int]string{1: "base\n", 2: "main\n", 3: "side\n"} {
		if b, ok, err := g.Stage(gd, "a.txt", n); err != nil || !ok || string(b) != want {
			t.Fatalf("stage %d = %q %v %v", n, b, ok, err)
		}
	}
	if _, ok, err := g.Stage(gd, "gone.txt", 3); ok || err != nil {
		t.Fatalf("gone.txt has no theirs: %v %v", ok, err)
	}
	if b, ok, _ := g.ReadFile(dir, "a.txt"); !ok || !merge.HasMarkers(string(b)) {
		t.Fatalf("working file = %q", b)
	}
	// Theirs for a.txt; theirs deleted gone.txt, so taking it deletes it.
	if err := g.Take(dir, "a.txt", merge.Theirs); err != nil {
		t.Fatal(err)
	}
	if err := g.Take(dir, "gone.txt", merge.Theirs); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := g.ReadFile(dir, "a.txt"); string(b) != "side\n" {
		t.Fatalf("a.txt = %q", b)
	}
	if _, ok, _ := g.ReadFile(dir, "gone.txt"); ok {
		t.Fatal("gone.txt is still there")
	}
	if files, _ := g.Conflicted(gd); len(files) != 0 {
		t.Fatalf("still conflicted: %v", files)
	}
	if err := g.Run(dir, merge.OpMerge, merge.Continue); err != nil {
		t.Fatal(err)
	}
	if g.Op(gd) != "" || run("log", "-1", "--format=%s") != "Merge branch 'side'" || run("status", "--porcelain") != "" {
		t.Fatalf("after continue: %q, %q", run("log", "-1", "--format=%s"), run("status", "--porcelain"))
	}
}

func TestSaveAndAbortWithGit(t *testing.T) {
	dir, run := repo(t)
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	head := run("rev-parse", "HEAD")
	_ = g.Merge(dir, "side", merge.Commit)
	if err := g.WriteFile(dir, "a.txt", []byte("both\n")); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(dir, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if files, _ := g.Conflicted(gd); strings.Join(files, ",") != "gone.txt" {
		t.Fatalf("Conflicted = %v", files)
	}
	if err := g.Run(dir, merge.OpMerge, merge.Abort); err != nil {
		t.Fatal(err)
	}
	if g.Op(gd) != "" || run("rev-parse", "HEAD") != head || run("status", "--porcelain") != "" {
		t.Fatal("abort did not go back")
	}
	// A squash that clashes leaves no merge in progress; ResetMerge puts
	// the files back.
	_ = g.Merge(dir, "side", merge.Squash)
	if g.Op(gd) == merge.OpMerge {
		t.Fatal("a squash left a merge in progress")
	}
	if err := g.ResetMerge(dir); err != nil || run("status", "--porcelain") != "" {
		t.Fatalf("ResetMerge: %v %q", err, run("status", "--porcelain"))
	}
}

func TestOpTellsRevertSeriesApart(t *testing.T) {
	dir, run := repo(t)
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "-am", "more")
	// Reverting "main" clashes with "more": the series stops on it.
	cmd := exec.Command("git", "-C", dir, "revert", "--no-edit", "HEAD~1", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Me", "GIT_AUTHOR_EMAIL=me@example.com", "GIT_COMMITTER_NAME=Me", "GIT_COMMITTER_EMAIL=me@example.com")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the reverts did not stop:\n%s", out)
	}
	if op := g.Op(gd); op != merge.OpRevert {
		t.Fatalf("Op = %q", op)
	}
	// Committed by hand: only the sequencer is left, and it still is a
	// revert, not a cherry-pick.
	run("checkout", "--theirs", "--", "a.txt")
	run("add", "--all")
	run("commit", "-q", "--no-edit")
	if gitinfra.InProgress(gd) != merge.OpCherryPick || g.Op(gd) != merge.OpRevert {
		t.Fatalf("InProgress %q, Op %q", gitinfra.InProgress(gd), g.Op(gd))
	}
	if err := g.Run(dir, merge.OpRevert, merge.Abort); err != nil || g.Op(gd) != "" {
		t.Fatalf("abort: %v %q", err, g.Op(gd))
	}
}
