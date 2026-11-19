package tui

// Merge, the operation in progress and the conflict editor (the terminal
// side of the `merge` feature): `e` on History merges a branch into the
// current one, or opens the stopped operation; from there a conflicted file
// opens in the conflict view, where each clash is chosen one by one.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
)

// cTheirs colours the side coming in, so it differs from ours (green) and
// the errors (red).
const cTheirs = "\x1b[36m"

// mergeUI is the state of the operation and conflict views, embedded in tui.
type mergeUI struct {
	opv  *opView       // the operation in progress, while open
	conf *conflictView // a conflicted file, while open (on top of opv)
	// edit opens a file in the user's editor and waits; nil uses
	// $VISUAL or $EDITOR on the real terminal. Tests replace it.
	edit func(path string) error
}

// opView is the Operation page: what git is in the middle of and the
// files still in conflict.
type opView struct {
	gd  string
	op  merge.Operation
	sel int
}

// ---------------- merge a branch ----------------

// mergeKey is `e` on History: with an operation stopped it opens that;
// otherwise it asks which branch to merge into the current one.
func (t *tui) mergeKey() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	gd := t.histDir
	if op, err := t.app.Merge.Operation(gd); err == nil && op.Kind != "" {
		t.openOperation()
		return
	}
	bs, err := t.app.Refs.Branches(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	var opts, names []string
	cur := ""
	for _, b := range bs {
		if b.Current {
			cur = b.Name
		}
	}
	for _, remote := range []bool{false, true} { // local branches first
		for _, b := range bs {
			if b.Current || b.Remote != remote || strings.HasSuffix(b.Name, "/HEAD") {
				continue
			}
			o := b.Name
			if b.Remote {
				o += "  (remote)"
			}
			opts, names = append(opts, o), append(names, b.Name)
		}
	}
	if len(names) == 0 {
		t.say(nil, "There is no other branch to merge.")
		return
	}
	title := "Merge which branch into " + cur + "?"
	if cur == "" {
		title = "Merge which branch into the current commit?"
	}
	t.picker = &picker{title: title, options: opts, pick: func(i int) error { return t.mergePlan(gd, names[i]) }}
}

// mergePlan shows what merging from would do, then asks how.
func (t *tui) mergePlan(gd, from string) error {
	p, err := t.app.Merge.Plan(gd, from)
	if err != nil {
		return err
	}
	if p.Blocked != "" {
		t.view = &viewer{title: "Merge " + history.CleanText(from) + " into " + history.CleanText(p.Into), lines: []string{cRed + "Can't merge now:" + cReset, "  " + history.CleanText(p.Blocked)}}
		return nil
	}
	if p.UpToDate {
		t.say(nil, p.Summary)
		return nil
	}
	acts := map[string]func() error{}
	how := ""
	if p.FastForward {
		acts["f"] = func() error { return t.runMerge(gd, from, merge.FastForward) }
		how += "f = fast-forward (no merge commit) · "
	}
	acts["m"] = func() error { return t.runMerge(gd, from, merge.Commit) }
	acts["s"] = func() error { return t.runMerge(gd, from, merge.Squash) }
	how += "m = merge commit · s = squash (stage the changes, no commit) · n or Esc = cancel"
	t.confirm = &confirmBox{mergeQuestion(p) + "  " + how, acts}
	return nil
}

// mergeQuestion is the plan in one paragraph: the summary, the commits
// coming in and the files that may clash.
func mergeQuestion(p merge.Plan) string {
	q := fmt.Sprintf("Merge %s into %s? %s", history.CleanText(p.From), history.CleanText(p.Into), history.CleanText(p.Summary))
	if n := len(p.Incoming); n > 0 {
		var subs []string
		for _, c := range p.Incoming[:min(n, 4)] {
			subs = append(subs, c.Short+" "+history.CleanText(c.Subject))
		}
		q += " Coming in: " + strings.Join(subs, "; ")
		if more := p.IncomingCount - len(subs); more > 0 {
			q += fmt.Sprintf(" (+%d more)", more)
		}
		q += "."
	}
	if len(p.Conflicts) > 0 && p.Predicted {
		q += " Likely clashes: " + history.CleanText(strings.Join(p.Conflicts, ", ")) + "."
	}
	return q
}

