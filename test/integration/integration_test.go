// Package integration runs the real gitswitch program with real git
// (clone, pull, push) against local repos through a fake ssh.
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test harness
//
// The test binary doubles as a fake ssh: with GITSWITCH_FAKE_SSH=1 it logs
// its arguments and runs git-upload-pack / git-receive-pack locally against
// a folder of bare repos, so real `git clone/push` runs through gitswitch
// without any network.
// ---------------------------------------------------------------------------

var gsBin string // the gitswitch program under test

func TestMain(m *testing.M) {
	if os.Getenv("GITSWITCH_FAKE_SSH") == "1" {
		os.Exit(fakeSSH(os.Args[1:]))
	}
	dir, err := os.MkdirTemp("", "gitswitch-bin")
	if err != nil {
		panic(err)
	}
	gsBin = filepath.Join(dir, "gitswitch"+exeSuffix())
	build := exec.Command("go", "build", "-o", gsBin, "github.com/macdipu/gitswitch/cmd/gitswitch")
	build.Dir = filepath.Join("..", "..")
	out, err := build.CombinedOutput()
	if err != nil {
		panic("build failed: " + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func fakeSSH(args []string) int {
	if f, err := os.OpenFile(os.Getenv("GS_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "-G" {
			fmt.Println("user git\nhostname github.com\nstricthostkeychecking ask")
			return 0
		}
		if strings.HasPrefix(a, "-") {
			if len(a) == 2 && strings.ContainsRune("iopFlJ", rune(a[1])) {
				i += 2
			} else {
				i++
			}
			continue
		}
		break
	}
	if i+1 >= len(args) {
		return 255
	}
	cmdline := strings.Join(args[i+1:], " ")
	verb, rest, _ := strings.Cut(cmdline, " ")
	path := strings.Trim(strings.TrimSpace(rest), "'")
	cmd := exec.Command("git", strings.TrimPrefix(verb, "git-"), filepath.Join(os.Getenv("GS_SERVER"), filepath.FromSlash(path)))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

type env struct {
	t      *testing.T
	root   string
	home   string
	server string
	log    string
	vars   []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root, home: filepath.Join(root, "home"), server: filepath.Join(root, "server"), log: filepath.Join(root, "ssh.log")}
	for _, d := range []string{e.home, e.server, filepath.Join(e.home, ".ssh"), filepath.Join(root, "appdata"), filepath.Join(root, "localappdata"), filepath.Join(root, "work")} {
		must(t, os.MkdirAll(d, 0o755))
	}
	self, _ := os.Executable()
	keep := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
			"GIT_SSH_COMMAND", "GIT_SSH", "GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_GLOBAL", "VSCODE_GIT_ASKPASS_MAIN", "TERM_PROGRAM":
			continue
		}
		keep = append(keep, kv)
	}
	e.vars = append(keep,
		"HOME="+e.home, "USERPROFILE="+e.home,
		"APPDATA="+filepath.Join(root, "appdata"), "LOCALAPPDATA="+filepath.Join(root, "localappdata"),
		"XDG_CONFIG_HOME="+filepath.Join(e.home, ".config"), "XDG_CACHE_HOME="+filepath.Join(e.home, ".cache"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GITSWITCH_TEST_NO_SYSTEM=1", "GITSWITCH_NO_PROMPT=1",
		"GITSWITCH_FAKE_SSH=1", "GS_LOG="+e.log, "GS_SERVER="+e.server,
	)
	e.git("config", "--global", "init.defaultBranch", "main")
	e.git("config", "--global", "user.name", "Dipu")
	e.git("config", "--global", "user.email", "old@global.example")
	e.gs("install")
	e.gs("set", "ssh-path", self)
	for _, id := range []string{"work", "client", "me"} {
		e.keyPair(id)
	}
	e.gs("account", "add", "work", "--email", "dipu@work.example", "--key", e.key("work"), "--default")
	e.gs("account", "add", "client", "--email", "dipu@client.example", "--key", e.key("client"))
	e.gs("account", "add", "me", "--email", "dipu@me.example", "--key", e.key("me"))
	for _, r := range []string{"acme-corp/api", "acme-corp/web", "dipu/notes"} {
		e.serverRepo(r)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) key(id string) string { return filepath.Join(e.home, ".ssh", "k_"+id) }

// keyPair writes a fake key pair with a unique public key body.
func (e *env) keyPair(id string) {
	must(e.t, os.WriteFile(e.key(id), []byte("private "+id), 0o600))
	must(e.t, os.WriteFile(e.key(id)+".pub", []byte("ssh-ed25519 AAAA"+id+" "+id+"@test\n"), 0o644))
}

func (e *env) serverRepo(path string) {
	bare := filepath.Join(e.server, filepath.FromSlash(path)+".git")
	must(e.t, os.MkdirAll(filepath.Dir(bare), 0o755))
	e.git("init", "-q", "--bare", "-b", "main", bare)
	seed := e.t.TempDir()
	e.gitIn(seed, "init", "-q", "-b", "main")
	e.gitIn(seed, "-c", "user.email=seed@other.example", "-c", "user.name=seed", "commit", "-q", "--allow-empty", "-m", "Initial "+path)
	e.gitIn(seed, "push", "-q", bare, "HEAD:refs/heads/main")
}

func (e *env) run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = e.vars
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *env) gitIn(dir string, args ...string) string {
	e.t.Helper()
	out, err := e.run(dir, "git", args...)
	if err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func (e *env) git(args ...string) string { e.t.Helper(); return e.gitIn(e.root, args...) }

// gitMay runs git and returns output even on failure.
func (e *env) gitMay(dir string, args ...string) (string, error) { return e.run(dir, "git", args...) }

func (e *env) gsIn(dir string, args ...string) string {
	e.t.Helper()
	out, err := e.run(dir, gsBin, args...)
	if err != nil {
		e.t.Fatalf("gitswitch %v: %v\n%s", args, err, out)
	}
	return out
}

func (e *env) gs(args ...string) string { e.t.Helper(); return e.gsIn(e.root, args...) }

func (e *env) cfg(dir, key string) string {
	out, _ := e.run(dir, "git", "config", key)
	return strings.TrimSpace(out)
}

func (e *env) resetLog() { _ = os.Remove(e.log) }

// keysUsed lists the account keys passed to ssh, in order (ignores -G probes).
func (e *env) keysUsed() []string {
	b, _ := os.ReadFile(e.log)
	var used []string
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" || strings.HasPrefix(l, "-G") {
			continue
		}
		f := strings.Fields(l)
		k := "none"
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "-i" {
				k = strings.TrimPrefix(filepath.Base(f[i+1]), "k_")
			}
		}
		used = append(used, k)
	}
	return used
}

