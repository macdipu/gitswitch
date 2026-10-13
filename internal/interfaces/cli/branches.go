package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
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
	x.say(history.CleanText(h.Describe()))
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

// undoHint follows a change that can be undone.
const undoHint = "  (undo with: gitswitch undo)"

// cmdSwitch switches branch. With uncommitted changes, --smart takes them
// along and --park leaves them parked; without either, the switch-changes
// setting decides, else it asks in a terminal, else it stops. Changes
// parked on the new branch are offered back.
func (x *CLI) cmdSwitch(args []string) error {
	const usage = "gitswitch switch <branch> [--smart | --park]"
	var name, mode string
	for _, a := range args {
		switch {
		case (a == "--smart" || a == "--park") && mode == "":
			mode = a[2:]
		case name == "" && !strings.HasPrefix(a, "--"):
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
	var setting *refs.SwitchSetting // the setting that chose the mode
	if h, err := x.app.Refs.Head(gd); mode == "" && err == nil && h.Changes.Any() && h.Busy == "" {
		changes := strings.TrimPrefix(h.Changes.Describe(), "uncommitted: ")
		st := x.app.Refs.SwitchSetting(gd)
		switch {
		case st.Mode != refs.Ask:
			mode, setting = string(st.Mode), &st
		case !interactive():
			return fmt.Errorf("you have uncommitted changes on %s (%s), so say what to do with them: --smart takes them with you to %s, --park leaves them on %s. To not be asked again: gitswitch set switch-changes smart|park [--repo]", h.Branch, changes, name, h.Branch)
		default:
			// Smart switch is the default for a new branch, park for one
			// with its own work.
			bs, _ := x.app.Refs.Branches(gd)
			def, opts := "p", "[s/P/c]"
			if newBranch(bs, name) {
				def, opts = "s", "[S/p/c]"
			}
			x.sayf("You have uncommitted changes on %s (%s).\n", h.Branch, changes)
			x.sayf("  s  Smart switch: take them with you to %s\n", name)
			x.sayf("  p  Park them on %s (git stash), restored when you come back\n", h.Branch)
			x.say("  c  Cancel")
			k, remember := choose("Choose "+opts+" (add ! to remember for this repo, e.g. p!)", def, "s", "p", "c")
			switch k {
			case "s":
				mode = "smart"
			case "p":
				mode = "park"
			default:
				return errors.New("cancelled, nothing changed. Commit your changes first, or answer up front with --smart or --park")
			}
			if remember {
				if err := x.app.Refs.SetSwitchSetting(gd, mode); err != nil {
					return err
				}
				x.sayf("Remembered: in this repo, switching with uncommitted changes will %s. Change it with: gitswitch set switch-changes ask|smart|park --repo\n", map[string]string{"smart": "take them along", "park": "park them"}[mode])
			}
		}
	}
	var r refs.Switched
	switch mode {
	case "smart":
		r, err = x.app.Refs.SmartSwitch(gd, name)
	case "park":
		r, err = x.app.Refs.Park(gd, name)
	default:
		r.Branch, err = x.app.Refs.Switch(gd, name)
		if err == nil && !x.app.DryRun() {
			r.Waiting, _ = x.app.Refs.Waiting(gd)
		}
	}
	if err != nil || x.app.DryRun() {
		return err
	}
	if r.Clash != nil {
		x.sayClash(r.Clash)
		return nil
	}
	switch {
	case r.Parked:
		x.say("Parked your changes and switched to", r.Branch+". They come back when you switch back (gitswitch parked lists them).")
	case r.Carried:
		x.say("Switched to", r.Branch+", taking your uncommitted changes along.")
	default:
		x.say("Switched to", r.Branch)
	}
	x.say(undoHint)
	if setting != nil {
		x.say(settingHint(*setting))
	}
	return x.offerParked(gd, r.Waiting)
}

// settingHint says which switch-changes setting chose what happened to the
// changes, and how to change it.
func settingHint(st refs.SwitchSetting) string {
	if st.Repo != "" {
		return fmt.Sprintf("  (your choice for this repo: %s. Change it with: gitswitch set switch-changes ask|smart|park --repo, or follow every repo's with: gitswitch set switch-changes global --repo)", st.Mode)
	}
	return fmt.Sprintf("  (your choice for every repo: %s. Change it with: gitswitch set switch-changes ask|smart|park)", st.Mode)
}

// newBranch reports whether switching to name creates the local branch
// (from a remote one).
func newBranch(bs []refs.Branch, name string) bool {
	b := refs.Find(bs, name)
	if b == nil || !b.Remote {
		return false
	}
	l := refs.Find(bs, b.LocalName())
	return l == nil || l.Remote
}

// offerParked follows a switch to a branch with parked changes: in a
// terminal it offers to restore the newest (or look at them first),
// otherwise it says how.
func (x *CLI) offerParked(gd string, w []refs.Parked) error {
	if len(w) == 0 || x.app.DryRun() {
		return nil
	}
	p := w[0]
	x.sayf("\nYou parked changes on %s %s: %s\n", p.Branch, ago(p.Time), refs.FileList(p.Files))
	if len(w) > 1 {
		x.sayf("(%d sets are parked on %s; gitswitch parked lists them)\n", len(w), p.Branch)
	}
	if !interactive() {
		x.say("  Restore them with: gitswitch parked restore")
		return nil
	}
	for {
		switch k, _ := choose("Restore them now? [r]estore, [l]ook first, [N]ot now", "n", "r", "l", "n"); k {
		case "r":
			if err := x.app.Refs.RestoreParked(gd, p.Stash); err != nil {
				return err
			}
			x.say("Restored your changes on " + p.Branch + ".")
			return nil
		case "l":
			out, err := x.app.Refs.ParkedPatch(gd, p.Stash)
			if err != nil {
				return err
			}
			x.say(out)
		default:
			x.say("They stay parked. Restore them later with: gitswitch parked restore")
			return nil
		}
	}
}

// cmdParked lists the parked changes, or shows, restores or drops one:
// by its number in the list, else the newest parked on this branch.
func (x *CLI) cmdParked(args []string) error {
	const usage = "gitswitch parked [--json] | parked show|restore [<n>] | parked drop [<n>] [-y]"
	var verb, num string
	var yes, asJSON bool
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-y" || a == "--yes":
			yes = true
		case verb == "" && (a == "show" || a == "restore" || a == "drop"):
			verb = a
		case verb != "" && num == "" && !strings.HasPrefix(a, "-"):
			num = a
		default:
			return usageErr(usage)
		}
	}
	if (asJSON && verb != "") || (yes && verb != "drop") {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	all, err := x.app.Refs.Parked(gd)
	if err != nil {
		return err
	}
	h, _ := x.app.Refs.Head(gd)
	if verb == "" {
		if asJSON {
			if all == nil {
				all = []refs.Parked{}
			}
			return json.NewEncoder(x.out).Encode(all)
		}
		if len(all) == 0 {
			x.say("Nothing is parked in this repo.")
			return nil
		}
		x.say("Parked changes (git stash), newest first:")
		for i, p := range all {
			here := ""
			if p.Branch == h.Branch {
				here = "   ← this branch"
			}
			x.sayf("  %d. %s   %s%s\n", i+1, p.Describe(time.Now().Unix()), p.Ref, here)
		}
		x.say("Restore with: gitswitch parked restore [<n>]   look first: show [<n>]   delete: drop [<n>]")
		return nil
	}
	var p refs.Parked
	switch {
	case num != "":
		i, err := strconv.Atoi(num)
		if err != nil || i < 1 || i > len(all) {
			return fmt.Errorf("no parked changes numbered %s; gitswitch parked lists them", num)
		}
		p = all[i-1]
	case len(refs.ParkedOn(all, h.Branch)) > 0:
		p = refs.ParkedOn(all, h.Branch)[0]
	default:
		return fmt.Errorf("nothing is parked on %s; pick parked changes by their number in gitswitch parked", h.Branch)
	}
	switch verb {
	case "show":
		out, err := x.app.Refs.ParkedPatch(gd, p.Stash)
		if err != nil {
			return err
		}
		x.sayf("Parked on %s %s (%s):\n%s\n", p.Branch, ago(p.Time), p.Ref, out)
		return nil
	case "restore":
		if err := x.app.Refs.RestoreParked(gd, p.Stash); err != nil {
			return err
		}
		if !x.app.DryRun() {
			x.say("Restored the changes parked on " + p.Branch + ": " + refs.FileList(p.Files) + ".")
		}
		return nil
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask(fmt.Sprintf("Delete the changes parked on %s %s (%s)? This can't be undone.", p.Branch, ago(p.Time), refs.FileList(p.Files))); err != nil {
			return err
		}
	}
	if err := x.app.Refs.DropParked(gd, p.Stash); err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Deleted the changes parked on " + p.Branch + ".")
	}
	return nil
}

