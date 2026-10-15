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
	Refs     string   `json:"refs"` // the branches and tags here, as git log shows them
	Labels   []Label  `json:"labels"`
	Merge    bool     `json:"merge"`  // more than one parent
	OnHead   bool     `json:"onHead"` // part of what is checked out
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
	// More is true when git had more commits than were asked for: the
	// front ends offer to load more.
	More bool `json:"more"`
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

// AccountCheck says whether a repo's commits show the account that pushes
// them: the repo's own email, and the not-pushed commits on its branch.
type AccountCheck struct {
	Account string `json:"account"` // the account that pushes; "" when git will ask
	Email   string `json:"email"`   // the repo's user.email
	// AccountEmail is the account's email, which new commits should use.
	AccountEmail string `json:"accountEmail"`
	// EmailBad: the repo's user.email is not the account's, so new commits
	// show another person.
	EmailBad bool `json:"emailBad"`
	// Wrong counts the not-pushed commits on the current branch made with
	// another account's email (or an unknown one); WrongEmails lists those
	// emails.
	Wrong       int      `json:"wrong"`
	WrongEmails []string `json:"wrongEmails"`
}

// Describe explains the problems in plain words; "" when there are none.
func (c AccountCheck) Describe() string {
	var parts []string
	if c.Wrong > 0 {
		parts = append(parts, fmt.Sprintf("%d not-pushed commit(s) on this branch were made as %s, but %s will push them", c.Wrong, strings.Join(c.WrongEmails, ", "), c.Account))
	}
	if c.EmailBad {
		parts = append(parts, fmt.Sprintf("new commits here are made as %s, not as %s (%s)", c.Email, c.Account, c.AccountEmail))
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, "; ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
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

// CleanText makes text read from git safe to print on a terminal: it drops
// control characters (C0, DEL and C1), so a commit message, author or ref
// cannot move the cursor, change colours or send escape sequences. A tab
// becomes a space. Colour is added after cleaning.
func CleanText(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteByte(' ')
		case !isControl(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isControl(r rune) bool { return r < 0x20 || r >= 0x7f && r <= 0x9f }

// ErrNoCommits is returned for a repo that has no commits yet.
var ErrNoCommits = errors.New("this repo has no commits yet")

// ErrStashKept marks a rewrite (drop, fix-author) that was done, but whose
// uncommitted changes could not be put back cleanly: they are kept in the
// stash.
var ErrStashKept = errors.New("uncommitted changes kept in the stash")

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
	// Ref shows only the commits of one branch or tag ("" for every one).
	// That still draws the graph: a branch's history is whole.
	Ref string `json:"ref"`
	// Path shows only the commits that changed a file or folder.
	Path string `json:"path"`
	// How to list: Order "date" puts commits by date (still children
	// before parents), else topologically. FirstParent follows only the
	// first parent of merges: a branch's own story. NoMerges leaves merge
	// commits out.
	Order       string `json:"order"`
	FirstParent bool   `json:"firstParent"`
	NoMerges    bool   `json:"noMerges"`
}

// Active reports whether the filter narrows anything.
func (f Filter) Active() bool { return f != Filter{} }

// Narrows reports whether the filter hides commits of the history it
// shows, so graph lines would not connect: everything but the branch and
// how to list (first-parent history is whole too).
func (f Filter) Narrows() bool {
	f.Ref, f.Order, f.FirstParent = "", "", false
	return f.Active()
}

// Diff is one file's changes in one commit.
type Diff struct {
	Path      string `json:"path"`
	Text      string `json:"text"`      // a unified diff
	Truncated bool   `json:"truncated"` // cut at MaxDiffBytes
}

// MaxDiffBytes is how much of a file's diff is shown.
const MaxDiffBytes = 256 << 10

// CheckRef refuses a branch or tag name git would read as an option.
func CheckRef(ref string) error {
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\n") {
		return fmt.Errorf("%q is not a branch or tag name", ref)
	}
	return nil
}

// Label is a branch or tag on a commit.
type Label struct {
	Name string `json:"name"` // main, origin/main, v1.2.0
	Kind string `json:"kind"` // head (checked out), local, remote, tag, detached (HEAD with no branch)
}

// ParseLabels reads git log's %D with --decorate=full, e.g.
// "HEAD -> refs/heads/main, refs/remotes/origin/main, tag: refs/tags/v1".
// refs/remotes/origin/HEAD is left out: it only names the default branch.
func ParseLabels(d string) []Label {
	var out []Label
	for _, part := range strings.Split(d, ", ") {
		part = strings.TrimSpace(part)
		kind := ""
		if rest, ok := strings.CutPrefix(part, "HEAD -> "); ok {
			part, kind = rest, "head"
		}
		switch {
		case part == "":
		case part == "HEAD":
			out = append(out, Label{Name: "HEAD", Kind: "detached"})
		case strings.HasPrefix(part, "tag: refs/tags/"):
			out = append(out, Label{Name: strings.TrimPrefix(part, "tag: refs/tags/"), Kind: "tag"})
		case strings.HasPrefix(part, "refs/heads/"):
			if kind == "" {
				kind = "local"
			}
			out = append(out, Label{Name: strings.TrimPrefix(part, "refs/heads/"), Kind: kind})
		case strings.HasPrefix(part, "refs/remotes/"):
			if n := strings.TrimPrefix(part, "refs/remotes/"); !strings.HasSuffix(n, "/HEAD") {
				out = append(out, Label{Name: n, Kind: "remote"})
			}
		}
	}
	return out
}

// RefsText writes labels the way git log shows them by default, e.g.
// "HEAD -> main, origin/main, tag: v1".
func RefsText(ls []Label) string {
	var parts []string
	for _, l := range ls {
		switch l.Kind {
		case "head":
			parts = append(parts, "HEAD -> "+l.Name)
		case "tag":
			parts = append(parts, "tag: "+l.Name)
		default:
			parts = append(parts, l.Name)
		}
	}
	return strings.Join(parts, ", ")
}

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
// endOfDay its last, so both ends of a range are included; likewise a time
// without seconds means the whole minute.
func ParseDate(s string, now time.Time, endOfDay bool) (time.Time, error) {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	day := func(t time.Time) time.Time {
		y, m, d := t.Date()
		if endOfDay {
			// One second before the next midnight: a day is not always 24
			// hours long (daylight saving time).
			return time.Date(y, m, d+1, 0, 0, 0, 0, now.Location()).Add(-time.Second)
		}
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
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
			return monthsBefore(now, n), nil
		case "year":
			return monthsBefore(now, 12*n), nil
		}
		return t, bad
	}
	// s is lower case, so the T between date and time is too.
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02t15:04:05", "2006-01-02t15:04"} {
		if v, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			t = v
			if endOfDay && !strings.HasSuffix(layout, ":05") {
				t = t.Add(59 * time.Second)
			}
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

// monthsBefore goes back n months, staying in the target month: a month
// before 31 March is 28 (or 29) February, not 3 March.
func monthsBefore(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m-time.Month(n), 1, 0, 0, 0, 0, t.Location())
	if last := first.AddDate(0, 1, -1).Day(); d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
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
		rows = append(rows, Row{fmt.Sprintf("%s %s %s %s  %s%s", paint(color, "33", cm.Short), date, tag, CleanText(cm.Subject), paint(color, "2", CleanText(cm.Author)), tail), cm.Hash})
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
			rows = append(rows, Row{fmt.Sprintf("%s %s  %s  (%s)", paint(color, "33", cm.Short), CleanText(cm.Subject), paint(color, "31", CleanText(cm.Email)), state), cm.Hash})
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
			refs = " " + paint(color, "32", "("+CleanText(cm.Refs)+")")
		}
		tail := ""
		if cm.Flagged {
			tail += "  " + paint(color, "31", CleanText(cm.Email)+" WRONG ACCOUNT")
		}
		if cm.Unpushed {
			tail += "  " + paint(color, "2", "(not pushed)")
		}
		rows = append(rows, Row{fmt.Sprintf("%s%s%s %s %s%s", prefix, paint(color, "33", cm.Short), refs, tag, CleanText(cm.Subject), tail), hash})
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

// FileChange is one file a commit changed, with its line counts. Binary
// files have no line counts.
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Binary  bool   `json:"binary,omitempty"`
}

