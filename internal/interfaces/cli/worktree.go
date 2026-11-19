package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

const worktreeUsage = "gitswitch worktree [list] [--json] | worktree add <path> [<branch>] [-b <new> [--from <rev>]] [--detach <rev>] [--register] | worktree remove <path|branch> [--force] [-y] | worktree prune [-y] | worktree lock|unlock <path|branch> [--reason <text>] | worktree repair [<path>...]"

// cmdWorktree manages the repo's worktrees: more working folders of the
// same repo, each with its own branch. They share the repo's account.
func (x *CLI) cmdWorktree(args []string) error {
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	switch verb {
	case "list", "ls":
		for _, a := range args {
			if a != "--json" {
				return usageErr(worktreeUsage)
			}
		}
		all, err := x.app.Refs.Worktrees(gd)
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(x.out).Encode(all)
		}
		wd, _ := os.Getwd()
		for _, w := range all {
			x.say(describeWorktree(w, wd))
		}
		if len(all) == 1 {
			x.say("Only the repo's own folder. Add another with: gitswitch worktree add <folder> <branch>")
		} else if len(all) > 0 && all[0].Account != "" {
			x.say("All of them use the account " + all[0].Account + ".")
		}
		return nil
	case "add":
		return x.worktreeAdd(gd, args)
	case "remove", "rm":
		return x.worktreeRemove(gd, args)
	case "prune":
		return x.worktreePrune(gd, args)
	case "lock", "unlock":
		return x.worktreeLock(gd, verb == "lock", args)
	case "repair":
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				return usageErr(worktreeUsage)
			}
		}
		out, err := x.app.Refs.WorktreeRepair(gd, args...)
		if err != nil {
			return err
		}
		if out == "" {
			out = "Nothing to repair."
		}
		x.say(out)
		return nil
	}
	return usageErr(worktreeUsage)
}

// describeWorktree is one line of the list; "*" marks the folder you are in.
func describeWorktree(w refs.Worktree, wd string) string {
	mark := " "
	if wd != "" && inFolder(w.Path, wd) {
		mark = "*"
	}
	what := w.Branch
	switch {
	case w.Bare:
		what = "(bare)"
	case w.Detached:
		what = "(detached " + w.Short + ")"
	default:
		what += "  " + w.Short
	}
	var notes []string
	if w.Main {
		notes = append(notes, "main folder")
	}
	if w.Changes != nil {
		if d := w.Changes.Describe(); d != "" {
			notes = append(notes, strings.TrimPrefix(d, "uncommitted: "))
		} else {
			notes = append(notes, "clean")
		}
	}
	if w.Locked {
		notes = append(notes, "locked"+reasonSuffix(w.LockReason))
	}
	if w.Prunable {
		notes = append(notes, "folder is gone (worktree prune)")
	}
	s := fmt.Sprintf("%s %s  %s", mark, w.Path, what)
	if len(notes) > 0 {
		s += "   (" + strings.Join(notes, "; ") + ")"
	}
	return s
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

func inFolder(dir, p string) bool {
	rel, err := filepath.Rel(filesystem.RealPath(dir), filesystem.RealPath(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// worktreeArg turns <path|branch> into the folder: a branch checked out in
// a worktree names its folder; anything else is a path from here.
func (x *CLI) worktreeArg(gd, arg string) (string, error) {
	all, err := x.app.Refs.Worktrees(gd)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filesystem.ExpandHome(arg))
	if err != nil {
		return "", err
	}
	for _, w := range all {
		if filesystem.SamePath(filesystem.RealPath(w.Path), filesystem.RealPath(abs)) {
			return w.Path, nil
		}
	}
	for _, w := range all {
		if w.Branch != "" && w.Branch == arg {
			return w.Path, nil
		}
	}
	return abs, nil
}

func (x *CLI) worktreeAdd(gd string, args []string) error {
	var o refs.WorktreeAddOptions
	var pos []string
	register := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", usageErr(worktreeUsage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--register":
			register = true
		case a == "-b" || a == "-B":
			o.NewBranch, err = next()
		case a == "--from":
			o.From, err = next()
		case a == "--detach":
			// --detach [<rev>]: the rev is optional, HEAD when omitted
			o.Detach = "HEAD"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && len(pos) >= 1 {
				i++
				o.Detach = args[i]
			}
		case strings.HasPrefix(a, "-"):
			return usageErr(worktreeUsage)
		default:
			pos = append(pos, a)
		}
		if err != nil {
			return err
		}
	}
	if len(pos) == 0 || len(pos) > 2 {
		return usageErr(worktreeUsage)
	}
	path := pos[0]
	if len(pos) == 2 {
		if o.NewBranch != "" || o.Detach != "" {
			return usageErr(worktreeUsage)
		}
		o.Branch = pos[1]
	}
	if o.NewBranch == "" && o.Detach == "" && o.Branch == "" {
		// worktree add <path>: a new branch named after the folder
		o.NewBranch = filepath.Base(filepath.Clean(path))
	}
	res, err := x.app.Refs.WorktreeAdd(gd, path, o)
	if err != nil {
		return err
	}
	if x.app.DryRun() {
		return nil
	}
	what := "branch " + res.Branch
	if res.Branch == "" {
		what = "a detached HEAD at " + res.Head
	}
	x.sayf("Added the worktree %s on %s.\n", res.Path, what)
	if res.Account != "" {
		x.say("It uses the account " + res.Account + ", like the repo.")
	}
	for _, w := range res.Warnings {
		x.say("Warning: " + w)
	}
	if register {
		info, err := x.app.Repos.Add(res.Path)
		if err != nil {
			return fmt.Errorf("the worktree was added, but not to the repo list: %w", err)
		}
		x.say("Added it to the repo list: " + info.Path)
	} else {
		x.say("Show it in the repo list too with: gitswitch repo add " + res.Path)
	}
	x.say("Undo: gitswitch worktree remove " + res.Path)
	return nil
}

func (x *CLI) worktreeRemove(gd string, args []string) error {
	var arg string
	force, yes := false, false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case a == "-y" || a == "--yes":
			yes = true
		case arg == "" && !strings.HasPrefix(a, "-"):
			arg = a
		default:
			return usageErr(worktreeUsage)
		}
	}
	if arg == "" {
		return usageErr(worktreeUsage)
	}
	path, err := x.worktreeArg(gd, arg)
	if err != nil {
		return err
	}
	if wd, err := os.Getwd(); err == nil && inFolder(path, wd) {
		return errors.New("you are inside that worktree; cd out of it first")
	}
	if !yes && !x.app.DryRun() {
		q := "Remove the worktree " + path + "? Its folder is deleted; the branch and commits stay."
		if force {
			q = "Remove the worktree " + path + " WITH its uncommitted changes? Its folder is deleted; the branch and commits stay."
		}
		if err := x.ask(q); err != nil {
			return err
		}
	}
	if err := x.app.Refs.WorktreeRemove(gd, path, force); err != nil {
		return err
	}
	if x.app.DryRun() {
		return nil
	}
	x.say("Removed the worktree " + path + ". Its branch is kept.")
	return nil
}

