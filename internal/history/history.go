// Package history builds the commit graph with the account behind each
// commit, flags wrong-account commits, and fixes them before they are pushed.
package history

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"gitswitch/internal/account"
	"gitswitch/internal/gitcli"
	"gitswitch/internal/repo"
	"gitswitch/internal/sys"
)

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

type History struct {
	Repo     repo.Info `json:"repo"`
	Commits  []Commit  `json:"commits"`
	Lanes    int       `json:"lanes"`
	Flagged  int       `json:"flagged"`
	Unpushed int       `json:"unpushed"`
}

func loadCommits(git *gitcli.Runner, gitDir string, limit int) ([]Commit, error) {
	out, err := git.Run("--git-dir="+gitDir, "log", "--all", "--topo-order", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%P%x1f%ae%x1f%an%x1f%at%x1f%D%x1f%s%x1e")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var cs []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		t, _ := strconv.ParseInt(f[4], 10, 64)
		c := Commit{Hash: f[0], Email: f[2], Author: f[3], Time: t, Refs: f[5], Subject: f[6]}
		if len(c.Hash) >= 7 {
			c.Short = c.Hash[:7]
		}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// layoutLanes assigns each commit a column and the line segments to the next row.
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

func unpushedSet(git *gitcli.Runner, gitDir string) map[string]bool {
	set := map[string]bool{}
	out, err := git.Run("--git-dir="+gitDir, "rev-list", "--branches", "--not", "--remotes")
	if err != nil {
		return set
	}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

// Build loads the latest commits with lanes, accounts and flags.
func Build(git *gitcli.Runner, c *account.Config, gitDir string, limit int) (*History, error) {
	cs, err := loadCommits(git, gitDir, limit)
	if err != nil {
		return nil, err
	}
	h := &History{Repo: repo.InfoFor(git, c, gitDir)}
	h.Lanes = LayoutLanes(cs)
	up := unpushedSet(git, gitDir)
	repoAcct := c.Account(h.Repo.Account)
	for i := range cs {
		if a := c.AccountByEmail(cs[i].Email); a != nil {
			cs[i].Account = a.ID
		}
		cs[i].Unpushed = up[cs[i].Hash]
		if repoAcct != nil && IsWrongAccount(c, repoAcct, cs[i].Email, cs[i].Unpushed) {
			cs[i].Flagged = true
			h.Flagged++
		}
		if cs[i].Unpushed {
			h.Unpushed++
		}
	}
	h.Commits = cs
	return h, nil
}

// isWrongAccount: the commit used one of your other accounts, or an unknown
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

// historyLines renders git's own graph with the account of each commit.
// Used by `gitswitch log` and the TUI.
// Lines renders git's own graph with the account of each commit
// (used by `gitswitch log` and the terminal app).
func Lines(git *gitcli.Runner, c *account.Config, gitDir string, n int, flaggedOnly, color bool) ([]string, *History, error) {
	hist, err := Build(git, c, gitDir, n)
	if err != nil {
		return nil, nil, err
	}
	info := map[string]Commit{}
	for _, cm := range hist.Commits {
		info[cm.Hash] = cm
	}
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	var lines []string
	if flaggedOnly {
		for _, cm := range hist.Commits {
			if cm.Flagged {
				state := "pushed"
				if cm.Unpushed {
					state = "not pushed"
				}
				lines = append(lines, fmt.Sprintf("%s %s  %s  (%s)", paint("33", cm.Short), cm.Subject, paint("31", cm.Email), state))
			}
		}
		if len(lines) == 0 {
			lines = append(lines, fmt.Sprintf("No wrong-account commits in the last %d commits.", n))
		}
		return lines, hist, nil
	}
	gargs := []string{"--git-dir=" + gitDir, "log", "--graph", "--all", "-n", strconv.Itoa(n), "--format=%x1f%H"}
	if color {
		gargs = append(gargs, "--color=always")
	}
	out, err := git.Run(gargs...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return []string{"This repo has no commits yet."}, hist, nil
		}
		return nil, nil, err
	}
	for _, line := range strings.Split(out, "\n") {
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
		tag := paint(AccountColor(c, cm.Account), "["+label+"]")
		if cm.Flagged {
			tag = paint("1;31", "["+label+" !]")
		}
		refs := ""
		if cm.Refs != "" {
			refs = " " + paint("32", "("+cm.Refs+")")
		}
		tail := ""
		if cm.Flagged {
			tail += "  " + paint("31", cm.Email+" WRONG ACCOUNT")
		}
		if cm.Unpushed {
			tail += "  " + paint("2", "(not pushed)")
		}
		lines = append(lines, fmt.Sprintf("%s%s%s %s %s%s", graph, paint("33", cm.Short), refs, tag, cm.Subject, tail))
	}
	return lines, hist, nil
}

// acctColor gives each account the same colour in the TUI and log.
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

// fixAuthor rewrites the author of not-pushed commits on the current branch
// that used another account's email. Pushed commits are never touched.
// FixAuthor rewrites the author of not-pushed commits on the current branch
// that used another account's email. Pushed commits are never touched.
func FixAuthor(git *gitcli.Runner, fs *sys.FS, c *account.Config, gitDir string) (int, error) {
	a, _ := repo.AccountFor(git, c, gitDir)
	if a == nil || a.Email == "" {
		return 0, fmt.Errorf("this repo has no account yet; run `gitswitch use <account>` first")
	}
	wt := git.WorktreeOf(gitDir)
	out, err := git.Run("-C", wt, "rev-list", "--reverse", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	local := strings.Fields(out)
	if len(local) == 0 {
		return 0, nil
	}
	bad := 0
	for _, h := range local {
		e, _ := git.Run("-C", wt, "log", "-1", "--format=%ae", h)
		if IsWrongAccount(c, a, e, true) {
			bad++
		}
	}
	if bad == 0 {
		return 0, nil
	}
	if fs.Dry(fmt.Sprintf("rewrite the author of %d not-pushed commit(s) in %s to %s", bad, wt, a.Email)) {
		return bad, nil
	}
	name := a.Name
	if name == "" {
		name, _ = git.Run("-C", wt, "log", "-1", "--format=%an", "HEAD")
	}
	safe := strings.NewReplacer(`"`, "", "`", "", "$", "", `\`, "").Replace
	author := safe(name) + " <" + safe(a.Email) + ">"
	execLine := `if [ "$(git log -1 --format=%ae)" != "` + safe(a.Email) + `" ]; then git commit --amend --allow-empty --no-edit --no-verify --author="` + author + `"; fi`
	args := []string{"-C", wt, "-c", "user.email=" + a.Email, "-c", "user.name=" + name,
		"rebase", "--rebase-merges", "--keep-empty", "--autostash", "--exec", execLine}
	first := local[0]
	if p, err := git.Run("-C", wt, "rev-parse", "--verify", "-q", first+"^"); err == nil && p != "" {
		args = append(args, p)
	} else {
		args = append(args, "--root")
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
	sys.HideWindow(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		_, _ = git.Run("-C", wt, "rebase", "--abort")
		return 0, fmt.Errorf("rewrite failed, nothing changed: %s", strings.TrimSpace(string(b)))
	}
	return bad, nil
}
