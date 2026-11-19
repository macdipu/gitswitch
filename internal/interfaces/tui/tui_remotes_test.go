package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// remoteRepo is the changes repo with everything committed, a bare origin
// with main pushed and tracked, and the plain runner (no leaving the
// screen).
func remoteRepo(t *testing.T) (*tui, string, string, func(...string) string) {
	t.Helper()
	x, dir, git := changesRepo(t)
	x.ext.plain = true
	git("add", ".")
	git("commit", "-q", "-m", "second")
	withRemote(t, git)
	git("branch", "--set-upstream-to=origin/main", "main")
	bare := git("remote", "get-url", "origin")
	x.loadHistory()
	return x, dir, bare, git
}

// menu opens O and picks the entry whose text contains pick.
func menu(t *testing.T, x *tui, pick string) {
	t.Helper()
	x.handle("O")
	if x.picker == nil {
		t.Fatalf("O: no menu (msg %q)", x.msg)
	}
	typeKeys(x, pick)
	x.handle("enter")
}

func bareGit(t *testing.T, bare string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", bare}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRemotesAddRenameSetURLRemove(t *testing.T) {
	x, _, _, git := remoteRepo(t)
	menu(t, x, "Remotes:")
	if x.picker == nil || !strings.Contains(strings.Join(x.picker.options, "\n"), "origin") {
		t.Fatalf("no remote list: %+v", x.picker)
	}
	typeKeys(x, "Add")
	x.handle("enter")
	if x.form == nil {
		t.Fatalf("no add form (msg %q)", x.msg)
	}
	typeKeys(x, "backup")
	x.handle("enter")
	typeKeys(x, "https://github.com/acme/app.git")
	x.handle("enter")
	if !strings.Contains(git("remote"), "backup") || x.msgErr {
		t.Fatalf("not added: %q, msg %q", git("remote"), x.msg)
	}
	// A bad URL is refused with the reason.
	menu(t, x, "Remotes:")
	typeKeys(x, "Add")
	x.handle("enter")
	typeKeys(x, "bad")
	x.handle("enter")
	typeKeys(x, "not a url")
	x.handle("enter")
	if !x.msgErr || strings.Contains(git("remote"), "bad") {
		t.Fatalf("a bad URL was added: %q (msg %q)", git("remote"), x.msg)
	}
	// Rename.
	menu(t, x, "Remotes:")
	typeKeys(x, "backup")
	x.handle("enter")
	if x.confirm == nil || x.confirm.actions["r"] == nil || x.confirm.actions["x"] == nil {
		t.Fatalf("no remote actions: %+v", x.confirm)
	}
	x.handle("r")
	x.handle("ctrl-u")
	typeKeys(x, "mirror")
	x.handle("enter")
	if r := git("remote"); !strings.Contains(r, "mirror") || strings.Contains(r, "backup") {
		t.Fatalf("not renamed: %q (msg %q)", r, x.msg)
	}
	// Change the URL.
	menu(t, x, "Remotes:")
	typeKeys(x, "mirror")
	x.handle("enter")
	x.handle("u")
	x.handle("ctrl-u")
	typeKeys(x, "https://github.com/acme/other.git")
	x.handle("enter")
	if u := git("remote", "get-url", "mirror"); u != "https://github.com/acme/other.git" {
		t.Fatalf("url = %q (msg %q)", u, x.msg)
	}
	// Remove: asks first.
	menu(t, x, "Remotes:")
	typeKeys(x, "mirror")
	x.handle("enter")
	x.handle("x")
	if x.confirm == nil {
		t.Fatal("remove did not ask")
	}
	x.handle("y")
	if strings.Contains(git("remote"), "mirror") {
		t.Fatalf("not removed (msg %q)", x.msg)
	}
}

