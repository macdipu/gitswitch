package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

const repoUsage = "gitswitch repo list [--pinned] [--by none|account|owner|folder|group] [--sort folder|name|recent|account] [--json] | clone <url> [folder] [--account <id>] | add [path] | remove [path] | show [path] [--json] | open [path] [--editor] | pin [path] | unpin [path] | recent [--json] | group <name> [path] | ungroup [path]"

// cmdRepo manages the repo list. Paths default to the current folder.
func (x *CLI) cmdRepo(args []string) error {
	if len(args) == 0 {
		return usageErr(repoUsage)
	}
	sub, rest := args[0], args[1:]
	asJSON := hasFlag(rest, "--json")
	var pos []string // arguments that are not options or option values
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--by" || a == "--sort":
			i++
		case !strings.HasPrefix(a, "--"):
			pos = append(pos, a)
		}
	}
	path := "."
	if len(pos) > 0 {
		path = pos[0]
	}
	switch sub {
	case "clone":
		return x.cmdClone(rest)
	case "list", "ls":
		by := flagValue(rest, "--by")
		groups, err := x.app.Repos.Groups(by, flagValue(rest, "--sort"))
		if err != nil {
			return err
		}
		n := 0
		for i := range groups {
			var keep []repo.Info
			for _, r := range groups[i].Repos {
				if r.Pinned || !hasFlag(rest, "--pinned") {
					keep = append(keep, r)
				}
			}
			groups[i].Repos, n = keep, n+len(keep)
		}
		if asJSON {
			if by != "" { // grouped on request; without --by the flat list scripts already read
				out := []repo.Group{}
				for _, g := range groups {
					if len(g.Repos) > 0 {
						out = append(out, g)
					}
				}
				return json.NewEncoder(x.out).Encode(out)
			}
			list := []repo.Info{}
			for _, g := range groups {
				list = append(list, g.Repos...)
			}
			return json.NewEncoder(x.out).Encode(list)
		}
		if n == 0 {
			x.say("No repos yet. Add one with `gitswitch repo add <path>`, or a project folder with the apps.")
			return nil
		}
		for i, g := range groups {
			if len(g.Repos) == 0 {
				continue
			}
			if g.Name != "" {
				if i > 0 {
					x.say()
				}
				x.sayf("%s (%d)\n", g.Name, len(g.Repos))
			}
			for _, r := range g.Repos {
				x.sayf("%s %-48s %-20s %s%s\n", repoMarks(r), r.Path, orDash(r.Branch), repoAccount(r), cloneNote(r))
			}
		}
		return nil
	case "group":
		if len(pos) < 1 || len(pos) > 2 {
			return usageErr("gitswitch repo group <name> [path]")
		}
		path = "."
		if len(pos) == 2 {
			path = pos[1]
		}
		if err := x.app.Repos.SetGroup(path, pos[0]); err != nil {
			return err
		}
		x.sayf("In group %s. See the groups with: gitswitch repo list --by group\n", strings.TrimSpace(pos[0]))
		return nil
	case "ungroup":
		return x.app.Repos.SetGroup(path, "")
	case "add":
		r, err := x.app.Repos.Add(path)
		if err != nil {
			return err
		}
		x.sayf("Added %s (%s)\n", r.Path, repoAccount(r))
		return nil
	case "remove", "rm":
		if err := x.app.Repos.Remove(path); err != nil {
			return err
		}
		x.say("Removed from the list. The folder and its git settings were not changed.")
		return nil
	case "pin", "unpin":
		return x.app.Repos.Pin(path, sub == "pin")
	case "recent":
		list := x.app.Repos.Recent()
		if asJSON {
			return json.NewEncoder(x.out).Encode(list)
		}
		if len(list) == 0 {
			x.say("No recent repos yet. Repos you open with `gitswitch repo open` or in the apps show up here.")
			return nil
		}
		for _, r := range list {
			x.sayf("%s %-48s %-20s %s\n", repoMarks(r), r.Path, orDash(r.Branch), repoAccount(r))
		}
		return nil
	case "open":
		return x.app.Repos.Open(path, hasFlag(rest, "--editor"))
	case "show", "info":
		gd, err := x.app.Repos.For(path)
		if err != nil {
			return err
		}
		r := x.app.Repos.Info(gd)
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
		if r.Group != "" {
			x.say("Group:   ", r.Group)
		}
		for i, c := range x.clonesOf(r) {
			x.say(map[bool]string{true: "Clones:  ", false: "         "}[i == 0], c)
		}
		if r.EmailBad {
			x.say("warning: repo email does not match the account; run `gitswitch use", r.Account+"`")
		}
		return nil
	}
	return fmt.Errorf("unknown repo command %q\nusage: %s", sub, repoUsage)
}

// cmdClone clones a repo by URL, like `git clone`, with the account chosen
// from the start. Without a folder it clones into ./<repo name>.
func (x *CLI) cmdClone(args []string) error {
	var pos []string
	accountID := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--account" && i+1 < len(args):
			accountID = args[i+1]
			i++
		case strings.HasPrefix(a, "--account="):
			accountID = strings.TrimPrefix(a, "--account=")
		case strings.HasPrefix(a, "--"):
			return fmt.Errorf("unknown option %s\nusage: gitswitch repo clone <url> [folder] [--account <id>]", a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErr("gitswitch repo clone <url> [folder] [--account <id>]")
	}
	dir := remote.RepoName(pos[0])
	if len(pos) == 2 {
		dir = pos[1]
	}
	r, err := x.app.Repos.Clone(pos[0], dir, accountID, func(l string) { x.say(" ", l) })
	if err != nil {
		return err
	}
	if r.GitDir == "" { // dry run
		return nil
	}
	x.sayf("\nCloned into %s (%s). It is on your repo list.\n", r.Path, repoAccount(r))
	return nil
}

func (x *CLI) inList(path string) bool {
	for _, r := range x.app.Repos.List() {
		if r.Path == path {
			return true
		}
	}
	return false
}

// clonesOf lists the other listed clones of a repo's origin.
func (x *CLI) clonesOf(r repo.Info) []string {
	for _, l := range x.app.Repos.List() {
		if l.Path == r.Path {
			return l.Clones
		}
	}
	return nil
}

// cloneNote says where else a repo is cloned, for the repo list.
func cloneNote(r repo.Info) string {
	switch len(r.Clones) {
	case 0:
		return ""
	case 1:
		return "  · also cloned at " + r.Clones[0]
	}
	return fmt.Sprintf("  · also cloned at %s and %d more", r.Clones[0], len(r.Clones)-1)
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