// sayClash explains an open smart-switch clash and the ways out.
func (x *CLI) sayClash(c *refs.Clash) {
	x.sayf("Switched to %s, but %d file(s) clash with it: %s\n", c.To, len(c.Files), strings.Join(c.Files, ", "))
	x.say("Your original changes are kept safe until you finish.")
	x.say("  Fix the conflict markers (<<<<<<< ======= >>>>>>>) in your editor or IDE, then run: gitswitch resolve --done")
	x.say("  Or go back to " + c.From + " with your changes exactly as before: gitswitch resolve --abort")
	x.say("  Or park them on " + c.From + " and stay on " + c.To + " without them: gitswitch resolve --park")
}

// cmdResolve shows an open smart-switch clash, finishes it (--done), goes
// back (--abort) or parks the changes instead (--park). The last two throw
// away fixes made since, so they ask first unless -y.
func (x *CLI) cmdResolve(args []string) error {
	const usage = "gitswitch resolve [--done | --abort [-y] | --park [-y]]"
	var done, abort, park, yes bool
	for _, a := range args {
		switch a {
		case "--done":
			done = true
		case "--abort":
			abort = true
		case "--park":
			park = true
		case "-y", "--yes":
			yes = true
		default:
			return usageErr(usage)
		}
	}
	if (done && abort) || (done && park) || (abort && park) {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	c, err := x.app.Refs.Clash(gd)
	if err != nil {
		return err
	}
	switch {
	case c == nil:
		x.say("Nothing to resolve: no smart switch is waiting in this repo.")
		return nil
	case done:
		if err := x.app.Refs.ResolveDone(gd); err != nil {
			return err
		}
		if !x.app.DryRun() {
			x.say("Done: you are on " + c.To + " with your changes, uncommitted.")
		}
		return nil
	case abort:
		if !yes && !x.app.DryRun() {
			if err := x.ask("Go back to " + c.From + "? Your changes come back exactly as before the switch; fixes made since on " + c.To + " are thrown away."); err != nil {
				return err
			}
		}
		if err := x.app.Refs.ResolveAbort(gd); err != nil {
			return err
		}
		if !x.app.DryRun() {
			x.say("Back on " + c.From + " with your changes as they were.")
		}
		return nil
	case park:
		if !yes && !x.app.DryRun() {
			if err := x.ask("Park your changes on " + c.From + " and stay on " + c.To + " without them? They are parked exactly as before the switch; fixes made since on " + c.To + " are thrown away."); err != nil {
				return err
			}
		}
		r, err := x.app.Refs.ResolvePark(gd)
		if err != nil || x.app.DryRun() {
			return err
		}
		x.say("Parked your changes on " + c.From + "; you are on " + r.Branch + " without them. They come back when you switch to " + c.From + ".")
		return x.offerParked(gd, r.Waiting)
	}
	x.sayClash(c)
	return nil
}

// cmdBranchNew starts a branch at HEAD (or --from) and switches to it,
// with the branch-prefix setting in front of the name (--no-prefix: not).
// --move also takes the commits not pushed off the branch left.
func (x *CLI) cmdBranchNew(args []string) error {
	const usage = "gitswitch branch new <name> [--from <commit> | --move] [--no-prefix]"
	var name, from string
	noPrefix, move := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--no-prefix":
			noPrefix = true
		case a == "--move":
			move = true
		case a == "--from" && i+1 < len(args) && from == "":
			i++
			from = args[i]
		case strings.HasPrefix(a, "--from=") && from == "":
			from = strings.TrimPrefix(a, "--from=")
		case name == "" && !strings.HasPrefix(a, "-"):
			name = a
		default:
			return usageErr(usage)
		}
	}
	if name == "" || (move && from != "") {
		return usageErr(usage)
	}
	if !noPrefix {
		name = refs.WithPrefix(x.app.Refs.BranchPrefix(), name)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if move {
		return x.moveToNewBranch(gd, name)
	}
	r, err := x.app.Refs.NewBranch(gd, name, from)
	if err != nil || x.app.DryRun() {
		return err
	}
	at := ""
	if from != "" {
		at = " from " + from
	}
	if r.Carried {
		x.sayf("Started branch %s%s and switched to it, with your uncommitted changes.\n", r.Branch, at)
	} else {
		x.sayf("Started branch %s%s and switched to it.\n", r.Branch, at)
	}
	x.say("  Push it when ready: git push -u origin " + r.Branch)
	x.say(undoHint)
	return nil
}

