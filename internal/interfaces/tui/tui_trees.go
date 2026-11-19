package tui

// Worktrees (key W on History) and submodules (key V): more working
// folders of the repo, and the repos inside it. Both are lists with a few
// actions each, asking before anything that deletes or moves.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	sub "github.com/macdipu/gitswitch/internal/features/submodule/domain"
)

// ---------------- worktrees ----------------

// worktreeView is the History page's list of the repo's worktrees.
type worktreeView struct {
	gd  string
	all []refs.Worktree
	sel int
}

// openWorktrees shows the selected repo's worktrees.
func (t *tui) openWorktrees() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	t.wtView = &worktreeView{gd: t.histDir}
	t.msg = ""
	t.loadWorktrees()
}

// loadWorktrees reads the list again, keeping the selection on the same
// folder.
func (t *tui) loadWorktrees() {
	v := t.wtView
	if v == nil {
		return
	}
	was := ""
	if v.sel < len(v.all) {
		was = v.all[v.sel].Path
	}
	all, err := t.app.Refs.Worktrees(v.gd)
	if err != nil {
		t.wtView = nil
		t.say(err, "")
		return
	}
	v.all = all
	for i, w := range all {
		if w.Path == was {
			v.sel = i
			return
		}
	}
	v.sel = max(0, min(v.sel, len(all)-1))
}

func (t *tui) closeWorktrees() {
	t.wtView = nil
	sel := t.sel[tabHistory]
	t.readHistory()
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

func (t *tui) keyWorktrees(k string) bool {
	v := t.wtView
	n := len(v.all)
	var w *refs.Worktree
	if v.sel < n {
		w = &v.all[v.sel]
	}
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.closeWorktrees()
	case "up", "k":
		v.sel = max(0, v.sel-1)
	case "down", "j":
		v.sel = max(0, min(n-1, v.sel+1))
	case "pgup":
		v.sel = max(0, v.sel-max(1, t.h-6))
	case "pgdn":
		v.sel = max(0, min(n-1, v.sel+max(1, t.h-6)))
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = max(0, n-1)
	case "r":
		t.loadWorktrees()
	case "a":
		t.worktreeAdd(v.gd)
	case "x":
		if w != nil {
			t.worktreeRemove(v.gd, *w)
		}
	case "p":
		t.worktreePrune(v.gd)
	case "l":
		if w != nil {
			t.worktreeLock(v.gd, *w)
		}
	case "u":
		if w != nil {
			if err := t.app.Refs.WorktreeUnlock(v.gd, w.Path); err != nil {
				t.refusal("Can't unlock", err)
			} else {
				t.loadWorktrees()
				t.say(nil, "Unlocked "+w.Path+".")
			}
		}
	case "enter":
		if w != nil && !w.Prunable && !w.Bare {
			gd, err := t.app.Repos.For(w.Path)
			if err != nil {
				t.say(err, "")
				return true
			}
			t.wtView = nil
			t.openHistory(gd)
		}
	}
	return true
}

func (t *tui) drawWorktrees() ([]string, int) {
	v := t.wtView
	path := v.gd
	if t.hist != nil {
		path = t.hist.Repo.Path
	}
	l := []string{"\x00 " + cBold + "Worktrees" + cReset + "  " + path}
	if len(v.all) > 0 && v.all[0].Account != "" {
		l = append(l, "\x00 "+cDim+"all of them use the account "+cReset+t.color(v.all[0].Account)+v.all[0].Account+cReset)
	}
	l = append(l, "\x00")
	if len(v.all) == 0 {
		return append(l, " No worktrees."), -1
	}
	for i, w := range v.all {
		on := i == v.sel
		what := history.CleanText(w.Branch)
		switch {
		case w.Bare:
			what = "(bare)"
		case w.Detached:
			what = "(detached " + w.Short + ")"
		default:
			what += "  " + cDim + w.Short + cReset
		}
		var notes []string
		if w.Main {
			notes = append(notes, "main folder")
		}
		if w.Changes != nil {
			if d := strings.TrimPrefix(w.Changes.Describe(), "uncommitted: "); d != "" {
				notes = append(notes, cYellow+d+cReset+cDim)
			} else {
				notes = append(notes, "clean")
			}
		}
		if w.Locked {
			r := ""
			if w.LockReason != "" {
				r = ": " + history.CleanText(w.LockReason)
			}
			notes = append(notes, "locked"+r)
		}
		if w.Prunable {
			notes = append(notes, cRed+"folder is gone (p forgets it)"+cReset+cDim)
		}
		line := mark(on) + boldIf(on) + history.CleanText(w.Path) + cReset + "  " + what
		if len(notes) > 0 {
			line += cDim + "  (" + strings.Join(notes, "; ") + ")" + cReset
		}
		l = append(l, line)
	}
	return l, v.sel
}

