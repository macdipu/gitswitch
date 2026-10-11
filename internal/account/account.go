// Package account defines accounts, rules and settings, and stores them in
// git config so the terminal, the apps and git itself always agree.
//
// Global keys (~/.gitconfig):
//
//	gitswitch.default            account used when nothing else decides
//	gitswitch.guiPrompt          ask with a popup when git runs with no terminal
//	gitswitch.promptTimeout      seconds before a prompt falls back to the default
//	gitswitch.sshPath            optional path to the real ssh program
//	gitswitch.rule               multi-valued: owner:<prefix>=<id> | folder:<path>=<id>
//	gitswitch.scanFolder         multi-valued: folders the apps scan for repos
//	gitswitch.repo               multi-valued: repos added by hand (working folders)
//	gitswitch.hideRepo           multi-valued: scanned repos removed from the list
//	gitswitch.pin                multi-valued: pinned repos (working folders)
//	gitswitch.editor             command that opens a repo folder in an editor
//	gitswitch.renamed            multi-valued: <old id>=<new id>
//	gitswitch-account.<id>.host|name|email|key|user
//
// Per repo (.git/config):
//
//	gitswitch.account            the account saved for that repo
package account

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/macdipu/gitswitch/internal/sys"
)

// Account is one git identity: a host login, a key and a commit identity.
type Account struct {
	ID    string `json:"id"`
	Host  string `json:"host"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Key   string `json:"key"`
	User  string `json:"user"` // host username, learned by testing the login
}

// Rule picks an account automatically by repo owner or by folder.
type Rule struct {
	Kind    string `json:"kind"` // owner | folder
	Pattern string `json:"pattern"`
	Account string `json:"account"`
}

func (r Rule) String() string { return r.Kind + ":" + r.Pattern + "=" + r.Account }

// Config is everything gitswitch has stored.
type Config struct {
	Accounts      []Account         `json:"accounts"`
	Default       string            `json:"default"`
	GUIPrompt     bool              `json:"guiPrompt"`
	PromptTimeout int               `json:"promptTimeout"`
	SSHPath       string            `json:"sshPath"`
	Rules         []Rule            `json:"rules"`
	ScanFolders   []string          `json:"scanFolders"`
	Repos         []string          `json:"repos"`       // added by hand
	HiddenRepos   []string          `json:"hiddenRepos"` // found by a scan, then removed
	Pinned        []string          `json:"pinned"`
	Editor        string            `json:"editor"`
	Renamed       map[string]string `json:"-"` // old id (lower case) -> new id
}

// Account finds an account by id, following renames. nil if unknown.
func (c *Config) Account(id string) *Account {
	for hop := 0; hop < 5 && id != ""; hop++ {
		for i := range c.Accounts {
			if strings.EqualFold(c.Accounts[i].ID, id) {
				return &c.Accounts[i]
			}
		}
		id = c.Renamed[strings.ToLower(id)]
	}
	return nil
}

// AccountByEmail finds the account that commits with this email.
func (c *Config) AccountByEmail(email string) *Account {
	for i := range c.Accounts {
		if c.Accounts[i].Email != "" && strings.EqualFold(c.Accounts[i].Email, email) {
			return &c.Accounts[i]
		}
	}
	return nil
}

// SortedIDs lists account ids alphabetically.
func (c *Config) SortedIDs() []string {
	ids := make([]string, 0, len(c.Accounts))
	for _, a := range c.Accounts {
		ids = append(ids, a.ID)
	}
	sort.Strings(ids)
	return ids
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// ValidID reports whether an account id is allowed.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ParseRule reads "owner:acme=work" or "folder:/x=me".
func ParseRule(v string) (Rule, bool) {
	kind, rest, ok := strings.Cut(v, ":")
	if !ok || (kind != "owner" && kind != "folder") {
		return Rule{}, false
	}
	i := strings.LastIndex(rest, "=")
	if i <= 0 {
		return Rule{}, false
	}
	return Rule{Kind: kind, Pattern: rest[:i], Account: rest[i+1:]}, true
}

// ParseBool reads git-style booleans.
func ParseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return def
}

// DefaultKeyPath is where gitswitch creates a new key for an account.
func DefaultKeyPath(id string) string {
	return filepath.Join(sys.HomeDir(), ".ssh", "id_ed25519_gitswitch_"+id)
}

// IdentityFile is the git include file holding an account's name and email.
func IdentityFile(id string) string {
	return filepath.Join(sys.ConfigDir(), "accounts", id+".gitconfig")
}

// HookValue is the core.sshCommand value that routes git through gitswitch.
func HookValue(exePath string) string {
	return `"` + filepath.ToSlash(exePath) + `" ssh`
}

// IsHook reports whether a core.sshCommand value is gitswitch's hook.
func IsHook(v string) bool {
	return strings.Contains(strings.ToLower(v), "gitswitch") && strings.HasSuffix(strings.TrimSpace(v), " ssh")
}