// cmdBranchPrune deletes the branches merged into the default branch,
// after listing them and asking (-y: not).
func (x *CLI) cmdBranchPrune(args []string) error {
	yes := false
	for _, a := range args {
		if a != "-y" && a != "--yes" {
			return usageErr("gitswitch branch prune [-y]")
		}
		yes = true
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	m, err := x.app.Refs.Merged(gd)
	if err != nil {
		return err
	}
	for _, b := range m.Kept {
		x.sayf("Kept %s: its remote branch was deleted, but its commits aren't on %s (squash-merged? check, then: git branch -D %s)\n", b.Name, m.Into, b.Name)
	}
	if len(m.Branches) == 0 {
		x.sayf("No branch to delete: none is merged into %s.\n", m.Into)
		return nil
	}
	x.sayf("Merged into %s, so their commits are safe there:\n", m.Into)
	for _, b := range m.Branches {
		x.sayf("  %s  %s\n", b.Hash[:min(7, len(b.Hash))], b.Name)
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask(fmt.Sprintf("Delete %d branch(es)?", len(m.Branches))); err != nil {
			return err
		}
	}
	m, err = x.app.Refs.Prune(gd)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Deleted %d merged branch(es).\n", len(m.Branches))
	x.say(undoHint)
	return nil
}

// moveToNewBranch is branch new --move: for commits made on the wrong
// branch.
func (x *CLI) moveToNewBranch(gd, name string) error {
	r, err := x.app.Refs.MoveToNewBranch(gd, name)
	if err != nil || x.app.DryRun() {
		return err
	}
	commits := "1 commit"
	if r.Commits != 1 {
		commits = fmt.Sprintf("%d commits", r.Commits)
	}
	x.sayf("Moved %s from %s to the new branch %s, and switched to it.\n", commits, r.From, r.Branch)
	x.sayf("  %s is back at %s, the last commit it shares with its remote branch.\n", r.From, r.Base[:min(7, len(r.Base))])
	if r.Carried {
		x.say("  Your uncommitted changes are as they were.")
	}
	x.say("  Push it when ready: gitswitch push")
	x.say(undoHint)
	return nil
}

// cmdPush pushes the current branch and says which account pushed; with
// --open it opens the pull request page too. First it checks the commits
// to push show that account: when some don't, it offers to fix them, and
// without a terminal it needs -y to push them anyway.
func (x *CLI) cmdPush(args []string) error {
	open, yes := false, false
	for _, a := range args {
		switch a {
		case "--open":
			open = true
		case "-y", "--yes":
			yes = true
		default:
			return usageErr("gitswitch push [--open] [-y]")
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		if err := x.checkAccount(gd, yes); err != nil {
			return err
		}
	}
	p, err := x.app.Refs.Push(gd, func(l string) { x.say(" ", l) })
	if err != nil || x.app.DryRun() {
		return err
	}
	if p.Commits == 0 && !p.First {
		x.sayf("Nothing to push: %s is up to date with %s.\n", p.Branch, p.Upstream)
		return nil
	}
	as := " with the account git asked for"
	if p.Account != "" {
		as = " as " + p.Account
	}
	x.sayf("Pushed %d commit(s) to %s%s.\n", p.Commits, p.Upstream, as)
	if p.First {
		x.sayf("%s now tracks %s: update and push use it from now on.\n", p.Branch, p.Upstream)
	}
	if p.PRURL == "" {
		return nil
	}
	x.say("Open a pull request:", p.PRURL)
	if open {
		return process.OpenBrowser(p.PRURL)
	}
	return nil
}

// checkAccount warns before a push when the repo's commits don't show the
// account that pushes. Commits to push made as another account need an
// answer: fix them first, push anyway (-y) or cancel.
func (x *CLI) checkAccount(gd string, yes bool) error {
	ch, err := x.app.History.CheckAccount(gd)
	if err != nil || ch.Describe() == "" {
		return nil // the check only warns; the push says what is wrong with the repo
	}
	x.say("Account check:", ch.Describe())
	if ch.EmailBad {
		x.sayf("  To make new commits as %s: gitswitch use %s --once\n", ch.Account, ch.Account)
	}
	if ch.Wrong == 0 || yes {
		return nil
	}
	if !interactive() {
		return fmt.Errorf("not pushed. Fix the commits first (gitswitch fix-author rewrites them as %s), or push them as they are with -y", ch.Account)
	}
	switch k, _ := choose(fmt.Sprintf("[f]ix them as %s first, then push · [p]ush them as they are · [C]ancel", ch.Account), "c", "f", "p", "c"); k {
	case "f":
		n, err := x.app.History.FixAuthor(gd)
		if err != nil {
			return err
		}
		x.sayf("Fixed %d commit(s): they are now made as %s.\n", n, ch.Account)
		return nil
	case "p":
		return nil
	}
	return errors.New("cancelled, nothing pushed")
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
		x.say("Updated:", history.CleanText(h.Describe()))
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
		x.say("Reverted", history.CleanText(t.Label())+". Push when ready.")
		x.say(undoHint)
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
		x.say("Dropped", history.CleanText(t.Label()))
		x.say(undoHint)
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
		x.sayf("Rolled back to %s. The changes of %d commit(s) are now uncommitted changes.\n", history.CleanText(t.Label()), n)
		x.say(undoHint)
	}
	return nil
}

// cmdUndo undoes the repo's last change after saying what it does.
// --show only says it, --json as JSON.
func (x *CLI) cmdUndo(args []string) error {
	var yes, show, asJSON bool
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		case "--show":
			show = true
		case "--json":
			asJSON = true
		default:
			return usageErr("gitswitch undo [-y] [--show [--json]]")
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	st, err := x.app.Undo.Last(gd)
	if show && asJSON {
		if errors.Is(err, undo.ErrNothing) {
			return json.NewEncoder(x.out).Encode(nil)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(x.out).Encode(st)
	}
	if err != nil {
		return err
	}
	x.sayf("Last change: %s (%s)\n", history.CleanText(st.Label), ago(st.Time))
	if st.Blocked != "" {
		return errors.New("it can't be undone: " + st.Blocked)
	}
	x.say("Undo:", st.Effect)
	if show {
		return nil
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Undo it?"); err != nil {
			return err
		}
	}
	if _, err := x.app.Undo.Undo(gd); err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.say("Undone.")
	}
	return nil
}

