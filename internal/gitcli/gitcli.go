// Package gitcli is the only code that runs the git program. Everything
// that reads or changes git state goes through a Runner, so dry-run mode,
// hidden console windows and error messages are handled in one place.
//
// As the project grows into a full git client, porcelain parsers (status,
// diff, log, branches) belong here, each returning plain Go values.
package gitcli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/macdipu/gitswitch/internal/sys"
)

// Runner runs git. With DryRun set, commands that would change git config
// are printed to Out instead of run; reads still run.
type Runner struct {
	DryRun bool
	Out    io.Writer
}

// Run runs git and returns its trimmed standard output.
func (r *Runner) Run(args ...string) (string, error) {
	if r != nil && r.DryRun {
		if ca := ConfigArgs(args); ca != nil && IsConfigWrite(ca) {
			out := r.Out
			if out == nil {
				out = os.Stdout
			}
			fmt.Fprintln(out, "[dry-run] git "+ShellQuote(args))
			return "", nil
		}
	}
	cmd := exec.Command("git", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	sys.HideWindow(cmd)
	err := cmd.Run()
	text := strings.TrimRight(out.String(), "\r\n")
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return text, errors.New(msg)
	}
	return text, nil
}

// Global runs `git config --global <args>`.
func (r *Runner) Global(args ...string) (string, error) {
	return r.Run(append([]string{"config", "--global"}, args...)...)
}

// GlobalGet reads one global config value ("" when unset).
func (r *Runner) GlobalGet(key string) string {
	v, _ := r.Global("--get", key)
	return v
}

// RepoGet reads a key from a repo's own config ("" when unset).
func (r *Runner) RepoGet(gitDir, key string) string {
	v, _ := r.Run("--git-dir="+gitDir, "config", "--local", "--get", key)
	return v
}

func (r *Runner) RepoSet(gitDir, key, value string) error {
	_, err := r.Run("--git-dir="+gitDir, "config", "--local", key, value)
	return err
}

func (r *Runner) RepoUnset(gitDir, key string) error {
	_, err := r.Run("--git-dir="+gitDir, "config", "--local", "--unset", key)
	return err
}

// GitDirFor returns the absolute .git folder for a path, or "".
func (r *Runner) GitDirFor(path string) string {
	out, err := r.Run("-C", path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	return out
}

// WorktreeOf returns the working folder for a git dir.
func (r *Runner) WorktreeOf(gitDir string) string {
	if strings.HasSuffix(filepath.ToSlash(gitDir), "/.git") {
		return filepath.Dir(gitDir)
	}
	if out, err := r.Run("--git-dir="+gitDir, "rev-parse", "--show-toplevel"); err == nil && out != "" {
		return out
	}
	return gitDir
}

// ConfigArgs returns the arguments after "config" in a git command line,
// or nil when the command is not `git config`.
func ConfigArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-C" || a == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "config" {
			return args[i+1:]
		}
		return nil
	}
	return nil
}

// IsConfigWrite reports whether `git config <args>` changes anything.
func IsConfigWrite(args []string) bool {
	positional := 0
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--list", "-l", "--show-origin", "--show-scope":
			return false
		case "--unset", "--unset-all", "--add", "--replace-all", "--remove-section", "--rename-section":
			return true
		case "--file", "-f", "--blob", "--type", "--default", "--comment":
			i++
		default:
			if !strings.HasPrefix(a, "-") {
				positional++
			}
		}
	}
	return positional >= 2
}

// ShellQuote renders arguments for display.
func ShellQuote(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\*?") {
			out = append(out, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
		} else {
			out = append(out, a)
		}
	}
	return strings.Join(out, " ")
}

// ParseRemote splits a remote URL into host and repo path.
func ParseRemote(u string) (host, path string) {
	u = strings.TrimSpace(u)
	if u == "" {
		return
	}
	if strings.Contains(u, "://") {
		pu, err := url.Parse(u)
		if err != nil {
			return
		}
		return strings.ToLower(pu.Hostname()), strings.Trim(pu.Path, "/")
	}
	// scp-like: git@host:owner/repo.git
	if i := strings.Index(u, ":"); i > 0 && !strings.Contains(u[:i], "/") {
		h := u[:i]
		if j := strings.LastIndex(h, "@"); j >= 0 {
			h = h[j+1:]
		}
		return strings.ToLower(h), strings.Trim(u[i+1:], "/")
	}
	return
}

// CanonicalHost maps the "SSH over port 443" hosts to their main host.
func CanonicalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	switch h {
	case "ssh.github.com":
		return "github.com"
	case "altssh.gitlab.com":
		return "gitlab.com"
	case "altssh.bitbucket.org":
		return "bitbucket.org"
	}
	return h
}

// SameHost compares hosts after mapping port-443 aliases.
func SameHost(a, b string) bool { return CanonicalHost(a) == CanonicalHost(b) }
