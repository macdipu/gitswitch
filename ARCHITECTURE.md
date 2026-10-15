# Architecture

GitSwitch is organised **by feature**, and each feature is split into
**layers** (Clean Architecture). The three front ends call the same use
cases. The use cases depend only on interfaces (ports) declared in the
domain, and infrastructure implements those interfaces:

```text
CLI ─────┐
TUI ─────┼──> Application Use Cases ──> Domain Interfaces
Desktop ─┘                                  ▲
                                            │
                              Infrastructure Implementations
```

`internal/bootstrap` is the only package that knows every implementation:
it builds them once and hands them to the use cases. `test/architecture`
fails the build when an import breaks the rules below.

## Layout

```text
cmd/gitswitch/                main of the program git runs: calls interfaces/cli (hook, CLI, terminal app)
cmd/gitswitch-app/            main of the desktop app: calls interfaces/desktop/window
internal/
  bootstrap/                  builds and wires everything; App (use cases for the front ends), Version, the ssh hook entry
  features/
    account/
      domain/                 Account, Rule, Config; validation; ports Store, RepoAccounts; KeyChoice
      application/            add, edit, rename, remove accounts; keys of an account; rules; settings; a repo's account
      infrastructure/         Store: accounts, rules and settings in git config; identity files; key paths
    identity/
      domain/                 resolve: which account a repo uses (saved, ask every time, rules, the optional default; pure, table-tested)
    git/
      domain/                 remote URLs: host and path, port-443 hosts, which URLs may be cloned, web links and pull request links; Explain (git and ssh errors in plain words)
      infrastructure/         Runner: the only code that runs git (dry-run, hidden windows, errors)
    repository/
      domain/                 Info, Remote; grouping the list (Group, by account, owner, folder or your groups) and clones of the same origin; ports Reader, Locator, Cloner, ListStore
      application/            the repo list: show, add, clone, remove, pin, open; the recently opened repos
      infrastructure/         Reader (find repos on disk, read them with git), Cloner
    history/
      domain/                 commits, graph lanes, the wrong-account rule, filters and dates, line rendering, commit details (numstat), AccountCheck; port Log
      application/            commit graph with accounts and filters (paged: History.More); commit details; fix authors; account check before a push; revert, drop, roll back, cherry-pick; web links
      infrastructure/         Log: git log, rev-list, revert, rebase, reset, cherry-pick (aborted on failure)
    refs/
      domain/                 Head (branch, HEAD, upstream, ahead/behind, uncommitted files, parked count), Branch, Switched, Clash, Parked, SwitchSetting, Pushed, Compared, Merged; port Refs
      application/            show where a repo is, list branches, switch, smart switch, park and resolve; new branch; move not-pushed commits to a new branch; delete merged branches; compare with the upstream; push; restore, show and drop parked changes; the switch-changes and branch-prefix settings; update (pull --ff-only)
      infrastructure/         Git: symbolic-ref, for-each-ref, status --porcelain=v2, switch, push, stash (parked changes are stash entries named "gitswitch park: <branch>"), pull; check-ref-format; merge-base and update-ref (moving or deleting a branch that is not checked out); for-each-ref --merged; gitswitch.switchChanges (global and per repo) and gitswitch.branchPrefix; an open clash in GITSWITCH_SWITCH
    workspace/
      domain/                 Committed, commit message and file count; port Git
      application/            commit the staged files or every change, as the repo's account
      infrastructure/         Git: add --all, commit (Head through the refs infrastructure)
    undo/
      domain/                 Record of the last change, when it may be undone (Check); ports Store, Git, Recorder
      application/            Recorder for the history, refs and workspace use cases (Before, Done, Pruned); Last, Undo
      infrastructure/         the record in refs/gitswitch/undo and [gitswitch "undo"]; reset --keep/--mixed, switch, update-ref (to undo a move or bring back deleted branches)
    ssh/
      domain/                 ssh command line from git, key help per host, login greeting; ports KeyTool, HookRepos, Transport, Prompter
      application/            Hook (git runs it on every clone/pull/push/fetch); Keys (show, list, test, agent)
      infrastructure/         Keys (ssh-keygen, or an ed25519 pair made in Go when there is no terminal; ssh-add, ssh -T; known hosts via ssh -G); hook adapters (current repo, probe file, real ssh)
    setup/                    install and uninstall (single package, see below)
    doctor/                   health checks and repairs (single package, see below)
  interfaces/
    cli/                      commands, flags, completions, setup wizard
    tui/                      the terminal app
    desktop/
      api/                    the page and its JSON API (no cgo; tested everywhere)
      assets/                 index.html, built into the program
      window/                 the desktop app: Main, and the Wails window (`desktop` build tag) or a stub
    prompt/                   "which account?" on the terminal or a native popup (implements ssh.Prompter)
  platform/
    filesystem/               home/config/cache folders, path comparison, dry-run file changes, lock files
    process/                  start ssh, browser, file manager, editor, clipboard; hidden console windows
    terminal/                 open the terminal (only in the foreground), raw keys, window size (per-OS build tags)
```

