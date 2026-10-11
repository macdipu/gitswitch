// Package domain is the commit history as the apps show it: the graph
// layout, the account behind each commit and the rule that flags
// wrong-account commits. It is pure: the git log is read by the
// infrastructure package. Import it as history.
package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

// Commit is one commit with its place in the graph.
type Commit struct {
	Hash     string   `json:"hash"`
	Short    string   `json:"short"`
	Parents  []string `json:"-"`
	Email    string   `json:"email"`
	Author   string   `json:"author"`
	Time     int64    `json:"time"`
	Refs     string   `json:"refs"`
	Subject  string   `json:"subject"`
	Col      int      `json:"col"`
	Edges    [][2]int `json:"edges"` // [fromCol in this row, toCol in next row]
	Account  string   `json:"account"`
	Flagged  bool     `json:"flagged"`
	Unpushed bool     `json:"unpushed"`
}

// History is the latest commits of a repo.
type History struct {
	Repo     repo.Info `json:"repo"`
	Commits  []Commit  `json:"commits"`
	Lanes    int       `json:"lanes"`
	Flagged  int       `json:"flagged"`
	Unpushed int       `json:"unpushed"`
}

// LayoutLanes assigns each commit a column and the line segments to the next row.
func LayoutLanes(cs []Commit) int {
	var lanes []string
	maxW := 0
	idx := func(h string) int {
		for i, l := range lanes {
			if l == h {
				return i
			}
		}
		return -1
	}
	free := func(skip int) int {
		for i, l := range lanes {
			if l == "" && i != skip {
				return i
			}
		}
		lanes = append(lanes, "")
		return len(lanes) - 1
	}
	for r := range cs {
		c := &cs[r]
		before := append([]string(nil), lanes...)
		col := idx(c.Hash)
		if col < 0 {
			col = free(-1)
		}
		for i := range lanes {
			if lanes[i] == c.Hash {
				lanes[i] = ""
			}
		}
		c.Col = col
		parentAt := map[string]bool{}
		for k, p := range c.Parents {
			parentAt[p] = true
			if k == 0 {
				if j := idx(p); j >= 0 {
					if j > col { // keep the graph left-aligned: pull it into this lane
						lanes[j] = ""
						lanes[col] = p
					}
					continue // otherwise join the lane on the left
				}
				lanes[col] = p
				continue
			}
			if idx(p) < 0 {
				lanes[free(col)] = p
			}
		}
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
		}
		if len(lanes) > maxW {
			maxW = len(lanes)
		}
		if col+1 > maxW {
			maxW = col + 1
		}
		// Where does each lane go in the next row?
		var next string
		nextCol := -1
		if r+1 < len(cs) {
			next = cs[r+1].Hash
			nextCol = idx(next)
		}
		target := func(j int) int {
			if j < len(lanes) && lanes[j] == next && nextCol >= 0 {
				return nextCol
			}
			return j
		}
		for j := range lanes {
			h := lanes[j]
			if h == "" {
				continue
			}
			if parentAt[h] {
				c.Edges = append(c.Edges, [2]int{col, target(j)})
			}
		}
		// Lines passing through this row (or moving to another lane).
		for j, h := range before {
			if h == "" || h == c.Hash || j == col {
				continue
			}
			if k := idx(h); k >= 0 {
				c.Edges = append(c.Edges, [2]int{j, target(k)})
			}
		}
	}
	return maxW
}

// IsWrongAccount: the commit used one of your other accounts, or an unknown
// email on a commit that exists only on this computer. Teammates' commits
// (unknown emails already on the remote) are never flagged.
func IsWrongAccount(c *account.Config, repoAcct *account.Account, email string, unpushed bool) bool {
	if repoAcct.Email == "" || strings.EqualFold(email, repoAcct.Email) {
		return false
	}
	if other := c.AccountByEmail(email); other != nil {
		return true
	}
	return unpushed
}

