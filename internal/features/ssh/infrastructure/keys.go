// Package infrastructure creates SSH keys, reads public keys, tests logins
// and talks to the OS key agent; and finds the repo and real ssh for the
// hook. It never stores passphrases. Import it as sshinfra.
package infrastructure

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	accountinfra "github.com/macdipu/gitswitch/internal/features/account/infrastructure"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Keys works with key files, the agent and login tests. It implements the
// ssh domain's KeyTool. FS makes file changes (or prints them in dry-run).
type Keys struct {
	FS *filesystem.FS
}

// Generate creates an ed25519 key at the account's default path. With
// interactive=true ssh-keygen asks for the passphrase on the terminal;
// otherwise passphrase is used as given (possibly empty).
func (k *Keys) Generate(a *account.Account, interactive bool, passphrase string) (string, error) {
	path := accountinfra.DefaultKeyPath(a.ID)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; use it with `gitswitch key pick` or remove it first", path)
	}
	if k.FS.Dry("ssh-keygen -t ed25519 -f " + path) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	comment := a.Email
	if comment == "" {
		comment = "gitswitch-" + a.ID
	}
	if !interactive {
		// Made here rather than with `ssh-keygen -N <passphrase>`, which
		// would show the passphrase to every user of `ps`.
		if err := writeKeyPair(path, comment, passphrase); err != nil {
			return "", fmt.Errorf("could not create the key: %v", err)
		}
		return path, nil
	}
	cmd := exec.Command(process.FindSSHTool("ssh-keygen"), "-t", "ed25519", "-C", comment, "-f", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ssh-keygen failed: %v", err)
	}
	return path, nil
}

// writeKeyPair writes a new ed25519 key pair in OpenSSH's format, the same
// files `ssh-keygen -t ed25519` makes: the private key (encrypted when a
// passphrase is given, with bcrypt and aes256-ctr) readable only by you,
// and the public key next to it as path.pub.
func writeKeyPair(path, comment, passphrase string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = gossh.MarshalPrivateKey(priv, comment)
	} else {
		block, err = gossh.MarshalPrivateKeyWithPassphrase(priv, comment, []byte(passphrase))
	}
	if err != nil {
		return err
	}
	sshPub, err := gossh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	pubLine := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sshPub))) + " " + comment + "\n"
	if err := filesystem.WriteAtomic(path+".pub", []byte(pubLine), 0o644); err != nil {
		return err
	}
	if err := filesystem.WriteAtomic(path, pem.EncodeToMemory(block), 0o600); err != nil {
		os.Remove(path + ".pub")
		return err
	}
	return nil
}

