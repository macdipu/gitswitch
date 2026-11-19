package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

var stashRefArg = regexp.MustCompile(`^stash@\{(\d+)\}$`)

// stashByArg finds a stash by its number (n of stash@{n}), stash@{n}, or its
// commit hash (any unique start of at least 4 characters).
func stashByArg(all []refs.Stash, arg string) (refs.Stash, error) {
	if m := stashRefArg.FindStringSubmatch(arg); m != nil {
		arg = m[1]
	}
	if n, err := strconv.Atoi(arg); err == nil && n >= 0 && n < len(all) {
		return all[n], nil
	}
	if len(arg) >= 4 {
		var found []refs.Stash
		for _, s := range all {
			if strings.HasPrefix(s.Hash, strings.ToLower(arg)) {
				found = append(found, s)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return refs.Stash{}, fmt.Errorf("%q matches more than one stash; use more of the hash", arg)
		}
	}
	return refs.Stash{}, fmt.Errorf("no stash %q; gitswitch stash lists them", arg)
}

func stashKindNote(s refs.Stash) string {
	switch s.Kind {
	case refs.StashPark:
		return "   (changes parked on " + s.ParkedOn + ")"
	case refs.StashDiscard:
		return "   (changes you discarded on " + s.ParkedOn + ")"
	}
	return ""
}

// cmdStash is the stash manager: every stash, parked and discarded ones
// too. Stashes are named by number (stash@{n}) or commit hash.
func (x *CLI) cmdStash(args []string) error {
	const usage = "gitswitch stash [list] [--json] | stash show <n|hash> [<path>] [--json] | stash save [-m <message>] [--staged-keep] [-u|--no-untracked] [<path>...] | stash apply|pop|drop <n|hash> [-y] | stash branch <n|hash> <name>"
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	switch verb {
	case "list":
		for _, a := range args {
			if a != "--json" {
				return usageErr(usage)
			}
		}
		all, err := x.app.Refs.Stashes(gd)
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			if all == nil {
				all = []refs.Stash{}
			}
			return json.NewEncoder(x.out).Encode(all)
		}
		if len(all) == 0 {
			x.say("No stashes in this repo.")
			return nil
		}
		x.say("Stashes (git stash), newest first:")
		for _, s := range all {
			x.sayf("  %s%s\n", s.Describe(time.Now().Unix()), stashKindNote(s))
		}
		x.say("Look: stash show <n> [<path>]   put back: stash apply|pop <n>   delete: stash drop <n>   new branch: stash branch <n> <name>")
		return nil
	case "save":
		return x.stashSave(gd, args, usage)
	case "show", "apply", "pop", "drop", "branch":
	default:
		return usageErr(usage)
	}

	var pos []string
	var yes, asJSON bool
	for _, a := range args {
		switch {
		case a == "-y" || a == "--yes":
			yes = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return usageErr(usage)
		default:
			pos = append(pos, a)
		}
	}
	limit := map[string]int{"show": 2, "apply": 1, "pop": 1, "drop": 1, "branch": 2}[verb]
	if len(pos) == 0 || len(pos) > limit || (verb == "branch" && len(pos) != 2) || (yes && verb != "drop") || (asJSON && verb != "show") {
		return usageErr(usage)
	}
	all, err := x.app.Refs.Stashes(gd)
	if err != nil {
		return err
	}
	st, err := stashByArg(all, pos[0])
	if err != nil {
		return err
	}
	dry := x.app.DryRun()
	switch verb {
	case "show":
		if len(pos) == 2 {
			d, err := x.app.Refs.StashDiff(gd, st.Hash, pos[1])
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(x.out).Encode(d)
			}
			x.say(d.Text)
			if d.Truncated {
				x.say("(the diff is cut short)")
			}
			return nil
		}
		if asJSON {
			return json.NewEncoder(x.out).Encode(st)
		}
		x.sayf("%s%s\n", st.Describe(time.Now().Unix()), stashKindNote(st))
		for _, f := range st.Files {
			extra := ""
			if f.Staged {
				extra = " (staged)"
			}
			if f.Binary {
				x.sayf("  %s  %s  binary%s\n", f.Status, f.Path, extra)
			} else {
				x.sayf("  %s  %s  +%d -%d%s\n", f.Status, f.Path, f.Added, f.Deleted, extra)
			}
		}
		x.say("A diff of one file: gitswitch stash show " + strconv.Itoa(st.Index) + " <path>")
		return nil
	case "apply", "pop":
		do, past := x.app.Refs.StashApply, "Applied"
		if verb == "pop" {
			do, past = x.app.Refs.StashPop, "Popped"
		}
		if err := do(gd, st.Hash); err != nil {
			return err
		}
		if !dry {
			if verb == "pop" {
				x.say(past + " " + st.Label() + " and removed it from the list. (Not undoable: the changes are in your files.)")
			} else {
				x.say(past + " " + st.Label() + "; it is still in the stash list.")
			}
		}
		return nil
	case "branch":
		if err := x.app.Refs.StashBranch(gd, st.Hash, pos[1]); err != nil {
			return err
		}
		if !dry {
			x.say("Made branch " + strings.TrimSpace(pos[1]) + " from " + st.Label() + ", switched to it and removed the stash.")
		}
		return nil
	}
	// drop
	if !yes && !dry {
		if err := x.ask(fmt.Sprintf("Delete %s: %s? `gitswitch undo` brings it back.", st.Label(), refs.FileList(stashPaths(st)))); err != nil {
			return err
		}
	}
	if err := x.app.Refs.StashDrop(gd, st.Hash); err != nil {
		return err
	}
	if !dry {
		x.say("Deleted " + st.Label() + ". Undo it with: gitswitch undo")
	}
	return nil
}

func stashPaths(s refs.Stash) []string {
	p := make([]string, len(s.Files))
	for i, f := range s.Files {
		p[i] = f.Path
	}
	return p
}

// stashSave reads `save [-m msg] [--staged-keep] [-u|--no-untracked] [--] [path...]`.
func (x *CLI) stashSave(gd string, args []string, usage string) error {
	o := refs.StashOptions{Untracked: true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			o.Paths = append(o.Paths, args[i+1:]...)
			i = len(args)
		case a == "-m" || a == "--message":
			if i+1 >= len(args) {
				return usageErr(usage)
			}
			i++
			o.Message = args[i]
		case strings.HasPrefix(a, "--message="):
			o.Message = strings.TrimPrefix(a, "--message=")
		case a == "--staged-keep" || a == "--keep-index":
			o.KeepIndex = true
		case a == "-u" || a == "--include-untracked":
			o.Untracked = true
		case a == "--no-untracked":
			o.Untracked = false
		case strings.HasPrefix(a, "-"):
			return usageErr(usage)
		default:
			o.Paths = append(o.Paths, a)
		}
	}
	st, err := x.app.Refs.StashSave(gd, o)
	if err != nil {
		return err
	}
	if !x.app.DryRun() {
		x.sayf("Stashed %s as %s.\n", refs.FileList(stashPaths(st)), st.Label())
		if len(o.Paths) == 0 && !o.KeepIndex {
			x.say("Undo it with: gitswitch undo")
		}
	}
	return nil
}
