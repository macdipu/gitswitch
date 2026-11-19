// Package infrastructure runs the submodule commands with git. Downloads go
// through git's ssh hook, which picks the account of each submodule's own
// URL, so the environment is passed on untouched. Import it as submoduleinfra.
package infrastructure

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	sub "github.com/macdipu/gitswitch/internal/features/submodule/domain"
)

// DirtyTimeout caps how long counting the changes inside one submodule may
// take; a huge working folder gives "unknown" (-1) instead of a hang.
const DirtyTimeout = 4 * time.Second

// Git implements the submodule domain's Git.
type Git struct {
	Git *gitinfra.Runner
}

var _ sub.Git = (*Git)(nil)

func (g *Git) in(wt string, args ...string) (string, error) {
	return g.Git.RunIn(wt, nil, "", false, args...)
}

func (g *Git) Modules(wt string) (string, error) {
	if _, err := os.Stat(filepath.Join(wt, ".gitmodules")); err != nil {
		return "", nil
	}
	out, err := g.Git.RunIn(wt, nil, "", true, "config", "-f", ".gitmodules", "-z", "--get-regexp", `^submodule\.`)
	if gitinfra.ExitCode(err) == 1 {
		return "", nil // nothing configured
	}
	return out, err
}

func (g *Git) Status(wt string, cached bool) (string, error) {
	args := []string{"submodule", "status"}
	if cached {
		args = append(args, "--cached")
	}
	return g.in(wt, args...)
}

func (g *Git) Origin(wt string) string {
	out, _ := g.in(wt, "config", "--get", "remote.origin.url")
	return out
}

func (g *Git) Dirty(wt, path string) int {
	done := make(chan int, 1)
	go func() {
		out, err := g.Git.RunIn(filepath.Join(wt, filepath.FromSlash(path)), nil, "", true, "--no-optional-locks", "status", "--porcelain")
		if err != nil {
			done <- -1
			return
		}
		n := 0
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(l) != "" {
				n++
			}
		}
		done <- n
	}()
	select {
	case n := <-done:
		return n
	case <-time.After(DirtyTimeout):
		return -1
	}
}

func (g *Git) Upstream(wt, path, recorded, branch string) (string, int, int) {
	dir := filepath.Join(wt, filepath.FromSlash(path))
	var cands []string
	if branch != "" && branch != "." {
		cands = append(cands, "origin/"+branch)
	}
	cands = append(cands, "origin/HEAD")
	for _, up := range cands {
		if out, err := g.in(dir, "rev-parse", "-q", "--verify", "refs/remotes/"+up+"^{commit}"); err != nil || out == "" {
			continue
		}
		out, err := g.in(dir, "rev-list", "--left-right", "--count", recorded+"..."+"refs/remotes/"+up)
		if err != nil {
			return "", 0, 0
		}
		a, b, _ := strings.Cut(strings.ReplaceAll(out, "\t", " "), " ")
		ahead, _ := strconv.Atoi(strings.TrimSpace(a))
		behind, _ := strconv.Atoi(strings.TrimSpace(b))
		name := up
		if up == "origin/HEAD" {
			if t, err := g.in(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil && t != "" {
				name = t
			}
		}
		return name, ahead, behind
	}
	return "", 0, 0
}

func (g *Git) Unpushed(wt, path string) int {
	out, err := g.in(filepath.Join(wt, filepath.FromSlash(path)), "rev-list", "--count", "HEAD", "--branches", "--not", "--remotes")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

func (g *Git) GitDir(wt, path string) string {
	dir := filepath.Join(wt, filepath.FromSlash(path))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "" // an empty folder would answer with the superproject's own git dir
	}
	out, err := g.in(dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	return out
}

func (g *Git) PathExists(wt, path string) bool {
	if _, err := os.Lstat(filepath.Join(wt, filepath.FromSlash(path))); err == nil {
		return true
	}
	out, _ := g.in(wt, "ls-files", "--", path)
	return out != ""
}

func (g *Git) Update(wt string, paths []string, remote, recursive bool) error {
	args := []string{"submodule", "update", "--init"}
	if recursive {
		args = append(args, "--recursive")
	}
	if remote {
		args = append(args, "--remote")
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	_, err := g.in(wt, args...)
	return err
}

func (g *Git) Sync(wt string) error {
	_, err := g.in(wt, "submodule", "sync", "--recursive")
	return err
}

func (g *Git) Add(wt, url, path, branch string) error {
	args := []string{"submodule", "add"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	args = append(args, "--", url, path)
	_, err := g.in(wt, args...)
	return err
}

func (g *Git) Deinit(wt, path string) error {
	_, err := g.in(wt, "submodule", "deinit", "-f", "--", path)
	return err
}

func (g *Git) RemovePath(wt, path string) error {
	_, err := g.in(wt, "rm", "-f", "--", path)
	return err
}

func (g *Git) DropModule(wt, name string) error {
	if err := sub.CheckModuleName(name); err != nil {
		return err
	}
	out, err := g.in(wt, "rev-parse", "--git-path", "modules/"+name)
	if err != nil || out == "" {
		return err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(wt, out)
	}
	if !strings.Contains(filepath.ToSlash(out), "/modules/") {
		return nil
	}
	return os.RemoveAll(out)
}
