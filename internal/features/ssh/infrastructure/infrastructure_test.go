package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
)

func TestWriteKeyPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_work")
	if err := writeKeyPair(path, "you@company.com", "s3cret pass"); err != nil {
		t.Fatal(err)
	}
	priv, _ := os.ReadFile(path)
	pub, _ := os.ReadFile(path + ".pub")
	if !strings.HasPrefix(string(pub), "ssh-ed25519 ") || !strings.HasSuffix(string(pub), " you@company.com\n") {
		t.Errorf("public key = %q", pub)
	}
	if _, err := gossh.ParseRawPrivateKey(priv); err == nil {
		t.Error("the private key must be encrypted")
	}
	signer, err := gossh.ParsePrivateKeyWithPassphrase(priv, []byte("s3cret pass"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))); !strings.HasPrefix(string(pub), got) {
		t.Errorf("public key %q does not match the private key %q", pub, got)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("private key mode = %v", st.Mode().Perm())
		}
	}
	// ssh-keygen itself reads it, when installed.
	if kg, err := exec.LookPath("ssh-keygen"); err == nil {
		out, err := exec.Command(kg, "-y", "-P", "s3cret pass", "-f", path).CombinedOutput()
		if err != nil || !strings.HasPrefix(string(pub), strings.TrimSpace(string(out))) {
			t.Errorf("ssh-keygen -y: %v %s", err, out)
		}
	}

	plain := filepath.Join(t.TempDir(), "id_plain")
	if err := writeKeyPair(plain, "c", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(plain)
	if _, err := gossh.ParseRawPrivateKey(b); err != nil {
		t.Errorf("a key without a passphrase must load: %v", err)
	}
}

func TestKnownHostFindsAPort443Entry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake ssh")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	if err := writeKeyPair(key, "c", ""); err != nil {
		t.Fatal(err)
	}
	pub, _ := os.ReadFile(key + ".pub")
	f := strings.Fields(string(pub))
	known := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(known, []byte("[ssh.github.com]:443 "+f[0]+" "+f[1]+"\n"), 0o644)
	// ~/.ssh/config: Host github.com -> HostName ssh.github.com, Port 443.
	fake := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nprintf 'hostname ssh.github.com\\nport 443\\nuserknownhostsfile " + known + "\\n'\n"
	_ = os.WriteFile(fake, []byte(script), 0o755)
	if !KnownHost(fake, "github.com") {
		t.Error("[ssh.github.com]:443 should count as known for github.com")
	}
	other := filepath.Join(dir, "ssh-other")
	_ = os.WriteFile(other, []byte("#!/bin/sh\nprintf 'hostname gitlab.com\\nport 22\\nuserknownhostsfile "+known+"\\n'\n"), 0o755)
	if KnownHost(other, "gitlab.com") {
		t.Error("gitlab.com is not in known_hosts")
	}
}

func TestCurrentGitDirChecksTheRepoWeAreIn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR") // restored by t.Setenv
	t.Chdir(home)
	git := &gitinfra.Runner{}
	wt := filepath.Join(home, "api")
	for _, args := range [][]string{{"init", "-q", wt}, {"-C", wt, "remote", "add", "origin", "git@github.com:Acme/API.git"}} {
		if _, err := git.Run(args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(wt)
	if d := currentGitDir(git, ssh.Invocation{Host: "github.com", RepoPath: "acme/api.git"}); !strings.HasSuffix(filepath.ToSlash(d), "/api/.git") {
		t.Errorf("the repo being fetched: got %q", d)
	}
	if d := currentGitDir(git, ssh.Invocation{Host: "github.com", RepoPath: "other/lib.git"}); d != "" {
		t.Errorf("cloning another repo from inside this one: got %q, want none", d)
	}
	// A URL rewritten with url.<base>.insteadOf still matches.
	_, _ = git.Run("-C", wt, "remote", "set-url", "origin", "gh:acme/api")
	_, _ = git.Run("config", "--global", "url.git@github.com:.insteadOf", "gh:")
	if d := currentGitDir(git, ssh.Invocation{Host: "github.com", RepoPath: "acme/api.git"}); d == "" {
		t.Error("insteadOf URL not matched")
	}
}
