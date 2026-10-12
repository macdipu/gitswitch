package domain

// Store keeps accounts, rules and settings. The infrastructure Store keeps
// them in git config.
type Store interface {
	// Load reads the current settings, always fresh.
	Load() *Config
	// LoadChecked is Load that also reports settings that cannot be read
	// (a broken git config). The ssh hook uses it so it never guesses.
	LoadChecked() (*Config, error)
	// SuggestedName is the commit name to offer for a new account.
	SuggestedName() string
	// Save creates or updates an account after validating it.
	Save(a Account) error
	// SetUser records the host username learned from a login test.
	SetUser(id, user string) error
	// Remove deletes an account, its rules and its identity file.
	Remove(id string) error
	// Rename renames an account everywhere it is stored and records the
	// rename so repos that still name the old id keep working.
	Rename(oldID, newID string) error
	// SetDefault sets the default account; "" removes it, so repos with
	// nothing saved ask (and are saved only when you tick it).
	SetDefault(id string) error
	// DeleteKeyFiles removes an account's key pair and returns the files.
	DeleteKeyFiles(a *Account) []string
	// AddRule validates and adds a rule, replacing one with the same pattern.
	AddRule(r Rule) error
	RemoveRule(r Rule) error
	SetSetting(key, value string) error
	AddScanFolder(path string) error
	RemoveScanFolder(path string) error
}

// RepoAccounts saves the account and commit identity of single repos.
type RepoAccounts interface {
	// SetRepoAccount saves the account for a repo, applies its identity and
	// removes a repo-level core.sshCommand that would bypass gitswitch. It
	// returns the removed value, if any.
	SetRepoAccount(gitDir string, a *Account) (string, error)
	// SaveRepoAccountID only changes the saved account id (after a rename).
	SaveRepoAccountID(gitDir, id string) error
	// ForgetRepo removes a repo's saved account.
	ForgetRepo(gitDir string) error
	// ApplyIdentity sets the repo's user.name and user.email to the account's.
	ApplyIdentity(gitDir string, a *Account)
}

// KeyChoice says how a new account gets its key.
type KeyChoice struct {
	New         bool   // create a new key
	Interactive bool   // let ssh-keygen ask for the passphrase on the terminal
	Passphrase  string // used when not interactive
}