// worktreeAdd asks where the worktree goes and what it checks out.
func (t *tui) worktreeAdd(gd string) {
	base := gd
	if t.hist != nil {
		base = t.hist.Repo.Path
	}
	const newBranch, oldBranch, detached = "a new branch", "an existing branch", "a detached commit"
	t.form = &form{title: "Add a worktree", fields: []field{
		{label: "Folder", value: base + "-worktree", hint: "a new or empty folder, beside the repo"},
		{label: "Check out", value: newBranch, options: []string{newBranch, oldBranch, detached}, hint: "←→ to change"},
		{label: "Branch name or commit", hint: "the new branch's name, an existing branch, or a commit"},
		{label: "New branch starts at", hint: "only for a new branch; empty = where HEAD is"},
		{label: "Add to the repo list", value: "yes", options: []string{"yes", "no"}, hint: "show it on the Repos page too"},
	}, submit: func(v []string) error {
		var o refs.WorktreeAddOptions
		switch v[1] {
		case newBranch:
			o.NewBranch, o.From = v[2], v[3]
		case oldBranch:
			o.Branch = v[2]
		default:
			o.Detach = v[2]
		}
		res, err := t.app.Refs.WorktreeAdd(gd, v[0], o)
		if err != nil {
			t.refusal("Can't add the worktree", err)
			return nil
		}
		what := "branch " + res.Branch
		if res.Branch == "" {
			what = "a detached HEAD at " + res.Head
		}
		lines := []string{"Added the worktree " + res.Path + " on " + what + "."}
		if res.Account != "" {
			lines = append(lines, "It uses the account "+res.Account+", like the repo.")
		}
		for _, w := range res.Warnings {
			lines = append(lines, "Warning: "+w)
		}
		if v[4] == "yes" {
			if info, err := t.app.Repos.Add(res.Path); err != nil {
				lines = append(lines, "It was not added to the repo list: "+err.Error())
			} else {
				lines = append(lines, "Added it to the repo list: "+info.Path)
				t.scanned = false
			}
		}
		lines = append(lines, "Undo: x removes it again (its branch stays).")
		t.loadWorktrees()
		t.say(nil, "Added the worktree "+res.Path+".")
		if len(lines) > 2 {
			t.view = &viewer{title: "Worktree added", lines: lines}
		}
		return nil
	}}
}

// worktreeRemove asks, then removes. When the worktree holds uncommitted
// work it says what would be lost and asks again for a forced removal.
func (t *tui) worktreeRemove(gd string, w refs.Worktree) {
	if w.Main {
		t.refusal("Can't remove", errors.New("that is the repo's own folder, not a linked worktree; it can't be removed here"))
		return
	}
	var done func(force bool) func() error
	done = func(force bool) func() error {
		return func() error {
			if err := t.app.Refs.WorktreeRemove(gd, w.Path, force); err != nil {
				if !force && !w.Locked && w.Changes != nil && w.Changes.Any() {
					t.confirm = &confirmBox{fmt.Sprintf("%s has uncommitted work that would be lost: %s. The files in it are deleted for good. F = remove it anyway · n or Esc = cancel",
						w.Path, strings.TrimPrefix(w.Changes.Describe(), "uncommitted: ")), map[string]func() error{"F": done(true)}}
					return nil
				}
				t.refusal("Can't remove", err)
				return nil
			}
			t.loadWorktrees()
			t.say(nil, "Removed the worktree "+w.Path+". Its branch is kept.")
			return nil
		}
	}
	t.confirm = &confirmBox{"Remove the worktree " + w.Path + "? Its folder is deleted; the branch and commits stay. (y/n)", map[string]func() error{"y": done(false)}}
}

