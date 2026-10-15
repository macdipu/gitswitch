package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	hostingapp "github.com/macdipu/gitswitch/internal/features/hosting/application"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

const githubUsage = "gitswitch github list [words] [--account ID] [--owner O] [--lang L] [--private|--public] [--cloned|--not-cloned] [--archived] [--refresh] [--json] | github clone <owner/name> [folder] [--account ID]"

// cmdGithub browses the repos every signed-in account can see on GitHub,
// and clones one with the right account.
func (x *CLI) cmdGithub(args []string) error {
	if len(args) == 0 {
		return usageErr(githubUsage)
	}
	var pos []string
	f := hosting.Filter{}
	refresh, asJSON := false, false
	accountID := ""
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		val := func() (string, error) { // --flag=value or --flag value
			if _, v, ok := strings.Cut(a, "="); ok {
				return v, nil
			}
			if i+1 >= len(rest) {
				return "", usageErr(githubUsage)
			}
			i++
			return rest[i], nil
		}
		var err error
		switch name, _, _ := strings.Cut(a, "="); name {
		case "--account":
			accountID, err = val()
		case "--owner":
			f.Owner, err = val()
		case "--lang", "--language":
			f.Language, err = val()
		case "--private", "--public":
			f.Visibility = strings.TrimPrefix(a, "--")
		case "--cloned":
			f.Cloned = "yes"
		case "--not-cloned":
			f.Cloned = "no"
		case "--archived":
			f.Archived = true
		case "--refresh":
			refresh = true
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(a, "--") {
				return fmt.Errorf("unknown option %s\nusage: %s", a, githubUsage)
			}
			pos = append(pos, a)
		}
		if err != nil {
			return err
		}
	}
	fetch := hostingapp.FetchStale
	if refresh {
		fetch = hostingapp.FetchAll
	}
	switch args[0] {
	case "list", "ls", "search":
		f.Account, f.Text = accountID, strings.Join(pos, " ")
		b := x.app.Hosting.Browse(fetch)
		if len(b.Accounts) == 0 {
			return errors.New("no account is signed in to GitHub; sign one in first: gitswitch signin <account>")
		}
		list := f.Apply(b.Repos)
		if asJSON {
			return json.NewEncoder(x.out).Encode(map[string]any{"accounts": b.Accounts, "repos": list})
		}
		for _, a := range b.Accounts {
			if a.Error != "" {
				x.sayf("%s: could not read its repos (%s); showing the last list.\n", a.Account, a.Error)
			}
		}
		for _, r := range list {
			vis := "public "
			if r.Private {
				vis = "private"
			}
			where := "not cloned"
			if len(r.Cloned) > 0 {
				where = "cloned at " + r.Cloned[0]
			}
			x.sayf("%-40s %s %-12s %-12s %s\n", r.FullName, vis, orDash(r.Language), r.Account, where)
		}
		x.sayf("%d of %d repos.", len(list), len(b.Repos))
		if !f.Archived {
			x.sayf(" Archived repos are hidden (--archived shows them).")
		}
		x.say()
		return nil
	case "clone":
		if len(pos) < 1 || len(pos) > 2 {
			return usageErr("gitswitch github clone <owner/name> [folder] [--account ID]")
		}
		r, ok := hosting.Find(x.app.Hosting.Browse(fetch).Repos, pos[0], accountID)
		if !ok {
			return fmt.Errorf("%s is not among the repos your signed-in accounts can see; try --refresh, or clone by URL: gitswitch repo clone <url>", pos[0])
		}
		if len(r.Cloned) > 0 {
			x.say("Already cloned at", strings.Join(r.Cloned, ", ")+"; cloning another copy.")
		}
		if accountID == "" {
			accountID = r.Account
		}
		dir := remote.RepoName(r.SSHURL)
		if len(pos) == 2 {
			dir = pos[1]
		}
		return x.cmdClone([]string{r.SSHURL, dir, "--account", accountID})
	}
	return fmt.Errorf("unknown github command %q\nusage: %s", args[0], githubUsage)
}