func (e *env) clone(url, dir string) {
	e.t.Helper()
	e.git("clone", "-q", url, dir)
}

// ---------------------------------------------------------------------------
// Integration tests: real git through the real program
// ---------------------------------------------------------------------------

func TestInstallSetsHookAndUninstallRestores(t *testing.T) {
	e := newEnv(t)
	hook := e.cfg(e.root, "core.sshCommand")
	if !strings.Contains(hook, "gitswitch") || !strings.HasSuffix(hook, " ssh") {
		t.Fatalf("hook not set: %q", hook)
	}
	if got := e.cfg(e.root, "user.email"); got != "" {
		t.Fatalf("global email should be moved aside, got %q", got)
	}
	e.gs("uninstall")
	if got := e.cfg(e.root, "core.sshCommand"); got != "" {
		t.Fatalf("hook not removed: %q", got)
	}
	if got := e.cfg(e.root, "user.email"); got != "old@global.example" {
		t.Fatalf("global email not restored: %q", got)
	}
}

func TestOwnerRuleChoosesAccountAndEmail(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.resetLog()
	e.clone("git@github.com:acme-corp/api.git", "api")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "client" {
		t.Fatalf("keys used %v, want [client]", k)
	}
	if got := e.cfg(filepath.Join(e.root, "api"), "user.email"); got != "dipu@client.example" {
		t.Fatalf("email %q", got)
	}
}

func TestDefaultUsedWhenNobodyCanAnswer(t *testing.T) {
	e := newEnv(t)
	e.resetLog()
	e.clone("git@github.com:dipu/notes.git", "notes")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "work" {
		t.Fatalf("keys used %v, want [work]", k)
	}
}

