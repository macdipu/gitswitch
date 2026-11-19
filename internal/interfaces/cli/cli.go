// Package cli is the command-line front end. It parses arguments, calls
// the use cases (bootstrap.App), and prints results. It holds no logic of its own.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	"github.com/macdipu/gitswitch/internal/features/setup"
	"github.com/macdipu/gitswitch/internal/interfaces/tui"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

const usage = `gitswitch - use several git accounts on one computer

Setup
  gitswitch install                 one-time: hook gitswitch into git for every terminal and IDE
  gitswitch setup                   step-by-step setup in the terminal
  gitswitch                         open the app in this terminal (same as: gitswitch tui)
  gitswitch ui                      open the app in a desktop window
  gitswitch uninstall [--purge]     stop using gitswitch (--purge also deletes accounts)

Accounts
  gitswitch account add <id> --email E [--name N] [--host H] [--key PATH|new] [--default]
  gitswitch account edit <id> [--email E] [--name N] [--host H] [--key PATH]
                                    [--signing-key PATH|ID] [--signing-format ssh|gpg|off]
                                    sign this account's commits (ssh: the public key file, default the
                                    account's own key; gpg: a key id); add the PUBLIC key to the host
                                    as a signing key. Repos pick it up on their next use of the account
  gitswitch account list
  gitswitch account remove <id> [--delete-keys] [-y]
  gitswitch account rename <old> <new>
  gitswitch key new <id>            generate an ed25519 key (ssh-keygen asks for a passphrase)
  gitswitch key pick <id>           choose one of your existing keys in ~/.ssh
  gitswitch key show <id> [--open]
  gitswitch key agent <id>          load the key into the OS agent / Keychain once
  gitswitch test [<id>]             check each key logs in, and as whom
  gitswitch signin <id>             sign in to GitHub or GitLab with a token, kept in your keychain
  gitswitch signin [--check]        which accounts are signed in (--check asks the host)
  gitswitch signout <id>            remove the account's token from your keychain
  gitswitch github list [words] [--account ID] [--owner O] [--lang L] [--private|--public]
                                    [--cloned|--not-cloned] [--archived] [--refresh] [--json]
                                    repos your signed-in accounts can see on GitHub or GitLab, and where each is cloned
  gitswitch github clone <owner/name> [folder]clone one of them with the account that sees it
  gitswitch prs [--account ID] [--refresh] [--json]
                                              pull requests (merge requests on GitLab) waiting for your review,
                                              yours with what stands out, and ones you reviewed lately, for each
                                              signed-in account
  gitswitch default <id>            optional: account used when nobody answers (or Enter)
  gitswitch default --none          remove the default: repos with nothing saved always ask

Repos and rules
  gitswitch use <id> [--once]       set this repo's account (and its commit name/email)
  gitswitch use --ask               this repo asks on every pull/push and never remembers
  gitswitch forget                  remove this repo's account: follow rules and the default
  gitswitch rule add owner:<org> <id>     e.g. owner:acme-corp client-acme
  gitswitch rule add folder:<path> <id>   e.g. folder:~/work polygon
  gitswitch rule list | remove owner:<org>|folder:<path>|<n>
  gitswitch repo list [--pinned] [--by account|owner|folder|group] [--sort name|recent|account] [--json]
                                    repos in your project folders plus ones you added
  gitswitch init [<folder>] [--account <id>] [-b <branch>]   start a repo (git init) with an account's
                                    commit name and email, and add it to the list
  gitswitch repo clone <url> [folder] [--account <id>]   clone with the right account from the start
  gitswitch repo add [path]         add an existing repo to the list (nothing in it changes)
  gitswitch repo remove [path]      take a repo off the list (its files are not touched)
  gitswitch repo show [path] [--json]     folder, branch, remotes, account
  gitswitch repo open [path] [--editor]   open in the file manager or your editor
  gitswitch repo pin|unpin [path]   keep a repo at the top of the list
  gitswitch repo recent [--json]    repos you opened lately, newest first
  gitswitch repo group <name> [path] | ungroup [path]
                                    put a repo in a group you name, or take it out
  gitswitch status [--json]
  gitswitch fix-author              fix not-pushed commits made with the wrong account

History and branches
  gitswitch log [-n 40] [--flagged] commit graph with the account of each commit
  gitswitch log [--author TEXT] [--grep TEXT] [--since DATE] [--until DATE] [--account ID]
                                    only matching commits (author name or email, message, dates
                                    like 2024-05-01 or "2 weeks ago", gitswitch account)
  gitswitch log --code <text> [--regex] [--ignore-case]   only commits that added or removed that text
                                    (--regex: a regular expression matching changed lines)
  gitswitch log --file <path> [-n N] [--json]   the commits that changed a file, following renames
  gitswitch blame <path> [--rev <rev>] [-w] [-M] [--json]   who last changed each line (-w ignores
                                    whitespace, -M follows moved and copied lines)
  gitswitch branch [--json]         current branch, HEAD and the branch list
  gitswitch branch new <name> [--from <commit>] [--no-prefix]   start a branch here (or at a
                                    commit, branch or tag) and switch to it, bringing uncommitted
                                    changes; the branch-prefix setting goes in front of the name
  gitswitch branch new <name> --move   committed on the wrong branch: start <name> here with the
                                    commits not pushed yet, and take them off the current branch
  gitswitch branch compare [--json]   the commits you would push to and pull from the remote branch
  gitswitch branch pr [--json]      the branch's open pull or merge request: approvals, checks, conflicts
  gitswitch branch prune [-y]       delete the local branches merged into the remote's default
                                    branch (never the current one, main or master); asks first
  gitswitch branch delete <name> [--force] [-y]   delete a local branch (not the current one); asks
                                    first, and needs --force when its commits are on no other branch
  gitswitch branch rename [<old>] <new>   rename a branch (the current one without <old>); it keeps
                                    its remote branch
  gitswitch branch upstream <remote>/<branch>|--unset   the remote branch the current branch pulls from
  gitswitch changes [--json]        the uncommitted changes: staged, not staged, conflicts
  gitswitch diff [--staged] [<path>]   a file's uncommitted change (new files in full), or every file's;
                                    --staged shows what the next commit holds
  gitswitch stage <path>...|--all   add changes (new and deleted files too) to the next commit
  gitswitch unstage <path>...|--all   take changes out of the next commit; the files keep them
  gitswitch discard <path>...|--all [-y]   put files back as in the last commit (new files removed);
                                    asks first, and keeps the changes in git stash
  gitswitch hunks <path> [--staged] [--json]   a file's change in hunks, numbered from 1
  gitswitch stage|unstage|discard <path> --hunk <n>[,<n>...]   only those hunks; discard asks first
                                    (-y skips) and keeps the file as it was in git stash
  gitswitch ignore <path> [--ext|--folder] [--local] [--untrack]   add the file, its extension (*.ext)
                                    or its folder to .gitignore (--local: .git/info/exclude, this clone
                                    only); --untrack takes a tracked file out of git, keeping it
  gitswitch commit -m <message> [--all]   commit the staged changes as the repo's account;
                                    --all commits every change, new files too
  gitswitch commit --amend [-m <message>] [--all]   add the staged changes (--all: every change)
                                    to the last commit, or give it a new message; only while it
                                    is not pushed, and undo puts it back
  gitswitch commit ... --signoff|--no-signoff   add (or leave out) Signed-off-by with the repo's
                                    name and email, whatever the signoff setting says
  gitswitch commit ... --co-author "Name <email>"   add a Co-authored-by line (repeat for more)
  gitswitch commit --recent [--json]   the messages you wrote here lately, and the message of a
                                    commit that failed (kept so it is not lost)
  gitswitch switch <branch> [--smart|--park]   check out a branch (origin/x creates a local x);
                                    with uncommitted changes, --smart takes them along and --park
                                    leaves them on this branch until you come back; without either,
                                    the switch-changes setting decides, else it asks in a terminal
  gitswitch parked [--json]         the parked changes (git stash), newest first
  gitswitch parked show|restore|drop [<n>] [-y]   look at, restore or delete parked changes
                                    (<n> from the list; else the newest parked on this branch)
  gitswitch stash [list] [--json]   every stash (parked and discarded changes too), newest first
  gitswitch stash show <n|hash> [<path>] [--json]   a stash's files, or one file's diff (new files too)
  gitswitch stash save [-m <message>] [--staged-keep] [-u|--no-untracked] [<path>...]   stash the
                                    uncommitted changes, or only these files; new files are included
                                    unless --no-untracked; undo takes a plain stash of everything back
  gitswitch stash apply|pop <n|hash>   put a stash back (pop: and remove it); needs no uncommitted
                                    changes, and a clash changes nothing
  gitswitch stash drop <n|hash> [-y]   delete a stash; undo brings it back
  gitswitch stash branch <n|hash> <name>   a new branch from a stash, with its changes
  gitswitch resolve [--done|--abort|--park]   after a smart switch with clashing files: finish once
                                    they are fixed, go back exactly as before, or park them instead
  gitswitch merge <branch> [--ff-only|--no-ff|--squash] [-y]   merge a branch into the current one
                                    after showing what will happen (fast-forward, merge commit, clashes
                                    likely); --squash stages the changes without committing; undo takes
                                    the merge back off while it is not pushed. --plan [--json]: only show
  gitswitch compare <A> [<B>] [--since-fork|--direct] [--working] [--json]   how B (default HEAD) differs
                                    from A: the commits only on each side and the changed files; the
                                    default is what B changed since they went apart, --direct is the
                                    difference as they are now, --working compares with the files on disk
  gitswitch compare <A> [<B>] --file <path> [--direct]   one file's diff between the two
  gitswitch reflog [-n N] [--skip N] [--branch <name>] [--lost] [--json]   where HEAD (or a branch)
                                    has been, newest first; [lost] marks commits on no branch
  gitswitch reflog recover <n|hash> <branch-name>   make a branch at a reflog entry; nothing moves
  gitswitch bisect start <bad> <good>   find the commit that broke something: checks out commits for
                                    you to test; needs no uncommitted changes to tracked files
  gitswitch bisect good|bad|skip    say how the commit under test behaved; the next one, or the first
                                    bad commit, is shown
  gitswitch bisect status [--json]  the commit to test now, the steps left and the log so far
  gitswitch bisect reset            end the bisect and go back to the branch you were on
  gitswitch continue                after fixing the conflicts: finish the merge, rebase, cherry-pick
                                    or revert git stopped in the middle of (also: merge --continue)
  gitswitch skip [-y]               leave out the commit a rebase, cherry-pick or revert stopped at
  gitswitch abort [-y]              go back to how it was before the stopped operation
  gitswitch conflict [<path>] [--ours|--theirs|--done] [--json]   the files in conflict, or one
                                    file's base, ours and theirs; keep one side, or mark it fixed
                                    once its conflict markers are gone
  gitswitch fetch [--prune] [--json]   read the remote's new commits with the repo's account, so "to pull"
                                    and compare are up to date; no branch or file changes. --prune also
                                    forgets remote branches the server deleted (lists them); always: set fetch-prune on
  gitswitch update [--rebase]       pull the current branch with the repo's account: fast-forward only,
                                    or with --rebase put your not-pushed commits on top of the new ones
  gitswitch push [--open] [-y]      push the current branch with the repo's account, never forced
                                    (-u the first time); --open opens the pull request page. Commits
                                    made as another account are offered a fix first (-y: push anyway)
  gitswitch push --force-with-lease [-y]   replace the remote branch's commits with yours, after
                                    listing the ones that would be lost; only if nobody pushed since your
                                    last fetch (never a plain --force); asks first (-y: no question)
  gitswitch remote [list] [--json]  the remotes: URLs, the account each would use, which one the branch tracks
  gitswitch remote add <name> <url>   add a remote (SSH or HTTPS URL; says which account it would use)
  gitswitch remote remove <name> [--force] [-y]   remove one from this clone (refused while branches track it,
                                    unless --force; asks first); the server is not touched
  gitswitch remote rename <old> <new>   rename a remote; the branches that track it keep tracking
  gitswitch remote set-url <name> <url>   point a remote at another URL
  gitswitch branch delete-remote <remote>/<branch> [--force] [-y]   delete a branch on the server as the
                                    repo's account (asks first; not the default branch or the one you are
                                    on unless --force; prints how to restore it)
  gitswitch checkout <commit|tag|remote/branch> [--smart|--park] [--force]   look at a commit with HEAD on
                                    no branch ("detached"); changes are asked about like a switch;
                                    keep work by starting a branch here: gitswitch branch new <name>
  gitswitch back [--smart|--park] [--force]   leave a detached HEAD for the branch you came from
  gitswitch revert <commit>         add a commit that undoes one (safe for pushed commits)
  gitswitch drop <commit> [-y]      remove a not-pushed commit from the branch
  gitswitch rollback <commit> [-y]  undo the not-pushed commits after one, keeping their changes
  gitswitch cherry-pick <commit>... copy commits from other branches onto this one, oldest first
  gitswitch undo [-y] [--show [--json]]   undo the last commit, amend, switch, new branch, move, prune,
                                    branch delete or rename, revert, drop, rollback, cherry-pick or fix-author
                                    (--show: only say what it would do)
  gitswitch redo [-y] [--show [--json]]   do again what undo just took back (commit, amend, switch,
                                    branch delete or rename, merge, drop, ...) while nothing has changed since
  gitswitch show <commit> [--json]  a commit's full message and the files it changed
  gitswitch browse [<commit>]       open the commit (or the repo) on GitHub, GitLab, ...
  gitswitch tag [list] [--json]     the tags, newest first
  gitswitch tag new <name> [<commit>] [-m <message>]   tag the current commit (or another);
                                    annotated with -m, else lightweight
  gitswitch tag delete <name> [-y]  delete a tag in this clone (asks first)
  gitswitch tag push <name>         push one tag with the repo's account, never forced

Settings
  gitswitch doctor [--fix]
  gitswitch set gui-prompt on|off   native popup when git runs without a terminal
  gitswitch set prompt-timeout <seconds>
  gitswitch set editor <command>    editor for repo open --editor (e.g. code)
  gitswitch set repo-groups <how>   group the repo list: none, account, owner, folder or group
  gitswitch set repo-sort <how>     sort it: folder, name, recent or account (pinned first)
  gitswitch set pull <how> [--repo] how update works: ff-only (the default) or rebase
  gitswitch set fetch-on-open on|off   read the remote's new commits when History opens a repo (default: off)
  gitswitch set branch-prefix <prefix>   put e.g. feature/ in front of new branch names ("" for none)
  gitswitch set switch-changes ask|smart|park [--repo]   what switching branch does with
                                    uncommitted changes, in every repo or this one (default: ask;
                                    "global --repo": this repo follows every repo's again)
  gitswitch set signoff on|off [--repo]   add Signed-off-by to every commit, in every repo or
                                    this one (default: off; "global --repo" follows every repo's)
  gitswitch completions bash|zsh|fish|powershell

Add --dry-run to any command to see what it would change without changing anything.
`