Packages are named after their layer (`domain`, `application`,
`infrastructure`) and always imported with the same alias, so code reads
naturally: `account.Account`, `accountapp.Service`, `accountinfra.Store`,
`remote.Parse`, `gitinfra.Runner`, `resolve.Decide`, `repo.Info`,
`repoapp`, `repoinfra`, `history.Commit`, `historyapp`, `historyinfra`,
`refs.Head`, `refsapp`, `refsinfra`, `ssh.Invocation`, `sshapp`, `sshinfra`,
`undo.Record`, `undoapp`, `undoinfra`.

## Layers and what each may import

| Role | Where | May import |
| --- | --- | --- |
| Domain | `features/*/domain` | other domain packages only. Pure: no `os`, `os/exec`, `runtime`, `syscall`, network. OS behaviour a rule needs is passed in (e.g. `resolve.Input.RealPath`). |
| Application | `features/*/application` | domain, platform. Never infrastructure: use cases see only ports. No `os/exec` or `syscall`. |
| Infrastructure | `features/*/infrastructure` | domain, platform, other infrastructure (everything uses `gitinfra.Runner`). |
| Feature module | `features/setup`, `features/doctor` | domain, infrastructure, platform. |
| Interfaces | `interfaces/*` | application (through `bootstrap.App`), domain types, feature modules, platform. Never infrastructure. |
| Bootstrap | `bootstrap` | everything except `cmd`. |
| Platform | `platform/*` | other platform packages only. |
| Command | `cmd/gitswitch`, `cmd/gitswitch-app` | `interfaces/cli` only, and `interfaces/desktop/window` only. |

Domain packages may use other features' domain types (a repo knows its
account), but never in a circle; `test/architecture` checks that too.

**Why setup and doctor are single packages.** Their job *is* the machine:
install copies the program, edits PATH, shell profiles and the app menu;
doctor inspects git config, environment variables, `~/.ssh/config` and
running services, and repairs them. Splitting them into ports and adapters
would only wrap each system call in an interface. They are still reached by
the front ends through `bootstrap.App`, and doctor sees repos and keys
through the `repo.Reader` and `ssh.KeyTool` ports.

## How a request flows

`gitswitch repo clone <url> --account work`:

1. `interfaces/cli` parses the arguments and calls
   `app.Repos.Clone(url, dir, "work", progress)`.
2. `repoapp.Service.Clone` checks the URL with `remote.CheckCloneURL`, looks
   the account up in the `ListStore`, checks the folder, then asks the
   `Cloner` port to clone.
3. `repoinfra.Cloner` runs `git clone -c gitswitch.account=work ...`
   through `gitinfra.Runner`. Git calls `gitswitch ssh`, which runs
   `bootstrap.RunSSHHook` → `sshapp.Hook`.
4. The use case saves the account through `account.RepoAccounts` and adds
   the repo to the list; the CLI prints the result.

The TUI and the desktop API call the same `app.Repos.Clone`.

## Two programs: gitswitch and gitswitch-app

Git runs `gitswitch` on every clone, pull, push and fetch, so it must stay
small, static and fast. The desktop app will grow into a full git client.
They are therefore two programs built from this one module and installed
together:

- `gitswitch` (`cmd/gitswitch`): the ssh hook, the CLI, the terminal app,
  setup and doctor. Always built with `CGO_ENABLED=0` and no tags.
- `gitswitch-app` (`cmd/gitswitch-app`): the desktop window. `gitswitch ui`
  and the app menu entries start it (`setup.OpenApp`).

The installers download both into one folder and run `gitswitch install`,
which copies the hook program, then the app next to it, to their fixed
places (`setup.InstalledPath`, `setup.AppPath`). `test/architecture`
checks that nothing `cmd/gitswitch` imports reaches the desktop packages or
Wails, and that the ssh hook use case does not reach the git client
features or any front end.

