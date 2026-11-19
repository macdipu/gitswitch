package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	submoduleUsage = "gitswitch submodule [list] [--json] | submodule init [<path>] | submodule update [<path>] [--remote] [--recursive] [-y] | submodule sync | submodule add <url> <path> [-b <branch>] | submodule remove <path> [--force] [-y]"
)

// cmdSubmodule lists a repo's submodules and initializes, updates, syncs,
// adds and removes them. Each one is fetched as the account its own URL
// maps to, not the superproject's.
func (x *CLI) cmdSubmodule(args []string) error {
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	var pos []string
	var asJSON, yes, remote, recursive, force bool
	branch := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--json":
			asJSON = true
		case "-y", "--yes":
			yes = true
		case "--remote":
			remote = true
		case "--recursive":
			recursive = true
		case "--force", "-f":
			force = true
		case "-b", "--branch":
			if i+1 >= len(args) {
				return usageErr(submoduleUsage)
			}
			i++
			branch = args[i]
		default:
			if strings.HasPrefix(a, "-") {
				return usageErr(submoduleUsage)
			}
			pos = append(pos, a)
		}
	}
	path := func() string {
		if len(pos) > 0 {
			return pos[0]
		}
		return ""
	}
	bad := func(maxPos int, flags ...bool) bool {
		if len(pos) > maxPos {
			return true
		}
		for _, f := range flags {
			if f {
				return true
			}
		}
		return false
	}
	switch verb {
	case "list", "ls":
		if bad(0, yes, remote, recursive, force, branch != "") {
			return usageErr(submoduleUsage)
		}
		return x.submoduleList(gd, asJSON)
	case "init":
		if bad(1, asJSON, remote, recursive, force, branch != "") {
			return usageErr(submoduleUsage)
		}
		r, err := x.app.Submodules.Init(gd, path())
		if err != nil || x.app.DryRun() {
			return err
		}
		x.say(r.Message)
		return nil
	case "update":
		if bad(1, asJSON, force, branch != "") {
			return usageErr(submoduleUsage)
		}
		if remote && !yes && !x.app.DryRun() {
			if err := x.ask("Move " + subOr(path(), "every submodule") + " to the tip of its tracked branch? The repo will then record a different commit for it (you commit that change)."); err != nil {
				return err
			}
		}
		r, err := x.app.Submodules.Update(gd, path(), remote, recursive)
		if err != nil || x.app.DryRun() {
			return err
		}
		for _, c := range r.Changed {
			x.sayf("  %s  %s -> %s\n", c.Path, subShort(c.From), subShort(c.To))
		}
		x.say(r.Message)
		return nil
	case "sync":
		if bad(0, asJSON, yes, remote, recursive, force, branch != "") {
			return usageErr(submoduleUsage)
		}
		r, err := x.app.Submodules.Sync(gd)
		if err != nil || x.app.DryRun() {
			return err
		}
		x.say(r.Message)
		return nil
	case "add":
		if len(pos) != 2 || bad(2, asJSON, yes, remote, recursive, force) {
			return usageErr(submoduleUsage)
		}
		r, err := x.app.Submodules.Add(gd, pos[0], pos[1], branch)
		if r.Note != "" {
			x.say("Note: " + r.Note + ".")
		}
		if err != nil || x.app.DryRun() {
			return err
		}
		if r.Account != "" {
			x.sayf("It is fetched as the account %s.\n", r.Account)
		}
		x.say(r.Message)
		return nil
	case "remove", "rm":
		if len(pos) != 1 || bad(1, asJSON, remote, recursive, branch != "") {
			return usageErr(submoduleUsage)
		}
		plan, err := x.app.Submodules.PlanRemove(gd, pos[0], force)
		if err != nil {
			return err
		}
		if plan.Blocked != "" {
			return errors.New("can't remove: " + plan.Blocked)
		}
		if !yes && !x.app.DryRun() {
			x.sayf("Removing %s deletes:\n", plan.Path)
			for _, d := range plan.Deleted {
				x.say("  - " + d)
			}
			if err := x.ask("Remove " + plan.Path + "?"); err != nil {
				return err
			}
		}
		r, err := x.app.Submodules.Remove(gd, pos[0], force)
		if err != nil || x.app.DryRun() {
			return err
		}
		x.say(r.Message)
		return nil
	}
	return usageErr(submoduleUsage)
}

func (x *CLI) submoduleList(gd string, asJSON bool) error {
	es, err := x.app.Submodules.List(gd)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(es)
	}
	if len(es) == 0 {
		x.say("This repo has no submodules. Add one with: gitswitch submodule add <url> <path>")
		return nil
	}
	for _, e := range es {
		line := fmt.Sprintf("%-24s %s  %s", e.Path, subShort(e.CheckedOut), e.State)
		if !e.Initialized {
			line = fmt.Sprintf("%-24s %s  %s", e.Path, subShort(e.Recorded), e.State)
		}
		x.say(line)
		var more []string
		if e.Dirty > 0 {
			more = append(more, fmt.Sprintf("%d changed file(s) inside", e.Dirty))
		}
		if e.Behind > 0 {
			more = append(more, fmt.Sprintf("%d new commit(s) on %s", e.Behind, e.Upstream))
		}
		if e.Branch != "" {
			more = append(more, "tracks "+e.Branch)
		}
		switch {
		case e.Account != "":
			more = append(more, "account "+e.Account)
		default:
			more = append(more, "no account picked: git asks")
		}
		x.sayf("  %s\n  %s\n", e.URL, strings.Join(more, " · "))
	}
	return nil
}

func subShort(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	if h == "" {
		return "-------"
	}
	return h
}

func subOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
