package infrastructure

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// A real repo: recent messages by email on every branch, authors, the
// identity, the draft file and the sign-off setting.
func TestMessagesWithGit(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "GIT_CONFIG_NOSYSTEM=1")
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	g := &Git{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")

	// Before the first commit: nothing, no error.
	if r, err := g.Recent(gd, "me@x.example", 10); err != nil || len(r) != 0 {
		t.Fatalf("empty Recent = %+v %v", r, err)
	}
	if a, err := g.Authors(gd, 10); err != nil || len(a) != 0 {
		t.Fatalf("empty Authors = %+v %v", a, err)
	}

	n := 0
	commit := func(name, email, msg string) {
		t.Helper()
		n++ // a second apart: the order is clear
		date := fmt.Sprintf("2026-01-01T00:00:%02dZ", n)
		saved := env
		env = append(env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		defer func() { env = saved }()
		run("-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "--allow-empty", "-m", msg)
	}
	commit("Me", "me@x.example", "First\n\nThe body.")
	commit("Ada", "ada@x.example", "Hers")
	commit("Me", "ME@x.example", "Second")
	run("switch", "-q", "-c", "side")
	commit("Me", "me@x.example", "On a side branch")
	commit("Someone", "notme@x.example.org", "Similar email")
	run("switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-c", "user.name=Me", "-c", "user.email=me@x.example", "stash", "push", "-q", "-u", "-m", "stashed") // a stash is not a message
	run("update-ref", "refs/gitswitch/undo", "stash")
	r, err := g.Recent(gd, "me@x.example", 10)
	var got []string
	for _, m := range r {
		got = append(got, m.Message)
		if m.Time.IsZero() {
			t.Errorf("no time: %+v", m)
		}
	}
	if err != nil || strings.Join(got, "|") != "On a side branch|Second|First\n\nThe body." {
		t.Fatalf("Recent = %q %v", got, err)
	}
	if r, _ := g.Recent(gd, "me@x.example", 1); len(r) != 1 {
		t.Errorf("max 1: %+v", r)
	}

	a, err := g.Authors(gd, 500)
	if err != nil || len(a) != 3 || a[0].Name != "Me" || a[1].Email != "ada@x.example" {
		t.Fatalf("Authors = %+v %v", a, err)
	}

	run("config", "user.name", "Repo Me")
	run("config", "user.email", "repo@x.example")
	if p := g.Identity(gd); p.Name != "Repo Me" || p.Email != "repo@x.example" {
		t.Errorf("Identity = %+v", p)
	}

	if d := g.Draft(gd); d != "" {
		t.Errorf("no draft = %q", d)
	}
	if err := g.SaveDraft(gd, "Kept\n\nbody"); err != nil || g.Draft(gd) != "Kept\n\nbody" {
		t.Fatalf("draft: %v %q", err, g.Draft(gd))
	}
	if _, err := os.Stat(filepath.Join(gd, DraftFile)); err != nil {
		t.Error(err)
	}
	if err := g.SaveDraft(gd, ""); err != nil || g.Draft(gd) != "" {
		t.Fatalf("cleared: %v", err)
	}
	if err := g.SaveDraft(gd, ""); err != nil {
		t.Errorf("clearing twice: %v", err)
	}

	if gl, rp := g.SignoffValues(gd); gl != "" || rp != "" {
		t.Errorf("unset = %q %q", gl, rp)
	}
	if err := g.SaveSignoff("", "true"); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveSignoff(gd, "false"); err != nil {
		t.Fatal(err)
	}
	if gl, rp := g.SignoffValues(gd); gl != "true" || rp != "false" {
		t.Errorf("saved = %q %q", gl, rp)
	}
	if err := g.SaveSignoff(gd, ""); err != nil {
		t.Fatal(err)
	}
	if err := g.SaveSignoff(gd, ""); err != nil {
		t.Errorf("unset twice: %v", err)
	}
	if err := g.SaveSignoff("", ""); err != nil {
		t.Fatal(err)
	}
	if gl, rp := g.SignoffValues(gd); gl != "" || rp != "" {
		t.Errorf("removed = %q %q", gl, rp)
	}
}
