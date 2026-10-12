package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

func TestCopyExeReplacesTheProgram(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new"), filepath.Join(dir, "gitswitch")
	_ = os.WriteFile(src, []byte("new program"), 0o644)
	_ = os.WriteFile(dst, []byte("old program"), 0o755)
	if err := copyExe(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new program" {
		t.Errorf("dst = %q", b)
	}
	if st, _ := os.Stat(dst); runtime.GOOS != "windows" && st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".gitswitch.new-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func pathTest(t *testing.T) (home string, said *[]string, in *Installer) {
	if runtime.GOOS != "linux" {
		t.Skip("shell profiles are tested on Linux")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "")
	said = &[]string{}
	in = &Installer{FS: &filesystem.FS{}, Report: func(s string) { *said = append(*said, s) }}
	return
}

func TestAddToPathWithNoShellProfile(t *testing.T) {
	home, said, in := pathTest(t)
	if err := in.addToPath(filepath.Join(home, ".local", "bin")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".profile"))
	if !strings.Contains(string(b), ".local/bin") {
		t.Errorf(".profile = %q", b)
	}
	if len(*said) != 1 || !strings.Contains((*said)[0], "~/.profile") {
		t.Errorf("said %q", *said)
	}
}

func TestAddToPathFishAndExisting(t *testing.T) {
	home, said, in := pathTest(t)
	_ = os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export PATH=~/.local/bin:$PATH\n"), 0o644)
	if err := in.addToPath(filepath.Join(home, ".local", "bin")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".config", "fish", "conf.d", "gitswitch.fish"))
	if !strings.Contains(string(b), "fish_add_path") {
		t.Errorf("fish file = %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".profile")); err == nil {
		t.Error(".profile written although .bashrc already adds the folder")
	}
	if len(*said) != 1 || strings.Contains((*said)[0], "bashrc") {
		t.Errorf("said %q", *said)
	}
}

func TestUninstallKeepsANewGlobalEmail(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "1")
	t.Chdir(home) // outside any repo
	git := &gitinfra.Runner{}
	in := &Installer{Git: git, FS: &filesystem.FS{}}

	_, _ = git.Global("gitswitch.backup.email", "old@example.com")
	_, _ = git.Global("user.email", "new@example.com")
	if err := in.Uninstall(false, nil); err != nil {
		t.Fatal(err)
	}
	if e := git.GlobalGet("user.email"); e != "new@example.com" {
		t.Errorf("user.email = %q, want the one set after install", e)
	}

	_, _ = git.Global("gitswitch.backup.email", "old@example.com")
	_, _ = git.Global("--unset", "user.email")
	if err := in.Uninstall(false, nil); err != nil {
		t.Fatal(err)
	}
	if e := git.GlobalGet("user.email"); e != "old@example.com" {
		t.Errorf("user.email = %q, want it restored", e)
	}
}
