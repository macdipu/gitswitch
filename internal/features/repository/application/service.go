// Package application holds the repo list use cases: show, add, clone,
// remove, pin and open repos. Adding or removing a repo changes only
// gitswitch's own list, never the repo. It depends only on domain ports.
// Import it as repoapp.
package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Service runs the repo use cases.
type Service struct {
	Lists        repo.ListStore
	Reader       repo.Reader
	Locator      repo.Locator
	Cloner       repo.Cloner
	RepoAccounts account.RepoAccounts
	DryRun       bool // clones are only printed
}

// At returns the git dir for a folder, or "" when it is not in a repo.
func (s *Service) At(path string) string { return s.Locator.GitDirFor(path) }

// Current returns the git dir for the working folder, or "".
func (s *Service) Current() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return s.At(wd)
}

// For returns the git dir for a repo folder or git dir, or an error when
// the path is not a git repo.
func (s *Service) For(path string) (string, error) {
	if path != "" {
		if gd := s.At(s.Locator.WorktreeOf(path)); gd != "" {
			return gd, nil
		}
	}
	return "", fmt.Errorf("not a git repo: %s", path)
}

// Info describes one repo.
func (s *Service) Info(gitDir string) repo.Info { return s.Reader.InfoFor(s.Lists.Load(), gitDir) }

// List is the repo list: repos in the project folders plus repos added by
// hand, pinned first.
func (s *Service) List() []repo.Info { return s.Reader.List(s.Lists.Load()) }

// Add adds an existing repo to the list. It only records the folder in
// gitswitch's settings; the repo itself is not changed.
func (s *Service) Add(path string) (repo.Info, error) {
	gd, err := s.For(filesystem.ExpandHome(path))
	if err != nil {
		return repo.Info{}, err
	}
	root := s.Locator.WorktreeOf(gd)
	if err := s.Lists.AddRepoPath(repo.ListAdded, root); err != nil {
		return repo.Info{}, err
	}
	if err := s.Lists.RemoveRepoPath(repo.ListHidden, root); err != nil {
		return repo.Info{}, err
	}
	return s.Info(gd), nil
}

// Clone clones url into dir (which must not exist, or be empty) and adds it
// to the repo list. With accountID the clone uses that account from the
// first fetch: the account is saved in the new repo before git downloads
// anything. Without one, rules decide, then git asks as usual. Git's
// progress lines go to progress.
func (s *Service) Clone(url, dir, accountID string, progress func(string)) (repo.Info, error) {
	url = strings.TrimSpace(url)
	if err := remote.CheckCloneURL(url); err != nil {
		return repo.Info{}, err
	}
	var acc *account.Account
	if accountID != "" {
		var err error
		if acc, err = s.Lists.Load().Lookup(accountID); err != nil {
			return repo.Info{}, err
		}
		if host, _ := remote.Parse(url); !remote.SameHost(host, acc.Host) {
			return repo.Info{}, fmt.Errorf("%s is an account on %s, but this repo is on %s", acc.ID, acc.Host, host)
		}
	}
	if dir = strings.TrimSpace(dir); dir == "" {
		return repo.Info{}, errors.New("choose a folder to clone into")
	}
	dir, err := filepath.Abs(filesystem.ExpandHome(dir))
	if err != nil {
		return repo.Info{}, err
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return repo.Info{}, fmt.Errorf("%s already exists and is not empty; choose another folder", dir)
	} else if err != nil && !os.IsNotExist(err) {
		return repo.Info{}, fmt.Errorf("cannot use %s: %v", dir, err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return repo.Info{}, err
	}
	id := ""
	if acc != nil {
		id = acc.ID
	}
	if err := s.Cloner.Clone(filepath.Dir(dir), url, filepath.Base(dir), id, progress); err != nil {
		return repo.Info{}, cloneError(err, acc)
	}
	if s.DryRun {
		return repo.Info{Path: dir}, nil
	}
	gd := s.At(dir)
	if gd == "" {
		return repo.Info{}, fmt.Errorf("git finished, but %s is not a repo", dir)
	}
	if acc != nil {
		if _, err := s.RepoAccounts.SetRepoAccount(gd, acc); err != nil {
			return repo.Info{}, err
		}
	}
	return s.Add(dir)
}

// cloneError adds a hint to git's message for the usual failures.
func cloneError(err error, acc *account.Account) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "permission denied") || strings.Contains(low, "repository not found") || strings.Contains(low, "could not read from remote"):
		who := "the chosen account"
		if acc != nil {
			who = acc.ID
		}
		return fmt.Errorf("%s\n\nThe repo does not exist, or %s cannot access it. Check the URL, and that the key works: gitswitch test", msg, who)
	case strings.Contains(low, "could not resolve host") || strings.Contains(low, "network is unreachable") || strings.Contains(low, "timed out"):
		return fmt.Errorf("%s\n\nCould not reach the server. Check your network connection.", msg)
	}
	return err
}

// Remove takes a repo off the list without touching its files, remotes or
// settings. A repo found in a project folder is hidden from the list.
func (s *Service) Remove(path string) error {
	root, listed := s.listed(path)
	c := s.Lists.Load()
	if !listed {
		return fmt.Errorf("not in the repo list: %s", root)
	}
	if err := s.Lists.RemoveRepoPath(repo.ListAdded, root); err != nil {
		return err
	}
	if err := s.Lists.RemoveRepoPath(repo.ListPinned, root); err != nil {
		return err
	}
	for _, r := range s.Reader.Scan(c, c.ScanFolders) {
		if samePlace(r.Path, root) {
			return s.Lists.AddRepoPath(repo.ListHidden, root)
		}
	}
	return nil
}

// Pin pins a listed repo to the top of the list, or unpins it.
func (s *Service) Pin(path string, on bool) error {
	root, listed := s.listed(path)
	if !on {
		return s.Lists.RemoveRepoPath(repo.ListPinned, root)
	}
	if listed {
		return s.Lists.AddRepoPath(repo.ListPinned, root)
	}
	return fmt.Errorf("not in the repo list: %s (add it first: gitswitch repo add)", root)
}

// Open opens a repo folder in the file manager, or with editor in the
// configured editor (gitswitch.editor, then $VISUAL, then $EDITOR).
func (s *Service) Open(path string, editor bool) error {
	root := s.root(path)
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("folder not found: %s", root)
	}
	if !editor {
		return process.OpenFolder(root)
	}
	cmd := s.Lists.Load().Editor
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if cmd == "" {
			cmd = os.Getenv(v)
		}
	}
	if cmd == "" {
		return errors.New("no editor set; choose one with: gitswitch set editor <command> (e.g. code)")
	}
	return process.StartProgram(cmd, root)
}

// listed finds the repo holding path in the list. It returns the path as
// the list shows it, which can differ from git's when a project folder is
// reached through a symlink (/var and /private/var on macOS).
func (s *Service) listed(path string) (string, bool) {
	root := s.root(path)
	for _, r := range s.List() {
		if samePlace(r.Path, root) {
			return r.Path, true
		}
	}
	return root, false
}

// samePlace reports whether two paths name the same folder, following
// symlinks only when the paths differ.
func samePlace(a, b string) bool {
	return filesystem.SamePath(a, b) || filesystem.SamePath(filesystem.RealPath(a), filesystem.RealPath(b))
}

// root turns a folder inside a repo into the repo's working folder. A
// folder that is no longer a repo (e.g. deleted) is returned as given.
func (s *Service) root(path string) string {
	p := filesystem.ExpandHome(path)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if gd := s.At(p); gd != "" {
		return s.Locator.WorktreeOf(gd)
	}
	return p
}
