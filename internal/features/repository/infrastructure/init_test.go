package infrastructure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

func TestInitMakesTheFolderAndTheRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	g := &gitinfra.Runner{}
	i := &Initializer{Git: g}
	if v, err := i.Version(); err != nil || !strings.HasPrefix(v, "git version") {
		t.Fatalf("Version = %q, %v", v, err)
	}
	dir := filepath.Join(home, "a", "b", "new")
	if err := i.Init(dir, "trunk"); err != nil {
		t.Fatal(err)
	}
	if gd := g.GitDirFor(dir); gd == "" {
		t.Fatal("not a repo")
	}
	if got, _ := g.Run("-C", dir, "symbolic-ref", "--short", "HEAD"); got != "trunk" {
		t.Errorf("branch = %q", got)
	}
	if err := i.Init(filepath.Join(home, "plain"), ""); err != nil {
		t.Fatal(err)
	}
	// Dry run: printed, nothing made.
	var out strings.Builder
	d := &Initializer{Git: &gitinfra.Runner{DryRun: true, Out: &out}}
	dry := filepath.Join(home, "dry")
	if err := d.Init(dry, "main"); err != nil || !strings.Contains(out.String(), "[dry-run] git") {
		t.Fatalf("dry: %v %q", err, out.String())
	}
	if _, err := os.Stat(dry); err == nil {
		t.Error("a dry run made the folder")
	}
}
