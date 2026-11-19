package tui

// The Inspect menu (I on History): blame, file history, compare, reflog,
// bisect and a search of the code changes. Blame and file history are in
// tui_blame.go.

import (
	"fmt"
	"strings"
	"time"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// inspectState is the Inspect view that is open on the History page, if any.
// Only the one named by kind is drawn.
type inspectState struct {
	kind string // "", blame, filelog, compare, reflog or bisect
	gd   string
	bl   *blameView
	fl   *fileLogView
	cm   *compareView
	rf   *reflogView
	bs   *bisectView
}

// inspecting reports whether an Inspect view is open for the shown repo.
func (t *tui) inspecting() bool { return t.insp.kind != "" && t.insp.gd == t.histDir }

// closeInspect leaves the Inspect views and reads the commits again (a
// recovered branch or a bisect moved things).
func (t *tui) closeInspect() {
	t.insp = inspectState{}
	sel := t.sel[tabHistory]
	t.readHistory()
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

// navKey moves a selection in a list of n rows.
func (t *tui) navKey(k string, sel *int, n int) bool {
	page := max(1, t.h-6)
	switch k {
	case "up", "k":
		*sel = max(0, *sel-1)
	case "down", "j":
		*sel = max(0, min(n-1, *sel+1))
	case "pgup":
		*sel = max(0, *sel-page)
	case "pgdn", " ":
		*sel = max(0, min(n-1, *sel+page))
	case "home", "g":
		*sel = 0
	case "end", "G":
		*sel = max(0, n-1)
	default:
		return false
	}
	return true
}

// inspectMenu is I on History.
func (t *tui) inspectMenu() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	t.picker = &picker{title: "Inspect", options: []string{
		"Blame a file…  who changed each line",
		"File history…  every commit that changed a file, across renames",
		"Compare two refs…  branches, tags or commits",
		"Reflog…  find lost commits and branches",
		"Bisect…  find the commit that broke something",
		"Search code changes…  commits that added or removed a text",
	}, pick: func(i int) error {
		switch i {
		case 0:
			t.askFile("Blame a file", true, func(p, rev string) { t.openBlame(p, rev, false) })
		case 1:
			t.askFile("File history", false, func(p, _ string) { t.openFileLog(p) })
		case 2:
			t.compareForm("", "", inspect.SinceFork)
		case 3:
			t.openReflog()
		case 4:
			t.openBisect()
		case 5:
			t.codeSearch()
		}
		return nil
	}}
}

// keyInspect handles a key while an Inspect view is shown. Keys it does not
// use (switching page, help) go to the main handler.
func (t *tui) keyInspect(k string) bool {
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	}
	switch t.insp.kind {
	case "blame":
		t.keyBlame(k)
	case "filelog":
		t.keyFileLog(k)
	case "compare":
		t.keyCompare(k)
	case "reflog":
		t.keyReflog(k)
	case "bisect":
		t.keyBisect(k)
	}
	return true
}

func (t *tui) drawInspect() ([]string, int) {
	switch t.insp.kind {
	case "blame":
		return t.drawBlame()
	case "filelog":
		return t.drawFileLog()
	case "compare":
		return t.drawCompare()
	case "reflog":
		return t.drawReflog()
	}
	return t.drawBisect()
}

func (t *tui) inspectFooter() string {
	switch t.insp.kind {
	case "blame":
		return "↑↓ move · Enter commit · b blame before this commit · Backspace back · w ignore whitespace · m moved lines · r reload · Esc close · ? help"
	case "filelog":
		return "↑↓ move · Enter diff · b blame at this commit · n older · p newer · r reload · Esc close · ? help"
	case "compare":
		return "↑↓ move · Enter file diff · m since fork / direct · e change refs · r reload · Esc close · ? help"
	case "reflog":
		return "↑↓ move · Enter commit · b make a branch here · l lost only · m older · r reload · Esc close · ? help"
	}
	if b := t.insp.bs; b != nil && b.st.Culprit != nil {
		return "Enter the commit · x go back to " + b.st.Original + " · Esc close (the bisect stays) · ? help"
	}
	return "g good · b bad · s skip · x stop · Enter the commit · Esc close (the bisect stays) · ? help"
}

