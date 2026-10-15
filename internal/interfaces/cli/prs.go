package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	hostingapp "github.com/macdipu/gitswitch/internal/features/hosting/application"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

const prsUsage = "gitswitch prs [--account ID] [--refresh] [--json]"

// cmdPrs lists each signed-in account's pull requests: waiting for its
// review, its own with what stands out, and ones it reviewed lately.
func (x *CLI) cmdPrs(args []string) error {
	accountID, refresh, asJSON := "", false, false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--account" && i+1 < len(args):
			accountID = args[i+1]
			i++
		case strings.HasPrefix(a, "--account="):
			accountID = strings.TrimPrefix(a, "--account=")
		case a == "--refresh":
			refresh = true
		case a == "--json":
			asJSON = true
		default:
			return usageErr(prsUsage)
		}
	}
	fetch := hostingapp.FetchStale
	if refresh {
		fetch = hostingapp.FetchAll
	}
	all := x.app.Hosting.Reviews(fetch)
	if len(all) == 0 {
		return errors.New("no account is signed in to GitHub; sign one in first: gitswitch signin <account>")
	}
	var inboxes []hosting.Inbox
	for _, in := range all {
		if accountID == "" || strings.EqualFold(in.Account, accountID) {
			inboxes = append(inboxes, in)
		}
	}
	if len(inboxes) == 0 {
		return fmt.Errorf("%s is not signed in to GitHub", accountID)
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(inboxes)
	}
	for i, in := range inboxes {
		if i > 0 {
			x.say()
		}
		x.sayf("%s (%s on %s)\n", in.Account, orDash(in.Login), in.Host)
		if in.Error != "" {
			x.sayf("  could not read it: %s%s\n", in.Error, map[bool]string{true: "; showing the last one", false: ""}[!in.Fetched.IsZero()])
		}
		x.prSection("Waiting for your review", in.ToReview, true)
		x.prSection("Your pull requests", in.Mine, false)
		x.prSection("Reviewed by you lately", in.Reviewed, true)
	}
	return nil
}

func (x *CLI) prSection(title string, prs []hosting.PullRequest, showAuthor bool) {
	x.sayf("  %s (%d)\n", title, len(prs))
	if len(prs) == 0 {
		x.say("    none")
		return
	}
	for _, p := range prs {
		line := fmt.Sprintf("    %s#%d  %s", p.Repo, p.Number, p.Title)
		if showAuthor {
			line += "  · " + p.Author
		}
		var chips []string
		for _, c := range p.Chips {
			if c.Bad {
				chips = append(chips, "! "+c.Text)
			} else {
				chips = append(chips, c.Text)
			}
		}
		if len(chips) > 0 {
			line += "  [" + strings.Join(chips, ", ") + "]"
		}
		x.say(line)
		x.say("      " + p.URL)
	}
}
