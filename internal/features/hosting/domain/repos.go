package domain

import (
	"sort"
	"strings"
	"time"

	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

// RemoteRepo is a repo on the host, as one account sees it.
type RemoteRepo struct {
	Account       string   `json:"account"`  // the signed-in account that sees it
	FullName      string   `json:"fullName"` // owner/name
	Owner         string   `json:"owner"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Private       bool     `json:"private"`
	Fork          bool     `json:"fork"`
	Archived      bool     `json:"archived"`
	Language      string   `json:"language"`
	DefaultBranch string   `json:"defaultBranch"`
	SSHURL        string   `json:"sshUrl"`
	HTTPSURL      string   `json:"httpsUrl"`
	WebURL        string   `json:"webUrl"`
	PushedAt      string   `json:"pushedAt"`
	Cloned        []string `json:"cloned"` // local folders of this repo in the repo list
}

// RepoList is what an account saw, and when.
type RepoList struct {
	Fetched time.Time    `json:"fetched"`
	Repos   []RemoteRepo `json:"repos"`
}

// Stale is how long a fetched list is shown before it is read again.
const Stale = time.Hour

// AccountRepos is one signed-in account's part of the browser.
type AccountRepos struct {
	Account string    `json:"account"`
	Host    string    `json:"host"`
	Fetched time.Time `json:"fetched"` // zero: never read
	Stale   bool      `json:"stale"`
	Error   string    `json:"error"`
	Count   int       `json:"count"`
}

// Browse is the remote repos of every signed-in account.
type Browse struct {
	Accounts []AccountRepos `json:"accounts"`
	Repos    []RemoteRepo   `json:"repos"`
}

// MarkCloned fills each repo's Cloned with the listed local repos whose
// origin is that repo, however its URL is written.
func MarkCloned(remote []RemoteRepo, local []repo.Info) {
	at := map[string][]string{}
	for _, l := range local {
		if k := repo.SameRemote(l.Remote); k != "" && !l.Missing {
			at[k] = append(at[k], l.Path)
		}
	}
	for i := range remote {
		remote[i].Cloned = []string{}
		for _, u := range []string{remote[i].SSHURL, remote[i].HTTPSURL} {
			if ps := at[repo.SameRemote(u)]; len(ps) > 0 {
				remote[i].Cloned = ps
				break
			}
		}
	}
}

// Merge joins the accounts' lists. A repo two accounts can see is listed
// once, for the first account (accounts come in config order).
func Merge(lists []RepoList) []RemoteRepo {
	seen := map[string]bool{}
	out := []RemoteRepo{}
	for _, l := range lists {
		for _, r := range l.Repos {
			k := strings.ToLower(r.FullName)
			if !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].FullName) < strings.ToLower(out[j].FullName) })
	return out
}

// Filter narrows the browser. Empty fields match everything.
type Filter struct {
	Text       string `json:"text"`       // in the name or description, every word
	Account    string `json:"account"`    // the account that sees it
	Owner      string `json:"owner"`      // user or organization
	Visibility string `json:"visibility"` // public | private
	Language   string `json:"language"`
	Cloned     string `json:"cloned"`   // yes | no
	Archived   bool   `json:"archived"` // also show archived repos
}

// Match reports whether a repo passes the filter.
func (f Filter) Match(r RemoteRepo) bool {
	switch {
	case r.Archived && !f.Archived,
		f.Account != "" && !strings.EqualFold(f.Account, r.Account),
		f.Owner != "" && !strings.EqualFold(f.Owner, r.Owner),
		f.Visibility == "private" && !r.Private,
		f.Visibility == "public" && r.Private,
		f.Language != "" && !strings.EqualFold(f.Language, r.Language),
		f.Cloned == "yes" && len(r.Cloned) == 0,
		f.Cloned == "no" && len(r.Cloned) > 0:
		return false
	}
	hay := strings.ToLower(r.FullName + " " + r.Description)
	for _, w := range strings.Fields(strings.ToLower(f.Text)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Apply returns the repos that pass the filter.
func (f Filter) Apply(rs []RemoteRepo) []RemoteRepo {
	out := []RemoteRepo{}
	for _, r := range rs {
		if f.Match(r) {
			out = append(out, r)
		}
	}
	return out
}

// Facets lists the owners and languages in rs, sorted, for filter menus.
func Facets(rs []RemoteRepo) (owners, languages []string) {
	o, l := map[string]bool{}, map[string]bool{}
	owners, languages = []string{}, []string{}
	for _, r := range rs {
		if r.Owner != "" && !o[strings.ToLower(r.Owner)] {
			o[strings.ToLower(r.Owner)] = true
			owners = append(owners, r.Owner)
		}
		if r.Language != "" && !l[strings.ToLower(r.Language)] {
			l[strings.ToLower(r.Language)] = true
			languages = append(languages, r.Language)
		}
	}
	less := func(s []string) func(i, j int) bool {
		return func(i, j int) bool { return strings.ToLower(s[i]) < strings.ToLower(s[j]) }
	}
	sort.Slice(owners, less(owners))
	sort.Slice(languages, less(languages))
	return owners, languages
}

// Find picks a repo by owner/name (or a clone URL) from rs, preferring
// the given account when two see it.
func Find(rs []RemoteRepo, name, accountID string) (RemoteRepo, bool) {
	key := strings.ToLower(strings.TrimSuffix(strings.Trim(name, "/"), ".git"))
	if k := repo.SameRemote(name); k != "" {
		if _, path, ok := strings.Cut(k, "/"); ok {
			key = path
		}
	}
	var found *RemoteRepo
	for i := range rs {
		if strings.ToLower(rs[i].FullName) != key {
			continue
		}
		if found == nil || strings.EqualFold(rs[i].Account, accountID) {
			found = &rs[i]
		}
	}
	if found == nil {
		return RemoteRepo{}, false
	}
	return *found, true
}