// worktreePrune shows the worktrees whose folder is gone, then asks.
func (t *tui) worktreePrune(gd string) {
	plan, err := t.app.Refs.WorktreePrunePlan(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if len(plan) == 0 {
		t.say(nil, "Nothing to prune: every worktree's folder is there.")
		return
	}
	lines := []string{fmt.Sprintf("Forget %d worktree(s)? No files are touched; the branches stay. (y/n)", len(plan)), "These have no folder any more:"}
	for _, w := range plan {
		lines = append(lines, "  "+history.CleanText(w.Path)+"  "+history.CleanText(w.Branch))
	}
	t.confirm = &confirmBox{strings.Join(capped(lines, t.overlayRows()-1), "\n"), map[string]func() error{"y": func() error {
		got, err := t.app.Refs.WorktreePrune(gd)
		if err != nil {
			t.refusal("Can't prune", err)
			return nil
		}
		t.loadWorktrees()
		t.say(nil, fmt.Sprintf("Forgot %d worktree(s).", len(got)))
		return nil
	}}}
}

// worktreeLock asks for a reason (optional) and locks.
func (t *tui) worktreeLock(gd string, w refs.Worktree) {
	if w.Locked {
		t.say(nil, w.Path+" is locked already. u unlocks it.")
		return
	}
	t.form = &form{title: "Lock " + filepath.Base(w.Path), fields: []field{{label: "Reason", hint: "optional; a locked worktree can't be pruned or removed"}},
		submit: func(v []string) error {
			if err := t.app.Refs.WorktreeLock(gd, w.Path, v[0]); err != nil {
				t.refusal("Can't lock", err)
				return nil
			}
			t.loadWorktrees()
			t.say(nil, "Locked "+w.Path+": it can't be pruned or removed. u unlocks it.")
			return nil
		}}
}

// ---------------- submodules ----------------

// submoduleView is the History page's list of the repo's submodules.
type submoduleView struct {
	gd  string
	all []sub.Entry
	sel int
}

func (t *tui) openSubmodules() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	t.subView = &submoduleView{gd: t.histDir}
	t.msg = ""
	t.loadSubmodules()
}

func (t *tui) loadSubmodules() {
	v := t.subView
	if v == nil {
		return
	}
	was := ""
	if v.sel < len(v.all) {
		was = v.all[v.sel].Path
	}
	all, err := t.app.Submodules.List(v.gd)
	if err != nil {
		t.subView = nil
		t.say(err, "")
		return
	}
	v.all = all
	for i, e := range all {
		if e.Path == was {
			v.sel = i
			return
		}
	}
	v.sel = max(0, min(v.sel, len(all)-1))
}

func (t *tui) closeSubmodules() {
	t.subView = nil
	sel := t.sel[tabHistory]
	t.readHistory()
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

func (t *tui) keySubmodules(k string) bool {
	v := t.subView
	n := len(v.all)
	path := "" // the selected submodule; capital letters act on every submodule
	if v.sel < n {
		path = v.all[v.sel].Path
	}
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.closeSubmodules()
	case "up", "k":
		v.sel = max(0, v.sel-1)
	case "down", "j":
		v.sel = max(0, min(n-1, v.sel+1))
	case "pgup":
		v.sel = max(0, v.sel-max(1, t.h-6))
	case "pgdn":
		v.sel = max(0, min(n-1, v.sel+max(1, t.h-6)))
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = max(0, n-1)
	case "r":
		t.loadSubmodules()
	case "i", "I":
		t.submoduleInit(v.gd, pathFor(k == "I", path))
	case "u", "U":
		t.submoduleUpdate(v.gd, pathFor(k == "U", path), false)
	case "m", "M":
		t.submoduleUpdate(v.gd, pathFor(k == "M", path), true)
	case "s":
		r, err := t.app.Submodules.Sync(v.gd)
		if err != nil {
			t.refusal("Can't sync", err)
		} else {
			t.loadSubmodules()
			t.say(nil, r.Message+".")
		}
	case "a":
		t.submoduleAdd(v.gd)
	case "x":
		if path != "" {
			t.submoduleRemove(v.gd, path)
		}
	case "enter":
		if v.sel < n {
			t.showSubmodule(v.all[v.sel])
		}
	}
	return true
}

