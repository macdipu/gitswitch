package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
)

const rebaseUsage = "gitswitch rebase <onto> [-y] | rebase --plan <onto> [--json]"

// cmdRebase puts the current branch's own commits on top of another
// branch, tag or commit, after showing what will happen. When commits
// clash it stops (never aborts): fix the files and `gitswitch continue`,
// or `gitswitch abort`.
func (x *CLI) cmdRebase(args []string) error {
	if hasFlag(args, "-i") || hasFlag(args, "--interactive") {
		return x.cmdRebaseInteractive(args) // the rewrite feature: --since, --todo, --messages
	}
	var onto string
	var plan, asJSON, yes, interactive bool
	for _, a := range args {
		switch a {
		case "--plan":
			plan = true
		case "--json":
			asJSON = true
		case "-y", "--yes":
			yes = true
		case "-i", "--interactive":
			interactive = true
		case "--continue", "--skip", "--abort":
			if len(args) != 1 && !(len(args) == 2 && hasFlag(args, "-y")) {
				return usageErr("gitswitch rebase " + a + " [-y]")
			}
			return x.cmdOperation(strings.TrimPrefix(a, "--"), args)
		default:
			if onto != "" || strings.HasPrefix(a, "-") {
				return usageErr(rebaseUsage)
			}
			onto = a
		}
	}
	if interactive {
		return x.rebaseInteractive(args)
	}
	if onto == "" || (asJSON && !plan) {
		return usageErr(rebaseUsage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	p, err := x.app.Rebase.Plan(gd, onto)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(p)
	}
	x.printRebasePlan(p)
	if plan {
		return nil
	}
	if p.Blocked != "" {
		return errors.New(p.Blocked)
	}
	if p.UpToDate {
		return nil
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask(fmt.Sprintf("Rebase %s onto %s? Your %d commit(s) get new hashes (undo puts them back).", p.Branch, p.Onto, p.ReplayedCount)); err != nil {
			return err
		}
	}
	r, err := x.app.Rebase.Run(gd, onto)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.say(r.Message)
	switch r.Outcome {
	case rebase.Stopped:
		x.say()
		for _, f := range r.Conflicts {
			x.say("  conflict  " + f)
		}
		x.say("\n  Fix each file: gitswitch conflict <path> --ours|--theirs, or edit it and run gitswitch conflict <path> --done")
		x.say("  Then: gitswitch continue    Or go back: gitswitch abort")
		return fmt.Errorf("the rebase stopped: %d file(s) have conflicts", len(r.Conflicts))
	case rebase.Done, rebase.FastForwards:
		x.say("Undo it with: gitswitch undo")
	}
	return nil
}

func (x *CLI) printRebasePlan(p rebase.Plan) {
	x.say(p.Summary)
	commits := func(title string, cs []rebase.CommitInfo, n int) {
		if n == 0 {
			return
		}
		x.sayf("\n%s (%d):\n", title, n)
		for _, c := range cs {
			x.sayf("  %s %s  (%s)\n", c.Short, c.Subject, c.Author)
		}
		if n > len(cs) {
			x.sayf("  ... and %d more\n", n-len(cs))
		}
	}
	commits("Your commits, replayed in this order", p.Replayed, p.ReplayedCount)
	commits("Coming in from "+p.Onto, p.Incoming, p.IncomingCount)
	if p.Blocked != "" {
		x.say("\nCan't rebase now: " + p.Blocked)
	}
}

// rebaseInteractive: the interactive rebase is not part of this build
// unless the rewrite feature provides it.
func (x *CLI) rebaseInteractive(args []string) error {
	return x.cmdRebaseInteractive(args)
}
