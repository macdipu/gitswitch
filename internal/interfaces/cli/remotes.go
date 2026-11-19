package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// cmdRemote is the remotes manager: list (the default), add, remove,
// rename and set-url.
func (x *CLI) cmdRemote(args []string) error {
	const usage = "gitswitch remote [list] [--json] | add <name> <url> | remove <name> [--force] [-y] | rename <old> <new> | set-url <name> <url>"
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	var pos []string
	force, yes, asJSON := false, false, false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case a == "-y" || a == "--yes":
			yes = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return usageErr(usage)
		default:
			pos = append(pos, a)
		}
	}
	want := map[string]int{"list": 0, "add": 2, "remove": 1, "rename": 2, "set-url": 2}
	n, known := want[verb]
	if !known || len(pos) != n || (force && verb != "remove") || (yes && verb != "remove") || (asJSON && verb != "list") {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	switch verb {
	case "list":
		return x.remoteList(gd, asJSON)
	case "add":
		c, err := x.app.Refs.AddRemote(gd, pos[0], pos[1])
		if err != nil || x.app.DryRun() {
			return err
		}
		x.sayf("Added the remote %s at %s.\n", c.Name, c.URL)
		x.sayRemoteAccount(c)
		x.sayf("  Read it with: gitswitch fetch  (git fetch %s)\n", c.Name)
		x.say("  (remotes are not part of undo; remove it again with: gitswitch remote remove " + c.Name + ")")
	case "remove":
		return x.remoteRemove(gd, pos[0], force, yes)
	case "rename":
		c, err := x.app.Refs.RenameRemote(gd, pos[0], pos[1])
		if err != nil || x.app.DryRun() {
			return err
		}
		x.sayf("Renamed the remote %s to %s.\n", c.Name, c.NewName)
		if c.Note != "" {
			x.say(" ", c.Note)
		}
	case "set-url":
		c, err := x.app.Refs.SetRemoteURL(gd, pos[0], pos[1])
		if err != nil || x.app.DryRun() {
			return err
		}
		x.sayf("The remote %s now points at %s (it was %s).\n", c.Name, c.URL, c.Was)
		x.sayRemoteAccount(c)
		x.say("  (to point it back: gitswitch remote set-url " + c.Name + " " + c.Was + ")")
	}
	return nil
}

// sayRemoteAccount says which account a remote's URL uses, and the note
// when it is worth a warning.
func (x *CLI) sayRemoteAccount(c refs.RemoteChanged) {
	if c.Account != "" {
		x.sayf("  Fetching and pushing there uses the account %s.\n", c.Account)
	}
	if c.Note != "" {
		x.say("  Note:", c.Note+".")
	}
}

func (x *CLI) remoteList(gd string, asJSON bool) error {
	rs, err := x.app.Refs.Remotes(gd)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(rs)
	}
	if len(rs) == 0 {
		x.say("This repo has no remote. Add one with: gitswitch remote add origin <url>")
		return nil
	}
	for _, r := range rs {
		who := "as " + r.Account
		if r.Account == "" {
			who = "no account for " + r.Host + ": git asks"
		}
		url := r.FetchURL
		if r.PushURL != r.FetchURL {
			url += " (push: " + r.PushURL + ")"
		}
		x.sayf("%-10s %s  [%s]\n", r.Name, url, who)
		var notes []string
		if r.Upstream {
			notes = append(notes, "the current branch pulls from and pushes to it")
		}
		if len(r.Tracking) > 0 {
			notes = append(notes, "tracked by "+strings.Join(r.Tracking, ", "))
		}
		if len(notes) > 0 {
			x.say("           " + strings.Join(notes, "; "))
		}
	}
	return nil
}

