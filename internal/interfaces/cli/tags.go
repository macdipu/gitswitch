package cli

import (
	"encoding/json"
	"strings"
)

// cmdTag lists, creates, deletes and pushes tags.
func (x *CLI) cmdTag(args []string) error {
	if len(args) == 0 {
		return x.cmdTagList(nil)
	}
	switch args[0] {
	case "list":
		return x.cmdTagList(args[1:])
	case "new":
		return x.cmdTagNew(args[1:])
	case "delete":
		return x.cmdTagDelete(args[1:])
	case "push":
		return x.cmdTagPush(args[1:])
	case "--json":
		return x.cmdTagList(args)
	}
	return usageErr("gitswitch tag [list] [--json] | tag new <name> [<commit>] [-m <message>] | tag delete <name> [-y] | tag push <name>")
}

// cmdTagList lists the tags, newest first.
func (x *CLI) cmdTagList(args []string) error {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return usageErr("gitswitch tag [list] [--json]")
		}
		asJSON = true
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	tags, err := x.app.Refs.Tags(gd)
	if err != nil {
		return err
	}
	if asJSON {
		if tags == nil {
			tags = []string{}
		}
		return json.NewEncoder(x.out).Encode(tags)
	}
	if len(tags) == 0 {
		x.say("No tags yet. Make one with: gitswitch tag new <name> [-m <message>]")
		return nil
	}
	for _, t := range tags {
		x.say(t)
	}
	return nil
}

// cmdTagNew tags HEAD or a commit: annotated with -m, else lightweight.
func (x *CLI) cmdTagNew(args []string) error {
	const usage = "gitswitch tag new <name> [<commit>] [-m <message>]"
	var name, rev, msg string
	hasMsg := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-m" || a == "--message") && i+1 < len(args) && !hasMsg:
			i++
			msg, hasMsg = args[i], true
		case strings.HasPrefix(a, "--message=") && !hasMsg:
			msg, hasMsg = strings.TrimPrefix(a, "--message="), true
		case strings.HasPrefix(a, "-"):
			return usageErr(usage)
		case name == "":
			name = a
		case rev == "":
			rev = a
		default:
			return usageErr(usage)
		}
	}
	if name == "" || (hasMsg && strings.TrimSpace(msg) == "") {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	t, err := x.app.Refs.NewTag(gd, name, rev, msg)
	if err != nil || x.app.DryRun() {
		return err
	}
	kind := "lightweight"
	if t.Annotated {
		kind = "annotated"
	}
	x.sayf("Tagged %s as %s (%s).\n", t.Hash[:min(7, len(t.Hash))], t.Tag, kind)
	x.say("  Push it when ready: gitswitch tag push " + t.Tag)
	return nil
}

// cmdTagDelete deletes a local tag after asking (-y: not).
func (x *CLI) cmdTagDelete(args []string) error {
	name, yes, err := commitArg(args, "gitswitch tag delete <name> [-y]")
	if err != nil {
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Delete the tag " + name + " in this clone?"); err != nil {
			return err
		}
	}
	if err := x.app.Refs.DeleteTag(gd, name); err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Deleted the tag %s here. A copy pushed to a remote stays there.\n", name)
	return nil
}

// cmdTagPush pushes one tag with the repo's account, never forced.
func (x *CLI) cmdTagPush(args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return usageErr("gitswitch tag push <name>")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	p, err := x.app.Refs.PushTag(gd, args[0], func(l string) { x.say(" ", l) })
	if err != nil || x.app.DryRun() {
		return err
	}
	as := " with the account git asked for"
	if p.Account != "" {
		as = " as " + p.Account
	}
	x.sayf("Pushed the tag %s to %s%s.\n", p.Tag, p.Remote, as)
	return nil
}
