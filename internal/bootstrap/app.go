// Package bootstrap builds the application: it creates the infrastructure
// (git runner, git-config store, ssh tools, file system) once and hands it
// to the use cases. It is the only package that knows every
// implementation; front ends get the use cases through App.
package bootstrap

import (
	"io"
	"os"

	accountapp "github.com/macdipu/gitswitch/internal/features/account/application"
	accountinfra "github.com/macdipu/gitswitch/internal/features/account/infrastructure"
	"github.com/macdipu/gitswitch/internal/features/doctor"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	historyapp "github.com/macdipu/gitswitch/internal/features/history/application"
	historyinfra "github.com/macdipu/gitswitch/internal/features/history/infrastructure"
	hostingapp "github.com/macdipu/gitswitch/internal/features/hosting/application"
	hostinginfra "github.com/macdipu/gitswitch/internal/features/hosting/infrastructure"
	refsapp "github.com/macdipu/gitswitch/internal/features/refs/application"
	refsinfra "github.com/macdipu/gitswitch/internal/features/refs/infrastructure"
	repoapp "github.com/macdipu/gitswitch/internal/features/repository/application"
	repoinfra "github.com/macdipu/gitswitch/internal/features/repository/infrastructure"
	"github.com/macdipu/gitswitch/internal/features/setup"
	sshapp "github.com/macdipu/gitswitch/internal/features/ssh/application"
	sshinfra "github.com/macdipu/gitswitch/internal/features/ssh/infrastructure"
	undoapp "github.com/macdipu/gitswitch/internal/features/undo/application"
	undoinfra "github.com/macdipu/gitswitch/internal/features/undo/infrastructure"
	workspaceapp "github.com/macdipu/gitswitch/internal/features/workspace/application"
	workspaceinfra "github.com/macdipu/gitswitch/internal/features/workspace/infrastructure"
	"github.com/macdipu/gitswitch/internal/interfaces/prompt"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Version is set at build time with
// -ldflags "-X github.com/macdipu/gitswitch/internal/bootstrap.Version=v1.2.0".
var Version = "dev"

// App holds the use cases every front end (command line, terminal app and
// desktop window) calls. They return values and errors and never print;
// long operations take a report or progress callback.
type App struct {
	Accounts  *accountapp.Service
	Keys      *sshapp.Keys
	Repos     *repoapp.Service
	History   *historyapp.Service
	Refs      *refsapp.Service
	Undo      *undoapp.Service
	Workspace *workspaceapp.Service
	Hosting   *hostingapp.Service
	Setup     *setup.Installer
	Doctor    *doctor.Doctor

	dryRun bool
}

// DryRun reports whether changes are only being printed.
func (a *App) DryRun() bool { return a.dryRun }

// New builds the App. With dryRun, changes are printed to out instead of
// made (out defaults to standard output).
func New(dryRun bool, out io.Writer) *App {
	if out == nil {
		out = os.Stdout
	}
	git := &gitinfra.Runner{DryRun: dryRun, Out: out}
	fs := &filesystem.FS{DryRun: dryRun, Out: out}
	store := &accountinfra.Store{Git: git, FS: fs}
	reader := &repoinfra.Reader{Git: git}
	keys := &sshinfra.Keys{FS: fs}
	installer := &setup.Installer{Git: git, FS: fs, Store: store, Version: Version}
	undoGit := &undoinfra.Git{Git: git}
	undos := &undoapp.Service{Store: undoGit, Git: undoGit, FS: fs}
	secrets := hostinginfra.NewSecrets()
	return &App{
		Accounts: &accountapp.Service{Store: store, RepoAccounts: store, Keys: keys, Repos: reader, Tokens: hostinginfra.Tokens{Secrets: secrets}},
		Keys:     &sshapp.Keys{Accounts: store, Tool: keys},
		Repos: &repoapp.Service{Lists: store, Reader: reader, Locator: git, Cloner: &repoinfra.Cloner{Git: git},
			RepoAccounts: store, DryRun: dryRun},
		History:   &historyapp.Service{Accounts: store, Repos: reader, Locator: git, Log: &historyinfra.Log{Git: git}, FS: fs, Undo: undos},
		Refs:      &refsapp.Service{Refs: &refsinfra.Git{Git: git}, Accounts: store, Repos: reader, Locator: git, FS: fs, Undo: undos},
		Undo:      undos,
		Workspace: &workspaceapp.Service{Git: &workspaceinfra.Git{Git: git}, Accounts: store, Repos: reader, Locator: git, FS: fs, Undo: undos},
		Hosting: &hostingapp.Service{Accounts: store, Secrets: secrets, API: &hostinginfra.GitHub{Version: Version},
			Helper: &hostinginfra.HelperConfig{Git: git}, Repos: &hostinginfra.HelperRepos{Git: git, Reader: reader}},
		Setup:  installer,
		Doctor: &doctor.Doctor{Git: git, Store: store, Installer: installer, Repos: reader, Keys: keys},
		dryRun: dryRun,
	}
}

// RunSSHHook is `gitswitch ssh`, which git runs on every clone, pull, push
// and fetch. It builds only what the hook needs, never prints to standard
// output (git reads its protocol from it) and returns ssh's exit code.
func RunSSHHook(args []string) int {
	git := &gitinfra.Runner{}
	store := &accountinfra.Store{Git: git, FS: &filesystem.FS{}}
	h := &sshapp.Hook{
		Accounts:  store,
		Identity:  store,
		Repos:     &sshinfra.HookRepos{Git: git},
		Transport: sshinfra.Transport{},
		Prompter:  prompt.Prompter{},
		Notice:    os.Stderr,
	}
	return h.Run(args)
}
