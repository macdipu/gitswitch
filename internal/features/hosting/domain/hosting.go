// Package domain describes signing an account in to its host's API with a
// token, and declares what the hosting use cases need from the outside
// world (ports). Only GitHub (github.com and GitHub Enterprise) for now.
// Import it as hosting.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

// ErrNoToken means an account has no token saved: it is not signed in.
var ErrNoToken = errors.New("not signed in")

// ErrNoKeychain means this computer has no keychain GitSwitch can use. No
// account is signed in then; signing in says how to get one.
var ErrNoKeychain = errors.New("no keychain found")

// APIBase is the REST API address for a host: api.github.com for
// github.com, and <host>/api/v3 for a GitHub Enterprise server. GitLab and
// Bitbucket are not supported yet.
func APIBase(host string) (string, error) {
	h := remote.CanonicalHost(host)
	switch {
	case h == "":
		return "", errors.New("the account has no host")
	case h == "github.com":
		return "https://api.github.com", nil
	case h == "gitlab.com" || h == "bitbucket.org":
		return "", fmt.Errorf("GitSwitch can sign in to GitHub only for now, not %s", h)
	}
	return "https://" + h + "/api/v3", nil
}

// TokenPage is where to create a token for a host.
func TokenPage(host string) string {
	h := remote.CanonicalHost(host)
	if h == "" {
		h = "github.com"
	}
	return "https://" + h + "/settings/personal-access-tokens/new"
}

// CheckToken cleans a pasted token. It may not hold spaces, quotes or
// control characters, which no GitHub token has, so it is never mangled
// on its way to the keychain.
func CheckToken(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", errors.New("paste the token")
	}
	if len(t) > 255 {
		return "", errors.New("that is too long for a token; paste only the token")
	}
	for _, r := range t {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '"' || r == '\'' || r == '\\' || r > unicode.MaxASCII {
			return "", errors.New("that does not look like a token: paste only the token, without spaces or quotes")
		}
	}
	return t, nil
}

// SameUser checks that a token belongs to the account. User is the host
// username learned by testing the ssh login ("" when not tested yet).
func SameUser(a *account.Account, login string) error {
	if a.User == "" || strings.EqualFold(a.User, login) {
		return nil
	}
	return fmt.Errorf("this token belongs to %s, but %s logs in as %s; create the token while signed in to %s as %s",
		login, a.ID, a.User, remote.CanonicalHost(a.Host), a.User)
}

// User is who a token signs in as.
type User struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

// Grant is what the host says about a token.
type Grant struct {
	Scopes  []string `json:"scopes"`  // classic tokens only; fine-grained tokens have none
	Expires string   `json:"expires"` // "" when it never expires or the host does not say
}

// SignedIn is an account signed in to its host's API.
type SignedIn struct {
	Account string `json:"account"`
	Host    string `json:"host"`
	// HTTPS is whether git now gets the token for HTTPS remotes of this
	// account's repos ("" when it does, else why not).
	HTTPS string `json:"https"`
	User
	Grant
}

// Status is whether an account is signed in, read without the network.
type Status struct {
	Account   string `json:"account"`
	Host      string `json:"host"`
	Supported bool   `json:"supported"` // the host has an API GitSwitch can use
	SignedIn  bool   `json:"signedIn"`
	Error     string `json:"error"` // the keychain could not be read
}

// Secrets keeps one token per account in the OS keychain.
type Secrets interface {
	// Get returns ErrNoToken when the account has none.
	Get(accountID string) (string, error)
	Set(accountID, token string) error
	// Delete removes the token; a missing one is not an error.
	Delete(accountID string) error
}

// API talks to a host's REST API.
type API interface {
	// Whoami returns who the token signs in as.
	Whoami(base, token string) (User, Grant, error)
	// Repos lists every repo the token's user can see: their own, ones
	// they collaborate on and their organizations', private ones included.
	Repos(base, token string) ([]RemoteRepo, error)
}

// Cache keeps each account's last repo list on disk.
type Cache interface {
	Load(accountID string) (RepoList, bool)
	Save(accountID string, l RepoList) error
	Delete(accountID string) error
}

// HelperConfig keeps GitSwitch's credential helper section for each host
// in the global git config.
type HelperConfig interface {
	// Hook is git's global core.sshCommand.
	Hook() string
	// Entries lists the system and global credential helper settings.
	Entries() ([]HelperEntry, error)
	// Write replaces the host's section with these helper values.
	Write(host string, values []string) error
	// Remove deletes the host's section; a missing one is fine.
	Remove(host string) error
}

// Repos finds the repo git is working on and the account it uses.
type Repos interface {
	// Current is the git dir of the repo git runs the helper for, or "".
	Current() string
	// AccountFor resolves the repo's account without asking; nil when
	// rules and the default do not decide.
	AccountFor(c *account.Config, gitDir string) *account.Account
	// List is the local repo list, to see what is cloned already.
	List(c *account.Config) []repo.Info
}

// Accounts reads the accounts and records a learned username.
type Accounts interface {
	Load() *account.Config
	SetUser(id, user string) error
}
