// Package app is the single API used by every front end (command line,
// terminal app and desktop window). Front ends hold
// no logic of their own: they call App methods and display the results.
//
// App methods return values and errors; they never print. Progress
// messages from long operations go to the report callback the caller gives.
package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/doctor"
	"github.com/macdipu/gitswitch/internal/gitcli"
	"github.com/macdipu/gitswitch/internal/history"
	"github.com/macdipu/gitswitch/internal/keys"
	"github.com/macdipu/gitswitch/internal/repo"
	"github.com/macdipu/gitswitch/internal/setup"
	"github.com/macdipu/gitswitch/internal/sys"
)

// Version is set at build time with -ldflags "-X gitswitch/internal/app.Version=v1.2.0".
var Version = "dev"

// App wires the layers together.
type App struct {
	Git       *gitcli.Runner
	FS        *sys.FS
	Store     *account.Store
	Installer *setup.Installer
	Doctor    *doctor.Doctor
}

// New creates an App. With dryRun, changes are printed to out instead of made.
func New(dryRun bool, out io.Writer) *App {
	if out == nil {
		out = os.Stdout
	}
	g := &gitcli.Runner{DryRun: dryRun, Out: out}
	fs := &sys.FS{DryRun: dryRun, Out: out}
	st := &account.Store{Git: g, FS: fs}
	in := &setup.Installer{Git: g, FS: fs, Store: st}
	return &App{Git: g, FS: fs, Store: st, Installer: in, Doctor: &doctor.Doctor{Git: g, Store: st, Installer: in}}
}

// DryRun reports whether changes are only being printed.
func (a *App) DryRun() bool { return a.FS.DryRun }

// ---------------------------------------------------------------- queries

// Config returns the current settings, read fresh from git config.
func (a *App) Config() *account.Config { return a.Store.Load() }

// Account returns an account by id (following renames) or an error.
func (a *App) Account(id string) (*account.Account, error) {
	acc := a.Config().Account(id)
	if acc == nil {
		return nil, fmt.Errorf("no account %q (see `gitswitch account list`)", id)
	}
	return acc, nil
}

// Installed reports whether git currently runs through gitswitch.
func (a *App) Installed() bool {
	return a.Git.GlobalGet("core.sshCommand") == account.HookValue(setup.InstalledPath())
}

// Hook returns git's current core.sshCommand.
func (a *App) Hook() string { return a.Git.GlobalGet("core.sshCommand") }

// RepoAt returns the git dir for a folder, or "" when it is not in a repo.
func (a *App) RepoAt(path string) string { return a.Git.GitDirFor(path) }

// CurrentRepo returns the git dir for the working folder, or "".
func (a *App) CurrentRepo() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return a.RepoAt(wd)
}

// RepoFor returns the git dir for a repo folder or git dir, or an error
// when the path is not a git repo.
func (a *App) RepoFor(path string) (string, error) {
	if path != "" {
		if gd := a.RepoAt(a.Git.WorktreeOf(path)); gd != "" {
			return gd, nil
		}
	}
	return "", fmt.Errorf("not a git repo: %s", path)
}

// SuggestedName is the commit name to offer for a new account.
func (a *App) SuggestedName() string {
	if n := a.Git.GlobalGet("gitswitch.backup.name"); n != "" {
		return n
	}
	return a.Git.GlobalGet("user.name")
}

// RepoInfo describes one repo.
func (a *App) RepoInfo(gitDir string) repo.Info { return repo.InfoFor(a.Git, a.Config(), gitDir) }

// Repos is the repo list: repos in the project folders plus repos added
// by hand, pinned first.
func (a *App) Repos() []repo.Info { return repo.List(a.Git, a.Config()) }

// AddRepo adds an existing repo to the list. It only records the folder in
// gitswitch's settings; the repo itself is not changed.
func (a *App) AddRepo(path string) (repo.Info, error) {
	gd, err := a.RepoFor(sys.ExpandHome(path))
	if err != nil {
		return repo.Info{}, err
	}
	root := a.Git.WorktreeOf(gd)
	if err := a.Store.AddRepoPath("repo", root); err != nil {
		return repo.Info{}, err
	}
	if err := a.Store.RemoveRepoPath("hideRepo", root); err != nil {
		return repo.Info{}, err
	}
	return a.RepoInfo(gd), nil
}

