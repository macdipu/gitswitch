// Package cli is the command-line front end. It parses arguments, calls
// the app layer, and prints results. It holds no logic of its own.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/macdipu/gitswitch/internal/app"
	"github.com/macdipu/gitswitch/internal/sshhook"
	"github.com/macdipu/gitswitch/internal/sys"
	"github.com/macdipu/gitswitch/internal/ui/tui"
	"github.com/macdipu/gitswitch/internal/ui/web"
)

const usage = `gitswitch - use several git accounts on one computer

Setup
  gitswitch install                 one-time: hook gitswitch into git for every terminal and IDE
  gitswitch setup                   step-by-step setup in the terminal
  gitswitch                         open the app in this terminal (same as: gitswitch tui)
  gitswitch ui                      open the app in your browser
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
  gitswitch default <id>            account used when nothing else decides

Repos and rules
  gitswitch use <id> [--once]       set this repo's account (and its commit name/email)
  gitswitch forget                  make this repo ask again
  gitswitch rule add owner:<org> <id>     e.g. owner:acme-corp client-acme
  gitswitch rule add folder:<path> <id>   e.g. folder:~/work polygon
  gitswitch rule list | remove <n>
  gitswitch status [--json]
  gitswitch log [-n 40] [--flagged] commit graph with the account of each commit
  gitswitch fix-author              fix not-pushed commits made with the wrong account
  gitswitch doctor [--fix]
  gitswitch set gui-prompt on|off   native popup when git runs without a terminal
  gitswitch set prompt-timeout <seconds>
  gitswitch completions bash|zsh|fish|powershell

Add --dry-run to any command to see what it would change without changing anything.
`

// CLI runs commands against the app.
type CLI struct {
	app *app.App
	out io.Writer
}

// Run executes a command line and returns the exit code.
func Run(args []string) int {
	if len(args) > 0 && args[0] == "ssh" {
		return sshhook.Run(args[1:]) // called by git; must stay silent on stdout
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
	x := &CLI{app: app.New(dry, os.Stdout), out: os.Stdout}
	if err := x.dispatch(kept); err != nil {
		fmt.Fprintln(os.Stderr, "gitswitch:", err)
		return 1
	}
	return 0
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
		// First run by double-click: install, then open the app.
		if !x.app.Installed() {
			if err := x.app.Install(x.report()); err != nil {
				return err
			}
			return web.Run(x.app, false)
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
		if err := x.app.Install(x.report()); err != nil {
			return err
		}
		x.say("\nNext: `gitswitch setup` (terminal) or `gitswitch ui` (app) to add your accounts.")
		return nil
	case "uninstall":
		return x.app.Uninstall(hasFlag(rest, "--purge"), x.report())
	case "setup":
		return x.wizard()
	case "ui", "web":
		return web.Run(x.app, hasFlag(rest, "--no-open"))
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
			return usageErr("gitswitch default <id>")
		}
		if err := x.app.SetDefault(rest[0]); err != nil {
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
		if _, err := x.app.SetRepoAccount(gd, ""); err != nil {
			return err
		}
		x.say("This repo will ask again on the next pull or push.")
		return nil
	case "rule":
		return x.cmdRule(rest)
	case "status":
		return x.cmdStatus(hasFlag(rest, "--json"))
	case "log":
		return x.cmdLog(rest)
	case "fix-author":
		return x.cmdFixAuthor()
	case "doctor":
		return x.cmdDoctor(hasFlag(rest, "--fix"), hasFlag(rest, "--json"))
	case "set":
		return x.cmdSet(rest)
	case "completions", "completion":
		if len(rest) != 1 {
			return usageErr("gitswitch completions bash|zsh|fish|powershell")
		}
		return printCompletions(x.out, rest[0])
	case "__accounts":
		for _, id := range x.app.Config().SortedIDs() {
			x.say(id)
		}
		return nil
	case "version", "--version", "-v":
		x.say("gitswitch", app.Version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(x.out, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
}

func (x *CLI) requireRepo() (string, error) {
	if gd := x.app.CurrentRepo(); gd != "" {
		return gd, nil
	}
	return "", errors.New("not inside a git repo")
}

// interactive reports whether a person is at a terminal.
func interactive() bool {
	t := sys.OpenTerminal()
	if t == nil {
		return false
	}
	t.Close()
	return true
}

// confirm asks a yes/no question on the terminal.
func confirm(q string) bool {
	t := sys.OpenTerminal()
	if t == nil {
		return false
	}
	defer t.Close()
	fmt.Fprintf(t.Out, "%s [y/N]: ", q)
	line, _ := bufio.NewReader(t.In).ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
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
