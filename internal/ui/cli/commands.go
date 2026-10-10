package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/app"
	"github.com/macdipu/gitswitch/internal/sys"
)

func (x *CLI) cmdAccount(args []string) error {
	if len(args) == 0 {
		return usageErr("gitswitch account add|edit|list|remove|rename")
	}
	sub, args := args[0], args[1:]
	pos, fl := splitArgs(args)
	fs := flag.NewFlagSet("account", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "")
	name := fs.String("name", "", "")
	host := fs.String("host", "", "")
	key := fs.String("key", "", "")
	def := fs.Bool("default", false, "")
	delKeys := fs.Bool("delete-keys", false, "")
	if err := fs.Parse(fl); err != nil {
		return err
	}
	c := x.app.Config()
	switch sub {
	case "list", "ls":
		if len(c.Accounts) == 0 {
			x.say("No accounts yet. Add one: gitswitch account add <id> --email you@example.com --key new")
			return nil
		}
		for _, a := range c.Accounts {
			mark := " "
			if strings.EqualFold(a.ID, c.Default) {
				mark = "*"
			}
			x.sayf("%s %-14s %-28s %-14s %s\n", mark, a.ID, a.Email, a.Host, a.Key)
		}
		x.say("(* = default)")
		return nil

	case "add":
		if len(pos) != 1 {
			return usageErr("gitswitch account add <id> --email E [--name N] [--host H] [--key PATH|new]")
		}
		acc := account.Account{ID: pos[0], Email: *email, Name: *name, Host: *host}
		if *key != "new" {
			acc.Key = *key
		}
		acc, err := x.app.AddAccount(acc, app.KeyChoice{New: *key == "new", Interactive: interactive()})
		if err != nil {
			return err
		}
		if *def {
			_ = x.app.SetDefault(acc.ID)
		}
		x.say("Added account", acc.ID)
		if acc.Key != "" && !x.app.DryRun() {
			if err := x.showKey(acc.ID, false); err != nil {
				x.say("Note:", err)
			}
		}
		return nil

	case "edit":
		if len(pos) != 1 {
			return usageErr("gitswitch account edit <id> [--email E] [--name N] [--host H] [--key PATH]")
		}
		a, err := x.app.Account(pos[0])
		if err != nil {
			return err
		}
		upd := *a
		for _, f := range []struct {
			dst *string
			v   string
		}{{&upd.Email, *email}, {&upd.Name, *name}, {&upd.Host, *host}, {&upd.Key, *key}} {
			if f.v != "" {
				*f.dst = f.v
			}
		}
		if err := x.app.UpdateAccount(upd); err != nil {
			return err
		}
		if *def {
			_ = x.app.SetDefault(upd.ID)
		}
		x.say("Updated", upd.ID)
		return nil

	case "remove", "rm":
		yes := indexOf(pos, "-y") >= 0
		var ids []string
		for _, p := range pos {
			if p != "-y" {
				ids = append(ids, p)
			}
		}
		if len(ids) != 1 {
			return usageErr("gitswitch account remove <id> [--delete-keys] [-y]")
		}
		a, err := x.app.Account(ids[0])
		if err != nil {
			return err
		}
		if !yes && !x.app.DryRun() {
			q := "Remove account " + a.ID + " and its rules?"
			if *delKeys {
				q = "Remove account " + a.ID + ", its rules, AND delete its key files " + a.Key + "(.pub)?"
			}
			if !interactive() {
				return errors.New("add -y to confirm when not running in a terminal")
			}
			if !confirm(q) {
				x.say("Cancelled.")
				return nil
			}
		}
		host := a.Host
		deleted, err := x.app.RemoveAccount(a.ID, *delKeys)
		if err != nil {
			return err
		}
		for _, p := range deleted {
			x.say("Deleted", p)
		}
		if *delKeys {
			x.say("Removed", ids[0]+". Remember to delete its public key on", host, "too.")
		} else {
			x.say("Removed", ids[0], "(key files kept; add --delete-keys to delete them)")
		}
		return nil

	case "rename", "mv":
		if len(pos) != 2 {
			return usageErr("gitswitch account rename <old> <new>")
		}
		n, err := x.app.RenameAccount(pos[0], pos[1])
		if err != nil {
			return err
		}
		x.sayf("Renamed %s to %s (updated rules, default and %d repo(s) in your project folders).\n", pos[0], pos[1], n)
		return nil
	}
	return fmt.Errorf("unknown: account %s", sub)
}

