package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
)

// Ways to group the repo list (gitswitch.repoGroups).
const (
	ByNone    = "none"
	ByAccount = "account" // the account the repo uses
	ByOwner   = "owner"   // the user or organization in the origin URL
	ByFolder  = "folder"  // the folder the repo is in
	ByGroup   = "group"   // a group you name (gitswitch repo group)
)

// GroupBys lists the ways to group, in the order the apps offer them.
var GroupBys = []string{ByNone, ByAccount, ByOwner, ByFolder, ByGroup}

// CheckGroupBy accepts one of GroupBys ("" is ByNone).
func CheckGroupBy(by string) (string, error) {
	by = strings.ToLower(strings.TrimSpace(by))
	if by == "" {
		return ByNone, nil
	}
	for _, b := range GroupBys {
		if by == b {
			return by, nil
		}
	}
	return "", fmt.Errorf("group by %s, %s, %s, %s or %s, not %q", ByNone, ByAccount, ByOwner, ByFolder, ByGroup, by)
}

// CheckGroupName cleans a group name. It is stored as "<name>=<path>", so
// it may not hold "=" or a line break.
func CheckGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("give the group a name")
	case strings.ContainsAny(name, "=\r\n"):
		return "", errors.New("a group name cannot hold = or a line break")
	case len([]rune(name)) > 40:
		return "", errors.New("a group name can be at most 40 characters")
	}
	return name, nil
}

// Group is a named part of the repo list.
type Group struct {
	Name  string `json:"name"`  // "" when the list is not grouped
	Other bool   `json:"other"` // the repos that fit no group, shown last
	Repos []Info `json:"repos"`
}

// GroupRepos splits list into groups, sorted by name (case-insensitive),
// with the repos that fit none last. The repos keep their order (pinned
// first) inside each group. ByNone gives one group with every repo.
func GroupRepos(list []Info, by string) []Group {
	if by == ByNone || by == "" {
		return []Group{{Repos: append([]Info{}, list...)}}
	}
	var groups []Group
	at := map[string]int{}
	for _, r := range list {
		name, other := groupOf(r, by)
		key := strings.ToLower(name)
		i, ok := at[key]
		if !ok {
			i = len(groups)
			at[key] = i
			groups = append(groups, Group{Name: name, Other: other})
		}
		groups[i].Repos = append(groups[i].Repos, r)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Other != groups[j].Other {
			return groups[j].Other
		}
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups
}

// groupOf names a repo's group, and reports when it fits none.
func groupOf(r Info, by string) (string, bool) {
	switch by {
	case ByAccount:
		if r.Account != "" {
			return r.Account, false
		}
		return "No account", true
	case ByOwner:
		if o := Owner(r.Remote); o != "" {
			return o, false
		}
		return "No remote", true
	case ByFolder:
		return filepath.Dir(r.Path), false
	case ByGroup:
		if r.Group != "" {
			return r.Group, false
		}
		return "Not in a group", true
	}
	return "", false
}

// Owner is the host and user or organization of a remote URL, such as
// "github.com/acme" (GitLab subgroups included: "gitlab.com/acme/web").
// It is "" for a URL with no owner, such as a local folder.
func Owner(url string) string {
	host, path := remote.Parse(url)
	i := strings.LastIndex(path, "/")
	if host == "" || i <= 0 {
		return ""
	}
	return remote.CanonicalHost(host) + "/" + path[:i]
}

// SameRemote is a key that is equal for two URLs of the same remote repo,
// whichever way they are written: ssh or https, with or without .git, in
// any case. It is "" for a URL it cannot read.
func SameRemote(url string) string {
	host, path := remote.Parse(url)
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(remote.CanonicalHost(host) + "/" + strings.TrimSuffix(path, ".git"))
}

// MarkClones fills each repo's Clones with the other repos in list that
// are clones of the same origin.
func MarkClones(list []Info) {
	by := map[string][]int{}
	for i, r := range list {
		if k := SameRemote(r.Remote); k != "" {
			by[k] = append(by[k], i)
		}
	}
	for _, idx := range by {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			list[i].Clones = []string{}
			for _, j := range idx {
				if j != i {
					list[i].Clones = append(list[i].Clones, list[j].Path)
				}
			}
		}
	}
}
