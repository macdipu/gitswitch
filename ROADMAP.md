# Roadmap

Planned work. Want to build one of these? See
[CONTRIBUTING.md](CONTRIBUTING.md#planned-work): open an issue first so two
people don't build the same thing.

Status: **done**, **partly** (some of it exists), **planned** (not started),
**later**.

**Next release (v2): a full git client.** Past v1.5, GitSwitch grew from the
account switcher into a git client: the [changes panel and commit
box](#a-full-git-client-v2), merge and a conflict editor, stashes, blame,
compare, reflog and bisect, remotes and force push with lease, rebase and
reword, signing, worktrees, submodules, live refresh, a command palette,
tabs and themes. The core of every item below is built and tested against
real git; what is left is mostly screens (desktop and terminal app) for the
newest items, then release work. Hosting writes (creating pull requests,
Bitbucket, a team review report) are not planned.

**Starting a session? Read [Where to pick up next](#where-to-pick-up-next)** at the end of this file.

**Latest release (v1.5)**: a [History screen](#history-screen) like an
IDE's git log, a tidier [repo list](#repository-management) (recent repos,
groups, sorting, clones), optional GitHub sign-in with a browser of every
account's repos and HTTPS remotes that use the right account, and update
with rebase. Before it, v1.4 brought [everyday branch
work](#everyday-branch-work-v14).

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
| 7 | **Local repository manager**: paths, branches, remotes, identities | done | Paths, branch, remotes, account and email per repo; grouping (#12); sorting by folder, name, recently opened or account, pinned first (`repo.SortRepos`, `gitswitch.repoSort`): `repo list --sort`, `set repo-sort`, desktop "Sort by", terminal app `s`. |
| 16 | **Repository details**: URL, remotes, branch, git root, account | done | `gitswitch repo show [--json]`. |
| 9 | **Open repository** in the file manager or editor | done | `gitswitch repo open [--editor]`; editor from `gitswitch set editor`, `$VISUAL` or `$EDITOR`. |
| 11 | **Favorites and recent repositories** | done | Pinning (`gitswitch repo pin`), and the last 10 repos opened (`gitswitch.recent`, newest last): `repo open` and showing a repo's history record one. `gitswitch repo recent [--json]`, the top of the desktop History repo menu, and the terminal app's `o` list. |
| 12 | **Repository grouping** by account, organization, parent folder or custom group | done | `repo.GroupRepos`: by account, owner (host and user or organization from the origin URL, GitLab subgroups included), folder, or a group you name (`gitswitch.group = <name>=<path>`). The way to group is saved in `gitswitch.repoGroups`, shared by every front end. `gitswitch repo list [--by ...] [--json]`, `repo group <name> [path]`, `repo ungroup [path]`, `gitswitch set repo-groups ...`; desktop Repos page "Group by" and "Group…"; terminal app `g` (group by) and `G` (group). |
| 14 | **Multiple local clones** of the same remote | done | Each clone is its own entry, and says where the others are ("also cloned at ..."): `repo.MarkClones` matches origins however they are written (ssh or https, port-443 hosts, `.git`, case). `clones` in `repo list --json`, "Clones" in `repo show`, the desktop and terminal Repos pages. Grouping by owner shows them together. |

### Remote repositories (GitHub)

These need a `hosting` feature (`internal/features/hosting`) that talks to the GitHub API as the right
account (see [ARCHITECTURE.md](ARCHITECTURE.md#future-scope-a-full-git-client)).

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| 1 | **Multi-account GitHub integration**: repos from every account and organization, private ones included | done | The `hosting` feature keeps one token per account in the OS keychain, checked with `GET /user` and refused when it belongs to another user than the account's key. `gitswitch signin <id>\|--check`, `signout`; desktop account menu; terminal app `g` / `G`. The browser (#2) lists every repo each signed-in account sees (`GET /user/repos`: owned, collaborator and organization repos, private included). |
| 2 | **Remote repository browser**: name, owner, description, visibility, language, default branch | done | `hostingapp.Browse`: every signed-in account's repos, merged (a repo two accounts see is listed once). `gitswitch github list [--json]`, the GitHub repos on the desktop Accounts page, terminal app `b` on the Repos page. Owner, description, visibility, language, fork and archived; the default branch is in `--json`. |
| 3 | **Search and filters** by account, organization, visibility, language, clone status | done | `hosting.Filter`: words in the name or description, account, owner, visibility, language, cloned or not; archived repos hidden unless asked. CLI flags, desktop search box and menus, typing in the terminal app's list. |
| 4 | **Clone repository** over SSH or HTTPS, with folder choice and progress | done | From the list: `gitswitch github clone <owner/name> [folder]`, "Clone…" in the desktop Accounts page's GitHub repos (opens the clone form, prefilled), Enter in the terminal app. Over SSH as the account that sees the repo; HTTPS URLs work with `repo clone` (and the token, #23). |
| 5 | **Clone via URL** | done | `gitswitch repo clone <url> [folder] [--account id]`, "Clone from URL…" in the desktop app, `c` in the terminal app. The account is saved in the new repo before the first fetch (`git clone -c gitswitch.account=<id>`). HTTPS uses git's own credentials for now (see #23). |
| 13 | **Clone status detection**: which remote repos are already cloned, and where | done | `hosting.MarkCloned` matches the repo list's origins against the API results however the URL is written; each remote repo says where it is cloned, and the apps offer History instead of Clone. |
| 17 | **Refresh and sync metadata** | done | Each account's list is cached in the cache folder (`remote-repos/<account>.json`, no tokens). Lists over an hour old are read again: in the background on the desktop page, before listing in the CLI (`--refresh` reads now). A failed read keeps the last list and says why. Signing out deletes the account's cache. |
| 23 | **Secure authentication**: account-specific credentials, SSH and HTTPS, nothing exposed | done | Tokens are in the OS keychain (never in git config, files or command lines). For HTTPS, `gitswitch credential` is a git credential helper in its own `credential.https://<host>/` section, written while an account on the host is signed in: ahead of your helpers, answering only for repos whose account is signed in, so everything else reaches your helpers unchanged. Removed on the last sign-out and on uninstall; `uninstall --purge` also deletes the tokens. |
| 18 | **Error handling**: auth failures, inaccessible repos, invalid folders, network issues, clone conflicts | done | Clone explains bad URLs, non-empty folders, wrong-host accounts, access and network failures. The API explains refused tokens (401), organization approval or rate limits (403), unreachable hosts and unreadable answers, per account, keeping the last list. |
| 25 | **GitLab and Codeberg accounts**: sign in with a token, browse and clone repos, Reviews (merge or pull requests), the branch's request, HTTPS token for git | done | `hosting.KindOf` guesses GitHub or GitLab from the host name (gitlab.com, or a host starting with `gitlab.`); no setting, tokens only. `hostinginfra.GitLab` (REST `/api/v4`: `/user`, `/personal_access_tokens/self`, `/projects`, `/merge_requests` by reviewer, author and approver, one project's merge requests with approvals and pipeline), reached through `Hosts`. Chips come from GitLab's `detailed_merge_status` (`ReadyToMerge`), so "approved" is less exact than on GitHub. `hostinginfra.Gitea` (Codeberg, Gitea, Forgejo: `/api/v1` `/user`, `/user/repos`, `/repos/issues/search?type=pulls`, a pull request's reviews and commit status; your ten newest pull requests get reviews and checks). Not for Bitbucket yet. |

### Front ends

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| 19 | **GUI**: browsing, native folder picker, contextual actions, clone progress | done | Folder picker, clone with progress, add, remove, pin and open, and the GitHub repos under the accounts (search, filters, clone, history of a cloned repo). |
| 20 | **TUI**: keyboard-driven browsing, cloning, adding, opening, removing | done | Clone, add, pin, remove and open (Repos page: c, a, p, x, o), and `b` to browse GitHub (type to filter; Enter clones a repo or opens a cloned one). |
| 21 | **CLI**: list, clone, add, open, inspect, pin, remove, with JSON output | done | `gitswitch repo list\|clone\|add\|remove\|show\|open\|pin\|unpin\|recent\|group\|ungroup`, `gitswitch github list\|clone`, `--json`. |

## A full git client (v2)

GitSwitch began as the tool that picks the right account for every repo,
then grew a History screen and everyday branch work, leaving staging,
committing details and merging to the IDE. v2 drops that line: everything
a git GUI does (GitKraken, Fork, Tower, the IDEs' git panels), in the
desktop app first, with the CLI and terminal app alongside. The account
core does not change, and every action still runs as the repo's account.
See [ARCHITECTURE.md](ARCHITECTURE.md#future-scope-a-full-git-client) for
where each piece goes.

### Design rules

The [History](#history-screen) and [branch work](#everyday-branch-work-v14)
rules still hold: plain words first, ask once, never half done, plain git
underneath, the same in every front end. Added for v2:

- **Your work is never lost.** Discarding changes keeps them as a stash
  (`gitswitch discard: <branch>`), so `git stash list` and any IDE can
  bring them back; rewriting history keeps what it replaced for Undo.
- **Stop, don't abort, when you ask for it.** Merges and rebases that clash
  stop in a state the app explains (continue, skip, abort), instead of
  being aborted the way one-click actions are.
- **Never load everything.** Stream from git, page in the use cases,
  render only what is on screen: big diffs, big histories, big working
  folders.

### Working copy and commit

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G1 | **Changes list**: every changed, new, deleted, renamed and conflicted file, staged and not | done | `workspace.Status` (`git status --porcelain=v2 -z`): a `workspace.File` per path with its staged and working-folder state. `gitswitch changes [--json]`, `GET /api/repo/changes`; desktop: the "Uncommitted changes" row at the top of History opens the changes panel (Conflicts, Staged, Not staged; a letter per change). Terminal app: `s` on History opens the Changes view (Enter or `d` diff, `s`/`u`/`x` stage, unstage, discard, `a`/`A`/`X` for all, `C` commit). |
| G2 | **Diff of uncommitted changes**: staged, not staged, new files | done | `workspace.Diff`: `git diff [--cached]`, new files against nothing (`--no-index`), cut at 256 KB. `gitswitch diff [--staged] [<path>]`, `GET /api/repo/diff`; desktop: click a file in the changes panel. Terminal app: the Changes view (`s` on History). |
| G3 | **Stage, unstage and discard** files | done | `Stage` (`add --all -- <paths>`), `Unstage` (`reset -- <paths>`), `Discard` (`stash push --include-untracked -m "gitswitch discard: <branch>" -- <paths>`: the changes stay in the stash list). Literal paths only. Refused for conflicted files, discard during a merge or rebase, and unstaging everything mid-merge. `gitswitch stage\|unstage <path>...\|--all`, `gitswitch discard <path>...\|--all [-y]`, `POST /api/repo/stage\|unstage\|discard`; desktop: + − ↺ per file and Stage all, Unstage all, Discard all (asks first). Terminal app: the Changes view (`s` on History). |
| G4 | **Stage, unstage and discard hunks and lines** | done | `workspace` hunks: a diff is parsed into hunks and lines, and a patch is built from the picked ones (counts recomputed; unpicked lines become context or are dropped by direction; no-newline and CRLF kept) and applied with `git apply --cached --recount` (unstage: `--reverse`; discard: `--reverse` on the folder after `--check`). The file must be unchanged since you looked (a fingerprint), else "reload". A discard first stores the file as it was in a stash entry ("gitswitch discard: <branch>") built from plumbing, so nothing is lost; the way back is `git restore --source='stash@{0}' -- <path>`. Refused for binary and mode-only changes, conflicts, partial renames, partial discards of new or deleted files, and during a merge or rebase. `gitswitch hunks <path> [--staged] [--json]`, `stage\|unstage\|discard <path> --hunk 1,3`; `GET /api/repo/diff` also returns `hunks` and `fingerprint`; `POST /api/repo/stage-hunks\|unstage-hunks\|discard-hunks`; desktop: Stage, Unstage and Discard hunk buttons, a checkbox per changed line (click, Shift+click for a range, Space), and a bar for the picked lines. Terminal app: `H` in the Changes view lists a file's hunks (Space picks lines; `s`, `u`, `x` act on the picked lines or the hunk). To come: line picks in the CLI. |
| G5 | **Commit box**: message with subject and body, recent messages, amend the last commit, sign-off, co-authors | partly | Done: summary and description in the desktop panel; amend (`workspace.Amend`: refused when the last commit is on any remote branch, is a merge, or during a merge or rebase; Undo puts the commit back as it was; desktop tick box, terminal app form, `commit --amend`); recent messages (your own, all branches, trailers stripped), a draft kept in `.git/GITSWITCH_MSG` after a refused or failed commit, sign-off (`gitswitch.signoff`, global or per repo, as the account's name and email) and co-author trailers (suggested from the last 500 commits; your own accounts are never suggested). `commit --recent [--json]`, `--signoff`, `--co-author "Name <email>"`, `set signoff on\|off [--repo]`, `GET /api/repo/commit-info`, `POST /api/signoff-setting`. Desktop: a "Recent messages" picker, a sign-off box, co-author chips with suggestions, and a kept draft that comes back. To come: the terminal app. |
| G6 | **Ignore a file**: add a path, its extension or its folder to `.gitignore` or `.git/info/exclude` | done | `workspace.Ignore`: an escaped, anchored pattern (`/dir/name`, `*.ext`, `/dir/`) on its own line, never twice; says when git still tracks the file and untracks it (`git rm --cached`, files kept) only when asked; `git check-ignore` confirms. `gitswitch ignore <path> [--ext\|--folder] [--local] [--untrack]`, `POST /api/repo/ignore`. Desktop: right-click a file in the changes panel, or ⊘ on a new file (the file, its extension or its folder; only for me). Terminal app: `i` in the Changes view. |

### Merge, rebase and conflicts

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G7 | **Merge a branch** into the current one: fast-forward when possible, a merge commit, or squash | done | The `merge` feature. A plan first (`git merge-tree --write-tree` predicts clashes without touching your files; commits coming in and only here), refused with uncommitted tracked changes, mid-operation or on a detached HEAD. A squash that would clash is rolled back. Undo resets to before the merge (a merge commit already pushed is reverted instead). `gitswitch merge <branch> [--ff-only\|--no-ff\|--squash] [-y]`, `merge --plan [--json]`, `GET /api/merge/plan`, `POST /api/merge`; desktop: "Merge into <branch>…" in a branch's menu. Terminal app: `e` on History (or `m` in the branch list) asks the mode and runs it. |
| G8 | **Operation in progress**: continue, skip or abort a stopped merge, rebase, cherry-pick or revert | done | `merge.Operation`: the kind, the files still in conflict and what may be done (a merge cannot be skipped). Continue keeps git's message (`GIT_EDITOR=:`) and refuses while clashes remain. `gitswitch continue\|skip\|abort`, `GET`/`POST /api/operation`; desktop: a banner above the log with the clashing files, Continue, Skip and Abort. Terminal app: `e` opens the Operation view (`c` continue, `s` skip, `a` abort), and History shows a banner. |
| G9 | **Conflict editor**: ours, theirs and the result | done | `merge.Conflict`: base, yours and theirs (`git show :1/:2/:3:`), the file with its markers (up to 512 KB, no binary); take yours or theirs (a side that deleted the file deletes it), save an edited result, or mark it resolved (refused while markers remain). `gitswitch conflict [<path>] [--ours\|--theirs\|--done]`, `GET`/`POST /api/conflict`; desktop: a dialog with both sides and an editable result, opened from the banner or the Conflicts list. To come: clash-by-clash choices, "Open in IDE" for the file, the terminal app. Desktop: the conflict dialog lists each clash (diff3 base supported) with Take yours, Take theirs, Take both and Edit, counts "N of M clashes left", and has Open in editor and (when set) Open merge tool; the Conflicts list in the changes panel has the same two buttons. Terminal app: a Conflict view with the same per-clash choices. |
| G10 | **Rebase onto a branch** | partly | The `rebase` feature. A plan first (your commits oldest first, the commits coming in, an estimate of likely clashes from changed-file overlap), then `git rebase <onto>` with no autostash. Refused before anything runs: uncommitted tracked changes, another operation, a detached HEAD, replayed commits already on a remote branch (it would need a force push; merge instead), merge commits in the range (a rebase would flatten them), unrelated histories. Clashes stop the rebase instead of aborting it, for `gitswitch continue\|skip\|abort` and `gitswitch conflict`. Undo works for a rebase that finished at once and for one stopped and continued later (a pending record in `[gitswitch "rebase"]`, the original tip kept at `refs/gitswitch/rebase`, completed when the operation ends). `gitswitch rebase <onto> [-y] [--plan [--json]]`, `GET /api/rebase/plan`, `POST /api/rebase`, `POST /api/rebase/finish`. Terminal app: `L`, Rebase onto…. To come: the desktop screen. |
| G11 | **Interactive rebase**: reorder, squash, fixup, reword, drop | partly | The `rewrite` feature. The commits that can be rewritten (by default the not-pushed commits after the last merge; `--since` for older ones), a plan showing Before and After, then `git rebase -i` with the todo supplied by GitSwitch (`pick`, `reword`, `squash`, `fixup`, `drop`; list order is the new order). Messages reach git through `exec git commit --amend -F <file>` lines, so a reword survives a stop and a later continue, and `#` lines are kept. Refused: pushed commits, merge commits, the first commit of the repo, detached HEAD, uncommitted tracked changes, steps that don't match the commits, a first step that is squash or fixup, an empty reword. A clash stops it (`gitswitch continue\|skip\|abort`, `conflict`). One Undo puts back the original tip (kept at `refs/gitswitch/undo`); a rewrite stopped and continued later is recorded too (`refs/gitswitch/rewrite-orig`). `gitswitch rebase -i [--since <rev>] [--todo <file>] [--messages <file.json>] [--plan] [-y]` (in a terminal it opens your editor on git's todo format), `GET /api/rewrite/todo`, `POST /api/rewrite`, `POST /api/rewrite/finish`. Not tested on Windows. Terminal app: `L`, Interactive rebase… (`p` `r` `s` `f` `d` set the action, `J` `K` move, a live After list, `x` runs). To come: the desktop editor. |
| G12 | **Reword or amend an older commit** | partly | `gitswitch reword <commit> [-m <message>] [-y]` (a one-step rewrite; the commits after it are replayed with new hashes) and `gitswitch fold <staged changes into an older commit>` as `gitswitch fixup-into <commit>` (a `fixup! <full hash>` commit then an autosquash; refused with unstaged tracked changes; undone as an amend). `POST /api/rewrite/reword`, `POST /api/rewrite/fixup-into`. Terminal app: `l` rewords the selected commit, `L`, Fold into…. To come: the desktop screens. |

### Branches, tags, remotes and stash

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G13 | **Delete, rename a branch; set its upstream** | done | `refs.DeleteBranch` (refuses the current branch and one checked out elsewhere; commits on no other branch or remote need force, and the count is said), `RenameBranch` (keeps the upstream), `SetUpstream`. Delete and rename report to Undo (rename undoes as a rename back). `gitswitch branch delete <name> [--force] [-y]`, `branch rename [<old>] <new>`, `branch upstream <remote>/<branch>\|--unset`, `POST /api/branch/delete\|rename\|upstream`. Desktop: Rename…, Pull from a remote branch… and Delete… in the branch menu (panel and pills; asks again before deleting commits that are on no other branch). Terminal app: `B` lists branches (switch, rename, upstream, delete). |
| G14 | **Tags**: create (light or annotated), delete, push | done | `refs.NewTag`, `DeleteTag`, `PushTag` (with the repo's account, never forced). `gitswitch tag [list] [--json]`, `tag new <name> [<commit>] [-m <msg>]`, `tag delete <name> [-y]`, `tag push <name>`, `GET /api/tags`, `POST /api/tag/new\|delete\|push`. Not reported to Undo. Desktop: "Tag…" on a commit (then Push it), Push and Delete in a tag's menu. Terminal app: `t` tags the selected commit, `T` lists tags (push, delete). |
| G15 | **Remotes**: add, remove, rename, change URL | done | `refs` remotes: list (with the account each URL would use, and which branches track it), add (name and URL checked like clone URLs; says when your rules give the owner to a different account than the repo's), remove (refused while branches track it, unless forced; says which lose their upstream), rename (tracking follows) and set-url. `gitswitch remote [list\|add\|remove\|rename\|set-url]`, `GET /api/remotes`, `POST /api/remote/add\|remove\|rename\|set-url`. Not in Undo: each result prints the command that reverses it. Desktop: *Remotes…* in the ⋯ menu (add, rename, change URL, remove with the force retry; each shows the account it pushes as). Terminal app: `O` (Remotes and more: remotes, delete on the server, fetch and forget, force push with lease that needs a typed yes, check out detached) and `E` (back). |
| G16 | **Delete a remote branch; fetch with prune** | done | `gitswitch branch delete-remote <remote>/<branch> [--force] [-y]` as the repo's account (refuses the remote's default branch, and the branch you are on when it tracks it, unless forced; prints how to restore it); `fetch --prune` and the setting `gitswitch.fetchPrune` (off by default, so the background fetch never changes your branch list silently), listing what was forgotten and which local branches now have a gone upstream. `POST /api/remote-branch/delete` (with `preview`), `POST /api/repo/fetch {prune}`, `POST /api/fetch-prune-setting`. Desktop: "Delete on the server…" in a remote branch's menu, *Fetch and forget deleted branches*, and a Settings checkbox for the setting. Terminal app: `O` (Remotes and more: remotes, delete on the server, fetch and forget, force push with lease that needs a typed yes, check out detached) and `E` (back). |
| G17 | **Force push with lease** | done | Only `--force-with-lease=<branch>:<expected>`, never a plain force, only for the current branch, only after listing the remote commits it would overwrite and your own, running the account check, and asking (`-y` in scripts); if the remote moved since you looked it says to fetch and look first. `gitswitch push --force-with-lease [-y]`, `GET /api/repo/push-plan`, `force:"lease"` with `confirm` on `POST /api/repo/push`. Desktop: a force-push dialog (red warning, the commits it would lose and yours, the account, a confirmation box; the account check first; a stale lease re-reads the plan), offered when Push is refused and in the ⋯ menu. Terminal app: `O` (Remotes and more: remotes, delete on the server, fetch and forget, force push with lease that needs a typed yes, check out detached) and `E` (back). |
| G18 | **Stash manager**: every stash, not only parked changes; stash with a message or chosen files; apply, pop, drop, branch from a stash | done | `refs` (`refs.StashGit`): list with kind (park, discard or stash), the branch it was made on and its files with counts (new files read from the stash's third parent), show a file's diff, save (message, chosen paths, keep the index, new files by default), apply, pop, drop and "new branch from stash". Stashes are named by their commit hash and re-checked before each action ("the stash list changed; reload"). Apply and pop need a clean folder and, on a clash, leave everything as it was and the stash in place. Undo: drop (the commit stays reachable through `refs/gitswitch/undo`) and a plain save; apply, pop, branch and saves with paths are not undoable. `gitswitch stash [list\|show\|save\|apply\|pop\|drop\|branch]`, `GET /api/stashes`, `GET /api/stash/show`, `POST /api/stash/save\|apply\|pop\|drop\|branch`; terminal app: `h` on History. Parked changes (`P`, `gitswitch parked`) are unchanged. Desktop: "Stashes…" in the ⋯ menu: every stash with its kind, files and diffs; Apply, Pop, New branch…, Drop (Undo brings it back) and "Stash changes…". |
| G19 | **Check out a commit or tag** (detached HEAD), with a way back | done | `refs.CheckoutDetached` for a commit, tag or remote branch, with the same smart-switch or park question as Switch (a smart switch that clashes restores everything; park is refused from a detached HEAD); the branch you came from is remembered, and `back` returns there; commits made while detached that are on no branch block leaving unless forced. `gitswitch checkout <rev> [--smart\|--park] [--force]`, `gitswitch back`, `POST /api/repo/checkout`, `POST /api/repo/back`, `GET /api/repo/detached`. Desktop: *Check out this commit (detached)* on a commit, *Check out (detached)* on a tag, and a banner with *Back to <branch>* and *New branch here…* (it warns about commits on no branch). Terminal app: `O` (Remotes and more: remotes, delete on the server, fetch and forget, force push with lease that needs a typed yes, check out detached) and `E` (back). |

### Inspecting history

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G20 | **Blame**: who changed each line, and in which commit | done | `history.Blame`: `git blame --porcelain` for a path at a rev, with ignore-whitespace (`-w`) and moved or copied lines (`-M -C`); per line the commit, author, time, original path and line, and the gitswitch account owning the author's email; runs of lines from one commit are grouped into chunks; capped at 5000 lines, binary files refused, paths literal. `gitswitch blame <path> [--rev] [-w] [-M] [--json]`, `GET /api/blame`. Desktop: an annotate view (a band per commit with its account, syntax colours, ignore-whitespace and moved-lines toggles, "Blame before this commit" to walk back), opened from a file's right-click menu in a commit or in the changes panel, or from the palette. Terminal app: `I`, Blame (walk back with `b`, `w` and `m` toggles). |
| G21 | **File history**, following renames | done | `history.FileLog`: `git log --follow --name-status -M`, paged (`n`, `skip`), each commit with the file's name at that time and its status (A, M, D, R with similarity). `gitswitch log --file <path>`, `GET /api/file/history`. A rename row's diff shows the new path only. Desktop: a file-history dialog (rename rows, the file's diff per commit, "Blame at this commit"). Terminal app: `I`, File history. |
| G22 | **Compare any two commits or branches** | done | The `inspect` feature: what B changed since the two diverged (`A...B`, the default) or the direct difference (`A..B`); the commits only on each side (capped at 50, the rest counted), the changed files with status and counts, and one file's diff (cut at 256 KB, binary refused). B can be the files on disk (`--working`). `gitswitch compare <A> [<B>] [--since-fork\|--direct] [--working] [--file <path>] [--json]`, `GET /api/compare`, `GET /api/compare/file`. Desktop: a Compare dialog (⋯ menu, a branch's menu, the palette) with both commit lists, the changed files and each file's diff. Terminal app: `I`, Compare. |
| G23 | **Better diff view**: side by side, word changes, whitespace options, syntax colours, images | done | One renderer for every diff in the desktop app, with a toolbar: Unified or Side by side, Word changes (a token diff on paired lines), Ignore whitespace, Syntax (a built-in tokenizer for Go, JS/TS, Python, Rust, Java/Kotlin, C/C++, shell, JSON, YAML, Markdown, HTML, CSS), and collapse or expand each hunk. Only the first 400 lines are drawn, with "Show more"; an image change says so. In the changes panel, unified keeps the per-line checkboxes and side by side is read-only (whole-hunk buttons only). Not done: a "lines of context" control. |
| G24 | **Search the code changes** (`-S`/`-G`) | done | `history.Filter` gains Pickaxe, PickaxeRegex and PickaxeIgnoreCase: commits that added or removed a text (`-S`), or whose changed lines match a regex (`-G`). `gitswitch log --code <text> [--regex] [--ignore-case]`, `code`, `codeRegex` and `codeIgnoreCase` on `GET /api/history`. Desktop: "Code contains", Regex and Ignore case in the Filters panel, and the `code:` search token. Terminal app: `I`, Search code changes. |
| G25 | **Reflog**: find lost commits and branches | done | `inspect.Reflog`: HEAD or a branch, paged, each entry with its action and whether the commit is now reachable from no branch ("lost"); recover makes a branch at an entry (`git branch --no-track`, so HEAD and your files never move). `gitswitch reflog [-n] [--skip] [--branch] [--lost] [--json]`, `reflog recover <n\|hash> <name>`, `GET /api/reflog`, `POST /api/reflog/recover`. Recovery is not recorded in Undo; delete the branch to take it back. Desktop: a Reflog dialog (lost commits flagged, "Make a branch here…", then "Switch to it"). Terminal app: `I`, Reflog. |
| G26 | **Bisect**, guided | done | `inspect.Bisect`: start with a bad and a good commit, mark each step good, bad or skip, and get "about N steps left" or the culprit with its author and files; reset returns to the branch you were on (saved in the repo's `gitswitch.bisectbranch` and `gitswitch.bisecthead`, so it works after a restart). Refused with uncommitted changes to tracked files, mid-operation, or when good is not an ancestor of bad; skips that leave it open list the candidates instead of naming a wrong one. `gitswitch bisect start\|good\|bad\|skip\|status\|reset`, `GET`/`POST /api/bisect`. Not undoable. Desktop: a bisect dialog and a banner above the log while it runs (Good, Bad, Skip, Stop; the culprit card at the end). Terminal app: `I`, Bisect, and a banner on History while it runs. |

### Bigger repos

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G27 | **Live refresh**: the changes list and status follow the files | done | Done: the `watch` feature and `internal/platform/filewatch` (fsnotify, BSD-3): one watcher per repo, started on demand and stopped after 2 minutes idle, debounced (300 ms, at most 2 s), classified as files, index, head, refs or operation; git ignored folders are skipped, at most 4000 watches (then only the top folder and the git dir, flagged `partial`). `GET /api/watch?gitDir=&since=` is a long poll of up to 25 s. The desktop page follows it: the changes list and history are read again when files, HEAD, refs or an operation change (not under your cursor while you type a message; paused while the window is hidden). Checked in the real window on Linux (WebKitGTK): the held request does not block other calls, an outside edit shows within about a second, and the app exits cleanly. Not yet checked on macOS (WKWebView) or Windows (WebView2). A tab switch cancels the old repo's poll. |
| G28 | **Commit signing** per account (SSH or GPG), and verified badges in the log | done | An account gains a signing key and format (`gitswitch account edit <id> --signing-key <path\|key::...\|gpg id> --signing-format ssh\|gpg\|off`). Wherever the repo's identity is written (`use`, clone, init, the ssh hook) the repo also gets `commit.gpgsign`, `gpg.format`, `user.signingkey` and, for ssh, an allowed-signers file so `git log` can verify; keys GitSwitch wrote are removed again, and a repo's own signing config is never touched. After setting it, the public key to add to the host as a signing key is printed (GitHub: Settings → SSH and GPG keys → Signing Key). Doctor checks the key files. `GET /api/signatures?hashes=` returns git's status per commit (good, bad, unknown, expired, revoked, cannot check, none), kept out of the `/api/history` rows. Desktop: badges in the log (a check for good, a warning for bad or expired, grey when it cannot be checked), a Signature row in the details, and a "Sign commits" choice in the account form that shows the public key to add. Terminal app: `s` on Accounts, and a badge column in the History list when the repo's account signs. |
| G29 | **Worktrees**: list, add, remove, open | partly | `refs` worktrees: list (branch, HEAD, locked, prunable, uncommitted counts, the account), add (an existing branch, a new branch from a commit, or detached; refused for a branch checked out elsewhere, a non-empty folder, or a path inside the repo's own folder), remove (refused for the main folder, a locked one, or one with changes unless forced; says what would be lost), prune (with a plan first), lock and unlock, repair. A linked worktree resolves to the same account as its main repo: found and fixed a bug where folder rules matched the worktree's own folder (`Runner.RuleFolderOf`, also used by the ssh hook). `gitswitch worktree [list\|add\|remove\|prune\|lock\|unlock\|repair]`, `GET /api/worktrees`, `POST /api/worktree/add\|remove\|prune\|lock\|unlock`. Known: `use --once` inside a worktree writes the name and email into the shared repo config (git config is shared; a per-worktree identity would need `extensions.worktreeConfig`). Terminal app: `W` on History or Repos (add, remove with a second yes for uncommitted work, prune plan, lock). To come: the desktop screen. |
| G30 | **Submodules**: list, init, update | partly | The `submodule` feature. List with each one's recorded and checked-out commit, state (not initialized, up to date, at another commit, merge conflicts), changes inside, ahead and behind its upstream, and the account its URL maps to (same rules as the ssh hook: its own saved account, the owner rule, the folder rule, the only account for the host). Init, update (`--remote` asks first and says the repo then records a new commit; refused where changes inside would be overwritten), sync, add (URLs must be ssh or https; says when the URL's account differs from the repo's) and remove (shows what is deleted; refused with changes or unpushed commits inside unless forced). Each submodule's remote work runs as its own owner's account through the ssh hook (tested with two accounts). `gitswitch submodule [list\|init\|update\|sync\|add\|remove]`, `GET /api/submodules`, `POST /api/submodule/init\|update\|sync\|add\|remove`. Not recorded for Undo; the list is not recursive. Terminal app: `V` on History (init, update, update to the branch tip, sync, add, remove). To come: the desktop screen. |
| G31 | **Git LFS**: status, lock, unlock | planned | Only when `git lfs` is installed. |

### App

| # | Feature | Status | How |
| --- | --- | --- | --- |
| G34 | **Repo tabs**, a command palette and custom shortcuts | done | Done in the desktop app: a command palette (Ctrl/⌘+K, fuzzy, every page and History action, recently used first; a disabled command says why) and repo tabs (Ctrl+Tab, Ctrl+W, drag to reorder, kept across restarts). To come: custom shortcut Custom shortcuts are done: Settings, Keyboard shortcuts records a chord per command (21 more actions can be bound), refuses collisions (naming the command), browser and OS reserved keys, and keeps everything in this computer's storage; Reset per command or all. |
| G35 | **External tools**: open in terminal, diff and merge tool settings | done | Settings `terminal`, `difftool` and `mergetool` (command templates with `{path} {left} {right} {merged} {base} {dir}`; empty means the OS default, or git's own `difftool` and `mergetool`), next to the editor. `internal/platform/process` splits commands without a shell, expands the placeholders and opens a terminal (macOS Terminal or iTerm, Linux x-terminal-emulator and friends, Windows `wt` or `cmd`). `gitswitch set terminal\|difftool\|mergetool <cmd>`, `gitswitch terminal [<repo>]`, `gitswitch difftool [--staged] <path>`, `gitswitch mergetool <path>`, `POST /api/open-terminal\|difftool\|mergetool\|open-editor`. Desktop: *Open in terminal* in the ⋯ menu, on each repo row and in the palette; *Diff tool ↗* in a change's diff and a commit file's menu. Tools start detached, so a terminal-only tool such as vimdiff does not work. |
| G36 | **One workspace screen**: changes and commit box beside the History graph, an "Uncommitted changes" row at the top of the log, branch-tip pills (local or remote) with double-click to check out | done | The row (staged and unstaged chips, its dot on HEAD's lane) opens the changes panel, docked by default in wide windows; summary and description, "as Name <email>" with the account. Pills: filled for local branches in the lane's colour, outlined for remote ones, laptop and cloud icons, ✓ for the current branch, "+N" past two; double-click switches (smart switch or park as usual), right-click opens the branch menu. Toolbar icons with count badges. |
| G37 | **Redo** after Undo | done | Undo keeps what it undid, with the commit reachable through `refs/gitswitch/redo`; Redo does the inverse under the same refusal rules (the branch moved since, uncommitted work would be lost, commits it would remove are already pushed) and can be undone again. Covered: commit, amend, rollback, revert, drop, cherry-pick, fix-author, update with rebase, merge, switch, move, rename, prune, branch delete and stash drop; not stash save. `gitswitch redo [-y] [--show [--json]]`, `POST /api/redo`, `redo` next to `undo` in `GET /api/refs`. Desktop: a Redo button next to Undo (Shift+R). Terminal app: `Y`. |
| G38 | **Fetch on an interval** (every N minutes, off by default) and a notification when a long operation ends | done | `gitswitch.fetchInterval` (off, 5, 10, 15, 30 or 60 minutes; `gitswitch set fetch-interval`) fetches the open repo quietly while the window is visible, skips while a dialog or another action is running, and backs off when it fails (up to 2 hours). A Settings box turns on a desktop notification when clone, fetch, push, update, merge or Continue ends while the window is in the background; without notification support the window title gets a leading dot until you come back. |
| G39 | **Stashes in the graph**, and **author avatars** from the signed-in host or Gravatar | planned | Avatars are cached and optional. |
| G40 | **Themes**: light, dark or the system's | done | Settings and the command palette: System (default), Light, Dark and Dim (a softer dark). Set on `<html data-theme>` before first paint and remembered on this computer. |
| G41 | **Start a repo** (`git init`) and check that git is installed | done | `gitswitch init [<folder>] [--account <id>] [-b <branch>]`: the account comes from the flag, rules, the default or the only account (several and undecided: it asks, or says to pass `--account`, before creating anything); git older than 2.30 or missing is refused; the repo is added to the list. `POST /api/repo/init`, `GET /api/git-info` (the doctor check uses the same code). Desktop: *New repo…* on the Repos page and a banner when git is missing or too old. Terminal app: `n` on Repos; the setup wizard checks git first and ends with an optional "Start a new repo now?". |
| G42 | **Built-in terminal** in the desktop app | later | Big (a pty and a terminal emulator in the page); needs its own design note first. |

Compared with [gitcraft](https://github.com/Vancete/gitcraft) (Rust, egui; 2026-11): it already has most of G4–G35 and the items G36–G42 came from it. GitSwitch keeps what gitcraft lacks: accounts with the right key, token and email per repo, Undo that survives restarts, safe defaults (smart switch, park, discard into a stash), the Reviews inbox across hosts, and the CLI and terminal app.

Order: G1–G3 (core, CLI and API, then the desktop changes panel inside
History, G36, and the terminal app), G5, G4, G27, G34, then G7–G9, G23,
G13–G18, G10–G12, G20–G22, then the rest. Each is one commit with unit and integration tests (real git), and
the README.

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
| H12 | **Undo the last action** (drop, roll back, cherry-pick, revert, fix-author, switch, park, new branch, move, prune, commit) | done | The `undo` feature: history and refs use cases report each change to its Recorder; the record lives in `refs/gitswitch/undo` (so `git gc` keeps dropped commits) and `[gitswitch "undo"]`. Refused when the branch moved since, when it would remove pushed commits, or when uncommitted changes clash. "Undo" in the success message and the History toolbar, `U` in both apps, `gitswitch undo [-y] [--show [--json]]`. |
| H13 | **Load more**: page through history beyond the first page of commits | done | `History.More` says git had more; the desktop app's "Load more" button, `m` in the terminal app and `gitswitch log -n` read a bigger window, so the graph stays connected. |
| H14 | **Uncommitted changes when switching**: offer to stash and restore | done | Replaced by Smart switch, Park and Resolve (B2–B4) in [v1.4](#everyday-branch-work-v14). |
| H15 | **Update with rebase** for people who prefer it | done | `refs.UpdateWith` and `gitswitch.pull = ff-only\|rebase` (global, or per repo first). A fast-forward first; only when both sides have new commits, `git pull --rebase` with autostash off, refused before anything runs with uncommitted changes to tracked files or your own merge commits, aborted on a clash. Undo (`update`) puts your commits back while the rebased ones are not pushed. `gitswitch update --rebase`, `set pull`; desktop offers it when Update stops and in Settings; terminal app `r` and Settings. |
| H16 | **Compare a branch with its upstream**: list what you would push or pull | done | `refs.Compare`: `git log` of `<branch>@{upstream}..<branch>` and the reverse, up to 100 commits each (the rest counted from ahead/behind). Desktop "Compare with the remote" in the History toolbar's ⋯ menu, with Push or Update when only one applies; terminal app `S`; `gitswitch branch compare [--json]`. |
| H17 | **IDE-style log** (inspired by Android Studio's): branches panel, labels, changed files and diff, sort, view and highlight options | done | `history.Filter` gains Ref (one branch or tag, after `--end-of-options`; the graph stays), Path, Order (date or topological), FirstParent (only first-parent edges are drawn) and NoMerges; commits carry typed labels (`--decorate=full`), Merge and OnHead; `Log.FileDiff`; `refs.Tags`. Desktop: three panes (branches tree with click to show, double-click to switch, right-click menu; log; details with a file tree and diff preview), "Sort and options" and "View" menus kept in the page's storage. Collapse linear branches (in the page: plain one-lane runs of four or more fold, opened per run) and not cherry-picked commits (`Filter.Cherry`: `rev-list --cherry-mark --right-only HEAD...<ref>`, only with one branch shown) came after, then the changed files' view menu (group by directory and/or module, `history.FileChange.Module` from the commit's tree; show details; diff preview or a diff window). |

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
| H12 | **Undo the last action** | done | See [History screen](#history-screen). Smart switch, park, new branch, move, prune and commit report to it. |
| B6 | **New branch** from the current branch or any commit, bringing uncommitted changes | done | `refs.NewBranch`: `switch --create`, which keeps them without a stash; from another commit, when they touch files that differ there, it refuses and nothing changes. Works from a detached HEAD. Desktop branch menu "New branch…" and the commit's "New branch here", terminal app `n` / `N`, `gitswitch branch new <name> [--from <commit>] [--no-prefix]`, optional name prefix in `gitswitch.branchPrefix` (`gitswitch set branch-prefix feature/`, both apps' Settings). Reports to Undo. |
| B7 | **Push branch** and **open a pull request** | done | `refs.Push`: to the branch it tracks, else `push -u` to origin (or the only remote) under the same name; says which account pushed; refuses a diverged branch before pushing and explains a rejected push. `remote.PullRequestURL` builds the PR link (GitHub, GitLab, Bitbucket), not for the remote's default branch. Desktop "Push" button with "Open pull request", terminal app `p`, `gitswitch push [--open]`. Never a force push. |
| B8 | **Account check before commit and push** | done | `history.CheckAccount`: the account that pushes, whether the repo's `user.email` is that account's, and the not-pushed commits on the branch made as another account (the wrong-account rule). Before a push every front end offers "fix them first" (fix-author), "push as they are" or cancel; without a terminal `gitswitch push` needs `-y`. A wrong repo email shows in the desktop History banner ("Use <account>'s name and email") and the terminal app's History header (`i`), and `gitswitch push` and `status` say how to fix it (`gitswitch use <account> --once`). Before a commit, `gitswitch commit` (B11) refuses while the repo's email is not its account's; there is no git hook. |

Order: B1, H12, B2–B5, B6, B7, B8. Each is one commit with unit and
integration tests (real git: new files, staged changes, clashes, parking
twice, undo of each), then the desktop and terminal app screens and the
README.

### Later

| # | Feature | Status | Notes |
| --- | --- | --- | --- |
| B9 | **Move not-pushed commits to a new branch** (committed on the wrong branch) | done | `refs.MoveToNewBranch`: `switch --create` at HEAD, then `update-ref` moves the old branch back to its merge base with its upstream, only while it is still where it was (otherwise the new branch is removed and nothing changes). Uncommitted changes stay: HEAD does not move. Undo (`move`) puts the commits back on the old branch, switches back and deletes the new one, refused when either branch moved since. Desktop branch menu "Move not-pushed commits to a new branch…", terminal app `M`, `gitswitch branch new <name> --move`. |
| B10 | **Delete merged branches** | done | `refs.Merged` and `refs.Prune`: local branches merged into the remote's default branch (`for-each-ref --merged`; without one, a local main or master), never the current branch, main, master, the default branch or one checked out in another worktree. Deleted with `update-ref -d` at the commit listed, and their `branch.<name>` config removed. A branch whose upstream is gone but that is not merged (squash merge) is kept and named. Undo (`prune`) recreates them with their upstream. Desktop branch menu "Delete merged branches…", terminal app `D`, `gitswitch branch prune [-y] [--dry-run]`. |
| B11 | **Commit** (staged files or all, message) | done | The `workspace` feature: `workspace.Commit` commits the staged files, or with `--all` every change (`git add --all`), refused when nothing is staged without it, with conflicts, mid-operation, or when the repo's email is not its account's. Picking files and lines stays with the IDE. Undo (`commit`) moves the branch back and keeps the changes in the files. Desktop "Commit…" button, terminal app `C`, `gitswitch commit -m <message> [--all]`. |
| B12 | **Fetch**: read the remote's new commits so "to pull" and Compare are right | done | `refs.Fetch`: `git fetch --progress` with the repo's account, no pruning; reports how many commits the current branch's upstream gained (`refs.Fetched`). Never in the background. Desktop "Fetch" button (with an Update action when something is waiting), terminal app `F`, `gitswitch fetch [--json]`. `gitswitch set fetch-on-open on\|off` (off by default) fetches once per repo when History opens it: Settings in both apps. |

## Reviews (v1.6)

The pull requests waiting on you, and how your team's reviews are going,
for every signed-in account. Inspired by
[TeamStatus for GitHub](https://github.com/marcinreliga/TeamStatus-for-GitHub)
(one repo, one account, a token on the command line, polling every minute),
reworked for GitSwitch: all your accounts and their repos, the token in the
OS keychain, and nothing running in the background.

### Design rules

- **Optional.** Everything here needs an account signed in to GitHub. Without
  one the page says so in a line; the rest of GitSwitch is unchanged.
- **Each account on its own.** Work and personal inboxes never mix; every PR
  shows the account that sees it.
- **No background process.** Read when the page opens, every 5 minutes while
  it is open, or on request; cached like the GitHub repo list. The report is
  read only when asked for.
- **Read only.** GitSwitch shows and explains; approving, requesting
  reviewers and merging stay on GitHub and in IDEs, a click away.
- **Same in every front end.** Desktop app, terminal app, CLI with `--json`.
- **One more token permission.** Reading PRs on private repos needs
  "Pull requests: read" on a fine-grained token; the sign-in advice gains it,
  and Reviews says what is missing rather than showing empty lists.

### Features

| # | Feature | Status | How |
| --- | --- | --- | --- |
| R1 | **Review inbox per account**: PRs waiting for your review (from you or a team you are on), your open PRs, PRs you reviewed lately | done | `hostingapp.Reviews`: one GraphQL request per signed-in account (three `search` aliases with its login, `hosting.InboxQueries`), cached per account (`<cache>/remote-repos/<account>.inbox.json`) and read again after 5 minutes; a failed read keeps the last inbox and says why; signing out forgets it. Desktop "Reviews" page (read again every 5 minutes while open), terminal app tab 6, `gitswitch prs [--account id] [--refresh] [--json]`. A token without "Pull requests: read" gets that advice. |
| R2 | **Status on your own PRs**: approved, changes requested, checks failing, conflicts, draft | done | `hosting.StatusChips` from `reviewDecision`, `statusCheckRollup` and `mergeable`: draft, conflicts, checks failing or running, changes requested, approved or approved and ready to merge; what needs you in red with what to do, ready in green. |
| R3 | **PR status on the History toolbar** for the current branch | done | `hostingapp.BranchPR`: the open PR headed by the branch of the repo itself (a fork's same-named branch is skipped), read with the repo's account (`hostinginfra.GitHub.BranchPR`, one GraphQL request, nothing cached): `PR #42 · 1 approval · checks passing` (`hosting.BranchSummary`), linking to it, red or green by its chips. Quiet when there is no PR, no GitHub remote or no signed-in account. Desktop link by the sync status, terminal app `K`, `gitswitch branch pr [--json]`. |
| R5 | **Reviews in the terminal app and CLI** | done | Terminal app tab 6 and `K`, `gitswitch prs`, `gitswitch branch pr` (R1 to R3). |

Order: R1 and R2 together, then R3. Each is one commit with unit
tests (fake API), a sandbox check in the desktop app, and the README.

Left out on purpose: a count of PRs needing attention, reviewer
availability, and open-in-GitHub shortcuts.

## Where to pick up next

A hand-off for the next working session. Update it when you stop.

### State of the work

- **Committed** up to "Terminal app: merge, conflicts, hunks, inspect, remotes, rewrite, worktrees, submodules". Every feature in the v2 table above has its core, CLI and API; the desktop and terminal screens exist for everything except the items under "Still to build". `go vet` and the full test suite pass.
- **Nothing is in flight in the code.** The last feature round (custom shortcuts, clash-by-clash resolution, external tools, fetch interval) is committed and tagged `v2.0.0-beta.1` locally. The tag is **not pushed**: any pushed `v*` tag builds and publishes a GitHub release, and `install.sh` installs the latest release.
- **Unfinished: screenshots, then a landing page and README redesign.** See "Screenshots and landing page" below.

### Screenshots and landing page (next, in this order, one step at a time)

1. **Two small UI fixes first** (they show in marketing shots): the changes panel's title wraps onto three lines ("Uncommitted / changes on feature/checkout") because the panel is narrow, and the toolbar status is clipped ("Uncommitted: 1 sta…"). Fix in `internal/interfaces/desktop/assets/index.html`.
2. **Capture the screenshots** with the scripts in `sandbox/screenshots/` (see its README). `setup.sh` builds a demo repo of a fictional shop with fictional people and two accounts; nothing from the real machine may appear in an image. Two shots were captured and looked good (the hero with the changes panel, and the side-by-side diff; webp at 1440x900 @2x is about 100 to 260 KB each) but were not kept. The list: `history-changes` (the hero), `diff-split`, `hunks`, `conflict`, `merge`, `park` (the Park and Smart switch question, the feature the owner loves most), `stashes`, `blame`, `compare`, `palette`, `force-push`, each as `-light` and `-dark`, plus one `themes-dim`. Output: `docs/images/v2/<name>-light.webp` and `-dark.webp`. Look at every image before using it.
3. **Redesign `docs/index.html` and add screenshots to `README.md`.** Positioning, decided by the owner: GitSwitch is **a beautiful git client with the important features**; the demo people are only background, and multiple accounts are one important feature among others, not the headline. Lead with the app itself (a large hero screenshot of the changes panel and graph in a polished window frame), then a screenshot-led tour (stage by line, Park and Smart switch with the most weight, merge and conflicts, blame and compare, stashes, force push with lease, the palette, themes), then a section for multiple accounts and signing, install, apps and IDE notes. Use `<picture>` with light and dark webp, explicit width and height, lazy loading, a sticky nav, scroll reveal that respects reduced motion, semantic landmarks, AA contrast, 360 to 1920 px responsive. README: replace the hero image and add a "Screenshots" section (a two-column table with captions) linked from the contents. Check the page in light and dark at 1440, 1024, 768 and 390 px with headless Edge before committing.

### Still to build

- **Desktop screens** for rebase onto a branch (a plan dialog, G10), the interactive rebase editor with drag to reorder and a per-commit action (G11), reword and fold-into-older-commit buttons (G12), a worktrees list (G29) and a submodules list (G30). Their backends, CLI and terminal screens are done.
- **Terminal app:** the commit box pickers for recent messages, sign-off and co-authors (G5).
- **Not started:** Git LFS (G31), stashes drawn in the graph and author avatars (G39), a built-in terminal (G42, write a design note first).
- Line picks in the CLI for hunks (G4).
- Small: the Ctrl/⌘+W tab tooltip is static when you rebind it; custom diff and merge tools start detached (no terminal-only tools).

### Release work (not on the roadmap)

1. Run the desktop app on macOS and Windows. The real-window check was done on Linux only (WebKitGTK); WKWebView and WebView2 are untested, including the 25-second live-refresh request and the file-watch budget on macOS (kqueue needs a watch per file).
2. Try GitLab and Codeberg against real servers (only fake servers so far).
3. Sign and notarize the builds (an Apple Developer account and a Windows certificate).
4. An accessibility pass on the newest screens (the palette, tabs, merge banner, conflict, blame and compare dialogs).
5. Fresh README and landing-page screenshots (`docs/images` still shows the old History page).
6. Review `docs/release-notes/v2.0.0.md`, then tag `v2.0.0` (the release workflow reads that file).
7. Windows is untested for the interactive rebase (it relies on git's bundled `sh` for `cp` and `exec` lines).

### Known limits

- `gitswitch use --once` inside a linked worktree writes the name and email into the shared repo config (git config is shared; a per-worktree identity would need `extensions.worktreeConfig`).
- Undo does not cover remote changes (remotes, deleting a branch on the server), worktrees, submodules, or stash apply and pop; each prints the command that reverses it.
- Merge, rebase and rewrite refuse commits a remote branch already has; a force push with lease is the only way to overwrite the server, and it lists what it overwrites first.

### How the work was done (so you can continue the same way)

- **Go is not installed on the host.** Run everything in Docker from the repo root: `docker run --rm -v "$PWD":/src -v gomodcache:/go/pkg/mod -v gobuildcache:/root/.cache/go-build -w /src golang:1.27 sh -c 'gofmt -l .; go vet ./... && go test -count=1 ./...'`.
- **Desktop page checks:** serve the page with a temporary `internal/zzdevserve/main.go` (`api.Handler(bootstrap.New(false, nil), nil)` on port 8091) in the same container, drive headless Edge over the DevTools protocol, and delete the folder afterwards. The page is `go:embed`-ed, so recreate the container after each edit.
- **Parallel agents** worked well when each owned separate files (a new feature directory, new `tui_*.go` files, one agent for `index.html`) and made small anchored edits to shared files (`api.go`, `cli.go`, `completions.go`, the README command table, `bootstrap/app.go`). Five at once has hit the usage limit; stopped agents can be resumed with their work intact.
- **Terminal app keys on History** already taken: `f r m o x b B t T F u p i U R P h n N M D C s S K v d z / c w A e I O E Y L l W V`; per-view keys are listed in the help screen.
- Every history-rewriting feature is tested against real git (temp repos, a bare repo as the remote); keep it that way.
