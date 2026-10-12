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

These need a `hosting` feature (`internal/features/hosting`) that talks to the GitHub API as the right
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

## History screen

One screen for someone on their first week of git and for someone who lives
in it. The same actions in the desktop app, the terminal app and the CLI.

### Design rules

- **The commits are the screen.** One toolbar line (repo, branch, sync,
  update), one search line, then the graph. Filters, branch lists and
  actions open where you need them instead of sitting in rows above the
  graph.
- **Safe first, in plain words.** Every action says what it does to your
  work ("adds a new commit that undoes this one; safe after pushing").
  Actions that rewrite history sit apart, need a confirmation, and are
  disabled with the reason when the commit is already pushed.
- **Fast for daily use.** Keyboard first (`/` search, `j`/`k` move, `Enter`
  details, `Esc` clear), search tokens (`author:ana since:7d account:work`),
  multi-select with Ctrl/⌘-click, and the same commands in the CLI.
- **Never surprise.** Nothing changes a repo without a click and a
  confirmation; a failed operation is aborted so nothing is half done;
  `--dry-run` everywhere in the CLI.

### Features

| # | Feature | Status | How |
| --- | --- | --- | --- |
| H1 | **Current branch and HEAD**, ahead/behind the upstream | done | `refs` feature: `Head` (branch, HEAD, upstream, ahead, behind, operation in progress). |
| H2 | **Switch branch**, including a remote-only branch | done | `refs.Switch`; `origin/x` creates a local `x` that tracks it. Desktop: searchable branch menu. TUI `b`, CLI `gitswitch switch`. |
| H3 | **Update** the branch (pull, fast-forward only) with the repo's account | done | `refs.Update`; explains a diverged branch instead of merging. |
| H4 | **Revert** a commit (pushed or not) | done | `history.Revert`; a merge is reverted against its first parent. |
| H5 | **Drop** a not-pushed commit | done | `history.Drop`: `rebase --onto`, aborted on conflict. Refuses pushed, merge and first commits. |
| H6 | **Roll back (reset)** to a commit, keeping the changes | done | `history.Rollback`: `reset --mixed`; every undone commit must be not pushed. |
| H7 | **Cherry-pick** one or several commits | done | `history.CherryPick`: parents before children, aborted on conflict. |
| H8 | **View on the web** (commit or repo) | done | `remote.CommitURL`: GitHub, GitLab, Bitbucket and GitHub-style hosts. |
| H9 | **Filters**: message, author, account, dates; wrong-account and not-pushed only | done | `history.Filter`. Text, author and dates are sent to git, so they search the whole history; dates are parsed by gitswitch (git guesses silently). |
| H10 | **Redesigned desktop screen**: toolbar, search with tokens and date presets, filter chips, details panel, selection bar, keyboard | done | Desktop page only; uses the API above. |
| H11 | **Commit details**: full message, changed files and line counts | done | `history.Log.Show` (`git log -1 --numstat`, merges against their first parent). Desktop details panel, Enter in the terminal app, `gitswitch show <commit> [--json]`. |
| H12 | **Undo the last action** (drop, roll back, cherry-pick, revert, switch) | planned | An `undo` feature over the reflog: remember HEAD before each action, offer "Undo" in the toast, `git reset --keep` back to it. |
| H13 | **Load more**: page through history beyond the first page of commits | done | `History.More` says git had more; the desktop app's "Load more" button, `m` in the terminal app and `gitswitch log -n` read a bigger window, so the graph stays connected. |
| H14 | **Uncommitted changes when switching**: offer to stash and restore | planned | A `stash` use case in `refs`; ask before stashing, restore after switching. |
| H15 | **Update with rebase** for people who prefer it | planned | A setting (`gitswitch.pull = ff-only|rebase`), still never a merge commit by surprise. |
| H16 | **Compare a branch with its upstream**: list what you would push or pull | planned | `rev-list HEAD...@{upstream}` in `refs`, shown from the sync status. |
