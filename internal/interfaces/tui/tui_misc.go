package tui

import (
	"errors"
	"fmt"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repoapp "github.com/macdipu/gitswitch/internal/features/repository/application"
)

// extState is what the redo, detached HEAD, signing and init screens keep
// between keys (one field of tui, so the main struct stays small).
type extState struct {
	plain  bool                         // run commands without leaving the screen (tests)
	banner string                       // git missing or too old: shown on every page
	redo   string                       // the label of what Redo would do; "" when nothing
	det    refs.DetachedState           // the History repo's detached state
	sigs   map[string]history.Signature // commit signatures read so far, by hash
	tried  map[string]bool              // hashes already asked about
}

// refreshExt reads what the History page's header and footer show, after
// the history was read.
func (t *tui) refreshExt() {
	t.ext.redo, t.ext.det = "", refs.DetachedState{}
	if t.histDir == "" {
		return
	}
	if st, err := t.app.Undo.NextRedo(t.histDir); err == nil {
		t.ext.redo = history.CleanText(st.Label)
	}
	if d, err := t.app.Refs.DetachedState(t.histDir); err == nil {
		t.ext.det = d
	}
}

// ---------------- git check ----------------

// gitBanner checks git once, at the start: when it is missing or too old,
// every page says so.
func (t *tui) gitBanner() {
	if g := t.app.Repos.GitInfo(); !g.OK {
		t.ext.banner = g.Why
	}
}

// banner is the line under the page tabs.
func (t *tui) banner() string {
	if t.ext.banner == "" {
		return ""
	}
	return " " + cRed + history.CleanText(t.ext.banner) + cReset
}

// ---------------- redo ----------------

// redoLast does again what the last Undo took back, after asking.
func (t *tui) redoLast() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	st, err := t.app.Undo.NextRedo(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	label := history.CleanText(st.Label)
	if st.Blocked != "" {
		t.say(fmt.Errorf("can't redo “%s”: %s", label, st.Blocked), "")
		return
	}
	t.confirm = &confirmBox{"Redo “" + label + "”? " + st.Effect + " (y/n)", map[string]func() error{"y": func() error {
		if _, err := t.app.Undo.Redo(gd); err != nil {
			return err
		}
		t.marked = map[string]bool{}
		t.loadHistory()
		t.say(nil, "Redone: "+label+". U takes it back again.")
		return nil
	}}}
}

// ---------------- detached HEAD ----------------

// detachedLines is the History page's banner for a detached HEAD.
func (t *tui) detachedLines() []string {
	d := t.ext.det
	if !d.Detached {
		return nil
	}
	back := ""
	if d.Back != "" {
		back = "; E = Back to " + d.Back
	} else {
		back = "; switch to a branch with b"
	}
	l := "\x00 " + cYellow + "detached at " + d.Short + back + cReset
	if d.Orphans > 0 {
		l += "  " + cRed + plural(d.Orphans, "commit") + " made here on no branch (n makes a branch to keep them)" + cReset
	}
	return []string{l}
}

// checkoutForm asks which commit, tag or remote branch to look at.
func (t *tui) checkoutForm(gd string) {
	t.form = &form{title: "Check out a commit or tag with HEAD detached", fields: []field{
		{label: "Commit, tag or remote branch", value: shortOf(t.selectedCommit()), hint: "e.g. v1.2.0 or abc1234; Back returns to your branch"},
	}, submit: func(v []string) error {
		if v[0] == "" {
			return errors.New("type a commit, a tag or a remote branch")
		}
		t.checkout(gd, v[0], false)
		return nil
	}}
}

func shortOf(h string) string { return h[:min(7, len(h))] }