// runMerge merges, then says how it ended; a stop on clashes opens the
// operation.
func (t *tui) runMerge(gd, from string, mode merge.Mode) error {
	r, err := t.app.Merge.Merge(gd, from, mode)
	t.readHistory()
	if err != nil {
		return err
	}
	if r.Outcome == merge.Stopped {
		t.openOperation()
		t.say(nil, r.Message)
		return nil
	}
	msg := r.Message
	if r.Outcome == merge.FastForwards || r.Outcome == merge.MergeCommit {
		msg += undoHint
	}
	t.say(nil, msg)
	return nil
}

// ---------------- the operation in progress ----------------

// opBanner is the History header lines that say git is in the middle of
// something.
func (t *tui) opBanner() []string {
	if t.head == nil || t.head.Busy == "" {
		return nil
	}
	return []string{"\x00 " + cRed + cBold + "A " + history.CleanText(t.head.Busy) + " is in progress." + cReset + cDim + "  e to see the files in conflict, continue or abort it" + cReset}
}

func (t *tui) openOperation() {
	t.opv = &opView{gd: t.histDir}
	t.msg = ""
	t.loadOperation()
}

// loadOperation reads the operation again; when it is over the view closes.
func (t *tui) loadOperation() {
	v := t.opv
	if v == nil {
		return
	}
	op, err := t.app.Merge.Operation(v.gd)
	if err != nil {
		t.closeOperation()
		t.say(err, "")
		return
	}
	if op.Kind == "" {
		t.closeOperation()
		t.say(nil, "Nothing is in progress.")
		return
	}
	v.op = op
	v.sel = max(0, min(v.sel, len(op.Conflicts)-1))
}

func (t *tui) closeOperation() {
	t.opv, t.conf = nil, nil
	sel := t.sel[tabHistory]
	t.readHistory()
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

// keyOperation handles a key on the Operation page.
func (t *tui) keyOperation(k string) bool {
	v := t.opv
	n := len(v.op.Conflicts)
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.closeOperation()
	case "up", "k":
		v.sel = max(0, v.sel-1)
	case "down", "j":
		v.sel = max(0, min(n-1, v.sel+1))
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = max(0, n-1)
	case "r":
		t.loadOperation()
	case "enter":
		if n == 0 {
			t.say(nil, "No file is in conflict: c continues the "+v.op.Kind+".")
			return true
		}
		t.openConflict(v.op.Conflicts[v.sel])
	case "c":
		t.operationAct("continue")
	case "s":
		t.operationAct("skip")
	case "a":
		kind := v.op.Kind
		if len(v.op.Actions) == 0 {
			t.say(errors.New(v.op.Message), "")
			return true
		}
		t.confirm = &confirmBox{fmt.Sprintf("Abort the %s? The repo goes back to how it was before it started; the fixes you made to its files are lost. (y/n)", kind),
			map[string]func() error{"y": func() error { t.operationAct("abort"); return nil }}}
	}
	return true
}

// operationAct continues, skips or aborts, and says how it went. A rebase
// may stop again on the next commit.
func (t *tui) operationAct(action string) {
	v := t.opv
	kind := v.op.Kind
	if !v.op.Allowed(action) {
		why := fmt.Sprintf("a %s can't be %sed here", kind, action)
		switch {
		case len(v.op.Actions) == 0:
			why = v.op.Message
		case action == merge.Skip:
			why = fmt.Sprintf("a %s can't be skipped: continue or abort it", kind)
		}
		t.say(errors.New(why), "")
		return
	}
	o, err := t.app.Merge.Act(v.gd, action)
	if err != nil {
		t.loadOperation()
		t.say(err, "")
		return
	}
	if o.Kind != "" {
		t.loadOperation()
		t.say(nil, o.Message)
		return
	}
	t.closeOperation()
	switch action {
	case merge.Continue:
		t.say(nil, "The "+kind+" is finished."+undoHint)
	case merge.Skip:
		t.say(nil, "Skipped the commit; the "+kind+" is finished.")
	default:
		t.say(nil, "Aborted the "+kind+": the repo is back as it was before.")
	}
}

