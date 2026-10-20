// Package domain describes signing an account in to its host's API with a
// token, and declares what the hosting use cases need from the outside
// world (ports). GitHub (github.com and GitHub Enterprise) and GitLab
// (gitlab.com) for now.
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

// Kind is the kind of git host a host name belongs to. It is guessed from
// the name, never configured: github.com is GitHub, gitlab.com (or a host
// that starts with "gitlab.") is GitLab, and any other host is treated as
// a GitHub Enterprise server, as before.
type Kind string

const (
	KindGitHub Kind = "github"
	KindGitLab Kind = "gitlab"
)

// KindOf guesses the kind of host from its name.
func KindOf(host string) Kind {
	h := remote.CanonicalHost(host)
	if h == "gitlab.com" || strings.HasPrefix(h, "gitlab.") {
		return KindGitLab
	}
	return KindGitHub
}

// Name is how the host is called in messages.
func (k Kind) Name() string {
	if k == KindGitLab {
		return "GitLab"
	}
	return "GitHub"
}

// RequestName is what the host calls a change under review.
func (k Kind) RequestName() string {
	if k == KindGitLab {
		return "merge request"
	}
	return "pull request"
}

// Short is the short form that goes before a number: PR #42, MR !7.
func (k Kind) Short() string {
	if k == KindGitLab {
		return "MR"
	}
	return "PR"
}

// Mark goes before a number: #42 on GitHub, !7 on GitLab.
func (k Kind) Mark() string {
	if k == KindGitLab {
		return "!"
	}
	return "#"
}

// APIBase is the REST API address for a host: api.github.com for
// github.com, <host>/api/v3 for a GitHub Enterprise server, and
// <host>/api/v4 for GitLab. Bitbucket is not supported yet.
func APIBase(host string) (string, error) {
	h := remote.CanonicalHost(host)
	switch {
	case h == "":
		return "", errors.New("the account has no host")
	case h == "github.com":
		return "https://api.github.com", nil
	case h == "bitbucket.org":
		return "", fmt.Errorf("GitSwitch can sign in to GitHub and GitLab only for now, not %s", h)
	case KindOf(h) == KindGitLab:
		return "https://" + h + "/api/v4", nil
	}
	return "https://" + h + "/api/v3", nil
}

// TokenHelp says where to create a token for a host and what it needs.
func TokenHelp(host string) (page, needs string) {
	h := remote.CanonicalHost(host)
	if h == "" {
		h = "github.com"
	}
	if KindOf(h) == KindGitLab {
		return "https://" + h + "/-/user_settings/personal_access_tokens",
			`A personal access token with the scopes read_api, read_repository and write_repository is enough.`
	}
	return "https://" + h + "/settings/personal-access-tokens/new",
		`A fine-grained token with "All repositories" and read access to Metadata, Contents and Pull requests is enough.`
}

// TokenPage is where to create a token for a host.
func TokenPage(host string) string {
	page, _ := TokenHelp(host)
	return page
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
	// Provider is what the host is called (GitHub, GitLab); TokenPage and
	// TokenNeeds say where to create a token and what it needs.
	Provider   string `json:"provider"`
	TokenPage  string `json:"tokenPage"`
	TokenNeeds string `json:"tokenNeeds"`
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
	// Whoami returns who the token signs in as. Every call says which
	// kind of host it is for; base is its API address (see APIBase).
	Whoami(kind Kind, base, token string) (User, Grant, error)
	// Repos lists every repo the token's user can see: their own, ones
	// they collaborate on and their organizations', private ones included.
	Repos(kind Kind, base, token string) ([]RemoteRepo, error)
	// Inbox reads the open pull requests (merge requests) of login's
	// inbox: waiting for its review, its own, and ones it reviewed.
	Inbox(kind Kind, base, token, login string) (toReview, mine, reviewed []PullRequest, err error)
	// BranchPR reads the open pull request headed by branch of repo
	// (owner/name, or group/subgroup/name), with its approvals; nil when
	// there is none.
	BranchPR(kind Kind, base, token, repo, branch string) (*PullRequest, error)
}

// InboxCache keeps each account's last inbox on disk.
type InboxCache interface {
	LoadInbox(accountID string) (Inbox, bool)
	SaveInbox(accountID string, in Inbox) error
	DeleteInbox(accountID string) error
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
	// Info reads one repo, listed or not.
	Info(c *account.Config, gitDir string) repo.Info
}

// Accounts reads the accounts and records a learned username.
type Accounts interface {
	Load() *account.Config
	SetUser(id, user string) error
}
