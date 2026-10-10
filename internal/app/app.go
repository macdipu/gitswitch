// Package app is the single API used by every front end (command line,
// terminal app, browser app, and later a desktop window). Front ends hold
// no logic of their own: they call App methods and display the results.
//
// App methods return values and errors; they never print. Progress
// messages from long operations go to the report callback the caller gives.
package app

import (
	"fmt"
	"io"
	"os"
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

// Repos scans the configured project folders, plus extra folders.
func (a *App) Repos(extra ...string) []repo.Info {
	c := a.Config()
	return repo.Scan(a.Git, c, append(append([]string{}, c.ScanFolders...), extra...))
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

// PublicKey returns an account's public key and the host page to add it.
func (a *App) PublicKey(id string) (key, page string, err error) {
	acc, err := a.Account(id)
	if err != nil {
		return "", "", err
	}
	key, err = keys.PublicKey(acc)
	return key, keys.AddKeyPage(acc.Host), err
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
