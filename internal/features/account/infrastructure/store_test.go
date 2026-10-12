package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

func TestLoadCheckedReportsABrokenConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	cfg := filepath.Join(home, ".gitconfig")
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Chdir(home) // outside any repo
	s := &Store{Git: &gitinfra.Runner{}, FS: &filesystem.FS{}}

	if c, err := s.LoadChecked(); err != nil || len(c.Accounts) != 0 {
		t.Fatalf("no config file: %v %v", c, err)
	}
	_ = os.WriteFile(cfg, []byte("[user]\n\tname = x\n"), 0o644)
	if _, err := s.LoadChecked(); err != nil {
		t.Fatalf("no gitswitch settings yet is not an error: %v", err)
	}
	_ = os.WriteFile(cfg, []byte("[gitswitch-account \"work\"]\n\temail = w@x.com\n"), 0o644)
	if c, err := s.LoadChecked(); err != nil || len(c.Accounts) != 1 {
		t.Fatalf("one account: %v %v", c.Accounts, err)
	}
	_ = os.WriteFile(cfg, []byte("[gitswitch-account \"work\"\n\temail = w@x.com\n"), 0o644)
	if _, err := s.LoadChecked(); err == nil {
		t.Fatal("a broken config must be reported")
	}
	if c := s.Load(); c == nil || len(c.Accounts) != 0 {
		t.Errorf("Load still returns the defaults: %v", c)
	}
}
