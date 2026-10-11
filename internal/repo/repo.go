// Package repo finds repos on disk, keeps the list of repos added by hand,
// and reports which account each uses. It only reads repos: adding or
// removing one changes gitswitch's own list, never the repo.
package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/gitcli"
	"github.com/macdipu/gitswitch/internal/resolve"
	"github.com/macdipu/gitswitch/internal/sys"
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
	Missing     bool     `json:"missing"` // added by hand, but the folder is gone or no longer a repo
}

// Remote is one configured remote.
type Remote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// AccountFor resolves a repo's account without prompting. The reason is
// one of the resolve constants, or "" when no account matches the host.
func AccountFor(git *gitcli.Runner, c *account.Config, gitDir string) (*account.Account, string) {
	host, path := gitcli.ParseRemote(git.RepoGet(gitDir, "remote.origin.url"))
	cands := resolve.Candidates(c, host)
	if len(cands) == 0 {
		return nil, ""
	}
	d := resolve.Decide(c, cands, resolve.Input{
		SavedID:  git.RepoGet(gitDir, "gitswitch.account"),
		RepoPath: path,
		Worktree: func() string { return git.WorktreeOf(gitDir) },
	})
	if d == nil {
		return nil, resolve.Asks
	}
	return d.Account, d.Reason
}

// InfoFor reads one repo.
func InfoFor(git *gitcli.Runner, c *account.Config, gitDir string) Info {
	ri := Info{Path: git.WorktreeOf(gitDir), GitDir: gitDir}
	ri.Remote = git.RepoGet(gitDir, "remote.origin.url")
	ri.Saved = git.RepoGet(gitDir, "gitswitch.account")
	ri.Email = git.RepoGet(gitDir, "user.email")
	if v := git.RepoGet(gitDir, "core.sshCommand"); v != "" && !account.IsHook(v) {
		ri.SSHOverride = v
	}
	ri.Branch, _ = git.Run("--git-dir="+gitDir, "symbolic-ref", "--short", "-q", "HEAD")
	ri.Remotes = remotes(git, gitDir)
	ri.Added, ri.Pinned = account.HasPath(c.Repos, ri.Path), account.HasPath(c.Pinned, ri.Path)
	if a, why := AccountFor(git, c, gitDir); a != nil {
		ri.Account, ri.Reason = a.ID, why
		ri.EmailBad = ri.Email != "" && !strings.EqualFold(ri.Email, a.Email)
	} else {
		ri.Reason = why
	}
	return ri
}

var skipDirs = map[string]bool{"node_modules": true, ".gradle": true, "build": true, "Pods": true,
	".dart_tool": true, "vendor": true, "target": true, ".venv": true, "venv": true, ".idea": true}

// remotes lists a repo's remotes in config order.
func remotes(git *gitcli.Runner, gitDir string) []Remote {
	out, _ := git.Run("--git-dir="+gitDir, "config", "--local", "--get-regexp", `^remote\..*\.url$`)
	rs := []Remote{}
	for _, l := range strings.Split(out, "\n") {
		k, url, ok := strings.Cut(l, " ")
		if !ok {
			continue
		}
		rs = append(rs, Remote{Name: strings.TrimSuffix(strings.TrimPrefix(k, "remote."), ".url"), URL: url})
	}
	return rs
}

// List is the repo list every app shows: repos found in the project folders
// plus repos added by hand, minus the ones removed from the list. Pinned
// repos come first. Added repos whose folder is gone stay, marked Missing,
// so they can be removed.
func List(git *gitcli.Runner, c *account.Config) []Info {
	var dirs []string
	var missing []Info
	for _, p := range c.Repos {
		if gd := git.GitDirFor(p); gd != "" {
			dirs = append(dirs, gd)
		} else {
			missing = append(missing, Info{Path: p, Added: true, Missing: true, Pinned: account.HasPath(c.Pinned, p), Remotes: []Remote{}})
		}
	}
	var out []Info
	for _, r := range read(git, c, find(git, c.ScanFolders, dirs)) {
		if !account.HasPath(c.HiddenRepos, r.Path) || r.Added {
			out = append(out, r)
		}
	}
	out = append(out, missing...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})
	return out
}

// Scan finds repos in folders (up to 5 levels deep) and reads them in parallel.
func Scan(git *gitcli.Runner, c *account.Config, folders []string) []Info {
	out := read(git, c, find(git, folders, nil))
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

// find returns the git dirs of repos in folders, after the given ones,
// without duplicates.
func find(git *gitcli.Runner, folders, dirs []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, gd := range dirs {
		if !seen[gd] {
			seen[gd] = true
			unique = append(unique, gd)
		}
	}
	dirs = unique
	for _, root := range folders {
		root = sys.ExpandHome(root)
		base := strings.Count(filepath.ToSlash(root), "/")
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.Count(filepath.ToSlash(p), "/")-base > 5 || (skipDirs[d.Name()] && p != root) {
				return filepath.SkipDir
			}
			if fi, err := os.Stat(filepath.Join(p, ".git")); err == nil {
				gd := filepath.Join(p, ".git")
				if !fi.IsDir() { // worktree or submodule: .git is a file
					gd = git.GitDirFor(p)
				}
				if gd != "" && !seen[gd] {
					seen[gd] = true
					dirs = append(dirs, gd)
				}
				return filepath.SkipDir
			}
			return nil
		})
	}
	return dirs
}

// read reads repos in parallel: each one starts a few short git processes.
func read(git *gitcli.Runner, c *account.Config, dirs []string) []Info {
	out := make([]Info, len(dirs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, gd := range dirs {
		wg.Add(1)
		go func(i int, gd string) {
			defer wg.Done()
			sem <- struct{}{}
			out[i] = InfoFor(git, c, gd)
			<-sem
		}(i, gd)
	}
	wg.Wait()
	return out
}
