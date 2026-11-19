// Package domain describes rewriting the history of the current branch:
// the commits that may be rewritten (Todo), the steps asked for (reorder,
// squash, fixup, reword, drop), their validation, and the plan that turns
// them into the lines of a git rebase todo. It also declares what those
// use cases need from git (ports). It is pure. Import it as rewrite.
package domain

import (
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// Action is what a step does with its commit.
type Action string

const (
	Pick   Action = "pick"   // keep the commit as it is
	Reword Action = "reword" // keep it with a new message
	Squash Action = "squash" // fold it into the commit before, joining the messages
	Fixup  Action = "fixup"  // fold it into the commit before, dropping its message
	Drop   Action = "drop"   // remove it
)

// ParseAction reads an action as the API, the command line and git's todo
// name it (the one-letter forms too).
func ParseAction(s string) (Action, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pick", "p":
		return Pick, nil
	case "reword", "r":
		return Reword, nil
	case "squash", "s":
		return Squash, nil
	case "fixup", "f":
		return Fixup, nil
	case "drop", "d":
		return Drop, nil
	case "edit", "e", "exec", "x", "break", "b", "label", "reset", "merge":
		return "", fmt.Errorf("%q is not supported here: use pick, reword, squash, fixup or drop (git rebase -i in a terminal does the rest)", s)
	}
	return "", fmt.Errorf("unknown action %q: use pick, reword, squash, fixup or drop", s)
}

// Commit is a commit that may be rewritten.
type Commit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Subject string   `json:"subject"`
	Message string   `json:"message"` // the whole message
	Author  string   `json:"author"`
	Email   string   `json:"email"`
	Time    int64    `json:"time"` // author time, Unix seconds
	Files   int      `json:"files"`
	Merge   bool     `json:"merge"`
	Parents []string `json:"parents"`
}

// Step is one line of the new history: the commit, what to do with it and,
// for a reword (and optionally a squash), the new message. The order of the
// steps is the new order of the commits, oldest first.
type Step struct {
	Hash    string `json:"hash"`
	Action  Action `json:"action"`
	Message string `json:"message"`
}

// Todo is what can be rewritten now.
type Todo struct {
	// Base is the commit the rewritten ones sit on (the parent of the
	// oldest listed); "" when there is nothing to rewrite.
	Base   string `json:"base"`
	Branch string `json:"branch"`
	// Commits are the rewritable commits, oldest first.
	Commits []Commit `json:"commits"`
	// Blocked says why a rewrite can't start now; "" when it can.
	Blocked string `json:"blocked"`
	// Note says what was left out of the default list and why.
	Note string `json:"note"`
	// Pending says a rewrite that stopped on clashes is still open.
	Pending bool `json:"pending"`
}

// Eligible keeps from the not-pushed commits (oldest first) the run that
// can be rewritten: git would flatten a merge commit and the first commit
// of a repo has nothing to rebase onto, so the list starts after the last
// of them. The note says what was left out.
func Eligible(all []Commit) ([]Commit, string) {
	cut := -1
	for i, c := range all {
		if c.Merge || len(c.Parents) == 0 {
			cut = i
		}
	}
	if cut < 0 {
		return all, ""
	}
	c := all[cut]
	what := "the first commit of the repo"
	if c.Merge {
		what = "merge commit " + c.Short
	}
	return all[cut+1:], fmt.Sprintf("Commits up to %s are left out: it can't be rewritten (git would flatten a merge, and the first commit has nothing before it). Use --since to pick where to start.", what)
}

// CheckSince refuses a range that cannot be rewritten as asked: a merge
// commit or the first commit of the repo in it.
func CheckSince(commits []Commit) error {
	for _, c := range commits {
		switch {
		case len(c.Parents) == 0:
			return fmt.Errorf("%s is the first commit of the repo, so it can't be rewritten (there is nothing before it); start from the next commit", c.Short)
		case c.Merge:
			return fmt.Errorf("%s is a merge commit, and rewriting it would flatten the merge; start after it", c.Short)
		}
	}
	return nil
}

// Pushed is the refusal for a pushed commit.
func Pushed(c Commit) error {
	return fmt.Errorf("%s is already pushed, so rewriting it would rewrite pushed history; revert it instead, or start after it", c.Short)
}

// Check refuses to rewrite when the repo is not ready: git is in the
// middle of something, HEAD is detached, there are no commits, or there
// are uncommitted changes to tracked files (a rewrite could mix them with
// its own, and undoing it could lose them).
func Check(h refs.Head) error {
	if err := checkState(h); err != nil {
		return err
	}
	if c := h.Changes; c.Staged+c.Changed > 0 {
		return fmt.Errorf("you have uncommitted changes (%d staged, %d changed); commit, park or discard them first, then rewrite", c.Staged, c.Changed)
	}
	return nil
}