// AccountColor gives each account the same colour in the TUI and log.
func AccountColor(c *account.Config, id string) string {
	pal := []string{"38;5;33", "38;5;37", "38;5;135", "38;5;172", "38;5;168", "38;5;70"}
	for i, a := range c.Accounts {
		if a.ID == id {
			return pal[i%len(pal)]
		}
	}
	return "2"
}

// StripANSI removes colour codes.
func StripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ErrNoCommits is returned for a repo that has no commits yet.
var ErrNoCommits = errors.New("this repo has no commits yet")

// Annotate fills in each commit's account, whether it is pushed and whether
// it used the wrong account, and the history's totals.
func Annotate(c *account.Config, h *History, cs []Commit, unpushed map[string]bool) {
	repoAcct := c.Account(h.Repo.Account)
	for i := range cs {
		if a := c.AccountByEmail(cs[i].Email); a != nil {
			cs[i].Account = a.ID
		}
		cs[i].Unpushed = unpushed[cs[i].Hash]
		if repoAcct != nil && IsWrongAccount(c, repoAcct, cs[i].Email, cs[i].Unpushed) {
			cs[i].Flagged = true
			h.Flagged++
		}
		if cs[i].Unpushed {
			h.Unpushed++
		}
	}
	h.Commits = cs
}

func paint(color bool, code, s string) string {
	if !color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Filter narrows the history. Author, Text, Since and Until are passed to
// git, so they search the whole history, not only the latest commits.
type Filter struct {
	Author  string `json:"author"`  // part of the author's name or email
	Text    string `json:"text"`    // part of the commit message
	Since   string `json:"since"`   // e.g. 2024-05-01 or "2 weeks ago"
	Until   string `json:"until"`   // e.g. 2024-06-30
	Account string `json:"account"` // gitswitch account that made the commit
}

// Active reports whether the filter narrows anything.
func (f Filter) Active() bool { return f != Filter{} }

// Keep applies the account part of the filter, which git cannot know.
func (f Filter) Keep(cs []Commit) []Commit {
	if f.Account == "" {
		return cs
	}
	var out []Commit
	for _, c := range cs {
		if strings.EqualFold(c.Account, f.Account) {
			out = append(out, c)
		}
	}
	return out
}

// Describe is a short summary, e.g. `author "ana", since 2024-05-01`.
func (f Filter) Describe() string {
	var parts []string
	for _, p := range [][2]string{{"author", f.Author}, {"text", f.Text}, {"since", f.Since}, {"until", f.Until}, {"account", f.Account}} {
		if p[1] != "" {
			parts = append(parts, fmt.Sprintf("%s %q", p[0], p[1]))
		}
	}
	return strings.Join(parts, ", ")
}

// ParseDate reads a filter date: 2024-05-01, 2024-05-01 14:30, today,
// yesterday, or "3 weeks ago" (seconds, minutes, hours, days, weeks,
// months, years). A day without a time means its first second, or with
// endOfDay its last, so both ends of a range are included.
func ParseDate(s string, now time.Time, endOfDay bool) (time.Time, error) {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	day := func(t time.Time) time.Time {
		y, m, d := t.Date()
		start := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		if endOfDay {
			return start.Add(24*time.Hour - time.Second)
		}
		return start
	}
	bad := fmt.Errorf("cannot read the date %q; use 2024-05-01, 2024-05-01 14:30, yesterday or 2 weeks ago", s)
	var t time.Time
	switch {
	case s == "today":
		return day(now), nil
	case s == "yesterday":
		return day(now.AddDate(0, 0, -1)), nil
	case strings.HasSuffix(s, " ago"):
		f := strings.Fields(strings.TrimSuffix(s, " ago"))
		if len(f) != 2 {
			return t, bad
		}
		n, err := strconv.Atoi(f[0])
		if err != nil || n < 0 {
			return t, bad
		}
		switch strings.TrimSuffix(f[1], "s") {
		case "second":
			return now.Add(-time.Duration(n) * time.Second), nil
		case "minute":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "hour":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "day":
			return now.AddDate(0, 0, -n), nil
		case "week":
			return now.AddDate(0, 0, -7*n), nil
		case "month":
			return now.AddDate(0, -n, 0), nil
		case "year":
			return now.AddDate(-n, 0, 0), nil
		}
		return t, bad
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if v, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			t = v
			break
		}
	}
	if t.IsZero() {
		v, err := time.ParseInLocation("2006-01-02", s, now.Location())
		if err != nil {
			return t, bad
		}
		t = day(v)
	}
	if t.Year() < 1970 || t.Year() > 2099 {
		return time.Time{}, fmt.Errorf("the date %q is out of range; use a year from 1970 to 2099", s)
	}
	return t, nil
}