// pathFor is the path an action runs on: "" (every submodule) for the
// capital letter.
func pathFor(all bool, path string) string {
	if all {
		return ""
	}
	return path
}

func subShort(h string) string {
	if h == "" {
		return "-------"
	}
	return h[:min(7, len(h))]
}

func (t *tui) drawSubmodules() ([]string, int) {
	v := t.subView
	path := v.gd
	if t.hist != nil {
		path = t.hist.Repo.Path
	}
	l := []string{"\x00 " + cBold + "Submodules" + cReset + "  " + path, "\x00"}
	if len(v.all) == 0 {
		return append(l, " This repo has no submodules. a adds one; Esc goes back to the commits."), -1
	}
	hdr, selLine := len(l), -1
	for i, e := range v.all {
		on := i == v.sel
		if on {
			selLine = len(l) - hdr
		}
		state, col := e.State, cYellow
		switch e.State {
		case sub.UpToDate:
			col = cGreen
		case sub.Conflicts:
			col = cRed
		}
		at := e.CheckedOut
		if !e.Initialized {
			at = e.Recorded
		}
		l = append(l, fmt.Sprintf("%s%s%s%s  %s%s%s  %s%s%s", mark(on), boldIf(on), history.CleanText(e.Path), cReset, cDim, subShort(at), cReset, col, state, cReset))
		var more []string
		if e.Dirty > 0 {
			more = append(more, fmt.Sprintf("%d changed file(s) inside", e.Dirty))
		}
		if e.Upstream != "" && (e.Ahead > 0 || e.Behind > 0) {
			more = append(more, fmt.Sprintf("%d ahead, %d behind %s", e.Ahead, e.Behind, history.CleanText(e.Upstream)))
		}
		if e.Branch != "" {
			more = append(more, "tracks "+history.CleanText(e.Branch))
		}
		if e.Account != "" {
			more = append(more, "account "+e.Account)
		} else {
			more = append(more, "no account picked: git asks")
		}
		l = append(l, "    "+cDim+history.CleanText(e.URL)+" · "+strings.Join(more, " · ")+cReset)
	}
	return l, selLine
}

func (t *tui) showSubmodule(e sub.Entry) {
	lines := []string{"Path:       " + e.Path, "URL:        " + e.URL, "State:      " + e.State,
		"Recorded:   " + subShort(e.Recorded) + " (the commit this repo points at)", "Checked out: " + subShort(e.CheckedOut)}
	if e.Branch != "" {
		lines = append(lines, "Tracks:     "+e.Branch)
	}
	if e.Dirty > 0 {
		lines = append(lines, fmt.Sprintf("Inside:     %d changed file(s)", e.Dirty))
	}
	if e.Upstream != "" {
		lines = append(lines, fmt.Sprintf("Compared:   %d ahead, %d behind %s", e.Ahead, e.Behind, e.Upstream))
	}
	if e.Account != "" {
		lines = append(lines, "Account:    "+e.Account+" ("+e.AccountReason+")")
	} else {
		lines = append(lines, "Account:    none picked by your rules: git asks")
	}
	t.view = &viewer{title: "Submodule " + e.Path, lines: lines}
}

func (t *tui) submoduleInit(gd, path string) {
	r, err := t.app.Submodules.Init(gd, path)
	if err != nil {
		t.refusal("Can't initialize", err)
		return
	}
	t.loadSubmodules()
	t.say(nil, r.Message+".")
}