func TestSavedAccountWinsOverRules(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	dir := filepath.Join(e.root, "api")
	e.gsIn(dir, "use", "me")
	e.resetLog()
	e.gitIn(dir, "pull", "-q")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "me" {
		t.Fatalf("keys used %v, want [me]", k)
	}
}

func TestCloneInsideAnotherRepoLeavesParentAlone(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	parent := filepath.Join(e.root, "parent")
	must(t, os.MkdirAll(parent, 0o755))
	e.gitIn(parent, "init", "-q")
	e.gitIn(parent, "config", "user.email", "parent@keep.example")
	e.gitIn(parent, "clone", "-q", "git@github.com:acme-corp/api.git", "custom-name")
	if got := e.cfg(parent, "user.email"); got != "parent@keep.example" {
		t.Fatalf("parent repo changed: %q", got)
	}
	if got := e.cfg(filepath.Join(parent, "custom-name"), "user.email"); got != "dipu@client.example" {
		t.Fatalf("clone email %q", got)
	}
}

func TestMostSpecificOwnerRuleWins(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.gs("rule", "add", "owner:acme-corp/web", "me") // added later, more specific
	e.resetLog()
	e.clone("git@github.com:acme-corp/web.git", "web")
	e.clone("git@github.com:acme-corp/api.git", "api")
	if k := e.keysUsed(); len(k) != 2 || k[0] != "me" || k[1] != "client" {
		t.Fatalf("keys used %v, want [me client]", k)
	}
}

func TestPort443HostIsTreatedAsGitHub(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.resetLog()
	e.clone("ssh://git@ssh.github.com:443/acme-corp/api.git", "api443")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "client" {
		t.Fatalf("keys used %v, want [client]", k)
	}
}

func TestFolderRuleGivesIdentityBeforeAnyNetworkCommand(t *testing.T) {
	e := newEnv(t)
	work := filepath.Join(e.root, "work")
	e.gs("rule", "add", "folder:"+work, "work")
	repo := filepath.Join(work, "newproject")
	must(t, os.MkdirAll(repo, 0o755))
	e.gitIn(repo, "init", "-q")
	if got := e.cfg(repo, "user.email"); got != "dipu@work.example" {
		t.Fatalf("includeIf identity not applied: %q", got)
	}
	e.gitIn(repo, "commit", "-q", "--allow-empty", "-m", "first") // must not be blocked
}

func TestCommitBlockedInRepoWithoutAccount(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(e.root, "loose")
	must(t, os.MkdirAll(repo, 0o755))
	e.gitIn(repo, "init", "-q")
	if out, err := e.gitMay(repo, "commit", "-q", "--allow-empty", "-m", "x"); err == nil {
		t.Fatalf("commit should be blocked without a repo email, got: %s", out)
	}
}

func TestSubmoduleFromAnotherOwnerGetsItsOwnAccount(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.gs("rule", "add", "owner:dipu", "me")
	// superproject on the server containing dipu/notes as a submodule
	sup := filepath.Join(e.root, "supseed")
	must(t, os.MkdirAll(sup, 0o755))
	e.gitIn(sup, "init", "-q", "-b", "main")
	e.gitIn(sup, "config", "user.email", "s@x.example")
	e.gitIn(sup, "submodule", "add", "-q", "git@github.com:dipu/notes.git", "notes")
	e.gitIn(sup, "commit", "-q", "-m", "with submodule")
	e.serverRepo("acme-corp/sup")
	e.gitIn(sup, "push", "-q", "-f", filepath.Join(e.server, "acme-corp", "sup.git"), "HEAD:refs/heads/main")
	e.resetLog()
	e.git("clone", "-q", "--recurse-submodules", "git@github.com:acme-corp/sup.git", "sup")
	if got := e.cfg(filepath.Join(e.root, "sup"), "user.email"); got != "dipu@client.example" {
		t.Fatalf("super email %q", got)
	}
	if got := e.cfg(filepath.Join(e.root, "sup", "notes"), "user.email"); got != "dipu@me.example" {
		t.Fatalf("submodule email %q", got)
	}
}

