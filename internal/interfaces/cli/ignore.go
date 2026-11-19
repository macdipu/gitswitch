package cli

import (
	"encoding/json"
	"strings"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// cmdIgnore adds a file, its extension or its folder to the repo's
// .gitignore, or with --local to .git/info/exclude. --untrack takes a file
// git already tracks out of git (the file stays), never by default.
func (x *CLI) cmdIgnore(args []string) error {
	const usage = "gitswitch ignore <path> [--ext|--folder] [--local] [--untrack] [--json]"
	kind := workspace.IgnoreFile
	var path string
	local, untrack, asJSON, kinds := false, false, false, 0
	for _, a := range args {
		switch {
		case a == "--ext":
			kind, kinds = workspace.IgnoreExt, kinds+1
		case a == "--folder" || a == "--dir":
			kind, kinds = workspace.IgnoreFolder, kinds+1
		case a == "--local":
			local = true
		case a == "--untrack":
			untrack = true
		case a == "--json":
			asJSON = true
		case path == "" && a != "" && !strings.HasPrefix(a, "-"):
			path = a
		default:
			return usageErr(usage)
		}
	}
	if path == "" || kinds > 1 {
		return usageErr(usage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	typed := path
	path = x.app.Workspace.RepoPath(gd, cwd(), path)
	if strings.HasSuffix(typed, "/") && !strings.HasSuffix(path, "/") {
		path += "/" // keep "this is a folder"
	}
	r, err := x.app.Workspace.Ignore(gd, path, kind, local, untrack)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(r)
	}
	if x.app.DryRun() {
		return nil
	}
	if r.Added {
		x.sayf("Added %s to %s.\n", r.Pattern, r.Where)
	} else {
		x.sayf("%s already has %s.\n", r.Where, r.Pattern)
	}
	if len(r.Untracked) > 0 {
		x.sayf("Took %s out of git; still in your folder. Commit to record it: when others pull, git deletes it from their folders.\n", files(r.Untracked))
	}
	switch {
	case len(r.Tracked) > 0:
		x.sayf("Git still tracks %s, and a tracked file is never ignored: git keeps following its changes.\n", files(r.Tracked))
		x.sayf("To stop tracking and keep the files: gitswitch ignore %s%s%s --untrack\n", typed, kindFlag(kind), localFlag(local))
	case r.Ignored:
		x.sayf("Git now ignores %s.\n", r.Path)
	default:
		x.sayf("Git still does not ignore %s: a later line starting with ! may bring it back.\n", r.Path)
	}
	if r.Added && !local {
		x.say("Commit .gitignore to share it; --local keeps a pattern to this clone.")
	}
	return nil
}

func kindFlag(k workspace.IgnoreKind) string {
	switch k {
	case workspace.IgnoreExt:
		return " --ext"
	case workspace.IgnoreFolder:
		return " --folder"
	}
	return ""
}

func localFlag(local bool) string {
	if local {
		return " --local"
	}
	return ""
}
