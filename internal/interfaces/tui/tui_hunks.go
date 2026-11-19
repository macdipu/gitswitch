package tui

// Hunks: `H` in the Changes view opens a file's change as hunks, to stage,
// unstage or discard a whole hunk, or single lines picked with Space.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	wsapp "github.com/macdipu/gitswitch/internal/features/workspace/application"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// hunksUI is the state of the hunks view, embedded in tui.
type hunksUI struct {
	hv *hunksView // a file's hunks, while open (on top of the changes)
}

// hunksView is one file's diff taken apart. Rows are the hunk headers and
// their lines, one cursor moves over; picked holds the chosen changed
// lines as hunk and line index.
type hunksView struct {
	gd, path string
	staged   bool // the staged change; otherwise the one not staged
	d        workspace.Diff
	rows     []hunkRow
	cur      int
	picked   map[[2]int]bool
}

// hunkRow is a hunk's header (l = -1) or one of its lines.
type hunkRow struct{ h, l int }

// openHunks is H in the changes: the selected file's hunks.
func (t *tui) openHunks() {
	c := t.changes
	if c == nil || c.sel >= len(c.rows) {
		return
	}
	r := c.rows[c.sel]
	if r.sec == "conflict" {
		t.say(fmt.Errorf("%s has a conflict: fix it first (e, from History, opens the operation)", history.CleanText(r.f.Path)), "")
		return
	}
	v := &hunksView{gd: c.gd, path: r.f.Path, staged: r.sec == "staged", picked: map[[2]int]bool{}}
	if !t.readHunks(v) {
		return
	}
	t.hv = v
	t.msg = ""
}

// readHunks reads the file's diff again into v, dropping the picks; it
// says why and returns false when there is nothing to pick from.
func (t *tui) readHunks(v *hunksView) bool {
	d, err := t.app.Workspace.Diff(v.gd, v.path, v.staged)
	if err != nil {
		t.say(err, "")
		return false
	}
	which := "not staged"
	if v.staged {
		which = "staged"
	}
	switch {
	case d.Text == "":
		t.say(fmt.Errorf("%s has no %s change left", history.CleanText(v.path), which), "")
		return false
	case len(d.Hunks) == 0:
		t.say(fmt.Errorf("%s: %s", history.CleanText(v.path), d.NoHunks), "")
		return false
	}
	v.d, v.rows, v.picked = d, nil, map[[2]int]bool{}
	for _, h := range d.Hunks {
		v.rows = append(v.rows, hunkRow{h.Index, -1})
		for i := range h.Lines {
			v.rows = append(v.rows, hunkRow{h.Index, i})
		}
	}
	v.cur = max(0, min(v.cur, len(v.rows)-1))
	return true
}

func (t *tui) closeHunks() {
	t.hv = nil
	t.loadChanges()
}

func (v *hunksView) changedIn(h int) []int {
	var out []int
	for i, l := range v.d.Hunks[h].Lines {
		if l.Changed() {
			out = append(out, i)
		}
	}
	return out
}

// selection is what an action works on: the picked lines, or else the
// hunk under the cursor. what names it for the messages.
func (v *hunksView) selection() (workspace.Selection, string, int) {
	if len(v.picked) > 0 {
		sel := workspace.Selection{Lines: map[int][]int{}}
		for k := range v.picked {
			sel.Lines[k[0]] = append(sel.Lines[k[0]], k[1])
		}
		for _, l := range sel.Lines {
			sort.Ints(l)
		}
		return sel, fmt.Sprintf("%d picked line(s)", len(v.picked)), len(v.picked)
	}
	h := v.rows[v.cur].h
	return workspace.Selection{Hunks: []int{h}}, fmt.Sprintf("hunk %d", h+1), 1
}

// keyHunks handles a key in the hunks view.
func (t *tui) keyHunks(k string) bool {
	v := t.hv
	n := len(v.rows)
	page := max(1, t.h-6)
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.closeHunks()
	case "up", "k":
		v.cur = max(0, v.cur-1)
	case "down", "j":
		v.cur = min(n-1, v.cur+1)
	case "pgup":
		v.cur = max(0, v.cur-page)
	case "pgdn":
		v.cur = min(n-1, v.cur+page)
	case "home", "g":
		v.cur = 0
	case "end", "G":
		v.cur = n - 1
	case "n":
		for i := v.cur + 1; i < n; i++ {
			if v.rows[i].l < 0 {
				v.cur = i
				break
			}
		}
	case "p":
		for i := v.cur - 1; i >= 0; i-- {
			if v.rows[i].l < 0 {
				v.cur = i
				break
			}
		}
	case " ":
		t.togglePick()
	case "c":
		v.picked = map[[2]int]bool{}
		t.say(nil, "Cleared the picked lines.")
	case "r":
		if t.readHunks(v) {
			t.say(nil, "Read the file again.")
		} else {
			t.closeHunks()
		}
	case "s", "u", "x":
		t.hunkAct(k)
	}
	return true
}

