package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
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

	x.say("GitSwitch setup. Press Enter to accept the value in [brackets].")
	if !x.app.Setup.Installed() {
		x.say("\nStep 1: connect gitswitch to git (one time).")
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
		if d := ask("Default account (used when a repo has no saved choice and nobody can be asked)", c.Default); d != "" {
			if err := x.app.Accounts.SetDefault(d); err != nil {
				x.say("Error:", err)
			}
		}
		x.say("\nRules pick an account automatically, so most repos never ask.")
		for yes("Add a rule?", false) {
			kind := ask("1 = by GitHub owner/org, 2 = by folder on this computer", "1")
			acct := ask("Account for it", c.Default)
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
	x.say("\nDone. Clone with normal URLs (git@github.com:owner/repo.git); gitswitch picks the account.")
	x.say("Check anytime with `gitswitch status`, or open the app with `gitswitch ui`.")
	return nil
}
