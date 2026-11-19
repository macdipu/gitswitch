package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// The "Remotes and more" menu (O on History): remotes, deleting a branch on
// the server, fetch with prune, force push with lease, and checking out a
// commit with a detached HEAD.

// run leaves the screen while a command that talks to the server runs, so
// git's progress and any question show in the terminal.
func (t *tui) run(f func()) {
	if t.ext.plain {
		f()
		return
	}
	t.suspend(f)
}

func progressLine(l string) { fmt.Print("\r\x1b[K  " + l) }

func shortHash(h string) string { return h[:min(7, len(h))] }

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func (t *tui) remotesMenu() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	gd := t.histDir
	var opts []string
	var acts []func() error
	add := func(label string, f func() error) {
		opts, acts = append(opts, label), append(acts, f)
	}
	add("Remotes: list, add, rename, change URL, remove…", func() error { t.remotesList(gd); return nil })
	add("Delete a branch on the server…", func() error { t.deleteOnServer(gd); return nil })
	add("Fetch and forget deleted branches", func() error { t.fetchPrune(gd); return nil })
	add("Force push with lease…", func() error { t.forcePush(gd); return nil })
	add("Check out a commit or tag (detached)…", func() error { t.checkoutForm(gd); return nil })
	if t.ext.det.Detached {
		back := t.ext.det.Back
		if back == "" {
			back = "the branch you came from"
		}
		add("Back to "+back, func() error { t.backToBranch(gd); return nil })
	}
	t.picker = &picker{title: "Remotes and more", options: opts, pick: func(i int) error { return acts[i]() }}
}

// ---------------- remotes ----------------