func (x *CLI) showKey(id string, open bool) error {
	pub, page, err := x.app.PublicKey(id)
	if err != nil {
		return err
	}
	a, _ := x.app.Account(id)
	x.sayf("\nPublic key for %s (add it to %s, signed in as that account):\n\n%s\n\n", id, a.Host, pub)
	if sys.CopyToClipboard(pub) {
		x.say("Copied to clipboard.")
	}
	if page != "" {
		x.say("Add it here:", page)
		if open {
			_ = sys.OpenBrowser(page)
		}
	}
	x.say("Then check it with: gitswitch test", id)
	return nil
}

func (x *CLI) cmdKey(args []string) error {
	pos, fl := splitArgs(args)
	if len(pos) != 2 {
		return usageErr("gitswitch key new|show|pick|agent <id>")
	}
	id := pos[1]
	if _, err := x.app.Account(id); err != nil {
		return err
	}
	switch pos[0] {
	case "new":
		if _, err := x.app.GenerateKey(id, true, ""); err != nil {
			return err
		}
		return x.showKey(id, false)
	case "show":
		return x.showKey(id, hasFlag(fl, "--open"))
	case "agent":
		return x.app.AddToAgent(id)
	case "pick":
		ks := x.app.ExistingKeys()
		if len(ks) == 0 {
			return errors.New("no key pairs found in ~/.ssh; create one with `gitswitch key new " + id + "`")
		}
		for i, k := range ks {
			used := ""
			if k.UsedBy != "" {
				used = "  (used by " + k.UsedBy + ")"
			}
			x.sayf("  %d  %s  %s%s\n", i+1, k.Path, k.Comment, used)
		}
		fmt.Fprint(x.out, "Key number for "+id+": ")
		var n int
		if _, err := fmt.Scan(&n); err != nil || n < 1 || n > len(ks) {
			return errors.New("no key chosen")
		}
		if err := x.app.UseKey(id, ks[n-1].Path); err != nil {
			return err
		}
		x.say(id, "now uses", ks[n-1].Path)
		return nil
	}
	return fmt.Errorf("unknown: key %s", pos[0])
}

func (x *CLI) cmdTest(args []string) error {
	c := x.app.Config()
	if len(c.Accounts) == 0 {
		return errors.New("no accounts yet")
	}
	allOK := true
	for _, a := range c.Accounts {
		if len(args) == 1 && !strings.EqualFold(a.ID, args[0]) {
			continue
		}
		r, err := x.app.TestAccount(a.ID, !interactive())
		if err != nil {
			return err
		}
		if r.OK {
			x.sayf("ok    %-14s logged in to %s as %s\n", a.ID, a.Host, r.User)
		} else {
			allOK = false
			x.sayf("FAIL  %-14s %s\n", a.ID, firstLine(r.Output))
		}
	}
	if !allOK {
		return errors.New("some accounts could not log in; add the public key with `gitswitch key show <id>`")
	}
	return nil
}

func (x *CLI) cmdUse(args []string) error {
	pos, fl := splitArgs(args)
	if len(pos) != 1 {
		return usageErr("gitswitch use <id> [--once]")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	a, err := x.app.Account(pos[0])
	if err != nil {
		return err
	}
	if hasFlag(fl, "--once") {
		if err := x.app.UseOnce(gd, a.ID); err != nil {
			return err
		}
	} else {
		removed, err := x.app.SetRepoAccount(gd, a.ID)
		if err != nil {
			return err
		}
		if removed != "" {
			x.say("Removed this repo's own core.sshCommand (" + removed + "), which was bypassing gitswitch.")
		}
	}
	x.sayf("This repo now uses %s (%s).\n", a.ID, a.Email)
	return nil
}

func (x *CLI) cmdRule(args []string) error {
	c := x.app.Config()
	if len(args) == 0 || args[0] == "list" {
		if len(c.Rules) == 0 {
			x.say("No rules. Example: gitswitch rule add owner:acme-corp client-acme")
		}
		for i, r := range c.Rules {
			x.sayf("%d  %-7s %-40s -> %s\n", i+1, r.Kind, r.Pattern, r.Account)
		}
		return nil
	}
	switch args[0] {
	case "add":
		if len(args) != 3 {
			return usageErr("gitswitch rule add owner:<org>|folder:<path> <id>")
		}
		kind, pat, ok := strings.Cut(args[1], ":")
		if !ok {
			return errors.New("rule must start with owner: or folder:")
		}
		if err := x.app.AddRule(account.Rule{Kind: kind, Pattern: pat, Account: args[2]}); err != nil {
			return err
		}
		x.say("Rule added.")
		return nil
	case "remove", "rm":
		var n int
		if len(args) != 2 {
			return usageErr("gitswitch rule remove <number>")
		}
		if _, err := fmt.Sscan(args[1], &n); err != nil {
			return errors.New("give the rule number from `gitswitch rule list`")
		}
		return x.app.RemoveRuleAt(n - 1)
	}
	return fmt.Errorf("unknown: rule %s", args[0])
}

func (x *CLI) cmdStatus(asJSON bool) error {
	c := x.app.Config()
	var info any
	gd := x.app.CurrentRepo()
	if asJSON {
		if gd != "" {
			info = x.app.RepoInfo(gd)
		}
		return json.NewEncoder(x.out).Encode(map[string]any{"config": c, "repo": info, "installed": x.app.Installed()})
	}
	x.say("Hook:    ", orNone(x.app.Hook()))
	x.say("Default: ", orNone(c.Default))
	x.sayf("Accounts: %d   Rules: %d   GUI prompt: %v\n", len(c.Accounts), len(c.Rules), c.GUIPrompt)
	for _, v := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if os.Getenv(v) != "" {
			x.sayf("warning: %s is set, so git ignores gitswitch in this terminal\n", v)
		}
	}
	if loaded, ok := x.app.AgentStatus(); ok {
		for _, a := range c.Accounts {
			state := "not loaded"
			if loaded[a.ID] {
				state = "loaded"
			}
			x.sayf("  %-14s key in ssh-agent: %s\n", a.ID, state)
		}
	} else {
		x.say("  ssh-agent: not running")
	}
	if gd == "" {
		return nil
	}
	r := x.app.RepoInfo(gd)
	x.say("\nThis repo:", r.Path)
	x.say("  remote: ", orNone(r.Remote))
	if r.Account != "" {
		x.sayf("  account: %s (%s)\n", r.Account, r.Reason)
	} else {
		x.say("  account: none, will ask on next pull/push")
	}
	x.say("  email:  ", orNone(r.Email))
	if r.SSHOverride != "" {
		x.say("  warning: this repo sets its own core.sshCommand, so gitswitch is NOT used here.")
		x.say("           Fix: gitswitch doctor --fix (inside this repo)")
	}
	if r.EmailBad {
		x.say("  warning: repo email does not match the account; run `gitswitch use", r.Account+"`")
	}
	return nil
}

