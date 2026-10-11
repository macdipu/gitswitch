// Package infrastructure creates SSH keys, reads public keys, tests logins
// and talks to the OS key agent; and finds the repo and real ssh for the
// hook. It never stores passphrases. Import it as sshinfra.
package infrastructure

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	args := []string{"-t", "ed25519", "-C", comment, "-f", path}
	if !interactive {
		args = append(args, "-N", passphrase, "-q")
	}
	cmd := exec.Command(process.FindSSHTool("ssh-keygen"), args...)
	if interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("ssh-keygen failed: %v", err)
		}
		return path, nil
	}
	process.HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ssh-keygen failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
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
func (k *Keys) KnownHost(host string) bool {
	cmd := exec.Command(process.FindSSHTool("ssh-keygen"), "-F", host)
	process.HideWindow(cmd)
	return cmd.Run() == nil
}
