package application

import (
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Keys runs the key use cases: show a public key, list keys on disk, check
// the agent and test logins.
type Keys struct {
	Accounts account.Store
	Tool     ssh.KeyTool
}

// PublicKey returns an account's public key and how to add it on the host.
func (k *Keys) PublicKey(id string) (key string, help ssh.KeyHelp, err error) {
	acc, err := k.Accounts.Load().Lookup(id)
	if err != nil {
		return "", help, err
	}
	key, err = k.Tool.PublicKey(acc)
	return key, ssh.AddKeyHelp(acc.Host), err
}

// Existing lists key pairs in ~/.ssh.
func (k *Keys) Existing() []ssh.Existing { return k.Tool.List(k.Accounts.Load()) }

// HasKey reports whether the account's private key file is on disk.
func (k *Keys) HasKey(a *account.Account) bool { return k.Tool.Exists(a) }

// AgentStatus reports, per account id, whether its key is loaded in
// ssh-agent. ok=false means no agent is running.
func (k *Keys) AgentStatus() (loaded map[string]bool, ok bool) {
	fps, ok := k.Tool.AgentFingerprints()
	loaded = map[string]bool{}
	for _, acc := range k.Accounts.Load().Accounts {
		acc := acc
		if fp := k.Tool.Fingerprint(&acc); fp != "" && fps[fp] {
			loaded[acc.ID] = true
		}
	}
	return loaded, ok
}

// Test logs in with the account's key and remembers the username.
func (k *Keys) Test(id string, batch bool) (ssh.TestResult, error) {
	acc, err := k.Accounts.Load().Lookup(id)
	if err != nil {
		return ssh.TestResult{}, err
	}
	r := k.Tool.Test(acc, process.FindSSH(k.Accounts.Load().SSHPath), batch)
	if r.OK && r.User != acc.User {
		_ = k.Accounts.SetUser(acc.ID, r.User)
	}
	return r, nil
}

// AddToAgent loads an account's key into the OS agent / Keychain.
func (k *Keys) AddToAgent(id string) error {
	acc, err := k.Accounts.Load().Lookup(id)
	if err != nil {
		return err
	}
	return k.Tool.AddToAgent(acc)
}
