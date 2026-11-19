package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// ErrChooseAccount means no account is decided for a new repo (no flag, no
// rule for its folder, no default) and there are several: the caller asks
// which one and calls Init again with it. Nothing has been created.
var ErrChooseAccount = errors.New("choose an account for this repo: pass --account <id>")

// GitInfo says whether git is installed and new enough.
func (s *Service) GitInfo() repo.GitInfo {
	if s.Initializer == nil {
		return repo.GitInfo{}
	}
	return repo.CheckGit(s.Initializer.Version())
}

// Init starts a repo in path (made when it does not exist; "" is the
// current folder): git init, the repo's commit name and email from an
// account, and a place in the repo list. The account is accountID, else
// the one the rules pick for the folder, else the default, else the only
// account; with several and none decided it returns ErrChooseAccount
// before creating anything. With no accounts at all the repo is made
// without an identity. branch "" keeps git's default initial branch.
func (s *Service) Init(path, accountID, branch string) (repo.Info, error) {
	if s.Initializer == nil {
		return repo.Info{}, errors.New("starting a repo is not available here")
	}
	if g := s.GitInfo(); !g.OK {
		return repo.Info{}, errors.New(g.Why)
	}
	if path = strings.TrimSpace(path); path == "" {
		path = "."
	}
	dir, err := filepath.Abs(filesystem.ExpandHome(path))
	if err != nil {
		return repo.Info{}, err
	}
	if branch = strings.TrimSpace(branch); branch != "" && !validBranchName(branch) {
		return repo.Info{}, fmt.Errorf("%q is not a valid branch name", branch)
	}
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return repo.Info{}, fmt.Errorf("%s is a file, not a folder", dir)
		}
		if gd := s.At(dir); gd != "" && filesystem.SamePath(s.Locator.WorktreeOf(gd), dir) {
			return repo.Info{}, fmt.Errorf("%s is a git repo already (add it to the list with `gitswitch repo add`)", dir)
		}
	}
	c := s.Lists.Load()
	acc, err := s.initAccount(c, dir, accountID)
	if err != nil {
		return repo.Info{}, err
	}
	if err := s.Initializer.Init(dir, branch); err != nil {
		return repo.Info{}, err
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

// initAccount picks the account for a new repo in dir: nil when there are
// no accounts.
func (s *Service) initAccount(c *account.Config, dir, accountID string) (*account.Account, error) {
	if accountID != "" {
		return c.Lookup(accountID)
	}
	if len(c.Accounts) == 0 {
		return nil, nil
	}
	d := resolve.Decide(c, c.Accounts, resolve.Input{
		Worktree: func() string { return dir },
		RealPath: filesystem.RealPath,
		FoldCase: filesystem.CaseInsensitiveFS(),
	})
	if d != nil {
		return d.Account, nil
	}
	if def := resolve.DefaultAmong(c, c.Accounts); def != nil {
		return def, nil
	}
	return nil, ErrChooseAccount
}

// validBranchName allows what git does for a new branch, without running it.
func validBranchName(b string) bool {
	if strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") ||
		strings.HasSuffix(b, ".lock") || strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{") || b == "@" {
		return false
	}
	return !strings.ContainsAny(b, " ~^:?*[\\\x7f") && strings.IndexFunc(b, func(r rune) bool { return r < 0x20 }) < 0
}