// CloneRepo clones url into dir (which must not exist, or be empty) and
// adds it to the repo list. With accountID the clone uses that account
// from the first fetch: the account is saved in the new repo before git
// downloads anything. Without one, rules decide, then git asks as usual.
// Git's progress lines go to progress.
func (a *App) CloneRepo(url, dir, accountID string, progress func(string)) (repo.Info, error) {
	url = strings.TrimSpace(url)
	if err := gitcli.CheckCloneURL(url); err != nil {
		return repo.Info{}, err
	}
	var acc *account.Account
	if accountID != "" {
		var err error
		if acc, err = a.Account(accountID); err != nil {
			return repo.Info{}, err
		}
		if host, _ := gitcli.ParseRemote(url); !gitcli.SameHost(host, acc.Host) {
			return repo.Info{}, fmt.Errorf("%s is an account on %s, but this repo is on %s", acc.ID, acc.Host, host)
		}
	}
	if dir = strings.TrimSpace(dir); dir == "" {
		return repo.Info{}, errors.New("choose a folder to clone into")
	}
	dir, err := filepath.Abs(sys.ExpandHome(dir))
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
	args := []string{"clone", "--progress"}
	if acc != nil {
		args = append(args, "-c", "gitswitch.account="+acc.ID)
	}
	// Run from the parent folder, like a person typing `git clone`, so the
	// ssh hook can find the new repo while it is being cloned.
	args = append(args, "--", url, filepath.Base(dir))
	if err := a.Git.Stream(filepath.Dir(dir), progress, args...); err != nil {
		return repo.Info{}, cloneError(err, acc)
	}
	if a.DryRun() {
		return repo.Info{Path: dir}, nil
	}
	gd := a.RepoAt(dir)
	if gd == "" {
		return repo.Info{}, fmt.Errorf("git finished, but %s is not a repo", dir)
	}
	if acc != nil {
		if _, err := a.SetRepoAccount(gd, acc.ID); err != nil {
			return repo.Info{}, err
		}
	}
	return a.AddRepo(dir)
}

// RepoName is the folder name git clone would create for a URL.
func RepoName(url string) string { return gitcli.RepoName(url) }

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

// RemoveRepo takes a repo off the list without touching its files, remotes
// or settings. A repo found in a project folder is hidden from the list.
func (a *App) RemoveRepo(path string) error {
	root := a.repoRoot(path)
	c := a.Config()
	listed := false
	for _, r := range a.Repos() {
		if sys.SamePath(r.Path, root) {
			listed = true
		}
	}
	if !listed {
		return fmt.Errorf("not in the repo list: %s", root)
	}
	if err := a.Store.RemoveRepoPath("repo", root); err != nil {
		return err
	}
	if err := a.Store.RemoveRepoPath("pin", root); err != nil {
		return err
	}
	for _, r := range repo.Scan(a.Git, c, c.ScanFolders) {
		if sys.SamePath(r.Path, root) {
			return a.Store.AddRepoPath("hideRepo", root)
		}
	}
	return nil
}

// PinRepo pins a listed repo to the top of the list, or unpins it.
func (a *App) PinRepo(path string, on bool) error {
	root := a.repoRoot(path)
	if !on {
		return a.Store.RemoveRepoPath("pin", root)
	}
	for _, r := range a.Repos() {
		if sys.SamePath(r.Path, root) {
			return a.Store.AddRepoPath("pin", root)
		}
	}
	return fmt.Errorf("not in the repo list: %s (add it first: gitswitch repo add)", root)
}

// OpenRepo opens a repo folder in the file manager, or with editor in the
// configured editor (gitswitch.editor, then $VISUAL, then $EDITOR).
func (a *App) OpenRepo(path string, editor bool) error {
	root := a.repoRoot(path)
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("folder not found: %s", root)
	}
	if !editor {
		return sys.OpenFolder(root)
	}
	cmd := a.Config().Editor
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if cmd == "" {
			cmd = os.Getenv(v)
		}
	}
	if cmd == "" {
		return errors.New("no editor set; choose one with: gitswitch set editor <command> (e.g. code)")
	}
	return sys.StartProgram(cmd, root)
}