func (t *tui) drawOperation() ([]string, int) {
	v := t.opv
	o := v.op
	path := v.gd
	if t.hist != nil {
		path = t.hist.Repo.Path
	}
	branch := o.Branch
	if branch == "" {
		branch = "(detached)"
	}
	l := []string{"\x00 " + cBold + "Operation in progress: " + history.CleanText(o.Kind) + cReset + "  " + path + "  branch: " + cBold + history.CleanText(branch) + cReset,
		"\x00 " + cDim + "ours: " + history.CleanText(o.Ours) + " · theirs: " + history.CleanText(o.Theirs) + cReset}
	l = append(l, "\x00 "+history.CleanText(o.Message))
	keys := "c continue"
	switch {
	case len(o.Actions) == 0:
		keys = "this one is finished outside GitSwitch"
	case !o.Allowed(merge.Continue):
		keys = "fix every file first, then c continues"
	}
	if o.Allowed(merge.Skip) {
		keys += " · s skip this commit"
	}
	if o.Allowed(merge.Abort) {
		keys += " · a abort (asks first)"
	}
	l = append(l, "\x00 "+cYellow+keys+cReset, "\x00")
	if len(o.Conflicts) == 0 {
		if o.CanContinue {
			l = append(l, " "+cGreen+"Every conflict is fixed."+cReset+" c continues the "+history.CleanText(o.Kind)+".")
		}
		return l, -1
	}
	l = append(l, " "+cBold+fmt.Sprintf("Files in conflict (%d)", len(o.Conflicts))+cReset+cDim+"  Enter opens one to fix it"+cReset)
	hdr, selLine := 0, -1
	for hdr < len(l) && strings.HasPrefix(l[hdr], "\x00") {
		hdr++
	}
	for i, f := range o.Conflicts {
		on := i == v.sel
		if on {
			selLine = len(l) - hdr
		}
		l = append(l, fmt.Sprintf("%s%s!%s %s%s%s", mark(on), cRed, cReset, boldIf(on), history.CleanText(f), cReset))
	}
	return l, selLine
}

// ---------------- the conflict view ----------------

// region is one clash in a file's text: the lines of the marker block, by
// index into the file's lines, and what each side has there.
type region struct {
	start, end  int // the "<<<<<<<" line and the ">>>>>>>" line
	ours, base  []string
	theirs      []string
	oursLabel   string
	theirsLabel string
	hasBase     bool
}

// choice of a region: none yet, ours, theirs or both.
const (
	pickNone = iota
	pickOurs
	pickTheirs
	pickBoth
)

func markerLine(l string, c byte) (label string, ok bool) {
	s := strings.TrimRight(l, "\r\n")
	if len(s) < 7 || s[:7] != strings.Repeat(string(c), 7) {
		return "", false
	}
	if len(s) == 7 {
		return "", true
	}
	if s[7] == ' ' {
		return strings.TrimSpace(s[8:]), true
	}
	return "", false
}

// splitLines cuts text into lines that keep their own line breaks, so
// the text can be put back together exactly.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.SplitAfter(text, "\n")
}

// parseRegions finds the clashes (<<<<<<<, optional |||||||, =======,
// >>>>>>>) in a file's lines. Markers that don't pair up are an error:
// the file has to be fixed in an editor.
func parseRegions(lines []string) ([]region, error) {
	var out []region
	state := 0 // 0 outside, 1 ours, 2 base, 3 theirs
	var cur region
	for i, l := range lines {
		s := strings.TrimRight(l, "\r\n")
		if lbl, ok := markerLine(l, '<'); ok {
			if state != 0 {
				return nil, fmt.Errorf("line %d: a clash starts inside another one", i+1)
			}
			cur, state = region{start: i, oursLabel: lbl}, 1
			continue
		}
		switch {
		case state == 0:
			continue
		case state == 1:
			if _, ok := markerLine(l, '|'); ok {
				cur.hasBase, state = true, 2
				continue
			}
			fallthrough
		case state == 2:
			if s == "=======" {
				state = 3
				continue
			}
			if state == 2 {
				cur.base = append(cur.base, l)
			} else {
				cur.ours = append(cur.ours, l)
			}
		case state == 3:
			if lbl, ok := markerLine(l, '>'); ok {
				cur.end, cur.theirsLabel = i, lbl
				out, state = append(out, cur), 0
				continue
			}
			cur.theirs = append(cur.theirs, l)
		}
	}
	if state != 0 {
		return nil, fmt.Errorf("line %d: a clash is never closed (no >>>>>>> line)", cur.start+1)
	}
	return out, nil
}