func (x *CLI) cmdLog(args []string) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	n := 40
	if i := indexOf(args, "-n"); i >= 0 && i+1 < len(args) {
		fmt.Sscan(args[i+1], &n)
	}
	flagged := hasFlag(args, "--flagged")
	lines, h, err := x.app.HistoryLines(gd, n, flagged, os.Getenv("NO_COLOR") == "")
	if err != nil {
		return err
	}
	acct := h.Repo.Account
	if acct == "" {
		acct = "not set (" + h.Repo.Reason + ")"
	}
	x.sayf("%s  account: %s\n", h.Repo.Path, acct)
	for _, l := range lines {
		x.say(l)
	}
	if h.Flagged > 0 {
		if flagged {
			x.say("Fix the not-pushed ones with: gitswitch fix-author")
		} else {
			x.sayf("\n%d commit(s) use another account's email. See them: gitswitch log --flagged\n", h.Flagged)
		}
	}
	return nil
}

func (x *CLI) cmdFixAuthor() error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	n, err := x.app.FixAuthor(gd)
	if err != nil {
		return err
	}
	if n == 0 {
		x.say("Nothing to fix: every not-pushed commit already uses this repo's account.")
	} else if !x.app.DryRun() {
		x.sayf("Fixed the author of %d not-pushed commit(s).\n", n)
	}
	return nil
}

func (x *CLI) cmdDoctor(fix, asJSON bool) error {
	cs := x.app.Checks(true)
	if fix {
		for _, ch := range cs {
			if ch.CanFix {
				if err := x.app.Fix(ch.ID); err != nil {
					x.say("could not fix", ch.ID+":", err)
				}
			}
		}
		cs = x.app.Checks(true)
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(cs)
	}
	bad := 0
	for _, ch := range cs {
		mark := "ok  "
		if !ch.OK {
			mark = "FIX "
			bad++
		}
		x.sayf("%s %s\n      %s\n", mark, ch.Title, ch.Detail)
	}
	if bad > 0 && !fix {
		x.say("\nRun `gitswitch doctor --fix` to fix what can be fixed automatically.")
	}
	return nil
}

func (x *CLI) cmdSet(args []string) error {
	if len(args) != 2 {
		return usageErr("gitswitch set gui-prompt on|off | prompt-timeout <seconds> | ssh-path <path>")
	}
	switch args[0] {
	case "gui-prompt":
		return x.app.SetSetting("guiPrompt", fmt.Sprint(account.ParseBool(args[1], true)))
	case "prompt-timeout":
		return x.app.SetSetting("promptTimeout", args[1])
	case "ssh-path":
		return x.app.SetSetting("sshPath", args[1])
	}
	return fmt.Errorf("unknown setting %q", args[0])
}