// submoduleUpdate asks, then updates to the recorded commits, or with
// remote to the tip of the tracked branch (which changes what the repo
// records).
func (t *tui) submoduleUpdate(gd, path string, remote bool) {
	what := "every submodule"
	if path != "" {
		what = path
	}
	q := "Update " + what + " to the commit(s) this repo records? Refused when it would overwrite changes inside a submodule. (y/n)"
	if remote {
		q = "Move " + what + " to the tip of its tracked branch? The repo will then record a different commit for it: you commit that change. (y/n)"
	}
	t.confirm = &confirmBox{q, map[string]func() error{"y": func() error {
		r, err := t.app.Submodules.Update(gd, path, remote, false)
		if err != nil {
			t.refusal("Can't update", err)
			return nil
		}
		t.loadSubmodules()
		msg := r.Message
		for _, c := range r.Changed {
			msg += fmt.Sprintf("  [%s %s → %s]", c.Path, subShort(c.From), subShort(c.To))
		}
		t.say(nil, msg+".")
		return nil
	}}}
}

// submoduleAdd asks for the URL, folder and branch.
func (t *tui) submoduleAdd(gd string) {
	t.form = &form{title: "Add a submodule", fields: []field{
		{label: "URL", hint: "fetched as the account its owner maps to"},
		{label: "Folder in the repo", hint: "e.g. libs/util"},
		{label: "Branch to track", hint: "optional"},
	}, submit: func(v []string) error {
		r, err := t.app.Submodules.Add(gd, v[0], v[1], v[2])
		if err != nil {
			text := err.Error()
			if r.Note != "" {
				text += "\n\nNote: " + r.Note + "."
			}
			t.refusal("Can't add the submodule", errors.New(text))
			return nil
		}
		lines := []string{r.Message + "."}
		if r.Account != "" {
			lines = append(lines, "It is fetched as the account "+r.Account+".")
		}
		if r.Note != "" {
			lines = append(lines, "Note: "+r.Note+".")
		}
		t.loadSubmodules()
		t.say(nil, r.Message+".")
		t.view = &viewer{title: "Submodule added", lines: lines}
		return nil
	}}
}

// submoduleRemove plans the removal and asks; work inside the submodule
// is asked about again before it is forced.
func (t *tui) submoduleRemove(gd, path string) {
	plan, err := t.app.Submodules.PlanRemove(gd, path, false)
	if err != nil {
		t.refusal("Can't remove", err)
		return
	}
	remove := func(force bool) func() error {
		return func() error {
			r, err := t.app.Submodules.Remove(gd, path, force)
			if err != nil {
				t.refusal("Can't remove", err)
				return nil
			}
			t.loadSubmodules()
			t.say(nil, r.Message+".")
			return nil
		}
	}
	lines := []string{"Removing " + plan.Path + " deletes:"}
	for _, d := range plan.Deleted {
		lines = append(lines, "  - "+history.CleanText(d))
	}
	if plan.Blocked != "" {
		lines = []string{"Can't remove it as it is: " + history.CleanText(plan.Blocked) + ".", "Removing it anyway deletes:"}
		for _, d := range plan.Deleted {
			lines = append(lines, "  - "+history.CleanText(d))
		}
		lines = append(lines, "F = remove it anyway, losing that work · n or Esc = cancel")
		t.confirm = &confirmBox{strings.Join(capped(lines, t.overlayRows()), "\n"), map[string]func() error{"F": remove(true)}}
		return
	}
	lines = append([]string{"Remove " + plan.Path + "? (y/n)"}, lines...)
	t.confirm = &confirmBox{strings.Join(capped(lines, t.overlayRows()), "\n"), map[string]func() error{"y": remove(false)}}
}

// rewriteUI holds the History page's views of this file and tui_rewrite.go,
// while one is open.
type rewriteUI struct {
	rwEd    *rewriteEditor // the interactive rebase editor
	wtView  *worktreeView  // the worktrees
	subView *submoduleView // the submodules
}
