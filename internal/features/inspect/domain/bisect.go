package domain

import (
	"fmt"
	"math/bits"
	"regexp"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// MaxCulpritFiles is how many files of the first bad commit are listed.
const MaxCulpritFiles = 50

// Marks a commit under test can get.
const (
	Good = "good"
	Bad  = "bad"
	Skip = "skip"
)

// CheckMark refuses anything but good, bad or skip.
func CheckMark(m string) (string, error) {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case Good, Bad, Skip:
		return m, nil
	}
	return "", fmt.Errorf("mark the commit good, bad or skip, not %q", m)
}

// Step is one line of the bisect log: a commit marked good, bad or skipped.
type Step struct {
	Mark    string `json:"mark"` // good, bad, skip
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
}

// Culprit is the first bad commit.
type Culprit struct {
	Commit
	Files     []string `json:"files"`
	FilesMore int      `json:"filesMore"` // files left out after MaxCulpritFiles
}

// State is where a bisect is.
type State struct {
	Running bool `json:"running"`
	// Original is the branch (or, when HEAD was detached, the commit) the
	// repo returns to on reset.
	Original string `json:"original"`
	// Next is the commit now checked out, to build and test; nil once the
	// culprit is found.
	Next *Commit `json:"next"`
	// Left is the number of commits still in doubt, and StepsLeft the
	// estimate "about N steps".
	Left      int    `json:"left"`
	StepsLeft int    `json:"stepsLeft"`
	Steps     []Step `json:"steps"` // the log so far
	// Culprit is set when the search is over.
	Culprit *Culprit `json:"culprit"`
	// Maybe lists the commits that could be the first bad one when too
	// many were skipped to tell.
	Maybe   []Commit `json:"maybe"`
	Message string   `json:"message"`
}

var hexRe = `[0-9a-f]{40,64}`

var logRe = regexp.MustCompile(`^# (good|bad|skip): \[(` + hexRe + `)\] ?(.*)$`)

// ParseLog reads `git bisect log`: the steps, the first bad commit when
// the search is over, and the commits it could be when skips left it open.
// The first two steps are the bad and good commits the bisect started with.
func ParseLog(out string) (steps []Step, culprit string, maybe []string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := logRe.FindStringSubmatch(line); m != nil {
			steps = append(steps, Step{Mark: m[1], Hash: m[2], Short: m[2][:7], Subject: m[3]})
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# first bad commit: ["); ok {
			culprit, _, _ = strings.Cut(rest, "]")
		}
		if rest, ok := strings.CutPrefix(line, "# possible first bad commit: ["); ok {
			h, _, _ := strings.Cut(rest, "]")
			maybe = append(maybe, h)
		}
	}
	return steps, culprit, maybe
}

// EstimateSteps is git's own estimate of the tests left when n commits
// are in doubt.
func EstimateSteps(n int) int {
	if n <= 1 {
		return 0
	}
	e := bits.Len(uint(n)) - 1 // log2
	x := n - 1<<e
	if 1<<e < 3*x {
		return e
	}
	return e - 1
}

// StepsWords says the estimate in plain words.
func StepsWords(steps int) string {
	switch {
	case steps <= 0:
		return "about 1 step"
	case steps == 1:
		return "about 1 step"
	}
	return fmt.Sprintf("about %d steps", steps)
}

// CheckStart refuses a bisect that can't start: bisect checks out older
// commits, so the repo must be quiet and clean.
func CheckStart(h refs.Head) error {
	c := h.Changes
	switch {
	case h.Busy == "bisect":
		return fmt.Errorf("a bisect is already running; finish it, or go back with `gitswitch bisect reset`")
	case h.Busy != "":
		return remote.Busy(h.Busy)
	case h.Hash == "":
		return fmt.Errorf("this repo has no commits yet, so there is nothing to search")
	case c.Conflicts > 0:
		return fmt.Errorf("%d file(s) still have conflicts; fix them first", c.Conflicts)
	case c.Staged+c.Changed > 0:
		return fmt.Errorf("you have uncommitted changes (%d staged, %d changed); commit, park or discard them first, because a bisect checks out older commits", c.Staged, c.Changed)
	}
	return nil
}

// CheckRunning refuses to mark or reset when no bisect is running.
func CheckRunning(h refs.Head) error {
	switch h.Busy {
	case "bisect":
		return nil
	case "":
		return fmt.Errorf("no bisect is running; start one with `gitswitch bisect start <bad> <good>`")
	}
	return remote.Busy(h.Busy)
}

// Words describes a State in plain words.
func (s State) Words() string {
	switch {
	case !s.Running:
		return "No bisect is running."
	case s.Culprit != nil:
		return fmt.Sprintf("The first bad commit is %s %s, by %s.", s.Culprit.Short, s.Culprit.Subject, s.Culprit.Author)
	case len(s.Maybe) > 0:
		return fmt.Sprintf("Too many commits were skipped to tell: the first bad commit is one of %d.", len(s.Maybe))
	case s.Next != nil:
		return fmt.Sprintf("Test %s %s, then mark it good, bad or skip. %s left.", s.Next.Short, s.Next.Subject, StepsWords(s.StepsLeft))
	}
	return "A bisect is running."
}

// BisectGit is the part of Git that runs a bisect.
type BisectGit interface {
	// Origin reads where the repo was before the bisect started ("" and ""
	// when unknown).
	Origin(gitDir string) (branch, hash string)
	SaveOrigin(gitDir, branch, hash string) error
	ClearOrigin(gitDir string)
	// IsAncestor reports whether a is an ancestor of (or is) b.
	IsAncestor(gitDir, a, b string) bool
	// BisectStart, BisectMark and BisectReset run git bisect in the working
	// folder. Start and Mark return git's output; Reset returns to to (a
	// branch or commit).
	BisectStart(wt, bad, good string) (string, error)
	BisectMark(wt, mark string) (string, error)
	BisectReset(wt, to string) error
	// BisectLog is `git bisect log`.
	BisectLog(gitDir string) string
	// Candidates counts the commits still in doubt (including the bad one).
	Candidates(gitDir string) int
	// CommitFiles lists the files a commit changed, at most limit, and how
	// many more there are.
	CommitFiles(gitDir, hash string, limit int) ([]string, int, error)
}
