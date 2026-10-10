// Package repo finds repos on disk and reports which account each uses.
package repo

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gitswitch/internal/account"
	"gitswitch/internal/gitcli"
	"gitswitch/internal/resolve"
	"gitswitch/internal/sys"
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
	SSHOverride string `json:"sshOverride"`
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

// Scan finds repos in folders (up to 5 levels deep) and reads them in parallel.
func Scan(git *gitcli.Runner, c *account.Config, folders []string) []Info {
	var dirs []string
	seen := map[string]bool{}
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
	// Read repos in parallel: each one starts a few short git processes.
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
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out
}
