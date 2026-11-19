package infrastructure

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// remoteRepo is a stashRepo with a bare repo as its "origin", pushed to.
type remoteRepo struct {
	*stashRepo
	bare string
}

func (r *remoteRepo) bareGit(args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", append([]string{"--git-dir=" + r.bare}, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v in the remote: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRemoteRepo(t *testing.T) *remoteRepo {
	t.Helper()
	r := &remoteRepo{stashRepo: newStashRepo(t), bare: filepath.Join(t.TempDir(), "srv.git")}
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", r.bare).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := r.g.AddRemote(r.gd, "origin", r.bare); err != nil {
		t.Fatal(err)
	}
	r.run("push", "-q", "-u", "origin", "main")
	return r
}

// other is a second clone of the remote, to push from "someone else".
func (r *remoteRepo) other() func(args ...string) string {
	dir := filepath.Join(r.t.TempDir(), "other")
	if out, err := exec.Command("git", "clone", "-q", r.bare, dir).CombinedOutput(); err != nil {
		r.t.Fatalf("%v\n%s", err, out)
	}
	return func(args ...string) string {
		r.t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Other", "-c", "user.email=o@x.example", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			r.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

func TestRemotesAddRenameRemove(t *testing.T) {
	r := newRemoteRepo(t)
	cs, err := r.g.Remotes(r.gd)
	if err != nil || len(cs) != 1 || cs[0].Name != "origin" || cs[0].FetchURL != r.bare || cs[0].PushURL != r.bare {
		t.Fatalf("remotes: %+v %v", cs, err)
	}
	if err := r.g.AddRemote(r.gd, "fork", "git@github.com:me/x.git"); err != nil {
		t.Fatal(err)
	}
	if err := r.g.AddRemote(r.gd, "fork", "git@github.com:me/y.git"); err == nil {
		t.Error("a duplicate name was accepted")
	}
	r.run("config", "remote.fork.pushurl", "git@github.com:me/push.git")
	cs, _ = r.g.Remotes(r.gd)
	if len(cs) != 2 || cs[0].FetchURL != "git@github.com:me/x.git" || cs[0].PushURL != "git@github.com:me/push.git" {
		t.Errorf("push url: %+v", cs)
	}
	if err := r.g.SetRemoteURL(r.gd, "fork", "git@github.com:me/z.git"); err != nil {
		t.Fatal(err)
	}
	if cs, _ = r.g.Remotes(r.gd); cs[0].FetchURL != "git@github.com:me/z.git" {
		t.Errorf("set-url: %+v", cs)
	}

	// main tracks origin; a rename keeps it tracking, under the new name.
	if got := r.g.TrackingBranches(r.gd, "origin"); strings.Join(got, ",") != "main" {
		t.Fatalf("tracking: %v", got)
	}
	if err := r.g.RenameRemote(r.gd, "origin", "srv"); err != nil {
		t.Fatal(err)
	}
	if got := r.g.TrackingBranches(r.gd, "srv"); strings.Join(got, ",") != "main" || len(r.g.TrackingBranches(r.gd, "origin")) != 0 {
		t.Errorf("tracking after rename: %v", got)
	}
	if up := r.run("rev-parse", "--abbrev-ref", "main@{upstream}"); up != "srv/main" {
		t.Errorf("upstream = %q", up)
	}
	h, _ := r.g.Head(r.gd)
	if h.Upstream != "srv/main" {
		t.Errorf("head upstream %q", h.Upstream)
	}
	// Removing it takes the upstream and the remote-tracking branches along.
	if err := r.g.RemoveRemote(r.gd, "srv"); err != nil {
		t.Fatal(err)
	}
	if len(r.g.TrackingBranches(r.gd, "srv")) != 0 {
		t.Error("main still tracks the removed remote")
	}
	if out := r.run("for-each-ref", "refs/remotes/srv"); out != "" {
		t.Errorf("remote-tracking branches left: %s", out)
	}
	if h, _ := r.g.Head(r.gd); h.Upstream != "" {
		t.Errorf("upstream after remove: %q", h.Upstream)
	}
}

func TestDeleteRemoteBranchAndPrune(t *testing.T) {
	r := newRemoteRepo(t)
	progress := func(string) {}
	r.run("branch", "feature", "main")
	r.run("push", "-q", "origin", "feature")
	r.run("branch", "stale", "main")
	r.run("push", "-q", "origin", "stale")
	// A tag with the same name as the branch is never what is deleted.
	r.run("tag", "feature")
	r.run("push", "-q", "origin", "refs/tags/feature")

	if err := r.g.DeleteRemoteBranch(r.dir, "origin", "feature", progress); err != nil {
		t.Fatal(err)
	}
	if out := r.bareGit("for-each-ref", "refs/heads/feature", "refs/tags/feature"); strings.Contains(out, "refs/heads/feature") || !strings.Contains(out, "refs/tags/feature") {
		t.Errorf("the remote has: %s", out)
	}
	if out := r.run("for-each-ref", "refs/remotes/origin/feature"); out != "" {
		t.Errorf("the local copy of the remote branch is still here: %s", out)
	}
	// Someone else deletes "stale" on the server: a plain fetch keeps the
	// local copy, a pruning fetch forgets it. Local branches are never touched.
	r.bareGit("update-ref", "-d", "refs/heads/stale")
	if err := r.g.Fetch(r.dir, progress); err != nil {
		t.Fatal(err)
	}
	if out := r.run("for-each-ref", "refs/remotes/origin/stale"); out == "" {
		t.Fatal("a plain fetch pruned")
	}
	if err := r.g.FetchPrune(r.dir, progress); err != nil {
		t.Fatal(err)
	}
	if out := r.run("for-each-ref", "refs/remotes/origin/stale"); out != "" {
		t.Errorf("not pruned: %s", out)
	}
	if out := r.run("for-each-ref", "refs/heads/stale"); out == "" {
		t.Error("the local branch went")
	}
}

func TestForcePushWithLease(t *testing.T) {
	r := newRemoteRepo(t)
	other := r.other()
	progress := func(string) {}
	tip := func() string { return r.run("rev-parse", "origin/main") }

	// Rewrite the last commit: a plain push is refused, the lease goes through.
	r.run("commit", "-q", "--amend", "-m", "first, reworded")
	if err := r.g.Push(r.dir, "origin", "refs/heads/main", "refs/heads/main", false, progress); err == nil {
		t.Fatal("a plain push of rewritten history went through")
	}
	seen := tip()
	if err := r.g.ForcePush(r.dir, "origin", "refs/heads/main", "refs/heads/main", seen, progress); err != nil {
		t.Fatalf("lease push: %v", err)
	}
	if got, want := r.bareGit("rev-parse", "refs/heads/main"), r.run("rev-parse", "HEAD"); got != want {
		t.Errorf("the remote is at %s, want %s", got, want)
	}

	// Someone pushes after the last fetch: the lease fails and nothing is overwritten.
	other("pull", "-q", "--rebase", "origin", "main")
	if out := other("log", "-1", "--format=%s"); out != "first, reworded" {
		t.Fatalf("other is at %q", out)
	}
	other("commit", "-q", "--allow-empty", "-m", "theirs")
	other("push", "-q", "origin", "main")
	theirs := r.bareGit("rev-parse", "refs/heads/main")
	r.run("commit", "-q", "--amend", "-m", "first, reworded again")
	err := r.g.ForcePush(r.dir, "origin", "refs/heads/main", "refs/heads/main", seen, progress)
	if err == nil || !strings.Contains(err.Error(), "stale info") {
		t.Fatalf("lease failure: %v", err)
	}
	if got := r.bareGit("rev-parse", "refs/heads/main"); got != theirs {
		t.Errorf("the remote moved to %s: their commit was overwritten", got)
	}

	// With the right expectation (the value on the server) it goes.
	r.run("fetch", "-q")
	if err := r.g.ForcePush(r.dir, "origin", "refs/heads/main", "refs/heads/main", tip(), progress); err != nil {
		t.Fatalf("after fetching: %v", err)
	}
	if got, want := r.bareGit("rev-parse", "refs/heads/main"), r.run("rev-parse", "HEAD"); got != want {
		t.Errorf("remote at %s, want %s", got, want)
	}
}

func TestDetachAndWayBack(t *testing.T) {
	r := newRemoteRepo(t)
	r.write("c.txt", "c\n")
	r.run("add", ".")
	r.run("commit", "-q", "-m", "second")
	first := r.run("rev-parse", "HEAD~1")
	r.run("tag", "v1", "HEAD~1")

	if err := r.g.Detach(r.dir, first); err != nil {
		t.Fatal(err)
	}
	h, err := r.g.Head(r.gd)
	if err != nil || h.Branch != "" || h.Hash != first {
		t.Fatalf("head: %+v %v", h, err)
	}
	if r.read("c.txt") != "" {
		t.Error("the files are still the second commit's")
	}
	if err := r.g.SaveDetachedFrom(r.gd, "main"); err != nil || r.g.DetachedFrom(r.gd) != "main" {
		t.Fatalf("way back: %q %v", r.g.DetachedFrom(r.gd), err)
	}
	// Nothing of your own here yet, then one commit on no branch.
	if n := r.g.Orphans(r.gd); n != 0 {
		t.Errorf("orphans = %d", n)
	}
	r.write("d.txt", "d\n")
	r.run("add", ".")
	r.run("commit", "-q", "-m", "detached work")
	if n := r.g.Orphans(r.gd); n != 1 {
		t.Errorf("orphans = %d, want 1", n)
	}
	// A tag or a branch at it keeps it from being lost.
	r.run("tag", "kept")
	if n := r.g.Orphans(r.gd); n != 0 {
		t.Errorf("orphans with a tag = %d", n)
	}
	if err := r.g.Switch(r.dir, "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.g.SaveDetachedFrom(r.gd, ""); err != nil || r.g.DetachedFrom(r.gd) != "" {
		t.Errorf("forget: %q %v", r.g.DetachedFrom(r.gd), err)
	}
	if err := r.g.SaveDetachedFrom(r.gd, ""); err != nil {
		t.Errorf("forgetting what is not there: %v", err)
	}
	// Uncommitted changes that clash with the commit make git refuse, and say so.
	r.write("a.txt", "changed locally\n")
	if err := r.g.Detach(r.dir, "v1~0"); err != nil {
		// v1 is where a.txt is the same as main's: carried along, no refusal.
		t.Fatalf("carry: %v", err)
	}
}

func TestPruneSettingFiles(t *testing.T) {
	r := newRemoteRepo(t)
	if g, p := r.g.PruneModes(r.gd); g != "" || p != "" {
		t.Fatalf("default: %q %q", g, p)
	}
	if err := r.g.SavePruneMode("", "true"); err != nil {
		t.Fatal(err)
	}
	if err := r.g.SavePruneMode(r.gd, "false"); err != nil {
		t.Fatal(err)
	}
	if g, p := r.g.PruneModes(r.gd); g != "true" || p != "false" {
		t.Errorf("saved: %q %q", g, p)
	}
	if err := r.g.SavePruneMode(r.gd, ""); err != nil {
		t.Fatal(err)
	}
	if g, p := r.g.PruneModes(r.gd); g != "true" || p != "" {
		t.Errorf("repo cleared: %q %q", g, p)
	}
}
