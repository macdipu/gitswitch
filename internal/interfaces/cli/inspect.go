package cli

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
)

const (
	compareUsage = "gitswitch compare <A> [<B>] [--since-fork|--direct] [--working] [--file <path>] [--json]"
	reflogUsage  = "gitswitch reflog [-n N] [--skip N] [--branch <name>] [--lost] [--json] | reflog recover <n|hash> <branch-name> [--branch <name>]"
	bisectUsage  = "gitswitch bisect start <bad> <good> | good | bad | skip | status [--json] | reset"
)

// cmdCompare shows how B (default HEAD) differs from A: the commits only
// on each side and the changed files, or one file's diff with --file.
func (x *CLI) cmdCompare(args []string) error {
	var refs []string
	mode, modes := inspect.SinceFork, 0
	var asJSON, working bool
	var file string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			asJSON = true
		case "--direct":
			mode, modes = inspect.Direct, modes+1
		case "--since-fork":
			mode, modes = inspect.SinceFork, modes+1
		case "--working":
			working = true
		case "--file":
			if i+1 >= len(args) {
				return usageErr(compareUsage)
			}
			i++
			file = args[i]
		default:
			if strings.HasPrefix(a, "-") && len(a) > 1 {
				return usageErr(compareUsage)
			}
			refs = append(refs, a)
		}
	}
	if len(refs) == 0 || len(refs) > 2 || modes > 1 || (working && len(refs) > 1) {
		return usageErr(compareUsage)
	}
	a, b := refs[0], ""
	if len(refs) == 2 {
		b = refs[1]
	}
	if working {
		b = inspect.Working
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if file != "" {
		d, err := x.app.Inspect.CompareFile(gd, a, b, mode, file)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(x.out).Encode(d)
		}
		if d.Text == "" {
			x.say("No change in " + file + " " + compareWords(mode) + ".")
			return nil
		}
		x.out.Write([]byte(d.Text))
		if !strings.HasSuffix(d.Text, "\n") {
			x.say()
		}
		if d.Truncated {
			x.sayf("\n... the diff is cut at %d KB\n", inspect.MaxDiffBytes>>10)
		}
		return nil
	}
	c, err := x.app.Inspect.Compare(gd, a, b, mode)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(c)
	}
	x.say(c.Summary)
	commits := func(title string, cs []inspect.Commit, n int) {
		if n == 0 {
			return
		}
		x.sayf("\n%s (%d):\n", title, n)
		for _, c := range cs {
			x.sayf("  %s %s  (%s)\n", c.Short, history.CleanText(c.Subject), history.CleanText(c.Author))
		}
		if n > len(cs) {
			x.sayf("  ... and %d more\n", n-len(cs))
		}
	}
	commits("Only on "+c.A, c.OnlyA, c.OnlyACount)
	b = c.B
	if c.Working {
		b = "the working folder"
	}
	commits("Only on "+b, c.OnlyB, c.OnlyBCount)
	if n := len(c.Files) + c.MoreFiles; n > 0 {
		x.sayf("\nChanged files (%d, %s):\n", n, compareWords(mode))
		for _, f := range c.Files {
			x.sayf("  %s  %s%s\n", statusLetter(f.Status), history.CleanText(f.Path), fileCounts(f))
		}
		if c.MoreFiles > 0 {
			x.sayf("  ... and %d more\n", c.MoreFiles)
		}
		x.say("\nOne file's diff: gitswitch compare " + strings.Join(refs, " ") + " --file <path>")
	}
	return nil
}

func compareWords(m inspect.Mode) string {
	if m == inspect.Direct {
		return "direct difference"
	}
	return "since they went apart"
}

func statusLetter(s string) string {
	switch s {
	case "added":
		return "A"
	case "deleted":
		return "D"
	case "renamed":
		return "R"
	case "copied":
		return "C"
	case "type changed":
		return "T"
	}
	return "M"
}

func fileCounts(f inspect.FileChange) string {
	s := ""
	if f.OldPath != "" {
		s = "  (from " + history.CleanText(f.OldPath) + ")"
	}
	if f.Binary {
		return s + "  binary"
	}
	return s + "  +" + strconv.Itoa(f.Added) + " -" + strconv.Itoa(f.Deleted)
}