// buildResult puts the file together with each clash replaced by the
// choice made for it. Every clash needs one.
func buildResult(lines []string, regions []region, choice []int) (string, error) {
	var b strings.Builder
	left := 0
	next := 0
	for i := 0; i < len(lines); i++ {
		if next < len(regions) && i == regions[next].start {
			r := regions[next]
			switch choice[next] {
			case pickOurs:
				b.WriteString(strings.Join(r.ours, ""))
			case pickTheirs:
				b.WriteString(strings.Join(r.theirs, ""))
			case pickBoth:
				o := strings.Join(r.ours, "")
				if o != "" && !strings.HasSuffix(o, "\n") {
					o += "\n" // ours had no line break at the end of the file
				}
				b.WriteString(o + strings.Join(r.theirs, ""))
			default:
				left++
			}
			i, next = r.end, next+1
			continue
		}
		b.WriteString(lines[i])
	}
	if left > 0 {
		return "", fmt.Errorf("%d clash(es) still need a choice: o keeps ours, t theirs, b both", left)
	}
	return b.String(), nil
}

// conflictView shows one conflicted file: the file with its markers, or
// one of the versions, scrolled by a line cursor.
type conflictView struct {
	gd      string
	path    string
	c       merge.Conflict
	err     string // why the file can't be shown (binary, too big)
	pane    int    // 0 the file with markers, 1 ours, 2 theirs, 3 base
	cur     int
	lines   []string // the file's lines with their line breaks
	regions []region
	perr    string // the markers don't pair up
	choice  []int
}

func (t *tui) openConflict(path string) {
	t.conf = &conflictView{gd: t.opv.gd, path: path}
	t.msg = ""
	t.loadConflict()
}

// loadConflict reads the file again; choices are kept only while the text
// is the same.
func (t *tui) loadConflict() {
	v := t.conf
	if v == nil {
		return
	}
	was := v.c.Text
	c, err := t.app.Merge.Conflict(v.gd, v.path)
	if err != nil && !strings.Contains(err.Error(), "binary") && !strings.Contains(err.Error(), "too big") {
		t.conf = nil
		t.loadOperation()
		t.say(err, "")
		return
	}
	v.c, v.err = c, ""
	if err != nil {
		v.err = err.Error()
	}
	v.lines, v.regions, v.perr = splitLines(c.Text), nil, ""
	if v.err == "" {
		if rs, perr := parseRegions(v.lines); perr != nil {
			v.perr = perr.Error()
		} else {
			v.regions = rs
		}
	}
	if len(v.choice) != len(v.regions) || was != c.Text {
		v.choice = make([]int, len(v.regions))
	}
	v.cur = max(0, min(v.cur, len(v.paneLines())-1))
}

func (v *conflictView) paneText() string {
	switch v.pane {
	case 1:
		return strDeref(v.c.Ours, "(this side deleted the file)")
	case 2:
		return strDeref(v.c.Theirs, "(this side deleted the file)")
	case 3:
		return strDeref(v.c.Base, "(the file did not exist before: both sides added it)")
	}
	if v.err != "" {
		return v.err
	}
	if !v.c.Exists {
		return "(the file is not in the folder: a side deleted it; take ours or theirs)"
	}
	return v.c.Text
}

func strDeref(s *string, none string) string {
	if s == nil {
		return none
	}
	return *s
}

func (v *conflictView) paneLines() []string {
	return strings.Split(strings.TrimSuffix(v.paneText(), "\n"), "\n")
}

// regionAt is the clash the cursor is on, or -1 (only in the file pane).
func (v *conflictView) regionAt() int {
	if v.pane != 0 {
		return -1
	}
	for i, r := range v.regions {
		if v.cur >= r.start && v.cur <= r.end {
			return i
		}
	}
	return -1
}

func (v *conflictView) chosen() int {
	n := 0
	for _, c := range v.choice {
		if c != pickNone {
			n++
		}
	}
	return n
}

