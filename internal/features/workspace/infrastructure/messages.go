package infrastructure

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// DraftFile, in the repo's git dir, keeps the message of the last commit
// that failed.
const DraftFile = "GITSWITCH_MSG"

// signoffKey holds whether commits get a Signed-off-by trailer.
const signoffKey = "gitswitch.signoff"

// Recent reads messages of commits whose author email is email, on every
// branch and tag (not stashes, not GitSwitch's own refs), newest first.
func (g *Git) Recent(gitDir, email string, max int) ([]workspace.Recent, error) {
	if strings.TrimSpace(email) == "" || max <= 0 {
		return nil, nil
	}
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "--exclude=refs/stash", "--exclude=refs/gitswitch/*", "--all",
		"--fixed-strings", "--regexp-ignore-case", "--author=<"+email+">", "--no-show-signature",
		"-n", strconv.Itoa(max), "--format=%ct%x00%B%x1e", "--")
	if err != nil {
		if !g.hasCommits(gitDir) {
			return nil, nil
		}
		return nil, err
	}
	var list []workspace.Recent
	for _, rec := range strings.Split(out, "\x1e") {
		ts, msg, ok := strings.Cut(strings.TrimLeft(rec, "\r\n"), "\x00")
		if !ok {
			continue
		}
		sec, _ := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
		list = append(list, workspace.Recent{Message: strings.TrimSpace(msg), Time: time.Unix(sec, 0).UTC()})
	}
	return list, nil
}

// Authors reads the author names and emails of the last max commits from
// HEAD, newest first; none before the first commit.
func (g *Git) Authors(gitDir string, max int) ([]workspace.Person, error) {
	if !g.hasCommits(gitDir) {
		return nil, nil
	}
	out, err := g.Git.Run("--git-dir="+gitDir, "log", "--no-show-signature", "-n", strconv.Itoa(max), "--format=%aN%x00%aE", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	var list []workspace.Person
	for _, l := range strings.Split(out, "\n") {
		if name, email, ok := strings.Cut(l, "\x00"); ok {
			list = append(list, workspace.Person{Name: name, Email: email})
		}
	}
	return list, nil
}

func (g *Git) hasCommits(gitDir string) bool {
	_, err := g.Git.Run("--git-dir="+gitDir, "rev-parse", "--quiet", "--verify", "HEAD^{commit}")
	return err == nil
}

// Identity is user.name and user.email as git reads them in the repo (its
// own config, else the global one).
func (g *Git) Identity(gitDir string) workspace.Person {
	name, _ := g.Git.Run("--git-dir="+gitDir, "config", "--get", "user.name")
	email, _ := g.Git.Run("--git-dir="+gitDir, "config", "--get", "user.email")
	return workspace.Person{Name: strings.TrimSpace(name), Email: strings.TrimSpace(email)}
}

func (g *Git) Draft(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, DraftFile))
	if err != nil {
		return ""
	}
	return workspace.Message(string(b))
}

func (g *Git) SaveDraft(gitDir, message string) error {
	p := filepath.Join(gitDir, DraftFile)
	if message == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(p, []byte(message+"\n"), 0o644)
}

func (g *Git) SignoffValues(gitDir string) (global, repo string) {
	global = g.Git.GlobalGet(signoffKey)
	if gitDir != "" {
		repo = g.Git.RepoGet(gitDir, signoffKey)
	}
	return global, repo
}

// SaveSignoff sets gitswitch.signoff for every repo (gitDir "") or one;
// value "" removes it.
func (g *Git) SaveSignoff(gitDir, value string) error {
	var err error
	switch {
	case gitDir == "" && value == "":
		_, err = g.Git.Global("--unset", signoffKey)
	case gitDir == "":
		_, err = g.Git.Global(signoffKey, value)
	case value == "":
		err = g.Git.RepoUnset(gitDir, signoffKey)
	default:
		return g.Git.RepoSet(gitDir, signoffKey, value)
	}
	if value == "" && gitinfra.ExitCode(err) == 5 { // was not set
		return nil
	}
	return err
}
