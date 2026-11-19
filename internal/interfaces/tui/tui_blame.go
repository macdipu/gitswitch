package tui

// The Blame and File history views of the Inspect menu (I on History).

import (
	"fmt"
	"strings"
	"time"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// fileLogPage is how many commits one page of file history lists (a var so a test can shrink it).
var fileLogPage = 50

type blameFrame struct {
	rev, path string
	sel       int
}

// blameView is who changed each line of a file, at a commit.
type blameView struct {
	gd, path string
	rev      string // "" is HEAD; "<hash>^" while walking back
	opt      history.BlameOptions
	b        *history.Blame
	sel      int
	stack    []blameFrame // where "blame before this commit" came from
	fromLog  bool         // opened from the file history: Esc goes back there
}

// fileLogView is the commits that changed one file, following renames.
type fileLogView struct {
	gd, path string
	commits  []history.FileCommit
	more     bool
	skip     int
	sel      int
}

// askFile asks for a file path (and a commit to look at it in). When a commit
// is selected on the History list, its files are offered first.
func (t *tui) askFile(title string, withRev bool, done func(path, rev string)) {
	typed := func() {
		fields := []field{{label: "File (path in the repo)", hint: "e.g. cmd/main.go"}}
		if withRev {
			fields = append(fields, field{label: "At (commit, branch or tag)", hint: "empty = the current commit"})
		}
		t.form = &form{title: title, fields: fields, submit: func(v []string) error {
			if err := history.CheckPath(v[0]); err != nil {
				return err
			}
			rev := ""
			if withRev {
				rev = v[1]
			}
			done(v[0], rev)
			return nil
		}}
	}
	hash := t.selectedCommit()
	if hash == "" {
		typed()
		return
	}
	d, err := t.app.History.Show(t.histDir, hash)
	if err != nil || len(d.Files) == 0 {
		typed()
		return
	}
	short := hash[:min(7, len(hash))]
	opts, paths := []string{"Type a path…"}, []string{""}
	for _, f := range d.Files {
		opts, paths = append(opts, history.CleanText(f.Path)+cDim+"  (changed in "+short+")"+cReset), append(paths, f.Path)
	}
	t.picker = &picker{title: title, options: opts, pick: func(i int) error {
		if i == 0 {
			typed()
			return nil
		}
		rev := ""
		if withRev {
			rev = hash
		}
		done(paths[i], rev)
		return nil
	}}
}

// ---------------- blame ----------------

func (t *tui) openBlame(path, rev string, fromLog bool) {
	v := &blameView{gd: t.histDir, path: path, rev: rev, fromLog: fromLog}
	if !t.loadBlame(v) {
		return
	}
	t.insp.gd, t.insp.kind, t.insp.bl = t.histDir, "blame", v
	t.msg = ""
}

// loadBlame reads the blame for the view's path and commit; on an error it
// says why and keeps what was shown.
func (t *tui) loadBlame(v *blameView) bool {
	b, err := t.app.History.Blame(v.gd, v.rev, v.path, v.opt)
	if err != nil {
		t.say(err, "")
		return false
	}
	v.b = b
	v.sel = max(0, min(v.sel, len(b.Lines)-1))
	return true
}

func (v *blameView) line() *history.BlameLine {
	if v.b == nil || v.sel < 0 || v.sel >= len(v.b.Lines) {
		return nil
	}
	return &v.b.Lines[v.sel]
}

// blameBefore blames the file as it was just before the selected line's commit.
func (t *tui) blameBefore() {
	v := t.insp.bl
	l := v.line()
	if l == nil {
		return
	}
	if l.Boundary {
		t.say(nil, "That line comes from the oldest commit git looked at; nothing is before it.")
		return
	}
	next := *v
	next.rev, next.path, next.stack = l.Hash+"^", l.OrigPath, nil
	if next.path == "" {
		next.path = v.path
	}
	next.sel = max(0, l.OrigLine-1)
	if !t.loadBlame(&next) {
		return
	}
	next.stack = append(append([]blameFrame(nil), v.stack...), blameFrame{v.rev, v.path, v.sel})
	*v = next
	t.say(nil, "Before "+l.Short+" (Backspace goes forward again).")
}

func (t *tui) blameBack() {
	v := t.insp.bl
	if len(v.stack) == 0 {
		t.say(nil, "This is the view you opened; Esc closes it.")
		return
	}
	f := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	v.rev, v.path, v.sel = f.rev, f.path, f.sel
	t.loadBlame(v)
}

func (t *tui) keyBlame(k string) {
	v := t.insp.bl
	n := 0
	if v.b != nil {
		n = len(v.b.Lines)
	}
	if t.navKey(k, &v.sel, n) {
		return
	}
	switch k {
	case "esc", "q":
		if v.fromLog && t.insp.fl != nil {
			t.insp.kind, t.insp.bl = "filelog", nil
		} else {
			t.closeInspect()
		}
	case "backspace":
		t.blameBack()
	case "enter":
		if l := v.line(); l != nil {
			t.showCommitOf(l.Hash)
		}
	case "b":
		t.blameBefore()
	case "w":
		v.opt.IgnoreWhitespace = !v.opt.IgnoreWhitespace
		t.loadBlame(v)
	case "m":
		v.opt.Moves = !v.opt.Moves
		t.loadBlame(v)
	case "r":
		t.loadBlame(v)
	}
}

func (t *tui) drawBlame() ([]string, int) {
	v := t.insp.bl
	where := "the current commit"
	if v.rev != "" {
		where = strings.TrimSuffix(v.rev, "^")
		if len(where) > 12 {
			where = where[:7]
		}
		if strings.HasSuffix(v.rev, "^") {
			where = "just before " + where
		}
	}
	opts := ""
	if v.opt.IgnoreWhitespace {
		opts += "  · ignoring whitespace"
	}
	if v.opt.Moves {
		opts += "  · following moved lines"
	}
	l := []string{
		"\x00 " + cBold + "Blame" + cReset + "  " + cBold + history.CleanText(v.path) + cReset + cDim + "  at " + history.CleanText(where) + opts + cReset,
		"\x00",
	}
	if v.b == nil || len(v.b.Lines) == 0 {
		return append(l, " The file is empty at this commit. Esc goes back."), -1
	}
	now := time.Now().Unix()
	sel := v.sel
	prev := ""
	for i, x := range v.b.Lines {
		sh := x.Short
		if x.Hash == prev {
			sh = ""
		}
		prev = x.Hash
		acct := cDim + fmt.Sprintf("%-10s", "-") + cReset
		if x.Account != "" {
			acct = t.color(x.Account) + padANSI(clip(history.CleanText(x.Account), 10), 10) + cReset
		}
		text := strings.ReplaceAll(history.CleanText(x.Text), "\t", " ")
		l = append(l, fmt.Sprintf("%s%s%-7s%s %s%4d%s %-12s %s%-11s%s %s %s", mark(i == sel), cYellow, sh, cReset, cDim, x.Line, cReset,
			clip(history.CleanText(x.Author), 12), cDim, refs.Ago(now, x.Time), cReset, acct, text))
	}
	if v.b.Truncated {
		l = append(l, "", " "+cYellow+fmt.Sprintf("Only the first %d lines are shown (the file is longer).", history.MaxBlameLines)+cReset)
	}
	return l, sel
}

// ---------------- file history ----------------

func (t *tui) openFileLog(path string) {
	v := &fileLogView{gd: t.histDir, path: path}
	if !t.loadFileLog(v) {
		return
	}
	if len(v.commits) == 0 {
		t.say(fmt.Errorf("no commit changed %s: check the path", path), "")
		return
	}
	t.insp.gd, t.insp.kind, t.insp.fl = t.histDir, "filelog", v
	t.msg = ""
}

func (t *tui) loadFileLog(v *fileLogView) bool {
	r, err := t.app.History.FileHistory(v.gd, v.path, fileLogPage, v.skip)
	if err != nil {
		t.say(err, "")
		return false
	}
	v.commits, v.more = r.Commits, r.More
	v.sel = max(0, min(v.sel, len(v.commits)-1))
	return true
}

func (v *fileLogView) commit() *history.FileCommit {
	if v.sel < 0 || v.sel >= len(v.commits) {
		return nil
	}
	return &v.commits[v.sel]
}

func (t *tui) keyFileLog(k string) {
	v := t.insp.fl
	if t.navKey(k, &v.sel, len(v.commits)) {
		return
	}
	c := v.commit()
	switch k {
	case "esc", "q":
		t.closeInspect()
	case "n", "p":
		next := *v
		if k == "n" {
			if !v.more {
				t.say(nil, "That is the file's first commit.")
				return
			}
			next.skip += fileLogPage
		} else {
			if v.skip == 0 {
				t.say(nil, "This is the newest page.")
				return
			}
			next.skip = max(0, v.skip-fileLogPage)
		}
		next.sel = 0
		if t.loadFileLog(&next) {
			*v = next
		}
	case "r":
		t.loadFileLog(v)
	case "enter", "d":
		if c != nil {
			t.showFileDiff(c)
		}
	case "b":
		if c == nil {
			return
		}
		if c.Status == "D" {
			t.say(fmt.Errorf("the file is deleted in %s: blame an older commit", c.Short), "")
			return
		}
		t.openBlame(c.Path, c.Hash, true)
	}
}

// showFileDiff shows the file's diff in one commit.
func (t *tui) showFileDiff(c *history.FileCommit) {
	d, err := t.app.History.Diff(t.insp.fl.gd, c.Hash, c.Path)
	if err != nil {
		t.say(err, "")
		return
	}
	lines := diffLines(d.Text)
	if d.Text == "" {
		lines = []string{"This commit has no text change in " + history.CleanText(c.Path) + " (a pure rename, a mode change or a binary file)."}
	}
	if d.Truncated {
		lines = append(lines, "", cDim+fmt.Sprintf("… cut at %d KB; git show shows all of it", history.MaxDiffBytes>>10)+cReset)
	}
	t.view = &viewer{title: history.CleanText(c.Path) + " · " + c.Short, lines: lines, copy: c.Hash}
}

// diffLines colours a unified diff.
func diffLines(text string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		l = history.CleanText(l)
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			l = cBold + l + cReset
		case strings.HasPrefix(l, "+"):
			l = cGreen + l + cReset
		case strings.HasPrefix(l, "-"):
			l = cRed + l + cReset
		case strings.HasPrefix(l, "@@"):
			l = cYellow + l + cReset
		case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			l = cDim + l + cReset
		}
		lines = append(lines, l)
	}
	return lines
}