func (x *CLI) worktreePrune(gd string, args []string) error {
	yes := false
	for _, a := range args {
		if a != "-y" && a != "--yes" {
			return usageErr(worktreeUsage)
		}
		yes = true
	}
	plan, err := x.app.Refs.WorktreePrunePlan(gd)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		x.say("Nothing to prune: every worktree's folder is there.")
		return nil
	}
	x.say("These worktrees have no folder any more:")
	for _, w := range plan {
		x.sayf("  %s  %s\n", w.Path, w.Branch)
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask(fmt.Sprintf("Forget %d worktree(s)? No files are touched; the branches stay.", len(plan))); err != nil {
			return err
		}
	}
	if _, err := x.app.Refs.WorktreePrune(gd); err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.sayf("Forgot %d worktree(s).\n", len(plan))
	}
	return nil
}

func (x *CLI) worktreeLock(gd string, lock bool, args []string) error {
	var arg string
	reason := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--reason" && i+1 < len(args):
			i++
			reason = args[i]
		case strings.HasPrefix(a, "--reason="):
			reason = strings.TrimPrefix(a, "--reason=")
		case arg == "" && !strings.HasPrefix(a, "-"):
			arg = a
		default:
			return usageErr(worktreeUsage)
		}
	}
	if arg == "" || (!lock && reason != "") {
		return usageErr(worktreeUsage)
	}
	path, err := x.worktreeArg(gd, arg)
	if err != nil {
		return err
	}
	if lock {
		err = x.app.Refs.WorktreeLock(gd, path, reason)
	} else {
		err = x.app.Refs.WorktreeUnlock(gd, path)
	}
	if err != nil || x.app.DryRun() {
		return err
	}
	if lock {
		x.say("Locked " + path + ": it can't be pruned or removed. Undo: gitswitch worktree unlock " + arg)
	} else {
		x.say("Unlocked " + path + ".")
	}
	return nil
}
