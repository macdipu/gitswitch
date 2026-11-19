package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A linked worktree is the same repo: it resolves the same account as the
// main folder, uses its key for network commands, and `use` in it changes
// the one shared setting. Add, list, remove, lock and prune work end to end.
func TestWorktreesShareTheReposAccount(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	fix := filepath.Join(e.root, "scratch", "api-fix")
	must(t, os.MkdirAll(filepath.Dir(fix), 0o755))

	out := e.gsIn(api, "worktree", "add", fix, "-b", "fix", "--register")
	if !strings.Contains(out, "uses the account client") || !strings.Contains(out, "repo list") {
		t.Fatalf("add:\n%s", out)
	}
	e.gsErr(api, "already checked out", "worktree", "add", filepath.Join(e.root, "scratch", "again"), "fix")
	e.gsErr(api, "inside this repo's own folder", "worktree", "add", filepath.Join(api, "inner"), "-b", "inner")

	// the same account, identity and key inside the worktree
	if got := e.cfg(fix, "user.email"); got != "dipu@client.example" {
		t.Fatalf("email in worktree = %q", got)
	}
	if st := e.gsIn(fix, "status"); !strings.Contains(st, "client") {
		t.Fatalf("status in worktree does not name the account:\n%s", st)
	}
	e.resetLog()
	e.gitIn(fix, "fetch", "-q")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "client" {
		t.Fatalf("keys used in worktree %v, want [client]", k)
	}
	if r, ok := e.repoList()["api-fix"]; !ok || !r.Added || r.Branch != "fix" {
		t.Fatalf("the registered worktree is not in the repo list: %+v", r)
	}

	// use in the worktree saves the account for the repo (shared config)
	e.gsIn(fix, "use", "me")
	if got := e.cfg(api, "gitswitch.account"); got != "me" {
		t.Fatalf("main repo account after use in worktree = %q, want me", got)
	}
	e.resetLog()
	e.gitIn(api, "fetch", "-q")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "me" {
		t.Fatalf("keys used in main %v, want [me]", k)
	}
	e.gsIn(fix, "forget")
	// use --once changes only identity (shared by every worktree), no saved account
	e.gsIn(fix, "use", "work", "--once")
	if got := e.cfg(api, "gitswitch.account"); got != "" {
		t.Fatalf("use --once saved an account: %q", got)
	}
	if got := e.cfg(api, "core.sshCommand"); strings.Contains(got, "-i ") {
		t.Fatalf("use --once broke the main repo's ssh setup: %q", got)
	}

	// list, json
	var all []struct {
		Path    string `json:"path"`
		Branch  string `json:"branch"`
		Main    bool   `json:"main"`
		Account string `json:"account"`
	}
	if err := json.Unmarshal([]byte(e.gsIn(fix, "worktree", "--json")), &all); err != nil || len(all) != 2 || !all[0].Main || all[1].Branch != "fix" || all[0].Account == "" || all[0].Account != all[1].Account {
		t.Fatalf("list: %v %+v", err, all)
	}

	// dirty: refused; lock: refused until unlocked; then removed, branch kept
	must(t, os.WriteFile(filepath.Join(fix, "wip.txt"), []byte("x"), 0o644))
	e.gsErr(api, "would be lost", "worktree", "remove", fix, "-y")
	e.gsErr(fix, "inside that worktree", "worktree", "remove", fix, "-y", "--force")
	e.gsIn(api, "worktree", "lock", "fix", "--reason", "usb")
	e.gsErr(api, "unlock it first", "worktree", "remove", "fix", "-y", "--force")
	e.gsIn(api, "worktree", "unlock", "fix")
	e.gsIn(api, "worktree", "remove", "fix", "--force", "-y")
	if _, err := os.Stat(fix); err == nil {
		t.Fatal("folder still there")
	}
	if !strings.Contains(e.gitIn(api, "branch", "--list", "fix"), "fix") {
		t.Fatal("branch fix was deleted")
	}

	// prune forgets a worktree whose folder was deleted by hand
	gone := filepath.Join(e.root, "scratch", "gone")
	e.gsIn(api, "worktree", "add", gone, "-b", "gone")
	must(t, os.RemoveAll(gone))
	if out := e.gsIn(api, "worktree", "prune", "-y"); !strings.Contains(out, "Forgot 1") {
		t.Fatalf("prune:\n%s", out)
	}
	if out := e.gsIn(api, "worktree", "prune", "-y"); !strings.Contains(out, "Nothing to prune") {
		t.Fatalf("prune again:\n%s", out)
	}
}
