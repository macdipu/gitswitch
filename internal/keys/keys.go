// Package keys creates SSH keys, reads public keys, tests logins and talks
// to the OS key agent. It never stores passphrases.
package keys

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/sys"
)

// Generate creates an ed25519 key at the account's default path. With
// interactive=true ssh-keygen asks for the passphrase on the terminal;
// otherwise passphrase is used as given (possibly empty).
func Generate(fs *sys.FS, a *account.Account, interactive bool, passphrase string) (string, error) {
	path := account.DefaultKeyPath(a.ID)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; use it with `gitswitch key pick` or remove it first", path)
	}
	if fs.Dry("ssh-keygen -t ed25519 -f " + path) {
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
	cmd := exec.Command(sys.FindSSHTool("ssh-keygen"), args...)
	if interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("ssh-keygen failed: %v", err)
		}
		return path, nil
	}
	sys.HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ssh-keygen failed: %v %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// PublicKey returns the account's public key text.
func PublicKey(a *account.Account) (string, error) {
	if a.Key == "" {
		return "", fmt.Errorf("account %s has no key yet", a.ID)
	}
	b, err := os.ReadFile(sys.ExpandHome(a.Key) + ".pub")
	if err != nil {
		return "", fmt.Errorf("public key not found: %v", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Exists reports whether the account's private key file is on disk.
func Exists(a *account.Account) bool {
	if a.Key == "" {
		return false
	}
	_, err := os.Stat(sys.ExpandHome(a.Key))
	return err == nil
}

// AddKeyPage is where the user pastes a new public key on the host.
func AddKeyPage(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "github.com":
		return "https://github.com/settings/ssh/new"
	case h == "gitlab.com":
		return "https://gitlab.com/-/user_settings/ssh_keys"
	case h == "bitbucket.org":
		return "https://bitbucket.org/account/settings/ssh-keys/"
	case strings.Contains(h, "github"):
		return "https://" + h + "/settings/ssh/new"
	case strings.Contains(h, "gitlab"):
		return "https://" + h + "/-/user_settings/ssh_keys"
	}
	return ""
}

// TestResult is the outcome of a login test.
type TestResult struct {
	OK     bool   `json:"ok"`
	User   string `json:"user"`
	Output string `json:"output"`
}

var greetings = []*regexp.Regexp{
	regexp.MustCompile(`Hi ([^!\s]+)!`),                   // GitHub
	regexp.MustCompile(`Welcome to GitLab, @([^!\s]+)!`),  // GitLab
	regexp.MustCompile(`logged in as ([^\s.]+)`),          // Bitbucket
	regexp.MustCompile(`(?i)authenticated as ([^\s.!]+)`), // others
}

// ParseGreeting finds the logged-in username in a host's ssh greeting.
func ParseGreeting(out string) string {
	for _, re := range greetings {
		if m := re.FindStringSubmatch(out); m != nil {
			return strings.TrimPrefix(m[1], "@")
		}
	}
	return ""
}

// Test logs in to the host with only this account's key. batch=true never
// asks for a passphrase (used by the apps).
func Test(a *account.Account, sshBin string, batch bool) TestResult {
	if !Exists(a) {
		return TestResult{Output: "key file not found: " + a.Key}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	args := []string{"-i", sys.ExpandHome(a.Key), "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=accept-new",
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
		sys.HideWindow(cmd)
	} else {
		cmd.Stdin = os.Stdin
	}
	_ = cmd.Run() // GitHub exits 1 even on success
	out := strings.TrimSpace(buf.String())
	if user := ParseGreeting(out); user != "" {
		return TestResult{OK: true, User: user, Output: out}
	}
	if ctx.Err() != nil {
		out = "timed out connecting to " + a.Host
	}
	if batch && strings.Contains(out, "Permission denied") {
		out += "\nIf the key has a passphrase, run `gitswitch key agent " + a.ID + "` once in a terminal."
	}
	return TestResult{Output: out}
}

// AddToAgent loads the key into the OS agent (and macOS Keychain).
func AddToAgent(a *account.Account) error {
	var args []string
	if runtime.GOOS == "darwin" {
		args = append(args, "--apple-use-keychain")
	}
	cmd := exec.Command(sys.FindSSHTool("ssh-add"), append(args, sys.ExpandHome(a.Key))...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Existing is a key pair found in ~/.ssh.
type Existing struct {
	Path    string `json:"path"`
	Comment string `json:"comment"`
	UsedBy  string `json:"usedBy"`
}

// List finds private keys in ~/.ssh that have a matching .pub file.
func List(c *account.Config) []Existing {
	matches, _ := filepath.Glob(filepath.Join(sys.HomeDir(), ".ssh", "*.pub"))
	var out []Existing
	for _, pub := range matches {
		priv := strings.TrimSuffix(pub, ".pub")
		if _, err := os.Stat(priv); err != nil {
			continue
		}
		k := Existing{Path: priv}
		b, _ := os.ReadFile(pub)
		if f := strings.Fields(string(b)); len(f) >= 3 {
			k.Comment = strings.Join(f[2:], " ")
		}
		for _, a := range c.Accounts {
			if a.Key != "" && filepath.Clean(sys.ExpandHome(a.Key)) == filepath.Clean(priv) {
				k.UsedBy = a.ID
			}
		}
		out = append(out, k)
	}
	return out
}

// AgentFingerprints lists keys loaded in ssh-agent. ok=false: no agent.
func AgentFingerprints() (fps map[string]bool, ok bool) {
	cmd := exec.Command(sys.FindSSHTool("ssh-add"), "-l")
	sys.HideWindow(cmd)
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
func Fingerprint(a *account.Account) string {
	if a.Key == "" {
		return ""
	}
	cmd := exec.Command(sys.FindSSHTool("ssh-keygen"), "-lf", sys.ExpandHome(a.Key)+".pub")
	sys.HideWindow(cmd)
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
func KnownHost(host string) bool {
	cmd := exec.Command(sys.FindSSHTool("ssh-keygen"), "-F", host)
	sys.HideWindow(cmd)
	return cmd.Run() == nil
}