// CLI runs commands against the app.
type CLI struct {
	app *bootstrap.App
	in  io.Reader // answers for `gitswitch setup`
	out io.Writer
}

// Run executes a command line and returns the exit code.
func Run(args []string) int {
	if len(args) > 0 && args[0] == "ssh" {
		return bootstrap.RunSSHHook(args[1:]) // called by git; must stay silent on stdout
	}
	dry := false
	var kept []string
	for _, a := range args {
		if a == "--dry-run" {
			dry = true
			continue
		}
		kept = append(kept, a)
	}
	x := &CLI{app: bootstrap.New(dry, os.Stdout), in: os.Stdin, out: os.Stdout}
	if err := x.dispatch(kept); err != nil {
		fmt.Fprintln(os.Stderr, "gitswitch:", err)
		writeHint(os.Stderr, err.Error(), "")
		return 1
	}
	return 0
}

// writeHint explains a git or ssh error in plain words below it, when it
// is one we know. id is the account involved, for the commands to suggest.
func writeHint(w io.Writer, msg, id string) {
	h := remote.Explain(msg)
	if h == nil {
		return
	}
	fmt.Fprintf(w, "\n  What happened: %s\n  What to do:    %s\n", h.Problem, h.Fix)
	switch {
	case h.Action == "pubkey" && id != "":
		fmt.Fprintf(w, "  Show the key and where to add it: gitswitch key show %s\n", id)
	case h.Action == "test" && id != "":
		fmt.Fprintf(w, "  Then test it: gitswitch test %s\n", id)
	}
}

