package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	wsapp "github.com/macdipu/gitswitch/internal/features/workspace/application"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// hunkFlag finds "--hunk 1,3" (or --hunk=1,3) in args. Hunks are numbered
// from 1, as `gitswitch hunks` shows them. found says the flag is there;
// rest is what remains (the path, and -y).
func hunkFlag(args []string, usage string) (rest []string, hunks []int, found bool, err error) {
	for i := 0; i < len(args); i++ {
		a, val := args[i], ""
		switch {
		case a == "--hunk" || a == "-H":
			if i+1 >= len(args) {
				return nil, nil, true, usageErr(usage)
			}
			i++
			val = args[i]
		case strings.HasPrefix(a, "--hunk="):
			val = strings.TrimPrefix(a, "--hunk=")
		default:
			rest = append(rest, a)
			continue
		}
		found = true
		for _, f := range strings.Split(val, ",") {
			n, e := strconv.Atoi(strings.TrimSpace(f))
			if e != nil || n < 1 {
				return nil, nil, true, fmt.Errorf("--hunk takes hunk numbers from 1, like 1,3 (gitswitch hunks <path> lists them)")
			}
			hunks = append(hunks, n-1)
		}
	}
	return rest, hunks, found, nil
}

// hunkAction stages, unstages or discards whole hunks of one file.
func (x *CLI) hunkAction(gd string, args []string, hunks []int, verb string, allowYes bool) error {
	usage := "gitswitch " + verb + " <path> --hunk <n>[,<n>...]"
	var path string
	yes := false
	for _, a := range args {
		switch {
		case allowYes && (a == "-y" || a == "--yes"):
			yes = true
		case a == "--":
		case path == "" && !strings.HasPrefix(a, "-"):
			path = x.app.Workspace.RepoPath(gd, cwd(), a)
		default:
			return usageErr(usage)
		}
	}
	if path == "" {
		return usageErr(usage)
	}
	// verb is a change of the staged diff only for unstage.
	d, err := x.app.Workspace.Diff(gd, path, verb == "unstage")
	if err != nil {
		return err
	}
	if d.Text == "" {
		return fmt.Errorf("%s has no change to %s", path, verb)
	}
	if len(d.Hunks) == 0 {
		return fmt.Errorf("%s: %s", path, d.NoHunks)
	}
	sel := workspace.Selection{Hunks: hunks}
	if verb == "discard" && !yes && !x.app.DryRun() {
		if err := x.ask(fmt.Sprintf("Discard hunk %s of %s? The change is removed from the file and kept in git stash.", numbers(hunks), path)); err != nil {
			return err
		}
	}
	var res wsapp.HunksChanged
	switch verb {
	case "stage":
		res, err = x.app.Workspace.StageHunks(gd, path, d.Fingerprint, sel)
	case "unstage":
		res, err = x.app.Workspace.UnstageHunks(gd, path, d.Fingerprint, sel)
	default:
		res, err = x.app.Workspace.DiscardHunks(gd, path, d.Fingerprint, sel)
	}
	if err != nil || x.app.DryRun() {
		return err
	}
	switch verb {
	case "stage":
		x.sayf("Staged hunk %s of %s (%d line(s)).\n", numbers(hunks), path, res.Lines)
	case "unstage":
		x.sayf("Unstaged hunk %s of %s (%d line(s)); the changes are still in the file.\n", numbers(hunks), path, res.Lines)
	default:
		x.sayf("Discarded hunk %s of %s (%d line(s)).\n", numbers(hunks), path, res.Lines)
		x.sayf("  The file as it was is kept as the stash %q: %s brings it back.\n", res.Stash, res.Restore)
	}
	return nil
}

func numbers(hunks []int) string {
	s := make([]string, len(hunks))
	for i, h := range hunks {
		s[i] = strconv.Itoa(h + 1)
	}
	return strings.Join(s, ",")
}

// cmdHunks lists the hunks of a file's uncommitted change, numbered for
// --hunk: not staged, or with --staged what is staged.
func (x *CLI) cmdHunks(args []string) error {
	const usage = "gitswitch hunks <path> [--staged] [--json]"
	staged, asJSON, path := false, false, ""
	for _, a := range args {
		switch {
		case a == "--staged" || a == "--cached":
			staged = true
		case a == "--json":
			asJSON = true
		case path == "" && a != "" && !strings.HasPrefix(a, "-"):
			path = a
		default:
			return usageErr(usage)
		}
	}
	if path == "" {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	path = x.app.Workspace.RepoPath(gd, cwd(), path)
	d, err := x.app.Workspace.Diff(gd, path, staged)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(d)
	}
	which := "not staged"
	if staged {
		which = "staged"
	}
	if d.Text == "" {
		x.sayf("No %s change in %s.\n", which, path)
		return nil
	}
	if len(d.Hunks) == 0 {
		return fmt.Errorf("%s: %s", path, d.NoHunks)
	}
	x.sayf("%s: %d hunk(s), %s\n", path, len(d.Hunks), which)
	for _, h := range d.Hunks {
		add, del := h.CountChanges()
		x.sayf("\n#%d  %s   (+%d -%d)\n", h.Index+1, h.Header, add, del)
		for _, l := range h.Lines {
			sign := " "
			switch l.Kind {
			case workspace.LineAdd:
				sign = "+"
			case workspace.LineDel:
				sign = "-"
			}
			x.sayf("  %s%s\n", sign, strings.TrimSuffix(l.Text, "\r"))
		}
	}
	verb := "stage"
	if staged {
		verb = "unstage"
	}
	x.sayf("\ngitswitch %s %s --hunk 1,3 moves whole hunks; gitswitch discard %s --hunk N [-y] removes one, keeping it in git stash.\n", verb, path, path)
	return nil
}
