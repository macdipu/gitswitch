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
  gitswitch account list
  gitswitch account remove <id> [--delete-keys] [-y]
  gitswitch account rename <old> <new>
  gitswitch key new <id>            generate an ed25519 key (ssh-keygen asks for a passphrase)
  gitswitch key pick <id>           choose one of your existing keys in ~/.ssh
  gitswitch key show <id> [--open]
  gitswitch key agent <id>          load the key into the OS agent / Keychain once
  gitswitch test [<id>]             check each key logs in, and as whom
  gitswitch default <id>            optional: account used when nobody answers (or Enter)
  gitswitch default --none          remove the default: repos with nothing saved always ask

Repos and rules
  gitswitch use <id> [--once]       set this repo's account (and its commit name/email)
  gitswitch use --ask               this repo asks on every pull/push and never remembers
  gitswitch forget                  remove this repo's account: follow rules and the default
  gitswitch rule add owner:<org> <id>     e.g. owner:acme-corp client-acme
  gitswitch rule add folder:<path> <id>   e.g. folder:~/work polygon
  gitswitch rule list | remove owner:<org>|folder:<path>|<n>
  gitswitch repo list [--pinned] [--json]   repos in your project folders plus ones you added
  gitswitch repo clone <url> [folder] [--account <id>]   clone with the right account from the start
  gitswitch repo add [path]         add an existing repo to the list (nothing in it changes)
  gitswitch repo remove [path]      take a repo off the list (its files are not touched)
  gitswitch repo show [path] [--json]     folder, branch, remotes, account
  gitswitch repo open [path] [--editor]   open in the file manager or your editor
  gitswitch repo pin|unpin [path]   keep a repo at the top of the list
  gitswitch status [--json]
  gitswitch fix-author              fix not-pushed commits made with the wrong account

History and branches
  gitswitch log [-n 40] [--flagged] commit graph with the account of each commit
  gitswitch log [--author TEXT] [--grep TEXT] [--since DATE] [--until DATE] [--account ID]
                                    only matching commits (author name or email, message, dates
                                    like 2024-05-01 or "2 weeks ago", gitswitch account)
  gitswitch branch [--json]         current branch, HEAD and the branch list
  gitswitch branch new <name> [--from <commit>] [--no-prefix]   start a branch here (or at a
                                    commit, branch or tag) and switch to it, bringing uncommitted
                                    changes; the branch-prefix setting goes in front of the name
  gitswitch switch <branch> [--smart|--park]   check out a branch (origin/x creates a local x);
                                    with uncommitted changes, --smart takes them along and --park
                                    leaves them on this branch until you come back; without either,
                                    the switch-changes setting decides, else it asks in a terminal
  gitswitch parked [--json]         the parked changes (git stash), newest first
  gitswitch parked show|restore|drop [<n>] [-y]   look at, restore or delete parked changes
                                    (<n> from the list; else the newest parked on this branch)
  gitswitch resolve [--done|--abort|--park]   after a smart switch with clashing files: finish once
                                    they are fixed, go back exactly as before, or park them instead
  gitswitch update                  pull the current branch, fast-forward only, with the repo's account
  gitswitch push [--open] [-y]      push the current branch with the repo's account, never forced
                                    (-u the first time); --open opens the pull request page. Commits
                                    made as another account are offered a fix first (-y: push anyway)
  gitswitch revert <commit>         add a commit that undoes one (safe for pushed commits)
  gitswitch drop <commit> [-y]      remove a not-pushed commit from the branch
  gitswitch rollback <commit> [-y]  undo the not-pushed commits after one, keeping their changes
  gitswitch cherry-pick <commit>... copy commits from other branches onto this one, oldest first
  gitswitch undo [-y] [--show [--json]]   undo the last switch, revert, drop, rollback,
                                    cherry-pick or fix-author (--show: only say what it would do)
  gitswitch show <commit> [--json]  a commit's full message and the files it changed
  gitswitch browse [<commit>]       open the commit (or the repo) on GitHub, GitLab, ...

Settings
  gitswitch doctor [--fix]
  gitswitch set gui-prompt on|off   native popup when git runs without a terminal
  gitswitch set prompt-timeout <seconds>
  gitswitch set editor <command>    editor for repo open --editor (e.g. code)
  gitswitch set branch-prefix <prefix>   put e.g. feature/ in front of new branch names ("" for none)
  gitswitch set switch-changes ask|smart|park [--repo]   what switching branch does with
                                    uncommitted changes, in every repo or this one (default: ask;
                                    "global --repo": this repo follows every repo's again)
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
	case "fix-author":
		return x.cmdFixAuthor()
	case "branch", "branches":
		if len(rest) > 0 && rest[0] == "new" {
			return x.cmdBranchNew(rest[1:])
		}
		return x.cmdBranch(hasFlag(rest, "--json"))
	case "switch":
		return x.cmdSwitch(rest)
	case "update", "pull":
		return x.cmdUpdate()
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
	case "resolve":
		return x.cmdResolve(rest)
	case "parked":
		return x.cmdParked(rest)
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
	boolFlags := map[string]bool{"--default": true, "--once": true, "--copy": true, "--open": true, "--delete-keys": true, "--json": true}
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