// ForGit checks the filter and turns its dates into exact times for git
// (@<unix seconds>), because git quietly guesses at dates it cannot read.
// none is true when no commit can match (the range is in the future).
func (f Filter) ForGit(now time.Time) (g Filter, none bool, err error) {
	g = f
	var since, until time.Time
	if f.Since != "" {
		if since, err = ParseDate(f.Since, now, false); err != nil {
			return g, false, err
		}
		g.Since = "@" + strconv.FormatInt(since.Unix(), 10)
	}
	if f.Until != "" {
		if until, err = ParseDate(f.Until, now, true); err != nil {
			return g, false, err
		}
		g.Until = "@" + strconv.FormatInt(until.Unix(), 10)
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return g, false, fmt.Errorf("the start date %s is after the end date %s", f.Since, f.Until)
	}
	return g, !since.IsZero() && since.After(now), nil
}

// ListRows lists commits one per line without the graph (used while a
// filter hides some commits, so graph lines would not connect).
func ListRows(c *account.Config, hist *History, color bool) []Row {
	var rows []Row
	for _, cm := range hist.Commits {
		label := cm.Account
		if label == "" {
			label = "unknown"
		}
		tag := paint(color, AccountColor(c, cm.Account), "["+label+"]")
		if cm.Flagged {
			tag = paint(color, "1;31", "["+label+" !]")
		}
		date := time.Unix(cm.Time, 0).Format("2006-01-02")
		tail := ""
		if cm.Unpushed {
			tail = "  " + paint(color, "2", "(not pushed)")
		}
		rows = append(rows, Row{fmt.Sprintf("%s %s %s %s  %s%s", paint(color, "33", cm.Short), date, tag, cm.Subject, paint(color, "2", cm.Author), tail), cm.Hash})
	}
	if len(rows) == 0 {
		rows = append(rows, Row{Text: "No commits match the filter."})
	}
	return rows
}

// Row is one line of a rendered history and the commit on it ("" for a
// line that only continues the graph).
type Row struct {
	Text string
	Hash string
}

// Texts returns the rows' text.
func Texts(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Text
	}
	return out
}

// FlaggedLines lists the wrong-account commits among the last n.
func FlaggedLines(hist *History, n int, color bool) []string {
	return Texts(FlaggedRows(hist, n, color))
}

// FlaggedRows is FlaggedLines with the commit of each line.
func FlaggedRows(hist *History, n int, color bool) []Row {
	var rows []Row
	for _, cm := range hist.Commits {
		if cm.Flagged {
			state := "pushed"
			if cm.Unpushed {
				state = "not pushed"
			}
			rows = append(rows, Row{fmt.Sprintf("%s %s  %s  (%s)", paint(color, "33", cm.Short), cm.Subject, paint(color, "31", cm.Email), state), cm.Hash})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, Row{Text: fmt.Sprintf("No wrong-account commits in the last %d commits.", n)})
	}
	return rows
}

// GraphLines adds the account of each commit to git's own graph (`git log
// --graph` with each line's hash after a \x1f).
func GraphLines(c *account.Config, hist *History, graph string, color bool) []string {
	return Texts(GraphRows(c, hist, graph, color))
}

