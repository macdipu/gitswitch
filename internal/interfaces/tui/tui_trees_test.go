package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// wtScreen is the worktrees list as drawn, without colours.
func wtScreen(x *tui) string {
	lines, _ := x.drawWorktrees()
	return history.StripANSI(strings.Join(lines, "\n"))
}

// addWorktree runs the add form with the given values and closes the panel
// that reports it.
func addWorktree(t *testing.T, x *tui, folder, kind, name, from string) {
	t.Helper()
	x.handle("a")
	if x.form == nil {
		t.Fatalf("no add form: %q", x.msg)
	}
	f := x.form.fields
	f[0].value, f[1].value, f[2].value, f[3].value, f[4].value = folder, kind, name, from, "no"
	for range f {
		x.handle("enter")
	}
}

func TestWorktreeAddListRemove(t *testing.T) {
	x, dir, git := rwRepo(t, 1)
	x.handle("W")
	if s := wtScreen(x); !strings.Contains(s, "main folder") || !strings.Contains(s, "main") {
		t.Fatalf("list:\n%s", s)
	}
	wt := filepath.Join(filepath.Dir(dir), "r-wt")
	addWorktree(t, x, wt, "a new branch", "wt-branch", "")
	if x.form != nil || x.view != nil && !strings.Contains(body(x), "Added the worktree") {
		t.Fatalf("not added: %q\n%s", x.msg, body(x))
	}
	x.view = nil
	if s := wtScreen(x); !strings.Contains(s, "wt-branch") || !strings.Contains(s, "clean") {
		t.Fatalf("list after add:\n%s", s)
	}
	if !strings.Contains(git("worktree", "list"), "wt-branch") {
		t.Fatal("git does not list it")
	}
	// the branch is checked out in the main folder: refused for an existing branch
	addWorktree(t, x, filepath.Join(filepath.Dir(dir), "r-wt2"), "an existing branch", "main", "")
	if x.view == nil || !strings.Contains(body(x), "already checked out") {
		t.Fatalf("no refusal:\n%s", body(x))
	}
	x.view = nil
	// uncommitted work: removal asks twice and says what is lost
	os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("x"), 0o644)
	x.loadWorktrees()
	x.handle("j")
	x.handle("x")
	if x.confirm == nil || !strings.Contains(body(x), "Remove the worktree") {
		t.Fatalf("no question: %q", x.msg)
	}
	x.handle("y")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "would be lost") || !strings.Contains(b, "F =") || !strings.Contains(b, "anyway") {
		t.Fatalf("no second question:\n%s", b)
	}
	x.handle("y") // y is not the key of the second question
	if x.confirm == nil {
		t.Fatal("y answered the force question")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("the folder is gone before force was asked")
	}
	x.handle("F")
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("the folder is still there")
	}
	if strings.Contains(wtScreen(x), "wt-branch  ") && strings.Contains(git("worktree", "list"), "r-wt ") {
		t.Fatal("still listed")
	}
	if git("branch", "--list", "wt-branch") == "" {
		t.Fatal("the branch should stay")
	}
	// the main folder can't be removed
	x.handle("g")
	x.handle("x")
	if x.confirm != nil || x.view == nil || !strings.Contains(body(x), "repo's own folder") {
		t.Fatalf("main folder not refused:\n%s", body(x))
	}
}

func TestWorktreeAddDetachedAndRegister(t *testing.T) {
	x, dir, _ := rwRepo(t, 1)
	x.handle("W")
	wt := filepath.Join(filepath.Dir(dir), "r-detached")
	x.handle("a")
	f := x.form.fields
	f[0].value, f[1].value, f[2].value = wt, "a detached commit", "HEAD"
	for range f { // "Add to the repo list" stays yes
		x.handle("enter")
	}
	if x.view == nil || !strings.Contains(body(x), "detached HEAD") || !strings.Contains(body(x), "Added it to the repo list") {
		t.Fatalf("report:\n%s", body(x))
	}
	x.handle("enter")
	if s := wtScreen(x); !strings.Contains(s, "(detached ") {
		t.Fatalf("list:\n%s", s)
	}
	found := false
	for _, r := range x.app.Repos.List() {
		if filepath.Base(r.Path) == "r-detached" {
			found = true
		}
	}
	if !found {
		t.Fatal("the worktree is not in the repo list")
	}
	// a folder that has files is refused
	os.MkdirAll(filepath.Join(filepath.Dir(dir), "full"), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "full", "f"), []byte("x"), 0o644)
	addWorktree(t, x, filepath.Join(filepath.Dir(dir), "full"), "a new branch", "nb", "")
	if x.view == nil || !strings.Contains(body(x), "not empty") {
		t.Fatalf("no refusal:\n%s", body(x))
	}
}