func checkState(h refs.Head) error {
	c := h.Changes
	switch {
	case h.Busy != "":
		return remote.Busy(h.Busy)
	case h.Hash == "":
		return fmt.Errorf("this repo has no commits yet, so there is nothing to rewrite")
	case h.Branch == "":
		return fmt.Errorf("HEAD is detached (not on a branch); switch to the branch to rewrite first")
	case c.Conflicts > 0:
		return fmt.Errorf("%d file(s) still have conflicts; fix them first", c.Conflicts)
	}
	return nil
}

// CheckFold is Check for folding the staged changes into an older commit:
// something must be staged, and nothing else tracked may be changed (the
// rebase would have to move it out of the way).
func CheckFold(h refs.Head) error {
	if err := checkState(h); err != nil {
		return err
	}
	switch c := h.Changes; {
	case c.Changed > 0:
		return fmt.Errorf("%d tracked file(s) have changes that are not staged; stage them too, or commit, park or discard them, so the rewrite does not have to move them", c.Changed)
	case c.Staged == 0:
		return fmt.Errorf("nothing is staged: stage the changes to fold into the older commit first")
	}
	return nil
}

// Tidy tidies a commit message like the commit box does: surrounding blank
// space goes and a message with no text is "". NUL bytes are removed.
func Tidy(m string) string {
	return workspace.Message(strings.ReplaceAll(m, "\x00", ""))
}

// Resolve turns each step's hash (a full hash or a unique prefix of at
// least 4 characters) into the commit's full hash and its action into a
// known one.
func Resolve(commits []Commit, steps []Step) ([]Step, error) {
	out := make([]Step, len(steps))
	for i, s := range steps {
		h := strings.ToLower(strings.TrimSpace(s.Hash))
		var found []string
		if len(h) >= 4 {
			for _, c := range commits {
				if strings.HasPrefix(c.Hash, h) {
					found = append(found, c.Hash)
				}
			}
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("step %d: %q is not one of the commits to rewrite", i+1, s.Hash)
		case 1:
		default:
			return nil, fmt.Errorf("step %d: %q matches more than one commit; give more of the hash", i+1, s.Hash)
		}
		a := s.Action
		if a == "" {
			a = Pick
		}
		if _, err := ParseAction(string(a)); err != nil {
			return nil, fmt.Errorf("step %d: %v", i+1, err)
		}
		a, _ = ParseAction(string(a))
		out[i] = Step{Hash: found[0], Action: a, Message: s.Message}
	}
	return out, nil
}

// Line is one line of the todo git runs: a pick, fixup or drop of a
// commit. A message set with Msg is written by a later `git commit --amend`
// the infrastructure adds after the line, with Pre naming the same message
// on the first line of its group, where HEAD is remembered so the amend is
// skipped if the group made no commit.
type Line struct {
	Action Action `json:"action"` // Pick, Fixup or Drop
	Hash   string `json:"hash"`
	Pre    int    `json:"pre"` // >= 0: remember HEAD before this line, for message Pre
	Msg    int    `json:"msg"` // >= 0: after this line, set message number Msg
}

// Result is a commit of the new history, as the plan shows it.
type Result struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
	From    []Step `json:"from"` // the steps that make it: the first is a pick or reword
}

// Plan is a validated rewrite.
type Plan struct {
	Lines    []Line   // the todo
	Messages []string // the messages the lines point at
	Result   []Result // the new history, oldest first
	Before   int      // commits before
	After    int      // commits after
	Dropped  int
	Squashed int // squashed and fixed up commits folded into others
	Reworded int
	Moved    bool // the order changed
	// NoChange: the rewrite would change nothing.
	NoChange bool
}

// Validate checks that steps are a rewrite of exactly commits: the same
// commits, none missing, extra or twice, a first kept commit that is not a
// squash or fixup, and a message for each reword.
func Validate(commits []Commit, steps []Step) error {
	_, err := Build(commits, steps)
	return err
}

