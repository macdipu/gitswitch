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
	c, err := h.Accounts.LoadChecked()
	sshBin := h.Transport.Find(c.SSHPath)

	if inv.Probe {
		h.Repos.RememberProbe()
		return h.Transport.Exec(sshBin, args)
	}
	if err != nil {
		// Without the settings we cannot tell which key to use. Connecting
		// anyway could log in as the wrong account, so stop instead.
		fmt.Fprintf(h.Notice, "gitswitch: %v\ngitswitch: stopped so git does not connect with the wrong key. Fix your git config (try `git config --global --list`), then run the command again.\n", err)
		return 255 // what ssh returns when it cannot connect
	}
	cands := resolve.Candidates(c, inv.Host)
	if inv.Host == "" || len(cands) == 0 {
		return h.Transport.Exec(sshBin, args) // a host we don't manage: plain ssh
	}

	gitDir := h.Repos.CurrentGitDir(inv)
	var term ssh.Conversation
	if !inv.Batch {
		term = h.Prompter.OpenTerminal()
	}
	if term != nil {
		defer term.Close()
	}
	in := resolve.Input{RepoPath: inv.RepoPath, RealPath: filesystem.RealPath, FoldCase: filesystem.CaseInsensitiveFS()}
	if gitDir != "" {
		in.SavedID = h.Repos.SavedAccount(gitDir)
		in.Worktree = func() string { return h.Repos.Worktree(gitDir) }
	}
	everyTime := resolve.AsksEveryTime(in.SavedID) // the repo asks on every connection
	d := resolve.Decide(c, cands, in)
	unlock := func() {}
	if d == nil && (term != nil || c.GUIPrompt && !inv.Batch) {
		// Ask one at a time: git runs several ssh at once for fetch --jobs
		// and submodules. The one before us may have saved an answer.
		unlock = h.Prompter.Lock(c.PromptTimeout + 5)
		if gitDir != "" && !everyTime {
			if in.SavedID = h.Repos.SavedAccount(gitDir); in.SavedID != "" {
				d = resolve.Decide(c, cands, in)
			}
		}
	}
	if d == nil {
		label := strings.TrimSuffix(inv.RepoPath, ".git")
		if label == "" {
			label = inv.Host
		}
		if everyTime {
			label += " (this repo asks every time)"
		}
		def := resolve.DefaultAmong(c, cands) // nil: no default to fall back on
		if term != nil {
			d = term.Ask(label, cands, def, c.PromptTimeout)
		} else if c.GUIPrompt && !inv.Batch {
			d = h.Prompter.Dialog(label, cands, def, c.PromptTimeout)
		}
		if d != nil && everyTime {
			d.Save = false // the repo asked to be asked every time
		}
		if d == nil && def == nil {
			// No default to fall back on, and guessing could log in (and
			// later commit) as the wrong account: stop.
			unlock()
			fmt.Fprintf(h.Notice, "gitswitch: no account chosen for %s, and you have no default account, so git did not connect.\n"+
				"gitswitch: inside the repo run `gitswitch use <account>`, set a default with `gitswitch default <account>`, or run the git command in a terminal to be asked.\n", label)
			return 255
		}
		if d == nil {
			d = &resolve.Decision{Account: def, Reason: resolve.Default}
			fmt.Fprintf(h.Notice, "gitswitch: no account saved for %s, using default %q for this connection only. Save one with: gitswitch use <account>\n", label, def.ID)
		}
	}
	if d.Save {
		if gitDir == "" {
			fmt.Fprintf(h.Notice, "gitswitch: no repo found to save %s for; using it this time only\n", d.Account.ID)
		} else if err := h.Repos.SaveAccount(gitDir, d.Account.ID); err != nil {
			fmt.Fprintf(h.Notice, "gitswitch: could not save %s for this repo (%v); using it this time only\n", d.Account.ID, err)
		} else {
			fmt.Fprintf(h.Notice, "gitswitch: %s saved for this repo\n", d.Account.ID)
		}
	}
	unlock() // saved: the next one waiting can use it
	// A fallback nobody chose (timeout, no terminal, no answer) is used for
	// this connection only. Writing it as the repo's commit identity would
	// silently defeat user.useConfigOnly and commit as the wrong account.
	if d.Reason != resolve.Default {
		h.Identity.ApplyIdentity(gitDir, d.Account)
	}
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
