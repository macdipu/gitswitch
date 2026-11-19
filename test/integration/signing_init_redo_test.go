package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realKey makes an unencrypted ed25519 key pair with ssh-keygen (git signs
// with it without an agent) and returns the private key path.
func (e *env) realKey(name string) string {
	e.t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		e.t.Skip("ssh-keygen not installed")
	}
	p := filepath.Join(e.home, ".ssh", name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name+"@test", "-f", p).CombinedOutput(); err != nil {
		e.t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	return p
}

// An account's signing key: set with account edit, written into a repo when
// it uses the account (with an allowed signers file), commits are signed and
// verify, a repo's own signing setup is left alone, and off removes ours.
func TestAccountSigning(t *testing.T) {
	e := newEnv(t)
	key := e.realKey("sign_me")
	pub, _ := os.ReadFile(key + ".pub")
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")

	out := e.gs("account", "edit", "me", "--signing-key", key)
	if !strings.Contains(out, "PUBLIC key") || !strings.Contains(out, "Signing Key") || !strings.Contains(out, strings.Fields(string(pub))[1]) {
		t.Fatalf("account edit must say to add the public key as a signing key:\n%s", out)
	}
	if strings.Contains(out, "PRIVATE KEY") {
		t.Fatal("private key material printed")
	}
	if l := e.gs("account", "list"); !strings.Contains(l, "signs commits: ssh") {
		t.Fatalf("account list:\n%s", l)
	}
	if e.gs("account", "edit", "me", "--name", "Dipu Me"); !strings.Contains(e.gs("account", "list"), "signs commits: ssh") {
		t.Fatal("editing the name dropped the signing")
	}

	e.gsIn(notes, "use", "me")
	for k, want := range map[string]string{"commit.gpgsign": "true", "gpg.format": "ssh", "user.signingkey": filepath.ToSlash(key), "gitswitch.signing": "true"} {
		if got := e.cfg(notes, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	signers := e.cfg(notes, "gpg.ssh.allowedSignersFile")
	b, err := os.ReadFile(signers)
	if err != nil || !strings.HasPrefix(string(b), `dipu@me.example namespaces="git" ssh-ed25519 `) {
		t.Fatalf("allowed signers %q: %q %v", signers, b, err)
	}

	must(t, os.WriteFile(filepath.Join(notes, "n.txt"), []byte("n\n"), 0o644))
	e.gsIn(notes, "commit", "-m", "Signed note", "--all")
	if got := e.gitIn(notes, "log", "-1", "--format=%G?|%GS"); got != "G|dipu@me.example" {
		t.Fatalf("signature = %q", got)
	}
	if out := e.gitIn(notes, "log", "-1", "--show-signature"); !strings.Contains(out, "Good \"git\" signature") {
		t.Fatalf("--show-signature:\n%s", out)
	}

	// A repo with its own signing setup keeps it.
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.gitIn(api, "config", "user.signingkey", "MYGPGKEY")
	e.gsIn(api, "use", "me")
	if got := e.cfg(api, "user.signingkey"); got != "MYGPGKEY" {
		t.Errorf("a repo's own key was replaced: %q", got)
	}
	if got := e.cfg(api, "gitswitch.signing"); got != "" {
		t.Errorf("flag set on a repo with its own signing: %q", got)
	}

	// Another account without signing: ours is removed.
	e.gsIn(notes, "use", "work")
	for _, k := range []string{"commit.gpgsign", "gpg.format", "user.signingkey", "gpg.ssh.allowedSignersFile", "gitswitch.signing"} {
		if got := e.cfg(notes, k); got != "" {
			t.Errorf("after switching to an account that does not sign, %s = %q", k, got)
		}
	}
	e.gsIn(notes, "use", "me")
	e.gs("account", "edit", "me", "--signing-format", "off")
	e.gsIn(notes, "use", "me")
	if got := e.cfg(notes, "commit.gpgsign"); got != "" {
		t.Errorf("signing off: commit.gpgsign = %q", got)
	}
	if l := e.gs("account", "list"); strings.Contains(l, "signs commits") {
		t.Fatalf("account list after off:\n%s", l)
	}
}

// Without a key of its own, signing refuses with the next step; a missing
// key file is a doctor warning, and a lost allowed signers file is fixed.
func TestSigningDoctor(t *testing.T) {
	e := newEnv(t)
	e.gsErr(e.root, "needs a key", "account", "edit", "me", "--signing-key", "", "--signing-format", "gpg")
	e.gs("account", "edit", "me", "--signing-key", "/nonexistent/key.pub")
	var cs []struct {
		ID     string
		OK     bool
		Detail string
		CanFix bool
	}
	check := func() (ok bool, detail string, fix bool) {
		cs = nil
		must(t, json.Unmarshal([]byte(e.gs("doctor", "--json")), &cs))
		for _, c := range cs {
			if c.ID == "signing-me" {
				return c.OK, c.Detail, c.CanFix
			}
		}
		t.Fatal("no signing-me check")
		return
	}
	if ok, d, fix := check(); ok || fix || !strings.Contains(d, "cannot read the public signing key") {
		t.Fatalf("missing key: ok=%v fix=%v %q", ok, fix, d)
	}
	key := e.realKey("sign_me")
	e.gs("account", "edit", "me", "--signing-key", key)
	if ok, d, _ := check(); !ok {
		t.Fatalf("with a key: %q", d)
	}
	// The allowed signers file is lost: doctor --fix writes it again.
	f := filepath.Join(e.home, ".config", "gitswitch", "signers", "me.allowed")
	if _, err := os.Stat(f); err != nil {
		t.Skipf("signers file is elsewhere on this OS: %v", err)
	}
	must(t, os.Remove(f))
	if ok, _, fix := check(); ok || !fix {
		t.Fatalf("lost file: ok=%v fix=%v", ok, fix)
	}
	e.gs("doctor", "--fix")
	if ok, d, _ := check(); !ok {
		t.Fatalf("after --fix: %q", d)
	}
}

// gitswitch init: a folder made, git init, the account from the rules or
// the flag, identity written, and the repo in the list.
func TestInitStartsARepo(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "folder:"+filepath.Join(e.root, "clientwork"), "client")
	dir := filepath.Join(e.root, "clientwork", "shop")
	out := e.gs("init", dir, "-b", "trunk")
	if !strings.Contains(out, "client") {
		t.Fatalf("init output:\n%s", out)
	}
	if got := e.gitIn(dir, "symbolic-ref", "--short", "HEAD"); got != "trunk" {
		t.Errorf("branch = %q", got)
	}
	if got := e.cfg(dir, "user.email"); got != "dipu@client.example" {
		t.Errorf("email = %q", got)
	}
	if got := e.cfg(dir, "gitswitch.account"); got != "client" {
		t.Errorf("saved account = %q", got)
	}
	if l := e.gs("repo", "list"); !strings.Contains(l, "shop") {
		t.Errorf("repo list:\n%s", l)
	}
	e.gsErr(e.root, "already", "init", dir)

	// The flag wins; the default account decides otherwise (work).
	other := filepath.Join(e.root, "other")
	e.gs("init", other, "--account", "me", "--json")
	if got := e.cfg(other, "user.email"); got != "dipu@me.example" {
		t.Errorf("flag: email = %q", got)
	}
	third := filepath.Join(e.root, "third")
	e.gs("init", third)
	if got := e.cfg(third, "user.email"); got != "dipu@work.example" {
		t.Errorf("default: email = %q", got)
	}
	// Several accounts, no default, no rule: asks (here: an error that says how).
	e.gs("default", "--none")
	e.gsErr(e.root, "--account", "init", filepath.Join(e.root, "fourth"))
	if _, err := os.Stat(filepath.Join(e.root, "fourth")); !os.IsNotExist(err) {
		t.Error("nothing may be created when the account is not chosen")
	}
	e.gsErr(e.root, "no account", "init", filepath.Join(e.root, "fifth"), "--account", "ghost")
}

// Redo after Undo, with real git: a commit, a branch delete, a rename and
// a drop come back, and redo refuses (changing nothing) when the branch
// moved on.
func TestRedoAfterUndo(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	head := func() string { return e.gitIn(api, "rev-parse", "HEAD") }
	e.gsErr(api, "nothing to redo", "redo", "-y")

	// Commit: undo keeps the files; redo puts the commit back.
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("a\n"), 0o644))
	e.gsIn(api, "commit", "-m", "Add a", "--all")
	c := head()
	e.gsIn(api, "undo", "-y")
	if head() == c || e.gitIn(api, "status", "--porcelain") != "?? a.txt" {
		t.Fatalf("undo commit: %s %q", head(), e.gitIn(api, "status", "--porcelain"))
	}
	var show struct {
		Action, Effect, Blocked string
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "redo", "--show", "--json")), &show))
	if show.Action != "commit" || show.Blocked != "" || show.Effect == "" {
		t.Fatalf("redo --show: %+v", show)
	}
	e.gsIn(api, "redo", "-y")
	if head() != c || e.gitIn(api, "status", "--porcelain") != "" {
		t.Fatalf("redo commit: HEAD %s want %s, status %q", head(), c, e.gitIn(api, "status", "--porcelain"))
	}
	e.gsErr(api, "nothing to redo", "redo", "-y") // used up
	e.gsIn(api, "undo", "-y")                     // and undoable again
	if head() == c {
		t.Fatal("undo after redo")
	}
	e.gsIn(api, "redo", "-y")

	// A new commit after an undo: redo is refused and changes nothing.
	e.gsIn(api, "undo", "-y")
	must(t, os.WriteFile(filepath.Join(api, "b.txt"), []byte("b\n"), 0o644))
	e.gitIn(api, "add", "b.txt")
	e.gitIn(api, "commit", "-qm", "other work")
	mine := head()
	e.gsErr(api, "can't be redone", "redo", "-y")
	if head() != mine {
		t.Fatal("a refused redo moved the branch")
	}
	e.gitIn(api, "reset", "-q", "--hard", c) // back to the redone state for the rest

	// Branch delete and rename.
	e.gitIn(api, "branch", "feature")
	e.gsIn(api, "branch", "delete", "feature", "-y", "--force")
	e.gsIn(api, "undo", "-y")
	if e.gitIn(api, "branch", "--list", "feature") == "" {
		t.Fatal("undo delete")
	}
	e.gsIn(api, "redo", "-y")
	if e.gitIn(api, "branch", "--list", "feature") != "" {
		t.Fatal("redo delete: the branch is still there")
	}
	e.gitIn(api, "branch", "feat2")
	e.gsIn(api, "branch", "rename", "feat2", "feat3")
	e.gsIn(api, "undo", "-y")
	e.gsIn(api, "redo", "-y")
	if e.gitIn(api, "branch", "--list", "feat3") == "" || e.gitIn(api, "branch", "--list", "feat2") != "" {
		t.Fatalf("redo rename:\n%s", e.gitIn(api, "branch"))
	}

	// Drop: redo drops again, and the commit stays reachable for undo.
	must(t, os.WriteFile(filepath.Join(api, "d.txt"), []byte("d\n"), 0o644))
	e.gsIn(api, "commit", "-m", "Add d", "--all")
	d := head()
	e.gsIn(api, "drop", d[:7], "-y")
	e.gsIn(api, "undo", "-y")
	if head() != d {
		t.Fatal("undo drop")
	}
	e.gsIn(api, "redo", "-y")
	if head() == d {
		t.Fatal("redo drop: the commit is still on the branch")
	}
	e.gsIn(api, "undo", "-y")
	if head() != d {
		t.Fatal("undo after the redone drop")
	}

	// A stash save cannot be redone: it says why.
	must(t, os.WriteFile(filepath.Join(api, "e.txt"), []byte("e\n"), 0o644))
	e.gsIn(api, "stash", "save", "-m", "wip")
	e.gsIn(api, "undo", "-y")
	e.gsErr(api, "not remembered", "redo", "-y")
}
