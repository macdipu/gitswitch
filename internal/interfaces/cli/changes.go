package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// cmdChanges lists the uncommitted changes: staged, not staged, conflicts.
func (x *CLI) cmdChanges(args []string) error {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return usageErr("gitswitch changes [--json]")
		}
		asJSON = true
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	st, err := x.app.Workspace.Status(gd)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(st)
	}
	if st.Busy != "" {
		x.sayf("git is in the middle of a %s.\n\n", st.Busy)
	}
	if len(st.Files) == 0 {
		x.say("No uncommitted changes.")
		return nil
	}
	section := func(title string, files []workspace.File, hint string) {
		if len(files) == 0 {
			return
		}
		x.sayf("%s (%d):\n", title, len(files))
		for _, f := range files {
			x.sayf("  %-26s %s\n", changeWord(f, title), f.Path)
		}
		if hint != "" {
			x.say("  " + hint)
		}
		x.say()
	}
	section("Conflicts", st.Conflicts(), "fix them in your editor, then stage them")
	section("Staged", st.Staged(), "gitswitch commit -m <message> commits these; gitswitch unstage <path> takes one out")
	section("Not staged", st.Changed(), "gitswitch stage <path> (or --all) adds them; gitswitch diff <path> shows a change")
	return nil
}

// changeWord describes a file's change in one section of the list: its
// staged change under Staged, the one in the working folder elsewhere.
func changeWord(f workspace.File, section string) string {
	if f.Conflict || f.New() {
		return f.Describe()
	}
	l := f.Worktree
	if section == "Staged" {
		l = f.Staged
	}
	switch l {
	case "A":
		return "added"
	case "D":
		return "deleted"
	case "R":
		return "renamed from " + f.Orig
	case "C":
		return "copied from " + f.Orig
	case "T":
		return "type changed"
	}
	return "modified"
}

// cmdDiff prints the uncommitted change of a file, or of every file: not
// staged (new files in full), or with --staged what is staged.
func (x *CLI) cmdDiff(args []string) error {
	const usage = "gitswitch diff [--staged] [<path>]"
	staged, path := false, ""
	for _, a := range args {
		switch {
		case a == "--staged" || a == "--cached":
			staged = true
		case path == "" && a != "" && !strings.HasPrefix(a, "-"):
			path = a
		default:
			return usageErr(usage)
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	if path != "" {
		path = x.app.Workspace.RepoPath(gd, cwd(), path)
	}
	d, err := x.app.Workspace.Diff(gd, path, staged)
	if err != nil {
		return err
	}
	if d.Text == "" {
		switch {
		case staged:
			x.say("Nothing staged" + forPath(d.Path) + ".")
		default:
			x.say("No changes that are not staged" + forPath(d.Path) + ". Staged ones: gitswitch diff --staged")
		}
		return nil
	}
	fmt.Fprintln(x.out, d.Text)
	if d.Truncated {
		x.sayf("... cut at %d KB; git diff shows all of it\n", workspace.MaxDiffBytes>>10)
	}
	return nil
}

func forPath(p string) string {
	if p == "" {
		return ""
	}
	return " in " + p
}

// pathsArg reads "<path>... | --all [-y]" with paths relative to the
// current folder, as git takes them, turned into repo paths.
func (x *CLI) pathsArg(gd string, args []string, usage string, allowYes bool) (paths []string, all, yes bool, err error) {
	for _, a := range args {
		switch {
		case a == "--all" || a == "-A":
			all = true
		case allowYes && (a == "-y" || a == "--yes"):
			yes = true
		case a == "--":
		case strings.HasPrefix(a, "-"):
			return nil, false, false, usageErr(usage)
		default:
			paths = append(paths, x.app.Workspace.RepoPath(gd, cwd(), a))
		}
	}
	if all == (len(paths) > 0) {
		return nil, false, false, usageErr(usage)
	}
	return paths, all, yes, nil
}

func (x *CLI) cmdStage(args []string) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	paths, all, _, err := x.pathsArg(gd, args, "gitswitch stage <path>... | --all", false)
	if err != nil {
		return err
	}
	done, err := x.app.Workspace.Stage(gd, paths, all)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Staged %s. Commit them: gitswitch commit -m <message>\n", files(done))
	return nil
}

func (x *CLI) cmdUnstage(args []string) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	paths, all, _, err := x.pathsArg(gd, args, "gitswitch unstage <path>... | --all", false)
	if err != nil {
		return err
	}
	done, err := x.app.Workspace.Unstage(gd, paths, all)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Unstaged %s; the changes are still in the files.\n", files(done))
	return nil
}

func (x *CLI) cmdDiscard(args []string) error {
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	paths, all, yes, err := x.pathsArg(gd, args, "gitswitch discard <path>... | --all [-y]", true)
	if err != nil {
		return err
	}
	if !yes && !x.app.DryRun() {
		what := strings.Join(paths, ", ")
		if all {
			what = "every uncommitted change"
		}
		if err := x.ask("Discard " + what + "? The files go back to the last commit (new files are removed); the changes are kept in git stash."); err != nil {
			return err
		}
	}
	d, err := x.app.Workspace.Discard(gd, paths, all)
	if err != nil || x.app.DryRun() {
		return err
	}
	x.sayf("Discarded the changes in %s.\n", files(d.Files))
	x.sayf("  They are kept as the stash %q: git stash list shows it, git stash apply brings it back.\n", d.Stash)
	return nil
}

func files(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	return fmt.Sprintf("%d files", len(paths))
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}
