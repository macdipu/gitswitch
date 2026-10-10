// Package resolve decides which account a repo uses. It is pure logic: it
// reads only what the caller passes in, so it is fast and easy to test.
package resolve

import (
	"path/filepath"
	"strings"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/gitcli"
	"github.com/macdipu/gitswitch/internal/sys"
)

// Reasons an account was chosen.
const (
	Saved       = "saved"
	OwnerRule   = "owner-rule"
	FolderRule  = "folder-rule"
	OnlyAccount = "only-account"
	Prompt      = "prompt"
	Dialog      = "dialog"
	Default     = "default"
	Asks        = "asks"
)

// Decision is the account chosen for a repo and why.
type Decision struct {
	Account *account.Account
	Reason  string
	Save    bool // remember the choice for this repo
}

// Input is what is known about the repo being used.
type Input struct {
	SavedID  string        // gitswitch.account from the repo, if any
	RepoPath string        // owner/repo.git from the remote
	Worktree func() string // the repo folder (called only when needed)
}

// Candidates returns the accounts for a host (port-443 hosts included).
func Candidates(c *account.Config, host string) []account.Account {
	var out []account.Account
	for _, a := range c.Accounts {
		if host == "" || gitcli.SameHost(a.Host, host) {
			out = append(out, a)
		}
	}
	return out
}

// Decide runs the silent steps: saved account, the most specific owner
// rule, the deepest folder rule, or the only account. nil means ask.
func Decide(c *account.Config, cands []account.Account, in Input) *Decision {
	pick := func(id string) *account.Account {
		if a := c.Account(id); a != nil { // follows renames
			id = a.ID
		}
		for i := range cands {
			if id != "" && strings.EqualFold(cands[i].ID, id) {
				return &cands[i]
			}
		}
		return nil
	}
	if a := pick(in.SavedID); a != nil {
		return &Decision{Account: a, Reason: Saved}
	}
	if in.RepoPath != "" {
		if a := best(c.Rules, "owner", pick, func(p string) bool { return MatchesOwner(in.RepoPath, p) }); a != nil {
			return &Decision{Account: a, Reason: OwnerRule}
		}
	}
	if in.Worktree != nil {
		if wt := in.Worktree(); wt != "" {
			if a := best(c.Rules, "folder", pick, func(p string) bool { return MatchesFolder(wt, p) }); a != nil {
				return &Decision{Account: a, Reason: FolderRule}
			}
		}
	}
	if len(cands) == 1 {
		return &Decision{Account: &cands[0], Reason: OnlyAccount}
	}
	return nil
}

// best returns the account of the longest matching pattern of a kind.
func best(rules []account.Rule, kind string, pick func(string) *account.Account, match func(string) bool) *account.Account {
	var found *account.Account
	bestLen := -1
	for _, r := range rules {
		if r.Kind == kind && len(r.Pattern) > bestLen && match(r.Pattern) {
			if a := pick(r.Account); a != nil {
				found, bestLen = a, len(r.Pattern)
			}
		}
	}
	return found
}

// DefaultAmong returns the default account if it is a candidate, else the first.
func DefaultAmong(c *account.Config, cands []account.Account) *account.Account {
	for i := range cands {
		if strings.EqualFold(cands[i].ID, c.Default) {
			return &cands[i]
		}
	}
	return &cands[0]
}

// MatchesOwner: "acme" matches acme/api.git and acme/sub/x, not acme-corp/api.
func MatchesOwner(repoPath, pattern string) bool {
	rp := strings.ToLower(strings.TrimSuffix(repoPath, ".git"))
	p := strings.TrimSuffix(strings.ToLower(strings.Trim(pattern, "/")), "/*")
	return rp == p || strings.HasPrefix(rp, p+"/")
}

// MatchesFolder: the worktree is the folder or inside it (symlinks resolved).
func MatchesFolder(worktree, folder string) bool {
	for _, wt := range []string{worktree, sys.RealPath(worktree)} {
		for _, fd := range []string{folder, sys.RealPath(filepath.FromSlash(folder))} {
			w := filepath.ToSlash(wt) + "/"
			f := strings.TrimSuffix(filepath.ToSlash(fd), "/") + "/"
			if sys.CaseInsensitiveFS() {
				w, f = strings.ToLower(w), strings.ToLower(f)
			}
			if strings.HasPrefix(w, f) {
				return true
			}
		}
	}
	return false
}
