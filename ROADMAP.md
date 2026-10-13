# Roadmap

Planned work. Want to build one of these? See
[CONTRIBUTING.md](CONTRIBUTING.md#planned-work): open an issue first so two
people don't build the same thing.

Status: **done**, **partly** (some of it exists), **planned** (not started),
**later**.

**Latest release (v1.4): [Everyday branch work](#everyday-branch-work-v14)**:
switch branches with uncommitted changes (Smart switch or Park), undo the
last action, start a branch and push it, always as the right account.

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
| H12 | **Undo the last action** (drop, roll back, cherry-pick, revert, fix-author, switch; park and new branch when they come) | done | The `undo` feature: history and refs use cases report each change to its Recorder; the record lives in `refs/gitswitch/undo` (so `git gc` keeps dropped commits) and `[gitswitch "undo"]`. Refused when the branch moved since, when it would remove pushed commits, or when uncommitted changes clash. "Undo" in the success message and the History toolbar, `U` in both apps, `gitswitch undo [-y] [--show [--json]]`. |
| H13 | **Load more**: page through history beyond the first page of commits | done | `History.More` says git had more; the desktop app's "Load more" button, `m` in the terminal app and `gitswitch log -n` read a bigger window, so the graph stays connected. |
| H14 | **Uncommitted changes when switching**: offer to stash and restore | done | Replaced by Smart switch, Park and Resolve (B2–B4) in [v1.4](#everyday-branch-work-v14). |
| H15 | **Update with rebase** for people who prefer it | later | A setting (`gitswitch.pull = ff-only|rebase`), still never a merge commit by surprise. |
| H16 | **Compare a branch with its upstream**: list what you would push or pull | planned | `rev-list HEAD...@{upstream}` in `refs`, shown from the sync status. |

## Everyday branch work (v1.4)

The work you do every day: leave a branch with changes, come back to it,
start a branch for work you already did, push it, and undo a mistake. IDEs
already commit, diff and merge well, so GitSwitch does not compete there.
It does what they don't: uncommitted changes that stay with their branch,
undo for every action, never a half-done state, and always the right
account.

### Design rules

- **Plain words first, git words next to them.** "Park" with "(stash)" in
  small text. A *Show git commands* setting prints the exact commands.
- **Ask once, remember if asked.** A clear question with a safe default;
  "Remember my choice", a setting and CLI flags skip it.
- **Every action can be undone or walked back.** Nothing is ever left half
  done, and work is never lost.
- **Plain git underneath.** Parked changes are ordinary stashes, visible in
  `git stash list`, IntelliJ and VS Code.
- **Same in every front end.** Desktop app, terminal app keys, CLI with
  `--json`, `-y` and `--dry-run`.

### Switching with uncommitted changes

With no changes, switching just switches. With changes, GitSwitch asks:

```
You have uncommitted changes on feature-login (3 files, 1 new).
  ● Smart switch: take them with me to main
  ○ Park them on feature-login (restored when you come back)
  ○ Cancel
  ☐ Remember my choice for this repo
```

Smart switch is preselected for a new branch, Park for an existing one.

**Smart switch.** If git can switch keeping the changes, it just switches.
Otherwise: `stash push -u`, switch, `stash pop --index` (plain `pop` when
the staged state cannot be restored). If files clash, GitSwitch stays on
the new branch with the clashing files marked as conflicts and keeps the
stash as a safe copy until you finish:

| Choice | What it does |
| --- | --- |
| Open in editor | Opens the clashing files with a short guide to the conflict markers. |
| Resolve in IDE | The files are git conflicts, so IntelliJ, VS Code and `git mergetool` show their merge tools. |
| I've fixed them | Refuses while conflict markers remain; then makes the files normal uncommitted changes again and drops the stash. |
| Go back | Restores the files the stash touched, switches back and pops the stash: exactly as before. |
| Park instead | Go back, then park and switch clean. |

While a clash is open, the status line shows it, other actions refuse
("finish or go back first"), and it survives closing the app (kept in the
repo's `gitswitch.switch.*` config).

**Park.** `stash push -u -m "gitswitch park: <branch>"`, then switch clean.
Coming back offers Restore, Look first or Not now. A clash on restore keeps
the changes parked; nothing is half applied. Parking the same branch twice
keeps both. Parked changes are never deleted automatically, and those of a
renamed or deleted branch are still listed.

### Features

| # | Feature | Status | How |
| --- | --- | --- | --- |
| B1 | **Status line**: changed, new and staged files, to push, to pull, parked, open clash | done | `refs.Head` reads `git status --porcelain=v2` (staged, changed, new, conflicts) and counts the changes parked on the branch, shown in the desktop toolbar, the terminal app's History header, `gitswitch branch`, `log` and `status [--json]`. An open smart-switch clash shows as "smart switch in progress" and a desktop banner. |
| B2 | **Smart switch**: take uncommitted changes to the other branch | done | `refs.SmartSwitch`: a plain switch when git keeps the changes; otherwise stash, switch, `stash apply --index` (plain `apply` when the staged state does not apply), and switch back if they cannot be put back at all. Asked before switching with changes in the desktop app, the terminal app and a terminal; `gitswitch switch <branch> --smart`. Reports to Undo. |
| B3 | **Park**: leave changes on their branch, restore them on return | done | `refs.Park`: `stash push -u -m "gitswitch park: <branch>"`, then a clean switch (the changes are put back if it fails). Every switch returns the changes parked on the new branch, offered as Restore, Look first or Not now. `RestoreParked` needs a clean working folder and, on a clash, discards the half-applied files, so they stay parked. Branch menu badge, desktop "Parked changes" list, terminal app `p` in the switch question and `P`, `gitswitch switch <branch> --park`, `gitswitch parked [show\|restore\|drop] [<n>]`. Reports to Undo. |
| B4 | **Resolve a clash** after Smart switch: done, go back or park instead | done | `gitswitch resolve [--done\|--abort\|--park]`, the desktop banner, terminal app `R`. Park instead is go back, then park. The clash lives in the git dir (`GITSWITCH_SWITCH`), so every other action waits. |
| B5 | **Switch setting**: ask, smart or park, global or per repo | done | `gitswitch.switchChanges` in `~/.gitconfig` or the repo's `.git/config` (the repo's comes first), read by `refs.SwitchSetting`. `gitswitch set switch-changes ask\|smart\|park [--repo]` (`global --repo` removes the repo's), desktop Settings, terminal app Settings. Remember for this repo: the desktop checkbox, `S` / `P` in the terminal app, an answer ending in `!` in a terminal. With no terminal and no flag or setting, a switch with changes stops instead of guessing. `gitswitch status` shows the setting in effect. |
| H12 | **Undo the last action** | done | See [History screen](#history-screen). Smart switch, park and new branch will report to it too. |
| B6 | **New branch** from the current branch or any commit, bringing uncommitted changes | done | `refs.NewBranch`: `switch --create`, which keeps them without a stash; from another commit, when they touch files that differ there, it refuses and nothing changes. Works from a detached HEAD. Desktop branch menu "New branch…" and the commit's "New branch here", terminal app `n` / `N`, `gitswitch branch new <name> [--from <commit>] [--no-prefix]`, optional name prefix in `gitswitch.branchPrefix` (`gitswitch set branch-prefix feature/`, both apps' Settings). Reports to Undo. |
| B7 | **Push branch** and **open a pull request** | done | `refs.Push`: to the branch it tracks, else `push -u` to origin (or the only remote) under the same name; says which account pushed; refuses a diverged branch before pushing and explains a rejected push. `remote.PullRequestURL` builds the PR link (GitHub, GitLab, Bitbucket), not for the remote's default branch. Desktop "Push" button with "Open pull request", terminal app `p`, `gitswitch push [--open]`. Never a force push. |
| B8 | **Account check before commit and push** | done | `history.CheckAccount`: the account that pushes, whether the repo's `user.email` is that account's, and the not-pushed commits on the branch made as another account (the wrong-account rule). Before a push every front end offers "fix them first" (fix-author), "push as they are" or cancel; without a terminal `gitswitch push` needs `-y`. A wrong repo email shows in the desktop History banner ("Use <account>'s name and email") and the terminal app's History header (`i`), and `gitswitch push` and `status` say how to fix it (`gitswitch use <account> --once`). GitSwitch does not commit, so "before commit" is this warning, not a git hook. |

Order: B1, H12, B2–B5, B6, B7, B8. Each is one commit with unit and
integration tests (real git: new files, staged changes, clashes, parking
twice, undo of each), then the desktop and terminal app screens and the
README.

### Later

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| B9 | **Move not-pushed commits to a new branch** (committed on the wrong branch) | done | `refs.MoveToNewBranch`: `switch --create` at HEAD, then `update-ref` moves the old branch back to its merge base with its upstream, only while it is still where it was (otherwise the new branch is removed and nothing changes). Uncommitted changes stay: HEAD does not move. Undo (`move`) puts the commits back on the old branch, switches back and deletes the new one, refused when either branch moved since. Desktop branch menu "Move not-pushed commits to a new branch…", terminal app `M`, `gitswitch branch new <name> --move`. |
| B10 | **Delete merged branches** | later | `gitswitch branch prune [--dry-run]`. |
| B11 | **Commit** (pick files, message) | later | IDEs do this well; at most a simple "Commit all". The `workspace` feature in [ARCHITECTURE.md](ARCHITECTURE.md#future-scope-a-full-git-client). |