func TestWorktreeLockPrune(t *testing.T) {
	x, dir, git := rwRepo(t, 1)
	x.handle("W")
	wt := filepath.Join(filepath.Dir(dir), "r-lock")
	addWorktree(t, x, wt, "a new branch", "lockme", "")
	x.view = nil
	x.handle("j")
	x.handle("l")
	typeKeys(x, "usb drive")
	x.handle("enter")
	if s := wtScreen(x); !strings.Contains(s, "locked: usb drive") {
		t.Fatalf("not locked:\n%s", s)
	}
	x.handle("x")
	x.handle("y")
	if x.view == nil || !strings.Contains(body(x), "is locked") {
		t.Fatalf("a locked worktree was removed or not refused:\n%s", body(x))
	}
	x.view = nil
	x.handle("u")
	if s := wtScreen(x); strings.Contains(s, "locked") {
		t.Fatalf("still locked:\n%s", s)
	}
	// nothing to prune yet
	x.handle("p")
	if x.confirm != nil || !strings.Contains(x.msg, "Nothing to prune") {
		t.Fatalf("msg %q", x.msg)
	}
	// its folder disappears: prune lists it, asks, forgets it
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	x.handle("r")
	if s := wtScreen(x); !strings.Contains(s, "folder is gone") {
		t.Fatalf("list:\n%s", s)
	}
	x.handle("p")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "r-lock") || !strings.Contains(b, "No files are touched") {
		t.Fatalf("prune plan:\n%s", b)
	}
	x.handle("y")
	if strings.Contains(git("worktree", "list"), "r-lock") || len(x.wtView.all) != 1 {
		t.Fatalf("not pruned: %s", git("worktree", "list"))
	}
}

// ---------------- submodules ----------------

// subRepo is a repo (on History) with one local submodule libs/sub whose
// source has two commits; git is allowed local-file submodules.
func subRepo(t *testing.T) (*tui, string, string, func(...string) string, func(string, ...string) string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	x, dir, git := rwRepo(t, 0)
	src := filepath.Join(t.TempDir(), "subsrc")
	in := func(d string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", d}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.MkdirAll(src, 0o755)
	in(src, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(src, "lib.txt"), []byte("one\n"), 0o644)
	in(src, "add", ".")
	in(src, "commit", "-q", "-m", "lib one")
	git("submodule", "add", "-q", "-b", "main", src, "libs/sub")
	git("commit", "-q", "-m", "add the submodule")
	x.loadHistory()
	return x, dir, src, git, in
}

func subScreen(x *tui) string {
	lines, _ := x.drawSubmodules()
	return history.StripANSI(strings.Join(lines, "\n"))
}

func TestSubmoduleListInitUpdate(t *testing.T) {
	x, dir, src, git, in := subRepo(t)
	x.handle("V")
	if s := subScreen(x); !strings.Contains(s, "libs/sub") || !strings.Contains(s, "up to date") || !strings.Contains(s, "tracks main") {
		t.Fatalf("list:\n%s", s)
	}
	// not initialized -> i
	git("submodule", "deinit", "-q", "-f", "libs/sub")
	x.handle("r")
	if s := subScreen(x); !strings.Contains(s, "not initialized") {
		t.Fatalf("list:\n%s", s)
	}
	x.handle("i")
	if s := subScreen(x); !strings.Contains(s, "up to date") {
		t.Fatalf("not initialized by i: msg %q\n%s", x.msg, s)
	}
	// a new commit upstream: m asks, moves to the tip, says to commit
	os.WriteFile(filepath.Join(src, "lib.txt"), []byte("two\n"), 0o644)
	in(src, "commit", "-q", "-am", "lib two")
	x.handle("m")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "tip of its tracked branch") || !strings.Contains(b, "record a different commit") {
		t.Fatalf("question:\n%s", b)
	}
	x.handle("y")
	if !strings.Contains(x.msg, "commit that change") {
		t.Fatalf("msg %q", x.msg)
	}
	if s := subScreen(x); !strings.Contains(s, "another commit") {
		t.Fatalf("list:\n%s", s)
	}
	if !strings.Contains(git("status", "--porcelain"), "libs/sub") {
		t.Fatal("the repo should show the new commit as a change")
	}
	// u goes back to the recorded commit, after asking
	x.handle("u")
	if x.confirm == nil || !strings.Contains(body(x), "this repo records") {
		t.Fatalf("question:\n%s", body(x))
	}
	// but not over changes inside: refused with the reason
	os.WriteFile(filepath.Join(dir, "libs/sub/lib.txt"), []byte("my edit\n"), 0o644)
	x.loadSubmodules()
	x.handle("y")
	if x.view == nil || !strings.Contains(body(x), "can't update") {
		t.Fatalf("not refused:\n%s", body(x))
	}
	x.view = nil
	in(filepath.Join(dir, "libs/sub"), "checkout", "-q", "--", "lib.txt")
	x.loadSubmodules()
	x.handle("u")
	x.handle("y")
	if s := subScreen(x); !strings.Contains(s, "up to date") {
		t.Fatalf("not back:\n%s\nmsg %q", s, x.msg)
	}
	x.handle("s")
	if !strings.Contains(x.msg, "match .gitmodules") {
		t.Fatalf("sync: %q", x.msg)
	}
}