func TestDuplicateKeyIsRefused(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(e.root, gsBin, "account", "add", "dup", "--email", "x@y.example", "--key", e.key("work"))
	if err == nil || !strings.Contains(out, "already used by account work") {
		t.Fatalf("expected refusal, got err=%v out=%s", err, out)
	}
	// same public key under a different file name is also refused
	copyPath := filepath.Join(e.home, ".ssh", "copy_of_work")
	must(t, os.WriteFile(copyPath, []byte("x"), 0o600))
	pub, _ := os.ReadFile(e.key("work") + ".pub")
	must(t, os.WriteFile(copyPath+".pub", pub, 0o644))
	if _, err := e.run(e.root, gsBin, "account", "add", "dup2", "--email", "x@y.example", "--key", copyPath); err == nil {
		t.Fatal("same public key under another path was accepted")
	}
}

func TestInputValidation(t *testing.T) {
	e := newEnv(t)
	cases := [][]string{
		{"account", "add", "bad id", "--email", "a@b.example"},
		{"account", "add", "x1", "--email", "not-an-email"},
		{"account", "add", "x2", "--email", "a@b.example", "--host", "https://github.com"},
		{"rule", "add", "owner:~/work", "work"},
		{"rule", "add", "owner:acme", "nobody"},
		{"default", "nobody"},
	}
	for _, c := range cases {
		if out, err := e.run(e.root, gsBin, c...); err == nil {
			t.Errorf("%v accepted, want error. Output: %s", c, out)
		}
	}
}

func TestRenameAccountKeepsReposWorking(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	e.gsIn(notes, "use", "client") // saved, and outside any scan folder
	e.gs("account", "rename", "client", "acme")
	list := e.gs("account", "list")
	if !strings.Contains(list, "acme") || strings.Contains(list, "client ") {
		t.Fatalf("rename not reflected:\n%s", list)
	}
	if !strings.Contains(e.gs("rule", "list"), "-> acme") {
		t.Fatal("rule not moved to the new name")
	}
	e.resetLog()
	e.gitIn(notes, "pull", "-q")                            // repo still says "client" -> follows the rename
	if k := e.keysUsed(); len(k) != 1 || k[0] != "client" { // key file name unchanged (not generated by us)
		t.Fatalf("keys used %v after rename", k)
	}
	if _, err := e.run(e.root, gsBin, "account", "rename", "acme", "work"); err == nil {
		t.Fatal("rename onto an existing account was accepted")
	}
}

