package infrastructure

import (
	"sort"
	"strconv"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// Git also implements refs.RemoteGit.
var _ refs.RemoteGit = (*Git)(nil)

func (g *Git) Remotes(gitDir string) ([]refs.RemoteConf, error) {
	out, err := g.Git.Run("--git-dir="+gitDir, "remote")
	if err != nil {
		return nil, err
	}
	var all []refs.RemoteConf
	for _, name := range strings.Fields(out) {
		c := refs.RemoteConf{Name: name}
		c.FetchURL, _ = g.Git.Run("--git-dir="+gitDir, "remote", "get-url", name)
		c.PushURL, _ = g.Git.Run("--git-dir="+gitDir, "remote", "get-url", "--push", name)
		if c.PushURL == "" {
			c.PushURL = c.FetchURL
		}
		all = append(all, c)
	}
	return all, nil
}

func (g *Git) TrackingBranches(gitDir, remote string) []string {
	out, _ := g.Git.Run("--git-dir="+gitDir, "config", "--local", "--get-regexp", `^branch\..*\.remote$`)
	var names []string
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, " ")
		if !ok || val != remote {
			continue
		}
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(key, "branch."), ".remote"))
	}
	sort.Strings(names)
	return names
}

func (g *Git) AddRemote(gitDir, name, url string) error {
	// "--" keeps a URL from being read as an option.
	_, err := g.Git.Run("--git-dir="+gitDir, "remote", "add", "--", name, url)
	return err
}

func (g *Git) RemoveRemote(gitDir, name string) error {
	_, err := g.Git.Run("--git-dir="+gitDir, "remote", "remove", name)
	return err
}

func (g *Git) RenameRemote(gitDir, from, to string) error {
	_, err := g.Git.Run("--git-dir="+gitDir, "remote", "rename", from, to)
	return err
}

func (g *Git) SetRemoteURL(gitDir, name, url string) error {
	_, err := g.Git.Run("--git-dir="+gitDir, "remote", "set-url", "--", name, url)
	return err
}

func (g *Git) DeleteRemoteBranch(wt, remote, branch string, progress func(string)) error {
	// The full ref: a tag of the same name is never meant.
	return g.Git.Stream(wt, progress, "push", "--progress", remote, "--delete", "refs/heads/"+branch)
}

func (g *Git) FetchPrune(wt string, progress func(string)) error {
	return g.Git.Stream(wt, progress, "fetch", "--prune", "--progress")
}

func (g *Git) ForcePush(wt, remote, src, dst, expect string, progress func(string)) error {
	// Never --force: the push goes only if the remote branch is still at
	// expect, the commit the person last saw.
	return g.Git.Stream(wt, progress, "push", "--progress", "--force-with-lease="+dst+":"+expect, remote, src+":"+dst)
}

func (g *Git) Detach(wt, commit string) error {
	return g.Git.Change(wt, "switch", "--detach", commit)
}

// detachedKey holds the branch a detached HEAD was left from.
const detachedKey = "gitswitch.detachedFrom"

func (g *Git) DetachedFrom(gitDir string) string { return g.Git.RepoGet(gitDir, detachedKey) }

func (g *Git) SaveDetachedFrom(gitDir, branch string) error {
	return g.save(detachedKey, gitDir, branch)
}

func (g *Git) Orphans(gitDir string) int {
	out, err := g.Git.Run("--git-dir="+gitDir, "rev-list", "--count", "HEAD", "--not", "--branches", "--remotes", "--tags", "--")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// pruneKey holds "true" or "false": whether a fetch also prunes.
const pruneKey = "gitswitch.fetchPrune"

func (g *Git) PruneModes(gitDir string) (global, repo string) {
	global = g.Git.GlobalGet(pruneKey)
	if gitDir != "" {
		repo = g.Git.RepoGet(gitDir, pruneKey)
	}
	return global, repo
}

func (g *Git) SavePruneMode(gitDir, value string) error { return g.save(pruneKey, gitDir, value) }
