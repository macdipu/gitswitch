package domain

import (
	"fmt"
	"strings"
)

// RemoteConf is a remote as git has it configured.
type RemoteConf struct {
	Name     string
	FetchURL string
	PushURL  string // the push URL; the fetch URL when none is set apart
}

// Remote is a remote as the apps show it.
type Remote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchURL"`
	PushURL  string `json:"pushURL"`
	Host     string `json:"host"`
	// Account is the gitswitch account fetching from and pushing to this
	// URL would use; "" when none is set up for its host (git asks).
	Account string `json:"account"`
	// Upstream: the current branch pulls from and pushes to this remote.
	Upstream bool `json:"upstream"`
	// Tracking lists the local branches whose upstream is on this remote.
	Tracking []string `json:"tracking"`
}

// RemoteChanged is the result of adding, removing, renaming or re-pointing
// a remote.
type RemoteChanged struct {
	Name    string `json:"name"`
	NewName string `json:"newName"` // rename: the new name
	URL     string `json:"url"`     // add, set-url: the URL now
	Was     string `json:"was"`     // remove, set-url: the URL before
	Host    string `json:"host"`
	Account string `json:"account"` // the account the URL would use
	// Branches are the local branches that tracked the remote: with
	// remove they lost their upstream, with rename they keep tracking.
	Branches []string `json:"branches"`
	// Note says in words what is worth knowing, e.g. that the URL's owner
	// maps to another account than the one saved for the repo.
	Note string `json:"note"`
}

// RemoteBranchDeleted is the result of deleting a branch on a remote.
type RemoteBranchDeleted struct {
	Remote  string `json:"remote"`
	Branch  string `json:"branch"`  // without the remote, e.g. feature/x
	Hash    string `json:"hash"`    // where it was; push it again to restore it
	Account string `json:"account"` // who deleted it; "" when git asked
	// Tracking lists the local branches that pulled from it: they now
	// point at a remote branch that is gone.
	Tracking []string `json:"tracking"`
	Restore  string   `json:"restore"` // the command that puts it back
}

// PushPlan is what a force push with lease would do, read from the last
// fetch: the commits on the remote branch that would be overwritten, and
// the ones that would go up.
type PushPlan struct {
	Branch      string `json:"branch"`
	Remote      string `json:"remote"`
	Upstream    string `json:"upstream"`
	Expect      string `json:"expect"` // the remote branch's commit as last fetched: the lease
	ToOverwrite []Line `json:"toOverwrite"`
	ToPush      []Line `json:"toPush"`
	// MoreOverwrite and MorePush count the commits left out of long lists.
	MoreOverwrite int `json:"moreOverwrite"`
	MorePush      int `json:"morePush"`
	// Account is the gitswitch account that would push.
	Account  string `json:"account"`
	CanForce bool   `json:"canForce"`
	Why      string `json:"why"` // when CanForce is false: the reason, in words
}

// Detached is the result of checking out a commit: HEAD on no branch.
type Detached struct {
	Rev     string `json:"rev"`   // as typed
	Hash    string `json:"hash"`  // the commit now checked out
	Short   string `json:"short"` // its short hash
	From    string `json:"from"`  // the branch left; "" when HEAD was detached already
	Carried bool   `json:"carried"`
	Parked  bool   `json:"parked"`
	// Back is the branch "back" returns to; "" when none is known.
	Back string `json:"back"`
	// Words explains in plain words what detached means.
	Words string `json:"words"`
}

// DetachedWords is what a detached HEAD is, for anyone who sees one.
func DetachedWords(short, back string) string {
	w := fmt.Sprintf("You are looking at commit %s, not on a branch. Commits made here belong to no branch and are easy to lose: to keep work, start a branch here (New branch here).", short)
	if back != "" {
		w += " To leave, go back to " + back + "."
	}
	return w
}