// repoRoot turns a folder inside a repo into the repo's working folder.
// A folder that is no longer a repo (e.g. deleted) is returned as given.
func (a *App) repoRoot(path string) string {
	p := sys.ExpandHome(path)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if gd := a.RepoAt(p); gd != "" {
		return a.Git.WorktreeOf(gd)
	}
	return p
}

// History builds a repo's commit graph.
func (a *App) History(gitDir string, n int) (*history.History, error) {
	return history.Build(a.Git, a.Config(), gitDir, n)
}

// HistoryLines renders git's graph with accounts (terminal output).
func (a *App) HistoryLines(gitDir string, n int, flaggedOnly, color bool) ([]string, *history.History, error) {
	return history.Lines(a.Git, a.Config(), gitDir, n, flaggedOnly, color)
}

// Checks runs the health checks; deep also scans project folders.
func (a *App) Checks(deep bool) []doctor.Check { return a.Doctor.Run(deep) }

// ExistingKeys lists key pairs in ~/.ssh.
func (a *App) ExistingKeys() []keys.Existing { return keys.List(a.Config()) }

// PublicKey returns an account's public key and how to add it on the host.
func (a *App) PublicKey(id string) (key string, help keys.KeyHelp, err error) {
	acc, err := a.Account(id)
	if err != nil {
		return "", help, err
	}
	key, err = keys.PublicKey(acc)
	return key, keys.AddKeyHelp(acc.Host), err
}

// AgentStatus reports, per account id, whether its key is loaded in
// ssh-agent. ok=false means no agent is running.
func (a *App) AgentStatus() (loaded map[string]bool, ok bool) {
	fps, ok := keys.AgentFingerprints()
	loaded = map[string]bool{}
	for _, acc := range a.Config().Accounts {
		acc := acc
		if fp := keys.Fingerprint(&acc); fp != "" && fps[fp] {
			loaded[acc.ID] = true
		}
	}
	return loaded, ok
}

// --------------------------------------------------------------- accounts

// KeyChoice says how an account gets its key.
type KeyChoice struct {
	New         bool   // create a new key
	Interactive bool   // let ssh-keygen ask for the passphrase on the terminal
	Passphrase  string // used when not interactive
}

// AddAccount creates an account and, if asked, its key.
func (a *App) AddAccount(acc account.Account, k KeyChoice) (account.Account, error) {
	if a.Config().Account(acc.ID) != nil {
		return acc, fmt.Errorf("an account called %s already exists", acc.ID)
	}
	if acc.Email == "" {
		return acc, fmt.Errorf("commit email is required")
	}
	if acc.Name == "" {
		acc.Name = a.SuggestedName()
	}
	if err := a.Store.Save(acc); err != nil {
		return acc, err
	}
	if k.New {
		p, err := keys.Generate(a.FS, &acc, k.Interactive, k.Passphrase)
		if err != nil {
			return acc, err
		}
		acc.Key = p
		if err := a.Store.Save(acc); err != nil {
			return acc, err
		}
	}
	return acc, nil
}

// UpdateAccount saves changed fields of an existing account.
func (a *App) UpdateAccount(acc account.Account) error {
	old, err := a.Account(acc.ID)
	if err != nil {
		return err
	}
	acc.ID = old.ID
	if acc.User == "" {
		acc.User = old.User
	}
	return a.Store.Save(acc)
}

// RemoveAccount removes an account and its rules; with deleteKeys also its
// key files. It returns the deleted files.
func (a *App) RemoveAccount(id string, deleteKeys bool) ([]string, error) {
	acc, err := a.Account(id)
	if err != nil {
		return nil, err
	}
	copyOf := *acc
	if err := a.Store.Remove(copyOf.ID); err != nil {
		return nil, err
	}
	if deleteKeys {
		return a.Store.DeleteKeyFiles(&copyOf), nil
	}
	return nil, nil
}

