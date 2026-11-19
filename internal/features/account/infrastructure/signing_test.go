package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Real git and ssh-keygen: a repo using an account gets signed commits
// that verify, keeps its own signing setup, and loses ours when the account
// stops signing.
func TestApplyIdentityWritesSigning(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	key := filepath.Join(home, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "me@test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	s := &Store{Git: &gitinfra.Runner{}, FS: &filesystem.FS{}}
	repo := func(name string) (dir, gd string) {
		dir = filepath.Join(home, name)
		if out, err := exec.Command("git", "init", "-q", "-b", "main", dir).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return dir, filepath.Join(dir, ".git")
	}
	get := func(gd, k string) string { return s.Git.RepoGet(gd, k) }

	a := account.Account{ID: "me", Email: "me@x.example", Name: "Me", SigningFormat: account.SigningSSH, SigningKey: key}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	if c := s.Load(); len(c.Accounts) != 1 || c.Accounts[0].SigningFormat != "ssh" || c.Accounts[0].SigningKey != key {
		t.Fatalf("loaded %+v", c.Accounts)
	}
	if b, err := os.ReadFile(SignersFile("me")); err != nil || !strings.HasPrefix(string(b), `me@x.example namespaces="git" ssh-ed25519 `) || strings.Contains(string(b), "PRIVATE") {
		t.Fatalf("signers file: %q %v", b, err)
	}

	dir, gd := repo("one")
	s.ApplyIdentity(gd, &a)
	if get(gd, "commit.gpgsign") != "true" || get(gd, "gpg.format") != "ssh" || get(gd, "user.signingkey") != key || get(gd, "gitswitch.signing") != "true" ||
		get(gd, "gpg.ssh.allowedsignersfile") != filepath.ToSlash(SignersFile("me")) || get(gd, "user.email") != "me@x.example" {
		t.Fatalf("settings not written: %s", mustRun(t, dir, "config", "--local", "--list"))
	}
	mustRun(t, dir, "commit", "-q", "--allow-empty", "-m", "signed")
	if got := mustRun(t, dir, "log", "-1", "--format=%G?|%GS"); got != "G|me@x.example" {
		t.Fatalf("signature %q", got)
	}
	s.ApplyIdentity(gd, &a) // again: nothing changes, nothing breaks
	if get(gd, "commit.gpgsign") != "true" {
		t.Fatal("second apply broke it")
	}

	// gpg: a key id, no allowed signers file.
	g := a
	g.SigningFormat, g.SigningKey = account.SigningGPG, "ABCD1234"
	s.ApplyIdentity(gd, &g)
	if get(gd, "gpg.format") != "openpgp" || get(gd, "user.signingkey") != "ABCD1234" || get(gd, "gpg.ssh.allowedsignersfile") != "" {
		t.Fatalf("gpg: %s", mustRun(t, dir, "config", "--local", "--list"))
	}

	// Off: ours goes.
	off := a
	off.SigningFormat, off.SigningKey = "", ""
	s.ApplyIdentity(gd, &off)
	for _, k := range []string{"commit.gpgsign", "gpg.format", "user.signingkey", "gpg.ssh.allowedsignersfile", "gitswitch.signing"} {
		if v := get(gd, k); v != "" {
			t.Errorf("%s = %q after off", k, v)
		}
	}
	if get(gd, "user.email") != "me@x.example" {
		t.Error("identity must stay")
	}

	// A repo with its own signing setup keeps it, whatever the account says.
	dir2, gd2 := repo("two")
	mustRun(t, dir2, "config", "commit.gpgsign", "false")
	mustRun(t, dir2, "config", "user.signingkey", "MINE")
	s.ApplyIdentity(gd2, &a)
	if get(gd2, "user.signingkey") != "MINE" || get(gd2, "commit.gpgsign") != "false" || get(gd2, "gitswitch.signing") != "" || get(gd2, "gpg.format") != "" {
		t.Fatalf("own settings touched: %s", mustRun(t, dir2, "config", "--local", "--list"))
	}
	// An account that does not sign leaves a clean repo clean.
	_, gd3 := repo("three")
	s.ApplyIdentity(gd3, &off)
	if get(gd3, "gitswitch.signing") != "" || get(gd3, "commit.gpgsign") != "" {
		t.Error("nothing to write for an account that does not sign")
	}

	// Removing the account removes its signers file; a rename moves it.
	if err := s.Rename("me", "me2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SignersFile("me2")); err != nil {
		t.Errorf("signers file did not follow the rename: %v", err)
	}
	if _, err := os.Stat(SignersFile("me")); err == nil {
		t.Error("old signers file left behind")
	}
	if err := s.Remove("me2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(SignersFile("me2")); err == nil {
		t.Error("signers file left after remove")
	}
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Me", "-c", "user.email=me@x.example"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPublicSigningKeyNeverReadsThePrivateKey(t *testing.T) {
	home := t.TempDir()
	priv := filepath.Join(home, "id")
	if err := os.WriteFile(priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := account.Account{ID: "x", Email: "x@y", SigningFormat: account.SigningSSH, SigningKey: priv}
	if _, err := PublicSigningKey(a); err == nil {
		t.Fatal("no .pub beside the key: an error")
	} else if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("private key material leaked")
	}
	if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 AAAAB comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := PublicSigningKey(a); err != nil || got != "ssh-ed25519 AAAAB" {
		t.Errorf("got %q, %v", got, err)
	}
	a.SigningKey = "key::ssh-ed25519 ZZZ me"
	if got, err := PublicSigningKey(a); err != nil || got != "ssh-ed25519 ZZZ" {
		t.Errorf("literal: %q, %v", got, err)
	}
	a.SigningFormat = account.SigningGPG
	if _, err := PublicSigningKey(a); err == nil {
		t.Error("gpg has no public key file")
	}
}
