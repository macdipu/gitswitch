package domain

import (
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
)

// KeyTool works with SSH key files, the key agent and login tests (through
// ssh-keygen, ssh-add and ssh). It never stores passphrases.
type KeyTool interface {
	// Generate creates an ed25519 key at the account's default path. With
	// interactive=true ssh-keygen asks for the passphrase on the terminal.
	Generate(a *account.Account, interactive bool, passphrase string) (string, error)
	PublicKey(a *account.Account) (string, error)
	// Exists reports whether the account's private key file is on disk.
	Exists(a *account.Account) bool
	// List finds key pairs in ~/.ssh.
	List(c *account.Config) []Existing
	// AgentFingerprints lists keys loaded in ssh-agent. ok=false: no agent.
	AgentFingerprints() (fps map[string]bool, ok bool)
	Fingerprint(a *account.Account) string
	// Test logs in to the host with only this account's key. batch=true
	// never asks for a passphrase.
	Test(a *account.Account, sshBin string, batch bool) TestResult
	// AddToAgent loads the key into the OS agent (and macOS Keychain).
	AddToAgent(a *account.Account) error
}

// HookRepos finds the repo git is working on during the hook and reads or
// saves its account.
type HookRepos interface {
	// CurrentGitDir finds the repo git is working on, including mid-clone;
	// "" when there is none.
	CurrentGitDir(inv Invocation) string
	// RememberProbe stores the repo seen during git's "-G" probe, for the
	// clone that follows.
	RememberProbe()
	SavedAccount(gitDir string) string
	SaveAccount(gitDir, id string) error
	Worktree(gitDir string) string
}

// Transport runs the real ssh program.
type Transport interface {
	// Find picks the ssh program (override is the user's setting).
	Find(override string) string
	// HostKeyPolicy reads StrictHostKeyChecking from the user's ssh config.
	HostKeyPolicy(sshBin string, args []string) string
	// Exec hands over to ssh and returns its exit code.
	Exec(bin string, args []string) int
}

// Prompter asks the user which account to use when no rule decides.
type Prompter interface {
	// OpenTerminal opens the user's terminal, or returns nil when git runs
	// without one (IDE panels, GUI clients, cron).
	OpenTerminal() Conversation
	// Dialog asks with a small native popup; nil when not answered.
	Dialog(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision
}

// Conversation is an open terminal to ask on.
type Conversation interface {
	Ask(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision
	Close()
}
