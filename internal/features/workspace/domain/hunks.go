package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Line kinds in a hunk.
const (
	LineAdd     = "add"
	LineDel     = "del"
	LineContext = "context"
)

// DiffLine is one line of a hunk. Text has no leading +, - or space and no
// line break; a carriage return (CRLF files) stays in it. Old and New are
// the line numbers in the old and the new file, 0 on the side the line is
// not on. NoEOL says the line has no line break at the end of the file
// ("\ No newline at end of file" follows it in the diff).
type DiffLine struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Old   int    `json:"old"`
	New   int    `json:"new"`
	NoEOL bool   `json:"noEol,omitempty"`
}

// Changed reports an added or deleted line, the ones that can be selected.
func (l DiffLine) Changed() bool { return l.Kind != LineContext }

// Hunk is one "@@ -a,b +c,d @@" block of a file's diff. Index counts from 0
// in the order of the diff, and is what the selection refers to; the line
// indexes count every line of the hunk, context included.
type Hunk struct {
	Index  int        `json:"index"`
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`

	oldStart, oldCount, newStart, newCount int
	suffix                                 string // the text after the second @@
}

// FileDiff is the unified diff of one file, taken apart.
type FileDiff struct {
	Header   []string // the lines before the first hunk
	Hunks    []Hunk
	Binary   bool // no lines to select: git shows no text
	Creates  bool // the file is new
	Deletes  bool // the file is deleted
	Renames  bool // renamed or copied
	ModeOnly bool // no hunks, only a mode change (or an empty file added)
}

var hunkHead = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

// ParseDiff takes apart the diff of ONE file (git diff, with or without
// --cached). A diff of several files is refused; so is text that is not a
// unified diff.
func ParseDiff(text string) (FileDiff, error) {
	var fd FileDiff
	if strings.TrimSpace(text) == "" {
		return fd, errors.New("there is no change")
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	files := 0
	i := 0
	for ; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "diff --cc "):
			files++
		case strings.HasPrefix(l, "new file mode"):
			fd.Creates = true
		case strings.HasPrefix(l, "deleted file mode"):
			fd.Deletes = true
		case strings.HasPrefix(l, "rename from ") || strings.HasPrefix(l, "copy from "):
			fd.Renames = true
		case strings.HasPrefix(l, "Binary files ") || l == "GIT binary patch":
			fd.Binary = true
		}
		fd.Header = append(fd.Header, l)
	}
	if files > 1 {
		return fd, errors.New("the diff holds more than one file")
	}
	if files == 0 {
		return fd, errors.New("this is not a unified diff")
	}
	for i < len(lines) {
		m := hunkHead.FindStringSubmatch(lines[i])
		if m == nil {
			return fd, fmt.Errorf("unexpected line in the diff: %q", cut(lines[i]))
		}
		h := Hunk{Index: len(fd.Hunks), Header: lines[i], suffix: m[5]}
		h.oldStart, h.oldCount = atoi(m[1]), count(m[2])
		h.newStart, h.newCount = atoi(m[3]), count(m[4])
		i++
		o, n := h.oldStart, h.newStart
		if h.oldCount == 0 {
			o++
		}
		if h.newCount == 0 {
			n++
		}
		remOld, remNew := h.oldCount, h.newCount
		for i < len(lines) && (remOld > 0 || remNew > 0 || strings.HasPrefix(lines[i], `\`)) {
			l := lines[i]
			i++
			if l == "" { // an editor stripped the space of an empty context line
				l = " "
			}
			switch l[0] {
			case ' ':
				h.Lines = append(h.Lines, DiffLine{Kind: LineContext, Text: l[1:], Old: o, New: n})
				o, n, remOld, remNew = o+1, n+1, remOld-1, remNew-1
			case '-':
				h.Lines = append(h.Lines, DiffLine{Kind: LineDel, Text: l[1:], Old: o})
				o, remOld = o+1, remOld-1
			case '+':
				h.Lines = append(h.Lines, DiffLine{Kind: LineAdd, Text: l[1:], New: n})
				n, remNew = n+1, remNew-1
			case '\\':
				if len(h.Lines) == 0 {
					return fd, errors.New("the diff starts a hunk with a no-newline note")
				}
				h.Lines[len(h.Lines)-1].NoEOL = true
			default:
				return fd, fmt.Errorf("unexpected line in hunk %d: %q", h.Index+1, cut(l))
			}
		}
		if remOld != 0 || remNew != 0 {
			return fd, fmt.Errorf("hunk %d is cut short", h.Index+1)
		}
		fd.Hunks = append(fd.Hunks, h)
	}
	fd.ModeOnly = len(fd.Hunks) == 0 && !fd.Binary
	return fd, nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// count reads a hunk length; git leaves out ",1".
func count(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func cut(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// Fingerprint identifies a diff, so a request to change hunks or lines can
// be refused when the file is no longer as the person saw it.
func Fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// ErrStale is the refusal for a request made on an out-of-date diff.
var ErrStale = errors.New("the file changed since you looked: reload")

// Selection names what to stage, unstage or discard: whole hunks by index,
// and single lines as indexes within a hunk (every line of the hunk counts,
// context too; only added and deleted lines can be selected). The two add
// up.
type Selection struct {
	Hunks []int         `json:"hunks,omitempty"`
	Lines map[int][]int `json:"lines,omitempty"`
}

// resolve checks a selection against the diff and returns, per hunk, the
// selected line indexes.
func (fd FileDiff) resolve(sel Selection) (map[int]map[int]bool, error) {
	if fd.Binary {
		return nil, errors.New("this is a binary file: git shows no lines to pick, so stage or discard the whole file")
	}
	if len(fd.Hunks) == 0 {
		return nil, errors.New("this change has no lines to pick (a mode change): stage or discard the whole file")
	}
	out := map[int]map[int]bool{}
	hunk := func(i int) (Hunk, error) {
		if i < 0 || i >= len(fd.Hunks) {
			return Hunk{}, fmt.Errorf("there is no hunk %d (the file has %d)", i+1, len(fd.Hunks))
		}
		return fd.Hunks[i], nil
	}
	for _, i := range sel.Hunks {
		h, err := hunk(i)
		if err != nil {
			return nil, err
		}
		if out[i] == nil {
			out[i] = map[int]bool{}
		}
		for j, l := range h.Lines {
			if l.Changed() {
				out[i][j] = true
			}
		}
	}
	for i, idx := range sel.Lines {
		h, err := hunk(i)
		if err != nil {
			return nil, err
		}
		for _, j := range idx {
			if j < 0 || j >= len(h.Lines) {
				return nil, fmt.Errorf("hunk %d has no line %d", i+1, j+1)
			}
			if !h.Lines[j].Changed() {
				return nil, fmt.Errorf("line %d of hunk %d is not a change: pick added or deleted lines", j+1, i+1)
			}
			if out[i] == nil {
				out[i] = map[int]bool{}
			}
			out[i][j] = true
		}
	}
	for i := range out {
		if len(out[i]) == 0 {
			delete(out, i)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("pick at least one hunk or line")
	}
	return out, nil
}

// BuildPatch writes the patch for a selection, for git apply --recount.
//
// Forward (stage: apply to the index) the file git applies it to is the old
// side of the diff, so a deleted line that is not picked stays as context
// and an added line that is not picked is left out. Reverse (unstage or
// discard: apply with --reverse) the file is the new side, so an added line
// that is not picked stays as context and a deleted one is left out. The
// returned flag says every change of the file is picked, which is when the
// file's own header (new file, deleted, renamed) can be kept.
func BuildPatch(fd FileDiff, sel Selection, reverse bool) (patch string, whole bool, err error) {
	picked, err := fd.resolve(sel)
	if err != nil {
		return "", false, err
	}
	whole = true
	for _, h := range fd.Hunks {
		for j, l := range h.Lines {
			if l.Changed() && !picked[h.Index][j] {
				whole = false
			}
		}
	}
	if !whole && fd.Renames && !reverse {
		return "", false, errors.New("this file is renamed: stage the whole file, or unstage it and stage the lines")
	}
	var b strings.Builder
	for _, l := range partHeader(fd, whole) {
		b.WriteString(l + "\n")
	}
	hs := make([]int, 0, len(picked))
	for i := range picked {
		hs = append(hs, i)
	}
	sort.Ints(hs)
	delta := 0 // lines the earlier hunks add to the new side
	for _, i := range hs {
		text, d, err := buildHunk(fd.Hunks[i], picked[i], reverse, delta)
		if err != nil {
			return "", false, err
		}
		delta += d
		b.WriteString(text)
	}
	return b.String(), whole, nil
}

// partHeader is what goes before the hunks: the file's own header when all
// of it is picked; otherwise a plain edit of one file (no new file, mode
// or rename line, which the lines do not carry out).
func partHeader(fd FileDiff, whole bool) []string {
	if whole {
		return fd.Header
	}
	var diffLine, minus, plus string
	for _, l := range fd.Header {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			diffLine = l
		case strings.HasPrefix(l, "--- "):
			minus = l
		case strings.HasPrefix(l, "+++ "):
			plus = l
		}
	}
	// The edited file is at the new path (the old one for a deleted file).
	switch {
	case fd.Deletes:
		plus = "+++ " + swapPrefix(minus[min(4, len(minus)):], "a/", "b/")
	case minus == "" || strings.HasPrefix(minus, "--- /dev/null") || fd.Renames:
		minus = "--- " + swapPrefix(plus[min(4, len(plus)):], "b/", "a/")
	}
	if fd.Deletes || fd.Creates || fd.Renames {
		name := strings.TrimPrefix(strings.TrimSuffix(strings.SplitN(minus[min(4, len(minus)):], "\t", 2)[0], "\t"), "a/")
		diffLine = "diff --git a/" + name + " b/" + name
	}
	return []string{diffLine, minus, plus}
}

func swapPrefix(s, from, to string) string {
	if strings.HasPrefix(s, from) {
		return to + s[len(from):]
	}
	return s
}

// buildHunk writes one hunk with the picked lines. delta is how far the
// earlier hunks moved the new side; the second result is this hunk's move.
func buildHunk(h Hunk, picked map[int]bool, reverse bool, delta int) (string, int, error) {
	type out struct {
		sign byte
		line DiffLine
		conv bool // a line that was not picked and became context
	}
	var emit []out
	firstOld, firstNew := h.oldStart, h.newStart // the numbers of the hunk's first line
	if h.oldCount == 0 {
		firstOld++
	}
	if h.newCount == 0 {
		firstNew++
	}
	oldBefore, newBefore, started := 0, 0, false
	for j, l := range h.Lines {
		var o out
		switch {
		case l.Kind == LineContext:
			o = out{sign: ' ', line: l}
		case picked[j] && l.Kind == LineDel:
			o = out{sign: '-', line: l}
		case picked[j]:
			o = out{sign: '+', line: l}
		case l.Kind == LineDel && !reverse, l.Kind == LineAdd && reverse:
			o = out{sign: ' ', line: l, conv: true}
		default: // left out
			if !started {
				if l.Kind != LineAdd {
					oldBefore++
				}
				if l.Kind != LineDel {
					newBefore++
				}
			}
			continue
		}
		started = true
		emit = append(emit, o)
	}
	oc, nc := 0, 0
	for i, o := range emit {
		if o.sign != '+' {
			oc++
		}
		if o.sign != '-' {
			nc++
		}
		// A line without a line break can only be last in the file, so a
		// line that was kept as context cannot have a change after it.
		if o.conv && o.line.NoEOL && i != len(emit)-1 {
			return "", 0, fmt.Errorf("hunk %d ends the file without a line break: pick the whole hunk", h.Index+1)
		}
	}
	fo, fn := firstOld+oldBefore, firstNew+newBefore
	var os, ns int
	if !reverse { // the old side is the real file: its numbers are right
		os = fo
		if oc == 0 {
			os--
		}
		fn = fo + delta
		ns = fn
		if nc == 0 {
			ns--
		}
	} else { // the new side is the real file
		ns = fn
		if nc == 0 {
			ns--
		}
		fo = fn - delta
		os = fo
		if oc == 0 {
			os--
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%s +%s @@%s\n", span(os, oc), span(ns, nc), h.suffix)
	for _, o := range emit {
		b.WriteByte(o.sign)
		b.WriteString(o.line.Text)
		b.WriteByte('\n')
		if o.line.NoEOL {
			b.WriteString("\\ No newline at end of file\n")
		}
	}
	return b.String(), nc - oc, nil
}

// span writes "start,count" the way git does: no count for one line.
func span(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// CountChanges returns how many lines a hunk adds and deletes.
func (h Hunk) CountChanges() (added, deleted int) {
	for _, l := range h.Lines {
		switch l.Kind {
		case LineAdd:
			added++
		case LineDel:
			deleted++
		}
	}
	return
}
