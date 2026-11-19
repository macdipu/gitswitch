package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	repoapp "github.com/macdipu/gitswitch/internal/features/repository/application"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

const initUsage = "gitswitch init [<folder>] [--account <id>] [-b|--branch <name>] [--json]"

// cmdInit is `gitswitch init`: git init, the repo's identity from an
// account, and a place in the repo list.
func (x *CLI) cmdInit(args []string) error {
	var folder, accountID, branch string
	var asJSON bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", usageErr(initUsage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--account":
			accountID, err = val()
		case strings.HasPrefix(a, "--account="):
			accountID = strings.TrimPrefix(a, "--account=")
		case a == "-b" || a == "--branch":
			branch, err = val()
		case strings.HasPrefix(a, "--branch="):
			branch = strings.TrimPrefix(a, "--branch=")
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-") || folder != "":
			return usageErr(initUsage)
		default:
			folder = a
		}
		if err != nil {
			return err
		}
	}
	info, err := x.app.Repos.Init(folder, accountID, branch)
	if errors.Is(err, repoapp.ErrChooseAccount) {
		id, perr := x.pickAccount()
		if perr != nil {
			return perr
		}
		info, err = x.app.Repos.Init(folder, id, branch)
	}
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(info)
	}
	if x.app.DryRun() {
		return nil
	}
	x.say("Started a repo in", info.Path)
	if info.Account != "" {
		x.sayf("It uses %s (%s): commits are made as %s <%s>.\n", info.Account, info.Reason, info.UserName, info.Email)
	} else {
		x.say("No accounts yet, so no commit name or email is set. Add one with `gitswitch account add`, then run `gitswitch use <id>` here.")
	}
	return nil
}

// pickAccount asks on the terminal which account a new repo uses.
func (x *CLI) pickAccount() (string, error) {
	ids := x.app.Accounts.Config().Accounts
	t := terminal.OpenTerminal()
	if t == nil {
		return "", errors.New("no account decided for this repo: pass --account <id> (or set a default with `gitswitch default <id>`)")
	}
	defer t.Close()
	fmt.Fprintln(t.Out, "Which account does this repo use?")
	for i, a := range ids {
		fmt.Fprintf(t.Out, "  %d  %-14s %s\n", i+1, a.ID, a.Email)
	}
	fmt.Fprint(t.Out, "Number: ")
	line, _ := bufio.NewReader(t.In).ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(ids) {
		return "", errors.New("no account chosen")
	}
	return ids[n-1].ID, nil
}
