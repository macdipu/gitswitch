// Package sshhook is what git runs on every clone, pull, push and fetch
// (git's core.sshCommand points at `gitswitch ssh`). It decides the
// account, sets the repo's identity, then hands over to the real ssh.
//
// It must never write to standard output: git speaks its protocol over it.
package sshhook

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gitswitch/internal/account"
	"gitswitch/internal/gitcli"
	"gitswitch/internal/prompt"
	"gitswitch/internal/resolve"
	"gitswitch/internal/sys"
)

// Invocation is what git asked ssh to do.
type Invocation struct {
	Args     []string // original args, passed to ssh untouched
	Host     string   // e.g. github.com
	RepoPath string   // e.g. acme-corp/api.git
	Probe    bool     // git's "-G" variant probe: never prompt
}

// ssh options that take a value (from ssh(1)).
const argOpts = "BbcDEeFIiJLlmOoPpQRSWw"

// Parse reads git's ssh command line.
func Parse(args []string) Invocation {
	inv := Invocation{Args: args}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = args[i+1:]
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if strings.Contains(a[1:], "G") && !strings.ContainsAny(a[1:2], argOpts) {
				inv.Probe = true
			}
			if len(a) == 2 && strings.ContainsRune(argOpts, rune(a[1])) {
				i++ // skip the value
			}
			continue
		}
		rest = args[i:]
		break
	}
	if len(rest) == 0 {
		return inv
	}
	dest := rest[0]
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	inv.Host = strings.ToLower(dest)
	inv.RepoPath = RepoPathFromCommand(strings.Join(rest[1:], " "))
	return inv
}

// RepoPathFromCommand pulls owner/repo.git out of "git-upload-pack 'owner/repo.git'".
func RepoPathFromCommand(cmd string) string {
	i, j := strings.Index(cmd, "'"), strings.LastIndex(cmd, "'")
	var p string
	if i >= 0 && j > i {
		p = strings.ReplaceAll(cmd[i+1:j], `'\''`, `'`)
	} else if f := strings.Fields(cmd); len(f) >= 2 {
		p = strings.Trim(f[len(f)-1], `"`)
	}
	return strings.TrimLeft(strings.TrimPrefix(p, "~"), "/")
}

// Run is the whole hook. It returns ssh's exit code.
func Run(args []string) int {
	git := &gitcli.Runner{}
	store := &account.Store{Git: git, FS: &sys.FS{}}
	inv := Parse(args)
	c := store.Load()
	sshBin := sys.FindSSH(c.SSHPath)

	if inv.Probe {
		rememberProbe()
		return sys.ExecReplace(sshBin, args)
	}
	cands := resolve.Candidates(c, inv.Host)
	if inv.Host == "" || len(cands) == 0 {
		return sys.ExecReplace(sshBin, args) // a host we don't manage: plain ssh
	}

	gitDir := currentGitDir(git, inv)
	term := sys.OpenTerminal()
	if term != nil {
		defer term.Close()
	}
	in := resolve.Input{RepoPath: inv.RepoPath}
	if gitDir != "" {
		in.SavedID = git.RepoGet(gitDir, "gitswitch.account")
		in.Worktree = func() string { return git.WorktreeOf(gitDir) }
	}
	d := resolve.Decide(c, cands, in)
	if d == nil {
		label := strings.TrimSuffix(inv.RepoPath, ".git")
		if label == "" {
			label = inv.Host
		}
		def := resolve.DefaultAmong(c, cands)
		if term != nil {
			d = prompt.Terminal(term, label, cands, def, c.PromptTimeout)
		} else if c.GUIPrompt {
			d = prompt.Dialog(label, cands, def, c.PromptTimeout)
		}
		if d == nil {
			d = &resolve.Decision{Account: def, Reason: resolve.Default}
			fmt.Fprintf(os.Stderr, "gitswitch: no account saved for %s, using default %q. Save one with: gitswitch use <account>\n", label, def.ID)
		}
	}
	if d.Save && gitDir != "" {
		if err := git.RepoSet(gitDir, "gitswitch.account", d.Account.ID); err == nil {
			fmt.Fprintf(os.Stderr, "gitswitch: %s saved for this repo\n", d.Account.ID)
		}
	}
	store.ApplyIdentity(gitDir, d.Account)
	return sys.ExecReplace(sshBin, SSHArgs(d.Account, args, term == nil && hostKeyPolicy(sshBin, args) == "ask"))
}

