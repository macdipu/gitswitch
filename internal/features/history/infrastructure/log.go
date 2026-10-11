// Package infrastructure reads the commit history with git and rewrites
// commit authors. Import it as historyinfra.
package infrastructure

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Log reads commits with git. It implements the history domain's Log.
type Log struct {
	Git *gitinfra.Runner
}

func (l *Log) Commits(gitDir string, limit int) ([]history.Commit, error) {
	git := l.Git
	out, err := git.Run("--git-dir="+gitDir, "log", "--all", "--topo-order", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%P%x1f%ae%x1f%an%x1f%at%x1f%D%x1f%s%x1e")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var cs []history.Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		t, _ := strconv.ParseInt(f[4], 10, 64)
		c := history.Commit{Hash: f[0], Email: f[2], Author: f[3], Time: t, Refs: f[5], Subject: f[6]}
		if len(c.Hash) >= 7 {
			c.Short = c.Hash[:7]
		}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		cs = append(cs, c)
	}
	return cs, nil
}

func (l *Log) Unpushed(gitDir string) map[string]bool {
	git := l.Git
	set := map[string]bool{}
	out, err := git.Run("--git-dir="+gitDir, "rev-list", "--branches", "--not", "--remotes")
	if err != nil {
		return set
	}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

func (l *Log) Graph(gitDir string, n int, color bool) (string, error) {
	gargs := []string{"--git-dir=" + gitDir, "log", "--graph", "--all", "-n", strconv.Itoa(n), "--format=%x1f%H"}
	if color {
		gargs = append(gargs, "--color=always")
	}
	out, err := l.Git.Run(gargs...)
	if err != nil && strings.Contains(err.Error(), "does not have any commits") {
		return "", history.ErrNoCommits
	}
	return out, err
}

func (l *Log) LocalCommits(wt string) ([]string, error) {
	out, err := l.Git.Run("-C", wt, "rev-list", "--reverse", "HEAD", "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (l *Log) AuthorEmail(wt, hash string) string {
	e, _ := l.Git.Run("-C", wt, "log", "-1", "--format=%ae", hash)
	return e
}

func (l *Log) HeadAuthorName(wt string) string {
	name, _ := l.Git.Run("-C", wt, "log", "-1", "--format=%an", "HEAD")
	return name
}

func (l *Log) RewriteAuthor(wt, name, email, first string) error {
	git := l.Git
	safe := strings.NewReplacer(`"`, "", "`", "", "$", "", `\`, "").Replace
	author := safe(name) + " <" + safe(email) + ">"
	execLine := `if [ "$(git log -1 --format=%ae)" != "` + safe(email) + `" ]; then git commit --amend --allow-empty --no-edit --no-verify --author="` + author + `"; fi`
	args := []string{"-C", wt, "-c", "user.email=" + email, "-c", "user.name=" + name,
		"rebase", "--rebase-merges", "--keep-empty", "--autostash", "--exec", execLine}
	if p, err := git.Run("-C", wt, "rev-parse", "--verify", "-q", first+"^"); err == nil && p != "" {
		args = append(args, p)
	} else {
		args = append(args, "--root")
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
	process.HideWindow(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		_, _ = git.Run("-C", wt, "rebase", "--abort")
		return fmt.Errorf("rewrite failed, nothing changed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}