func (x *CLI) remoteRemove(gd, name string, force, yes bool) error {
	rs, err := x.app.Refs.Remotes(gd)
	if err != nil {
		return err
	}
	var r *refs.Remote
	for i := range rs {
		if rs[i].Name == name {
			r = &rs[i]
		}
	}
	if r == nil {
		_, err := x.app.Refs.RemoveRemote(gd, name, false) // says which remotes there are
		return err
	}
	if len(r.Tracking) > 0 && !force {
		_, err := x.app.Refs.RemoveRemote(gd, name, false)
		return fmt.Errorf("%v: gitswitch remote remove %s --force", err, name)
	}
	if !yes && !x.app.DryRun() {
		q := fmt.Sprintf("Remove the remote %s (%s) from this clone? Its remote-tracking branches here go too; the server is untouched.", name, r.FetchURL)
		if len(r.Tracking) > 0 {
			q = fmt.Sprintf("Remove the remote %s (%s) from this clone? %s will have no upstream. Its remote-tracking branches here go too; the server is untouched.", name, r.FetchURL, strings.Join(r.Tracking, ", "))
		}
		if err := x.ask(q); err != nil {
			return err
		}
	}
	c, err := x.app.Refs.RemoveRemote(gd, name, force)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Removed the remote %s from this clone.\n", c.Name)
	if len(c.Branches) > 0 {
		x.sayf("  No upstream any more: %s. Set one with: gitswitch branch upstream <remote>/<branch>\n", strings.Join(c.Branches, ", "))
	}
	x.say(" ", c.Note)
	x.say("  (remotes are not part of undo)")
	return nil
}

// sayPruned lists what a fetch with prune forgot.
func (x *CLI) sayPruned(f refs.Fetched) {
	if len(f.Pruned) == 0 {
		return
	}
	x.sayf("Forgot %d remote branch(es) the server no longer has: %s\n", len(f.Pruned), strings.Join(f.Pruned, ", "))
	if len(f.Gone) > 0 {
		x.sayf("  These local branches pulled from them: %s (their commits are untouched; delete them with gitswitch branch delete, or give them an upstream)\n", strings.Join(f.Gone, ", "))
	}
}

// setFetchPrune saves whether a fetch also prunes, for every repo or
// (--repo) this one, where "global" follows every repo's.
func (x *CLI) setFetchPrune(args []string) error {
	const usage = "gitswitch set fetch-prune on|off [--repo] | fetch-prune global --repo"
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
	if err := x.app.Refs.SetPruneSetting(gd, value); err != nil || x.app.DryRun() {
		return err
	}
	st := x.app.Refs.PruneSetting(gd)
	where := "every repo"
	if st.Repo != "" {
		where = "this repo"
	}
	if st.On {
		x.sayf("Fetching in %s also forgets the remote branches the server deleted, and lists them.\n", where)
	} else {
		x.sayf("Fetching in %s leaves remote branches alone. Prune once with: gitswitch fetch --prune\n", where)
	}
	return nil
}

// cmdBranchDeleteRemote deletes a branch on the server after asking (-y:
// not), through the repo's account. The default branch and the branch you
// are on need --force.
func (x *CLI) cmdBranchDeleteRemote(args []string) error {
	const usage = "gitswitch branch delete-remote <remote>/<branch> [--force] [-y]"
	var ref string
	force, yes := false, false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case a == "-y" || a == "--yes":
			yes = true
		case ref == "" && !strings.HasPrefix(a, "-"):
			ref = a
		default:
			return usageErr(usage)
		}
	}
	if ref == "" {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	cf := func(err error) error {
		if err != nil && !force && strings.Contains(err.Error(), "only with force") {
			return fmt.Errorf("%v: gitswitch branch delete-remote %s --force", err, ref)
		}
		return err
	}
	if !yes && !x.app.DryRun() {
		d, err := x.app.Refs.CanDeleteRemoteBranch(gd, ref, force)
		if err != nil {
			return cf(err)
		}
		as := ""
		if d.Account != "" {
			as = " as " + d.Account
		}
		q := fmt.Sprintf("Delete the branch %s on %s%s? It is at %s; everyone who uses it loses it there.", d.Branch, d.Remote, as, d.Hash[:min(7, len(d.Hash))])
		if err := x.ask(q); err != nil {
			return err
		}
	}
	d, err := x.app.Refs.DeleteRemoteBranch(gd, ref, force, func(l string) { x.say(" ", l) })
	if err != nil || x.app.DryRun() {
		return cf(err)
	}
	x.sayf("Deleted the branch %s on %s (it was at %s).\n", d.Branch, d.Remote, d.Hash[:min(7, len(d.Hash))])
	if len(d.Tracking) > 0 {
		x.sayf("  %s pulled from it and now point at a remote branch that is gone: give them another upstream (gitswitch branch upstream) or delete them.\n", strings.Join(d.Tracking, ", "))
	}
	x.say("  To put it back:", d.Restore)
	x.say("  (this is on the server, so gitswitch undo can't take it back)")
	return nil
}