func TestRemoveAccountWithDeleteKeys(t *testing.T) {
	e := newEnv(t)
	e.gs("account", "remove", "me", "--delete-keys", "-y")
	if _, err := os.Stat(e.key("me")); !os.IsNotExist(err) {
		t.Fatal("private key still on disk")
	}
	e.gs("account", "remove", "client", "-y")
	if _, err := os.Stat(e.key("client")); err != nil {
		t.Fatal("key deleted without --delete-keys")
	}
	if out, err := e.run(e.root, gsBin, "account", "remove", "work"); err == nil {
		t.Fatalf("remove without -y and without a terminal should refuse, got: %s", out)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	cfgPath := filepath.Join(e.home, ".gitconfig")
	before, _ := os.ReadFile(cfgPath)
	out := e.gs("--dry-run", "account", "add", "extra", "--email", "x@y.example", "--key", "new")
	e.gs("rule", "add", "owner:x", "work", "--dry-run")
	e.gs("--dry-run", "account", "rename", "work", "renamed")
	e.gs("--dry-run", "account", "remove", "me", "--delete-keys")
	e.gs("--dry-run", "default", "client")
	e.gs("--dry-run", "uninstall", "--purge")
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Fatalf("--dry-run changed ~/.gitconfig\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("dry-run printed nothing: %s", out)
	}
	if _, err := os.Stat(e.key("me")); err != nil {
		t.Fatal("dry-run deleted a key")
	}
	if _, err := os.Stat(defaultKeyPathFor(e.home, "extra")); err == nil {
		t.Fatal("dry-run created a key")
	}
}

func defaultKeyPathFor(home, id string) string {
	return filepath.Join(home, ".ssh", "id_ed25519_gitswitch_"+id)
}

func TestDoctorFindsBypassesAndFixesRepoOverride(t *testing.T) {
	e := newEnv(t)
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	e.gitIn(notes, "config", "core.sshCommand", "ssh -i ~/.ssh/other")
	out, _ := e.run(notes, gsBin, "doctor")
	if !strings.Contains(out, "sets its own core.sshCommand") {
		t.Fatalf("doctor missed repo override:\n%s", out)
	}
	e.gsIn(notes, "doctor", "--fix")
	if got := e.cfg(notes, "core.sshCommand"); got != "" && !strings.Contains(got, "gitswitch") {
		t.Fatalf("override not removed: %q", got)
	}
	// environment override
	cmd := exec.Command(gsBin, "doctor")
	cmd.Env = append(append([]string{}, e.vars...), "GIT_SSH_COMMAND=ssh -i x")
	b, _ := cmd.CombinedOutput()
	if !strings.Contains(string(b), "git ignores gitswitch while GIT_SSH_COMMAND") {
		t.Fatalf("doctor missed GIT_SSH_COMMAND:\n%s", b)
	}
	// shell profile override
	must(t, os.WriteFile(filepath.Join(e.home, ".bashrc"), []byte("export GIT_SSH_COMMAND='ssh -i ~/.ssh/x'\n"), 0o644))
	if out := e.gs("doctor"); !strings.Contains(out, "~/.bashrc") {
		t.Fatalf("doctor missed .bashrc override:\n%s", out)
	}
}

func TestUseRemovesRepoOverride(t *testing.T) {
	e := newEnv(t)
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	e.gitIn(notes, "config", "core.sshCommand", "ssh -i x")
	e.gsIn(notes, "use", "me")
	if got := e.cfg(notes, "core.sshCommand"); got != "" && !strings.Contains(got, "gitswitch") {
		t.Fatalf("override still set: %q", got)
	}
}

func TestHistoryFlagsAndFixesOnlyUnpushedOwnCommits(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.gitIn(api, "commit", "-q", "--allow-empty", "-m", "good")
	e.gitIn(api, "-c", "user.email=dipu@me.example", "commit", "-q", "--allow-empty", "-m", "wrong account")
	e.gitIn(api, "commit", "-q", "--allow-empty", "-m", "good again")
	out := e.gsIn(api, "log", "--flagged")
	if !strings.Contains(out, "wrong account") || strings.Contains(out, "Initial acme-corp") {
		t.Fatalf("flagged list wrong (teammate's pushed commit must not be flagged):\n%s", out)
	}
	pushedBefore := e.gitIn(api, "rev-parse", "origin/main")
	e.gsIn(api, "fix-author")
	if out := e.gsIn(api, "log", "--flagged"); !strings.Contains(out, "No wrong-account commits") {
		t.Fatalf("still flagged after fix:\n%s", out)
	}
	if got := e.gitIn(api, "log", "-1", "--format=%ae", "HEAD~1"); got != "dipu@client.example" {
		t.Fatalf("author not fixed: %q", got)
	}
	if got := e.gitIn(api, "rev-parse", "origin/main"); got != pushedBefore {
		t.Fatal("pushed history changed")
	}
	if got := e.gitIn(api, "log", "-1", "--format=%s", "HEAD"); got != "good again" {
		t.Fatalf("commit order or content changed: %q", got)
	}
}

func TestRepoOverrideIsReportedInStatus(t *testing.T) {
	e := newEnv(t)
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	e.gitIn(notes, "config", "core.sshCommand", "ssh")
	if out := e.gsIn(notes, "status"); !strings.Contains(out, "NOT used here") {
		t.Fatalf("status missed override:\n%s", out)
	}
}

func TestCompletionsAndAccountsList(t *testing.T) {
	e := newEnv(t)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		if out := e.gs("completions", sh); !strings.Contains(out, "gitswitch") {
			t.Errorf("%s completion empty", sh)
		}
	}
	if out := e.gs("__accounts"); strings.Join(strings.Fields(out), ",") != "client,me,work" {
		t.Fatalf("__accounts = %q", out)
	}
	if runtime.GOOS != "windows" {
		if _, err := exec.LookPath("bash"); err == nil {
			script := e.gs("completions", "bash")
			p := filepath.Join(e.root, "c.bash")
			must(t, os.WriteFile(p, []byte(script), 0o644))
			if out, err := e.run(e.root, "bash", "-n", p); err != nil {
				t.Fatalf("bash completion has a syntax error: %s", out)
			}
		}
	}
}