// RenameAccount renames an account everywhere and updates repos in the
// project folders. It returns how many repos were updated.
func (a *App) RenameAccount(oldID, newID string) (int, error) {
	acc, err := a.Account(oldID)
	if err != nil {
		return 0, err
	}
	oldID = acc.ID
	if err := a.Store.Rename(oldID, newID); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range a.Repos() {
		if strings.EqualFold(r.Saved, oldID) && a.Git.RepoSet(r.GitDir, "gitswitch.account", newID) == nil {
			n++
		}
	}
	return n, nil
}

func (a *App) SetDefault(id string) error { return a.Store.SetDefault(id) }

// GenerateKey creates a new key for an existing account.
func (a *App) GenerateKey(id string, interactive bool, passphrase string) (string, error) {
	acc, err := a.Account(id)
	if err != nil {
		return "", err
	}
	p, err := keys.Generate(a.FS, acc, interactive, passphrase)
	if err != nil {
		return "", err
	}
	acc.Key = p
	return p, a.Store.Save(*acc)
}

// UseKey points an account at an existing key file.
func (a *App) UseKey(id, path string) error {
	acc, err := a.Account(id)
	if err != nil {
		return err
	}
	acc.Key = path
	return a.Store.Save(*acc)
}

// TestAccount logs in with the account's key and remembers the username.
func (a *App) TestAccount(id string, batch bool) (keys.TestResult, error) {
	acc, err := a.Account(id)
	if err != nil {
		return keys.TestResult{}, err
	}
	r := keys.Test(acc, sys.FindSSH(a.Config().SSHPath), batch)
	if r.OK && r.User != acc.User {
		_ = a.Store.SetUser(acc.ID, r.User)
	}
	return r, nil
}

// AddToAgent loads an account's key into the OS agent / Keychain.
func (a *App) AddToAgent(id string) error {
	acc, err := a.Account(id)
	if err != nil {
		return err
	}
	return keys.AddToAgent(acc)
}

// ---------------------------------------------------------- rules, settings

func (a *App) AddRule(r account.Rule) error    { return a.Store.AddRule(r) }
func (a *App) RemoveRule(r account.Rule) error { return a.Store.RemoveRule(r) }

// RemoveRuleAt removes the rule at a 0-based position in Config().Rules.
func (a *App) RemoveRuleAt(i int) error {
	rules := a.Config().Rules
	if i < 0 || i >= len(rules) {
		return fmt.Errorf("no such rule")
	}
	return a.Store.RemoveRule(rules[i])
}

func (a *App) SetSetting(key, value string) error { return a.Store.SetSetting(key, value) }
func (a *App) AddScanFolder(p string) error       { return a.Store.AddScanFolder(p) }
func (a *App) RemoveScanFolder(p string) error    { return a.Store.RemoveScanFolder(p) }

// ------------------------------------------------------------------ repos

// SetRepoAccount saves an account for a repo ("" makes it ask again). It
// returns a removed repo-level core.sshCommand, if there was one.
func (a *App) SetRepoAccount(gitDir, id string) (string, error) {
	if id == "" {
		return "", a.Store.ForgetRepo(gitDir)
	}
	acc, err := a.Account(id)
	if err != nil {
		return "", err
	}
	return a.Store.SetRepoAccount(gitDir, acc)
}

// UseOnce applies an account's name and email without saving the choice.
func (a *App) UseOnce(gitDir, id string) error {
	acc, err := a.Account(id)
	if err != nil {
		return err
	}
	a.Store.ApplyIdentity(gitDir, acc)
	return nil
}

// FixAuthor fixes not-pushed wrong-account commits on the current branch.
func (a *App) FixAuthor(gitDir string) (int, error) {
	return history.FixAuthor(a.Git, a.FS, a.Config(), gitDir)
}

// ---------------------------------------------------------- install, health

// Install connects gitswitch to git. report receives progress messages.
func (a *App) Install(report func(string)) error {
	a.Installer.Report = report
	return a.Installer.Install()
}

// Uninstall disconnects gitswitch from git.
func (a *App) Uninstall(purge bool, report func(string)) error {
	a.Installer.Report = report
	return a.Installer.Uninstall(purge)
}

// Fix repairs one failed health check.
func (a *App) Fix(checkID string) error { return a.Doctor.Fix(checkID) }