// cmdReflog lists the reflog, or makes a branch at one of its entries.
func (x *CLI) cmdReflog(args []string) error {
	if len(args) > 0 && args[0] == "recover" {
		return x.cmdRecover(args[1:])
	}
	n, skip, ref := 0, 0, ""
	var lost, asJSON bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			asJSON = true
		case "--lost":
			lost = true
		case "-n", "--skip", "--branch":
			if i+1 >= len(args) {
				return usageErr(reflogUsage)
			}
			i++
			if a == "--branch" {
				ref = args[i]
				continue
			}
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 0 || (a == "-n" && v == 0) {
				return usageErr(reflogUsage)
			}
			if a == "-n" {
				n = v
			} else {
				skip = v
			}
		default:
			return usageErr(reflogUsage)
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	r, err := x.app.Inspect.Reflog(gd, ref, n, skip, lost)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(r)
	}
	if len(r.Entries) == 0 {
		if lost {
			x.say("No lost commits in the reflog of " + r.Ref + ": every commit it names is still on a branch, tag or remote branch.")
		} else {
			x.say("The reflog of " + r.Ref + " is empty.")
		}
		return nil
	}
	anyLost := false
	for _, e := range r.Entries {
		mark := ""
		if e.Lost {
			mark, anyLost = "  [lost]", true
		}
		text := e.Subject
		if e.Action != "commit" || text == "" {
			text = e.Message
		}
		x.sayf("%4d  %s  %-12s %s  (%s)%s\n", e.Index, e.Short, e.Action, history.CleanText(truncate(text, 60)), ago(e.Time), mark)
	}
	if anyLost {
		x.say("\n[lost] = on no branch, tag or remote branch; only the reflog still leads to it (git removes it for good after a few weeks).")
		x.say("Get it back: gitswitch reflog recover <n> <branch-name>")
	}
	if r.More {
		x.sayf("\nOlder entries: gitswitch reflog --skip %d\n", r.Next)
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// cmdRecover is `reflog recover <n|hash> <name>`: a branch at that entry.
func (x *CLI) cmdRecover(args []string) error {
	const usage = "gitswitch reflog recover <n|hash> <branch-name> [--branch <name>]"
	var pos []string
	ref := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--branch" && i+1 < len(args) {
			i++
			ref = args[i]
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			return usageErr(usage)
		}
		pos = append(pos, args[i])
	}
	if len(pos) != 2 {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	index, hash := -1, pos[0]
	if i, err := strconv.Atoi(pos[0]); err == nil && len(pos[0]) < 7 && i >= 0 {
		index, hash = i, ""
	}
	r, err := x.app.Inspect.Recover(gd, ref, index, hash, pos[1])
	if err != nil || x.app.DryRun() {
		return err
	}
	x.say(r.Message)
	if !r.Lost {
		x.say("(That commit was already on a branch.)")
	}
	return nil
}

// cmdBisect runs a guided bisect: start, good, bad, skip, status, reset.
func (x *CLI) cmdBisect(args []string) error {
	if len(args) == 0 {
		return usageErr(bisectUsage)
	}
	sub, rest := args[0], args[1:]
	asJSON := hasFlag(rest, "--json")
	var pos []string
	for _, a := range rest {
		if a == "--json" {
			continue
		}
		if strings.HasPrefix(a, "-") && sub != "start" {
			return usageErr(bisectUsage)
		}
		pos = append(pos, a)
	}
	if asJSON && sub != "status" {
		return usageErr(bisectUsage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	var st inspect.State
	switch sub {
	case "start":
		if len(pos) != 2 {
			return usageErr("gitswitch bisect start <bad> <good>   (e.g. HEAD v1.0: broken now, fine in v1.0)")
		}
		st, err = x.app.Inspect.BisectStart(gd, pos[0], pos[1])
	case "good", "bad", "skip":
		if len(pos) != 0 {
			return usageErr("gitswitch bisect " + sub)
		}
		st, err = x.app.Inspect.BisectMark(gd, sub)
	case "status":
		if len(pos) != 0 {
			return usageErr("gitswitch bisect status [--json]")
		}
		st, err = x.app.Inspect.BisectStatus(gd)
	case "reset":
		if len(pos) != 0 {
			return usageErr("gitswitch bisect reset")
		}
		st, err = x.app.Inspect.BisectReset(gd)
	default:
		return errors.New("unknown bisect command " + sub + "\n" + "usage: " + bisectUsage)
	}
	if err != nil || x.app.DryRun() {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(st)
	}
	x.printBisect(st, sub)
	return nil
}

func (x *CLI) printBisect(st inspect.State, sub string) {
	x.say(st.Message)
	if sub == "reset" || !st.Running {
		return
	}
	switch {
	case st.Culprit != nil:
		c := st.Culprit
		x.sayf("  %s  %s\n  by %s\n", c.Short, history.CleanText(c.Subject), history.CleanText(c.Author))
		for _, f := range c.Files {
			x.say("  changed  " + history.CleanText(f))
		}
		if c.FilesMore > 0 {
			x.sayf("  ... and %d more files\n", c.FilesMore)
		}
		x.say("\nGo back to " + st.Original + ": gitswitch bisect reset")
	case len(st.Maybe) > 0:
		for _, c := range st.Maybe {
			x.sayf("  %s  %s\n", c.Short, history.CleanText(c.Subject))
		}
		x.say("\nGo back to " + st.Original + ": gitswitch bisect reset")
	default:
		x.say("Build and test it, then: gitswitch bisect good | bad | skip (skip: can't test this one)")
		x.say("Quit and go back to " + st.Original + ": gitswitch bisect reset")
	}
	if sub == "status" && len(st.Steps) > 0 {
		x.say("\nSo far:")
		for _, s := range st.Steps {
			x.sayf("  %-5s %s  %s\n", s.Mark, s.Short, history.CleanText(s.Subject))
		}
	}
}
