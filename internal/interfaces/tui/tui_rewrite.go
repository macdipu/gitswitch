package tui

// Rewrite history on the History page (key L): rebase onto a branch, the
// interactive rebase editor, reword an older commit, and fold staged
// changes into one. Nothing here changes git itself: it asks the rewrite
// and rebase use cases, which refuse pushed commits and a dirty tree. A
// rebase or rewrite that stops on clashes is finished with the operation
// view (key e), which the merge feature provides.

import (
	"errors"
	"fmt"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
)

// rewriteEditor is the interactive rebase editor: one step per commit,
// oldest first; the order of the steps is the new history.
type rewriteEditor struct {
	gd    string
	since string // "" = the commits not pushed yet
	todo  rewrite.Todo
	steps []rewrite.Step
	sel   int
}

// stopHint is what to do after a rebase or rewrite stopped on clashes.
const stopHint = "Press e for the operation view: fix the files, then continue, skip or abort."

// refusal shows why something can't be done, in a panel (the reasons are
// long sentences).
func (t *tui) refusal(title string, err error) {
	t.msg, t.msgErr = "", false
	t.view = &viewer{title: title, lines: strings.Split(history.CleanText(err.Error()), "\n")}
}

// capped keeps at most n lines, ending with "… and k more" when cut.
func capped(lines []string, n int) []string {
	n = max(n, 2)
	if len(lines) <= n {
		return lines
	}
	return append(append([]string(nil), lines[:n-1]...), fmt.Sprintf("  … and %d more", len(lines)-n+1))
}

// ---------------- the menu ----------------

// rewriteMenu is the L menu.
func (t *tui) rewriteMenu() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	gd := t.histDir
	_, _ = t.app.Rebase.Finish(gd) // a stopped rebase or rewrite that ended since is recorded for undo
	_, _ = t.app.Rewrite.Finish(gd)
	opts := []string{
		"Rebase onto a branch, tag or commit…        put your commits on top of it",
		"Interactive rebase…                          reorder, squash, fixup, reword, drop",
		"Reword a commit…                             a new message for an older commit",
		"Fold staged changes into an older commit…    a fixup, folded in",
	}
	t.picker = &picker{title: "Rewrite history (commits that are not pushed yet)", options: opts, pick: func(i int) error {
		switch i {
		case 0:
			t.rebaseOnto(gd)
		case 1:
			t.interactiveStart(gd)
		case 2:
			t.pickCommit(gd, "Reword which commit?", false, t.rewordCommit)
		case 3:
			t.pickCommit(gd, "Fold the staged changes into which commit?", true, t.foldInto)
		}
		return nil
	}}
}

// pickCommit lists the commits that are not pushed yet, newest first, and
// calls next with the chosen one. With staged, a tree with only staged
// changes is fine (that is what a fold needs).
func (t *tui) pickCommit(gd, title string, staged bool, next func(gd, rev string)) {
	td, err := t.app.Rewrite.Todo(gd, "")
	switch {
	case err != nil:
		t.refusal("Can't list the commits", err)
		return
	case td.Pending:
		t.refusal("A rewrite is stopped", errors.New("a rewrite is stopped in this repo. "+stopHint))
		return
	case td.Blocked != "" && !(staged && strings.Contains(td.Blocked, "uncommitted changes")):
		t.refusal("Can't rewrite now", errors.New(td.Blocked))
		return
	case len(td.Commits) == 0:
		t.say(nil, "There is nothing to rewrite: no commit that is not pushed yet. (L, Interactive rebase can start from an older commit.)")
		return
	}
	var opts []string
	var revs []string
	for i := len(td.Commits) - 1; i >= 0; i-- {
		c := td.Commits[i]
		opts = append(opts, c.Short+" "+history.CleanText(c.Subject))
		revs = append(revs, c.Hash)
	}
	t.picker = &picker{title: title, options: opts, pick: func(i int) error { next(gd, revs[i]); return nil }}
}

// ---------------- rebase onto ----------------