type repoRow struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Added   bool   `json:"added"`
	Pinned  bool   `json:"pinned"`
	Missing bool   `json:"missing"`
	Remotes []struct {
		Name, URL string
	} `json:"remotes"`
}

func (e *env) repoList() map[string]repoRow {
	e.t.Helper()
	var rows []repoRow
	must(e.t, json.Unmarshal([]byte(e.gs("repo", "list", "--json")), &rows))
	m := map[string]repoRow{}
	for _, r := range rows {
		m[filepath.Base(r.Path)] = r
	}
	return m
}

// Adding, pinning and removing repos changes only gitswitch's own list:
// never the repo's config, remotes, branch or files.
func TestRepoRegistry(t *testing.T) {
	e := newEnv(t)
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	work := filepath.Join(e.root, "work")
	e.gitIn(work, "clone", "-q", "git@github.com:acme-corp/api.git", "api")
	before := e.gitIn(notes, "config", "--local", "--list")

	if got := e.repoList(); len(got) != 0 {
		t.Fatalf("list should start empty, got %v", got)
	}
	e.gs("repo", "add", notes)
	e.git("config", "--global", "--add", "gitswitch.scanFolder", work)
	l := e.repoList()
	if r := l["notes"]; !r.Added || r.Branch != "main" || len(r.Remotes) != 1 || r.Remotes[0].Name != "origin" {
		t.Fatalf("notes after add: %+v", r)
	}
	if r, ok := l["api"]; !ok || r.Added {
		t.Fatalf("api should be found by the folder scan, not added: %+v", l)
	}

	e.gs("repo", "pin", filepath.Join(work, "api"))
	var rows []repoRow
	must(t, json.Unmarshal([]byte(e.gs("repo", "list", "--json")), &rows))
	if len(rows) != 2 || filepath.Base(rows[0].Path) != "api" || !rows[0].Pinned {
		t.Fatalf("pinned repo should come first: %+v", rows)
	}

	// A scanned repo is hidden when removed, and comes back when added.
	e.gs("repo", "remove", filepath.Join(work, "api"))
	if _, ok := e.repoList()["api"]; ok {
		t.Fatal("removed scanned repo is still listed")
	}
	e.gs("repo", "add", filepath.Join(work, "api", "."))
	if r, ok := e.repoList()["api"]; !ok || r.Pinned {
		t.Fatalf("re-added api: %+v (ok=%v); it should be back and unpinned", r, ok)
	}

	e.gs("repo", "remove", notes)
	if _, ok := e.repoList()["notes"]; ok {
		t.Fatal("removed repo is still listed")
	}
	if after := e.gitIn(notes, "config", "--local", "--list"); after != before {
		t.Fatalf("repo config changed by add/remove:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if out := e.gs("repo", "show", notes); !strings.Contains(out, "not in the list") || !strings.Contains(out, "origin") {
		t.Fatalf("repo show:\n%s", out)
	}

	// An added repo whose folder is gone stays listed as missing until removed.
	e.gs("repo", "add", notes)
	must(t, os.RemoveAll(notes))
	if r := e.repoList()["notes"]; !r.Missing {
		t.Fatalf("deleted repo should be listed as missing: %+v", r)
	}
	e.gs("repo", "remove", notes)
	if _, ok := e.repoList()["notes"]; ok {
		t.Fatal("missing repo could not be removed")
	}
	if _, err := e.run(e.root, gsBin, "repo", "add", e.home); err == nil {
		t.Fatal("adding a folder that is not a repo should fail")
	}
}

// `gitswitch repo clone` uses the chosen account from the very first fetch,
// saves it with its identity, and puts the repo on the list.
func TestCloneByURL(t *testing.T) {
	e := newEnv(t)
	e.resetLog()
	out := e.gs("repo", "clone", "git@github.com:acme-corp/web.git", "--account", "client")
	web := filepath.Join(e.root, "web")
	if k := e.keysUsed(); len(k) != 1 || k[0] != "client" {
		t.Fatalf("keys used %v, want [client]\n%s", k, out)
	}
	if got := e.cfg(web, "gitswitch.account"); got != "client" {
		t.Fatalf("saved account %q", got)
	}
	if got := e.cfg(web, "user.email"); got != "dipu@client.example" {
		t.Fatalf("email %q", got)
	}
	if r := e.repoList()["web"]; !r.Added {
		t.Fatalf("cloned repo not on the list: %+v", e.repoList())
	}

	// Without --account, rules decide; a folder that does not exist yet is created.
	e.gs("rule", "add", "owner:acme-corp", "me")
	e.resetLog()
	e.gs("repo", "clone", "ssh://git@github.com/acme-corp/api.git", filepath.Join("nested", "deeper", "api-copy"))
	if k := e.keysUsed(); len(k) != 1 || k[0] != "me" {
		t.Fatalf("keys used %v, want [me] from the owner rule", k)
	}
	if got := e.cfg(filepath.Join(e.root, "nested", "deeper", "api-copy"), "user.email"); got != "dipu@me.example" {
		t.Fatalf("email %q", got)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"git@github.com:acme-corp/web.git"}, "already exists and is not empty"},
		{[]string{"ext::sh -c touch% /tmp/pwned"}, "not a repo URL"},
		{[]string{"file:///etc"}, "not supported"},
		{[]string{"git@github.com:acme-corp/web.git", "web2", "--account", "nobody"}, "nobody"},
		{[]string{"git@github.com:acme-corp/missing.git"}, "does not exist, or"},
	} {
		got, err := e.run(e.root, gsBin, append([]string{"repo", "clone"}, c.args...)...)
		if err == nil || !strings.Contains(got, c.want) {
			t.Errorf("repo clone %v: err=%v, want %q in:\n%s", c.args, err, c.want, got)
		}
	}
}

