// Package infrastructure finds repos on disk and reads them with git: their
// remotes, branch and which account each uses. It only reads repos.
// Import it as repoinfra.
package infrastructure

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// AccountFor resolves a repo's account without prompting. The reason is
// one of the resolve constants, or "" when no account matches the host.
func (r *Reader) AccountFor(c *account.Config, gitDir string) (*account.Account, string) {
	host, path := remote.Parse(r.Git.RepoGet(gitDir, "remote.origin.url"))
	cands := resolve.Candidates(c, host)
	if len(cands) == 0 {
		return nil, ""
	}
	saved := r.Git.RepoGet(gitDir, "gitswitch.account")
	if resolve.AsksEveryTime(saved) {
		return nil, resolve.AsksAlways
	}
	d := resolve.Decide(c, cands, resolve.Input{
		SavedID:  saved,
		RepoPath: path,
		Worktree: func() string { return r.Git.WorktreeOf(gitDir) },
		RealPath: filesystem.RealPath,
		FoldCase: filesystem.CaseInsensitiveFS(),
	})
	if d == nil {
		return nil, resolve.Asks
	}
	return d.Account, d.Reason
}

// InfoFor reads one repo.
func (r *Reader) InfoFor(c *account.Config, gitDir string) repo.Info {
	ri := repo.Info{Path: r.Git.WorktreeOf(gitDir), GitDir: gitDir}
	ri.Remote = r.Git.RepoGet(gitDir, "remote.origin.url")
	ri.Saved = r.Git.RepoGet(gitDir, "gitswitch.account")
	ri.Email = r.Git.RepoGet(gitDir, "user.email")
	if v := r.Git.RepoGet(gitDir, "core.sshCommand"); v != "" && !account.IsHook(v) {
		ri.SSHOverride = v
	}
	ri.Branch, _ = r.Git.Run("--git-dir="+gitDir, "symbolic-ref", "--short", "-q", "HEAD")
	ri.Remotes = r.remotes(gitDir)
	ri.Added, ri.Pinned = filesystem.HasPath(c.Repos, ri.Path), filesystem.HasPath(c.Pinned, ri.Path)
	ri.Recent = recentRank(c, ri.Path)
	if a, why := r.AccountFor(c, gitDir); a != nil {
		ri.Account, ri.Reason = a.ID, why
		ri.EmailBad = ri.Email != "" && !strings.EqualFold(ri.Email, a.Email)
	} else {
		ri.Reason = why
	}
	return ri
}

// Reader reads repos with git. It implements the repository domain's Reader.
type Reader struct {
	Git *gitinfra.Runner
}

var skipDirs = map[string]bool{"node_modules": true, ".gradle": true, "build": true, "Pods": true,
	".dart_tool": true, "vendor": true, "target": true, ".venv": true, "venv": true, ".idea": true}

// remotes lists a repo's remotes in config order.
func (r *Reader) remotes(gitDir string) []repo.Remote {
	out, _ := r.Git.Run("--git-dir="+gitDir, "config", "--local", "--get-regexp", `^remote\..*\.url$`)
	rs := []repo.Remote{}
	for _, l := range strings.Split(out, "\n") {
		k, url, ok := strings.Cut(l, " ")
		if !ok {
			continue
		}
		rs = append(rs, repo.Remote{Name: strings.TrimSuffix(strings.TrimPrefix(k, "remote."), ".url"), URL: url})
	}
	return rs
}

// List is the repo list every app shows: repos found in the project folders
// plus repos added by hand, minus the ones removed from the list. Pinned
// repos come first. Added repos whose folder is gone stay, marked Missing,
// so they can be removed.
func (r *Reader) List(c *account.Config) []repo.Info {
	var dirs []string
	var missing []repo.Info
	for _, p := range c.Repos {
		if gd := r.Git.GitDirFor(p); gd != "" {
			dirs = append(dirs, gd)
		} else {
			missing = append(missing, repo.Info{Path: p, Added: true, Missing: true, Pinned: filesystem.HasPath(c.Pinned, p),
				Recent: recentRank(c, p), Remotes: []repo.Remote{}})
		}
	}
	var out []repo.Info
	for _, ri := range r.read(c, r.find(c.ScanFolders, dirs)) {
		if !filesystem.HasPath(c.HiddenRepos, ri.Path) || ri.Added {
			out = append(out, ri)
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

// recentRank is a repo's place among the recently opened ones: 1 for the
// newest, 0 when it is not there.
func recentRank(c *account.Config, path string) int {
	for i, p := range c.Recent {
		if filesystem.SamePath(p, path) {
			return i + 1
		}
	}
	return 0
}

// Scan finds repos in folders (up to 5 levels deep) and reads them in parallel.
func (r *Reader) Scan(c *account.Config, folders []string) []repo.Info {
	out := r.read(c, r.find(folders, nil))
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}

// find returns the git dirs of repos in folders, after the given ones,
// without duplicates (the same repo reached through a symlink counts once).
func (r *Reader) find(folders, dirs []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, gd := range dirs {
		if !seen[filesystem.RealPath(gd)] {
			seen[filesystem.RealPath(gd)] = true
			unique = append(unique, gd)
		}
	}
	dirs = unique
	for _, root := range folders {
		root = filesystem.ExpandHome(root)
		// WalkDir does not follow a symlinked root, so walk the folder it
		// points to and report paths under the folder as it was given.
		walk := root
		if rp, err := filepath.EvalSymlinks(root); err == nil {
			walk = rp
		}
		base := strings.Count(filepath.ToSlash(walk), "/")
		_ = filepath.WalkDir(walk, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.Count(filepath.ToSlash(p), "/")-base > 5 || (skipDirs[d.Name()] && p != walk) {
				return filepath.SkipDir
			}
			if rel, err := filepath.Rel(walk, p); err == nil {
				p = filepath.Join(root, rel)
			}
			if fi, err := os.Stat(filepath.Join(p, ".git")); err == nil {
				gd := filepath.Join(p, ".git")
				if !fi.IsDir() { // worktree or submodule: .git is a file
					gd = r.Git.GitDirFor(p)
				}
				if gd != "" && !seen[filesystem.RealPath(gd)] {
					seen[filesystem.RealPath(gd)] = true
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
func (r *Reader) read(c *account.Config, dirs []string) []repo.Info {
	out := make([]repo.Info, len(dirs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, gd := range dirs {
		wg.Add(1)
		go func(i int, gd string) {
			defer wg.Done()
			sem <- struct{}{}
			out[i] = r.InfoFor(c, gd)
			<-sem
		}(i, gd)
	}
	wg.Wait()
	return out
}