// Build checks steps and turns them into a Plan. Steps need full hashes
// (see Resolve). Each group is a kept commit (pick or reword) with the
// squash and fixup steps after it (drops may sit between). A group's
// message is the last message given on one of its squash steps; with none, a group with a squash gets the
// joined messages of its commits (the reworded message for the first, a
// fixup adds none), and one without keeps its first commit's (or its
// reworded message).
func Build(commits []Commit, steps []Step) (Plan, error) {
	var p Plan
	p.Before = len(commits)
	byHash := map[string]Commit{}
	for _, c := range commits {
		byHash[c.Hash] = c
	}
	seen := map[string]bool{}
	for i, s := range steps {
		if _, ok := byHash[s.Hash]; !ok {
			return p, fmt.Errorf("step %d: %q is not one of the commits to rewrite", i+1, s.Hash)
		}
		if seen[s.Hash] {
			return p, fmt.Errorf("%s appears twice: each commit must appear once", byHash[s.Hash].Short)
		}
		seen[s.Hash] = true
		if _, err := ParseAction(string(s.Action)); err != nil {
			return p, fmt.Errorf("step %d: %v", i+1, err)
		}
	}
	for _, c := range commits {
		if !seen[c.Hash] {
			return p, fmt.Errorf("%s is missing: every commit must have a step (use drop to remove one)", c.Short)
		}
	}

	type group struct {
		head    int   // index of the kept step
		members []int // steps folded in (squash, fixup)
		last    int   // index of the group's last line in Lines
		first   int   // index of its first line
	}
	var groups []*group
	var cur *group
	for i, s := range steps {
		c := byHash[s.Hash]
		switch s.Action {
		case Drop:
			p.Dropped++
			p.Lines = append(p.Lines, Line{Action: Drop, Hash: s.Hash, Pre: -1, Msg: -1})
		case Pick, Reword:
			if s.Action == Reword && Tidy(s.Message) == "" {
				return p, fmt.Errorf("reword %s needs a new message", c.Short)
			}
			cur = &group{head: i, first: len(p.Lines), last: len(p.Lines)}
			groups = append(groups, cur)
			p.Lines = append(p.Lines, Line{Action: Pick, Hash: s.Hash, Pre: -1, Msg: -1})
		case Squash, Fixup:
			if cur == nil {
				return p, fmt.Errorf("%s is a %s but nothing before it is kept: it needs a commit to fold into", c.Short, s.Action)
			}
			cur.members = append(cur.members, i)
			cur.last = len(p.Lines)
			p.Lines = append(p.Lines, Line{Action: Fixup, Hash: s.Hash, Pre: -1, Msg: -1})
			p.Squashed++
		}
	}

	for _, g := range groups {
		head := steps[g.head]
		hc := byHash[head.Hash]
		msg, explicit := "", false
		headMsg := hc.Message
		if head.Action == Reword {
			headMsg = Tidy(head.Message)
			msg, explicit = headMsg, true
			p.Reworded++
		}
		parts := []string{Tidy(headMsg)}
		hasSquash, squashMsg := false, false
		for _, mi := range g.members {
			m := steps[mi]
			if m.Action == Squash {
				hasSquash = true
				parts = append(parts, Tidy(byHash[m.Hash].Message))
				if t := Tidy(m.Message); t != "" {
					msg, explicit, squashMsg = t, true, true
				}
			}
		}
		if !squashMsg && hasSquash {
			var keep []string
			for _, x := range parts {
				if x != "" {
					keep = append(keep, x)
				}
			}
			msg, explicit = strings.Join(keep, "\n\n"), true
		}
		final := Tidy(hc.Message)
		if explicit {
			final = msg
		}
		// Only a message that differs from the kept commit's own needs an amend.
		if explicit && (len(g.members) > 0 || msg != Tidy(hc.Message)) {
			idx := len(p.Messages)
			p.Messages = append(p.Messages, msg)
			p.Lines[g.first].Pre = idx
			p.Lines[g.last].Msg = idx
		} else if head.Action == Reword {
			p.Reworded-- // reworded to what it was: a plain pick
		}
		from := []Step{head}
		for _, mi := range g.members {
			from = append(from, steps[mi])
		}
		sub, _, _ := strings.Cut(final, "\n")
		p.Result = append(p.Result, Result{Subject: sub, Message: final, From: from})
	}
	p.After = len(p.Result)

	// The kept commits in the order of commits, to see whether it moved.
	idx := map[string]int{}
	for i, c := range commits {
		idx[c.Hash] = i
	}
	last := -1
	for _, s := range steps {
		if s.Action == Drop {
			continue
		}
		if idx[s.Hash] < last {
			p.Moved = true
		}
		last = idx[s.Hash]
	}
	p.NoChange = !p.Moved && p.Dropped == 0 && p.Squashed == 0 && p.Reworded == 0 && len(p.Messages) == 0
	return p, nil
}

// Summary says in plain words what a plan does, e.g. "5 commits become 3:
// 1 reworded, 2 squashed, 1 dropped, reordered".
func (p Plan) Summary() string {
	if p.NoChange {
		return "Nothing would change."
	}
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(p.Reworded, "reworded")
	add(p.Squashed, "folded into another")
	add(p.Dropped, "dropped")
	if p.Moved {
		parts = append(parts, "reordered")
	}
	s := fmt.Sprintf("%s become %s", plural(p.Before, "commit"), plural(p.After, "commit"))
	if p.Before == p.After {
		s = plural(p.Before, "commit") + " rewritten"
	}
	if len(parts) > 0 {
		s += ": " + strings.Join(parts, ", ")
	}
	return s
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return fmt.Sprintf("%d %ss", n, w)
}