func (t *tui) remotesList(gd string) {
	rs, err := t.app.Refs.Remotes(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	opts := []string{"+ Add a remote…"}
	for _, r := range rs {
		chip := cDim + "no account for " + r.Host + cReset
		if r.Account != "" {
			chip = t.color(r.Account) + "█ " + r.Account + cReset
		}
		o := fmt.Sprintf("%-10s %s  %s", r.Name, history.CleanText(r.FetchURL), chip)
		if r.Upstream {
			o += cDim + "  · this branch pulls from it" + cReset
		}
		opts = append(opts, o)
	}
	t.picker = &picker{title: "Remotes (Enter: rename, change URL or remove)", options: opts, pick: func(i int) error {
		if i == 0 {
			t.addRemoteForm(gd, len(rs) == 0)
			return nil
		}
		t.remoteActions(gd, rs[i-1])
		return nil
	}}
}

func (t *tui) addRemoteForm(gd string, first bool) {
	name := ""
	if first {
		name = "origin"
	}
	t.form = &form{title: "Add a remote", fields: []field{
		{label: "Name", value: name, hint: "e.g. origin, upstream"},
		{label: "URL", hint: "git@github.com:owner/repo.git or https://…"},
	}, submit: func(v []string) error {
		c, err := t.app.Refs.AddRemote(gd, v[0], v[1])
		if err != nil {
			return err
		}
		msg := "Added the remote " + c.Name + "."
		if c.Account != "" {
			msg += " It uses the account " + c.Account + "."
		}
		if c.Note != "" {
			msg += " " + c.Note + "."
		}
		t.say(nil, msg+" To remove it again: gitswitch remote remove "+c.Name)
		return nil
	}}
}

func (t *tui) remoteActions(gd string, r refs.Remote) {
	as := "no account is set up for " + r.Host
	if r.Account != "" {
		as = "pushes as " + r.Account
	}
	tracked := ""
	if len(r.Tracking) > 0 {
		tracked = " Tracked by: " + strings.Join(r.Tracking, ", ") + "."
	}
	t.confirm = &confirmBox{fmt.Sprintf("%s (%s, %s).%s  r = rename · u = change the URL · x = remove it from this clone · n or Esc = close", r.Name, history.CleanText(r.FetchURL), as, tracked),
		map[string]func() error{
			"r": func() error {
				t.form = &form{title: "Rename " + r.Name, fields: []field{{label: "New name", value: r.Name}}, submit: func(v []string) error {
					c, err := t.app.Refs.RenameRemote(gd, r.Name, v[0])
					if err != nil {
						return err
					}
					msg := "Renamed " + r.Name + " to " + c.NewName + "."
					if c.Note != "" {
						msg += " " + c.Note
					}
					t.say(nil, msg)
					return nil
				}}
				return nil
			},
			"u": func() error {
				t.form = &form{title: "Change the URL of " + r.Name, fields: []field{{label: "URL", value: r.FetchURL}}, submit: func(v []string) error {
					c, err := t.app.Refs.SetRemoteURL(gd, r.Name, v[0])
					if err != nil {
						return err
					}
					msg := r.Name + " now points at " + c.URL + "."
					if c.Account != "" {
						msg += " It uses the account " + c.Account + "."
					}
					if c.Note != "" {
						msg += " " + c.Note + "."
					}
					t.say(nil, msg+" It was "+c.Was+".")
					return nil
				}}
				return nil
			},
			"x": func() error { t.removeRemote(gd, r); return nil },
		}}
}

func (t *tui) removeRemote(gd string, r refs.Remote) {
	t.confirm = &confirmBox{"Remove the remote " + r.Name + " from this clone? Its remote-tracking branches go; the server is untouched (add it back: gitswitch remote add " + r.Name + " " + history.CleanText(r.FetchURL) + "). (y/n)",
		map[string]func() error{"y": func() error {
			c, err := t.app.Refs.RemoveRemote(gd, r.Name, false)
			if err != nil && len(c.Branches) > 0 {
				// Refused because branches track it: name them and ask again.
				t.confirm = &confirmBox{fmt.Sprintf("%s pull from %s. Removing it leaves them without an upstream: no update and no push until you set one. f = remove it anyway · n or Esc = keep it", strings.Join(c.Branches, ", "), r.Name),
					map[string]func() error{"f": func() error {
						c, err := t.app.Refs.RemoveRemote(gd, r.Name, true)
						if err == nil {
							t.say(nil, "Removed the remote "+r.Name+"; "+strings.Join(c.Branches, ", ")+" lost their upstream. "+c.Note)
						}
						return err
					}}}
				return nil
			}
			if err == nil {
				t.say(nil, "Removed the remote "+r.Name+". "+c.Note)
			}
			return err
		}}}
}

// ---------------- delete on the server, prune ----------------

func (t *tui) deleteOnServer(gd string) {
	bs, err := t.app.Refs.Branches(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	var names []string
	for _, b := range bs {
		if b.Remote && !strings.HasSuffix(b.Name, "/HEAD") {
			names = append(names, b.Name)
		}
	}
	if len(names) == 0 {
		t.say(fmt.Errorf("this clone has no remote branches (fetch first: F)"), "")
		return
	}
	t.picker = &picker{title: "Delete which branch on the server?", options: names, pick: func(i int) error {
		t.deleteRemoteBranch(gd, names[i], false)
		return nil
	}}
}

func (t *tui) deleteRemoteBranch(gd, ref string, force bool) {
	d, err := t.app.Refs.CanDeleteRemoteBranch(gd, ref, force)
	if err != nil {
		if !force && d.Hash != "" {
			t.confirm = &confirmBox{err.Error() + "  f = go on anyway (force) · n or Esc = cancel",
				map[string]func() error{"f": func() error { t.deleteRemoteBranch(gd, ref, true); return nil }}}
			return
		}
		t.say(err, "")
		return
	}
	as := ""
	if d.Account != "" {
		as = " as " + d.Account
	}
	tracking := ""
	if len(d.Tracking) > 0 {
		tracking = " " + strings.Join(d.Tracking, ", ") + " pull from it and would point at a branch that is gone."
	}
	t.confirm = &confirmBox{fmt.Sprintf("Delete the branch %s on %s%s? It is at %s; everyone who uses it loses it there.%s To put it back: %s (y/n)",
		d.Branch, d.Remote, as, shortHash(d.Hash), tracking, d.Restore), map[string]func() error{"y": func() error {
		var res refs.RemoteBranchDeleted
		var err error
		t.run(func() {
			fmt.Println("Deleting on the server ...")
			res, err = t.app.Refs.DeleteRemoteBranch(gd, ref, force, progressLine)
			fmt.Println()
			if err != nil {
				fmt.Println("gitswitch:", err)
			}
		})
		t.loadHistory()
		if err != nil {
			return err
		}
		t.say(nil, fmt.Sprintf("Deleted the branch %s on %s (it was at %s). To put it back: %s (the server's copy can't be undone with U)", res.Branch, res.Remote, shortHash(res.Hash), res.Restore))
		return nil
	}}}
}

func (t *tui) fetchPrune(gd string) {
	var f refs.Fetched
	var err error
	t.run(func() {
		fmt.Println("Fetching, and forgetting branches deleted on the server ...")
		f, err = t.app.Refs.FetchWith(gd, true, progressLine)
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	t.loadHistory()
	if err != nil {
		t.say(err, "")
		return
	}
	if len(f.Pruned) == 0 {
		t.say(nil, "Fetched: nothing was deleted on the server.")
		return
	}
	lines := []string{"Forgotten (deleted on the server; only your copies of them went):"}
	for _, p := range f.Pruned {
		lines = append(lines, "  "+history.CleanText(p))
	}
	if len(f.Gone) > 0 {
		lines = append(lines, "", "These local branches pulled from them and have no remote branch now: "+strings.Join(f.Gone, ", "))
	}
	t.view = &viewer{title: "Fetched and forgot", lines: lines}
	t.say(nil, fmt.Sprintf("Forgot %s deleted on the server.", plural(len(f.Pruned), "branch")))
}

// ---------------- force push with lease ----------------

// forcePush shows what a force push would overwrite, runs the account
// check as Push does, and needs a typed yes.
func (t *tui) forcePush(gd string) {
	p, err := t.app.Refs.PushPlan(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if !p.CanForce {
		t.say(errors.New(p.Why), "")
		return
	}
	if ch, err := t.app.History.CheckAccount(gd); err == nil && ch.Wrong > 0 {
		t.confirm = &confirmBox{fmt.Sprintf("%s  f = fix them as %s first, then go on · y = go on as they are · n or Esc = cancel", ch.Describe(), ch.Account),
			map[string]func() error{
				"f": func() error {
					if _, err := t.app.History.FixAuthor(gd); err != nil {
						return err
					}
					t.loadHistory()
					t.forceAsk(gd)
					return nil
				},
				"y": func() error { t.forceAsk(gd); return nil },
			}}
		return
	}
	t.forceAsk(gd)
}

func (t *tui) forceAsk(gd string) {
	p, err := t.app.Refs.PushPlan(gd) // read again: fixing authors changes the commits
	if err != nil {
		t.say(err, "")
		return
	}
	if !p.CanForce {
		t.say(errors.New(p.Why), "")
		return
	}
	over, up := len(p.ToOverwrite)+p.MoreOverwrite, len(p.ToPush)+p.MorePush
	var subj []string
	for i, l := range p.ToOverwrite {
		if i == 3 {
			subj = append(subj, "…")
			break
		}
		subj = append(subj, shortHash(l.Hash)+" "+history.CleanText(l.Subject))
	}
	as := ""
	if p.Account != "" {
		as = " as " + p.Account
	}
	expect := p.Expect
	t.confirm = &confirmBox{fmt.Sprintf("Force push %s to %s%s? It would overwrite %s on %s that are not in %s (%s) with your %s. It goes only if %s is still at %s. Other people's work can be lost.  y = go on (you then type yes) · l = list the commits · n or Esc = cancel",
		p.Branch, p.Upstream, as, plural(over, "commit"), p.Upstream, p.Branch, strings.Join(subj, "; "), plural(up, "commit"), p.Upstream, shortHash(expect)),
		map[string]func() error{
			"l": func() error { t.forcePlanView(p); return nil },
			"y": func() error {
				t.form = &form{title: "Force push " + p.Branch + " to " + p.Upstream + ": this overwrites " + plural(over, "commit") + " there",
					fields: []field{{label: "Type yes to force push", hint: "anything else cancels"}},
					submit: func(v []string) error {
						if strings.ToLower(v[0]) != "yes" {
							t.say(nil, "Cancelled: nothing was pushed.")
							return nil
						}
						return t.doForcePush(gd, p)
					}}
				return nil
			},
		}}
}

func (t *tui) forcePlanView(p refs.PushPlan) {
	var lines []string
	list := func(what string, ls []refs.Line, more int) {
		lines = append(lines, what)
		for _, l := range ls {
			lines = append(lines, fmt.Sprintf("  %s  %s  (%s, %s)", l.Short, history.CleanText(l.Subject), history.CleanText(l.Author), refs.Ago(time.Now().Unix(), l.Time)))
		}
		if more > 0 {
			lines = append(lines, fmt.Sprintf("  … and %d more", more))
		}
		lines = append(lines, "")
	}
	list(fmt.Sprintf("Would be overwritten: on %s, not in %s, as of your last fetch (%s):", p.Upstream, p.Branch, plural(len(p.ToOverwrite)+p.MoreOverwrite, "commit")), p.ToOverwrite, p.MoreOverwrite)
	list(fmt.Sprintf("Yours, which go up instead (%s):", plural(len(p.ToPush)+p.MorePush, "commit")), p.ToPush, p.MorePush)
	who := "the account git asks for"
	if p.Account != "" {
		who = p.Account
	}
	lines = append(lines, "Pushed as "+who+". The push goes only if "+p.Upstream+" is still at "+shortHash(p.Expect)+", where your last fetch saw it.", "Press O, then Force push with lease, to go on.")
	t.view = &viewer{title: "Force push " + p.Branch, lines: lines}
}

func (t *tui) doForcePush(gd string, p refs.PushPlan) error {
	var r refs.Pushed
	var err error
	t.run(func() {
		fmt.Println("Force pushing (with lease) ...")
		r, err = t.app.Refs.ForcePushLease(gd, p.Expect, true, progressLine)
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	t.loadHistory()
	if err != nil {
		if strings.Contains(err.Error(), "since you fetched") {
			return fmt.Errorf("someone pushed to %s since you looked: fetch and look at what they added first (F), then force push again. Nothing was overwritten", p.Upstream)
		}
		return err
	}
	as := " with the account git asked for"
	if r.Account != "" {
		as = " as " + r.Account
	}
	t.say(nil, fmt.Sprintf("Force pushed %s to %s%s. The branch was at %s; to put that back: git push --force-with-lease %s %s:refs/heads/%s",
		plural(r.Commits, "commit"), r.Upstream, as, shortHash(p.Expect), r.Remote, p.Expect, strings.TrimPrefix(r.Upstream, r.Remote+"/")))
	return nil
}