// Removing a remote that branches track is refused, naming them; f goes on.
func TestRemoteRemoveForceNamesBranches(t *testing.T) {
	x, _, _, git := remoteRepo(t)
	menu(t, x, "Remotes:")
	typeKeys(x, "origin")
	x.handle("enter")
	x.handle("x")
	x.handle("y")
	if x.confirm == nil || x.confirm.actions["f"] == nil || !strings.Contains(x.confirm.text, "main") {
		t.Fatalf("no force question naming main: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if !strings.Contains(git("remote"), "origin") {
		t.Fatal("n removed it")
	}
	menu(t, x, "Remotes:")
	typeKeys(x, "origin")
	x.handle("enter")
	x.handle("x")
	x.handle("y")
	x.handle("f")
	if git("remote") != "" {
		t.Fatalf("not removed: %q (msg %q)", git("remote"), x.msg)
	}
}

func TestDeleteRemoteBranch(t *testing.T) {
	x, _, bare, git := remoteRepo(t)
	git("push", "-q", "origin", "main:topic")
	git("fetch", "-q", "origin")
	menu(t, x, "Delete a branch")
	if x.picker == nil {
		t.Fatalf("no remote branch list (msg %q)", x.msg)
	}
	typeKeys(x, "origin/topic")
	x.handle("enter")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "To put it back: git push origin") {
		t.Fatalf("no preview with the restore command: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if !strings.Contains(bareGit(t, bare, "branch"), "topic") {
		t.Fatal("cancelled delete deleted")
	}
	menu(t, x, "Delete a branch")
	typeKeys(x, "origin/topic")
	x.handle("enter")
	x.handle("y")
	if strings.Contains(bareGit(t, bare, "branch"), "topic") || x.msgErr {
		t.Fatalf("not deleted: %q (msg %q)", bareGit(t, bare, "branch"), x.msg)
	}
	// The default branch needs force.
	menu(t, x, "Delete a branch")
	typeKeys(x, "origin/main")
	x.handle("enter")
	if x.confirm == nil || x.confirm.actions["f"] == nil || !strings.Contains(x.confirm.text, "default branch") {
		t.Fatalf("no force question: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if !strings.Contains(bareGit(t, bare, "branch"), "main") {
		t.Fatal("main was deleted")
	}
}

func TestFetchPruneListsForgotten(t *testing.T) {
	x, _, bare, git := remoteRepo(t)
	git("push", "-q", "origin", "main:gone")
	git("fetch", "-q", "origin")
	bareGit(t, bare, "branch", "-D", "gone")
	menu(t, x, "Fetch and forget")
	if x.view == nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "origin/gone") {
		t.Fatalf("forgotten branches not listed: %+v (msg %q)", x.view, x.msg)
	}
	if strings.Contains(git("branch", "-r"), "origin/gone") {
		t.Fatal("origin/gone is still known")
	}
}

// diverge makes main one commit ahead of and one behind origin/main.
func diverge(t *testing.T, x *tui, git func(...string) string) {
	t.Helper()
	git("reset", "-q", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(git("rev-parse", "--show-toplevel"), "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "mine")
	x.loadHistory()
}

func TestForcePushWithLease(t *testing.T) {
	x, _, bare, git := remoteRepo(t)
	diverge(t, x, git)
	menu(t, x, "Force push")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "overwrite 1 commit") || !strings.Contains(x.confirm.text, "second") {
		t.Fatalf("no plan: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("l")
	if x.view == nil || !strings.Contains(strings.Join(x.view.lines, "\n"), "Would be overwritten") {
		t.Fatalf("no commit list: %+v", x.view)
	}
	x.handle("esc")
	// Anything but yes cancels.
	menu(t, x, "Force push")
	x.handle("y")
	if x.form == nil {
		t.Fatal("no typed confirmation")
	}
	typeKeys(x, "no")
	x.handle("enter")
	if bareGit(t, bare, "rev-parse", "main") == git("rev-parse", "HEAD") {
		t.Fatal("pushed without yes")
	}
	menu(t, x, "Force push")
	x.handle("y")
	typeKeys(x, "yes")
	x.handle("enter")
	if x.msgErr || bareGit(t, bare, "rev-parse", "main") != git("rev-parse", "HEAD") {
		t.Fatalf("not pushed: msg %q", x.msg)
	}
	if !strings.Contains(x.msg, "Force pushed 1 commit") {
		t.Errorf("msg = %q", x.msg)
	}
}

func TestForcePushStaleLeaseChangesNothing(t *testing.T) {
	x, _, bare, git := remoteRepo(t)
	diverge(t, x, git)
	menu(t, x, "Force push")
	x.handle("y") // the plan was read; now someone else pushes
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "clone", "-q", "-b", "main", bare, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	for _, a := range [][]string{{"commit", "-q", "--allow-empty", "-m", "theirs"}, {"push", "-q", "origin", "HEAD:main"}} {
		if out, err := exec.Command("git", append([]string{"-C", other}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	theirs := bareGit(t, bare, "rev-parse", "main")
	typeKeys(x, "yes")
	x.handle("enter")
	if !x.msgErr || !strings.Contains(x.msg, "someone pushed") || !strings.Contains(x.msg, "fetch") {
		t.Fatalf("no stale lease message: %q", x.msg)
	}
	if bareGit(t, bare, "rev-parse", "main") != theirs {
		t.Fatal("their commit was overwritten")
	}
}

func TestForcePushNeedsSomethingToOverwrite(t *testing.T) {
	x, _, _, _ := remoteRepo(t)
	menu(t, x, "Force push")
	if x.confirm != nil || !x.msgErr || !strings.Contains(x.msg, "normal push") {
		t.Fatalf("msg %q, confirm %+v", x.msg, x.confirm)
	}
}

func TestDetachedCheckoutBannerAndBack(t *testing.T) {
	x, _, _, git := remoteRepo(t)
	first := git("rev-parse", "--short", "HEAD~1")
	menu(t, x, "Check out a commit")
	if x.form == nil {
		t.Fatalf("no form (msg %q)", x.msg)
	}
	x.handle("ctrl-u")
	typeKeys(x, first)
	x.handle("enter")
	if git("branch", "--show-current") != "" || !x.ext.det.Detached {
		t.Fatalf("not detached (msg %q)", x.msg)
	}
	lines, _ := x.drawHistory()
	if s := history.StripANSI(strings.Join(lines, "\n")); !strings.Contains(s, "detached at "+first) || !strings.Contains(s, "E = Back to main") {
		t.Fatalf("no banner:\n%s", s)
	}
	// Commits made here are on no branch: Back warns first.
	git("commit", "-q", "--allow-empty", "-m", "lost?")
	x.loadHistory()
	x.handle("E")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "on no branch") || git("branch", "--show-current") != "" {
		t.Fatalf("no orphan warning: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if git("branch", "--show-current") != "" {
		t.Fatal("n went back")
	}
	x.handle("E")
	x.handle("f")
	if git("branch", "--show-current") != "main" || x.ext.det.Detached {
		t.Fatalf("not back (msg %q)", x.msg)
	}
}

// Back from a clean detached HEAD goes straight back; the menu offers it
// only while detached.
func TestBackFromMenu(t *testing.T) {
	x, _, _, git := remoteRepo(t)
	x.handle("O")
	if strings.Contains(strings.Join(x.picker.options, "\n"), "Back to") {
		t.Fatal("Back offered on a branch")
	}
	x.handle("esc")
	menu(t, x, "Check out a commit")
	x.handle("enter") // the selected commit is filled in
	if !x.ext.det.Detached {
		t.Fatalf("not detached (msg %q)", x.msg)
	}
	menu(t, x, "Back to")
	if git("branch", "--show-current") != "main" {
		t.Fatalf("not back (msg %q)", x.msg)
	}
}

// With uncommitted changes the checkout asks smart or park, like a switch.
func TestDetachedCheckoutAsksAboutChanges(t *testing.T) {
	x, dir, _, git := remoteRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	menu(t, x, "Check out a commit")
	x.handle("ctrl-u")
	typeKeys(x, git("rev-parse", "--short", "HEAD~1"))
	x.handle("enter")
	if x.confirm == nil || x.confirm.actions["s"] == nil || x.confirm.actions["p"] == nil {
		t.Fatalf("no smart or park question: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("p")
	if git("branch", "--show-current") != "" || git("stash", "list") == "" {
		t.Fatalf("changes not parked (msg %q)", x.msg)
	}
}