// cmdPushLease is push --force-with-lease: it lists the commits on the
// remote branch that would be overwritten, asks (-y: not), and pushes only
// if the remote branch is still where the last fetch saw it.
func (x *CLI) cmdPushLease(args []string) error {
	const usage = "gitswitch push --force-with-lease [-y]"
	yes := false
	for _, a := range args {
		switch a {
		case "--force-with-lease":
		case "-y", "--yes":
			yes = true
		default:
			return usageErr(usage)
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	p, err := x.app.Refs.PushPlan(gd)
	if err != nil {
		return err
	}
	if !p.CanForce {
		return errors.New(p.Why)
	}
	x.sayf("%s would be overwritten: they are on %s, not in %s, as of your last fetch:\n", plural(len(p.ToOverwrite)+p.MoreOverwrite, "commit"), p.Upstream, p.Branch)
	x.sayLines(p.ToOverwrite, p.MoreOverwrite)
	x.sayf("Your %s would go up instead:\n", plural(len(p.ToPush)+p.MorePush, "commit"))
	x.sayLines(p.ToPush, p.MorePush)
	x.say("The push goes only if", p.Upstream, "is still at", p.Expect[:min(7, len(p.Expect))]+", where your last fetch saw it.")
	if !x.app.DryRun() {
		if err := x.checkAccount(gd, yes); err != nil {
			return err
		}
		if !yes {
			as := ""
			if p.Account != "" {
				as = " as " + p.Account
			}
			if err := x.ask(fmt.Sprintf("Force push %s to %s%s, overwriting those %s?", p.Branch, p.Upstream, as, plural(len(p.ToOverwrite)+p.MoreOverwrite, "commit"))); err != nil {
				return err
			}
		}
	}
	r, err := x.app.Refs.ForcePushLease(gd, p.Expect, true, func(l string) { x.say(" ", l) })
	if err != nil || x.app.DryRun() {
		return err
	}
	as := " with the account git asked for"
	if r.Account != "" {
		as = " as " + r.Account
	}
	x.sayf("Force pushed %s to %s%s.\n", plural(r.Commits, "commit"), r.Upstream, as)
	x.say("  The commits it replaced are no longer on", r.Upstream+". The branch was at", p.Expect[:min(7, len(p.Expect))]+"; to put that back: git push --force-with-lease", r.Remote, p.Expect+":refs/heads/"+strings.TrimPrefix(r.Upstream, r.Remote+"/"))
	return nil
}

func (x *CLI) sayLines(ls []refs.Line, more int) {
	for _, l := range ls {
		x.sayf("  %s  %s  (%s, %s)\n", l.Short, l.Subject, l.Author, ago(l.Time))
	}
	if more > 0 {
		x.sayf("  ... and %d more\n", more)
	}
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// changesMode decides what happens to uncommitted changes before HEAD
// moves, as a switch does: a mode given, else the saved setting, else ask
// in a terminal. It returns "" (plain: git carries them or refuses),
// "smart" or "park", and the setting that chose it, if any. canPark is
// false on a detached HEAD, which has no branch to park changes on.
func (x *CLI) changesMode(gd, mode, target string, canPark bool) (string, *refs.SwitchSetting, error) {
	h, err := x.app.Refs.Head(gd)
	if mode != "" || err != nil || !h.Changes.Any() || h.Busy != "" {
		return mode, nil, nil
	}
	changes := strings.TrimPrefix(h.Changes.Describe(), "uncommitted: ")
	st := x.app.Refs.SwitchSetting(gd)
	where := h.Branch
	if where == "" {
		where = "the detached HEAD"
	}
	switch {
	case st.Mode == refs.Park && !canPark:
		return "smart", &st, nil
	case st.Mode != refs.Ask:
		return string(st.Mode), &st, nil
	case !interactive():
		opts := "--smart takes them with you to " + target
		if canPark {
			opts += ", --park leaves them on " + h.Branch
		}
		return "", nil, fmt.Errorf("you have uncommitted changes on %s (%s), so say what to do with them: %s. To not be asked again: gitswitch set switch-changes smart|park [--repo]", where, changes, opts)
	}
	x.sayf("You have uncommitted changes on %s (%s).\n", where, changes)
	x.sayf("  s  Smart: take them with you to %s\n", target)
	keys, def, opts := []string{"s", "c"}, "s", "[S/c]"
	if canPark {
		x.sayf("  p  Park them on %s (git stash), restored when you come back\n", h.Branch)
		keys, def, opts = []string{"s", "p", "c"}, "p", "[s/P/c]"
	}
	x.say("  c  Cancel")
	k, remember := choose("Choose "+opts+" (add ! to remember for this repo, e.g. p!)", def, keys...)
	switch k {
	case "s":
		mode = "smart"
	case "p":
		mode = "park"
	default:
		return "", nil, errors.New("cancelled, nothing changed. Commit your changes first, or answer up front with --smart or --park")
	}
	if remember {
		if err := x.app.Refs.SetSwitchSetting(gd, mode); err != nil {
			return "", nil, err
		}
		x.sayf("Remembered: in this repo, switching with uncommitted changes will %s.\n", map[string]string{"smart": "take them along", "park": "park them"}[mode])
	}
	return mode, nil, nil
}

// modeArgs reads "[--smart|--park] [--force]" and the positional words.
func modeArgs(args []string, usage string) (pos []string, mode string, force bool, err error) {
	for _, a := range args {
		switch {
		case (a == "--smart" || a == "--park") && mode == "":
			mode = a[2:]
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-"):
			return nil, "", false, usageErr(usage)
		default:
			pos = append(pos, a)
		}
	}
	return pos, mode, force, nil
}

// cmdCheckout looks at a commit, tag or remote branch with HEAD on no
// branch, and says what that means.
func (x *CLI) cmdCheckout(args []string) error {
	const usage = "gitswitch checkout <commit|tag|remote/branch> [--smart|--park] [--force]"
	pos, mode, force, err := modeArgs(args, usage)
	if err != nil || len(pos) != 1 {
		if err == nil {
			err = usageErr(usage)
		}
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	canPark := true
	if h, herr := x.app.Refs.Head(gd); herr == nil && h.Branch == "" {
		canPark = false
	}
	mode, setting, err := x.changesMode(gd, mode, pos[0], canPark)
	if err != nil {
		return err
	}
	d, err := x.app.Refs.CheckoutDetached(gd, pos[0], mode, force)
	if err != nil || x.app.DryRun() {
		return err
	}
	switch {
	case d.Parked:
		x.sayf("Parked your changes on %s and checked out %s (%s).\n", d.From, d.Rev, d.Short)
	case d.Carried:
		x.sayf("Checked out %s (%s), taking your uncommitted changes along.\n", d.Rev, d.Short)
	default:
		x.sayf("Checked out %s (%s).\n", d.Rev, d.Short)
	}
	x.say("  " + d.Words)
	x.say("  New branch here: gitswitch branch new <name>")
	if d.Back != "" {
		x.say("  Way back: gitswitch back  (to " + d.Back + ")")
	}
	x.say("  (gitswitch undo can't take this back: there is no branch to return to; gitswitch back does)")
	if setting != nil {
		x.say(settingHint(*setting))
	}
	return nil
}

// cmdBack leaves a detached HEAD for the branch it came from.
func (x *CLI) cmdBack(args []string) error {
	const usage = "gitswitch back [--smart|--park] [--force]"
	pos, mode, force, err := modeArgs(args, usage)
	if err != nil || len(pos) != 0 {
		if err == nil {
			err = usageErr(usage)
		}
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	st, err := x.app.Refs.DetachedState(gd)
	if err != nil {
		return err
	}
	if !st.Detached {
		return errors.New("HEAD is on a branch, not detached: there is nothing to go back from")
	}
	if st.Back == "" {
		return errors.New("GitSwitch doesn't know which branch this detached HEAD came from (or it is gone); switch to one: gitswitch switch <branch>")
	}
	mode, setting, err := x.changesMode(gd, mode, st.Back, false)
	if err != nil {
		return err
	}
	r, err := x.app.Refs.Back(gd, mode, force)
	if err != nil || x.app.DryRun() {
		return err
	}
	if r.Clash != nil {
		x.sayClash(r.Clash)
		return nil
	}
	if r.Carried {
		x.say("Back on", r.Branch+", taking your uncommitted changes along.")
	} else {
		x.say("Back on", r.Branch+".")
	}
	if setting != nil {
		x.say(settingHint(*setting))
	}
	return x.offerParked(gd, r.Waiting)
}
