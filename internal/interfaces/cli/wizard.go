package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repoapp "github.com/macdipu/gitswitch/internal/features/repository/application"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// wizard is `gitswitch setup`: step-by-step questions in the terminal.
func (x *CLI) wizard() error {
	in := bufio.NewReader(x.in)
	ended := false // input is closed (Ctrl+D, or a pipe ran out): stop asking
	ask := func(q, def string) string {
		if def != "" {
			fmt.Fprintf(x.out, "%s [%s]: ", q, def)
		} else {
			fmt.Fprintf(x.out, "%s: ", q)
		}
		s, err := in.ReadString('\n')
		if err != nil {
			ended = true
			fmt.Fprintln(x.out)
		}
		if s = strings.TrimSpace(s); s == "" {
			return def
		}
		return s
	}
	yes := func(q string, def bool) bool {
		d := map[bool]string{true: "Y/n", false: "y/N"}[def]
		s := strings.ToLower(ask(q+" ("+d+")", ""))
		if s == "" {
			return def
		}
		return strings.HasPrefix(s, "y")
	}

	if g := x.app.Repos.GitInfo(); !g.OK {
		return errors.New(g.Why)
	}
	x.say("GitSwitch setup. Press Enter to accept the value in [brackets].")
	if !x.app.Setup.Installed() {
		x.say("\nStep 1: connect gitswitch to git (one time).")
		x.say("Git will start gitswitch for a moment whenever it talks to a server, so it can pick the account.")
		x.say("This sets core.sshCommand in your global git config; undo it any time with `gitswitch uninstall`.")
		if err := x.app.Setup.Install(x.report()); err != nil {
			return err
		}
	}
	defName := x.app.Accounts.SuggestedName()
	for {
		c := x.app.Accounts.Config()
		if ended {
			if len(c.Accounts) > 0 {
				break
			}
			return errors.New("setup stopped: no more input. Run `gitswitch setup` in a terminal and type the answers, or add accounts with `gitswitch account add`")
		}
		x.sayf("\nAdd an account (%d so far).\n", len(c.Accounts))
		id := ask("Account short name, e.g. work, personal, client-acme", "")
		if id == "" {
			if len(c.Accounts) > 0 {
				break
			}
			continue
		}
		if !account.ValidID(id) {
			x.say("Use letters, digits, - or _.")
			continue
		}
		acc := account.Account{ID: id, Host: ask("Git host", "github.com"), Name: ask("Commit name", defName)}
		defName = acc.Name
		acc.Email = ask("Commit email", "")
		choice := ask("Key: 1 = generate a new key, 2 = use an existing key file", "1")
		if choice == "2" {
			acc.Key = ask("Path to the private key", "~/.ssh/id_ed25519")
		} else {
			x.say("ssh-keygen will ask for a passphrase (recommended). gitswitch never stores it.")
		}
		if ended { // do not save a half-answered account
			continue
		}
		acc, err := x.app.Accounts.Add(acc, account.KeyChoice{New: choice != "2", Interactive: true})
		if err != nil {
			x.say("Error:", err)
			continue
		}
		_ = x.showKey(acc.ID, false)
		if _, help, _ := x.app.Keys.PublicKey(acc.ID); help.Page != "" && yes("Open that page in your browser now?", true) {
			_ = process.OpenBrowser(help.Page)
		}
		ask("Press Enter after you have added the key", "")
		if r, _ := x.app.Keys.Test(acc.ID, false); r.OK {
			x.sayf("Connected: %s is %s on %s.\n", acc.ID, r.User, acc.Host)
		} else {
			x.say("Could not log in yet:", firstLine(r.Output))
			writeHint(x.out, r.Output, acc.ID)
			x.say("You can retry later with: gitswitch test", acc.ID)
		}
		if !yes("Add another account?", len(c.Accounts) == 0) {
			break
		}
	}
	c := x.app.Accounts.Config()
	if len(c.Accounts) > 1 {
		x.say("\nAccounts:", strings.Join(c.SortedIDs(), ", "))
		x.say("A default account is optional: it is used only when nothing else decides and nobody answers.")
		x.say("Without one, GitSwitch asks (answer 2! to save it for the repo) and never guesses.")
		prompt := "Default account, or Enter for none"
		if c.Default != "" {
			prompt = "Default account (none to remove it)"
		}
		if d := ask(prompt, c.Default); d != "" && !strings.EqualFold(d, c.Default) {
			if strings.EqualFold(d, "none") {
				d = ""
			}
			if err := x.app.Accounts.SetDefault(d); err != nil {
				x.say("Error:", err)
			}
		}
		c = x.app.Accounts.Config()
		x.say("\nRules pick an account automatically, so most repos never ask.")
		for yes("Add a rule?", false) {
			kind := ask("1 = by GitHub owner/org, 2 = by folder on this computer", "1")
			acct := ask("Account for it ("+strings.Join(c.SortedIDs(), ", ")+")", c.Default)
			if acct == "" {
				x.say("Type one of the account names.")
				continue
			}
			r := account.Rule{Kind: "owner", Account: acct}
			if kind == "2" {
				r.Kind, r.Pattern = "folder", ask("Folder, e.g. ~/work", "")
			} else {
				r.Pattern = ask("Owner or org name, e.g. acme-corp", "")
			}
			if err := x.app.Accounts.AddRule(r); err != nil {
				x.say("Error:", err)
			} else {
				x.say("Rule added.")
			}
		}
	}
	if cfg := x.app.Accounts.Config(); len(cfg.ScanFolders) == 0 && !ended {
		x.say("\nGitSwitch can list the repos in the folder where you keep your projects (it only reads it).")
		for {
			p := ask("Projects folder, or Enter to skip", "")
			if p == "" || ended {
				break
			}
			if err := x.app.Accounts.AddScanFolder(p); err != nil {
				x.say("Error:", err)
				continue
			}
			x.say("Added.")
			break
		}
	}
	if !ended && yes("\nStart a new repo now?", false) {
		x.wizardInit(ask)
	}
	x.say("\nDone. Clone with normal URLs (git@github.com:owner/repo.git); gitswitch picks the account.")
	x.say("Check anytime with `gitswitch status`, or open the app with `gitswitch ui`.")
	return nil
}

// wizardInit is the optional last step: git init in a folder, with the
// account the rules decide or the one chosen. A mistake is reported and
// does not stop the setup.
func (x *CLI) wizardInit(ask func(q, def string) string) {
	folder := ask("Folder for the new repo (made when it does not exist)", ".")
	branch := ask("First branch name, or Enter for git's default", "")
	info, err := x.app.Repos.Init(folder, "", branch)
	if errors.Is(err, repoapp.ErrChooseAccount) {
		accs := x.app.Accounts.Config().Accounts
		x.say("Which account does this repo use?")
		for i, a := range accs {
			x.sayf("  %d  %-14s %s\n", i+1, a.ID, a.Email)
		}
		n, _ := strconv.Atoi(ask("Number", ""))
		if n < 1 || n > len(accs) {
			x.say("No account chosen, so no repo was started. Later: gitswitch init <folder> --account <id>")
			return
		}
		info, err = x.app.Repos.Init(folder, accs[n-1].ID, branch)
	}
	if err != nil {
		x.say("Error:", err)
		return
	}
	x.say("Started a repo in", info.Path)
	if info.Account != "" {
		x.sayf("It uses %s (%s): commits are made as %s <%s>.\n", info.Account, info.Reason, info.UserName, info.Email)
	}
}