// togglePick picks or unpicks the changed line under the cursor, or all
// the changed lines of the hunk on its header.
func (t *tui) togglePick() {
	v := t.hv
	r := v.rows[v.cur]
	if r.l < 0 {
		lines := v.changedIn(r.h)
		all := true
		for _, i := range lines {
			all = all && v.picked[[2]int{r.h, i}]
		}
		for _, i := range lines {
			if all {
				delete(v.picked, [2]int{r.h, i})
			} else {
				v.picked[[2]int{r.h, i}] = true
			}
		}
		return
	}
	if !v.d.Hunks[r.h].Lines[r.l].Changed() {
		t.say(errors.New("only added and deleted lines can be picked; this one is unchanged context"), "")
		return
	}
	key := [2]int{r.h, r.l}
	if v.picked[key] {
		delete(v.picked, key)
	} else {
		v.picked[key] = true
	}
	v.cur = min(len(v.rows)-1, v.cur+1)
}

// hunkAct stages (s), unstages (u) or discards (x) the picked lines, or
// the hunk under the cursor. Discard asks first.
func (t *tui) hunkAct(k string) {
	v := t.hv
	switch {
	case k == "u" && !v.staged:
		t.say(errors.New("this change is not staged: s stages it, x discards it"), "")
		return
	case k != "u" && v.staged:
		t.say(errors.New("this change is already staged: u takes it out of the next commit"), "")
		return
	}
	sel, what, _ := v.selection()
	path := v.path
	run := func() error {
		var res wsapp.HunksChanged
		var err error
		fp := v.d.Fingerprint
		switch k {
		case "s":
			res, err = t.app.Workspace.StageHunks(v.gd, path, fp, sel)
		case "u":
			res, err = t.app.Workspace.UnstageHunks(v.gd, path, fp, sel)
		default:
			res, err = t.app.Workspace.DiscardHunks(v.gd, path, fp, sel)
		}
		if err != nil {
			if errors.Is(err, workspace.ErrStale) {
				t.readHunks(v) // show what it is now
				err = fmt.Errorf("%v (read it again: the picks were cleared)", err)
			}
			return err
		}
		verb := map[string]string{"s": "Staged", "u": "Unstaged", "x": "Discarded"}[k]
		msg := fmt.Sprintf("%s %s of %s (%d line(s)).", verb, what, history.CleanText(path), res.Lines)
		switch k {
		case "u":
			msg = fmt.Sprintf("Unstaged %s of %s (%d line(s)); the changes are still in the file.", what, history.CleanText(path), res.Lines)
		case "x":
			msg += fmt.Sprintf(" Kept in git stash as \"%s\": %s brings it back.", res.Stash, res.Restore)
		}
		t.loadChanges()
		if t.readHunks(v) {
			v.cur = max(0, min(v.cur, len(v.rows)-1))
		} else {
			t.hv = nil // nothing left to pick from: back to the changes
		}
		t.say(nil, msg)
		return nil
	}
	if k != "x" {
		if err := run(); err != nil {
			t.say(err, "")
		}
		return
	}
	t.confirm = &confirmBox{fmt.Sprintf("Discard %s of %s? The change is removed from the file and kept in git stash as \"%s\"; to get it back: %s (y/n)",
		what, history.CleanText(path), workspace.DiscardMessage(t.changesBranch()), wsapp.RestoreHint(path)),
		map[string]func() error{"y": run}}
}

func (t *tui) changesBranch() string {
	if t.changes != nil {
		return t.changes.st.Branch
	}
	return ""
}

func (t *tui) drawHunks() ([]string, int) {
	v := t.hv
	which := "not staged"
	if v.staged {
		which = "staged"
	}
	l := []string{
		"\x00 " + cBold + "Hunks" + cReset + "  " + history.CleanText(v.path) + "  " + cDim + which + cReset,
		"\x00 " + cDim + "s stage · u unstage · x discard the hunk under the cursor, or the picked lines · Space picks a line (on a header: the whole hunk) · c clears picks · n/p next/previous hunk · r reload · Esc back" + cReset,
	}
	picked := ""
	if len(v.picked) > 0 {
		picked = cYellow + fmt.Sprintf("%d line(s) picked: s, u and x work on them, not on the hunk", len(v.picked)) + cReset
	}
	l = append(l, "\x00 "+picked, "\x00")
	hdr := len(l)
	_ = hdr
	var out []string
	for i, r := range v.rows {
		on := i == v.cur
		h := v.d.Hunks[r.h]
		if r.l < 0 {
			all, any := true, false
			for _, c := range v.changedIn(r.h) {
				p := v.picked[[2]int{r.h, c}]
				all, any = all && p, any || p
			}
			box := "[ ]"
			switch {
			case all && any:
				box = "[x]"
			case any:
				box = "[-]"
			}
			out = append(out, fmt.Sprintf("%s%s%s %sHunk %d%s %s", mark(on), cYellow, box, cBold, r.h+1, cReset+cYellow, history.CleanText(h.Header)+cReset))
			continue
		}
		dl := h.Lines[r.l]
		sign, col, box := " ", cDim, "   "
		switch dl.Kind {
		case workspace.LineAdd:
			sign, col = "+", cGreen
		case workspace.LineDel:
			sign, col = "-", cRed
		}
		if dl.Changed() {
			box = "[ ]"
			if v.picked[[2]int{r.h, r.l}] {
				box = "[x]"
			}
		}
		out = append(out, fmt.Sprintf("%s%s %s%s%s%s", mark(on), box, col, sign, strings.TrimRight(history.CleanText(dl.Text), "\r"), cReset))
	}
	return append(l, out...), v.cur
}