// ago says how long ago a Unix time was, roughly.
func ago(unix int64) string { return refs.Ago(time.Now().Unix(), unix) }

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
			x.say("Cherry-picked", history.CleanText(t.Label()))
		}
		x.say(undoHint)
	}
	return nil
}

// cmdShow prints a commit's full message and the files it changed.
func (x *CLI) cmdShow(args []string) error {
	asJSON := hasFlag(args, "--json")
	var revs []string
	for _, a := range args {
		if a != "--json" {
			revs = append(revs, a)
		}
	}
	if len(revs) != 1 {
		return usageErr("gitswitch show <commit> [--json]")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	d, err := x.app.History.Show(gd, revs[0])
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(d)
	}
	x.say("commit", d.Hash)
	x.say()
	for _, l := range strings.Split(d.Message, "\n") {
		x.say("   ", l)
	}
	x.say()
	for _, f := range d.Files {
		if f.Binary {
			x.sayf("  %8s  %s\n", "binary", f.Path)
		} else {
			x.sayf("  %+4d %-4s %s\n", f.Added, fmt.Sprintf("-%d", f.Deleted), f.Path)
		}
	}
	if d.More > 0 {
		x.sayf("  ... and %d more file(s)\n", d.More)
	}
	x.sayf("%d file(s) changed, %d insertion(s), %d deletion(s)\n", len(d.Files)+d.More, d.Added, d.Deleted)
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
