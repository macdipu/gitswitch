// Package domain defines accounts, rules and settings: what they are and
// which values are valid. It is pure: no git, no files. Import it as
// account. The git-config store that keeps them is in the infrastructure
// package, so the terminal, the apps and git itself always agree.
//
// Global keys (~/.gitconfig):
//
//	gitswitch.default            optional: account used when a repo has nothing saved, no rule
//	                             matches and nobody answers. Unset: ask, and save the answer
//	gitswitch.guiPrompt          ask with a popup when git runs with no terminal
//	gitswitch.promptTimeout      seconds a prompt waits; then the default, if set, or no connection
//	gitswitch.sshPath            optional path to the real ssh program
//	gitswitch.rule               multi-valued: owner:<prefix>=<id> | folder:<path>=<id>
//	gitswitch.scanFolder         multi-valued: folders the apps scan for repos
//	gitswitch.repo               multi-valued: repos added by hand (working folders)
//	gitswitch.hideRepo           multi-valued: scanned repos removed from the list
//	gitswitch.pin                multi-valued: pinned repos (working folders)
//	gitswitch.recent             multi-valued: repos opened lately, newest last (at most 10)
//	gitswitch.group              multi-valued: <name>=<path>, a repo in a group you named
//	gitswitch.repoGroups         none | account | owner | folder | group: how the apps group the repo list
//	gitswitch.repoSort           folder | name | recent | account: how the apps sort it (pinned first)
//	gitswitch.editor             command that opens a repo folder in an editor
//	gitswitch.renamed            multi-valued: <old id>=<new id>
//	gitswitch.switchChanges      ask | smart | park: what switching branch does with
//	                             uncommitted changes (kept by the refs feature)
//	gitswitch.branchPrefix       optional: put in front of new branch names, e.g. feature/
//	                             (kept by the refs feature)
//	gitswitch-account.<id>.host|name|email|key|user
//
// Per repo (.git/config):
//
//	gitswitch.account            the account saved for that repo
//	gitswitch.switchChanges      this repo's own choice, before the global one
package domain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// RepoGroup puts a repo (its working folder) in a group you named.
type RepoGroup struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

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
	Recent        []string          `json:"recent"` // opened in gitswitch, newest first
	Groups        []RepoGroup       `json:"groups"`
	RepoGroups    string            `json:"repoGroups"` // how the apps group the repo list; "" is none
	RepoSort      string            `json:"repoSort"`   // how the apps sort the repo list; "" is folder
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

// Lookup finds an account by id, following renames, or explains that there
// is none.
func (c *Config) Lookup(id string) (*Account, error) {
	if a := c.Account(id); a != nil {
		return a, nil
	}
	return nil, fmt.Errorf("no account %q (see `gitswitch account list`)", id)
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
func ValidID(id string) bool { return idRe.MatchString(id) && !strings.EqualFold(id, AskEveryTime) }

// AskEveryTime, saved as a repo's account (gitswitch.account), makes that
// repo ask on every connection and never remember the answer. It is not a
// valid account id.
const AskEveryTime = "ask"

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

// HookValue is the core.sshCommand value that routes git through gitswitch.
func HookValue(exePath string) string {
	return `"` + filepath.ToSlash(exePath) + `" ssh`
}

// IsHook reports whether a core.sshCommand value is gitswitch's hook.
func IsHook(v string) bool {
	return strings.Contains(strings.ToLower(v), "gitswitch") && strings.HasSuffix(strings.TrimSpace(v), " ssh")
}