func (x *CLI) say(a ...any)                { fmt.Fprintln(x.out, a...) }
func (x *CLI) sayf(f string, a ...any)     { fmt.Fprintf(x.out, f, a...) }
func (x *CLI) report() func(string)        { return func(m string) { x.say(m) } }
func usageErr(u string) error              { return errors.New("usage: " + u) }
func orNone(s string) string               { return map[bool]string{true: "(not set)", false: s}[s == ""] }
func firstLine(s string) string            { l, _, _ := strings.Cut(strings.TrimSpace(s), "\n"); return l }
func hasFlag(args []string, f string) bool { return indexOf(args, f) >= 0 }

func indexOf(args []string, f string) int {
	for i, a := range args {
		if a == f {
			return i
		}
	}
	return -1
}

func (x *CLI) dispatch(args []string) error {
	if len(args) == 0 {
		if !x.app.Setup.Installed() {
			return x.firstRun()
		}
		if interactive() {
			return tui.Run(x.app)
		}
		fmt.Fprint(x.out, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "install":
		if err := x.app.Setup.Install(x.report()); err != nil {
			return err
		}
		x.say("\nNext: `gitswitch setup` (terminal) or `gitswitch ui` (app) to add your accounts.")
		return nil
	case "uninstall":
		return x.app.Setup.Uninstall(hasFlag(rest, "--purge"), x.report())
	case "setup":
		return x.wizard()
	case "ui", "gui":
		return setup.OpenApp()
	case "tui":
		return tui.Run(x.app)
	case "account":
		return x.cmdAccount(rest)
	case "key":
		return x.cmdKey(rest)
	case "test":
		return x.cmdTest(rest)
	case "signin", "login":
		return x.cmdSignin(rest)
	case "github", "gh":
		return x.cmdGithub(rest)
	case "prs", "reviews":
		return x.cmdPrs(rest)
	case "credential": // git runs it as a credential helper
		return x.cmdCredential(rest)
	case "signout", "logout":
		return x.cmdSignout(rest)
	case "default":
		if len(rest) != 1 {
			return usageErr("gitswitch default <id> | --none")
		}
		if rest[0] == "--none" {
			if err := x.app.Accounts.SetDefault(""); err != nil {
				return err
			}
			x.say("No default account: a repo with no saved account or rule asks every time. Answer 2! (or tick \"always\") to save it for that repo.")
			return nil
		}
		if err := x.app.Accounts.SetDefault(rest[0]); err != nil {
			return err
		}
		x.say("Default account:", rest[0])
		return nil
	case "use":
		return x.cmdUse(rest)
	case "forget":
		gd, err := x.requireRepo()
		if err != nil {
			return err
		}
		if _, err := x.app.Accounts.SetRepoAccount(gd, ""); err != nil {
			return err
		}
		x.say("This repo has no saved account now: it follows your rules and default, or asks on the next pull or push.")
		return nil
	case "rule":
		return x.cmdRule(rest)
	case "repo", "repos":
		return x.cmdRepo(rest)
	case "status":
		return x.cmdStatus(hasFlag(rest, "--json"))
	case "log":
		return x.cmdLog(rest)
	case "blame":
		return x.cmdBlame(rest)
	case "fix-author":
		return x.cmdFixAuthor()
	case "branch", "branches":
		if len(rest) > 0 && rest[0] == "new" {
			return x.cmdBranchNew(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "prune" {
			return x.cmdBranchPrune(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "pr" {
			return x.cmdBranchPR(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "delete" {
			return x.cmdBranchDelete(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "delete-remote" {
			return x.cmdBranchDeleteRemote(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "rename" {
			return x.cmdBranchRename(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "upstream" {
			return x.cmdBranchUpstream(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "compare" {
			return x.cmdBranchCompare(rest[1:])
		}
		return x.cmdBranch(hasFlag(rest, "--json"))
	case "tag", "tags":
		return x.cmdTag(rest)
	case "commit":
		return x.cmdCommit(rest)
	case "changes":
		return x.cmdChanges(rest)
	case "diff":
		return x.cmdDiff(rest)
	case "stage":
		return x.cmdStage(rest)
	case "unstage":
		return x.cmdUnstage(rest)
	case "discard":
		return x.cmdDiscard(rest)
	case "hunks":
		return x.cmdHunks(rest)
	case "ignore":
		return x.cmdIgnore(rest)
	case "switch":
		return x.cmdSwitch(rest)
	case "checkout":
		return x.cmdCheckout(rest)
	case "back":
		return x.cmdBack(rest)
	case "remote", "remotes":
		return x.cmdRemote(rest)
	case "fetch":
		return x.cmdFetch(rest)
	case "update", "pull":
		return x.cmdUpdate(rest)
	case "push":
		return x.cmdPush(rest)
	case "revert":
		return x.cmdRevert(rest)
	case "drop":
		return x.cmdDrop(rest)
	case "rollback", "reset":
		return x.cmdRollback(rest)
	case "cherry-pick", "pick":
		return x.cmdCherryPick(rest)
	case "undo":
		return x.cmdUndo(rest)
	case "redo":
		return x.cmdRedo(rest)
	case "init":
		return x.cmdInit(rest)
	case "merge":
		return x.cmdMerge(rest)
	case "compare":
		return x.cmdCompare(rest)
	case "reflog":
		return x.cmdReflog(rest)
	case "bisect":
		return x.cmdBisect(rest)
	case "continue", "skip", "abort":
		return x.cmdOperation(cmd, rest)
	case "conflict", "conflicts":
		return x.cmdConflict(rest)
	case "resolve":
		return x.cmdResolve(rest)
	case "parked":
		return x.cmdParked(rest)
	case "stash":
		return x.cmdStash(rest)
	case "browse":
		return x.cmdBrowse(rest)
	case "show":
		return x.cmdShow(rest)
	case "doctor":
		return x.cmdDoctor(hasFlag(rest, "--fix"), hasFlag(rest, "--json"))
	case "set":
		return x.cmdSet(rest)
	case "completions", "completion":
		if len(rest) != 1 {
			return usageErr("gitswitch completions bash|zsh|fish|powershell")
		}
		return printCompletions(x.out, rest[0])
	case "__branches":
		x.branchNames()
		return nil
	case "__accounts":
		for _, id := range x.app.Accounts.Config().SortedIDs() {
			x.say(id)
		}
		return nil
	case "version", "--version", "-v":
		x.say("gitswitch", bootstrap.Version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(x.out, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
}

// firstRun is a bare `gitswitch` before git uses it. Installing changes the
// global git config, so it happens only when someone clearly asked: a
// double-click (Windows opens a console just for us), or a yes typed in a
// terminal. Scripts and pipes get told what to run instead.
func (x *CLI) firstRun() error {
	switch {
	case startedByDoubleClick():
	case interactive():
		if !confirm("GitSwitch is not connected to git yet. Connect it now? This changes your global git config (core.sshCommand).") {
			x.say("Not connected. Run `gitswitch install` when you are ready.")
			return tui.Run(x.app)
		}
	default:
		return errors.New("gitswitch is not connected to git yet; run `gitswitch install`, then `gitswitch setup`")
	}
	if err := x.app.Setup.Install(x.report()); err != nil {
		return err
	}
	if setup.AppInstalled() {
		return setup.OpenApp()
	}
	return tui.Run(x.app)
}

func (x *CLI) requireRepo() (string, error) {
	if gd := x.app.Repos.Current(); gd != "" {
		return gd, nil
	}
	return "", errors.New("not inside a git repo")
}

// interactive reports whether a person is at a terminal.
func interactive() bool {
	t := terminal.OpenTerminal()
	if t == nil {
		return false
	}
	t.Close()
	return true
}

// confirm asks a yes/no question on the terminal.
func confirm(q string) bool {
	t := terminal.OpenTerminal()
	if t == nil {
		return false
	}
	defer t.Close()
	fmt.Fprintf(t.Out, "%s [y/N]: ", q)
	line, _ := bufio.NewReader(t.In).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

// choose asks on the terminal for one of keys, the first letter of the
// answer; def for an empty or other answer, or without a terminal. bang
// reports an answer ending in "!" (e.g. "p!": and remember it).
func choose(q, def string, keys ...string) (key string, bang bool) {
	t := terminal.OpenTerminal()
	if t == nil {
		return def, false
	}
	defer t.Close()
	fmt.Fprintf(t.Out, "%s: ", q)
	line, _ := bufio.NewReader(t.In).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	bang = strings.HasSuffix(a, "!")
	if a = strings.TrimSuffix(a, "!"); a != "" {
		for _, k := range keys {
			if a[:1] == k {
				return k, bang
			}
		}
	}
	return def, bang
}

// splitArgs separates flags (with their values) from positional args.
func splitArgs(args []string) (pos, flags []string) {
	boolFlags := map[string]bool{"--default": true, "--once": true, "--copy": true, "--open": true, "--delete-keys": true, "--json": true, "--check": true}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && !boolFlags[args[i]] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, args[i])
	}
	return
}
