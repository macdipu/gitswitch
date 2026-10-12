package infrastructure

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// HookRepos finds the repo git is working on during the ssh hook and reads
// or saves its account. It implements the ssh domain's HookRepos.
type HookRepos struct {
	Git *gitinfra.Runner
}

// CurrentGitDir finds the repo git is working on, including mid-clone.
func (h *HookRepos) CurrentGitDir(inv ssh.Invocation) string { return currentGitDir(h.Git, inv) }

// RememberProbe stores the repo seen during git's "-G" probe.
func (h *HookRepos) RememberProbe() { rememberProbe() }

func (h *HookRepos) SavedAccount(gitDir string) string {
	return h.Git.RepoGet(gitDir, "gitswitch.account")
}

func (h *HookRepos) SaveAccount(gitDir, id string) error {
	return h.Git.RepoSet(gitDir, "gitswitch.account", id)
}

func (h *HookRepos) Worktree(gitDir string) string { return h.Git.WorktreeOf(gitDir) }

// Transport runs the real ssh program. It implements the ssh domain's
// Transport.
type Transport struct{}

func (Transport) Find(override string) string { return process.FindSSH(override) }

func (Transport) HostKeyPolicy(sshBin string, args []string) string {
	return hostKeyPolicy(sshBin, args)
}

func (Transport) Exec(bin string, args []string) int { return process.ExecReplace(bin, args) }

// currentGitDir finds the repo git is working on, including mid-clone.
// Git removes GIT_DIR before the real transport call, so for clones we use
// the path seen by git's earlier "-G" probe (which still has GIT_DIR), and
// only trust a folder whose remote URL is the one being fetched.
func currentGitDir(git *gitinfra.Runner, inv ssh.Invocation) string {
	if d := os.Getenv("GIT_DIR"); d != "" {
		return absFromWd(d)
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	var cands []string
	if st, err := os.Stat(probeFile(wd)); err == nil && time.Since(st.ModTime()) < 5*time.Minute {
		if b, err := os.ReadFile(probeFile(wd)); err == nil {
			cands = append(cands, strings.TrimSpace(string(b)))
		}
	}
	if inv.RepoPath != "" {
		base := strings.TrimSuffix(filepath.Base(inv.RepoPath), ".git")
		cands = append(cands, filepath.Join(wd, base, ".git"))
	}
	// Last, the repo we are in, but only if it is the one being fetched:
	// cloning from inside another repo must not save or set that one.
	cands = append(cands, git.GitDirFor(wd))
	for _, c := range cands {
		if hasRemote(git, c, inv.Host, inv.RepoPath) {
			return c
		}
	}
	return ""
}

func absFromWd(d string) string {
	if !filepath.IsAbs(d) {
		if wd, err := os.Getwd(); err == nil {
			d = filepath.Join(wd, d)
		}
	}
	return d
}

// probeFile lives in the user's private cache folder, not the shared /tmp.
func probeFile(wd string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(wd))))
	return filepath.Join(filesystem.CacheDir(), "probe-"+hex.EncodeToString(h[:8]))
}

// rememberProbe stores the repo seen during git's "-G" probe.
func rememberProbe() {
	d := os.Getenv("GIT_DIR")
	if d == "" {
		return
	}
	if wd, err := os.Getwd(); err == nil {
		p := probeFile(wd)
		if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
			_ = os.WriteFile(p, []byte(absFromWd(d)), 0o600)
		}
	}
}

func hasRemote(git *gitinfra.Runner, gitDir, host, repoPath string) bool {
	if gitDir == "" || repoPath == "" {
		return false
	}
	if _, err := os.Stat(gitDir); err != nil {
		return false
	}
	// `remote -v` lists fetch and push URLs after url.<base>.insteadOf.
	out, _ := git.Run("--git-dir="+gitDir, "remote", "-v")
	want := strings.ToLower(strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git"))
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			h, p := remote.Parse(f[1])
			if remote.SameHost(h, host) && strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(p, "~"), ".git")) == want {
				return true
			}
		}
	}
	return false
}

// hostKeyPolicy reads StrictHostKeyChecking from the user's ssh config.
func hostKeyPolicy(sshBin string, args []string) string {
	cmd := exec.Command(sshBin, append([]string{"-G"}, ssh.DestArgs(args)...)...)
	process.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "stricthostkeychecking "); ok {
			switch v = strings.ToLower(strings.TrimSpace(v)); v {
			case "false":
				return "no"
			case "true":
				return "yes"
			default:
				return v
			}
		}
	}
	return ""
}