## Desktop window

`interfaces/desktop/window` shows one HTML page
(`interfaces/desktop/assets/index.html`) in the system web view through
[Wails](https://wails.io): WebView2 on Windows, WebKit on macOS,
WebKitGTK 4.1 on Linux. The page calls a small JSON API
(`interfaces/desktop/api`) with `fetch`. Wails passes those requests
straight to the Go handler inside the process, so there is no network port,
token or background server. Two consequences:

- The Wails code is behind the `desktop` build tag (`window/window.go`). A
  plain `go build` leaves it out (`window/nowindow.go`) and stays static and
  cross-compilable; release builds add `-tags desktop,production` (plus
  `webkit2_41` on Linux). The `api` package builds either way, so its tests
  need no cgo.
- On Linux the window links WebKitGTK. `install.sh` skips the app when
  WebKitGTK is missing or the app does not start (`gitswitch-app --version`);
  `gitswitch` itself never links it.

## Rules

1. **Imports follow the table above.** `test/architecture` enforces it, for
   the files of every OS and build tag. **The hook stays small:** nothing
   `cmd/gitswitch` imports may reach `interfaces/desktop` or Wails, and
   `ssh/application` may not reach history, refs, repository, setup, doctor
   or any front end. New git client features go in their own feature
   packages and are reached only from the front ends.
2. **Front ends hold no logic.** They call use cases on `bootstrap.App` and
   display the results. If a screen needs something new, add a use case.
3. **Use cases depend on ports.** A use case that needs the outside world
   declares (or reuses) an interface in its feature's `domain` package;
   infrastructure implements it; `bootstrap` connects them. Add an interface
   only when a use case needs one.
4. **Only `gitinfra.Runner` runs git**, and only `platform` deals with OS
   differences (`runtime.GOOS`, build tags).
5. **Core packages never print.** They return values and errors; long
   operations take a `report` or `progress` callback. Enforced by
   `test/architecture`. The ssh hook must never write to stdout, because git
   reads its protocol from it; its notices go to `Hook.Notice` (stderr).
6. **No global state.** Settings like dry-run travel in `gitinfra.Runner`
   and `filesystem.FS`, created once by `bootstrap.New`. The ssh hook builds
   only what it needs (`bootstrap.RunSSHHook`), so it stays fast.
7. **Keep decisions pure.** Logic that can be a function of its inputs (like
   `resolve.Decide` or `history.LayoutLanes`) lives in a domain package and
   gets table tests.

## Tests

| Where | What |
| --- | --- |
| `internal/**/..._test.go` | Unit tests next to the code (URL parsing, account decisions, graph layout, ssh arguments, key input, the desktop API). |
| `test/integration` | Builds the real program and runs real `git clone/pull/push` through it against local repos via a fake ssh. |
| `test/architecture` | Enforces the import rules above, checks domain features have no cycles, that core code never prints, and that the `gitswitch` program and the ssh hook stay free of desktop and git-client code. |
| `test/screenshots` | Not a test: `run.sh` regenerates the README screenshots from demo data (see CONTRIBUTING.md). |

Run everything with `go test ./...`. CI runs it on Linux, macOS and Windows.

## Future scope: a full git client

GitSwitch can grow into a complete desktop git client (commit graph,
staging, diffs, merges, pull requests) by adding features. The account core
does not change.

| Feature | Where it goes |
| --- | --- |
| Staging (files, hunks, lines) | The `workspace` feature (commit exists): porcelain parsers (`git status --porcelain=v2`, `git diff`) in its infrastructure behind a domain port, use cases in its application. |
| Diff viewer, blame | A `diff` feature over git parsers. Render only the visible rows. |
| Branches, checkout, stash, tags | The `refs` feature (branches, switch and update exist; stash and tags go there too). |
| Merge, rebase, conflict editor | A `merge` feature (3-way conflict model in its domain). New changes report to the `undo` feature's Recorder, as history and refs do. |
| Pull requests and issues | A `hosting` feature (GitHub/GitLab APIs in its infrastructure) that uses the account domain so each repo talks to its host as the right account. |
| File watching (live refresh) | File events in `platform` + a `watch` feature that tells the front ends what changed. |

Performance rule for the client: never load or render a whole history or
diff. Stream from `git`, page in the use cases, and render only what is on
screen.
