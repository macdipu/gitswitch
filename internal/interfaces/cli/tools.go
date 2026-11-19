package cli

import (
	"strconv"
	"strings"
)

// cmdTerminal opens a terminal in a repo (this one, or the folder given),
// with `gitswitch set terminal` or the OS default.
func (x *CLI) cmdTerminal(args []string) error {
	if len(args) > 1 {
		return usageErr("gitswitch terminal [<repo>]")
	}
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	} else {
		gd, err := x.requireRepo()
		if err != nil {
			return err
		}
		dir = gd
	}
	if x.app.DryRun() {
		x.say("[dry-run] open a terminal in " + dir)
		return nil
	}
	return x.app.Repos.OpenTerminal(dir)
}

// cmdDiffTool shows a file's change in the external diff tool.
func (x *CLI) cmdDiffTool(args []string) error {
	staged, path := false, ""
	for _, a := range args {
		switch {
		case a == "--staged":
			staged = true
		case path == "" && !strings.HasPrefix(a, "-"):
			path = a
		default:
			return usageErr("gitswitch difftool [--staged] <path>")
		}
	}
	if path == "" {
		return usageErr("gitswitch difftool [--staged] <path>")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if x.app.DryRun() {
		x.say("[dry-run] open " + path + " in the diff tool")
		return nil
	}
	return x.app.Repos.ExternalDiff(gd, path, staged)
}

// cmdMergeTool opens a conflicted file in the external merge tool.
func (x *CLI) cmdMergeTool(args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return usageErr("gitswitch mergetool <path>")
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if x.app.DryRun() {
		x.say("[dry-run] open " + args[0] + " in the merge tool")
		return nil
	}
	x.say("Fix the file in the tool, then run: gitswitch resolve --done")
	return x.app.Repos.ExternalMerge(gd, args[0])
}

// setFetchInterval saves the minutes between the desktop app's quiet
// fetches: off, 5, 10, 15, 30 or 60.
func (x *CLI) setFetchInterval(v string) error {
	m := 0
	if v != "off" {
		var err error
		if m, err = strconv.Atoi(v); err != nil || m == 0 {
			return usageErr("gitswitch set fetch-interval off|5|10|15|30|60")
		}
	}
	if err := x.app.Refs.SetFetchInterval(m); err != nil || x.app.DryRun() {
		return err
	}
	if m == 0 {
		x.say("The desktop app no longer fetches on a timer.")
	} else {
		x.sayf("While the desktop app is open it reads the open repo's new commits every %d minutes.\n", m)
	}
	return nil
}