func TestSubmoduleAddRemove(t *testing.T) {
	x, dir, _, git, in := subRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	os.MkdirAll(other, 0o755)
	in(other, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(other, "o.txt"), []byte("o\n"), 0o644)
	in(other, "add", ".")
	in(other, "commit", "-q", "-m", "other one")
	// GitSwitch takes SSH and HTTPS URLs only: this one is rewritten to the local repo by git
	const url = "https://example.test/o/other.git"
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_1", "url."+other+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_1", url)
	x.handle("V")
	x.handle("a")
	f := x.form.fields
	f[0].value, f[1].value, f[2].value = url, "libs/other", "main"
	for range f {
		x.handle("enter")
	}
	if x.view == nil || !strings.Contains(body(x), "Added") {
		t.Fatalf("not added: %q\n%s", x.msg, body(x))
	}
	x.handle("enter")
	if s := subScreen(x); !strings.Contains(s, "libs/other") {
		t.Fatalf("list:\n%s", s)
	}
	// the path is taken
	x.handle("a")
	f = x.form.fields
	f[0].value, f[1].value = url, "libs/other"
	for range f {
		x.handle("enter")
	}
	if x.view == nil || !strings.Contains(body(x), "already exists") {
		t.Fatalf("no refusal:\n%s", body(x))
	}
	x.view = nil
	// remove: planned first, asks
	for i, e := range x.subView.all {
		if e.Path == "libs/other" {
			x.subView.sel = i
		}
	}
	x.handle("x")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "Removing libs/other deletes") || !strings.Contains(b, ".gitmodules") {
		t.Fatalf("plan:\n%s", b)
	}
	x.handle("y")
	if _, err := os.Stat(filepath.Join(dir, "libs/other")); err == nil {
		t.Fatal("the folder is still there")
	}
	// changes inside: refused, then force asks again
	for i, e := range x.subView.all {
		if e.Path == "libs/sub" {
			x.subView.sel = i
		}
	}
	os.WriteFile(filepath.Join(dir, "libs/sub/lib.txt"), []byte("unsaved\n"), 0o644)
	x.loadSubmodules()
	x.handle("x")
	if b := body(x); x.confirm == nil || !strings.Contains(b, "changed file(s)") || !strings.Contains(b, "F =") {
		t.Fatalf("force question:\n%s", b)
	}
	x.handle("y")
	if x.confirm == nil {
		t.Fatal("y answered the force question")
	}
	x.handle("F")
	if _, err := os.Stat(filepath.Join(dir, "libs/sub/lib.txt")); err == nil {
		t.Fatal("not removed")
	}
	if strings.Contains(git("status", "--porcelain"), "libs/sub/") {
		t.Fatalf("leftovers: %s", git("status", "--porcelain"))
	}
}
