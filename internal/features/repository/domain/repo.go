// Package domain describes repos as the apps show them, and declares what
// the repository use cases need from the outside world (ports). Import it
// as repo.
package domain

import (
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

// Info describes one repo for the apps.
type Info struct {
	Path     string `json:"path"`
	GitDir   string `json:"gitDir"`
	Remote   string `json:"remote"`
	Saved    string `json:"saved"`
	Account  string `json:"account"` // effective account
	Reason   string `json:"reason"`
	Email    string `json:"email"`
	EmailBad bool   `json:"emailBad"`
	// SSHOverride is a repo-local core.sshCommand that bypasses gitswitch.
	SSHOverride string   `json:"sshOverride"`
	Branch      string   `json:"branch"` // "" when HEAD is detached
	Remotes     []Remote `json:"remotes"`
	Added       bool     `json:"added"` // added by hand (gitswitch repo add)
	Pinned      bool     `json:"pinned"`
	Recent      int      `json:"recent"`  // 1 for the repo opened last, 2 for the one before; 0 when not recent
	Missing     bool     `json:"missing"` // added by hand, but the folder is gone or no longer a repo
}

// Remote is one configured remote.
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Names of the repo lists kept in gitswitch's settings.
const (
	ListAdded  = "repo"     // added by hand
	ListHidden = "hideRepo" // found by a scan, then removed from the list
	ListPinned = "pin"      // pinned to the top
	ListRecent = "recent"   // opened in gitswitch, newest last
)

// RecentMax is how many recently opened repos gitswitch remembers.
const RecentMax = 10

// Reader finds and reads repos on disk.
type Reader interface {
	// AccountFor resolves a repo's account without prompting. The reason
	// is one of the resolve constants, or "" when no account matches.
	AccountFor(c *account.Config, gitDir string) (*account.Account, string)
	InfoFor(c *account.Config, gitDir string) Info
	// List is the repo list: repos found in the project folders plus repos
	// added by hand, minus removed ones, pinned first.
	List(c *account.Config) []Info
	// Scan finds repos in folders (up to 5 levels deep).
	Scan(c *account.Config, folders []string) []Info
}

// Locator finds the repo a folder belongs to.
type Locator interface {
	// GitDirFor returns the absolute git dir for a path, or "".
	GitDirFor(path string) string
	// WorktreeOf returns the working folder for a git dir.
	WorktreeOf(gitDir string) string
}

// Cloner clones repos.
type Cloner interface {
	// Clone clones url into parent/folder, passing each progress line to
	// progress. With accountID the new repo has that account saved before
	// git downloads anything.
	Clone(parent, url, folder, accountID string, progress func(string)) error
}

// ListStore keeps the repo lists (see the List constants) in gitswitch's
// settings. Values are absolute working folders.
type ListStore interface {
	Load() *account.Config
	AddRepoPath(list, path string) error
	RemoveRepoPath(list, path string) error
}