// rebaseOnto asks what to rebase the current branch onto.
func (t *tui) rebaseOnto(gd string) {
	bs, err := t.app.Refs.Branches(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	tags, _ := t.app.Refs.Tags(gd)
	var opts, names []string
	for _, b := range bs {
		if b.Current {
			continue
		}
		o := b.Name
		if b.Remote {
			o += "  · remote branch"
		}
		opts, names = append(opts, o), append(names, b.Name)
	}
	for _, tg := range tags {
		opts, names = append(opts, tg+"  · tag"), append(names, tg)
	}
	opts, names = append(opts, "A commit, or another name…"), append(names, "")
	t.picker = &picker{title: "Rebase the current branch onto", options: opts, pick: func(i int) error {
		if names[i] != "" {
			t.rebasePlan(gd, names[i])
			return nil
		}
		t.form = &form{title: "Rebase onto", fields: []field{{label: "Commit, branch or tag", hint: "e.g. a1b2c3d or origin/main"}},
			submit: func(v []string) error { t.rebasePlan(gd, v[0]); return nil }}
		return nil
	}}
}

// rebasePlan shows what the rebase would do (your commits, the incoming
// ones, the files likely to clash) and asks.
func (t *tui) rebasePlan(gd, onto string) {
	p, err := t.app.Rebase.Plan(gd, onto)
	if err != nil {
		t.refusal("Can't rebase", err)
		return
	}
	if p.Blocked != "" {
		t.refusal("Can't rebase now", errors.New(p.Blocked))
		return
	}
	if p.UpToDate {
		t.say(nil, p.Summary)
		return
	}
	summary := wrapPlain(p.Summary, min(t.w-8, 96))
	room := max(4, t.overlayRows()-len(summary)-5)
	block := func(title string, cs []rebase.CommitInfo, n int) []string {
		if n == 0 {
			return nil
		}
		l := []string{"", fmt.Sprintf("%s (%d):", title, n)}
		var rows []string
		for _, c := range cs {
			rows = append(rows, fmt.Sprintf("  %s %s  (%s)", c.Short, history.CleanText(c.Subject), history.CleanText(c.Author)))
		}
		if n > len(cs) {
			rows = append(rows, fmt.Sprintf("  … and %d more", n-len(cs)))
		}
		return append(l, capped(rows, room/2)...)
	}
	lines := []string{fmt.Sprintf("Rebase %s onto %s? Your %d commit(s) get new hashes; undo puts them back. (y/n)", p.Branch, p.Onto, p.ReplayedCount), p.Summary}
	lines = append(lines, block("Your commits, replayed in this order", p.Replayed, p.ReplayedCount)...)
	lines = append(lines, block("Coming in from "+p.Onto, p.Incoming, p.IncomingCount)...)
	if len(p.BothChanged) > 0 {
		lines = append(lines, "", "Likely clashes: "+strings.Join(capped(p.BothChanged, 5), ", "))
	}
	t.confirm = &confirmBox{strings.Join(lines, "\n"), map[string]func() error{"y": func() error { return t.rebaseRun(gd, onto) }}}
}

func (t *tui) rebaseRun(gd, onto string) error {
	r, err := t.app.Rebase.Run(gd, onto)
	if err != nil {
		t.refusal("The rebase did not run", err)
		return nil
	}
	t.loadHistory()
	if r.Outcome == rebase.Stopped {
		lines := []string{r.Message, ""}
		for _, f := range r.Conflicts {
			lines = append(lines, "  clash  "+history.CleanText(f))
		}
		t.view = &viewer{title: "The rebase stopped on clashes", lines: append(lines, "", stopHint)}
		t.msg, t.msgErr = "The rebase stopped: "+stopHint, true
		return nil
	}
	t.say(nil, r.Message+undoHint)
	return nil
}

// ---------------- reword and fold ----------------

// rewordCommit asks for the commit's new message, then confirms.
func (t *tui) rewordCommit(gd, rev string) {
	td, err := t.app.Rewrite.Todo(gd, rev)
	switch {
	case err != nil:
		t.refusal("Can't reword", err)
		return
	case td.Blocked != "":
		t.refusal("Can't reword now", errors.New(td.Blocked))
		return
	case len(td.Commits) == 0:
		t.say(nil, "Nothing to reword.")
		return
	}
	c, later := td.Commits[0], len(td.Commits)-1
	t.form = &form{title: "Reword " + c.Short + " " + history.CleanText(c.Subject), fields: []field{{label: "New message", value: c.Message, hint: "Enter saves, then you confirm"}},
		submit: func(v []string) error {
			msg := rewrite.Tidy(v[0])
			switch {
			case msg == "":
				return errors.New("the new message can't be empty")
			case msg == rewrite.Tidy(c.Message):
				return errors.New("that is the same message: nothing to change")
			}
			text := fmt.Sprintf("Reword %s %s? (y/n)\nto: %s", c.Short, history.CleanText(c.Subject), history.CleanText(firstLine(msg)))
			if later > 0 {
				text += fmt.Sprintf("\nThe %d commit(s) after it are replayed on top: they get new hashes (same content). Undo puts the old ones back.", later)
			} else {
				text += "\nIt gets a new hash. Undo puts the old one back."
			}
			t.confirm = &confirmBox{text, map[string]func() error{"y": func() error {
				r, err := t.app.Rewrite.Reword(gd, rev, msg)
				if err != nil {
					t.refusal("The reword did not run", err)
					return nil
				}
				return t.rewritten(r)
			}}}
			return nil
		}}
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// foldInto folds the staged changes into an older commit, after asking.
func (t *tui) foldInto(gd, rev string) {
	h, err := t.app.Refs.Head(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if err := rewrite.CheckFold(h); err != nil {
		t.refusal("Can't fold now", err)
		return
	}
	td, err := t.app.Rewrite.Todo(gd, rev)
	switch {
	case err != nil:
		t.refusal("Can't fold into that commit", err)
		return
	case td.Blocked != "" && !strings.Contains(td.Blocked, "uncommitted changes"):
		t.refusal("Can't fold now", errors.New(td.Blocked))
		return
	case len(td.Commits) == 0:
		return
	}
	c, later := td.Commits[0], len(td.Commits)-1
	text := fmt.Sprintf("Fold the %d staged file(s) into %s %s? (y/n)\nThey are committed as a fixup of it and folded in.", h.Changes.Staged, c.Short, history.CleanText(c.Subject))
	if later > 0 {
		text += fmt.Sprintf("\nThe %d commit(s) after it are replayed on top: new hashes. Undo takes it all back; the changes come back uncommitted.", later)
	}
	t.confirm = &confirmBox{text, map[string]func() error{"y": func() error {
		r, err := t.app.Rewrite.FixupInto(gd, rev)
		if err != nil {
			t.refusal("The fold did not run", err)
			return nil
		}
		return t.rewritten(r)
	}}}
}

// rewritten reports a finished (or stopped) rewrite and reads History again.
func (t *tui) rewritten(r rewrite.Rewritten) error {
	t.rwEd = nil
	t.loadHistory()
	if r.Outcome == rewrite.Stopped {
		lines := []string{r.Message, ""}
		for _, f := range r.Conflicts {
			lines = append(lines, "  clash  "+history.CleanText(f))
		}
		t.view = &viewer{title: "The rewrite stopped on clashes", lines: append(lines, "", stopHint)}
		t.msg, t.msgErr = "The rewrite stopped: "+stopHint, true
		return nil
	}
	t.say(nil, r.Message+undoHint)
	return nil
}

// ---------------- the interactive rebase editor ----------------

// interactiveStart asks which commits to rewrite: the ones not pushed (the
// default) or those since an older commit.
func (t *tui) interactiveStart(gd string) {
	t.picker = &picker{title: "Interactive rebase: which commits?", options: []string{
		"The commits that are not pushed yet",
		"Everything since a commit I name…",
	}, pick: func(i int) error {
		if i == 0 {
			t.openRewriteEditor(gd, "")
			return nil
		}
		t.form = &form{title: "Rewrite since", fields: []field{{label: "Oldest commit to rewrite", hint: "a commit, branch or tag; it must not be pushed"}},
			submit: func(v []string) error {
				if v[0] == "" {
					return errors.New("name the oldest commit to rewrite")
				}
				t.openRewriteEditor(gd, v[0])
				return nil
			}}
		return nil
	}}
}

// openRewriteEditor lists the rewritable commits as picks.
func (t *tui) openRewriteEditor(gd, since string) {
	td, err := t.app.Rewrite.Todo(gd, since)
	switch {
	case err != nil:
		t.refusal("Can't rewrite", err)
		return
	case td.Pending:
		t.refusal("A rewrite is stopped", errors.New("a rewrite is stopped in this repo. "+stopHint))
		return
	case td.Blocked != "":
		t.refusal("Can't rewrite now", errors.New(td.Blocked))
		return
	case len(td.Commits) == 0:
		t.say(nil, "There is nothing to rewrite: no commit that is not pushed yet. Start from an older commit instead.")
		return
	}
	e := &rewriteEditor{gd: gd, since: since, todo: td}
	for _, c := range td.Commits {
		e.steps = append(e.steps, rewrite.Step{Hash: c.Hash, Action: rewrite.Pick})
	}
	t.rwEd = e
	t.msg = ""
}

func (e *rewriteEditor) commit(hash string) rewrite.Commit {
	for _, c := range e.todo.Commits {
		if c.Hash == hash {
			return c
		}
	}
	return rewrite.Commit{Hash: hash, Short: hash[:min(7, len(hash))]}
}

func (e *rewriteEditor) byHash() map[string]rewrite.Commit {
	by := map[string]rewrite.Commit{}
	for _, c := range e.todo.Commits {
		by[c.Hash] = c
	}
	return by
}

// preview is what the steps make, for the live "After" list. A reword
// with no message yet counts with the commit's own (asked for on run).
func (e *rewriteEditor) preview() (rewrite.Plan, error) {
	steps := append([]rewrite.Step(nil), e.steps...)
	for i, s := range steps {
		if s.Action == rewrite.Reword && rewrite.Tidy(s.Message) == "" {
			steps[i].Message = e.commit(s.Hash).Message
		}
	}
	return rewrite.Build(e.todo.Commits, steps)
}

func actionColor(a rewrite.Action) string {
	switch a {
	case rewrite.Reword:
		return cYellow
	case rewrite.Squash, rewrite.Fixup:
		return cGreen
	case rewrite.Drop:
		return cRed
	}
	return ""
}

func (t *tui) drawRewrite() ([]string, int) {
	e := t.rwEd
	path, branch := e.gd, e.todo.Branch
	if t.hist != nil {
		path = t.hist.Repo.Path
	}
	from := "the commits not pushed yet"
	if e.since != "" {
		from = "since " + e.since
	}
	l := []string{"\x00 " + cBold + "Rewrite history" + cReset + "  " + path + "  branch: " + cBold + history.CleanText(branch) + cReset + cDim + "  " + from + ", oldest first" + cReset}
	p, err := e.preview()
	switch {
	case err != nil:
		l = append(l, "\x00 "+cRed+history.CleanText(err.Error())+cReset)
	case p.NoChange:
		l = append(l, "\x00 "+cDim+"Nothing changes yet: p pick · r reword · s squash · f fixup · d drop · J/K move"+cReset)
	default:
		l = append(l, "\x00 "+cGreen+history.CleanText(p.Summary())+"."+cReset)
	}
	l = append(l, "\x00")
	for i, s := range e.steps {
		c := e.commit(s.Hash)
		on := i == e.sel
		subj := history.CleanText(c.Subject)
		if s.Action == rewrite.Reword && rewrite.Tidy(s.Message) != "" {
			subj = cDim + subj + cReset + " → " + history.CleanText(firstLine(s.Message))
		}
		l = append(l, fmt.Sprintf("%s%s%-7s%s %s%s%s %s%s", mark(on), actionColor(s.Action), s.Action, cReset, cDim, c.Short, cReset, boldIf(on), subj+cReset))
	}
	l = append(l, "", " "+cBold+"After"+cReset+cDim+" (what the commits become, oldest first)"+cReset)
	if err != nil {
		return append(l, "  "+cDim+"fix the error above to see it"+cReset), e.sel
	}
	for _, s := range p.SummaryLines(e.byHash()) {
		l = append(l, "  "+history.CleanText(s))
	}
	return l, e.sel
}

// keyRewrite handles a key in the editor. Keys it does not use (switching
// page, help) go to the main handler.
func (t *tui) keyRewrite(k string) bool {
	e := t.rwEd
	n := len(e.steps)
	move := func(d int) { e.sel = max(0, min(n-1, e.sel+d)) }
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.rwEd = nil
		t.say(nil, "Cancelled: nothing changed.")
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup":
		move(-max(1, t.h-6))
	case "pgdn":
		move(max(1, t.h-6))
	case "home", "g":
		e.sel = 0
	case "end", "G":
		e.sel = max(0, n-1)
	case "p", "r", "s", "f", "d":
		a, _ := rewrite.ParseAction(k)
		e.steps[e.sel].Action = a
	case "J", "shift-down":
		if e.sel < n-1 {
			e.steps[e.sel], e.steps[e.sel+1] = e.steps[e.sel+1], e.steps[e.sel]
			e.sel++
		}
	case "K", "shift-up":
		if e.sel > 0 {
			e.steps[e.sel], e.steps[e.sel-1] = e.steps[e.sel-1], e.steps[e.sel]
			e.sel--
		}
	case "enter":
		if e.steps[e.sel].Action == rewrite.Reword {
			t.editRewordMessage(e, e.sel)
		} else {
			t.showRewriteCommit(e.steps[e.sel].Hash)
		}
	case "v":
		t.showRewriteCommit(e.steps[e.sel].Hash)
	case "x":
		t.rewriteRun()
	}
	return true
}

// showRewriteCommit shows a commit's message and files.
func (t *tui) showRewriteCommit(hash string) {
	d, err := t.app.History.Show(t.rwEd.gd, hash)
	if err != nil {
		t.say(err, "")
		return
	}
	lines := []string{cDim + d.Hash + cReset, ""}
	for _, l := range strings.Split(d.Message, "\n") {
		lines = append(lines, history.CleanText(l))
	}
	lines = append(lines, "", fmt.Sprintf("%d file(s) changed", len(d.Files)+d.More))
	for _, f := range d.Files {
		lines = append(lines, "  "+history.CleanText(f.Path))
	}
	t.view = &viewer{title: "Commit " + d.Hash[:min(7, len(d.Hash))], lines: lines}
}

// editRewordMessage asks for the new message of a reword row.
func (t *tui) editRewordMessage(e *rewriteEditor, i int) {
	hash := e.steps[i].Hash
	c := e.commit(hash)
	cur := e.steps[i].Message
	if rewrite.Tidy(cur) == "" {
		cur = c.Message
	}
	t.form = &form{title: "New message for " + c.Short + " " + history.CleanText(c.Subject), fields: []field{{label: "Message", value: cur}},
		submit: func(v []string) error {
			if rewrite.Tidy(v[0]) == "" {
				return errors.New("the new message can't be empty")
			}
			for j := range e.steps {
				if e.steps[j].Hash == hash {
					e.steps[j].Message = v[0]
				}
			}
			return nil
		}}
}

// rewriteRun checks the steps, asks for the messages still missing (a
// reword's, a squash group's), then confirms with the before and after.
func (t *tui) rewriteRun() {
	e := t.rwEd
	p, err := e.preview()
	if err != nil {
		t.say(err, "")
		return
	}
	if p.NoChange {
		t.say(nil, "Nothing would change: pick, reword, squash, fixup, drop or move a commit first.")
		return
	}
	steps, prompts := rewrite.Prepare(e.todo.Commits, e.steps)
	t.askRewriteMessages(e, steps, prompts, 0)
}

func (t *tui) askRewriteMessages(e *rewriteEditor, steps []rewrite.Step, prompts []rewrite.Prompt, i int) {
	if i >= len(prompts) {
		t.confirmRewrite(e, steps)
		return
	}
	p := prompts[i]
	title, hint := "New message for "+history.CleanText(p.Subject), "Enter saves"
	if p.Combine {
		title = "Message for the squashed commit starting with " + history.CleanText(p.Subject)
		hint = "the messages joined; Enter keeps it"
	}
	t.form = &form{title: title, fields: []field{{label: "Message", value: p.Initial, hint: hint}}, submit: func(v []string) error {
		msg := rewrite.Tidy(v[0])
		if msg == "" && !p.Combine {
			t.askRewriteMessages(e, steps, prompts, i) // ask again
			return errors.New("the new message can't be empty")
		}
		steps[p.Index].Message = msg
		t.askRewriteMessages(e, steps, prompts, i+1)
		return nil
	}}
}

// confirmRewrite asks, showing the commits before and after.
func (t *tui) confirmRewrite(e *rewriteEditor, steps []rewrite.Step) {
	p, td, err := t.app.Rewrite.Preview(e.gd, e.since, steps)
	if err != nil {
		t.refusal("Can't rewrite", err)
		return
	}
	by := map[string]rewrite.Commit{}
	var before, after []string
	for _, c := range td.Commits {
		by[c.Hash] = c
		before = append(before, "  "+c.Short+" "+history.CleanText(c.Subject))
	}
	for _, s := range p.SummaryLines(by) {
		after = append(after, "  "+history.CleanText(s))
	}
	room := max(4, t.overlayRows()-5)
	lines := []string{"Rewrite these commits? " + p.Summary() + ". Undo brings the old ones back while they are not pushed. (y/n)",
		fmt.Sprintf("Before (%d, oldest first):", p.Before)}
	lines = append(lines, capped(before, room/2)...)
	lines = append(lines, fmt.Sprintf("After (%d):", p.After))
	lines = append(lines, capped(after, room/2)...)
	t.confirm = &confirmBox{strings.Join(lines, "\n"), map[string]func() error{"y": func() error {
		r, err := t.app.Rewrite.Run(e.gd, e.since, steps)
		if err != nil {
			t.refusal("The rewrite did not run", err)
			return nil
		}
		return t.rewritten(r)
	}}}
}

// keyRewordSelected is l on History: reword the selected commit.
func (t *tui) keyRewordSelected() {
	if t.histDir == "" {
		return
	}
	h := t.selectedCommit()
	if h == "" {
		t.say(fmt.Errorf("select a commit line first (↑↓)"), "")
		return
	}
	t.rewordCommit(t.histDir, h)
}
