package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

func TestParseStatus(t *testing.T) {
	entries := []string{
		"1 M. N... 100644 100644 100644 aaa bbb staged.go",
		"1 .M N... 100644 100644 100644 aaa aaa dir/with space.go",
		"2 R. N... 100644 100644 100644 aaa aaa R100 new name.go", "old name.go",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc clash.go",
		"? notes.txt",
	}
	got := parseStatus(strings.Join(entries, "\x00") + "\x00")
	want := []workspace.File{
		{Path: "staged.go", Staged: "M", Worktree: "."},
		{Path: "dir/with space.go", Staged: ".", Worktree: "M"},
		{Path: "new name.go", Orig: "old name.go", Staged: "R", Worktree: "."},
		{Path: "clash.go", Staged: "U", Worktree: "U", Conflict: true},
		{Path: "notes.txt", Staged: "?", Worktree: "?"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseStatus =\n%+v\nwant\n%+v", got, want)
	}
	if got := parseStatus(""); len(got) != 0 {
		t.Errorf("empty status = %+v", got)
	}
}

// A real repo: list, diff, stage, unstage and discard, with paths git
// could read as patterns ("*.txt") or that hold spaces.
func TestChangesWithGit(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "me@example.com")
	run("config", "user.name", "Me")
	write("a.txt", "one\n")
	write("*.txt", "star\n")
	write("keep.txt", "keep\n")
	run("add", "--all")
	run("commit", "-q", "-m", "first")

	write("a.txt", "one\ntwo\n")
	write("*.txt", "star changed\n")
	write("dir/new file.txt", "fresh\n")
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	if real, err := filepath.EvalSymlinks(gd); err == nil {
		gd = real
	}
	wt := filepath.Dir(gd)

	files, err := g.Status(gd)
	if err != nil || len(files) != 3 {
		t.Fatalf("status: %+v %v", files, err)
	}
	d, err := g.Diff(gd, false, []string{"dir/new file.txt"})
	if err != nil || !strings.Contains(d, "+fresh") {
		t.Fatalf("diff of a new file: %q %v", d, err)
	}
	if d, _ := g.Diff(gd, false, nil); !strings.Contains(d, "+two") || !strings.Contains(d, "+fresh") || !strings.Contains(d, "+star changed") {
		t.Fatalf("diff of every file:\n%s", d)
	}

	// "*.txt" is that one file, not every .txt file.
	if err := g.Stage(wt, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := run("diff", "--cached", "--name-only"); got != "*.txt" {
		t.Fatalf("staged %q, want only *.txt", got)
	}
	if d, _ := g.Diff(gd, true, nil); !strings.Contains(d, "+star changed") || strings.Contains(d, "+two") {
		t.Fatalf("staged diff:\n%s", d)
	}
	if err := g.Unstage(wt, []string{"*.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := run("diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("still staged: %q", got)
	}

	// Discard a changed file and a new one; the other change stays, and
	// the discarded ones are in the stash.
	if err := g.Discard(wt, "gitswitch discard: main", []string{"a.txt", "dir/new file.txt"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Errorf("a.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "dir", "new file.txt")); !os.IsNotExist(err) {
		t.Errorf("new file still there: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "*.txt")); string(b) != "star changed\n" {
		t.Errorf("*.txt lost its change: %q", b)
	}
	if got := run("stash", "list"); !strings.Contains(got, "gitswitch discard: main") {
		t.Errorf("stash list: %q", got)
	}
	run("stash", "pop", "-q")
	if b, _ := os.ReadFile(filepath.Join(dir, "dir", "new file.txt")); string(b) != "fresh\n" {
		t.Errorf("the stash did not bring the new file back: %q", b)
	}

	// Discard everything: changed and new files go, all into one stash.
	write("a.txt", "again\n")
	if err := g.Discard(wt, "gitswitch discard: main", nil); err != nil {
		t.Fatal(err)
	}
	if got := run("status", "--porcelain"); got != "" {
		t.Errorf("left after discarding everything: %q", got)
	}
}

// hunkRepo is a real repo for the hunk and line tests.
type hunkRepo struct {
	t   *testing.T
	dir string
	g   *Git
	gd  string
}

func newHunkRepo(t *testing.T) *hunkRepo {
	t.Helper()
	r := &hunkRepo{t: t, dir: t.TempDir(), g: &Git{Git: &gitinfra.Runner{}}}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "me@example.com")
	r.git("config", "user.name", "Me")
	r.git("config", "core.autocrlf", "false")
	r.gd = filepath.Join(r.dir, ".git")
	if real, err := filepath.EvalSymlinks(r.gd); err == nil {
		r.gd = real
	}
	r.dir = filepath.Dir(r.gd)
	return r
}

func (r *hunkRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

func (r *hunkRepo) write(name, text string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *hunkRepo) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

func (r *hunkRepo) commit(name, text string) {
	r.write(name, text)
	r.git("add", name)
	r.git("commit", "-q", "-m", "add "+name)
}

// act does what the use cases do: read the diff, build the patch, apply.
func (r *hunkRepo) act(path string, staged, reverse bool, sel workspace.Selection) error {
	r.t.Helper()
	raw, err := r.g.Diff(r.gd, staged, []string{path})
	if err != nil {
		r.t.Fatal(err)
	}
	fd, err := workspace.ParseDiff(raw)
	if err != nil {
		return err
	}
	patch, _, err := workspace.BuildPatch(fd, sel, reverse)
	if err != nil {
		return err
	}
	return r.g.Apply(r.dir, patch, workspace.ApplyOptions{Cached: staged || !reverse, Reverse: reverse})
}

func TestStageUnstageHunkAndLines(t *testing.T) {
	r := newHunkRepo(t)
	base := ""
	for i := 1; i <= 30; i++ {
		base += "row " + string(rune('a'+i%26)) + string(rune('0'+i%10)) + "\n"
	}
	r.commit("f.txt", base)
	lines := strings.Split(strings.TrimSuffix(base, "\n"), "\n")
	lines[2] = "CHANGED 3"
	lines[25] = "CHANGED 26"
	lines = append(lines[:10], append([]string{"inserted A", "inserted B"}, lines[10:]...)...)
	r.write("f.txt", strings.Join(lines, "\n")+"\n")

	d, _ := r.g.Diff(r.gd, false, []string{"f.txt"})
	fd, err := workspace.ParseDiff(d)
	if err != nil || len(fd.Hunks) != 3 {
		t.Fatalf("hunks: %d %v\n%s", len(fd.Hunks), err, d)
	}
	// Stage the middle hunk only.
	if err := r.act("f.txt", false, false, workspace.Selection{Hunks: []int{1}}); err != nil {
		t.Fatal(err)
	}
	staged := r.git("diff", "--cached")
	if !strings.Contains(staged, "+inserted A") || strings.Contains(staged, "CHANGED") {
		t.Fatalf("staged:\n%s", staged)
	}
	// Stage a single line of the third hunk: the new line, not the deletion.
	d, _ = r.g.Diff(r.gd, false, []string{"f.txt"})
	fd, _ = workspace.ParseDiff(d)
	add := -1
	for j, l := range fd.Hunks[1].Lines {
		if l.Kind == "add" {
			add = j
		}
	}
	if err := r.act("f.txt", false, false, workspace.Selection{Lines: map[int][]int{1: {add}}}); err != nil {
		t.Fatal(err)
	}
	staged = r.git("diff", "--cached")
	if !strings.Contains(staged, "+CHANGED 26") {
		t.Fatalf("staged after the line:\n%s", staged)
	}
	// The deletion of the old line is still not staged: the index holds both lines.
	idx := r.git("show", ":f.txt")
	if !strings.Contains(idx, "CHANGED 26") || !strings.Contains(idx, strings.Split(base, "\n")[25]) {
		t.Fatalf("index should hold the old line and the new one:\n%s", idx)
	}
	// Unstage the inserted lines (hunk of the staged diff), then one line of what is left.
	sd, _ := r.g.Diff(r.gd, true, []string{"f.txt"})
	sfd, _ := workspace.ParseDiff(sd)
	if err := r.act("f.txt", true, true, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("diff", "--cached"); strings.Contains(got, "inserted A") || !strings.Contains(got, "+CHANGED 26") {
		t.Fatalf("after unstaging hunk 0 of %d:\n%s", len(sfd.Hunks), got)
	}
	if got := r.read("f.txt"); got != strings.Join(lines, "\n")+"\n" {
		t.Errorf("the file must not change when staging or unstaging")
	}
	// Everything staged, then every line unstaged one hunk at a time, brings back an empty index change.
	r.git("add", "f.txt")
	for r.git("diff", "--cached", "--name-only") != "" {
		if err := r.act("f.txt", true, true, workspace.Selection{Hunks: []int{0}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageLinesNewDeletedAndEdges(t *testing.T) {
	r := newHunkRepo(t)
	r.commit("keep.txt", "k\n")

	// A new file: staged line by line (intent to add first).
	r.write("new.txt", "n1\nn2\nn3\nn4\n")
	raw, _ := r.g.Diff(r.gd, false, []string{"new.txt"})
	fd, err := workspace.ParseDiff(raw)
	if err != nil || !fd.Creates {
		t.Fatalf("new file diff: %v\n%s", err, raw)
	}
	if err := r.g.IntentToAdd(r.dir, "new.txt"); err != nil {
		t.Fatal(err)
	}
	patch, whole, err := workspace.BuildPatch(fd, workspace.Selection{Lines: map[int][]int{0: {1, 2}}}, false)
	if err != nil || whole {
		t.Fatal(err, whole)
	}
	if err := r.g.Apply(r.dir, patch, workspace.ApplyOptions{Cached: true}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":new.txt"); got != "n2\nn3" {
		t.Fatalf("index holds %q, want n2 and n3", got)
	}
	// The rest of it, by the second diff.
	if err := r.act("new.txt", false, false, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":new.txt"); got != "n1\nn2\nn3\nn4" {
		t.Fatalf("index holds %q", got)
	}
	// Unstage all of the new file's lines through a patch: the file leaves the index.
	if err := r.act("new.txt", true, true, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("ls-files", "--", "new.txt"); got != "" {
		t.Errorf("still in the index: %q", got)
	}
	if r.read("new.txt") != "n1\nn2\nn3\nn4\n" {
		t.Error("the new file must keep its content")
	}

	// A deleted file: stage the deletion of two lines (the file stays with the third).
	r.commit("gone.txt", "g1\ng2\ng3\n")
	if err := os.Remove(filepath.Join(r.dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := r.act("gone.txt", false, false, workspace.Selection{Lines: map[int][]int{0: {0, 1}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":gone.txt"); got != "g3" {
		t.Errorf("index holds %q", got)
	}
	// The whole deletion.
	if err := r.act("gone.txt", false, false, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("ls-files", "--", "gone.txt"); got != "" {
		t.Errorf("deleted file still in the index: %q", got)
	}
	if err := r.act("gone.txt", true, true, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("ls-files", "--", "gone.txt"); got == "" {
		t.Error("unstaging the deletion should bring the file back to the index")
	}

	// No newline at the end of the file.
	r.commit("eol.txt", "a\nb\nc")
	r.write("eol.txt", "a\nB\nc\nd")
	if err := r.act("eol.txt", false, false, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":eol.txt"); got != "a\nB\nc\nd" {
		t.Errorf("no-eol whole hunk: %q", got)
	}
	r.git("reset", "-q")
	// Picking "b" -> "B" but not the end of the file: git lists the deleted
	// lines before the added ones, so "c" (no line break) would come before
	// the new "B". That is refused instead of giving a broken file.
	raw, _ = r.g.Diff(r.gd, false, []string{"eol.txt"})
	fd, _ = workspace.ParseDiff(raw)
	var sel []int
	for j, l := range fd.Hunks[0].Lines {
		if l.Text == "B" || l.Text == "b" {
			sel = append(sel, j)
		}
	}
	if err := r.act("eol.txt", false, false, workspace.Selection{Lines: map[int][]int{0: sel}}); err == nil || !strings.Contains(err.Error(), "without a line break") {
		t.Errorf("want the refusal, got %v", err)
	}
	// An end that stays without a line break works line by line.
	r.commit("eol2.txt", "a\nb\nc")
	r.write("eol2.txt", "a\nB\nc")
	if err := r.act("eol2.txt", false, false, workspace.Selection{Lines: map[int][]int{0: {1}}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":eol2.txt"); got != "a\nc" {
		t.Errorf("eol2 deletion only: %q", got)
	}
	if err := r.act("eol2.txt", false, false, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":eol2.txt"); got != "a\nB\nc" {
		t.Errorf("eol2 rest: %q", got)
	}

	// CRLF files.
	r.commit("crlf.txt", "a\r\nb\r\nc\r\n")
	r.write("crlf.txt", "a\r\nB\r\nc\r\nd\r\n")
	raw, _ = r.g.Diff(r.gd, false, []string{"crlf.txt"})
	fd, _ = workspace.ParseDiff(raw)
	var del []int
	for j, l := range fd.Hunks[0].Lines {
		if l.Kind != "context" && (l.Text == "B\r" || l.Text == "b\r") {
			del = append(del, j)
		}
	}
	if err := r.act("crlf.txt", false, false, workspace.Selection{Lines: map[int][]int{0: del}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":crlf.txt"); got != "a\r\nB\r\nc\r" {
		t.Errorf("crlf: %q", got)
	}
	// A whole hunk whose last changed line ends in CRLF keeps that CR.
	r.commit("crlf2.txt", "x\r\ny\r\n")
	r.write("crlf2.txt", "x\r\nY\r\n")
	if err := r.act("crlf2.txt", false, false, workspace.Selection{Hunks: []int{0}}); err != nil {
		t.Fatal(err)
	}
	if got := r.git("show", ":crlf2.txt"); got != "x\r\nY\r" {
		t.Errorf("crlf last line: %q", got)
	}
}

func TestDiscardHunkIsKept(t *testing.T) {
	r := newHunkRepo(t)
	base := ""
	for i := 1; i <= 30; i++ {
		base += "row " + string(rune('a'+i%26)) + string(rune('0'+i%10)) + "\n"
	}
	r.commit("f.txt", base)
	ls := strings.Split(strings.TrimSuffix(base, "\n"), "\n")
	ls[1] = "FIRST"
	ls[27] = "LAST"
	changed := strings.Join(ls, "\n") + "\n"
	r.write("f.txt", changed)
	r.commit("other.txt", "o\n")
	r.write("other.txt", "o changed\n")

	raw, _ := r.g.Diff(r.gd, false, []string{"f.txt"})
	fd, _ := workspace.ParseDiff(raw)
	if len(fd.Hunks) != 2 {
		t.Fatalf("hunks %d", len(fd.Hunks))
	}
	patch, _, err := workspace.BuildPatch(fd, workspace.Selection{Hunks: []int{0}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.g.Apply(r.dir, patch, workspace.ApplyOptions{Reverse: true, Check: true}); err != nil {
		t.Fatal(err)
	}
	if r.read("f.txt") != changed {
		t.Fatal("check must not change the file")
	}
	msg := "gitswitch discard: main"
	if err := r.g.Backup(r.dir, msg, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if r.read("f.txt") != changed || r.git("diff", "--cached", "--name-only") != "" {
		t.Fatal("backup must not touch the folder or the index")
	}
	if err := r.g.Apply(r.dir, patch, workspace.ApplyOptions{Reverse: true}); err != nil {
		t.Fatal(err)
	}
	got := r.read("f.txt")
	if strings.Contains(got, "FIRST") || !strings.Contains(got, "LAST") {
		t.Fatalf("after discarding hunk 0:\n%s", got)
	}
	if r.read("other.txt") != "o changed\n" {
		t.Error("other files are untouched")
	}
	if list := r.git("stash", "list"); !strings.Contains(list, msg) {
		t.Fatalf("stash list: %q", list)
	}
	// The stash holds only f.txt, as it was.
	if names := r.git("stash", "show", "--name-only", "stash@{0}"); names != "f.txt" {
		t.Errorf("stash holds %q", names)
	}
	if r.git("show", "stash@{0}:f.txt") != strings.TrimSuffix(changed, "\n") {
		t.Error("the stash must hold the file as it was before the discard")
	}
	// Getting it back: git restore --source=stash@{0} -- f.txt puts the file as it was (the stash holds only that file).
	r.git("restore", "--source=stash@{0}", "--", "f.txt")
	if r.read("f.txt") != changed {
		t.Errorf("restoring from the stash did not give the file back:\n%s", r.read("f.txt"))
	}
}