// Label is what the undo list calls a finished rewrite.
func Label(branch string, p Plan) string {
	return fmt.Sprintf("Rewrote %s: %s", branch, p.Summary())
}

// Outcome is how a rewrite ended.
type Outcome string

const (
	Done    Outcome = "done"      // the history was rewritten
	Stopped Outcome = "conflicts" // git stopped with files to fix
)

// Rewritten is the result of a rewrite.
type Rewritten struct {
	Outcome Outcome `json:"outcome"`
	Hash    string  `json:"hash"`  // HEAD afterwards
	Short   string  `json:"short"` // empty while stopped
	Before  int     `json:"before"`
	After   int     `json:"after"`
	// Conflicts lists the files to fix when it stopped.
	Conflicts []string `json:"conflicts"`
	// Summary lists the new history, oldest first, one line per commit.
	Summary []string `json:"summary"`
	// Message says what happened and what to do next.
	Message string `json:"message"`
}

// SummaryLines lists a plan's new history as short lines.
func (p Plan) SummaryLines(byHash map[string]Commit) []string {
	var out []string
	for _, r := range p.Result {
		line := r.Subject
		var folded []string
		for _, f := range r.From[1:] {
			folded = append(folded, string(f.Action)+" "+shortOf(byHash, f.Hash))
		}
		head := r.From[0]
		line = shortOf(byHash, head.Hash) + " " + line
		if head.Action == Reword {
			line += "  (reworded)"
		}
		if len(folded) > 0 {
			line += "  (" + strings.Join(folded, ", ") + ")"
		}
		out = append(out, line)
	}
	return out
}

func shortOf(by map[string]Commit, h string) string {
	if c, ok := by[h]; ok {
		return c.Short
	}
	return h[:min(7, len(h))]
}

// Pending is a rewrite that stopped on clashes and is not finished yet:
// the repo keeps it so that, once the stopped rebase has been continued,
// skipped or aborted, the result can be recorded for undo.
type Pending struct {
	Head     string      // the branch's tip before the rewrite
	Branch   string      // the branch
	Worktree string      // its working folder
	Label    string      // what the undo list calls it
	Action   undo.Action // Rewrite, or Amend for folding staged changes in
	// Fixup is the commit made from the staged changes, when folding them
	// into an older commit; "" otherwise.
	Fixup string
}

// Finished says what Finish did.
type Finished struct {
	// Pending: a stopped rewrite is still open (the rebase is in progress).
	Pending bool `json:"pending"`
	// Recorded: the finished rewrite can now be undone.
	Recorded bool   `json:"recorded"`
	Message  string `json:"message"`
}

// Git reads and changes the repo for rewriting.
type Git interface {
	// Head reads where the repo is.
	Head(gitDir string) (refs.Head, error)
	// Resolve is the commit a branch, tag or commit names.
	Resolve(gitDir, rev string) (string, error)
	// Commit reads one commit.
	Commit(gitDir, hash string) (Commit, error)
	// IsAncestor reports whether a is reachable from b.
	IsAncestor(gitDir, a, b string) bool
	// Unpushed lists the commits of HEAD that no remote-tracking branch
	// contains, oldest first (topological). It refuses, explaining why,
	// when the branch pushes to a remote branch this clone has no copy of.
	Unpushed(gitDir string) ([]Commit, error)
	// Range lists the commits after base up to HEAD, oldest first; "" is
	// every commit.
	Range(gitDir, base string) ([]Commit, error)
	// Rebase runs the plan on top of base (git rebase -i, the todo
	// supplied without an editor). An error means git stopped or failed.
	Rebase(gitDir, worktree, base string, p Plan) error
	// Op names the operation in progress ("rebase" after a stop).
	Op(gitDir string) string
	// Conflicted lists the files in conflict.
	Conflicted(gitDir string) ([]string, error)
	// Abort ends the stopped rebase, back to where it began.
	Abort(worktree string) error
	// CommitFixup commits what is staged as a fixup for the commit.
	CommitFixup(worktree, hash string) (string, error)
	// SoftReset moves the branch to hash, keeping the changes staged.
	SoftReset(worktree, hash string) error
	// SavePending remembers a stopped rewrite and keeps its original tip
	// reachable; Pending reads it; ClearPending forgets it and removes the
	// files the rewrite left in the git dir.
	SavePending(gitDir string, p Pending) error
	Pending(gitDir string) (Pending, bool)
	ClearPending(gitDir string)
	// Cleanup removes the files a rewrite left in the git dir.
	Cleanup(gitDir string)
}

// Recorder remembers actions so they can be undone (the undo feature's).
type Recorder interface {
	Before(gitDir string) undo.Mark
	Done(gitDir string, before undo.Mark, a undo.Action, label string)
}