func (t *tui) drawFileLog() ([]string, int) {
	v := t.insp.fl
	page := fmt.Sprintf("commits %d-%d", v.skip+1, v.skip+len(v.commits))
	if v.more {
		page += " · n older page"
	}
	if v.skip > 0 {
		page += " · p newer page"
	}
	l := []string{"\x00 " + cBold + "File history" + cReset + "  " + cBold + history.CleanText(v.path) + cReset + cDim + "  follows renames · " + page + cReset, "\x00"}
	now := time.Now().Unix()
	for i, c := range v.commits {
		col := cYellow
		switch c.Status {
		case "A":
			col = cGreen
		case "D":
			col = cRed
		case "R":
			col = cDim + cYellow
		}
		who := history.CleanText(c.Author)
		if c.Account != "" {
			who = t.color(c.Account) + "[" + history.CleanText(c.Account) + "]" + cReset + " " + who
		}
		line := fmt.Sprintf("%s%s%s%s %s%s%s %s%-10s%s %s  %s", mark(i == v.sel), col, c.Status, cReset, cYellow, c.Short, cReset,
			cDim, refs.Ago(now, c.Time), cReset, history.CleanText(c.Subject), cDim+who+cReset)
		if c.Status == "R" {
			line += cDim + fmt.Sprintf("  (renamed from %s, %d%% alike)", history.CleanText(c.OldPath), c.Similarity) + cReset
		} else if c.Path != v.path {
			line += cDim + "  (" + history.CleanText(c.Path) + ")" + cReset
		}
		l = append(l, line)
	}
	return l, v.sel
}
