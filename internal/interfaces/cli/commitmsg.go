package cli

import (
	"encoding/json"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// commitExtras are commit's flags for the message: sign-off, co-authors,
// and listing the recent messages.
type commitExtras struct {
	signoff   *bool
	coAuthors []string
	recent    bool
	asJSON    bool
}

// parse reads the flag at args[i]; n is how many more arguments it took.
func (e *commitExtras) parse(args []string, i int) (n int, ok bool) {
	a := args[i]
	on, off := true, false
	switch {
	case a == "--signoff" || a == "-s":
		e.signoff = &on
	case a == "--no-signoff":
		e.signoff = &off
	case a == "--co-author" && i+1 < len(args):
		e.coAuthors = append(e.coAuthors, args[i+1])
		return 1, true
	case strings.HasPrefix(a, "--co-author="):
		e.coAuthors = append(e.coAuthors, strings.TrimPrefix(a, "--co-author="))
	case a == "--recent":
		e.recent = true
	case a == "--json":
		e.asJSON = true
	default:
		return 0, false
	}
	return 0, true
}

// changes reports flags that change the commit's message.
func (e *commitExtras) changes() bool { return e.signoff != nil || len(e.coAuthors) > 0 }

func (e *commitExtras) options() workspace.CommitOptions {
	return workspace.CommitOptions{Signoff: e.signoff, CoAuthors: e.coAuthors}
}

// commitRecent lists the messages you wrote in this repo lately, and the
// message of the last commit that failed.
func (x *CLI) commitRecent(asJSON bool) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	info, err := x.app.Workspace.CommitInfo(gd, 0)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(map[string]any{"recent": info.Recent, "draft": info.Draft})
	}
	if info.Draft != "" {
		x.say("Kept from the commit that failed (gitswitch commit -m with it again):")
		for _, l := range strings.Split(info.Draft, "\n") {
			x.say("  " + history.CleanText(l))
		}
		x.say()
	}
	if len(info.Recent) == 0 {
		who := info.Identity.Email
		if who == "" {
			who = "this repo's email"
		}
		x.sayf("No commits by %s in this repo yet.\n", history.CleanText(who))
		return nil
	}
	x.sayf("Your recent messages (%s):\n", history.CleanText(info.Identity.Email))
	for _, r := range info.Recent {
		x.sayf("  %-14s %s\n", ago(r.Time.Unix()), history.CleanText(r.Subject))
	}
	return nil
}

// setSignoff saves whether commits get a Signed-off-by trailer, for every
// repo or (--repo) this one, where "global" follows every repo's.
func (x *CLI) setSignoff(args []string) error {
	const usage = "gitswitch set signoff on|off [--repo] | signoff global --repo"
	var value string
	repo := false
	for _, a := range args {
		switch {
		case a == "--repo":
			repo = true
		case value == "" && !strings.HasPrefix(a, "-"):
			value = a
		default:
			return usageErr(usage)
		}
	}
	if value == "" || (value == "global" && !repo) {
		return usageErr(usage)
	}
	gd := ""
	if repo {
		var err error
		if gd, err = x.requireRepo(); err != nil {
			return err
		}
		if value == "global" {
			value = ""
		}
	}
	if err := x.app.Workspace.SetSignoffSetting(gd, value); err != nil || x.app.DryRun() {
		return err
	}
	st := x.app.Workspace.SignoffSetting(gd)
	verb := "Commits get no Signed-off-by line"
	if st.Mode {
		verb = "Commits get a Signed-off-by line with the repo's name and email"
	}
	x.sayf("%s: %s. Override one commit with --signoff or --no-signoff.\n", verb, st.Describe())
	return nil
}