// bisectBanner is the line above the commits while a bisect runs.
func (t *tui) bisectBanner() string {
	if t.head == nil || t.head.Busy != "bisect" {
		return ""
	}
	return "\x00 " + cYellow + "Bisecting…" + cReset + cDim + "  looking for the commit that broke something · I then Bisect to go on or stop" + cReset
}

func ago(ts int64) string { return refs.Ago(time.Now().Unix(), ts) }

func plainWord(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---------------- compare ----------------

type compareView struct {
	gd, a, b string
	mode     inspect.Mode
	c        *inspect.Compared
	sel      int // among the changed files
}

// compareRows is how many commits of each side are listed on screen (the
// service reads up to inspect.MaxCommits).
const compareRows = 8

// compareForm asks for the two sides; B is the current branch unless said.
func (t *tui) compareForm(a, b string, mode inspect.Mode) {
	if b == "" {
		b = "HEAD"
		if t.head != nil && t.head.Branch != "" {
			b = t.head.Branch
		}
	}
	gd := t.histDir
	t.form = &form{title: "Compare two refs", fields: []field{
		{label: "A (branch, tag or commit)", value: a},
		{label: "B (what is compared to A)", value: b, hint: inspect.Working + " = the files on disk"},
		{label: "Show", value: string(mode), options: []string{string(inspect.SinceFork), string(inspect.Direct)}, hint: "←→: since they went apart, or the direct difference"},
	}, submit: func(v []string) error {
		if err := inspect.CheckSide(v[0], false); err != nil {
			return err
		}
		if err := inspect.CheckSide(v[1], true); err != nil {
			return err
		}
		m, err := inspect.ParseMode(v[2])
		if err != nil {
			return err
		}
		cv := &compareView{gd: gd, a: v[0], b: v[1], mode: m}
		if !t.loadCompare(cv) {
			return nil
		}
		t.insp.gd, t.insp.kind, t.insp.cm = gd, "compare", cv
		t.msg = ""
		return nil
	}}
}

func (t *tui) loadCompare(v *compareView) bool {
	c, err := t.app.Inspect.Compare(v.gd, v.a, v.b, v.mode)
	if err != nil {
		t.say(err, "")
		return false
	}
	v.c = &c
	v.sel = max(0, min(v.sel, len(c.Files)-1))
	return true
}

func (t *tui) keyCompare(k string) {
	v := t.insp.cm
	if t.navKey(k, &v.sel, len(v.c.Files)) {
		return
	}
	switch k {
	case "esc", "q":
		t.closeInspect()
	case "m":
		if v.mode == inspect.Direct {
			v.mode = inspect.SinceFork
		} else {
			v.mode = inspect.Direct
		}
		t.loadCompare(v)
	case "r":
		t.loadCompare(v)
	case "e":
		t.compareForm(v.a, v.b, v.mode)
	case "enter", "d":
		if v.sel >= len(v.c.Files) {
			return
		}
		f := v.c.Files[v.sel]
		d, err := t.app.Inspect.CompareFile(v.gd, v.a, v.b, v.mode, f.Path)
		if err != nil {
			t.say(err, "")
			return
		}
		lines := diffLines(d.Text)
		if d.Text == "" {
			lines = []string{"No text change in " + history.CleanText(f.Path) + "."}
		}
		if d.Truncated {
			lines = append(lines, "", cDim+fmt.Sprintf("… cut at %d KB; git diff shows all of it", inspect.MaxDiffBytes>>10)+cReset)
		}
		t.view = &viewer{title: history.CleanText(f.Path) + " · " + history.CleanText(v.a) + " → " + history.CleanText(v.b), lines: lines}
	}
}

func (t *tui) drawCompare() ([]string, int) {
	v := t.insp.cm
	c := v.c
	b := history.CleanText(v.b)
	if c.Working {
		b = "your working folder"
	}
	how := "what B changed since they went apart"
	if v.mode == inspect.Direct {
		how = "the direct difference"
	}
	l := []string{
		"\x00 " + cBold + "Compare" + cReset + "  " + cBold + history.CleanText(v.a) + cReset + " → " + cBold + b + cReset + cDim + "  " + how + " · m switches" + cReset,
		"\x00 " + history.CleanText(c.Summary),
		"\x00",
	}
	if c.Same {
		return append(l, " Nothing differs. Esc goes back; e picks other refs."), -1
	}
	side := func(title string, cs []inspect.Commit, total int) {
		l = append(l, " "+cBold+title+cReset+cDim+fmt.Sprintf(" (%d)", total)+cReset)
		if total == 0 {
			l = append(l, "   "+cDim+"none"+cReset)
		}
		for i, x := range cs {
			if i == compareRows {
				break
			}
			l = append(l, fmt.Sprintf("   %s%s%s %s  %s", cYellow, x.Short, cReset, history.CleanText(x.Subject), cDim+history.CleanText(x.Author)+" · "+ago(x.Time)+cReset))
		}
		if total > min(len(cs), compareRows) {
			l = append(l, "   "+cDim+fmt.Sprintf("… and %d more", total-min(len(cs), compareRows))+cReset)
		}
		l = append(l, "")
	}
	side("Only on A", c.OnlyA, c.OnlyACount)
	side("Only on B", c.OnlyB, c.OnlyBCount)
	l = append(l, " "+cBold+"Changed files"+cReset+cDim+fmt.Sprintf(" (%d)  +%d -%d", len(c.Files)+c.MoreFiles, c.Added, c.Deleted)+cReset)
	if len(c.Files) == 0 {
		l = append(l, "   "+cDim+"none"+cReset)
	}
	selLine := -1
	for i, f := range c.Files {
		on := i == v.sel
		if on {
			selLine = len(l) - 3
		}
		letter, col := "M", cYellow
		switch f.Status {
		case "added":
			letter, col = "A", cGreen
		case "deleted":
			letter, col = "D", cRed
		case "renamed":
			letter = "R"
		case "copied":
			letter = "C"
		case "type changed":
			letter = "T"
		}
		extra := fmt.Sprintf("+%d -%d", f.Added, f.Deleted)
		if f.Binary {
			extra = "binary"
		}
		name := history.CleanText(f.Path)
		if f.OldPath != "" {
			name = history.CleanText(f.OldPath) + " → " + name
		}
		l = append(l, fmt.Sprintf("%s%s%s%s %s%s%s  %s", mark(on), col, letter, cReset, boldIf(on), name, cReset, cDim+extra+cReset))
	}
	if c.MoreFiles > 0 {
		l = append(l, "   "+cDim+fmt.Sprintf("… and %d more files", c.MoreFiles)+cReset)
	}
	return l, selLine
}

// ---------------- reflog ----------------

type reflogView struct {
	gd      string
	entries []inspect.Entry
	more    bool
	next    int // the skip that reads the next page
	lost    bool
	sel     int
}

const reflogPage = 100

func (t *tui) openReflog() {
	v := &reflogView{gd: t.histDir}
	if !t.loadReflog(v, false) {
		return
	}
	t.insp.gd, t.insp.kind, t.insp.rf = t.histDir, "reflog", v
	t.msg = ""
}

// loadReflog reads the first page, or with more the page after the last one.
func (t *tui) loadReflog(v *reflogView, more bool) bool {
	skip := 0
	if more {
		skip = v.next
	}
	r, err := t.app.Inspect.Reflog(v.gd, "HEAD", reflogPage, skip, v.lost)
	if err != nil {
		t.say(err, "")
		return false
	}
	if more {
		v.entries = append(v.entries, r.Entries...)
	} else {
		v.entries = r.Entries
	}
	v.more, v.next = r.More, r.Next
	v.sel = max(0, min(v.sel, len(v.entries)-1))
	return true
}

func (t *tui) keyReflog(k string) {
	v := t.insp.rf
	if t.navKey(k, &v.sel, len(v.entries)) {
		return
	}
	var e *inspect.Entry
	if v.sel < len(v.entries) {
		e = &v.entries[v.sel]
	}
	switch k {
	case "esc", "q":
		t.closeInspect()
	case "r":
		t.loadReflog(v, false)
	case "l":
		v.lost, v.sel = !v.lost, 0
		t.loadReflog(v, false)
	case "m":
		if !v.more {
			t.say(nil, "No older entries.")
			return
		}
		t.loadReflog(v, true)
	case "enter":
		if e != nil {
			t.showCommitOf(e.Hash)
		}
	case "b":
		if e != nil {
			t.recoverForm(*e)
		}
	}
}

// recoverForm asks for a branch name and makes the branch at a reflog entry.
func (t *tui) recoverForm(e inspect.Entry) {
	gd := t.insp.rf.gd
	t.form = &form{title: "Make a branch at " + e.Short, fields: []field{
		{label: "Branch name", hint: "your files and the current branch do not move"},
	}, submit: func(v []string) error {
		r, err := t.app.Inspect.Recover(gd, "HEAD", e.Index, "", v[0])
		if err != nil {
			return err
		}
		t.say(nil, fmt.Sprintf("Made branch %s at %s. To switch to it: Esc, then b and pick it (or git switch %s). Delete the branch to take it back.", r.Name, r.Short, r.Name))
		if t.insp.rf != nil {
			t.loadReflog(t.insp.rf, false) // the entry is no longer lost
		}
		return nil
	}}
}

func (t *tui) drawReflog() ([]string, int) {
	v := t.insp.rf
	what := "HEAD"
	if v.lost {
		what += cYellow + "  · lost commits only" + cReset
	}
	l := []string{"\x00 " + cBold + "Reflog" + cReset + "  " + what + cDim + "  lost = on no branch, tag or remote branch · b makes a branch there · l lost only" + cReset, "\x00"}
	if len(v.entries) == 0 {
		if v.lost {
			return append(l, " No lost commits in the reflog. l shows everything."), -1
		}
		return append(l, " The reflog is empty."), -1
	}
	for i, e := range v.entries {
		msg := e.Message
		if msg == "" {
			msg = e.Subject
		}
		lost := ""
		if e.Lost {
			lost = " " + cRed + "lost" + cReset
		}
		l = append(l, fmt.Sprintf("%s%s%s %-10s %s  %s%s", mark(i == v.sel), cYellow, e.Short, cReset, history.CleanText(e.Action)+" "+history.CleanText(clip(msg, 60)), cDim+ago(e.Time)+cReset, lost))
	}
	if v.more {
		l = append(l, "", " "+cDim+"m reads older entries"+cReset)
	}
	return l, v.sel
}

// ---------------- bisect ----------------

type bisectView struct {
	gd string
	st inspect.State
}

// openBisect shows the running bisect, or asks for the two ends to start one.
func (t *tui) openBisect() {
	gd := t.histDir
	st, err := t.app.Inspect.BisectStatus(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if st.Running {
		t.insp.gd, t.insp.kind, t.insp.bs = gd, "bisect", &bisectView{gd: gd, st: st}
		t.msg = ""
		return
	}
	t.form = &form{title: "Bisect: find the commit that broke something", fields: []field{
		{label: "Bad commit (broken)", value: "HEAD", hint: "a commit, branch or tag where it is broken"},
		{label: "Good commit (worked)", hint: "an older commit where it still worked"},
	}, submit: func(v []string) error {
		if v[1] == "" {
			return fmt.Errorf("say a commit where it still worked")
		}
		st, err := t.app.Inspect.BisectStart(gd, v[0], v[1])
		if err != nil {
			return err
		}
		t.insp.gd, t.insp.kind, t.insp.bs = gd, "bisect", &bisectView{gd: gd, st: st}
		t.readHistory()
		t.say(nil, st.Message)
		return nil
	}}
}

func (t *tui) keyBisect(k string) {
	v := t.insp.bs
	switch k {
	case "esc", "q":
		t.closeInspect()
	case "enter":
		switch {
		case v.st.Culprit != nil:
			t.showCommitOf(v.st.Culprit.Hash)
		case v.st.Next != nil:
			t.showCommitOf(v.st.Next.Hash)
		}
	case "g", "b", "s":
		if v.st.Culprit != nil || len(v.st.Maybe) > 0 {
			t.say(fmt.Errorf("the search is over: x goes back to %s", v.st.Original), "")
			return
		}
		mk := map[string]string{"g": inspect.Good, "b": inspect.Bad, "s": inspect.Skip}[k]
		st, err := t.app.Inspect.BisectMark(v.gd, mk)
		if err != nil {
			t.say(err, "")
			return
		}
		v.st = st
		t.readHistory()
		t.say(nil, st.Message)
	case "x":
		text := "Stop the bisect and go back to " + v.st.Original + "? (y/n)"
		if v.st.Culprit != nil {
			text = "Go back to " + v.st.Original + " and end the bisect? The culprit is " + v.st.Culprit.Short + ". (y/n)"
		}
		gd := v.gd
		t.confirm = &confirmBox{text, map[string]func() error{"y": func() error {
			st, err := t.app.Inspect.BisectReset(gd)
			if err != nil {
				return err
			}
			t.closeInspect()
			t.say(nil, st.Message)
			return nil
		}}}
	}
}

func (t *tui) drawBisect() ([]string, int) {
	v := t.insp.bs
	st := v.st
	l := []string{"\x00 " + cBold + "Bisect" + cReset + cDim + "  started on " + history.CleanText(st.Original) + cReset, "\x00"}
	switch {
	case st.Culprit != nil:
		c := st.Culprit
		l = append(l, " "+cRed+cBold+"The first bad commit"+cReset+"  "+cYellow+c.Short+cReset+" "+history.CleanText(c.Subject),
			"   "+cDim+history.CleanText(c.Author)+" · "+ago(c.Time)+cReset, "", " "+cBold+"Files it changed"+cReset+cDim+fmt.Sprintf(" (%d)", len(c.Files)+c.FilesMore)+cReset)
		for _, f := range c.Files {
			l = append(l, "   "+history.CleanText(f))
		}
		if c.FilesMore > 0 {
			l = append(l, "   "+cDim+fmt.Sprintf("… and %d more", c.FilesMore)+cReset)
		}
		l = append(l, "", " Press "+cBold+"x"+cReset+" to go back to "+cBold+history.CleanText(st.Original)+cReset+" and end the bisect.")
	case len(st.Maybe) > 0:
		l = append(l, " "+cYellow+history.CleanText(st.Message)+cReset)
		for _, c := range st.Maybe {
			l = append(l, "   "+cYellow+c.Short+cReset+" "+history.CleanText(c.Subject))
		}
		l = append(l, "", " Press "+cBold+"x"+cReset+" to go back to "+cBold+history.CleanText(st.Original)+cReset+".")
	case st.Next != nil:
		l = append(l, " Test this commit, then mark it:  "+cYellow+st.Next.Short+cReset+" "+cBold+history.CleanText(st.Next.Subject)+cReset,
			"   "+cDim+history.CleanText(st.Next.Author)+" · "+ago(st.Next.Time)+cReset,
			" "+inspect.StepsWords(st.StepsLeft)+" left ("+plainWord(st.Left, "commit", "commits")+" in doubt)",
			"", " "+cGreen+"g"+cReset+" good (it works)   "+cRed+"b"+cReset+" bad (it is broken)   s skip (can't test it)   x stop")
	default:
		l = append(l, " "+history.CleanText(st.Message))
	}
	if len(st.Steps) > 0 {
		l = append(l, "", " "+cBold+"Steps so far"+cReset)
		for _, s := range st.Steps {
			col := cYellow
			switch s.Mark {
			case inspect.Good:
				col = cGreen
			case inspect.Bad:
				col = cRed
			}
			l = append(l, fmt.Sprintf("   %s%-5s%s %s%s%s %s", col, s.Mark, cReset, cYellow, s.Short, cReset, history.CleanText(s.Subject)))
		}
	}
	return l, -1
}

// ---------------- search the code changes ----------------

// codeSearch asks for a text to look for in the commits' changes and filters
// the History list to the commits that added or removed it. Empty clears it.
func (t *tui) codeSearch() {
	f := t.filter
	kind, icase := "text (-S)", "no"
	if f.PickaxeRegex {
		kind = "regex (-G)"
	}
	if f.PickaxeIgnoreCase {
		icase = "yes"
	}
	t.form = &form{title: "Search code changes (empty = show all)", fields: []field{
		{label: "Code contains", value: f.Pickaxe, hint: "commits that added or removed this"},
		{label: "Match as", value: kind, options: []string{"text (-S)", "regex (-G)"}, hint: "←→: a plain text, or a regex on the changed lines"},
		{label: "Ignore case", value: icase, options: []string{"no", "yes"}, hint: "←→ to change"},
	}, submit: func(v []string) error {
		old := t.filter
		nf := old
		nf.Pickaxe, nf.PickaxeRegex, nf.PickaxeIgnoreCase = v[0], strings.HasPrefix(v[1], "regex"), v[2] == "yes"
		if v[0] == "" {
			nf.PickaxeRegex, nf.PickaxeIgnoreCase = false, false
		}
		if err := nf.CheckPickaxe(); err != nil {
			return err
		}
		t.filter = nf
		t.loadHistory()
		if t.hist == nil { // git refused it (a bad regex): keep what was shown
			msg := t.msg
			t.filter = old
			t.loadHistory()
			t.msg, t.msgErr = msg, true
		}
		return nil
	}}
}
