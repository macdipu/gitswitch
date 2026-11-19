package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	watch "github.com/macdipu/gitswitch/internal/features/watch/domain"
)

func repo(t *testing.T) (dir, gitDir string, git func(...string)) {
	t.Helper()
	dir, _ = filepath.EvalSymlinks(t.TempDir())
	git = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.example", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.example")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	put(t, filepath.Join(dir, ".gitignore"), "build/\n")
	put(t, filepath.Join(dir, "src", "a.txt"), "a\n")
	put(t, filepath.Join(dir, "build", "out.bin"), "x\n")
	git("add", ".")
	git("commit", "-q", "-m", "first")
	return dir, filepath.Join(dir, ".git"), git
}

func put(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// expect reads events until one classifies as want.
func expect(t *testing.T, st watch.Stream, want watch.Kinds) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-st.Events():
			if st.Roots().Classify(e.Path)&want != 0 {
				return
			}
		case <-deadline:
			t.Fatalf("no %v change seen", want.Names())
		}
	}
}

// none expects no event under path for a while.
func none(t *testing.T, st watch.Stream, path string) {
	t.Helper()
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case e := <-st.Events():
			if strings.HasPrefix(e.Path, path) {
				t.Fatalf("event under an ignored folder: %s", e.Path)
			}
		case <-deadline:
			return
		}
	}
}

func drain(st watch.Stream) {
	for {
		select {
		case <-st.Events():
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

// A real repo: file, index, branch, HEAD and merge changes are seen; an
// ignored folder is not watched.
func TestWatchRepo(t *testing.T) {
	dir, gitDir, git := repo(t)
	st, err := (&Source{Git: &gitinfra.Runner{}}).Watch(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Partial() {
		t.Fatal("a small repo is only partly watched")
	}
	if r := st.Roots(); r.Worktree != dir || r.GitDir != gitDir || r.CommonDir != gitDir {
		t.Fatalf("roots = %+v", r)
	}

	put(t, filepath.Join(dir, "src", "a.txt"), "changed\n")
	expect(t, st, watch.Files)

	put(t, filepath.Join(dir, "build", "more.bin"), "y\n")
	none(t, st, filepath.Join(dir, "build"))

	git("add", "src/a.txt")
	expect(t, st, watch.Index)
	drain(st)

	git("branch", "side")
	expect(t, st, watch.Refs)
	drain(st)

	git("switch", "-q", "side")
	expect(t, st, watch.Head)
	drain(st)

	put(t, filepath.Join(gitDir, "MERGE_HEAD"), "0000\n")
	expect(t, st, watch.Operation)

	// A folder made later is watched.
	put(t, filepath.Join(dir, "new", "deep", "f.txt"), "f\n")
	expect(t, st, watch.Files)
	drain(st)
	put(t, filepath.Join(dir, "new", "deep", "g.txt"), "g\n")
	expect(t, st, watch.Files)

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// Over the budget only the top folder and the git dir are watched.
func TestWatchPartial(t *testing.T) {
	dir, gitDir, git := repo(t)
	for i := 0; i < 30; i++ {
		put(t, filepath.Join(dir, "many", string(rune('a'+i%26))+strings.Repeat("x", i/26), "f.txt"), "f\n")
	}
	_ = git
	st, err := (&Source{Git: &gitinfra.Runner{}, Budget: 25}).Watch(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !st.Partial() {
		t.Fatal("a repo over the budget is reported as fully watched")
	}
	put(t, filepath.Join(dir, "top.txt"), "t\n")
	expect(t, st, watch.Files)
}

func TestCommonDir(t *testing.T) {
	d := t.TempDir()
	if got := commonDir(d); got != d {
		t.Fatalf("no commondir file = %s", got)
	}
	put(t, filepath.Join(d, "commondir"), "../..\n")
	if got := commonDir(d); got != filepath.Dir(filepath.Dir(d)) {
		t.Fatalf("commondir = %s", got)
	}
}