// MaxDetailFiles is how many changed files a commit's details list.
const MaxDetailFiles = 500

// Detail is what a commit's details show beyond its line in the list: the
// whole message and the files it changed. A merge is compared with its
// first parent, which is what the merge brought into the branch.
type Detail struct {
	Hash    string       `json:"hash"`
	Message string       `json:"message"`
	Files   []FileChange `json:"files"`
	More    int          `json:"more"` // changed files left out after MaxDetailFiles
	Added   int          `json:"added"`
	Deleted int          `json:"deleted"`
}

// ParseNumstat reads `git log --numstat` lines ("added<TAB>deleted<TAB>path",
// "-" counts for binary files) into d, keeping MaxDetailFiles files.
func ParseNumstat(d *Detail, out string) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(f) != 3 || f[2] == "" {
			continue
		}
		fc := FileChange{Path: f[2], Binary: f[0] == "-" && f[1] == "-"}
		fc.Added, _ = strconv.Atoi(f[0])
		fc.Deleted, _ = strconv.Atoi(f[1])
		d.Added += fc.Added
		d.Deleted += fc.Deleted
		if len(d.Files) >= MaxDetailFiles {
			d.More++
			continue
		}
		d.Files = append(d.Files, fc)
	}
	if d.Files == nil {
		d.Files = []FileChange{}
	}
}

// Log reads commits from git and rewrites authors.
type Log interface {
	// Show reads a commit's full message and changed files.
	Show(gitDir, rev string) (Detail, error)
	// FileDiff is one file's diff in a commit (against its first parent).
	FileDiff(gitDir, rev, path string) (Diff, error)
	// OnHead is the set of commits of HEAD's history made at or after
	// since (Unix seconds).
	OnHead(gitDir string, since int64) map[string]bool
	// Commits loads the latest commits of all branches in topological
	// order; none for a repo without commits.
	Commits(gitDir string, limit int, f Filter) ([]Commit, error)
	// Unpushed is the set of commits on a branch but on no remote.
	Unpushed(gitDir string) map[string]bool
	// OnRemote reports whether a commit is on a remote branch.
	OnRemote(gitDir, hash string) bool
	// Graph is git's own graph of the last n commits, each line's hash
	// after a \x1f. ErrNoCommits for a repo without commits.
	Graph(gitDir string, n int, color bool) (string, error)
	// LocalCommits lists the current branch's not-pushed commits, oldest first.
	LocalCommits(worktree string) ([]string, error)
	AuthorEmail(worktree, hash string) string
	// HeadAuthorName is the author name of the latest commit.
	HeadAuthorName(worktree string) string
	// Rewritten lists the commits a rewrite starting at first replays:
	// every commit on the current branch after first's parent.
	Rewritten(worktree, first string) ([]string, error)
	// RewriteAuthor sets name and email as the author of the commits from
	// first on whose author email is one of wrong; all others keep their
	// author. Nothing changes on failure.
	RewriteAuthor(worktree, name, email, first string, wrong []string) error

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
