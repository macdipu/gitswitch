// Package application holds the submodule use cases: list a repo's
// submodules with their state and account, initialize, update, sync, add
// and remove them. Every download runs as the account the submodule's own
// URL maps to: git's ssh hook picks it per submodule, so nothing here
// chooses a key. It depends only on domain ports. Import it as submoduleapp.
package application

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	sub "github.com/macdipu/gitswitch/internal/features/submodule/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the submodule use cases.
type Service struct {
	Git      sub.Git
	Accounts account.Store
	Repos    repo.Reader
	Locator  repo.Locator
	FS       *filesystem.FS // dry-run: changes are only printed
}

func (s *Service) worktree(gitDir string) (string, error) {
	wt := s.Locator.WorktreeOf(gitDir)
	if wt == "" || wt == gitDir {
		return "", errors.New("this repo has no working folder, so it has no submodules to work on")
	}
	return wt, nil
}

// List is the repo's submodules, in the order .gitmodules lists them.
func (s *Service) List(gitDir string) ([]sub.Entry, error) {
	wt, err := s.worktree(gitDir)
	if err != nil {
		return nil, err
	}
	return s.list(gitDir, wt, true)
}

func (s *Service) list(gitDir, wt string, details bool) ([]sub.Entry, error) {
	raw, err := s.Git.Modules(wt)
	if err != nil {
		return nil, err
	}
	mods := sub.ParseModules(raw)
	if len(mods) == 0 {
		return []sub.Entry{}, nil
	}
	plain, err := s.Git.Status(wt, false)
	if err != nil {
		return nil, err
	}
	cached, err := s.Git.Status(wt, true)
	if err != nil {
		return nil, err
	}
	es := sub.Build(mods, sub.ParseStatus(plain), sub.ParseStatus(cached))
	if !details {
		return es, nil
	}
	origin := s.Git.Origin(wt)
	var cfg *account.Config
	if s.Accounts != nil {
		cfg = s.Accounts.Load()
	}
	var wg sync.WaitGroup
	for i := range es {
		wg.Add(1)
		go func(e *sub.Entry) {
			defer wg.Done()
			saved := ""
			if e.Initialized {
				e.Dirty = s.Git.Dirty(wt, e.Path)
				e.Upstream, e.Ahead, e.Behind = s.Git.Upstream(wt, e.Path, e.Recorded, e.Branch)
				if cfg != nil && s.Repos != nil {
					if gd := s.Git.GitDir(wt, e.Path); gd != "" {
						saved = s.Repos.InfoFor(cfg, gd).Saved
					}
				}
			}
			e.HasRepoConfigAccount = saved != ""
			if cfg != nil {
				e.Account, e.AccountReason = s.accountFor(cfg, sub.ResolveURL(origin, e.URL), saved, filepath.Join(wt, filepath.FromSlash(e.Path)))
			}
		}(&es[i])
	}
	wg.Wait()
	return es, nil
}

// accountFor is the account git's hook would pick for url in a folder: the
// submodule's own saved account, else the owner rule, the folder rule, the
// only account; "" when git would ask.
func (s *Service) accountFor(cfg *account.Config, url, saved, folder string) (id, reason string) {
	host, p := remote.Parse(url)
	if host == "" {
		return "", ""
	}
	cands := resolve.Candidates(cfg, host)
	d := resolve.Decide(cfg, cands, resolve.Input{SavedID: saved, RepoPath: p, Worktree: func() string { return folder }})
	if d == nil {
		return "", ""
	}
	return d.Account.ID, d.Reason
}

// Init downloads and checks out the submodules that are not initialized
// yet (all of them, or the one at path).
func (s *Service) Init(gitDir, path string) (sub.Updated, error) {
	wt, err := s.worktree(gitDir)
	if err != nil {
		return sub.Updated{}, err
	}
	if path, err = cleanPath(path); err != nil {
		return sub.Updated{}, err
	}
	es, err := s.list(gitDir, wt, false)
	if err != nil {
		return sub.Updated{}, err
	}
	ts, err := sub.Targets(es, path)
	if err != nil {
		return sub.Updated{}, err
	}
	var paths []string
	for _, e := range ts {
		if !e.Initialized {
			paths = append(paths, e.Path)
		}
	}
	if len(paths) == 0 {
		if path != "" {
			return sub.Updated{Message: path + " is already initialized; gitswitch submodule update brings it to the recorded commit"}, nil
		}
		return sub.Updated{Message: "Every submodule is already initialized"}, nil
	}
	if s.FS.Dry(fmt.Sprintf("initialize %s in %s", strings.Join(paths, ", "), wt)) {
		return sub.Updated{}, nil
	}
	if err := s.Git.Update(wt, paths, false, true); err != nil {
		return sub.Updated{}, err
	}
	return sub.Updated{Message: fmt.Sprintf("Initialized %s", strings.Join(paths, ", "))}, nil
}

