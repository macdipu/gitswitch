// Package domain is the commit history as the apps show it: the graph
// layout, the account behind each commit and the rule that flags
// wrong-account commits. It is pure: the git log is read by the
// infrastructure package. Import it as history.
package domain

import (
	"errors"
	"fmt"
	"strings"

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

// FlaggedLines lists the wrong-account commits among the last n.
func FlaggedLines(hist *History, n int, color bool) []string {
	var lines []string
	for _, cm := range hist.Commits {
		if cm.Flagged {
			state := "pushed"
			if cm.Unpushed {
				state = "not pushed"
			}
			lines = append(lines, fmt.Sprintf("%s %s  %s  (%s)", paint(color, "33", cm.Short), cm.Subject, paint(color, "31", cm.Email), state))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("No wrong-account commits in the last %d commits.", n))
	}
	return lines
}

// GraphLines adds the account of each commit to git's own graph (`git log
// --graph` with each line's hash after a \x1f).
func GraphLines(c *account.Config, hist *History, graph string, color bool) []string {
	info := map[string]Commit{}
	for _, cm := range hist.Commits {
		info[cm.Hash] = cm
	}
	var lines []string
	for _, line := range strings.Split(graph, "\n") {
		graph, hash, ok := strings.Cut(line, "\x1f")
		if !ok {
			lines = append(lines, line)
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
		lines = append(lines, fmt.Sprintf("%s%s%s %s %s%s", graph, paint(color, "33", cm.Short), refs, tag, cm.Subject, tail))
	}
	return lines
}

// Log reads commits from git and rewrites authors.
type Log interface {
	// Commits loads the latest commits of all branches in topological
	// order; none for a repo without commits.
	Commits(gitDir string, limit int) ([]Commit, error)
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
}