func (t *tui) keyConflict(k string) bool {
	v := t.conf
	page := max(1, t.h-8)
	last := len(v.paneLines()) - 1
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.conf = nil
		t.loadOperation()
	case "up", "k":
		v.cur = max(0, v.cur-1)
	case "down", "j":
		v.cur = min(last, v.cur+1)
	case "pgup":
		v.cur = max(0, v.cur-page)
	case "pgdn":
		v.cur = min(last, v.cur+page)
	case "home", "g":
		v.cur = 0
	case "end", "G":
		v.cur = max(0, last)
	case "v":
		v.pane, v.cur = (v.pane+1)%4, 0
	case "n", "p":
		if v.pane != 0 || len(v.regions) == 0 {
			t.say(errors.New("no clash to jump to here"), "")
			return true
		}
		found := -1
		for i, r := range v.regions {
			if k == "n" && r.start > v.cur {
				found = i
				break
			}
			if k == "p" && r.start < v.cur {
				found = i
			}
		}
		if found < 0 {
			t.say(nil, "No more clashes that way.")
			return true
		}
		v.cur = v.regions[found].start
	case "o", "t", "b", "u":
		t.pickRegion(k)
	case "w":
		t.saveRegions()
	case "O", "T":
		t.takeSide(k)
	case "d":
		t.markResolved()
	case "e":
		t.editConflict()
	case "r":
		t.loadConflict()
		t.say(nil, "Read the file again.")
	}
	return true
}

// pickRegion sets the choice for the clash under the cursor.
func (t *tui) pickRegion(k string) {
	v := t.conf
	i := v.regionAt()
	switch {
	case v.perr != "":
		t.say(fmt.Errorf("the markers don't pair up (%s): fix the file in your editor (e)", v.perr), "")
		return
	case len(v.regions) == 0:
		t.say(errors.New("this file has no clash markers to choose from"), "")
		return
	case i < 0:
		t.say(errors.New("put the cursor on a clash first (n jumps to the next one)"), "")
		return
	}
	v.choice[i] = map[string]int{"o": pickOurs, "t": pickTheirs, "b": pickBoth, "u": pickNone}[k]
	what := map[string]string{"o": "ours", "t": "theirs", "b": "both (ours, then theirs)", "u": "no choice"}[k]
	t.say(nil, fmt.Sprintf("Clash %d of %d: %s. %d of %d chosen; w saves the file.", i+1, len(v.regions), what, v.chosen(), len(v.regions)))
	if k != "u" { // on to the next clash
		if i+1 < len(v.regions) {
			v.cur = v.regions[i+1].start
		}
	}
}

// saveRegions writes the file with every clash resolved as chosen and
// marks it resolved.
func (t *tui) saveRegions() {
	v := t.conf
	switch {
	case v.perr != "":
		t.say(fmt.Errorf("the markers don't pair up (%s): fix the file in your editor (e)", v.perr), "")
		return
	case len(v.regions) == 0:
		t.say(errors.New("this file has no clash markers: d marks it resolved as it is"), "")
		return
	}
	text, err := buildResult(v.lines, v.regions, v.choice)
	if err != nil {
		t.say(err, "")
		return
	}
	t.resolved(v.path, "save", text, "Saved "+history.CleanText(v.path)+" with your choices")
}

// takeSide asks, then keeps one side's whole file.
func (t *tui) takeSide(k string) {
	v := t.conf
	side, label := "ours", v.c.OursLabel
	if k == "T" {
		side, label = "theirs", v.c.TheirsLabel
	}
	path := v.path
	t.confirm = &confirmBox{fmt.Sprintf("Keep %s for the whole file %s (%s)? The other side's changes to this file are dropped. (y/n)", side, history.CleanText(path), history.CleanText(label)),
		map[string]func() error{"y": func() error {
			t.resolved(path, side, "", "Kept "+side+" for "+history.CleanText(path))
			return nil
		}}}
}

// markResolved marks the file as it is in the folder; refused while
// markers remain, saying so.
func (t *tui) markResolved() {
	v := t.conf
	t.resolved(v.path, "done", "", "Marked "+history.CleanText(v.path)+" resolved")
}

// resolved runs the merge service's resolve and goes back to the operation.
func (t *tui) resolved(path, action, text, done string) {
	v := t.conf
	op, err := t.app.Merge.Resolve(v.gd, path, action, text)
	if err != nil {
		t.loadConflict()
		t.say(err, "")
		return
	}
	t.conf = nil
	t.loadOperation()
	if t.opv == nil {
		return
	}
	left := "Every conflict is fixed: c continues the " + op.Kind + "."
	if n := len(op.Conflicts); n > 0 {
		left = fmt.Sprintf("%d file(s) still in conflict.", n)
	}
	t.say(nil, done+". "+left)
}