// Update brings the submodules (all, or the one at path) to the commits
// the superproject records, initializing missing ones. With remote it
// moves them to the tip of their tracked branch instead, which changes
// the commit the superproject records. It refuses when that would
// overwrite changes inside a submodule, and says which.
func (s *Service) Update(gitDir, path string, remote, recursive bool) (sub.Updated, error) {
	wt, err := s.worktree(gitDir)
	if err != nil {
		return sub.Updated{}, err
	}
	if path, err = cleanPath(path); err != nil {
		return sub.Updated{}, err
	}
	es, err := s.list(gitDir, wt, true)
	if err != nil {
		return sub.Updated{}, err
	}
	ts, err := sub.Targets(es, path)
	if err != nil {
		return sub.Updated{}, err
	}
	if len(ts) == 0 {
		return sub.Updated{Message: "This repo has no submodules"}, nil
	}
	if msg := sub.CheckUpdate(ts, remote); msg != "" {
		return sub.Updated{}, errors.New("can't update: " + msg)
	}
	what := "recorded commits"
	if remote {
		what = "tips of their tracked branches"
	}
	if s.FS.Dry(fmt.Sprintf("update submodules in %s to the %s", wt, what)) {
		return sub.Updated{}, nil
	}
	var paths []string
	if path != "" {
		paths = []string{path}
	}
	if err := s.Git.Update(wt, paths, remote, recursive); err != nil {
		return sub.Updated{}, err
	}
	after, err := s.list(gitDir, wt, false)
	if err != nil {
		return sub.Updated{}, err
	}
	r := sub.Updated{Changed: []sub.Change{}}
	for _, b := range ts {
		if a, ok := sub.Find(after, b.Path); ok && a.CheckedOut != b.CheckedOut {
			r.Changed = append(r.Changed, sub.Change{Path: b.Path, From: b.CheckedOut, To: a.CheckedOut})
		}
	}
	switch {
	case len(r.Changed) == 0:
		r.Message = "Submodules are already where they should be"
	default:
		r.Message = fmt.Sprintf("Updated %d submodule(s)", len(r.Changed))
	}
	if remote && len(r.Changed) > 0 {
		r.RecordedChanged = true
		r.Message += "; the repo now records different commits for them: commit that change to keep it"
	}
	return r, nil
}

// Sync copies the URLs in .gitmodules to the submodules' own settings
// (after the URL in .gitmodules changed), recursively.
func (s *Service) Sync(gitDir string) (sub.Updated, error) {
	wt, err := s.worktree(gitDir)
	if err != nil {
		return sub.Updated{}, err
	}
	if s.FS.Dry("sync submodule URLs in " + wt) {
		return sub.Updated{}, nil
	}
	if err := s.Git.Sync(wt); err != nil {
		return sub.Updated{}, err
	}
	return sub.Updated{Message: "Submodule URLs now match .gitmodules"}, nil
}

