package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/macdipu/gitswitch/internal/repo"
)

const repoUsage = "gitswitch repo list [--pinned] [--json] | add [path] | remove [path] | show [path] [--json] | open [path] [--editor] | pin [path] | unpin [path]"

// cmdRepo manages the repo list. Paths default to the current folder.
func (x *CLI) cmdRepo(args []string) error {
	if len(args) == 0 {
		return usageErr(repoUsage)
	}
	sub, rest := args[0], args[1:]
	asJSON := hasFlag(rest, "--json")
	path := "."
	for _, a := range rest {
		if !strings.HasPrefix(a, "--") {
			path = a
			break
		}
	}
	switch sub {
	case "list", "ls":
		var list []repo.Info
		for _, r := range x.app.Repos() {
			if r.Pinned || !hasFlag(rest, "--pinned") {
				list = append(list, r)
			}
		}
		if asJSON {
			if list == nil {
				list = []repo.Info{}
			}
			return json.NewEncoder(x.out).Encode(list)
		}
		if len(list) == 0 {
			x.say("No repos yet. Add one with `gitswitch repo add <path>`, or a project folder with the apps.")
			return nil
		}
		for _, r := range list {
			x.sayf("%s %-48s %-20s %s\n", repoMarks(r), r.Path, orDash(r.Branch), repoAccount(r))
		}
		return nil
	case "add":
		r, err := x.app.AddRepo(path)
		if err != nil {
			return err
		}
		x.sayf("Added %s (%s)\n", r.Path, repoAccount(r))
		return nil
	case "remove", "rm":
		if err := x.app.RemoveRepo(path); err != nil {
			return err
		}
		x.say("Removed from the list. The folder and its git settings were not changed.")
		return nil
	case "pin", "unpin":
		return x.app.PinRepo(path, sub == "pin")
	case "open":
		return x.app.OpenRepo(path, hasFlag(rest, "--editor"))
	case "show", "info":
		gd, err := x.app.RepoFor(path)
		if err != nil {
			return err
		}
		r := x.app.RepoInfo(gd)
		if asJSON {
			return json.NewEncoder(x.out).Encode(r)
		}
		x.say("Folder:  ", r.Path)
		x.say("Git dir: ", r.GitDir)
		x.say("Branch:  ", orNone(r.Branch))
		x.say("Account: ", repoAccount(r))
		x.say("Email:   ", orNone(r.Email))
		if len(r.Remotes) == 0 {
			x.say("Remotes:  (none)")
		}
		for i, rm := range r.Remotes {
			label := "         "
			if i == 0 {
				label = "Remotes: "
			}
			x.sayf("%s %-8s %s\n", label, rm.Name, rm.URL)
		}
		listed := "found in a project folder"
		if r.Added {
			listed = "added by hand"
		}
		if !x.inList(r.Path) {
			listed = "not in the list (gitswitch repo add)"
		}
		x.say("List:    ", listed+map[bool]string{true: ", pinned"}[r.Pinned])
		if r.EmailBad {
			x.say("warning: repo email does not match the account; run `gitswitch use", r.Account+"`")
		}
		return nil
	}
	return fmt.Errorf("unknown repo command %q\nusage: %s", sub, repoUsage)
}

func (x *CLI) inList(path string) bool {
	for _, r := range x.app.Repos() {
		if r.Path == path {
			return true
		}
	}
	return false
}

// repoMarks is a two-letter status column: * pinned, + added by hand, ! missing.
func repoMarks(r repo.Info) string {
	m := []byte("  ")
	if r.Pinned {
		m[0] = '*'
	}
	if r.Missing {
		m[1] = '!'
	} else if r.Added {
		m[1] = '+'
	}
	return string(m)
}

func repoAccount(r repo.Info) string {
	switch {
	case r.Missing:
		return "folder not found (remove with: gitswitch repo remove " + r.Path + ")"
	case r.Account != "":
		return r.Account + " (" + r.Reason + ")"
	case r.Reason != "":
		return "asks on next pull or push"
	}
	return "no account for this host"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
