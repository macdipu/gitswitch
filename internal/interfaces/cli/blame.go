package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// repoPath turns a path typed in the current folder into one relative to
// the repo's top folder.
func (x *CLI) repoPath(gd, p string) string {
	return x.app.Workspace.RepoPath(gd, cwd(), p)
}

// cmdBlame is `gitswitch blame <path> [--rev <rev>] [-w] [-M] [--json]`.
func (x *CLI) cmdBlame(args []string) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	rev := flagValue(args, "--rev")
	var path string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--rev":
			i++
		case strings.HasPrefix(a, "--rev="), a == "--json", a == "-w", a == "-M":
		case path == "":
			path = a
		}
	}
	if path == "" {
		return fmt.Errorf("usage: gitswitch blame <path> [--rev <rev>] [-w] [-M] [--json]")
	}
	b, err := x.app.History.Blame(gd, rev, x.repoPath(gd, path), history.BlameOptions{IgnoreWhitespace: hasFlag(args, "-w"), Moves: hasFlag(args, "-M")})
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(x.out).Encode(b)
	}
	authorW := 0
	for _, l := range b.Lines {
		authorW = max(authorW, len([]rune(history.CleanText(l.Author))))
	}
	authorW = min(authorW, 20)
	numW := len(fmt.Sprint(len(b.Lines)))
	prev := ""
	for _, l := range b.Lines {
		short, author, date := l.Short, history.CleanText(l.Author), time.Unix(l.Time, 0).Format("2006-01-02")
		if l.Hash == prev {
			short, author, date = strings.Repeat(" ", len(short)), "", ""
		}
		prev = l.Hash
		if r := []rune(author); len(r) > authorW {
			author = string(r[:authorW])
		}
		mark := " "
		if l.Boundary && strings.TrimSpace(short) != "" {
			mark = "^"
		}
		x.sayf("%s%s %-*s %-10s %*d  %s\n", mark, short, authorW, author, date, numW, l.Line, history.CleanText(l.Text))
	}
	if b.Truncated {
		x.sayf("\n(showing the first %d lines of %s)\n", history.MaxBlameLines, b.Path)
	}
	return nil
}

// cmdFileLog is `gitswitch log --file <path>`: the commits that changed a
// file, following renames.
func (x *CLI) cmdFileLog(gd, path string, n int, asJSON bool) error {
	fh, err := x.app.History.FileHistory(gd, x.repoPath(gd, path), n, 0)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(fh)
	}
	if len(fh.Commits) == 0 {
		x.say("No commit changed", fh.Path)
		return nil
	}
	x.sayf("History of %s (follows renames)\n", fh.Path)
	for _, c := range fh.Commits {
		label := c.Account
		if label == "" {
			label = "unknown"
		}
		st := c.Status
		if c.Status == "R" {
			st = fmt.Sprintf("R%d", c.Similarity)
		}
		x.sayf("%s %s %-4s [%s] %s  %s\n", c.Short, time.Unix(c.Time, 0).Format("2006-01-02"), st, label, history.CleanText(c.Subject), history.CleanText(c.Author))
		if c.Status == "R" {
			x.sayf("        renamed from %s to %s\n", history.CleanText(c.OldPath), history.CleanText(c.Path))
		}
	}
	if fh.More {
		x.sayf("\n(latest %d commits; see more with: gitswitch log --file %s -n %d)\n", n, path, n*2)
	}
	return nil
}