// GraphRows is GraphLines with the commit of each line.
func GraphRows(c *account.Config, hist *History, graph string, color bool) []Row {
	info := map[string]Commit{}
	for _, cm := range hist.Commits {
		info[cm.Hash] = cm
	}
	var rows []Row
	for _, line := range strings.Split(graph, "\n") {
		prefix, hash, ok := strings.Cut(line, "\x1f")
		if !ok {
			rows = append(rows, Row{Text: line})
			continue
		}
		hash = strings.TrimSpace(StripANSI(hash))
		cm := info[hash]
		label := cm.Account
		if label == "" {
			label = "unknown"
		}
		tag := paint(color, AccountColor(c, cm.Account), "["+label+"]")
		if cm.Flagged {
			tag = paint(color, "1;31", "["+label+" !]")
		}
		refs := ""
		if cm.Refs != "" {
			refs = " " + paint(color, "32", "("+cm.Refs+")")
		}
		tail := ""
		if cm.Flagged {
			tail += "  " + paint(color, "31", cm.Email+" WRONG ACCOUNT")
		}
		if cm.Unpushed {
			tail += "  " + paint(color, "2", "(not pushed)")
		}
		rows = append(rows, Row{fmt.Sprintf("%s%s%s %s %s%s", prefix, paint(color, "33", cm.Short), refs, tag, cm.Subject, tail), hash})
	}
	return rows
}

// Target is a commit picked for an action (revert, drop, roll back,
// cherry-pick).
type Target struct {
	Hash    string
	Short   string
	Subject string
	Parents []string
}

// IsMerge reports whether the commit joins two or more branches.
func (t Target) IsMerge() bool { return len(t.Parents) > 1 }

// Label is the short hash and subject, e.g. "1a2b3c4 Fix login".
func (t Target) Label() string { return t.Short + " " + t.Subject }

// Log reads commits from git and rewrites authors.
type Log interface {
	// Commits loads the latest commits of all branches in topological
	// order; none for a repo without commits.
	Commits(gitDir string, limit int, f Filter) ([]Commit, error)
	// Unpushed is the set of commits on a branch but on no remote.
	Unpushed(gitDir string) map[string]bool
	// Graph is git's own graph of the last n commits, each line's hash
	// after a \x1f. ErrNoCommits for a repo without commits.
	Graph(gitDir string, n int, color bool) (string, error)
	// LocalCommits lists the current branch's not-pushed commits, oldest first.
	LocalCommits(worktree string) ([]string, error)
	AuthorEmail(worktree, hash string) string
	// HeadAuthorName is the author name of the latest commit.
	HeadAuthorName(worktree string) string
	// RewriteAuthor sets name and email as the author of the not-pushed
	// commits from first on that used another email. Nothing changes on
	// failure.
	RewriteAuthor(worktree, name, email, first string) error

	// Resolve finds the commit a hash (or other revision) names.
	Resolve(worktree, rev string) (Target, error)
	// Branch is the checked-out branch; "" when HEAD is detached.
	Branch(worktree string) string
	// InProgress names an operation git is in the middle of ("rebase",
	// "merge", ...), or "".
	InProgress(gitDir string) string
	// OnBranch reports whether a commit is part of the current branch.
	OnBranch(worktree, hash string) bool
	// After lists the commits on the current branch made after hash.
	After(worktree, hash string) ([]string, error)
	// Revert adds a commit that undoes the commit's changes. Nothing
	// changes on failure.
	Revert(worktree string, t Target) error
	// Drop removes a commit from the current branch, replaying the commits
	// after it. Uncommitted changes are kept. Nothing changes on failure.
	Drop(worktree string, t Target) error
	// Reset moves the current branch back to the commit, keeping the
	// changes made after it as uncommitted changes.
	Reset(worktree, hash string) error
	// Order sorts commits that are not on the current branch oldest first:
	// parents before their children.
	Order(worktree string, hashes []string) ([]string, error)
	// CherryPick copies commits, in the given order, onto the current
	// branch. Nothing changes on failure.
	CherryPick(worktree string, ts []Target) error
}
