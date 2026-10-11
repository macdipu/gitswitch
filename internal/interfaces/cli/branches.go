package cli

import (
	"encoding/json"
	"errors"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// cmdBranch shows where the repo is and lists its branches.
func (x *CLI) cmdBranch(asJSON bool) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	h, err := x.app.Refs.Head(gd)
	if err != nil {
		return err
	}
	bs, err := x.app.Refs.Branches(gd)
	if err != nil {
		return err
	}
	if asJSON {
		if bs == nil {
			bs = []refs.Branch{}
		}
		return json.NewEncoder(x.out).Encode(map[string]any{"head": h, "branches": bs})
	}
	x.say(h.Describe())
	for _, b := range bs {
		mark := "  "
		if b.Current {
			mark = "* "
		}
		up := ""
		if b.Upstream != "" {
			up = "  → " + b.Upstream
		}
		x.sayf("%s%s%s\n", mark, b.Name, up)
	}
	return nil
}

func (x *CLI) cmdSwitch(args []string) error {
	if len(args) != 1 {
		return usageErr("gitswitch switch <branch>")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	b, err := x.app.Refs.Switch(gd, args[0])
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Switched to", b)
	}
	return nil
}

func (x *CLI) cmdUpdate() error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	h, err := x.app.Refs.Update(gd, func(l string) { x.say(" ", l) })
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Updated:", h.Describe())
	}
	return nil
}

func (x *CLI) cmdRevert(args []string) error {
	if len(args) != 1 {
		return usageErr("gitswitch revert <commit>")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	t, err := x.app.History.Revert(gd, args[0])
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Reverted", t.Label()+". Push when ready.")
	}
	return nil
}

// cmdDrop and cmdRollback rewrite not-pushed history, so they ask first
// (or take -y).
func (x *CLI) cmdDrop(args []string) error {
	rev, yes, err := commitArg(args, "gitswitch drop <commit> [-y]")
	if err != nil {
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Drop commit " + rev + " from this branch? Its changes are removed; later commits are kept."); err != nil {
			return err
		}
	}
	t, err := x.app.History.Drop(gd, rev)
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Dropped", t.Label())
	}
	return nil
}

func (x *CLI) cmdRollback(args []string) error {
	rev, yes, err := commitArg(args, "gitswitch rollback <commit> [-y]")
	if err != nil {
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Roll this branch back to " + rev + "? The later commits are undone; their changes stay in your files, uncommitted."); err != nil {
			return err
		}
	}
	n, t, err := x.app.History.Rollback(gd, rev)
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.sayf("Rolled back to %s. The changes of %d commit(s) are now uncommitted changes.\n", t.Label(), n)
	}
	return nil
}

// ask confirms on the terminal; without one, -y is required.
func (x *CLI) ask(q string) error {
	if !interactive() {
		return errors.New("add -y to confirm when not running in a terminal")
	}
	if !confirm(q) {
		return errors.New("cancelled")
	}
	return nil
}

// commitArg reads "<commit> [-y]".
func commitArg(args []string, usage string) (rev string, yes bool, err error) {
	var pos []string
	for _, a := range args {
		if a == "-y" || a == "--yes" {
			yes = true
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) != 1 {
		return "", false, usageErr(usage)
	}
	return pos[0], yes, nil
}

// branchNames lists branch names for shell completion.
func (x *CLI) branchNames() {
	gd := x.app.Repos.Current()
	if gd == "" {
		return
	}
	bs, _ := x.app.Refs.Branches(gd)
	for _, b := range bs {
		x.say(b.Name)
	}
}

func (x *CLI) cmdCherryPick(args []string) error {
	if len(args) == 0 {
		return usageErr("gitswitch cherry-pick <commit>...")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	ts, err := x.app.History.CherryPick(gd, args)
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		for _, t := range ts {
			x.say("Cherry-picked", t.Label())
		}
	}
	return nil
}

// cmdBrowse opens a commit's page (or the repo's) on its host.
func (x *CLI) cmdBrowse(args []string) error {
	if len(args) > 1 {
		return usageErr("gitswitch browse [<commit>]")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	rev := ""
	if len(args) == 1 {
		rev = args[0]
	}
	u, err := x.app.History.WebURL(gd, rev)
	if err != nil {
		return err
	}
	x.say(u)
	return process.OpenBrowser(u)
}
