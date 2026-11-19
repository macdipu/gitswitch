package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
)

const mergeUsage = "gitswitch merge <branch> [--ff-only|--no-ff|--squash] [-y] | merge --plan <branch> [--json]"

// cmdMerge merges a branch into the current one, after showing the plan.
// `merge --continue|--skip|--abort` are git's spellings of continue, skip
// and abort, which work for any stopped operation.
func (x *CLI) cmdMerge(args []string) error {
	var branch string
	var plan, asJSON, yes bool
	mode, modes := merge.Auto, 0
	for _, a := range args {
		switch a {
		case "--continue", "--skip", "--abort":
			if len(args) != 1 && !(len(args) == 2 && hasFlag(args, "-y")) {
				return usageErr("gitswitch merge " + a + " [-y]")
			}
			return x.cmdOperation(strings.TrimPrefix(a, "--"), args)
		case "--plan":
			plan = true
		case "--json":
			asJSON = true
		case "-y", "--yes":
			yes = true
		case "--ff-only", "--ff":
			mode, modes = merge.FastForward, modes+1
		case "--no-ff":
			mode, modes = merge.Commit, modes+1
		case "--squash":
			mode, modes = merge.Squash, modes+1
		default:
			if branch != "" || strings.HasPrefix(a, "-") {
				return usageErr(mergeUsage)
			}
			branch = a
		}
	}
	if branch == "" || modes > 1 || (asJSON && !plan) {
		return usageErr(mergeUsage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	p, err := x.app.Merge.Plan(gd, branch)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(p)
	}
	x.printPlan(p)
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
		if err := x.ask(fmt.Sprintf("Merge %s into %s?", p.From, p.Into)); err != nil {
			return err
		}
	}
	r, err := x.app.Merge.Merge(gd, branch, mode)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.say(r.Message)
	switch r.Outcome {
	case merge.Stopped:
		x.say()
		for _, f := range r.Conflicts {
			x.say("  conflict  " + f)
		}
		x.say("\n  Fix each file: gitswitch conflict <path> --ours|--theirs, or edit it and run gitswitch conflict <path> --done")
		x.say("  Then: gitswitch continue    Or go back: gitswitch abort")
		return fmt.Errorf("the merge stopped: %d file(s) have conflicts", len(r.Conflicts))
	case merge.FastForwards, merge.MergeCommit:
		x.say("Undo it with: gitswitch undo")
	}
	return nil
}

func (x *CLI) printPlan(p merge.Plan) {
	x.say(p.Summary)
	commits := func(title string, cs []merge.CommitInfo, n int) {
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
	commits("Coming in from "+p.From, p.Incoming, p.IncomingCount)
	commits("Only on "+p.Into, p.Local, p.LocalCount)
	if p.Blocked != "" {
		x.say("\nCan't merge now: " + p.Blocked)
	}
}

// cmdOperation continues, skips or aborts the operation git stopped in
// the middle of (merge, rebase, cherry-pick, revert). Skip and abort throw
// work away, so they ask first (-y to skip the question).
func (x *CLI) cmdOperation(action string, args []string) error {
	yes := false
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		case "--" + action:
		default:
			return usageErr("gitswitch " + action + " [-y]")
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	o, err := x.app.Merge.Operation(gd)
	if err != nil {
		return err
	}
	if action != merge.Continue && !yes && !x.app.DryRun() && o.Allowed(action) {
		q := fmt.Sprintf("Abort the %s and go back to how it was before? Fixes made since are lost.", o.Kind)
		if action == merge.Skip {
			q = fmt.Sprintf("Skip the commit the %s stopped at? Its changes are left out.", o.Kind)
		}
		if err := x.ask(q); err != nil {
			return err
		}
	}
	after, err := x.app.Merge.Act(gd, action)
	if err != nil || x.app.DryRun() {
		return err
	}
	if after.Kind != "" {
		x.printOperation(after)
		if len(after.Conflicts) > 0 {
			return fmt.Errorf("the %s stopped again: %d file(s) have conflicts", after.Kind, len(after.Conflicts))
		}
		return nil
	}
	switch action {
	case merge.Abort:
		x.sayf("Aborted the %s: the repo is back as it was before.\n", o.Kind)
	case merge.Skip:
		x.sayf("Skipped; the %s is finished.\n", o.Kind)
	default:
		x.sayf("The %s is finished.\n", o.Kind)
		if o.Kind == merge.OpMerge {
			x.say("Undo it with: gitswitch undo")
		}
	}
	return nil
}

func (x *CLI) printOperation(o merge.Operation) {
	x.say(o.Message)
	if len(o.Conflicts) > 0 {
		x.sayf("\n  ours:   %s\n  theirs: %s\n\n", o.Ours, o.Theirs)
		for _, f := range o.Conflicts {
			x.say("  conflict  " + f)
		}
	}
	if len(o.Actions) > 0 {
		var cmds []string
		for _, a := range o.Actions {
			cmds = append(cmds, "gitswitch "+a)
		}
		x.say("\n  " + strings.Join(cmds, " · "))
	}
}

// cmdConflict shows a conflicted file's three versions, or resolves it:
// keep ours or theirs, or mark it fixed once its markers are gone. With no
// path it lists the operation in progress and its conflicts.
func (x *CLI) cmdConflict(args []string) error {
	const usage = "gitswitch conflict [<path>] [--ours|--theirs|--done] [--json]"
	var path, action string
	asJSON := false
	for _, a := range args {
		switch a {
		case "--ours", "--theirs", "--done":
			if action != "" {
				return usageErr(usage)
			}
			action = strings.TrimPrefix(a, "--")
		case "--json":
			asJSON = true
		default:
			if path != "" || strings.HasPrefix(a, "-") {
				return usageErr(usage)
			}
			path = a
		}
	}
	if (path == "" && action != "") || (asJSON && action != "") {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if path == "" {
		o, err := x.app.Merge.Operation(gd)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(x.out).Encode(o)
		}
		x.printOperation(o)
		return nil
	}
	path = x.app.Workspace.RepoPath(gd, cwd(), path)
	if action != "" {
		o, err := x.app.Merge.Resolve(gd, path, action, "")
		if err != nil || x.app.DryRun() {
			return err
		}
		what := map[string]string{"ours": "Kept ours for ", "theirs": "Kept theirs for ", "done": "Marked resolved: "}[action]
		x.say(what + path + ".")
		if o.CanContinue {
			x.sayf("Every conflict is fixed: gitswitch continue finishes the %s.\n", o.Kind)
		} else if len(o.Conflicts) > 0 {
			x.sayf("Still in conflict: %s\n", strings.Join(o.Conflicts, ", "))
		}
		return nil
	}
	c, err := x.app.Merge.Conflict(gd, path)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(c)
	}
	x.sayf("%s: %s\n  ours:   %s\n  theirs: %s\n", c.Path, c.Kind, c.OursLabel, c.TheirsLabel)
	show := func(title string, v *string) {
		if v == nil {
			x.sayf("\n--- %s: (none)\n", title)
			return
		}
		x.sayf("\n--- %s:\n", title)
		fmt.Fprint(x.out, *v)
		if !strings.HasSuffix(*v, "\n") {
			fmt.Fprintln(x.out)
		}
	}
	show("base (the last version both shared)", c.Base)
	show("ours", c.Ours)
	show("theirs", c.Theirs)
	if c.Exists {
		show("the file now", &c.Text)
	} else {
		show("the file now", nil)
	}
	x.say("\nKeep one side: gitswitch conflict " + c.Path + " --ours|--theirs")
	x.say("Or edit the file, then: gitswitch conflict " + c.Path + " --done")
	return nil
}