// DetachedState says whether HEAD is detached and where it came from.
type DetachedState struct {
	Detached bool   `json:"detached"`
	Hash     string `json:"hash"`
	Short    string `json:"short"`
	Back     string `json:"back"`    // the branch you came from, when it still exists; else ""
	Orphans  int    `json:"orphans"` // commits made here that no branch, tag or remote branch has
	Words    string `json:"words"`
}

// PruneSetting says whether a fetch also forgets the remote-tracking
// branches the server no longer has: the repo's own choice, else the one
// for every repo, else off.
type PruneSetting struct {
	On     bool   `json:"on"`     // in effect
	Repo   string `json:"repo"`   // "on" or "off" saved for this repo; "" follows Global
	Global string `json:"global"` // "on" or "off" for every repo; "" is off
}

// NewPruneSetting reads the saved values (git's true/false); anything else
// counts as unset.
func NewPruneSetting(global, repo string) PruneSetting {
	word := func(v string) string {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "yes", "1":
			return "on"
		case "false", "off", "no", "0":
			return "off"
		}
		return ""
	}
	s := PruneSetting{Global: word(global), Repo: word(repo)}
	s.On = s.Global == "on"
	if s.Repo != "" {
		s.On = s.Repo == "on"
	}
	return s
}

// CheckRemoteName refuses a remote name git or the apps would trip over.
func CheckRemoteName(name string) error {
	bad := fmt.Errorf("%q can't be a remote name: use letters, digits, - _ and . (for example origin or upstream)", name)
	if name == "" || len(name) > 100 {
		return bad
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || (i > 0 && (r == '-' || r == '_' || r == '.'))
		if !ok {
			return bad
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") || strings.Contains(name, "..") {
		return bad
	}
	return nil
}

// SplitRemoteBranch splits origin/feature/x into the remote and the branch,
// using the longest of the remote names that fits.
func SplitRemoteBranch(ref string, remotes []string) (remote, branch string, ok bool) {
	for _, r := range remotes {
		if rest, found := strings.CutPrefix(ref, r+"/"); found && rest != "" && len(r) > len(remote) {
			remote, branch, ok = r, rest, true
		}
	}
	return
}

// RemoteGit is what the remotes manager, remote branch deletion, force push
// with lease and detached checkouts need from git. The use cases look for it
// on the Refs they are given (like StashGit), so a Refs without it simply
// has no remote features.
type RemoteGit interface {
	Remotes(gitDir string) ([]RemoteConf, error)
	// TrackingBranches lists the local branches whose upstream is on remote.
	TrackingBranches(gitDir, remote string) []string
	AddRemote(gitDir, name, url string) error
	// RemoveRemote deletes the remote and its remote-tracking branches
	// here, and the upstream of the branches that tracked it.
	RemoveRemote(gitDir, name string) error
	// RenameRemote renames the remote; branches keep tracking it.
	RenameRemote(gitDir, from, to string) error
	SetRemoteURL(gitDir, name, url string) error

	// DeleteRemoteBranch runs git push <remote> --delete <branch>.
	DeleteRemoteBranch(worktree, remote, branch string, progress func(string)) error
	// FetchPrune is fetch that also removes the remote-tracking branches
	// the server no longer has.
	FetchPrune(worktree string, progress func(string)) error
	// ForcePush pushes src to dst with --force-with-lease=dst:expect.
	ForcePush(worktree, remote, src, dst, expect string, progress func(string)) error

	// Detach checks out a commit on no branch.
	Detach(worktree, commit string) error
	// DetachedFrom is the branch left for a detached HEAD; "" when none.
	DetachedFrom(gitDir string) string
	SaveDetachedFrom(gitDir, branch string) error // "" forgets it
	// Orphans counts the commits only HEAD has: on no branch, tag or
	// remote branch.
	Orphans(gitDir string) int

	PruneModes(gitDir string) (global, repo string)
	SavePruneMode(gitDir, value string) error // "true", "false"; "" removes it
}