// SSHArgs builds the real ssh command line for an account.
func SSHArgs(a *account.Account, gitArgs []string, acceptNewHost bool) []string {
	var full []string
	if a.Key != "" {
		full = append(full, "-i", sys.ExpandHome(a.Key), "-o", "IdentitiesOnly=yes")
	}
	full = append(full, "-o", "AddKeysToAgent=yes")
	// With no terminal (IDE panels) ssh cannot ask "trust this host?" on the
	// first connection. For hosts you set up an account for, accept a host
	// seen for the first time; a changed host key is still refused.
	if acceptNewHost {
		full = append(full, "-o", "StrictHostKeyChecking=accept-new")
	}
	if runtime.GOOS == "darwin" {
		full = append(full, "-o", "IgnoreUnknown=UseKeychain", "-o", "UseKeychain=yes")
	}
	return append(full, gitArgs...)
}

// currentGitDir finds the repo git is working on, including mid-clone.
// Git removes GIT_DIR before the real transport call, so for clones we use
// the path seen by git's earlier "-G" probe (which still has GIT_DIR), and
// only trust a folder whose remote URL is the one being fetched.
func currentGitDir(git *gitcli.Runner, inv Invocation) string {
	if d := os.Getenv("GIT_DIR"); d != "" {
		return absFromWd(d)
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	var cands []string
	if st, err := os.Stat(probeFile(wd)); err == nil && time.Since(st.ModTime()) < 5*time.Minute {
		if b, err := os.ReadFile(probeFile(wd)); err == nil {
			cands = append(cands, strings.TrimSpace(string(b)))
		}
	}
	if inv.RepoPath != "" {
		base := strings.TrimSuffix(filepath.Base(inv.RepoPath), ".git")
		cands = append(cands, filepath.Join(wd, base, ".git"))
	}
	for _, c := range cands {
		if hasRemote(git, c, inv.Host, inv.RepoPath) {
			return c
		}
	}
	return git.GitDirFor(wd)
}

func absFromWd(d string) string {
	if !filepath.IsAbs(d) {
		if wd, err := os.Getwd(); err == nil {
			d = filepath.Join(wd, d)
		}
	}
	return d
}

// probeFile lives in the user's private cache folder, not the shared /tmp.
func probeFile(wd string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(wd))))
	return filepath.Join(sys.CacheDir(), "probe-"+hex.EncodeToString(h[:8]))
}

// rememberProbe stores the repo seen during git's "-G" probe.
func rememberProbe() {
	d := os.Getenv("GIT_DIR")
	if d == "" {
		return
	}
	if wd, err := os.Getwd(); err == nil {
		p := probeFile(wd)
		if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
			_ = os.WriteFile(p, []byte(absFromWd(d)), 0o600)
		}
	}
}

func hasRemote(git *gitcli.Runner, gitDir, host, repoPath string) bool {
	if gitDir == "" || repoPath == "" {
		return false
	}
	if _, err := os.Stat(gitDir); err != nil {
		return false
	}
	out, _ := git.Run("--git-dir="+gitDir, "config", "--local", "--get-regexp", `^remote\..*\.url$`)
	want := strings.ToLower(strings.TrimSuffix(repoPath, ".git"))
	for _, line := range strings.Split(out, "\n") {
		if _, u, ok := strings.Cut(line, " "); ok {
			h, p := gitcli.ParseRemote(u)
			if gitcli.SameHost(h, host) && strings.ToLower(strings.TrimSuffix(p, ".git")) == want {
				return true
			}
		}
	}
	return false
}

// hostKeyPolicy reads StrictHostKeyChecking from the user's ssh config.
func hostKeyPolicy(sshBin string, args []string) string {
	cmd := exec.Command(sshBin, append([]string{"-G"}, destArgs(args)...)...)
	sys.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "stricthostkeychecking "); ok {
			switch v = strings.ToLower(strings.TrimSpace(v)); v {
			case "false":
				return "no"
			case "true":
				return "yes"
			default:
				return v
			}
		}
	}
	return ""
}

// destArgs keeps port/user/config options and the destination, dropping
// the remote command, for an `ssh -G` lookup.
func destArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if len(a) == 2 && strings.ContainsRune(argOpts, rune(a[1])) && i+1 < len(args) {
				if a == "-p" || a == "-l" || a == "-F" || a == "-o" {
					out = append(out, a, args[i+1])
				}
				i++
			}
			continue
		}
		return append(out, a)
	}
	return out
}