// checkout goes to rev with a detached HEAD, asking what to do with
// uncommitted changes as a switch does.
func (t *tui) checkout(gd, rev string, force bool) {
	h, err := t.app.Refs.Head(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if h.Changes.Any() && h.Busy == "" {
		what := strings.TrimPrefix(h.Changes.Describe(), "uncommitted: ")
		acts := map[string]func() error{"s": func() error { t.doCheckout(gd, rev, "smart", force); return nil }}
		q := fmt.Sprintf("You have uncommitted changes (%s).  s = smart: take them along to %s (if they clash, nothing changes)", what, rev)
		if h.Branch != "" {
			acts["p"] = func() error { t.doCheckout(gd, rev, "park", force); return nil }
			q += " · p = park them on " + h.Branch + " (git stash), restored when you come back"
		}
		t.confirm = &confirmBox{q + " · n or Esc = cancel", acts}
		return
	}
	t.doCheckout(gd, rev, "", force)
}

func (t *tui) doCheckout(gd, rev, mode string, force bool) {
	d, err := t.app.Refs.CheckoutDetached(gd, rev, mode, force)
	t.loadHistory()
	if err != nil && !force && strings.Contains(err.Error(), "with force") {
		t.confirm = &confirmBox{err.Error() + "  f = go on anyway · n or Esc = cancel",
			map[string]func() error{"f": func() error { t.doCheckout(gd, rev, mode, true); return nil }}}
		return
	}
	if err != nil {
		t.say(err, "")
		return
	}
	msg := d.Words
	if d.Parked {
		msg += " Your changes are parked on " + d.From + "."
	}
	t.say(nil, msg)
}

// backToBranch leaves a detached HEAD for the branch it came from, with
// the warning when commits made here are on no branch.
func (t *tui) backToBranch(gd string) {
	st, err := t.app.Refs.DetachedState(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if !st.Detached {
		t.say(errors.New("HEAD is on a branch, not detached: there is nothing to go back from"), "")
		return
	}
	if st.Back == "" {
		t.say(errors.New("GitSwitch doesn't know which branch this came from; switch to one with b"), "")
		return
	}
	if st.Orphans > 0 {
		t.confirm = &confirmBox{fmt.Sprintf("%s you made here (HEAD is detached) %s on no branch. Going back to %s would hide them: only the reflog would still find them. Keep them first with n (new branch here). f = go back anyway · n or Esc = stay",
			plural(st.Orphans, "commit"), map[bool]string{true: "is", false: "are"}[st.Orphans == 1], st.Back),
			map[string]func() error{"f": func() error { t.goBack(gd, true); return nil }}}
		return
	}
	t.goBack(gd, false)
}

func (t *tui) goBack(gd string, force bool) {
	h, _ := t.app.Refs.Head(gd)
	if h.Changes.Any() && h.Busy == "" {
		what := strings.TrimPrefix(h.Changes.Describe(), "uncommitted: ")
		t.confirm = &confirmBox{fmt.Sprintf("You have uncommitted changes (%s).  s = smart: take them back with you · p = park them on the branch you return to · n or Esc = cancel", what),
			map[string]func() error{
				"s": func() error { t.doBack(gd, "smart", force); return nil },
				"p": func() error { t.doBack(gd, "park", force); return nil },
			}}
		return
	}
	t.doBack(gd, "", force)
}

func (t *tui) doBack(gd, mode string, force bool) {
	r, err := t.app.Refs.Back(gd, mode, force)
	t.loadHistory()
	if err != nil {
		t.say(err, "")
		return
	}
	if r.Clash != nil {
		t.say(nil, fmt.Sprintf("Back on %s, but %d file(s) clash: %s. Fix them in your editor, then press R.", r.Branch, len(r.Clash.Files), strings.Join(r.Clash.Files, ", ")))
		return
	}
	t.say(nil, "Back on "+r.Branch+".")
	if len(r.Waiting) > 0 {
		t.offerParked(gd, r.Waiting)
	}
}

// ---------------- init ----------------

// initForm starts a repo (n on Repos): a folder, a first branch and the
// account, by the rules unless chosen.
func (t *tui) initForm() {
	if g := t.app.Repos.GitInfo(); !g.OK {
		t.say(errors.New(g.Why), "")
		return
	}
	opts := []string{"By rules"}
	for _, a := range t.cfg.Accounts {
		opts = append(opts, a.ID)
	}
	folder := ""
	if len(t.cfg.ScanFolders) > 0 {
		folder = strings.TrimRight(t.cfg.ScanFolders[0], "/\\") + "/"
	}
	t.form = &form{title: "Start a new repo (git init)", fields: []field{
		{label: "Folder", value: folder, hint: "made when it does not exist"},
		{label: "First branch", hint: "empty: git's default"},
		{label: "Account", value: opts[0], options: opts, hint: "←→ by rules, or one of your accounts"},
	}, submit: func(v []string) error {
		if v[0] == "" {
			return errors.New("type the folder for the new repo")
		}
		acct := ""
		if v[2] != opts[0] {
			acct = v[2]
		}
		t.initRepo(v[0], v[1], acct)
		return nil
	}}
}

func (t *tui) initRepo(folder, branch, acct string) {
	info, err := t.app.Repos.Init(folder, acct, branch)
	if errors.Is(err, repoapp.ErrChooseAccount) {
		var opts []string
		var ids []string
		for _, a := range t.cfg.Accounts {
			opts, ids = append(opts, a.ID+"  "+a.Email), append(ids, a.ID)
		}
		t.picker = &picker{title: "Which account does this repo use?", options: opts, pick: func(i int) error {
			t.initRepo(folder, branch, ids[i])
			return nil
		}}
		t.say(nil, "No rule or default decides the account for this folder: choose one.")
		return
	}
	if err != nil {
		t.say(err, "")
		return
	}
	t.reload()
	t.scanRepos()
	t.keepSelected(info.Path)
	msg := "Started a repo in " + info.Path + "."
	if info.Account != "" {
		msg += " It uses " + info.Account + " (" + info.Reason + ")."
	} else {
		msg += " No accounts yet, so no commit name or email is set: add one on Accounts (a)."
	}
	t.say(nil, msg)
}

// redoFooter says in the History footer that Y redoes what U took back.
func (t *tui) redoFooter() string {
	if t.ext.redo == "" {
		return ""
	}
	return " · Y redo"
}