// PublicKey returns the account's public key text.
func (k *Keys) PublicKey(a *account.Account) (string, error) {
	if a.Key == "" {
		return "", fmt.Errorf("account %s has no key yet", a.ID)
	}
	b, err := os.ReadFile(filesystem.ExpandHome(a.Key) + ".pub")
	if err != nil {
		return "", fmt.Errorf("public key not found: %v", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Exists reports whether the account's private key file is on disk.
func (k *Keys) Exists(a *account.Account) bool {
	if a.Key == "" {
		return false
	}
	_, err := os.Stat(filesystem.ExpandHome(a.Key))
	return err == nil
}

// Test logs in to the host with only this account's key. batch=true never
// asks for a passphrase (used by the apps).
func (k *Keys) Test(a *account.Account, sshBin string, batch bool) ssh.TestResult {
	if !k.Exists(a) {
		return ssh.TestResult{Output: "key file not found: " + a.Key}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{"-i", filesystem.ExpandHome(a.Key), "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=15", "-o", "AddKeysToAgent=yes"}
	if runtime.GOOS == "darwin" {
		args = append(args, "-o", "IgnoreUnknown=UseKeychain", "-o", "UseKeychain=yes")
	}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	cmd := exec.CommandContext(ctx, sshBin, append(args, "-T", "git@"+a.Host)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if batch {
		process.HideWindow(cmd)
	} else {
		cmd.Stdin = os.Stdin
	}
	_ = cmd.Run() // GitHub exits 1 even on success
	out := strings.TrimSpace(buf.String())
	if user := ssh.ParseGreeting(out); user != "" {
		return ssh.TestResult{OK: true, User: user, Output: out}
	}
	if ctx.Err() != nil {
		out = "timed out connecting to " + a.Host
	}
	if batch && strings.Contains(out, "Permission denied") {
		out += "\nIf the key has a passphrase, run `gitswitch key agent " + a.ID + "` once in a terminal."
	}
	return ssh.TestResult{Output: out}
}

// AddToAgent loads the key into the OS agent (and macOS Keychain).
func (k *Keys) AddToAgent(a *account.Account) error {
	var args []string
	if runtime.GOOS == "darwin" {
		args = append(args, "--apple-use-keychain")
	}
	cmd := exec.Command(process.FindSSHTool("ssh-add"), append(args, filesystem.ExpandHome(a.Key))...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// List finds private keys in ~/.ssh that have a matching .pub file.
func (k *Keys) List(c *account.Config) []ssh.Existing {
	matches, _ := filepath.Glob(filepath.Join(filesystem.HomeDir(), ".ssh", "*.pub"))
	var out []ssh.Existing
	for _, pub := range matches {
		priv := strings.TrimSuffix(pub, ".pub")
		if _, err := os.Stat(priv); err != nil {
			continue
		}
		e := ssh.Existing{Path: priv}
		b, _ := os.ReadFile(pub)
		if f := strings.Fields(string(b)); len(f) >= 3 {
			e.Comment = strings.Join(f[2:], " ")
		}
		for _, a := range c.Accounts {
			if a.Key != "" && filepath.Clean(filesystem.ExpandHome(a.Key)) == filepath.Clean(priv) {
				e.UsedBy = a.ID
			}
		}
		out = append(out, e)
	}
	return out
}

// AgentFingerprints lists keys loaded in ssh-agent. ok=false: no agent.
func (k *Keys) AgentFingerprints() (fps map[string]bool, ok bool) {
	cmd := exec.Command(process.FindSSHTool("ssh-add"), "-l")
	process.HideWindow(cmd)
	out, err := cmd.Output()
	fps = map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			fps[f[1]] = true
		}
	}
	if err != nil && len(fps) == 0 {
		// exit status 1 = agent running with no keys; anything else = no agent
		if ee, isExit := err.(*exec.ExitError); isExit && ee.ExitCode() == 1 {
			return fps, true
		}
		return fps, false
	}
	return fps, true
}

// Fingerprint returns the SHA256 fingerprint of the account's public key.
func (k *Keys) Fingerprint(a *account.Account) string {
	if a.Key == "" {
		return ""
	}
	cmd := exec.Command(process.FindSSHTool("ssh-keygen"), "-lf", filesystem.ExpandHome(a.Key)+".pub")
	process.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if f := strings.Fields(string(out)); len(f) >= 2 {
		return f[1]
	}
	return ""
}

// KnownHost reports whether the host's key is in known_hosts.
func (k *Keys) KnownHost(host string) bool { return KnownHost(process.FindSSH(""), host) }

// KnownHost reports whether ssh would find the host's key in a known_hosts
// file. It asks ssh (`ssh -G`) where the host really is, because
// ~/.ssh/config may send it elsewhere: GitHub over port 443 is recorded as
// [ssh.github.com]:443, which `ssh-keygen -F github.com` never finds.
func KnownHost(sshBin, host string) bool {
	name, port := host, "22"
	var files []string
	cmd := exec.Command(sshBin, "-G", host)
	process.HideWindow(cmd)
	if out, err := cmd.Output(); err == nil {
		alias := ""
		for _, l := range strings.Split(string(out), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
			switch strings.ToLower(k) {
			case "hostname":
				name = v
			case "port":
				port = v
			case "hostkeyalias":
				alias = v
			case "userknownhostsfile", "globalknownhostsfile":
				files = append(files, strings.Fields(v)...)
			}
		}
		if alias != "" && alias != "none" {
			name = alias
		}
	}
	if port != "22" && port != "" {
		name = "[" + name + "]:" + port
	}
	if len(files) == 0 {
		files = []string{""} // ssh-keygen's default, ~/.ssh/known_hosts
	}
	for _, f := range files {
		args := []string{"-F", name}
		if f != "" {
			f = filesystem.ExpandHome(f)
			if _, err := os.Stat(f); err != nil {
				continue
			}
			args = append(args, "-f", f)
		}
		cmd := exec.Command(process.FindSSHTool("ssh-keygen"), args...)
		process.HideWindow(cmd)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}
