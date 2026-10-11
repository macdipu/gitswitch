// Package application holds the ssh use cases: the hook git runs on every
// clone, pull, push and fetch (git's core.sshCommand points at
// `gitswitch ssh`), and working with an account's key. Import it as sshapp.
package application

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Hook decides the account, sets the repo's identity, then hands over to
// the real ssh.
//
// It must never write to standard output: git speaks its protocol over it.
// Notices go to Notice (standard error).
type Hook struct {
	Accounts  account.Store
	Identity  account.RepoAccounts
	Repos     ssh.HookRepos
	Transport ssh.Transport
	Prompter  ssh.Prompter
	Notice    io.Writer
}

// Run is the whole hook. It returns ssh's exit code.
func (h *Hook) Run(args []string) int {
	inv := ssh.Parse(args)
	c := h.Accounts.Load()
	sshBin := h.Transport.Find(c.SSHPath)

	if inv.Probe {
		h.Repos.RememberProbe()
		return h.Transport.Exec(sshBin, args)
	}
	cands := resolve.Candidates(c, inv.Host)
	if inv.Host == "" || len(cands) == 0 {
		return h.Transport.Exec(sshBin, args) // a host we don't manage: plain ssh
	}

	gitDir := h.Repos.CurrentGitDir(inv)
	term := h.Prompter.OpenTerminal()
	if term != nil {
		defer term.Close()
	}
	in := resolve.Input{RepoPath: inv.RepoPath, RealPath: filesystem.RealPath, FoldCase: filesystem.CaseInsensitiveFS()}
	if gitDir != "" {
		in.SavedID = h.Repos.SavedAccount(gitDir)
		in.Worktree = func() string { return h.Repos.Worktree(gitDir) }
	}
	d := resolve.Decide(c, cands, in)
	if d == nil {
		label := strings.TrimSuffix(inv.RepoPath, ".git")
		if label == "" {
			label = inv.Host
		}
		def := resolve.DefaultAmong(c, cands)
		if term != nil {
			d = term.Ask(label, cands, def, c.PromptTimeout)
		} else if c.GUIPrompt {
			d = h.Prompter.Dialog(label, cands, def, c.PromptTimeout)
		}
		if d == nil {
			d = &resolve.Decision{Account: def, Reason: resolve.Default}
			fmt.Fprintf(h.Notice, "gitswitch: no account saved for %s, using default %q. Save one with: gitswitch use <account>\n", label, def.ID)
		}
	}
	if d.Save && gitDir != "" {
		if err := h.Repos.SaveAccount(gitDir, d.Account.ID); err == nil {
			fmt.Fprintf(h.Notice, "gitswitch: %s saved for this repo\n", d.Account.ID)
		}
	}
	h.Identity.ApplyIdentity(gitDir, d.Account)
	return h.Transport.Exec(sshBin, SSHArgs(d.Account, args, term == nil && h.Transport.HostKeyPolicy(sshBin, args) == "ask"))
}

// SSHArgs builds the real ssh command line for an account.
func SSHArgs(a *account.Account, gitArgs []string, acceptNewHost bool) []string {
	var full []string
	if a.Key != "" {
		full = append(full, "-i", filesystem.ExpandHome(a.Key), "-o", "IdentitiesOnly=yes")
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
