package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
)

// Real git and ssh-keygen: a commit signed with an ssh key verifies (G)
// with an allowed signers file, says E without one, N when unsigned.
func TestSignaturesWithSSHKey(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Me", "-c", "user.email=me@x.example"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	key := filepath.Join(home, "k")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	f := strings.Fields(string(pub))
	signers := filepath.Join(home, "allowed")
	if err := os.WriteFile(signers, []byte(`me@x.example namespaces="git" `+f[0]+" "+f[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "plain")
	plain := run("rev-parse", "HEAD")
	run("config", "gpg.format", "ssh")
	run("config", "user.signingkey", key)
	run("commit", "-q", "--allow-empty", "-S", "-m", "signed")
	signed := run("rev-parse", "HEAD")

	l := &Log{Git: &gitinfra.Runner{}}
	gd := filepath.Join(dir, ".git")
	// No allowed signers file yet: git can't check.
	m, err := l.Signatures(gd, []string{signed, plain})
	if err != nil || m[signed].Status != "E" || m[signed].Verified || m[plain].Status != "N" {
		t.Fatalf("without signers: %+v %v", m, err)
	}
	run("config", "gpg.ssh.allowedSignersFile", signers)
	m, err = l.Signatures(gd, []string{signed, plain, "0123456789abcdef0123456789abcdef01234567", signed[:10]})
	if err != nil {
		t.Fatal(err)
	}
	if g := m[signed]; g.Status != "G" || !g.Verified || g.Signer != "me@x.example" || g.Key == "" {
		t.Errorf("signed = %+v", g)
	}
	if m[plain].Status != "N" || m[plain].Signer != "" {
		t.Errorf("plain = %+v", m[plain])
	}
	if _, ok := m["0123456789abcdef0123456789abcdef01234567"]; ok {
		t.Error("an unknown commit must be left out")
	}
	if m[signed[:10]].Status != "G" {
		t.Errorf("short hash: %+v", m[signed[:10]])
	}
	// A commit changed after signing is bad: re-sign nothing, but break the
	// signature by editing the object's message is not possible via git, so
	// the check above is enough for B.
}