// Add clones url into path as a submodule, tracking branch when given. It
// refuses a path that is taken, and says which account the URL uses,
// warning when that is not the repo's own.
func (s *Service) Add(gitDir, url, path, branch string) (sub.Added, error) {
	r := sub.Added{URL: strings.TrimSpace(url), Branch: strings.TrimSpace(branch)}
	wt, err := s.worktree(gitDir)
	if err != nil {
		return r, err
	}
	if err := remote.CheckCloneURL(r.URL); err != nil {
		return r, err
	}
	if r.Path, err = sub.CheckPath(path); err != nil {
		return r, err
	}
	if err := sub.CheckBranch(r.Branch); err != nil {
		return r, err
	}
	if s.Git.PathExists(wt, r.Path) {
		return r, fmt.Errorf("%s already exists in this repo; choose another path", r.Path)
	}
	if s.Accounts != nil {
		cfg := s.Accounts.Load()
		r.Account, _ = s.accountFor(cfg, r.URL, "", filepath.Join(wt, filepath.FromSlash(r.Path)))
		repoAcct := ""
		if s.Repos != nil {
			repoAcct = s.Repos.InfoFor(cfg, gitDir).Account
		}
		r.Note = sub.AccountNote(r.URL, repoAcct, r.Account)
	}
	if s.FS.Dry(fmt.Sprintf("add the submodule %s at %s in %s", r.URL, r.Path, wt)) {
		return r, nil
	}
	if err := s.Git.Add(wt, r.URL, r.Path, r.Branch); err != nil {
		return r, err
	}
	r.Message = fmt.Sprintf("Added %s at %s (staged; commit to keep it)", r.URL, r.Path)
	return r, nil
}

// PlanRemove says what removing the submodule at path would delete, and
// why it can't now (Blocked), unless force.
func (s *Service) PlanRemove(gitDir, path string, force bool) (sub.RemoveInfo, error) {
	_, e, unpushed, _, err := s.removeTarget(gitDir, path)
	if err != nil {
		return sub.RemoveInfo{}, err
	}
	return sub.RemoveInfo{Path: e.Path, Deleted: sub.RemovePlan(e, unpushed), Blocked: sub.CheckRemove(e, unpushed, force)}, nil
}

func (s *Service) removeTarget(gitDir, path string) (wt string, e sub.Entry, unpushed int, es []sub.Entry, err error) {
	if wt, err = s.worktree(gitDir); err != nil {
		return
	}
	if path, err = cleanPath(path); err != nil {
		return
	}
	if path == "" {
		err = errors.New("say which submodule to remove (its path)")
		return
	}
	if es, err = s.list(gitDir, wt, true); err != nil {
		return
	}
	var ts []sub.Entry
	if ts, err = sub.Targets(es, path); err != nil {
		return
	}
	e = ts[0]
	if e.Initialized {
		unpushed = s.Git.Unpushed(wt, e.Path)
	}
	return
}

// Remove takes a submodule out of the repo: empties and removes its
// folder, its .gitmodules entry and its downloaded copy under
// .git/modules. The removal is staged, not committed. It refuses while
// the submodule holds changes or commits that exist nowhere else, unless
// force.
func (s *Service) Remove(gitDir, path string, force bool) (sub.Removed, error) {
	wt, e, unpushed, _, err := s.removeTarget(gitDir, path)
	if err != nil {
		return sub.Removed{}, err
	}
	r := sub.Removed{Path: e.Path, Deleted: sub.RemovePlan(e, unpushed)}
	if msg := sub.CheckRemove(e, unpushed, force); msg != "" {
		return r, errors.New("can't remove: " + msg)
	}
	if err := sub.CheckModuleName(e.Name); err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("remove the submodule %s from %s", e.Path, wt)) {
		return r, nil
	}
	if err := s.Git.Deinit(wt, e.Path); err != nil {
		return r, err
	}
	if err := s.Git.RemovePath(wt, e.Path); err != nil {
		return r, err
	}
	if err := s.Git.DropModule(wt, e.Name); err != nil {
		return r, err
	}
	r.Message = fmt.Sprintf("Removed %s (staged; commit to keep the removal)", e.Path)
	return r, nil
}

// Register returns the working folder of an initialized submodule, so it
// can be added to the repo list (gitswitch repo add <folder>).
func (s *Service) Register(gitDir, path string) (string, error) {
	wt, err := s.worktree(gitDir)
	if err != nil {
		return "", err
	}
	if path, err = cleanPath(path); err != nil {
		return "", err
	}
	es, err := s.list(gitDir, wt, false)
	if err != nil {
		return "", err
	}
	ts, err := sub.Targets(es, path)
	if err != nil || path == "" {
		if err == nil {
			err = errors.New("say which submodule to open (its path)")
		}
		return "", err
	}
	if !ts[0].Initialized {
		return "", fmt.Errorf("%s is not initialized yet; run gitswitch submodule init %s first", path, path)
	}
	return filepath.Join(wt, filepath.FromSlash(ts[0].Path)), nil
}

func cleanPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", nil
	}
	return sub.CheckPath(p)
}
