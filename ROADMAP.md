# Roadmap

Planned work. Want to build one of these? See
[CONTRIBUTING.md](CONTRIBUTING.md#planned-work): open an issue first so two
people don't build the same thing.

Status: **done**, **partly** (some of it exists), **planned** (not started).

## Repository management

Find, clone, register and open repos from every connected account, in the
desktop app, the terminal app and the CLI alike.

### Registry and local repos

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| 22 | **Shared repository registry**: GUI, TUI and CLI show the same list | done | `gitswitch.repo` in global git config, like every other setting. |
| 6 | **Add existing repository**: register an already-cloned repo without cloning again | done | `gitswitch repo add [path]`; desktop app uses the native folder picker. |
| 10 | **Remove repository** from GitSwitch without deleting files | done | `gitswitch repo remove`; a repo found by a folder scan is hidden instead. |
| 24 | **Safe repository operations**: registering or removing never touches remotes, branches, files or identity | done | Only `gitswitch.*` keys in global git config change. |
| 8 | **Repository discovery**: scan a chosen folder for repos | done | Project folders (`gitswitch.scanFolder`), 5 levels deep. |
| 15 | **Git identity association** and mismatch warnings | done | `gitswitch use`, rules, and the wrong-email flag. |
| 7 | **Local repository manager**: paths, branches, remotes, identities | partly | Branch and remotes added; grouping and sorting still to come. |
| 16 | **Repository details**: URL, remotes, branch, git root, account | done | `gitswitch repo show [--json]`. |
| 9 | **Open repository** in the file manager or editor | done | `gitswitch repo open [--editor]`; editor from `gitswitch set editor`, `$VISUAL` or `$EDITOR`. |
| 11 | **Favorites and recent repositories** | partly | Pinning is done (`gitswitch repo pin`). Recently opened is planned. |
| 12 | **Repository grouping** by account, organization, parent folder or custom group | planned | |
| 14 | **Multiple local clones** of the same remote | partly | Each clone is its own entry today; showing them together is planned. |

### Remote repositories (GitHub)

These need a `hosting` package that talks to the GitHub API as the right
account (see [ARCHITECTURE.md](ARCHITECTURE.md#future-scope-a-full-git-client)).

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| 1 | **Multi-account GitHub integration**: repos from every account and organization, private ones included | planned | Needs a per-account API token, stored in the OS keychain, never in git config. |
| 2 | **Remote repository browser**: name, owner, description, visibility, language, default branch | planned | |
| 3 | **Search and filters** by account, organization, visibility, language, clone status | planned | |
| 4 | **Clone repository** over SSH or HTTPS, with folder choice and progress | partly | Cloning, folder choice and progress are done (see #5); picking the repo from a list needs the browser (#2). |
| 5 | **Clone via URL** | done | `gitswitch repo clone <url> [folder] [--account id]`, "Clone from URL…" in the desktop app, `c` in the terminal app. The account is saved in the new repo before the first fetch (`git clone -c gitswitch.account=<id>`). HTTPS uses git's own credentials for now (see #23). |
| 13 | **Clone status detection**: which remote repos are already cloned, and where | planned | Match registry remotes against API results. |
| 17 | **Refresh and sync metadata** | planned | Cache API results; never block the UI on the network. |
| 23 | **Secure authentication**: account-specific credentials, SSH and HTTPS, nothing exposed | planned | Tokens in the OS keychain; HTTPS through a git credential helper per account. |
| 18 | **Error handling**: auth failures, inaccessible repos, invalid folders, network issues, clone conflicts | partly | Clone explains bad URLs, non-empty folders, wrong-host accounts, access and network failures. API errors come with the browser. |

### Front ends

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| 19 | **GUI**: browsing, native folder picker, contextual actions, clone progress | partly | Folder picker, clone with progress, add, remove, pin and open are done. Browsing needs #2. |
| 20 | **TUI**: keyboard-driven browsing, cloning, adding, opening, removing | partly | Clone, add, pin, remove and open are done (Repos page: c, a, p, x, o). Browsing needs #2. |
| 21 | **CLI**: list, clone, add, open, inspect, pin, remove, with JSON output | done | `gitswitch repo list\|clone\|add\|remove\|show\|open\|pin\|unpin`, `--json`. |

## A full git client

Commit graph, staging, diffs, branches, merges, pull requests. See
[ARCHITECTURE.md](ARCHITECTURE.md#future-scope-a-full-git-client) for where
each piece goes.
