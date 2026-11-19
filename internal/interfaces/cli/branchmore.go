package cli

import (
	"errors"
	"fmt"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// cmdBranchDelete deletes a local branch after asking (-y: not). A branch
// whose commits are on no other branch or remote needs --force.
func (x *CLI) cmdBranchDelete(args []string) error {
	const usage = "gitswitch branch delete <name> [--force] [-y]"
	var name string
	force, yes := false, false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case a == "-y" || a == "--yes":
			yes = true
		case name == "" && !strings.HasPrefix(a, "-"):
			name = a
		default:
			return usageErr(usage)
		}
	}
	if name == "" {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	// Check first, so the question says what is at stake.
	var u *refs.Unmerged
	q := "Delete the branch " + name + "?"
	if !yes && !x.app.DryRun() {
		d, err := x.app.Refs.CanDeleteBranch(gd, name, force)
		if errors.As(err, &u) {
			return fmt.Errorf("%v: gitswitch branch delete %s --force", err, name)
		}
		if err != nil {
			return err
		}
		if d.Only > 0 {
			q = fmt.Sprintf("Delete the branch %s? %d commit(s) on it are on no other branch or remote; only undo can bring them back.", name, d.Only)
		}
		if err := x.ask(q); err != nil {
			return err
		}
	}
	d, err := x.app.Refs.DeleteBranch(gd, name, force)
	if errors.As(err, &u) {
		return fmt.Errorf("%v: gitswitch branch delete %s --force", err, name)
	}
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Deleted the branch %s (it was at %s).\n", d.Branch, d.Hash[:min(7, len(d.Hash))])
	if d.Only > 0 {
		x.sayf("  %d commit(s) were only on it.\n", d.Only)
	}
	x.say(undoHint)
	return nil
}

// cmdBranchRename renames a branch: <old> <new>, or <new> for the current
// one.
func (x *CLI) cmdBranchRename(args []string) error {
	const usage = "gitswitch branch rename [<old>] <new>"
	var from, to string
	switch {
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		to = args[0]
	case len(args) == 2 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[1], "-"):
		from, to = args[0], args[1]
	default:
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	r, err := x.app.Refs.RenameBranch(gd, from, to)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Renamed the branch %s to %s.\n", r.From, r.To)
	if r.Upstream != "" {
		x.sayf("  It still pulls from %s.\n", r.Upstream)
	}
	x.say(undoHint)
	return nil
}

// cmdBranchUpstream sets or (--unset) unsets the remote branch the
// current branch pulls from.
func (x *CLI) cmdBranchUpstream(args []string) error {
	const usage = "gitswitch branch upstream <remote>/<branch> | --unset"
	if len(args) != 1 || (strings.HasPrefix(args[0], "-") && args[0] != "--unset") {
		return usageErr(usage)
	}
	up := args[0]
	if up == "--unset" {
		up = ""
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	u, err := x.app.Refs.SetUpstream(gd, "", up)
	if err != nil || x.app.DryRun() {
		return err
	}
	switch {
	case u.Upstream != "":
		x.sayf("%s now pulls from %s: update and push use it.\n", u.Branch, u.Upstream)
	case u.Was != "":
		x.sayf("%s no longer pulls from %s.\n", u.Branch, u.Was)
	default:
		x.sayf("%s pulls from no remote branch.\n", u.Branch)
	}
	return nil
}