// editConflict opens the file in $VISUAL or $EDITOR (leaving the screen
// until it ends), then reads it again.
func (t *tui) editConflict() {
	v := t.conf
	wt := ""
	if t.hist != nil {
		wt = t.hist.Repo.Path
	}
	if wt == "" {
		wt = filepath.Dir(v.gd)
	}
	file := filepath.Join(wt, filepath.FromSlash(v.path))
	var err error
	if t.edit != nil {
		err = t.edit(file)
	} else {
		err = t.runEditor(file)
	}
	t.loadConflict()
	switch {
	case err != nil:
		t.say(err, "")
	case t.conf != nil && t.conf.c.Markers:
		t.say(nil, "Read the file again; it still has clash markers: edit them away, or choose here.")
	case t.conf != nil:
		t.say(nil, "Read the file again: no clash markers left; d marks it resolved.")
	}
}

func (t *tui) runEditor(file string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	parts := strings.Fields(ed)
	if len(parts) == 0 {
		return errors.New("no editor: set $VISUAL or $EDITOR (e.g. EDITOR=vim), or edit the file elsewhere and press r")
	}
	var err error
	t.suspend(func() {
		cmd := exec.Command(parts[0], append(parts[1:], file)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err = cmd.Run(); err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	return err
}

func (t *tui) drawConflict() ([]string, int) {
	v := t.conf
	kind := v.c.Kind
	if kind == "" {
		kind = "conflict"
	}
	l := []string{"\x00 " + cBold + "Conflict" + cReset + "  " + history.CleanText(v.path) + "  " + cDim + history.CleanText(kind) + cReset,
		"\x00 " + cGreen + "ours: " + history.CleanText(v.c.OursLabel) + cReset + "   " + cTheirs + "theirs: " + history.CleanText(v.c.TheirsLabel) + cReset}
	pane := []string{"the file with markers", "ours", "theirs", "the last version both shared"}[v.pane]
	status := ""
	switch {
	case v.perr != "":
		status = cRed + "the markers don't pair up (" + v.perr + "): e edits the file" + cReset
	case len(v.regions) > 0:
		status = fmt.Sprintf("%d clash(es), %d chosen", len(v.regions), v.chosen())
	case v.c.Markers:
		status = cRed + "clash markers remain" + cReset
	case v.err == "":
		status = cGreen + "no clash markers" + cReset
	}
	l = append(l, "\x00 showing: "+cBold+pane+cReset+cDim+"  (v: next version) · "+cReset+status)
	l = append(l, "\x00 "+cDim+"o ours · t theirs · b both for the clash under the cursor (n/p jump) · w save · O/T keep a whole side · d mark resolved · e editor · Esc back"+cReset, "\x00")
	text := v.paneLines()
	// which part of which clash each line is
	part := make([]int, len(text)) // 0 plain, 1 marker, 2 ours, 3 base, 4 theirs
	ridx := make([]int, len(text))
	for i := range ridx {
		ridx[i] = -1
	}
	if v.pane == 0 && v.err == "" {
		for ri, r := range v.regions {
			for i := r.start; i <= r.end && i < len(text); i++ {
				ridx[i], part[i] = ri, 1
			}
			at := r.start + 1
			for range r.ours {
				if at < len(text) {
					part[at] = 2
				}
				at++
			}
			if r.hasBase {
				at++
				for range r.base {
					if at < len(text) {
						part[at] = 3
					}
					at++
				}
			}
			at++
			for range r.theirs {
				if at < len(text) {
					part[at] = 4
				}
				at++
			}
		}
	}
	for i, s := range text {
		s = history.CleanText(strings.TrimRight(s, "\r"))
		col := ""
		switch part[i] {
		case 1:
			col = cYellow
		case 2:
			col = cGreen
		case 3:
			col = cDim
		case 4:
			col = cTheirs
		}
		tag := ""
		if ridx[i] >= 0 && part[i] == 1 && strings.HasPrefix(s, "<<<<<<<") {
			tag = [4]string{"  ← not chosen yet", "  ← ours", "  ← theirs", "  ← both"}[v.choice[ridx[i]]]
			tag = cDim + tag + cReset
		}
		cursor := "  "
		if i == v.cur {
			cursor = cBold + "› " + cReset
		}
		l = append(l, fmt.Sprintf("%s%s%4d %s%s%s%s", cursor, cDim, i+1, cReset+col, s, cReset, tag))
	}
	return l, v.cur
}