// The test binary is built without -tags desktop: `gitswitch ui` must explain
// that instead of failing obscurely (the window itself serves no network port).
func TestUIWithoutWindowExplains(t *testing.T) {
	e := newEnv(t)
	cmd := exec.Command(gsBin, "ui")
	cmd.Env = e.vars
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "without the desktop window") {
		t.Fatalf("gitswitch ui = %v, %q", err, out)
	}
}

func TestTerminalPromptSavesAnswer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix pseudo-terminal")
	}
	scriptBin, err := exec.LookPath("script")
	if err != nil {
		t.Skip("`script` not available")
	}
	e := newEnv(t)
	var vars []string
	for _, kv := range e.vars {
		if !strings.HasPrefix(kv, "GITSWITCH_NO_PROMPT=") {
			vars = append(vars, kv)
		}
	}
	gitCmd := "git clone -q git@github.com:dipu/notes.git notes"
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(scriptBin, "-q", "/dev/null", "sh", "-c", gitCmd)
	} else {
		cmd = exec.Command(scriptBin, "-qec", gitCmd, "/dev/null")
	}
	cmd.Dir, cmd.Env = e.root, vars
	// Answer only once the question is on screen, like a person would, and
	// keep stdin open: macOS `script` sends ^D to the terminal when its stdin
	// ends, which would reach the prompt before the answer.
	stdin, err := cmd.StdinPipe()
	must(t, err)
	out, err := cmd.StdoutPipe()
	must(t, err)
	cmd.Stderr = cmd.Stdout
	must(t, cmd.Start())
	go func() {
		var seen strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), "Type a number") {
				io.WriteString(stdin, "3!\n") // 3 = "me" (accounts in the order added)
				io.Copy(io.Discard, out)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("prompt hung")
	}
	stdin.Close()
	notes := filepath.Join(e.root, "notes")
	if got := e.cfg(notes, "gitswitch.account"); got != "me" {
		t.Fatalf("saved account %q, want me", got)
	}
	if got := e.cfg(notes, "user.email"); got != "dipu@me.example" {
		t.Fatalf("email %q", got)
	}
}
