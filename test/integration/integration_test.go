// Package integration runs the real gitswitch program with real git
// (clone, pull, push) against local repos through a fake ssh.
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
		{"account", "add", "x3", "--email", "a@b.example", "--name", "Ann\n[core]\n\tsshCommand = evil"},
		{"account", "add", "x4", "--email", "a@b.example\r"},
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
	Path    string   `json:"path"`
	Branch  string   `json:"branch"`
	Added   bool     `json:"added"`
	Pinned  bool     `json:"pinned"`
	Recent  int      `json:"recent"`
	Group   string   `json:"group"`
	Clones  []string `json:"clones"`
	Missing bool     `json:"missing"`
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

	// Opening a repo makes it the newest recent one; opening it again
	// moves it back to the front instead of listing it twice.
	if out := e.gs("repo", "recent"); !strings.Contains(out, "No recent repos") {
		t.Fatalf("recent should start empty:\n%s", out)
	}
	e.gs("repo", "open", notes)
	e.gs("repo", "open", filepath.Join(work, "api"))
	e.gs("repo", "open", notes)
	var recent []repoRow
	must(t, json.Unmarshal([]byte(e.gs("repo", "recent", "--json")), &recent))
	if len(recent) != 2 || filepath.Base(recent[0].Path) != "notes" || filepath.Base(recent[1].Path) != "api" {
		t.Fatalf("recent = %+v, want notes then api", recent)
	}
	if l := e.repoList(); l["notes"].Recent != 1 || l["api"].Recent != 2 {
		t.Fatalf("list ranks: notes=%d api=%d", l["notes"].Recent, l["api"].Recent)
	}

	// A scanned repo is hidden when removed, and comes back when added.
	e.gs("repo", "remove", filepath.Join(work, "api"))
	if _, ok := e.repoList()["api"]; ok {
		t.Fatal("removed scanned repo is still listed")
	}
	if out := e.gs("repo", "recent"); strings.Contains(out, "api") {
		t.Fatalf("a removed repo must leave the recent list:\n%s", out)
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

// The repo list groups by account, owner, folder or a group you name, and
// two clones of the same origin know about each other, however the URL is
// written.
func TestRepoGroupsAndClones(t *testing.T) {
	e := newEnv(t)
	work := filepath.Join(e.root, "work")
	e.gitIn(work, "clone", "-q", "git@github.com:acme-corp/api.git", "api")
	e.gitIn(work, "clone", "-q", "git@github.com:acme-corp/web.git", "web")
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	again := filepath.Join(e.root, "api-again")
	e.clone("git@github.com:acme-corp/api.git", "api-again")
	e.gitIn(again, "remote", "set-url", "origin", "ssh://git@ssh.github.com:443/Acme-Corp/api") // same repo, written another way
	e.git("config", "--global", "--add", "gitswitch.scanFolder", work)
	e.gs("repo", "add", notes)
	e.gs("repo", "add", again)

	l := e.repoList()
	if c := l["api"].Clones; len(c) != 1 || filepath.Base(c[0]) != "api-again" {
		t.Fatalf("api clones = %v", c)
	}
	if c := l["web"].Clones; len(c) != 0 {
		t.Fatalf("web has no other clone, got %v", c)
	}
	if out := e.gs("repo", "list"); !strings.Contains(out, "also cloned at") {
		t.Fatalf("repo list should say where else api is cloned:\n%s", out)
	}

	// groups maps each group's name, in lower case, to its repos.
	groups := func(args ...string) map[string][]string {
		t.Helper()
		var gs []struct {
			Name  string
			Repos []repoRow
		}
		must(t, json.Unmarshal([]byte(e.gs(append([]string{"repo", "list", "--json"}, args...)...)), &gs))
		m := map[string][]string{}
		for _, g := range gs {
			for _, r := range g.Repos {
				m[strings.ToLower(g.Name)] = append(m[strings.ToLower(g.Name)], filepath.Base(r.Path))
			}
		}
		return m
	}
	if g := groups("--by", "owner"); len(g["github.com/acme-corp"]) != 3 || len(g["github.com/dipu"]) != 1 {
		t.Fatalf("by owner = %v", g)
	}
	if g := groups("--by", "folder"); len(g) != 2 || len(g[strings.ToLower(filepath.Dir(l["api"].Path))]) != 2 {
		t.Fatalf("by folder = %v", g)
	}

	e.gs("repo", "group", "Client", filepath.Join(work, "web"))
	e.gs("repo", "group", "client", notes) // the same group, whatever the case
	if g := groups("--by", "group"); len(g["client"]) != 2 || len(g["not in a group"]) != 2 {
		t.Fatalf("by group = %v", g)
	}
	e.gs("repo", "ungroup", notes)
	if g := groups("--by", "group"); len(g["client"]) != 1 {
		t.Fatalf("after ungroup = %v", g)
	}

	// The saved way to group applies to the text list; --json stays flat.
	e.gs("set", "repo-groups", "group")
	if out := e.gs("repo", "list"); !strings.Contains(out, "Client (1)") || !strings.Contains(out, "Not in a group (3)") {
		t.Fatalf("grouped list:\n%s", out)
	}
	if l := e.repoList(); len(l) != 4 || l["web"].Group != "Client" {
		t.Fatalf("flat list: %+v", l)
	}
	// Sorting: --sort for one listing, set repo-sort for every one. (A
	// grouped list is sorted inside each group; this one is not grouped.)
	e.gs("set", "repo-groups", "none")
	order := func(args ...string) string {
		t.Helper()
		var rows []repoRow
		must(t, json.Unmarshal([]byte(e.gs(append([]string{"repo", "list", "--json"}, args...)...)), &rows))
		var names []string
		for _, r := range rows {
			names = append(names, filepath.Base(r.Path))
		}
		return strings.Join(names, " ")
	}
	if got := order("--sort", "name"); got != "api api-again notes web" {
		t.Fatalf("--sort name: %q", got)
	}
	e.gs("repo", "pin", filepath.Join(work, "web"))
	e.gs("set", "repo-sort", "name")
	if got := order(); got != "web api api-again notes" {
		t.Fatalf("saved sort, pinned first: %q", got)
	}
	if _, err := e.run(e.root, gsBin, "set", "repo-sort", "size"); err == nil {
		t.Fatal("an unknown sort must be refused")
	}
	if _, err := e.run(e.root, gsBin, "set", "repo-groups", "colour"); err == nil {
		t.Fatal("an unknown way to group must be refused")
	}
	if _, err := e.run(e.root, gsBin, "repo", "group", "a=b", notes); err == nil {
		t.Fatal("a group name with = must be refused")
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

// Without the desktop app installed (servers, `go install`), `gitswitch ui`
// must say so instead of failing obscurely.
func TestUIWithoutAppExplains(t *testing.T) {
	e := newEnv(t)
	cmd := exec.Command(gsBin, "ui")
	cmd.Env = e.vars
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "desktop app is not installed") {
		t.Fatalf("gitswitch ui = %v, %q", err, out)
	}
}

// The installers download gitswitch and gitswitch-app into one folder and
// run `gitswitch install`, which installs both. Uninstall removes the app.
func TestInstallAddsTheDesktopApp(t *testing.T) {
	e := newEnv(t)
	dl := filepath.Join(e.root, "download")
	must(t, os.MkdirAll(dl, 0o755))
	b, err := os.ReadFile(gsBin)
	must(t, err)
	gs := filepath.Join(dl, "gitswitch"+exeSuffix())
	must(t, os.WriteFile(gs, b, 0o755))
	must(t, os.WriteFile(filepath.Join(dl, "gitswitch-app"+exeSuffix()), []byte("the app"), 0o755))

	app := filepath.Join(e.home, ".local", "bin", "gitswitch-app")
	switch runtime.GOOS {
	case "darwin":
		app = filepath.Join(e.home, "Applications", "GitSwitch.app", "Contents", "MacOS", "gitswitch-app")
	case "windows":
		app = filepath.Join(e.root, "localappdata", "GitSwitch", "gitswitch-app.exe")
	}
	if out, err := e.run(e.root, gs, "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(app); err != nil || string(got) != "the app" {
		t.Fatalf("app not installed at %s: %v", app, err)
	}
	if runtime.GOOS == "darwin" {
		plist, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(app)), "Info.plist"))
		if err != nil || !strings.Contains(string(plist), "<string>gitswitch-app</string>") {
			t.Fatalf("Info.plist: %v\n%s", err, plist)
		}
	}
	if out, err := e.run(e.root, gsBin, "ui"); err != nil {
		t.Fatalf("gitswitch ui with the app installed: %v\n%s", err, out)
	}
	if out, _ := e.run(e.root, gsBin, "doctor"); !strings.Contains(out, "Desktop app") {
		t.Errorf("doctor does not mention the desktop app:\n%s", out)
	}
	if out, err := e.run(e.root, gsBin, "uninstall"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(app); !os.IsNotExist(err) {
		t.Errorf("uninstall left the app at %s", app)
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
		// Either one means "never ask on the terminal".
		if !strings.HasPrefix(kv, "GITSWITCH_NO_PROMPT=") && !strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT=") {
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

// commitFile writes a file and commits it with the repo's identity.
func (e *env) commitFile(dir, file, text, msg string) string {
	e.t.Helper()
	must(e.t, os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644))
	e.gitIn(dir, "add", file)
	e.gitIn(dir, "commit", "-q", "-m", msg)
	return e.gitIn(dir, "rev-parse", "HEAD")
}

func (e *env) subjects(dir, rev string, n int) string {
	e.t.Helper()
	return strings.ReplaceAll(e.gitIn(dir, "log", "-n", fmt.Sprint(n), "--format=%s", rev), "\n", ",")
}

// A merge that brings in a teammate's pushed commits must stop fix-author:
// the rebase would replay, and rewrite, those pushed commits too.
func TestFixAuthorRefusesPushedCommitsFromAMerge(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	e.clone("git@github.com:acme-corp/api.git", "mate")
	api, mate := filepath.Join(e.root, "api"), filepath.Join(e.root, "mate")
	e.gitIn(api, "-c", "user.email=dipu@me.example", "commit", "-q", "--allow-empty", "-m", "wrong account")
	e.gitIn(mate, "-c", "user.email=mate@acme.example", "commit", "-q", "--allow-empty", "-m", "teammate")
	e.gitIn(mate, "push", "-q", "origin", "main")
	e.gitIn(api, "pull", "-q", "--no-rebase", "--no-edit", "origin", "main")
	before := e.gitIn(api, "rev-parse", "HEAD")
	e.gsErr(api, "already pushed", "fix-author")
	if got := e.gitIn(api, "rev-parse", "HEAD"); got != before {
		t.Fatal("history changed although fix-author refused")
	}
	// After `git pull --rebase` the local commit comes last and can be fixed.
	e.gitIn(api, "pull", "-q", "--rebase", "origin", "main")
	e.gsIn(api, "fix-author")
	if got := e.gitIn(api, "log", "-2", "--format=%ae"); got != "dipu@client.example\nmate@acme.example" {
		t.Fatalf("authors after fix:\n%s", got)
	}
}

// In a single-branch clone a pushed branch has no remote-tracking ref, so
// rewriting commands must refuse instead of treating its commits as local.
func TestRewritesRefuseWhenTheRemoteBranchIsUnknown(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.git("clone", "-q", "--single-branch", "git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.gitIn(api, "switch", "-q", "-c", "feat")
	first := e.commitFile(api, "a.txt", "a\n", "feat a")
	e.commitFile(api, "b.txt", "b\n", "feat b")
	e.gitIn(api, "push", "-q", "-u", "origin", "feat")
	e.gsErr(api, "can't tell which commits are pushed", "drop", first[:7], "-y")
	e.gsErr(api, "can't tell which commits are pushed", "rollback", first[:7], "-y")
	if got := e.subjects(api, "HEAD", 2); got != "feat b,feat a" {
		t.Fatalf("history changed: %s", got)
	}
}

// Rules can be removed by value, so a script never removes the wrong one.
func TestRuleRemoveByValue(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.gs("rule", "add", "folder:~/work", "me")
	e.gsErr(e.root, "no owner rule", "rule", "remove", "owner:acme")
	e.gs("rule", "remove", "owner:Acme-Corp/")
	e.gs("rule", "remove", "folder:~/work")
	if out := e.gs("rule", "list"); !strings.Contains(out, "No rules") {
		t.Fatalf("rules left:\n%s", out)
	}
}

// gsErr runs gitswitch and expects it to fail with want in its output.
func (e *env) gsErr(dir, want string, args ...string) {
	e.t.Helper()
	out, err := e.run(dir, gsBin, args...)
	if err == nil || !strings.Contains(out, want) {
		e.t.Fatalf("gitswitch %v: err=%v, want an error with %q\n%s", args, err, want, out)
	}
}

// Branch, update, revert, drop, roll back, cherry-pick and browse, with
// real git. Pushed history is never rewritten.
func TestHistoryAndBranchActions(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	pushed := e.commitFile(api, "a.txt", "a\n", "pushed a")
	e.gitIn(api, "push", "-q", "origin", "main")
	local1 := e.commitFile(api, "b.txt", "b\n", "local b")
	e.commitFile(api, "c.txt", "c\n", "local c")

	if out := e.gsIn(api, "branch"); !strings.Contains(out, "main → origin/main, 2 to push") || !strings.Contains(out, "* main") {
		t.Fatalf("branch:\n%s", out)
	}
	if out := e.gsIn(api, "log"); !strings.Contains(out, "branch: main → origin/main") {
		t.Fatalf("log header:\n%s", out)
	}

	// Drop: pushed commits are refused, a not-pushed one is removed.
	e.gsErr(api, "already pushed", "drop", pushed[:7], "-y")
	e.gsErr(api, "add -y", "drop", local1[:7])
	e.gsIn(api, "drop", local1[:7], "-y")
	if got := e.subjects(api, "HEAD", 3); got != "local c,pushed a,Initial acme-corp/api" {
		t.Fatalf("after drop: %s", got)
	}
	if _, err := os.Stat(filepath.Join(api, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("the dropped commit's file is still there")
	}

	// Roll back: not over pushed commits; the undone changes stay as files.
	e.gsErr(api, "already pushed", "rollback", "HEAD~2", "-y")
	e.gsIn(api, "rollback", pushed[:7], "-y")
	if got := e.gitIn(api, "rev-parse", "HEAD"); got != pushed {
		t.Fatalf("HEAD after rollback = %s, want %s", got, pushed)
	}
	if st := e.gitIn(api, "status", "--porcelain"); !strings.Contains(st, "c.txt") {
		t.Fatalf("rolled-back changes should be uncommitted, status:\n%s", st)
	}
	must(t, os.Remove(filepath.Join(api, "c.txt")))

	// Revert works on pushed commits, with the repo's account.
	e.gsIn(api, "revert", pushed[:7])
	if got := e.gitIn(api, "log", "-1", "--format=%ae %s"); !strings.HasPrefix(got, "dipu@client.example Revert \"pushed a\"") {
		t.Fatalf("revert commit: %q", got)
	}
	if got := e.gitIn(api, "rev-parse", "origin/main"); got != pushed {
		t.Fatal("pushed history changed")
	}

	// Cherry-pick several commits from another branch, oldest first, given in any order.
	e.gitIn(api, "switch", "-q", "-c", "side", pushed)
	s1 := e.commitFile(api, "s1.txt", "1\n", "side one")
	s2 := e.commitFile(api, "s2.txt", "2\n", "side two")
	e.gitIn(api, "push", "-q", "origin", "side")
	e.gsIn(api, "switch", "main")
	e.gsIn(api, "cherry-pick", s2[:7], s1[:7])
	if got := e.subjects(api, "HEAD", 3); got != "side two,side one,Revert \"pushed a\"" {
		t.Fatalf("after cherry-pick: %s", got)
	}
	e.gsErr(api, "already on the current branch", "cherry-pick", pushed[:7])

	// A conflicting cherry-pick changes nothing.
	e.gitIn(api, "switch", "-q", "side")
	clash := e.commitFile(api, "s1.txt", "clash\n", "side clash")
	e.gitIn(api, "switch", "-q", "main")
	e.commitFile(api, "s1.txt", "main\n", "main edit")
	head := e.gitIn(api, "rev-parse", "HEAD")
	e.gsErr(api, "nothing changed", "cherry-pick", clash[:7])
	if got := e.gitIn(api, "rev-parse", "HEAD"); got != head || e.gitIn(api, "status", "--porcelain") != "" {
		t.Fatal("a failed cherry-pick left changes behind")
	}

	// Switching to a remote-only branch creates a local branch that tracks it.
	e.gitIn(api, "branch", "-q", "-D", "side")
	e.gsIn(api, "switch", "origin/side")
	if got := e.gitIn(api, "rev-parse", "--abbrev-ref", "side@{upstream}"); got != "origin/side" {
		t.Fatalf("side tracks %q", got)
	}

	// Update fast-forwards with the repo's account, and explains a divergence.
	other := filepath.Join(e.root, "other")
	e.clone("git@github.com:acme-corp/api.git", "other")
	e.gitIn(other, "switch", "-q", "side")
	e.commitFile(other, "new.txt", "new\n", "from elsewhere")
	e.gitIn(other, "push", "-q", "origin", "side")
	e.resetLog()
	if out := e.gsIn(api, "update"); !strings.Contains(out, "up to date") {
		t.Fatalf("update:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[0] != "client" {
		t.Fatalf("update used keys %v, want client", k)
	}
	if got := e.subjects(api, "HEAD", 1); got != "from elsewhere" {
		t.Fatalf("not updated: %s", got)
	}
	e.commitFile(other, "new2.txt", "x\n", "elsewhere again")
	e.gitIn(other, "push", "-q", "origin", "side")
	e.commitFile(api, "mine.txt", "x\n", "mine")
	e.gsErr(api, "both have new commits", "update")

	// browse prints the commit's page (the browser is not opened in tests).
	if out := e.gsIn(api, "browse", pushed[:7]); !strings.Contains(out, "https://github.com/acme-corp/api/commit/"+pushed) {
		t.Fatalf("browse:\n%s", out)
	}
	e.gsErr(api, "not pushed yet", "browse", "HEAD")

	// Filters: author, message, dates and account; a filtered log is a flat list.
	if out := e.gsIn(api, "log", "--grep", "SIDE", "-n", "50"); !strings.Contains(out, "side one") || !strings.Contains(out, "side two") || strings.Contains(out, "main edit") || !strings.Contains(out, `filter: text "SIDE"`) {
		t.Fatalf("log --grep:\n%s", out)
	}
	if out := e.gsIn(api, "log", "--author=seed@other"); !strings.Contains(out, "Initial acme-corp/api") || strings.Contains(out, "pushed a") {
		t.Fatalf("log --author:\n%s", out)
	}
	if out := e.gsIn(api, "log", "--account", "client", "-n", "50"); !strings.Contains(out, "[client]") || strings.Contains(out, "Initial acme-corp") {
		t.Fatalf("log --account:\n%s", out)
	}
	e.gsErr(api, "cannot read the date", "log", "--since", "bogus")
	if out := e.gsIn(api, "log", "--since", "2098-01-01"); !strings.Contains(out, "No commits match the filter.") {
		t.Fatalf("log --since in the future:\n%s", out)
	}
	if out := e.gsIn(api, "log", "--until", "1990-01-01", "--grep", "x"); !strings.Contains(out, "No commits match") {
		t.Fatalf("log --until in the past:\n%s", out)
	}
	if out := e.gsIn(api, "log", "--since", "yesterday", "--until", "today", "--grep", "side one"); !strings.Contains(out, "side one") {
		t.Fatalf("today's commits (both ends included):\n%s", out)
	}

	// --dry-run changes nothing.
	head = e.gitIn(api, "rev-parse", "HEAD")
	e.gsIn(api, "--dry-run", "rollback", "HEAD~1")
	e.gsIn(api, "--dry-run", "switch", "main")
	if e.gitIn(api, "rev-parse", "HEAD") != head || e.gitIn(api, "branch", "--show-current") != "side" {
		t.Fatal("dry run changed the repo")
	}
}

// The stash's commits are saved changes, not history: the log leaves them
// out and browse refuses them.
func TestLogLeavesOutTheStash(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "real commit")
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("wip\n"), 0o644))
	e.gitIn(api, "stash", "push", "-q", "-m", "secret wip")
	stash := e.gitIn(api, "rev-parse", "refs/stash")
	if out := e.gsIn(api, "log"); !strings.Contains(out, "real commit") || strings.Contains(out, "secret wip") || strings.Contains(out, "index on") {
		t.Fatalf("log shows the stash:\n%s", out)
	}
	e.gsErr(api, "not pushed yet", "browse", stash[:7])
}

// When the uncommitted changes clash with the history after a drop, git
// keeps them in the stash and still succeeds; gitswitch must say so.
func TestDropExplainsChangesKeptInTheStash(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "g.txt", "x\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")
	two := e.commitFile(api, "g.txt", "y\n", "two")
	e.commitFile(api, "f.txt", "3\n", "three")
	must(t, os.WriteFile(filepath.Join(api, "g.txt"), []byte("z\n"), 0o644))
	e.gsErr(api, "safe in the stash", "drop", two[:7], "-y")
	if got := e.subjects(api, "HEAD", 2); got != "three,base" {
		t.Fatalf("after drop: %s", got)
	}
	if got := e.gitIn(api, "stash", "list"); !strings.Contains(got, "autostash") {
		t.Fatalf("stash list: %q", got)
	}
}

// Update on a branch whose remote branch this clone has no copy of
// explains that, instead of saying the branch is not on a remote.
func TestUpdateExplainsAMissingRemoteBranch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.git("clone", "-q", "--single-branch", "git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.gitIn(api, "switch", "-q", "-c", "feat")
	e.commitFile(api, "a.txt", "a\n", "feat a")
	e.gitIn(api, "push", "-q", "-u", "origin", "feat")
	if out := e.gsIn(api, "branch"); !strings.Contains(out, "feat → origin/feat (not in this clone)") {
		t.Fatalf("branch:\n%s", out)
	}
	e.gsErr(api, "has no copy of that remote branch", "update")
}

// The branch line counts uncommitted changes: staged, changed and new
// files, in a linked worktree too.
func TestBranchShowsUncommittedChanges(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "a")
	e.commitFile(api, "b.txt", "b\n", "b")
	if out := e.gsIn(api, "branch"); strings.Contains(out, "uncommitted") {
		t.Fatalf("clean repo:\n%s", out)
	}
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("a2\n"), 0o644))
	e.gitIn(api, "add", "a.txt")
	must(t, os.WriteFile(filepath.Join(api, "b.txt"), []byte("b2\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "new 1.txt"), []byte("n\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "new2.txt"), []byte("n\n"), 0o644))
	const want = "uncommitted: 1 staged, 1 changed, 2 new"
	if out := e.gsIn(api, "branch"); !strings.Contains(out, want) {
		t.Fatalf("branch:\n%s", out)
	}
	if out := e.gsIn(api, "status"); !strings.Contains(out, want) {
		t.Fatalf("status:\n%s", out)
	}
	var st struct {
		Head struct {
			Changes struct{ Staged, Changed, New, Conflicts int }
		}
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "status", "--json")), &st))
	if c := st.Head.Changes; c.Staged != 1 || c.Changed != 1 || c.New != 2 || c.Conflicts != 0 {
		t.Fatalf("status --json changes: %+v", c)
	}

	wt := filepath.Join(e.root, "api-wt")
	e.gitIn(api, "worktree", "add", "-q", "-b", "side", wt)
	must(t, os.WriteFile(filepath.Join(wt, "w.txt"), []byte("w\n"), 0o644))
	if out := e.gsIn(wt, "branch"); !strings.Contains(out, "side (not on a remote)") || !strings.Contains(out, "uncommitted: 1 new") {
		t.Fatalf("worktree branch:\n%s", out)
	}
}

// Every change GitSwitch makes can be undone once, with real git: the
// branch goes back exactly, uncommitted work is kept, pushed history and
// later work are never lost.
func TestUndo(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "pushed a")
	e.gitIn(api, "push", "-q", "origin", "main")
	b := e.commitFile(api, "b.txt", "b\n", "local b")
	c := e.commitFile(api, "c.txt", "c\n", "local c")
	head := func() string { return e.gitIn(api, "rev-parse", "HEAD") }
	clean := func(what string) {
		t.Helper()
		if st := e.gitIn(api, "status", "--porcelain"); st != "" {
			t.Fatalf("%s: files changed:\n%s", what, st)
		}
	}

	e.gsErr(api, "nothing to undo", "undo", "-y")

	// Drop, then git gc: the ref keeps the dropped commit, so undo works.
	e.gsIn(api, "drop", b[:7], "-y")
	if strings.Contains(e.gsIn(api, "log"), "local b") {
		t.Fatal("the dropped commit must not show in the history graph")
	}
	e.gitIn(api, "reflog", "expire", "--expire=now", "--all")
	e.gitIn(api, "gc", "-q", "--prune=now")
	if out := e.gsIn(api, "undo", "--show"); !strings.Contains(out, "Dropped") || !strings.Contains(out, "exactly as it was") {
		t.Fatalf("undo --show:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if head() != c {
		t.Fatalf("undo drop: HEAD %s, want %s", head(), c)
	}
	clean("undo drop")
	e.gsErr(api, "nothing to undo", "undo", "-y")

	// Roll back, edit a file, undo: the commits are back and the edit stays.
	e.gsIn(api, "rollback", b[:7], "-y")
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("edited\n"), 0o644))
	e.gsIn(api, "undo", "-y")
	if head() != c || e.gitIn(api, "status", "--porcelain") != "M a.txt" {
		t.Fatalf("undo rollback: HEAD %s, status %q", head(), e.gitIn(api, "status", "--porcelain"))
	}
	e.gitIn(api, "checkout", "--", "a.txt")

	// Revert and cherry-pick add commits; undo removes them.
	e.gsIn(api, "revert", c[:7])
	e.gsIn(api, "undo", "-y")
	if head() != c {
		t.Fatal("undo revert")
	}
	clean("undo revert")
	e.gitIn(api, "switch", "-q", "-c", "side", b)
	side := e.commitFile(api, "s.txt", "s\n", "side s")
	e.gitIn(api, "switch", "-q", "main")
	e.gsIn(api, "cherry-pick", side[:7])
	e.gsIn(api, "undo", "-y")
	if head() != c {
		t.Fatal("undo cherry-pick")
	}
	clean("undo cherry-pick")

	// Switch, then undo: back on main.
	e.gsIn(api, "switch", "side")
	var st struct {
		Action, Branch, To, Effect, Blocked string
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "undo", "--show", "--json")), &st))
	if st.Action != "switch" || st.Branch != "main" || st.To != "side" || st.Blocked != "" {
		t.Fatalf("undo --show --json: %+v", st)
	}
	e.gsIn(api, "undo", "-y")
	if br := e.gitIn(api, "branch", "--show-current"); br != "main" {
		t.Fatalf("undo switch: on %s", br)
	}

	// Refused: a commit made since would be lost.
	e.gsIn(api, "revert", c[:7])
	d := e.commitFile(api, "d.txt", "d\n", "later d")
	e.gsErr(api, "has changed since", "undo", "-y")

	// Refused: the revert to undo is already pushed.
	e.gsIn(api, "revert", d[:7])
	e.gitIn(api, "push", "-q", "origin", "main")
	before := head()
	e.gsErr(api, "already pushed", "undo", "-y")
	if head() != before {
		t.Fatal("a refused undo must change nothing")
	}

	// Refused cleanly: uncommitted changes in a file the undo would change.
	e.gsIn(api, "cherry-pick", side[:7])
	must(t, os.WriteFile(filepath.Join(api, "s.txt"), []byte("mine\n"), 0o644))
	before = head()
	e.gsErr(api, "uncommitted changes in files the undo would change", "undo", "-y")
	if head() != before || e.gitIn(api, "status", "--porcelain") != "M s.txt" {
		t.Fatal("a refused undo must keep the uncommitted changes")
	}

	// --dry-run says what it would do and changes nothing.
	e.gitIn(api, "checkout", "--", ".")
	if out := e.gsIn(api, "--dry-run", "undo"); !strings.Contains(out, "[dry-run] undo") || head() != before {
		t.Fatalf("dry run:\n%s", out)
	}
}

// Smart switch takes uncommitted changes to another branch, with real
// git: directly when git can, through the stash when it refuses, and with
// conflict markers to fix (or a way back) when they clash.
func TestSmartSwitch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(file, text string) {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(api, file), []byte(text), 0o644))
	}
	read := func(file string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(api, file))
		must(t, err)
		return string(b)
	}
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	on := func() string { return e.gitIn(api, "branch", "--show-current") }
	e.commitFile(api, "f.txt", "1\n2\n3\n4\n5\n", "base f")
	e.commitFile(api, "keep.txt", "k\n", "base keep")
	e.gitIn(api, "push", "-q", "origin", "main")
	e.gitIn(api, "switch", "-q", "-c", "other")
	e.commitFile(api, "f.txt", "1\n2\n3\n4\nOTHER\n", "other f")
	e.gitIn(api, "push", "-q", "origin", "other")
	e.gitIn(api, "switch", "-q", "main")
	e.gitIn(api, "branch", "-q", "-D", "other") // only origin/other is left

	// 1. Git can keep the changes: they just come along.
	write("keep.txt", "k2\n")
	e.gsIn(api, "switch", "origin/other", "--smart")
	if on() != "other" || status() != "M keep.txt" {
		t.Fatalf("plain carry: on %s, status %q", on(), status())
	}
	e.gsIn(api, "switch", "main", "--smart")

	// 2. Git refuses, but the changes merge cleanly: stash, switch, back.
	write("f.txt", "ONE\n2\n3\n4\n5\n")
	e.gitIn(api, "add", "f.txt")
	out := e.gsIn(api, "switch", "other", "--smart")
	if on() != "other" || read("f.txt") != "ONE\n2\n3\n4\nOTHER\n" || !strings.Contains(out, "taking your uncommitted changes along") {
		t.Fatalf("merged: on %s, f.txt %q\n%s", on(), read("f.txt"), out)
	}
	if e.gitIn(api, "stash", "list") != "" {
		t.Fatal("the stashed copy must be dropped")
	}
	e.gitIn(api, "checkout", "--", ".")
	e.gitIn(api, "reset", "-q", "--hard")
	e.gitIn(api, "switch", "-q", "main")

	// 3. A clash: stay on other with markers, keep the copy, block other
	// actions until done.
	setup := func() {
		t.Helper()
		write("f.txt", "1\n2\n3\n4\nMINE\n")
		write("keep.txt", "k3\n")
		e.gitIn(api, "add", "keep.txt")
		write("new.txt", "n\n")
	}
	setup()
	out = e.gsIn(api, "switch", "other", "--smart")
	if on() != "other" || !strings.Contains(out, "1 file(s) clash with it: f.txt") || !strings.Contains(read("f.txt"), "<<<<<<<") ||
		read("new.txt") != "n\n" || e.gitIn(api, "stash", "list") == "" {
		t.Fatalf("clash: on %s\n%s\nf.txt:\n%s", on(), out, read("f.txt"))
	}
	if out := e.gsIn(api, "branch"); !strings.Contains(out, "smart switch in progress") || !strings.Contains(out, "1 conflict") {
		t.Fatalf("branch during a clash:\n%s", out)
	}
	e.gsErr(api, "gitswitch resolve --done", "switch", "main")
	e.gsErr(api, "still have conflict markers: f.txt", "resolve", "--done")
	write("f.txt", "1\n2\n3\n4\nMINE AND OTHER\n")
	e.gsIn(api, "resolve", "--done")
	if on() != "other" || e.gitIn(api, "stash", "list") != "" || status() != "M f.txt\n M keep.txt\n?? new.txt" {
		t.Fatalf("done: on %s, stash %q, status %q", on(), e.gitIn(api, "stash", "list"), status())
	}
	if out := e.gsIn(api, "resolve"); !strings.Contains(out, "Nothing to resolve") {
		t.Fatalf("resolve after done:\n%s", out)
	}
	e.gitIn(api, "reset", "-q", "--hard")
	must(t, os.Remove(filepath.Join(api, "new.txt")))
	e.gitIn(api, "switch", "-q", "main")

	// 4. A clash, then go back: main and the changes exactly as before,
	// staged ones staged.
	setup()
	before := status()
	e.gsIn(api, "switch", "other", "--smart")
	write("f.txt", "half fixed\n")
	e.gsIn(api, "resolve", "--abort", "-y")
	if on() != "main" || status() != before || read("f.txt") != "1\n2\n3\n4\nMINE\n" || e.gitIn(api, "stash", "list") != "" {
		t.Fatalf("abort: on %s, status %q (want %q), f.txt %q", on(), status(), before, read("f.txt"))
	}
}

// Park leaves uncommitted changes on their branch as a stash entry, with
// real git: they are offered back on return and restored staged files
// staged, parking twice keeps both, a clash on restore changes nothing,
// and a smart switch that clashed can park them instead.
func TestPark(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(file, text string) {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(api, file), []byte(text), 0o644))
	}
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	on := func() string { return e.gitIn(api, "branch", "--show-current") }
	stashes := func() string { return e.gitIn(api, "stash", "list", "--format=%gs") }
	e.commitFile(api, "f.txt", "1\n2\n3\n4\n5\n", "base f")
	e.commitFile(api, "keep.txt", "k\n", "base keep")
	e.gitIn(api, "switch", "-q", "-c", "other")
	e.commitFile(api, "f.txt", "1\n2\n3\n4\nOTHER\n", "other f")
	e.gitIn(api, "switch", "-q", "main")

	// 1. Park: staged, changed and new files leave with the stash.
	write("f.txt", "ONE\n2\n3\n4\n5\n")
	e.gitIn(api, "add", "f.txt")
	write("keep.txt", "k2\n")
	write("new.txt", "n\n")
	before := status()
	out := e.gsIn(api, "switch", "other", "--park")
	if on() != "other" || status() != "" || stashes() != "On main: gitswitch park: main" || !strings.Contains(out, "Parked your changes") {
		t.Fatalf("park: on %s, status %q, stash %q\n%s", on(), status(), stashes(), out)
	}

	// 2. Parking twice keeps both; each branch is offered its own.
	write("keep.txt", "on other\n")
	out = e.gsIn(api, "switch", "main", "--park")
	if !strings.Contains(out, "You parked changes on main") || !strings.Contains(out, "gitswitch parked restore") {
		t.Fatalf("back on main: no offer\n%s", out)
	}
	if out := e.gsIn(api, "branch"); !strings.Contains(out, "1 parked here") {
		t.Fatalf("branch with parked changes:\n%s", out)
	}
	if out := e.gsIn(api, "parked"); !strings.Contains(out, "1. other") || !strings.Contains(out, "2. main") || !strings.Contains(out, "← this branch") {
		t.Fatalf("parked list:\n%s", out)
	}
	if out := e.gsIn(api, "parked", "show"); !strings.Contains(out, "-1") || !strings.Contains(out, "+ONE") || !strings.Contains(out, "new.txt") {
		t.Fatalf("parked show:\n%s", out)
	}

	// 3. Restore (the newest parked on this branch): exactly as before.
	e.gsIn(api, "parked", "restore")
	if on() != "main" || status() != before || stashes() != "On other: gitswitch park: other" {
		t.Fatalf("restore: status %q (want %q), stash %q", status(), before, stashes())
	}

	// 4. Restoring needs a clean working folder.
	e.gsErr(api, "Commit or park them first", "parked", "restore", "1")

	// 5. A clash on restore changes nothing; the changes stay parked.
	e.gsIn(api, "switch", "other", "--park")
	e.gsIn(api, "switch", "main") // clean: a plain switch
	e.commitFile(api, "f.txt", "CLASH\n2\n3\n4\n5\n", "moved on")
	e.gsErr(api, "stay parked", "parked", "restore")
	if status() != "" || !strings.Contains(stashes(), "On main: gitswitch park: main") {
		t.Fatalf("clash on restore: status %q, stash %q", status(), stashes())
	}

	// 6. Delete: both remaining sets, one by number.
	e.gsIn(api, "parked", "drop", "-y")
	e.gsIn(api, "parked", "drop", "1", "-y")
	if stashes() != "" || !strings.Contains(e.gsIn(api, "parked"), "Nothing is parked") {
		t.Fatalf("drop: stash %q", stashes())
	}

	// 7. A smart switch that clashed parks the changes instead: as they
	// were before the switch, and the clash is over.
	e.gitIn(api, "reset", "-q", "--hard", "HEAD~1")
	write("f.txt", "1\n2\n3\n4\nMINE\n")
	write("new.txt", "n\n")
	before = status()
	e.gsIn(api, "switch", "other", "--smart")
	e.gsIn(api, "resolve", "--park", "-y")
	if on() != "other" || status() != "" || stashes() != "On main: gitswitch park: main" {
		t.Fatalf("park instead: on %s, status %q, stash %q", on(), status(), stashes())
	}
	if out := e.gsIn(api, "resolve"); !strings.Contains(out, "Nothing to resolve") {
		t.Fatalf("resolve after park instead:\n%s", out)
	}

	// 8. Undo of a park switches back; the changes stay parked until
	// restored.
	e.gsIn(api, "undo", "-y")
	e.gsIn(api, "parked", "restore")
	if on() != "main" || status() != before || stashes() != "" {
		t.Fatalf("undo, restore: on %s, status %q (want %q), stash %q", on(), status(), before, stashes())
	}

	// 9. Changes parked on a branch deleted since are still listed.
	e.gsIn(api, "switch", "other", "--park")
	e.gitIn(api, "branch", "-q", "-D", "main")
	if out := e.gsIn(api, "parked"); !strings.Contains(out, "1. main") {
		t.Fatalf("parked on a deleted branch:\n%s", out)
	}
}

// The switch-changes setting says what a switch does with uncommitted
// changes, for every repo or one; with neither a flag, a setting nor a
// terminal, a switch with changes stops instead of guessing.
func TestSwitchSetting(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	on := func() string { return e.gitIn(api, "branch", "--show-current") }
	stashes := func() string { return e.gitIn(api, "stash", "list", "--format=%gs") }
	e.commitFile(api, "f.txt", "f\n", "base")
	e.gitIn(api, "branch", "other")
	change := func() { t.Helper(); must(t, os.WriteFile(filepath.Join(api, "f.txt"), []byte("changed\n"), 0o644)) }

	// 1. Ask (the default) without a terminal: stop, nothing changes.
	change()
	e.gsErr(api, "--smart takes them with you to other, --park leaves them on main", "switch", "other")
	if on() != "main" || e.gitIn(api, "status", "--porcelain") != "M f.txt" {
		t.Fatalf("stopped: on %s", on())
	}
	e.gsErr(api, "not a choice", "set", "switch-changes", "sometimes")
	e.gsErr(api, "usage", "set", "switch-changes", "global")

	// 2. Park for every repo.
	e.gs("set", "switch-changes", "park")
	if v := e.git("config", "--global", "gitswitch.switchChanges"); v != "park" {
		t.Fatalf("global config = %q", v)
	}
	if out := e.gsIn(api, "status"); !strings.Contains(out, "switching with uncommitted changes: park (every repo)") {
		t.Fatalf("status:\n%s", out)
	}
	out := e.gsIn(api, "switch", "other")
	if on() != "other" || stashes() != "On main: gitswitch park: main" || !strings.Contains(out, "your choice for every repo: park") {
		t.Fatalf("park setting: on %s, stash %q\n%s", on(), stashes(), out)
	}
	e.gsIn(api, "switch", "main")
	e.gsIn(api, "parked", "restore")

	// 3. This repo's own choice comes first; a flag beats both.
	e.gsIn(api, "set", "switch-changes", "smart", "--repo")
	if out := e.gsIn(api, "status"); !strings.Contains(out, "smart (this repo)") {
		t.Fatalf("status with a repo choice:\n%s", out)
	}
	e.gsIn(api, "switch", "other")
	if on() != "other" || stashes() != "" || e.gitIn(api, "status", "--porcelain") != "M f.txt" {
		t.Fatalf("smart repo setting: on %s, stash %q", on(), stashes())
	}
	e.gsIn(api, "switch", "main", "--park")
	if stashes() != "On other: gitswitch park: other" {
		t.Fatalf("flag over setting: stash %q", stashes())
	}

	// 4. "global --repo" follows every repo's choice again.
	e.gsIn(api, "set", "switch-changes", "global", "--repo")
	if v, err := e.gitMay(api, "config", "--local", "gitswitch.switchChanges"); err == nil {
		t.Fatalf("repo config still set: %q", v)
	}
	if out := e.gsIn(api, "status"); !strings.Contains(out, "park (every repo)") {
		t.Fatalf("status after global --repo:\n%s", out)
	}
	e.gsIn(api, "set", "switch-changes", "global", "--repo") // already gone: fine
}

// A new branch starts at HEAD or any commit and brings uncommitted
// changes, with real git; the prefix setting goes in front of its name.
func TestNewBranch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(file, text string) {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(api, file), []byte(text), 0o644))
	}
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	on := func() string { return e.gitIn(api, "branch", "--show-current") }
	first := e.commitFile(api, "f.txt", "1\n", "first")
	e.commitFile(api, "f.txt", "2\n", "second")

	// 1. From HEAD, with the prefix: staged, changed and new files come along.
	e.gs("set", "branch-prefix", "feature/")
	write("f.txt", "3\n")
	e.gitIn(api, "add", "f.txt")
	write("g.txt", "g\n")
	before := status()
	out := e.gsIn(api, "branch", "new", "login")
	if on() != "feature/login" || status() != before || !strings.Contains(out, "with your uncommitted changes") {
		t.Fatalf("new from HEAD: on %s, status %q\n%s", on(), status(), out)
	}
	e.gsErr(api, "already a branch feature/login", "branch", "new", "feature/login")
	e.gsErr(api, "can't be a branch name", "branch", "new", "has space", "--no-prefix")
	e.gsErr(api, "no commit, branch or tag", "branch", "new", "x", "--from", "nope")

	// 2. From an older commit: the changes touch f.txt, which is different
	// there, so nothing changes.
	e.gsErr(api, "nothing changed", "branch", "new", "old", "--from", first)
	if on() != "feature/login" || status() != before {
		t.Fatalf("refused: on %s, status %q", on(), status())
	}

	// 3. Undo switches back; the branch stays.
	e.gsIn(api, "undo", "-y")
	if on() != "main" || status() != before || !strings.Contains(e.gitIn(api, "branch"), "feature/login") {
		t.Fatalf("undo: on %s, status %q", on(), status())
	}

	// 4. From a commit with a clean folder, without the prefix.
	e.gitIn(api, "reset", "-q", "--hard")
	must(t, os.Remove(filepath.Join(api, "g.txt")))
	e.gsIn(api, "branch", "new", "hotfix", "--no-prefix", "--from="+first[:7])
	if on() != "hotfix" || e.gitIn(api, "rev-parse", "HEAD") != first {
		t.Fatalf("from a commit: on %s at %s", on(), e.gitIn(api, "rev-parse", "HEAD"))
	}

	// 5. A detached HEAD gets a branch to keep its commits on.
	e.gitIn(api, "checkout", "-q", "--detach", "main")
	e.gs("set", "branch-prefix", "")
	e.gsIn(api, "branch", "new", "rescue")
	if on() != "rescue" {
		t.Fatalf("from a detached HEAD: on %q", on())
	}
	e.gsErr(api, "can't start a branch name", "set", "branch-prefix", "a b")
}

// Commits made on the wrong branch move to a new branch, with real git:
// the old branch goes back to its remote branch, uncommitted changes stay,
// and undo puts everything back.
func TestMoveToNewBranch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	on := func() string { return e.gitIn(api, "branch", "--show-current") }
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	pushed := e.commitFile(api, "a.txt", "a\n", "pushed a")
	e.gitIn(api, "push", "-q", "origin", "main")
	e.gsErr(api, "nothing to move", "branch", "new", "login", "--move")
	e.commitFile(api, "b.txt", "b\n", "wrong branch b")
	tip := e.commitFile(api, "c.txt", "c\n", "wrong branch c")
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("mine\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "new.txt"), []byte("new\n"), 0o644))
	before := status()

	e.gsErr(api, "usage", "branch", "new", "x", "--move", "--from", pushed)
	out := e.gsIn(api, "branch", "new", "login", "--move")
	if on() != "login" || e.gitIn(api, "rev-parse", "HEAD") != tip || e.gitIn(api, "rev-parse", "main") != pushed || status() != before ||
		!strings.Contains(out, "Moved 2 commits from main to the new branch login") {
		t.Fatalf("move: on %s, main at %s, status %q\n%s", on(), e.gitIn(api, "rev-parse", "main"), status(), out)
	}
	if out := e.gsIn(api, "branch"); !strings.Contains(out, "login (not on a remote)") {
		t.Fatalf("branch after move:\n%s", out)
	}

	// Undo puts the commits back on main and deletes login.
	e.gsIn(api, "undo", "-y")
	if on() != "main" || e.gitIn(api, "rev-parse", "main") != tip || e.gitIn(api, "branch", "--list", "login") != "" || status() != before {
		t.Fatalf("undo: on %s, main at %s, branches %q, status %q", on(), e.gitIn(api, "rev-parse", "main"), e.gitIn(api, "branch"), status())
	}

	// Main moved since the move: undo refuses and changes nothing.
	e.gsIn(api, "branch", "new", "login", "--move")
	e.gitIn(api, "branch", "-f", "main", tip)
	e.gsErr(api, "main has changed since", "undo", "-y")
	if on() != "login" {
		t.Fatalf("refused undo: on %s", on())
	}

	// A branch with no remote branch can't tell what is not pushed.
	e.gsErr(api, "doesn't pull from a remote branch", "branch", "new", "other", "--move")
}

// Compare lists what the branch would push and pull, with real git.
func TestCompare(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	e.clone("git@github.com:acme-corp/api.git", "mate")
	api, mate := filepath.Join(e.root, "api"), filepath.Join(e.root, "mate")
	if out := e.gsIn(api, "branch", "compare"); !strings.Contains(out, "main is up to date with origin/main") {
		t.Fatalf("up to date:\n%s", out)
	}
	e.commitFile(api, "a.txt", "a\n", "mine")
	e.commitFile(mate, "b.txt", "b\n", "theirs")
	e.gitIn(mate, "push", "-q", "origin", "main")
	e.gitIn(api, "fetch", "-q", "origin")

	out := e.gsIn(api, "branch", "compare")
	if !strings.Contains(out, "To push to origin/main (1):") || !strings.Contains(out, "mine") ||
		!strings.Contains(out, "To pull from origin/main (1):") || !strings.Contains(out, "theirs") || !strings.Contains(out, "Both have new commits") {
		t.Fatalf("diverged:\n%s", out)
	}
	var c struct {
		ToPush, ToPull []struct{ Subject string }
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "branch", "compare", "--json")), &c))
	if len(c.ToPush) != 1 || c.ToPush[0].Subject != "mine" || len(c.ToPull) != 1 || c.ToPull[0].Subject != "theirs" {
		t.Fatalf("json: %+v", c)
	}

	e.gitIn(api, "switch", "-q", "-c", "local")
	e.gsErr(api, "not on a remote yet", "branch", "compare")
}

// Commits are made as the repo's account, with real git: the staged files,
// or every change with --all. Undo takes the commit back off, and a repo
// email of another account is refused.
func TestCommit(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	e.commitFile(api, "a.txt", "a\n", "first")
	e.gsErr(api, "nothing to commit", "commit", "-m", "x")
	e.gsErr(api, "usage", "commit", "--all")

	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("b\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "new.txt"), []byte("n\n"), 0o644))
	e.gsErr(api, "nothing is staged", "commit", "-m", "x")
	e.gitIn(api, "add", "a.txt")
	out := e.gsIn(api, "commit", "-m", "Change a")
	if !strings.Contains(out, "Change a on main as client (1 file(s))") || status() != "?? new.txt" ||
		e.gitIn(api, "log", "-1", "--format=%ae %s") != "dipu@client.example Change a" {
		t.Fatalf("staged commit: %q, %s\n%s", status(), e.gitIn(api, "log", "-1", "--format=%ae %s"), out)
	}
	before := e.gitIn(api, "rev-parse", "HEAD")

	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("c\n"), 0o644))
	e.gsIn(api, "commit", "-a", "--message=Everything")
	if status() != "" || e.gitIn(api, "show", "--name-only", "--format=", "HEAD") != "a.txt\nnew.txt" {
		t.Fatalf("commit --all: %q, %q", status(), e.gitIn(api, "show", "--name-only", "--format=", "HEAD"))
	}

	// Undo: the commit is gone, its changes are in the files.
	e.gsIn(api, "undo", "-y")
	if e.gitIn(api, "rev-parse", "HEAD") != before || status() != "M a.txt\n?? new.txt" {
		t.Fatalf("undo: HEAD %s, %q", e.gitIn(api, "rev-parse", "HEAD"), status())
	}

	// The repo's email is another account's: refused, nothing committed.
	e.gitIn(api, "config", "user.email", "dipu@work.example")
	e.gsErr(api, "gitswitch use client --once", "commit", "-a", "-m", "x")
	if e.gitIn(api, "rev-parse", "HEAD") != before {
		t.Fatal("committed with the wrong email")
	}
}

// Amend the last commit with real git: a new message, every change with
// --all, undo puts the commit back as it was, and a pushed commit is
// refused.
func TestCommitAmend(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	subject := func() string { return e.gitIn(api, "log", "-1", "--format=%s") }
	first := e.commitFile(api, "a.txt", "a\n", "frist")
	parent := e.gitIn(api, "rev-parse", "HEAD~1")

	e.gsErr(api, "nothing to amend", "commit", "--amend")
	e.gsErr(api, "nothing to amend", "commit", "--amend", "-m", "frist")
	out := e.gsIn(api, "commit", "--amend", "-m", "First")
	if !strings.Contains(out, "Amended "+first[:7]+": now ") || !strings.Contains(out, "First on main as client (1 file(s))") ||
		subject() != "First" || e.gitIn(api, "rev-parse", "HEAD~1") != parent {
		t.Fatalf("amend message: %q\n%s", subject(), out)
	}
	renamed := e.gitIn(api, "rev-parse", "HEAD")

	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("b\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "new.txt"), []byte("n\n"), 0o644))
	e.gsErr(api, "nothing is staged", "commit", "--amend")
	e.gsIn(api, "commit", "--amend", "--all")
	if status() != "" || subject() != "First" || e.gitIn(api, "show", "--name-only", "--format=", "HEAD") != "a.txt\nnew.txt" {
		t.Fatalf("amend --all: %q, %q, %q", status(), subject(), e.gitIn(api, "show", "--name-only", "--format=", "HEAD"))
	}

	// Undo: the commit is back as it was, what the amend added is in the
	// files, uncommitted.
	if out := e.gsIn(api, "undo", "--show"); !strings.Contains(out, "before the amend") {
		t.Fatalf("undo --show:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if e.gitIn(api, "rev-parse", "HEAD") != renamed || status() != "M a.txt\n?? new.txt" {
		t.Fatalf("undo: HEAD %s, %q", e.gitIn(api, "rev-parse", "HEAD"), status())
	}

	// Pushed: refused, nothing changed.
	e.gitIn(api, "push", "-q", "origin", "main")
	e.gsErr(api, "already pushed", "commit", "--amend", "-a", "-m", "x")
	if e.gitIn(api, "rev-parse", "HEAD") != renamed {
		t.Fatal("amended a pushed commit")
	}
}

// Branches merged into the remote's default branch are deleted, with real
// git; unmerged ones, main, the current one and one checked out in another
// working folder stay. Undo brings them back, tracking their remote branch.
func TestPruneMergedBranches(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	branches := func() string {
		return strings.Join(strings.Fields(e.gitIn(api, "branch", "--format=%(refname:short)")), " ")
	}
	if out := e.gsIn(api, "branch", "prune"); !strings.Contains(out, "No branch to delete: none is merged into origin/main") {
		t.Fatalf("nothing to prune:\n%s", out)
	}

	// feat is pushed, then merged into main on the server.
	e.gitIn(api, "switch", "-q", "-c", "feat")
	e.commitFile(api, "f.txt", "f\n", "feature")
	e.gitIn(api, "push", "-q", "-u", "origin", "feat")
	e.gitIn(api, "switch", "-q", "main")
	e.gitIn(api, "merge", "-q", "--ff-only", "feat")
	e.gitIn(api, "push", "-q", "origin", "main")
	// squashed: its remote branch is deleted, but its commit is not on main.
	e.gitIn(api, "switch", "-q", "-c", "squashed")
	e.commitFile(api, "s.txt", "s\n", "squashed")
	e.gitIn(api, "push", "-q", "-u", "origin", "squashed")
	e.gitIn(api, "push", "-q", "origin", "--delete", "squashed")
	// wip: not merged, never pushed. tree: merged, but checked out elsewhere.
	e.gitIn(api, "switch", "-q", "-c", "wip")
	e.commitFile(api, "w.txt", "w\n", "wip")
	e.gitIn(api, "switch", "-q", "main")
	e.gitIn(api, "worktree", "add", "-q", "-b", "tree", filepath.Join(e.root, "tree"))
	e.gitIn(api, "branch", "old", "main~1")
	before := branches()

	e.gsErr(api, "add -y", "branch", "prune") // no terminal to ask in
	out := e.gsIn(api, "branch", "prune", "-y")
	if got := branches(); got != "main squashed tree wip" || !strings.Contains(out, "Deleted 2 merged branch(es)") ||
		!strings.Contains(out, "Kept squashed") {
		t.Fatalf("prune: branches %q\n%s", got, out)
	}
	if cfg, err := e.run(api, "git", "config", "--get-regexp", `^branch\.feat\.`); err == nil {
		t.Fatalf("feat's tracking config is left: %s", cfg)
	}
	if !strings.Contains(e.gitIn(api, "rev-list", "origin/main"), e.gitIn(api, "rev-parse", "origin/main")) {
		t.Fatal("origin/main lost its commits")
	}

	// Undo brings feat and old back; feat tracks origin/feat again.
	e.gsIn(api, "undo", "-y")
	if got := branches(); got != before || e.gitIn(api, "rev-parse", "--abbrev-ref", "feat@{upstream}") != "origin/feat" {
		t.Fatalf("undo: branches %q, want %q", got, before)
	}
}

// Push sends a branch with the repo's account, -u the first time, with a
// pull request link; it is never forced, and a refused or diverged push
// is explained.
func TestPush(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")

	// 1. A new branch: pushed with the client key, tracking it, with a
	// pull request link.
	e.gsIn(api, "branch", "new", "feature/x")
	e.commitFile(api, "x.txt", "x\n", "x")
	e.resetLog()
	out := e.gsIn(api, "push")
	if !strings.Contains(out, "Pushed 1 commit(s) to origin/feature/x as client") || !strings.Contains(out, "feature/x now tracks origin/feature/x") ||
		!strings.Contains(out, "Open a pull request: https://github.com/acme-corp/api/pull/new/feature/x") {
		t.Fatalf("first push:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("keys used: %v", k)
	}
	if up := e.gitIn(api, "rev-parse", "--abbrev-ref", "feature/x@{upstream}"); up != "origin/feature/x" {
		t.Fatalf("upstream = %q", up)
	}
	if out := e.gsIn(api, "push"); !strings.Contains(out, "Nothing to push") {
		t.Fatalf("second push:\n%s", out)
	}

	// 2. Someone else pushed to it meanwhile: refused, explained, not forced.
	e.clone("git@github.com:acme-corp/api.git", "other")
	other := filepath.Join(e.root, "other")
	e.gitIn(other, "switch", "-q", "feature/x")
	e.commitFile(other, "o.txt", "o\n", "other")
	e.gitIn(other, "push", "-q")
	e.commitFile(api, "y.txt", "y\n", "y")
	e.gsErr(api, "Update first", "push")
	e.gitIn(api, "fetch", "-q")
	e.gsErr(api, "both have new commits", "push")
	if e.gitIn(other, "log", "-1", "--format=%s", "origin/feature/x") != "other" {
		t.Fatal("the other push was overwritten")
	}

	// 3. The default branch gets no pull request link.
	e.gitIn(api, "switch", "-q", "main")
	e.commitFile(api, "m.txt", "m\n", "m")
	if out := e.gsIn(api, "push"); !strings.Contains(out, "Pushed 1 commit(s) to origin/main") || strings.Contains(out, "pull request") {
		t.Fatalf("push main:\n%s", out)
	}
}

// Before a push, commits made as another account than the one that pushes
// are caught: without a terminal the push needs -y, and fix-author makes
// them right. A repo email that is not the account's is reported too.
func TestAccountCheckBeforePush(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")

	// A commit made with the personal email in the work repo.
	e.gitIn(api, "config", "user.email", "dipu@me.example")
	e.commitFile(api, "b.txt", "b\n", "as me")
	if out := e.gsIn(api, "status"); !strings.Contains(out, "repo email does not match the account") {
		t.Fatalf("status:\n%s", out)
	}
	e.gsErr(api, "push them as they are with -y", "push")
	if e.gitIn(api, "rev-parse", "origin/main") == e.gitIn(api, "rev-parse", "HEAD") {
		t.Fatal("pushed without -y")
	}

	// Fix it, make new commits as client again, then push: no warning.
	e.gsIn(api, "fix-author")
	e.gsIn(api, "use", "client", "--once")
	out := e.gsIn(api, "push")
	if strings.Contains(out, "Account check") || !strings.Contains(out, "Pushed 1 commit(s) to origin/main as client") {
		t.Fatalf("push after fix:\n%s", out)
	}
	if got := e.gitIn(api, "log", "-1", "--format=%ae", "origin/main"); got != "dipu@client.example" {
		t.Fatalf("pushed author = %q", got)
	}

	// -y pushes them as they are, after saying so.
	e.gitIn(api, "config", "user.email", "dipu@me.example")
	e.commitFile(api, "c.txt", "c\n", "as me again")
	out = e.gsIn(api, "push", "-y")
	if !strings.Contains(out, "1 not-pushed commit(s) on this branch were made as dipu@me.example, but client will push them") ||
		!strings.Contains(out, "gitswitch use client --once") || !strings.Contains(out, "Pushed 1 commit(s)") {
		t.Fatalf("push -y:\n%s", out)
	}
}

// Repos under a scan folder that is a symlink are found.
func TestScanFolderThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	e := newEnv(t)
	real := filepath.Join(e.root, "real")
	must(t, os.MkdirAll(real, 0o755))
	e.gitIn(real, "clone", "-q", "git@github.com:dipu/notes.git", "notes")
	link := filepath.Join(e.root, "projects")
	must(t, os.Symlink(real, link))
	e.git("config", "--global", "--add", "gitswitch.scanFolder", link)
	if _, ok := e.repoList()["notes"]; !ok {
		t.Fatal("the repo under the symlinked scan folder was not found")
	}
}

// Renaming an account moves its folder rule and the includeIf that gives
// repos there its identity, without losing either.
func TestRenameMovesFolderRuleAndIdentity(t *testing.T) {
	e := newEnv(t)
	work := filepath.Join(e.root, "work")
	e.gs("rule", "add", "folder:"+work, "client")
	e.gs("account", "rename", "client", "acme")
	if rules := e.gs("rule", "list"); !strings.Contains(rules, "-> acme") || strings.Contains(rules, "-> client") {
		t.Fatalf("rules after rename:\n%s", rules)
	}
	repo := filepath.Join(work, "p")
	must(t, os.MkdirAll(repo, 0o755))
	e.gitIn(repo, "init", "-q")
	if got := e.cfg(repo, "user.email"); got != "dipu@client.example" {
		t.Fatalf("identity in the rule's folder after rename: %q", got)
	}
	if strings.Contains(e.git("config", "--global", "--get-regexp", `^includeif\.`), "client") {
		t.Fatal("an includeIf still points at the old identity file")
	}
}

// `gitswitch show` reads a commit's whole message and changed files from
// real git, including a merge (compared with its first parent).
func TestShowCommit(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "one\ntwo\n", "add a")
	must(t, os.WriteFile(filepath.Join(api, "b.bin"), []byte{0, 1, 2, 0}, 0o644))
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("one\n"), 0o644))
	e.gitIn(api, "add", "-A")
	e.gitIn(api, "commit", "-q", "-m", "Second\n\nThe body explains why.")

	out := e.gsIn(api, "show", "HEAD", "--json")
	var d struct {
		Message        string
		Added, Deleted int
		Files          []struct {
			Path   string
			Binary bool
		}
	}
	must(t, json.Unmarshal([]byte(out), &d))
	if !strings.Contains(d.Message, "The body explains why.") || d.Deleted != 1 || len(d.Files) != 2 || !d.Files[1].Binary {
		t.Fatalf("show --json = %+v", d)
	}
	if out := e.gsIn(api, "show", "HEAD"); !strings.Contains(out, "2 file(s) changed, 0 insertion(s), 1 deletion(s)") {
		t.Errorf("show:\n%s", out)
	}

	e.gitIn(api, "switch", "-q", "-c", "side", "HEAD~1")
	e.commitFile(api, "side.txt", "s\n", "side work")
	e.gitIn(api, "switch", "-q", "main")
	e.gitIn(api, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
	if out := e.gsIn(api, "show", "HEAD"); !strings.Contains(out, "side.txt") || !strings.Contains(out, "1 file(s) changed") {
		t.Errorf("show merge:\n%s", out)
	}
	e.gsErr(api, "no commit", "show", "--output=/tmp/x")
	e.gsErr(api, "no commit", "show", "nope")
}

// `gitswitch default --none`: no account is ever guessed. With nobody to
// ask, a repo without a saved account or rule does not connect; adding an
// account does not quietly make it the default; picking a default again
// brings the old behaviour back.
func TestNoDefaultNeverGuesses(t *testing.T) {
	e := newEnv(t)
	e.gs("default", "--none")
	if out := e.gs("status"); !strings.Contains(out, "Default:  none") {
		t.Fatalf("status:\n%s", out)
	}
	e.keyPair("extra")
	e.gs("account", "add", "extra", "--email", "x@extra.example", "--key", e.key("extra"))
	if got := e.cfg(e.root, "gitswitch.default"); got != "" {
		t.Fatalf("adding an account set the default to %q", got)
	}

	out, err := e.gitMay(e.root, "clone", "-q", "git@github.com:dipu/notes.git", "notes")
	if err == nil || !strings.Contains(out, "no default account") || !strings.Contains(out, "gitswitch use <account>") {
		t.Fatalf("clone with no default and nobody to ask: err=%v\n%s", err, out)
	}

	e.gs("rule", "add", "owner:dipu", "me")
	e.clone("git@github.com:dipu/notes.git", "notes")
	if got := e.keysUsed(); len(got) == 0 || got[len(got)-1] != "me" {
		t.Errorf("a rule still decides with no default: keys %v", got)
	}

	// A repo can ask on every connection: rules no longer decide for it,
	// and with nobody to ask and no default it does not connect.
	notes := filepath.Join(e.root, "notes")
	e.gsIn(notes, "use", "--ask")
	if out := e.gsIn(notes, "status"); !strings.Contains(out, "asks on every pull and push") {
		t.Errorf("status:\n%s", out)
	}
	if out, err := e.gitMay(notes, "fetch", "-q"); err == nil || !strings.Contains(out, "asks every time") {
		t.Errorf("fetch in an ask-every-time repo with nobody to ask: err=%v\n%s", err, out)
	}
	// With a default, nobody answering means the default, for this
	// connection only: the repo keeps asking.
	e.gs("default", "client")
	e.resetLog()
	if out, err := e.gitMay(notes, "fetch", "-q"); err != nil {
		t.Fatalf("fetch with a default: %v\n%s", err, out)
	}
	if got := e.keysUsed(); len(got) == 0 || got[0] != "client" {
		t.Errorf("keys %v, want the default", got)
	}
	if got := e.cfg(notes, "gitswitch.account"); got != "ask" {
		t.Errorf("the repo must keep asking, gitswitch.account=%q", got)
	}
	e.gsIn(notes, "forget")
	if got := e.cfg(notes, "gitswitch.account"); got != "" {
		t.Errorf("forget left gitswitch.account=%q", got)
	}
}

// Signing in to GitHub checks the token with the API, keeps it out of git
// config, refuses another user's token, and follows the account when it is
// renamed or removed. The API is a fake one: gitswitch only talks to it
// under tests.
func TestSignInToGitHub(t *testing.T) {
	e := newEnv(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login := map[string]string{"Bearer tok-work": "dipu-work", "Bearer tok-other": "someone"}[r.Header.Get("Authorization")]
		if r.URL.Path != "/user" || login == "" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, `{"login":%q}`, login)
	}))
	defer api.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITHUB_API="+api.URL)
	signin := func(token string, args ...string) (string, error) {
		cmd := exec.Command(gsBin, append([]string{"signin"}, args...)...)
		cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader(token+"\n")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	type status struct {
		Account  string
		SignedIn bool
		Login    string
		Check    string
	}
	statuses := func() map[string]status {
		t.Helper()
		var rows []status
		must(t, json.Unmarshal([]byte(e.gs("signin", "--check", "--json")), &rows))
		m := map[string]status{}
		for _, r := range rows {
			m[r.Account] = r
		}
		return m
	}

	if st := statuses(); st["work"].SignedIn {
		t.Fatalf("nobody is signed in yet: %+v", st)
	}
	if out, err := signin("tok-bad", "work"); err == nil || !strings.Contains(out, "refused the token") {
		t.Fatalf("a bad token: %v\n%s", err, out)
	}
	if out, err := signin("tok-work", "work"); err != nil || !strings.Contains(out, "Signed in") {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	if st := statuses()["work"]; !st.SignedIn || st.Login != "dipu-work" || st.Check != "" {
		t.Fatalf("work after signin: %+v", st)
	}
	if u := e.cfg(e.root, "gitswitch-account.work.user"); u != "dipu-work" {
		t.Fatalf("the user must be learned from the token, got %q", u)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, ".gitconfig")); strings.Contains(string(b), "tok-work") {
		t.Fatal("the token must never be written to git config")
	}
	if out, err := signin("tok-other", "work"); err == nil || !strings.Contains(out, "logs in as dipu-work") {
		t.Fatalf("another user's token: %v\n%s", err, out)
	}

	e.gs("account", "rename", "work", "job")
	if st := statuses(); !st["job"].SignedIn || st["job"].Login != "dipu-work" {
		t.Fatalf("the token must follow the rename: %+v", st)
	}
	e.gs("signout", "job")
	if st := statuses(); st["job"].SignedIn {
		t.Fatalf("after signout: %+v", st)
	}
	if out, err := signin("tok-work", "job"); err != nil {
		t.Fatalf("signin again: %v\n%s", err, out)
	}
	e.gs("account", "remove", "job", "-y")
	e.gs("account", "add", "job", "--email", "job@example.com", "--key", e.key("work"))
	if st := statuses(); st["job"].SignedIn {
		t.Fatal("removing an account must remove its token")
	}
}

// Once an account is signed in, git gets its token for HTTPS remotes of
// that account's repos, and only those: every other repo and host still
// gets the helpers you had. Signing out and uninstalling take GitSwitch's
// helper out again.
func TestHTTPSCredentials(t *testing.T) {
	e := newEnv(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-work" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"login":"dipu-work"}`)
	}))
	defer api.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITHUB_API="+api.URL)
	// the helper you already had
	e.git("config", "--global", "credential.helper", `!f() { test "$1" = get && echo username=old && echo password=oldpass; }; f`)

	repoAt := func(name, acct string) string {
		dir := filepath.Join(e.root, name)
		e.git("init", "-q", dir)
		e.gitIn(dir, "remote", "add", "origin", "https://github.com/acme-corp/"+name+".git")
		e.gsIn(dir, "use", acct)
		return dir
	}
	api1, web := repoAt("api", "work"), repoAt("web", "client")
	fill := func(dir, host string) string {
		t.Helper()
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir, cmd.Env = dir, e.vars
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git credential fill in %s: %v\n%s", dir, err, out)
		}
		for _, l := range strings.Split(string(out), "\n") {
			if p, ok := strings.CutPrefix(l, "password="); ok {
				return p
			}
		}
		return ""
	}
	if p := fill(api1, "github.com"); p != "oldpass" {
		t.Fatalf("before signing in, your helper answers: %q", p)
	}

	cmd := exec.Command(gsBin, "signin", "work")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-work\n")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "HTTPS remotes of work's repos") {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	if p := fill(api1, "github.com"); p != "tok-work" {
		t.Fatalf("a repo of the signed-in account gets its token, got %q", p)
	}
	if p := fill(web, "github.com"); p != "oldpass" {
		t.Fatalf("a repo of another account keeps your helper, got %q", p)
	}
	if p := fill(e.root, "github.com"); p != "oldpass" {
		t.Fatalf("outside a repo, your helper answers, got %q", p)
	}
	if p := fill(api1, "gitlab.com"); p != "oldpass" {
		t.Fatalf("another host keeps your helper, got %q", p)
	}
	// While cloning, git points GIT_DIR at the new repo.
	cmd = exec.Command("git", "credential", "fill")
	cmd.Dir, cmd.Env = e.root, append(append([]string{}, e.vars...), "GIT_DIR="+filepath.Join(api1, ".git"))
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "password=tok-work") {
		t.Fatalf("with GIT_DIR: %v\n%s", err, out)
	}

	e.gs("signout", "work")
	if p := fill(api1, "github.com"); p != "oldpass" {
		t.Fatalf("after signout, your helper answers again: %q", p)
	}
	if out, _ := e.run(e.root, "git", "config", "--global", "--get-regexp", `^credential\.https`); strings.Contains(out, "gitswitch") {
		t.Fatalf("signout must remove GitSwitch's helper:\n%s", out)
	}
	cmd = exec.Command(gsBin, "signin", "work")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-work\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signin again: %v\n%s", err, out)
	}
	e.gs("uninstall", "--purge")
	if out, _ := e.run(e.root, "git", "config", "--global", "--get-regexp", `^credential\.https`); strings.Contains(out, "gitswitch") {
		t.Fatalf("uninstall must remove GitSwitch's helper:\n%s", out)
	}
	if v := e.cfg(e.root, "credential.helper"); !strings.Contains(v, "oldpass") {
		t.Fatalf("your own helper must stay: %q", v)
	}
}

// The GitHub browser lists what each signed-in account can see, marks
// what is cloned already, filters, clones as the account that sees the
// repo, and keeps showing the last list when GitHub cannot be reached.
func TestGitHubBrowser(t *testing.T) {
	e := newEnv(t)
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-work" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/user":
			fmt.Fprint(w, `{"login":"dipu-work"}`)
		case r.URL.Path == "/user/repos" && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", "<"+api.URL+"/user/repos?page=2>; rel=\"next\"")
			fmt.Fprint(w, `[{"full_name":"acme-corp/api","name":"api","owner":{"login":"acme-corp"},"description":"Billing API","private":true,"language":"Go",
				"ssh_url":"git@github.com:acme-corp/api.git","clone_url":"https://github.com/acme-corp/api.git"}]`)
		case r.URL.Path == "/user/repos":
			fmt.Fprint(w, `[{"full_name":"acme-corp/web","name":"web","owner":{"login":"acme-corp"},"language":"TypeScript",
				"ssh_url":"git@github.com:acme-corp/web.git","clone_url":"https://github.com/acme-corp/web.git"}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITHUB_API="+api.URL)

	if _, err := e.run(e.root, gsBin, "github", "list"); err == nil {
		t.Fatal("without a signed-in account, the browser says how to sign in")
	}
	work := filepath.Join(e.root, "work")
	e.gitIn(work, "clone", "-q", "git@github.com:acme-corp/api.git", "api")
	e.git("config", "--global", "--add", "gitswitch.scanFolder", work)
	cmd := exec.Command(gsBin, "signin", "work")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-work\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signin: %v\n%s", err, out)
	}

	type ghRepo struct {
		FullName string
		Account  string
		Private  bool
		Cloned   []string
	}
	list := func(args ...string) []ghRepo {
		t.Helper()
		var b struct{ Repos []ghRepo }
		must(t, json.Unmarshal([]byte(e.gs(append([]string{"github", "list", "--json"}, args...)...)), &b))
		return b.Repos
	}
	rs := list()
	if len(rs) != 2 || rs[0].FullName != "acme-corp/api" || !rs[0].Private || rs[0].Account != "work" {
		t.Fatalf("both pages, as work: %+v", rs)
	}
	if len(rs[0].Cloned) != 1 || filepath.Base(rs[0].Cloned[0]) != "api" || len(rs[1].Cloned) != 0 {
		t.Fatalf("api is cloned, web is not: %+v", rs)
	}
	if rs := list("--not-cloned"); len(rs) != 1 || rs[0].FullName != "acme-corp/web" {
		t.Fatalf("--not-cloned: %+v", rs)
	}
	if rs := list("billing", "--private"); len(rs) != 1 || rs[0].FullName != "acme-corp/api" {
		t.Fatalf("search: %+v", rs)
	}

	e.resetLog()
	e.gs("github", "clone", "acme-corp/web")
	web := filepath.Join(e.root, "web")
	if got := e.cfg(web, "gitswitch.account"); got != "work" {
		t.Fatalf("cloned as the account that sees it: %q", got)
	}
	if k := e.keysUsed(); len(k) == 0 || !strings.HasSuffix(k[0], "work") {
		t.Fatalf("the clone must use work's key: %v", k)
	}

	api.Close() // GitHub is gone: the last list is still shown
	if rs := list(); len(rs) != 2 {
		t.Fatalf("cached list: %+v", rs)
	}
	if out, err := e.run(e.root, gsBin, "github", "list", "--refresh"); err != nil || !strings.Contains(out, "could not read its repos") {
		t.Fatalf("a failed refresh says so and keeps the list: %v\n%s", err, out)
	}
}

// Update with rebase: only when both sides have new commits, never with
// uncommitted changes or your own merges, nothing changed on a clash, and
// undone exactly.
func TestUpdateWithRebase(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	e.clone("git@github.com:acme-corp/api.git", "other")
	api, other := filepath.Join(e.root, "api"), filepath.Join(e.root, "other")
	e.commitFile(other, "theirs.txt", "theirs\n", "their work")
	e.gitIn(other, "push", "-q")
	mine := e.commitFile(api, "mine.txt", "mine\n", "my work")

	e.gsErr(api, "gitswitch update --rebase", "update")
	if e.gitIn(api, "rev-parse", "HEAD") != mine {
		t.Fatal("a refused update must change nothing")
	}

	// Uncommitted changes to tracked files: refused before anything runs.
	must(t, os.WriteFile(filepath.Join(api, "mine.txt"), []byte("edited\n"), 0o644))
	e.gsErr(api, "commit or park them first", "update", "--rebase")
	e.gitIn(api, "checkout", "--", "mine.txt")

	e.gsIn(api, "update", "--rebase")
	log := e.gitIn(api, "log", "--format=%s")
	if !strings.HasPrefix(log, "my work\ntheir work") {
		t.Fatalf("my commit must be on top of theirs:\n%s", log)
	}
	if n := e.gitIn(api, "rev-list", "--count", "--merges", "HEAD"); n != "0" {
		t.Fatalf("no merge commit: %s", n)
	}
	e.gsIn(api, "undo", "-y")
	if got := e.gitIn(api, "rev-parse", "HEAD"); got != mine {
		t.Fatalf("undo puts my commit back as it was: %s, want %s", got, mine)
	}

	// A clash: the rebase is aborted and nothing changes.
	e.commitFile(other, "same.txt", "theirs\n", "their line")
	e.gitIn(other, "push", "-q")
	clash := e.commitFile(api, "same.txt", "mine\n", "my line")
	e.gs("set", "pull", "rebase")
	e.gsErr(api, "nothing changed", "update")
	if got := e.gitIn(api, "rev-parse", "HEAD"); got != clash {
		t.Fatalf("a clash must change nothing: HEAD %s, want %s", got, clash)
	}
	if out := e.gitIn(api, "status", "--porcelain"); out != "" {
		t.Fatalf("no half-done rebase or changed files:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(api, ".git", "rebase-merge")); err == nil {
		t.Fatal("the rebase must be aborted")
	}
}

// gitswitch prs lists each signed-in account's pull requests with what
// stands out about its own, and keeps the last inbox when GitHub is gone.
func TestReviewInbox(t *testing.T) {
	e := newEnv(t)
	var asked string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-work" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"dipu-work"}`)
		case "/graphql":
			var body struct{ Variables map[string]string }
			must(t, json.NewDecoder(r.Body).Decode(&body))
			asked = body.Variables["toReview"] + " | " + body.Variables["mine"]
			fmt.Fprint(w, `{"data":{
			  "toReview":{"nodes":[{"number":7,"title":"Add billing","url":"https://github.com/acme/api/pull/7","author":{"login":"naser"},"repository":{"nameWithOwner":"acme/api"},"reviewDecision":"REVIEW_REQUIRED","mergeable":"MERGEABLE","commits":{"nodes":[]}}]},
			  "mine":{"nodes":[{"number":3,"title":"Fix login","url":"https://github.com/acme/web/pull/3","author":{"login":"dipu-work"},"repository":{"nameWithOwner":"acme/web"},"reviewDecision":"CHANGES_REQUESTED","mergeable":"CONFLICTING","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}}]},
			  "reviewed":{"nodes":[]}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITHUB_API="+api.URL)

	if _, err := e.run(e.root, gsBin, "prs"); err == nil {
		t.Fatal("without a signed-in account, prs says how to sign in")
	}
	cmd := exec.Command(gsBin, "signin", "work")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-work\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	out := e.gs("prs")
	for _, want := range []string{"Waiting for your review (1)", "acme/api#7  Add billing  · naser", "Waiting for review",
		"Your pull requests (1)", "! Conflicts", "! Checks failing", "! Changes requested", "Reviewed by you lately (0)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prs output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(asked, "review-requested:dipu-work") || !strings.Contains(asked, "author:dipu-work") {
		t.Fatalf("searched as %q", asked)
	}
	var inboxes []struct {
		Account string
		Mine    []struct {
			Number int
			Chips  []struct{ Kind string }
		}
	}
	must(t, json.Unmarshal([]byte(e.gs("prs", "--json")), &inboxes))
	if len(inboxes) != 1 || inboxes[0].Account != "work" || len(inboxes[0].Mine) != 1 || len(inboxes[0].Mine[0].Chips) != 3 {
		t.Fatalf("--json: %+v", inboxes)
	}

	api.Close() // GitHub is gone: the last inbox is still shown
	if out, err := e.run(e.root, gsBin, "prs", "--refresh"); err != nil || !strings.Contains(out, "could not read it") || !strings.Contains(out, "Add billing") {
		t.Fatalf("a failed refresh keeps the inbox: %v\n%s", err, out)
	}
}

// gitswitch branch pr reads the open pull request of the current branch
// with the repo's account, and is quiet when there is none.
func TestBranchPullRequest(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")
	e.gsIn(api, "branch", "new", "feature/x")

	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-client" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"login":"dipu-client"}`)
		case "/graphql":
			var body struct{ Variables map[string]string }
			must(t, json.NewDecoder(r.Body).Decode(&body))
			asked = body.Variables["owner"] + "/" + body.Variables["name"] + "@" + body.Variables["branch"]
			if body.Variables["branch"] != "feature/x" {
				fmt.Fprint(w, `{"data":{"repository":{"pullRequests":{"nodes":[]}}}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"repository":{"pullRequests":{"nodes":[{"number":42,"title":"Billing","url":"https://github.com/acme-corp/api/pull/42",
			  "author":{"login":"dipu-client"},"repository":{"nameWithOwner":"acme-corp/api"},"headRepository":{"nameWithOwner":"acme-corp/api"},
			  "reviewDecision":"APPROVED","mergeable":"MERGEABLE","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]},
			  "latestOpinionatedReviews":{"nodes":[{"state":"APPROVED"}]}}]}}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITHUB_API="+srv.URL)

	e.gsErr(api, "sign client in", "branch", "pr")
	cmd := exec.Command(gsBin, "signin", "client")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-client\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	out := e.gsIn(api, "branch", "pr")
	if !strings.Contains(out, "PR #42 · 1 approval · checks passing") || !strings.Contains(out, "https://github.com/acme-corp/api/pull/42") || asked != "acme-corp/api@feature/x" {
		t.Fatalf("branch pr (asked %q):\n%s", asked, out)
	}
	var got struct {
		Found   bool
		Account string
		Summary string
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "branch", "pr", "--json")), &got))
	if !got.Found || got.Account != "client" || got.Summary == "" {
		t.Fatalf("--json = %+v", got)
	}
	e.gitIn(api, "switch", "-q", "main")
	if out := e.gsIn(api, "branch", "pr"); !strings.Contains(out, "main has no open pull request in acme-corp/api") {
		t.Fatalf("a branch without one:\n%s", out)
	}
}

// gitswitch fetch reads the remote's new commits with the repo's account,
// so "to pull" is right, and changes no branch or file.
func TestFetch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")

	if out := e.gsIn(api, "fetch"); !strings.Contains(out, "nothing new") {
		t.Fatalf("nothing waiting:\n%s", out)
	}
	e.clone("git@github.com:acme-corp/api.git", "other")
	other := filepath.Join(e.root, "other")
	e.commitFile(other, "o.txt", "o\n", "from other")
	e.commitFile(other, "p.txt", "p\n", "from other 2")
	e.gitIn(other, "push", "-q")

	head := e.gitIn(api, "rev-parse", "HEAD")
	if b := e.gsIn(api, "status", "--json"); strings.Contains(b, `"behind":2`) {
		t.Fatalf("behind is known before the fetch: %s", b)
	}
	e.resetLog()
	out := e.gsIn(api, "fetch")
	if !strings.Contains(out, "2 new commit(s) on origin/main") || !strings.Contains(out, "gitswitch update") {
		t.Fatalf("fetch:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("fetched with keys %v, want the repo's account", k)
	}
	if e.gitIn(api, "rev-parse", "HEAD") != head || e.gitIn(api, "status", "--porcelain") != "" {
		t.Fatal("fetch moved the branch or touched files")
	}
	var f struct {
		Gained int
		Head   struct{ Behind int }
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "fetch", "--json")), &f))
	if f.Head.Behind != 2 || f.Gained != 0 {
		t.Fatalf("--json after a second fetch: %+v (still 2 to pull, none newly gained)", f)
	}
	if out := e.gsIn(api, "--dry-run", "fetch"); strings.Contains(out, "Fetched") {
		t.Fatalf("dry run fetched:\n%s", out)
	}
}

// gitswitch set fetch-on-open turns reading the remote's new commits when
// History opens on or off, in every repo; off by default.
func TestSetFetchOnOpen(t *testing.T) {
	e := newEnv(t)
	if v, err := e.gitMay(e.root, "config", "--global", "--get", "gitswitch.fetchOnOpen"); err == nil {
		t.Fatalf("on by default: %q", v)
	}
	e.gsErr(e.root, "fetch-on-open on|off", "set", "fetch-on-open", "maybe")
	if out := e.gs("--dry-run", "set", "fetch-on-open", "on"); strings.Contains(out, "History will read") {
		t.Fatalf("dry run says it did it:\n%s", out)
	}
	if _, err := e.gitMay(e.root, "config", "--global", "--get", "gitswitch.fetchOnOpen"); err == nil {
		t.Fatal("a dry run saved it")
	}
	if out := e.gs("set", "fetch-on-open", "on"); !strings.Contains(out, "History will read the remote's new commits") || e.cfg(e.root, "gitswitch.fetchOnOpen") != "true" {
		t.Fatalf("on:\n%s", out)
	}
	if out := e.gs("set", "fetch-on-open", "off"); !strings.Contains(out, "without fetching") {
		t.Fatalf("off:\n%s", out)
	}
	if v, err := e.gitMay(e.root, "config", "--global", "--get", "gitswitch.fetchOnOpen"); err == nil {
		t.Fatalf("off leaves the key behind: %q", v)
	}
	e.gs("set", "fetch-on-open", "off") // turning it off twice is fine
}

// A GitLab account signs in with a token and gets the same help as a
// GitHub one: its merge requests, its repos (nested groups included), the
// branch's merge request, and the token for HTTPS remotes.
func TestGitLabAccount(t *testing.T) {
	e := newEnv(t)
	e.gs("account", "add", "lab", "--email", "dipu@lab.example", "--key", e.key("work"), "--host", "gitlab.com")
	e.serverRepo("acme/team/api")
	e.clone("git@gitlab.com:acme/team/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.gsIn(api, "use", "lab")
	e.gsIn(api, "branch", "new", "billing")

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-gl" {
			w.WriteHeader(401)
			return
		}
		q := r.URL.Query()
		switch path := r.URL.EscapedPath(); {
		case path == "/user":
			fmt.Fprint(w, `{"username":"ana","name":"Ana"}`)
		case path == "/personal_access_tokens/self":
			fmt.Fprint(w, `{"scopes":["read_api"],"expires_at":"2027-02-03"}`)
		case path == "/projects":
			fmt.Fprint(w, `[{"path_with_namespace":"acme/team/api","path":"api","namespace":{"full_path":"acme/team"},"visibility":"private","default_branch":"main",
			  "ssh_url_to_repo":"git@gitlab.com:acme/team/api.git","http_url_to_repo":"https://gitlab.com/acme/team/api.git","web_url":"https://gitlab.com/acme/team/api"}]`)
		case path == "/merge_requests":
			asked = append(asked, q.Get("reviewer_username")+q.Get("author_username"))
			switch {
			case q.Get("reviewer_username") == "ana":
				fmt.Fprint(w, `[{"iid":7,"title":"Add billing","author":{"username":"naser"},"references":{"full":"acme/team/api!7"},"detailed_merge_status":"not_approved"}]`)
			case q.Get("author_username") == "ana":
				fmt.Fprint(w, `[{"iid":4,"title":"Ship it","author":{"username":"ana"},"references":{"full":"acme/team/api!4"},"detailed_merge_status":"mergeable"}]`)
			default:
				fmt.Fprint(w, `[]`)
			}
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests":
			fmt.Fprint(w, `[{"iid":42,"source_project_id":5,"target_project_id":5}]`)
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests/42":
			fmt.Fprint(w, `{"iid":42,"title":"Billing","web_url":"https://gitlab.com/acme/team/api/-/merge_requests/42","author":{"username":"ana"},"references":{"full":"acme/team/api!42"},
			  "detailed_merge_status":"mergeable","head_pipeline":{"status":"success"}}`)
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests/42/approvals":
			fmt.Fprint(w, `{"approved_by":[{"user":{"username":"naser"}},{"user":{"username":"sam"}}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITLAB_API="+srv.URL)

	e.gsErr(api, "sign lab in to GitLab", "branch", "pr")
	cmd := exec.Command(gsBin, "signin", "lab")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-gl\n")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "Signed in: lab uses ana's token on gitlab.com") {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	if out := e.gs("signin", "--check"); !strings.Contains(out, "signed in as ana, token works") {
		t.Fatalf("signin --check:\n%s", out)
	}
	out := e.gs("prs")
	for _, want := range []string{"acme/team/api!7  Add billing  · naser", "Waiting for review", "acme/team/api!4  Ship it", "Ready to merge"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prs lacks %q:\n%s", want, out)
		}
	}
	if out := e.gsIn(api, "branch", "pr"); !strings.Contains(out, "MR !42 · 2 approvals · checks passing") || !strings.Contains(out, "merge_requests/42") {
		t.Fatalf("branch pr:\n%s", out)
	}
	if out := e.gs("github", "list"); !strings.Contains(out, "acme/team/api") {
		t.Fatalf("repo list:\n%s", out)
	}

	// git gets the token for an HTTPS remote of this account, as the host's user.
	fill := exec.Command("git", "credential", "fill")
	fill.Dir, fill.Env = api, e.vars
	fill.Stdin = strings.NewReader("protocol=https\nhost=gitlab.com\n\n")
	if out, err := fill.CombinedOutput(); err != nil || !strings.Contains(string(out), "username=ana") || !strings.Contains(string(out), "password=tok-gl") {
		t.Fatalf("git credential fill: %v\n%s", err, out)
	}
}

// A Codeberg (Gitea or Forgejo) account: token sign-in, its pull requests,
// the branch's pull request and the token for HTTPS remotes.
func TestCodebergAccount(t *testing.T) {
	e := newEnv(t)
	e.gs("account", "add", "cb", "--email", "dipu@cb.example", "--key", e.key("work"), "--host", "codeberg.org")
	e.serverRepo("ana/notes")
	e.clone("git@codeberg.org:ana/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")
	e.gsIn(notes, "use", "cb")
	e.gsIn(notes, "branch", "new", "billing")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token tok-cb" {
			w.WriteHeader(401)
			return
		}
		q := r.URL.Query()
		switch p := r.URL.Path; {
		case p == "/user":
			fmt.Fprint(w, `{"login":"ana","full_name":"Ana"}`)
		case p == "/user/repos":
			fmt.Fprint(w, `[{"full_name":"ana/notes","name":"notes","owner":{"login":"ana"},"private":true,"ssh_url":"git@codeberg.org:ana/notes.git","clone_url":"https://codeberg.org/ana/notes.git","html_url":"https://codeberg.org/ana/notes"}]`)
		case p == "/repos/issues/search" && q.Get("review_requested") == "true":
			fmt.Fprint(w, `[{"number":7,"title":"Add billing","user":{"login":"naser"},"repository":{"full_name":"acme/api"},"pull_request":{}}]`)
		case p == "/repos/issues/search":
			fmt.Fprint(w, `[]`)
		case p == "/repos/ana/notes/pulls":
			fmt.Fprint(w, `[{"number":42,"title":"Billing","html_url":"https://codeberg.org/ana/notes/pulls/42","user":{"login":"ana"},"mergeable":true,"head":{"ref":"billing","sha":"cafe","repo":{"full_name":"ana/notes"}}}]`)
		case p == "/repos/ana/notes/pulls/42/reviews":
			fmt.Fprint(w, `[{"state":"APPROVED","user":{"login":"naser"}}]`)
		case p == "/repos/ana/notes/commits/cafe/status":
			fmt.Fprint(w, `{"state":"success","total_count":1}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e.vars = append(e.vars, "GITSWITCH_TEST_GITEA_API="+srv.URL)

	e.gsErr(notes, "sign cb in to Codeberg", "branch", "pr")
	cmd := exec.Command(gsBin, "signin", "cb")
	cmd.Dir, cmd.Env, cmd.Stdin = e.root, e.vars, strings.NewReader("tok-cb\n")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "Signed in: cb uses ana's token on codeberg.org") {
		t.Fatalf("signin: %v\n%s", err, out)
	}
	if out := e.gs("prs"); !strings.Contains(out, "acme/api#7  Add billing  · naser") || !strings.Contains(out, "Waiting for review") {
		t.Fatalf("prs:\n%s", out)
	}
	if out := e.gsIn(notes, "branch", "pr"); !strings.Contains(out, "PR #42 · 1 approval · checks passing") {
		t.Fatalf("branch pr:\n%s", out)
	}
	if out := e.gs("github", "list"); !strings.Contains(out, "ana/notes") {
		t.Fatalf("repo list:\n%s", out)
	}
	fill := exec.Command("git", "credential", "fill")
	fill.Dir, fill.Env = notes, e.vars
	fill.Stdin = strings.NewReader("protocol=https\nhost=codeberg.org\n\n")
	if out, err := fill.CombinedOutput(); err != nil || !strings.Contains(string(out), "username=ana") || !strings.Contains(string(out), "password=tok-cb") {
		t.Fatalf("git credential fill: %v\n%s", err, out)
	}
}

// The changes commands from a subfolder: paths are relative to it, as
// git takes them; discard asks for -y without a terminal and keeps the
// changes in the stash.
func TestChangesStageUnstageDiscard(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	sub := filepath.Join(api, "src")
	must(t, os.MkdirAll(sub, 0o755))
	must(t, os.WriteFile(filepath.Join(sub, "main go.txt"), []byte("hello\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(api, "notes.txt"), []byte("notes\n"), 0o644))

	if out := e.gsIn(api, "changes"); !strings.Contains(out, "Not staged (2)") || !strings.Contains(out, "src/main go.txt") {
		t.Fatalf("changes:\n%s", out)
	}
	if out := e.gsIn(sub, "diff", "main go.txt"); !strings.Contains(out, "+hello") {
		t.Fatalf("diff from a subfolder:\n%s", out)
	}
	if out := e.gsIn(sub, "stage", "main go.txt"); !strings.Contains(out, "Staged src/main go.txt") {
		t.Fatalf("stage:\n%s", out)
	}
	if got := e.gitIn(api, "diff", "--cached", "--name-only"); got != "src/main go.txt" {
		t.Fatalf("staged %q", got)
	}
	if out := e.gsIn(api, "changes", "--json"); !strings.Contains(out, `"staged":"A"`) {
		t.Fatalf("changes --json:\n%s", out)
	}
	if out := e.gsIn(api, "diff", "--staged"); !strings.Contains(out, "+hello") {
		t.Fatalf("diff --staged:\n%s", out)
	}
	e.gsIn(api, "unstage", "--all")
	if got := e.gitIn(api, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("still staged %q", got)
	}
	if out, err := e.run(api, gsBin, "stage", "../outside.txt"); err == nil || !strings.Contains(out, "outside the repo") {
		t.Fatalf("stage outside the repo: %v\n%s", err, out)
	}
	if out, err := e.run(api, gsBin, "discard", "notes.txt"); err == nil || !strings.Contains(out, "-y") {
		t.Fatalf("discard without -y and no terminal: %v\n%s", err, out)
	}
	if out := e.gsIn(api, "discard", "--all", "-y"); !strings.Contains(out, "gitswitch discard: main") {
		t.Fatalf("discard:\n%s", out)
	}
	if got := e.gitIn(api, "status", "--porcelain"); got != "" {
		t.Fatalf("left after discard: %q", got)
	}
	if got := e.gitIn(api, "stash", "list"); !strings.Contains(got, "gitswitch discard: main") {
		t.Fatalf("stash list %q", got)
	}
	if out := e.gsIn(api, "changes"); !strings.Contains(out, "No uncommitted changes") {
		t.Fatalf("changes after discard:\n%s", out)
	}
}

// Ignore from a subfolder: the path is relative to it; a pattern is
// written once; a tracked file is reported and only untracked with
// --untrack, the file kept; --local writes .git/info/exclude.
func TestIgnore(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	sub := filepath.Join(api, "src")
	must(t, os.MkdirAll(sub, 0o755))
	must(t, os.WriteFile(filepath.Join(api, "secret.env"), []byte("k=v\n"), 0o644))
	e.gsIn(api, "commit", "-m", "Add secret", "--all")
	must(t, os.WriteFile(filepath.Join(sub, "debug.log"), []byte("x\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(sub, "*.tmp"), []byte("x\n"), 0o644))

	if out := e.gsIn(sub, "ignore", "debug.log"); !strings.Contains(out, "Added /src/debug.log to .gitignore") || !strings.Contains(out, "Git now ignores src/debug.log") {
		t.Fatalf("ignore:\n%s", out)
	}
	if out := e.gsIn(sub, "ignore", "debug.log"); !strings.Contains(out, ".gitignore already has /src/debug.log") {
		t.Fatalf("ignore again:\n%s", out)
	}
	if out := e.gsIn(sub, "ignore", "*.tmp", "--ext", "--local"); !strings.Contains(out, "Added *.tmp to .git/info/exclude") {
		t.Fatalf("ignore --ext --local:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(api, ".gitignore"))
	must(t, err)
	if string(b) != "/src/debug.log\n" {
		t.Fatalf(".gitignore = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(api, ".git", "info", "exclude")); !strings.HasSuffix(string(b), "\n*.tmp\n") {
		t.Fatalf("info/exclude = %q", b)
	}
	if out := e.gsIn(api, "ignore", "secret.env"); !strings.Contains(out, "Git still tracks secret.env") || !strings.Contains(out, "gitswitch ignore secret.env --untrack") {
		t.Fatalf("ignore a tracked file:\n%s", out)
	}
	if got := e.gitIn(api, "ls-files", "secret.env"); got != "secret.env" {
		t.Fatalf("untracked without --untrack: %q", got)
	}
	if out := e.gsIn(api, "ignore", "secret.env", "--untrack", "--json"); !strings.Contains(out, `"untracked":["secret.env"]`) || !strings.Contains(out, `"ignored":true`) {
		t.Fatalf("ignore --untrack --json:\n%s", out)
	}
	if got := e.gitIn(api, "ls-files", "secret.env"); got != "" {
		t.Fatalf("still tracked: %q", got)
	}
	if _, err := os.Stat(filepath.Join(api, "secret.env")); err != nil {
		t.Fatalf("--untrack removed the file: %v", err)
	}
	if out, err := e.run(api, gsBin, "ignore", "../outside.txt"); err == nil || !strings.Contains(out, "outside the repo") {
		t.Fatalf("ignore outside the repo: %v\n%s", err, out)
	}
	if out, err := e.run(api, gsBin, "ignore", "secret.env", "--ext", "--folder"); err == nil || !strings.Contains(out, "usage") {
		t.Fatalf("two kinds: %v\n%s", err, out)
	}
}

// Delete, rename and set the upstream of a branch, and make, push and
// delete tags, with real git through the real program. Delete needs
// --force for commits that are nowhere else; delete and rename are undone.
func TestBranchAndTagManagement(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")

	// done: merged into main. wip: one commit of its own, never pushed.
	e.gitIn(api, "branch", "done")
	e.gitIn(api, "switch", "-q", "-c", "wip")
	wip := e.commitFile(api, "w.txt", "w\n", "wip")
	e.gitIn(api, "switch", "-q", "main")

	e.gsErr(api, "current branch", "branch", "delete", "main", "-y")
	e.gsErr(api, "add -y", "branch", "delete", "done") // no terminal to ask in
	e.gsErr(api, "wip has 1 commit that is on no other branch or remote: once the branch is deleted, only Undo can bring it back. Delete it anyway only with force: gitswitch branch delete wip --force", "branch", "delete", "wip", "-y")
	if out := e.gsIn(api, "branch", "delete", "done", "-y"); !strings.Contains(out, "Deleted the branch done") {
		t.Fatalf("delete done:\n%s", out)
	}
	out := e.gsIn(api, "branch", "delete", "wip", "--force", "-y")
	if !strings.Contains(out, "1 commit(s) were only on it") || e.gitIn(api, "branch", "--list", "wip") != "" {
		t.Fatalf("delete wip:\n%s", out)
	}
	if out := e.gsIn(api, "undo", "--show"); !strings.Contains(out, "Brings back the deleted branches: wip") {
		t.Fatalf("undo --show:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if e.gitIn(api, "rev-parse", "wip") != wip {
		t.Fatal("undo did not bring wip back")
	}

	// main pulls from origin/main; renamed, it still does; undo renames it back.
	e.gsErr(api, "no remote branch \"origin/nope\"", "branch", "upstream", "origin/nope")
	if out := e.gsIn(api, "branch", "upstream", "origin/main"); !strings.Contains(out, "main now pulls from origin/main") {
		t.Fatalf("upstream:\n%s", out)
	}
	e.gsErr(api, "can't be a branch name", "branch", "rename", "bad..name")
	e.gsErr(api, "already a branch wip", "branch", "rename", "wip")
	if out := e.gsIn(api, "branch", "rename", "trunk"); !strings.Contains(out, "Renamed the branch main to trunk") || !strings.Contains(out, "still pulls from origin/main") {
		t.Fatalf("rename:\n%s", out)
	}
	if e.gitIn(api, "branch", "--show-current") != "trunk" || e.gitIn(api, "rev-parse", "--abbrev-ref", "trunk@{upstream}") != "origin/main" {
		t.Fatal("trunk is not current or lost its upstream")
	}
	e.gsIn(api, "branch", "rename", "wip", "later")
	e.gsIn(api, "undo", "-y") // later back to wip
	if e.gitIn(api, "rev-parse", "wip") != wip || e.gitIn(api, "branch", "--show-current") != "trunk" {
		t.Fatal("undo did not rename later back to wip")
	}
	if out := e.gsIn(api, "branch", "upstream", "--unset"); !strings.Contains(out, "trunk no longer pulls from origin/main") {
		t.Fatalf("unset:\n%s", out)
	}

	// Tags: annotated and lightweight, listed, pushed with the client key.
	if out := e.gsIn(api, "tag"); !strings.Contains(out, "No tags yet") {
		t.Fatalf("no tags:\n%s", out)
	}
	if out := e.gsIn(api, "tag", "new", "v1", "-m", "Release 1"); !strings.Contains(out, "as v1 (annotated)") || e.gitIn(api, "cat-file", "-t", "v1") != "tag" {
		t.Fatalf("tag new v1:\n%s", out)
	}
	if out := e.gsIn(api, "tag", "new", "wip-mark", "wip"); !strings.Contains(out, "(lightweight)") || e.gitIn(api, "rev-parse", "wip-mark") != wip {
		t.Fatalf("tag new wip-mark:\n%s", out)
	}
	e.gsErr(api, "already a tag v1", "tag", "new", "v1")
	if out := e.gsIn(api, "tag", "list", "--json"); !strings.Contains(out, `"v1"`) || !strings.Contains(out, `"wip-mark"`) {
		t.Fatalf("tag list --json:\n%s", out)
	}
	e.resetLog()
	if out := e.gsIn(api, "tag", "push", "v1"); !strings.Contains(out, "Pushed the tag v1 to origin as client") {
		t.Fatalf("tag push:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("keys used: %v", k)
	}
	bare := filepath.Join(e.server, "acme-corp", "api.git")
	if e.gitIn(e.root, "--git-dir", bare, "cat-file", "-t", "v1") != "tag" {
		t.Fatal("the server has no v1")
	}
	e.gsErr(api, "add -y", "tag", "delete", "v1")
	e.gsIn(api, "tag", "delete", "v1", "-y")
	if e.gitIn(api, "tag", "--list", "v1") != "" {
		t.Fatal("v1 is still here")
	}
	// A v1 at another commit: refused, the server's v1 stays.
	e.gsIn(api, "tag", "new", "v1", "wip")
	e.gsErr(api, "never forces", "tag", "push", "v1")
	if e.gitIn(e.root, "--git-dir", bare, "rev-parse", "v1^{commit}") == wip {
		t.Fatal("the server's v1 was overwritten")
	}
}

// The commit box's extras with real git: --signoff and --co-author add
// trailers with the account's email, the signoff setting signs every
// commit, --recent lists your messages, and a refused commit keeps its
// message as the draft until a commit succeeds.
func TestCommitSignoffCoAuthorsRecentDraft(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	trailers := func() string { return e.gitIn(api, "log", "-1", "--format=%(trailers:only,unfold)") }
	me := e.cfg(api, "user.name") + " <dipu@client.example>"

	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("a\n"), 0o644))
	e.gsErr(api, "Name <email>", "commit", "-a", "-m", "x", "--co-author", "Mate")
	e.gsIn(api, "commit", "-a", "-m", "Add a\n\nWith a body.", "--signoff", "--co-author", "Mate <mate@acme.example>", "--co-author=Ann <ann@acme.example>")
	if got := trailers(); got != "Co-authored-by: Mate <mate@acme.example>\nCo-authored-by: Ann <ann@acme.example>\nSigned-off-by: "+me {
		t.Fatalf("trailers = %q\n%s", got, e.gitIn(api, "log", "-1", "--format=%B"))
	}
	if got := e.gitIn(api, "log", "-1", "--format=%b"); !strings.HasPrefix(got, "With a body.\n\nCo-authored-by:") {
		t.Fatalf("body = %q", got)
	}

	// The setting signs every commit; --no-signoff leaves one out.
	e.gsIn(api, "set", "signoff", "on", "--repo")
	must(t, os.WriteFile(filepath.Join(api, "b.txt"), []byte("b\n"), 0o644))
	e.gsIn(api, "commit", "-a", "-m", "Add b")
	if got := trailers(); got != "Signed-off-by: "+me {
		t.Fatalf("setting: trailers = %q", got)
	}
	must(t, os.WriteFile(filepath.Join(api, "c.txt"), []byte("c\n"), 0o644))
	e.gsIn(api, "commit", "-a", "-m", "Add c", "--no-signoff")
	if got := trailers(); got != "" {
		t.Fatalf("--no-signoff: trailers = %q", got)
	}
	if got := e.gitIn(api, "config", "--local", "gitswitch.signoff"); got != "true" {
		t.Fatalf("gitswitch.signoff = %q", got)
	}
	e.gsIn(api, "set", "signoff", "global", "--repo")
	if out, err := e.gitMay(api, "config", "--local", "gitswitch.signoff"); err == nil {
		t.Fatalf("still set: %q", out)
	}

	// A refused commit keeps its message; --recent shows it and your
	// messages, newest first, without the trailers.
	e.gsErr(api, "nothing to commit", "commit", "-m", "Lost words")
	if b, err := os.ReadFile(filepath.Join(api, ".git", "GITSWITCH_MSG")); err != nil || strings.TrimSpace(string(b)) != "Lost words" {
		t.Fatalf("draft file = %q %v", b, err)
	}
	var got struct {
		Recent []struct{ Subject, Message string }
		Draft  string
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "commit", "--recent", "--json")), &got))
	if got.Draft != "Lost words" || len(got.Recent) != 3 || got.Recent[0].Subject != "Add c" || got.Recent[2].Message != "Add a\n\nWith a body." {
		t.Fatalf("--recent = %+v", got)
	}
	if out := e.gsIn(api, "commit", "--recent"); !strings.Contains(out, "Lost words") || !strings.Contains(out, "Add b") || strings.Contains(out, "Initial") {
		t.Fatalf("--recent:\n%s", out)
	}
	must(t, os.WriteFile(filepath.Join(api, "d.txt"), []byte("d\n"), 0o644))
	e.gsIn(api, "commit", "-a", "-m", "Lost words")
	if _, err := os.Stat(filepath.Join(api, ".git", "GITSWITCH_MSG")); !os.IsNotExist(err) {
		t.Fatalf("draft kept after the commit: %v", err)
	}
}

// Merge with real git through the command line: a fast-forward and its
// undo, a merge that stops on a clash, keeping theirs and continuing,
// undo of the merge commit, and abort.
func TestMergeContinueAbort(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	head := func() string { return e.gitIn(api, "rev-parse", "HEAD") }
	start := e.commitFile(api, "a.txt", "base\n", "base")
	e.gitIn(api, "switch", "-q", "-c", "side")
	sideTip := e.commitFile(api, "a.txt", "side\n", "side")
	e.gitIn(api, "switch", "-q", "main")

	// Fast-forward: the plan says so, -y merges, undo moves main back.
	if out := e.gsIn(api, "merge", "--plan", "side"); !strings.Contains(out, "can fast-forward to side") {
		t.Fatalf("plan:\n%s", out)
	}
	var plan map[string]any
	must(t, json.Unmarshal([]byte(e.gsIn(api, "merge", "--plan", "side", "--json")), &plan))
	if plan["fastForward"] != true || plan["hash"] != sideTip {
		t.Fatalf("plan --json: %v", plan)
	}
	e.gsErr(api, "add -y", "merge", "side")
	if out := e.gsIn(api, "merge", "side", "--ff-only", "-y"); !strings.Contains(out, "fast-forwarded") || head() != sideTip {
		t.Fatalf("ff merge: %s\n%s", head(), out)
	}
	if out := e.gsIn(api, "undo", "--show"); !strings.Contains(out, "before the fast-forward") {
		t.Fatalf("undo --show:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if head() != start || status() != "" {
		t.Fatalf("undo ff: %s %q", head(), status())
	}

	// Gone apart with a clash: the merge stops, conflict --theirs, continue.
	mainTip := e.commitFile(api, "a.txt", "main\n", "main")
	e.gsErr(api, "the merge stopped: 1 file(s) have conflicts", "merge", "side", "-y")
	if out := e.gsIn(api, "conflict"); !strings.Contains(out, "The merge stopped") || !strings.Contains(out, "a.txt") {
		t.Fatalf("conflict list:\n%s", out)
	}
	var c map[string]any
	must(t, json.Unmarshal([]byte(e.gsIn(api, "conflict", "a.txt", "--json")), &c))
	if c["base"] != "base\n" || c["ours"] != "main\n" || c["theirs"] != "side\n" || c["markers"] != true {
		t.Fatalf("conflict --json: %v", c)
	}
	e.gsErr(api, "still have conflicts", "continue")
	e.gsErr(api, "still has conflict markers", "conflict", "a.txt", "--done")
	if out := e.gsIn(api, "conflict", "a.txt", "--theirs"); !strings.Contains(out, "gitswitch continue") {
		t.Fatalf("--theirs:\n%s", out)
	}
	if out := e.gsIn(api, "continue"); !strings.Contains(out, "The merge is finished") {
		t.Fatalf("continue:\n%s", out)
	}
	if e.gitIn(api, "log", "-1", "--format=%s") != "Merge branch 'side'" || e.gitIn(api, "show", "HEAD:a.txt") != "side" || status() != "" {
		t.Fatalf("merged: %s", e.gitIn(api, "log", "-1", "--format=%s"))
	}
	e.gsIn(api, "undo", "-y")
	if head() != mainTip || status() != "" {
		t.Fatalf("undo merge: %s %q", head(), status())
	}

	// Abort goes back exactly; uncommitted changes are refused first.
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("dirty\n"), 0o644))
	e.gsErr(api, "commit, park or discard them first", "merge", "side", "-y")
	e.gitIn(api, "checkout", "--", "a.txt")
	e.gsErr(api, "conflicts", "merge", "side", "--no-ff", "-y")
	e.gsErr(api, "can't be skipped", "skip", "-y")
	if out := e.gsIn(api, "abort", "-y"); !strings.Contains(out, "Aborted the merge") || head() != mainTip || status() != "" {
		t.Fatalf("abort: %s %q\n%s", head(), status(), out)
	}
	e.gsErr(api, "nothing is in progress", "continue")
}

// The stash manager end to end: save with a message, chosen files and new
// files, list (parked changes too), show, apply (with a clash that changes
// nothing), pop, branch from a stash, drop and undo.
func TestStashManager(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(file, text string) {
		t.Helper()
		must(t, os.WriteFile(filepath.Join(api, file), []byte(text), 0o644))
	}
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	stashes := func() string { return e.gitIn(api, "stash", "list", "--format=%gs") }
	e.commitFile(api, "f.txt", "1\n2\n3\n", "base f")
	e.commitFile(api, "g.txt", "g\n", "base g")

	e.gsErr(api, "nothing to stash", "stash", "save")
	if out := e.gsIn(api, "stash"); !strings.Contains(out, "No stashes") {
		t.Fatalf("empty list:\n%s", out)
	}

	// Save everything, new files included, with a message.
	write("f.txt", "ONE\n2\n3\n")
	write("new.txt", "n\n")
	before := status()
	out := e.gsIn(api, "stash", "save", "-m", "work in progress")
	if status() != "" || stashes() != "On main: work in progress" || !strings.Contains(out, "gitswitch undo") {
		t.Fatalf("save: status %q, stash %q\n%s", status(), stashes(), out)
	}
	if out := e.gsIn(api, "stash"); !strings.Contains(out, "stash@{0}") || !strings.Contains(out, "work in progress") || !strings.Contains(out, "2 files") {
		t.Fatalf("list:\n%s", out)
	}
	out = e.gsIn(api, "stash", "show", "0")
	if !strings.Contains(out, "M  f.txt") || !strings.Contains(out, "?  new.txt") {
		t.Fatalf("show:\n%s", out)
	}
	if out := e.gsIn(api, "stash", "show", "0", "new.txt"); !strings.Contains(out, "+n") {
		t.Fatalf("show new file:\n%s", out)
	}
	if out := e.gsIn(api, "stash", "show", "0", "f.txt"); !strings.Contains(out, "-1") || !strings.Contains(out, "+ONE") {
		t.Fatalf("show f:\n%s", out)
	}
	if out := e.gsIn(api, "stash", "--json"); !strings.Contains(out, `"kind":"stash"`) || !strings.Contains(out, `"untracked":true`) {
		t.Fatalf("json:\n%s", out)
	}

	// Undo the save: the changes are back and the stash is gone.
	e.gsIn(api, "undo", "-y")
	if status() != before || stashes() != "" {
		t.Fatalf("undo save: status %q (want %q), stash %q", status(), before, stashes())
	}

	// Only chosen files.
	e.gsIn(api, "stash", "save", "-m", "just f", "f.txt")
	if status() != "?? new.txt" || stashes() != "On main: just f" {
		t.Fatalf("path save: status %q, stash %q", status(), stashes())
	}
	e.gsErr(api, "uncommitted changes", "stash", "apply", "0") // new.txt is uncommitted

	// Apply keeps the stash; pop removes it.
	must(t, os.Remove(filepath.Join(api, "new.txt")))
	e.gsIn(api, "stash", "apply", "0")
	if status() != "M f.txt" || stashes() != "On main: just f" {
		t.Fatalf("apply: status %q, stash %q", status(), stashes())
	}
	e.gitIn(api, "checkout", "-q", "--", "f.txt")
	e.gsIn(api, "stash", "pop", "0")
	if status() != "M f.txt" || stashes() != "" {
		t.Fatalf("pop: status %q, stash %q", status(), stashes())
	}

	// A clash on apply changes nothing and keeps the stash.
	e.gsIn(api, "stash", "save", "-m", "clashing")
	e.commitFile(api, "f.txt", "CLASH\n2\n3\n", "moved on")
	e.gsErr(api, "nothing changed and the stash stays", "stash", "apply", "0")
	if status() != "" || stashes() != "On main: clashing" {
		t.Fatalf("clash: status %q, stash %q", status(), stashes())
	}

	// A new branch from the stash, then drop and undo.
	e.gsIn(api, "stash", "branch", "0", "from-stash")
	if got := e.gitIn(api, "branch", "--show-current"); got != "from-stash" || status() != "M f.txt" || stashes() != "" {
		t.Fatalf("branch: on %s, status %q, stash %q", got, status(), stashes())
	}
	e.gitIn(api, "stash", "push", "-q", "-m", "to drop")
	e.gsErr(api, "add -y", "stash", "drop", "0")
	e.gsIn(api, "stash", "drop", "0", "-y")
	if stashes() != "" {
		t.Fatalf("drop: %q", stashes())
	}
	e.gsIn(api, "undo", "-y")
	if stashes() != "On from-stash: to drop" {
		t.Fatalf("undo drop: %q", stashes())
	}

	// Parked changes are in the list with their kind.
	e.gitIn(api, "stash", "drop", "-q")
	write("f.txt", "parked\n")
	e.gsIn(api, "switch", "main", "--park")
	if out := e.gsIn(api, "stash"); !strings.Contains(out, "changes parked on from-stash") {
		t.Fatalf("parked in list:\n%s", out)
	}
	e.gsErr(api, "start the message with other words", "stash", "save", "-m", "gitswitch park: x")
}

// Hunks from the command line: list them, stage and unstage some by number,
// discard one (asks first, keeps the file in a stash that git restore reads).
func TestHunksCommands(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	var rows []string
	for i := 1; i <= 30; i++ {
		rows = append(rows, fmt.Sprintf("row %d", i))
	}
	put := func(lines []string) {
		must(t, os.WriteFile(filepath.Join(api, "src.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	}
	put(rows)
	e.gitIn(api, "add", "src.txt")
	e.gitIn(api, "commit", "-q", "-m", "src")
	edit := append([]string{}, rows...)
	edit[1], edit[14], edit[27] = "ROW 2", "ROW 15", "ROW 28"
	put(edit)

	out := e.gsIn(api, "hunks", "src.txt")
	if !strings.Contains(out, "3 hunk(s), not staged") || !strings.Contains(out, "#3  @@") || !strings.Contains(out, "+ROW 28") {
		t.Fatalf("hunks:\n%s", out)
	}
	if out := e.gsIn(api, "hunks", "src.txt", "--json"); !strings.Contains(out, `"fingerprint":"`) || !strings.Contains(out, `"kind":"add"`) {
		t.Fatalf("hunks --json:\n%s", out)
	}
	if out := e.gsIn(api, "stage", "src.txt", "--hunk", "1,3"); !strings.Contains(out, "Staged hunk 1,3") {
		t.Fatalf("stage --hunk:\n%s", out)
	}
	cached := e.gitIn(api, "diff", "--cached")
	if !strings.Contains(cached, "+ROW 2") || !strings.Contains(cached, "+ROW 28") || strings.Contains(cached, "ROW 15") {
		t.Fatalf("staged:\n%s", cached)
	}
	if out := e.gsIn(api, "hunks", "src.txt", "--staged"); !strings.Contains(out, "2 hunk(s), staged") {
		t.Fatalf("hunks --staged:\n%s", out)
	}
	if out := e.gsIn(api, "unstage", "src.txt", "--hunk=1"); !strings.Contains(out, "Unstaged hunk 1") {
		t.Fatalf("unstage --hunk:\n%s", out)
	}
	if got := e.gitIn(api, "diff", "--cached"); strings.Contains(got, "ROW 2\n") || !strings.Contains(got, "+ROW 28") {
		t.Fatalf("after unstage:\n%s", got)
	}
	if out, err := e.run(api, gsBin, "stage", "src.txt", "--hunk", "9"); err == nil || !strings.Contains(out, "no hunk 9") {
		t.Fatalf("a hunk that is not there: %v\n%s", err, out)
	}
	if out, err := e.run(api, gsBin, "stage", "src.txt", "--hunk", "0"); err == nil || !strings.Contains(out, "from 1") {
		t.Fatalf("hunk 0: %v\n%s", err, out)
	}
	if out, err := e.run(api, gsBin, "discard", "src.txt", "--hunk", "1"); err == nil || !strings.Contains(out, "-y") {
		t.Fatalf("discard without -y and no terminal: %v\n%s", err, out)
	}
	out = e.gsIn(api, "discard", "src.txt", "--hunk", "1", "-y")
	if !strings.Contains(out, "Discarded hunk 1") || !strings.Contains(out, "git restore --source='stash@{0}' -- src.txt") {
		t.Fatalf("discard --hunk:\n%s", out)
	}
	b, _ := os.ReadFile(filepath.Join(api, "src.txt"))
	if strings.Contains(string(b), "ROW 2\n") || !strings.Contains(string(b), "ROW 15") {
		t.Fatalf("file after discarding hunk 1:\n%s", b)
	}
	e.gitIn(api, "restore", "--source=stash@{0}", "--", "src.txt")
	if b, _ := os.ReadFile(filepath.Join(api, "src.txt")); !strings.Contains(string(b), "ROW 2\n") {
		t.Fatalf("restore from the stash:\n%s", b)
	}
	// Not a text change.
	must(t, os.WriteFile(filepath.Join(api, "bin.dat"), []byte{0, 1, 2, 0, 3}, 0o644))
	e.gitIn(api, "add", "bin.dat")
	e.gitIn(api, "commit", "-q", "-m", "bin")
	must(t, os.WriteFile(filepath.Join(api, "bin.dat"), []byte{0, 9, 9, 0, 3}, 0o644))
	if out, err := e.run(api, gsBin, "stage", "bin.dat", "--hunk", "1"); err == nil || !strings.Contains(out, "binary") {
		t.Fatalf("binary: %v\n%s", err, out)
	}
}

// Blame, file history across a rename and the code search, with real git.
func TestBlameFileLogAndCodeSearch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(name, text string) { must(t, os.WriteFile(filepath.Join(api, name), []byte(text), 0o644)) }
	write("old.txt", "alpha line\nbeta line\ngamma line\n")
	e.gitIn(api, "add", "old.txt")
	e.gitIn(api, "commit", "-q", "-m", "add old")
	e.gitIn(api, "mv", "old.txt", "new.txt")
	e.gitIn(api, "commit", "-q", "-m", "rename old to new")
	write("new.txt", "alpha line\nBETA line\ngamma line\n")
	e.gitIn(api, "commit", "-q", "-am", "shout beta")

	out := e.gsIn(api, "blame", "new.txt")
	if !strings.Contains(out, "alpha line") || !strings.Contains(out, "BETA line") || strings.Count(out, "dipu@") != 0 {
		t.Fatalf("blame:\n%s", out)
	}
	var b struct {
		Lines []struct {
			Line     int
			Account  string
			OrigPath string
			Summary  string
		}
		Chunks    []struct{ Count int }
		Truncated bool
	}
	if err := json.Unmarshal([]byte(e.gsIn(api, "blame", "new.txt", "--rev", "HEAD", "-w", "-M", "--json")), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Lines) != 3 || len(b.Chunks) != 3 || b.Lines[0].OrigPath != "old.txt" || b.Lines[0].Account != "client" || b.Lines[1].Summary != "shout beta" {
		t.Fatalf("blame json: %+v", b)
	}
	e.gsErr(api, "no file", "blame", "missing.txt")
	e.gsErr(api, "not a branch or tag name", "blame", "new.txt", "--rev", "--output=x")

	out = e.gsIn(api, "log", "--file", "new.txt")
	if !strings.Contains(out, "shout beta") || !strings.Contains(out, "renamed from old.txt to new.txt") || !strings.Contains(out, "add old") || !strings.Contains(out, "R100") {
		t.Fatalf("log --file:\n%s", out)
	}
	out = e.gsIn(api, "log", "--code", "BETA")
	if !strings.Contains(out, "(1 commit(s))") || strings.Contains(out, "add old") || !strings.Contains(out, `code "BETA"`) {
		t.Fatalf("log --code:\n%s", out)
	}
	out = e.gsIn(api, "log", "--code", "^alpha", "--regex")
	if !strings.Contains(out, "add old") || !strings.Contains(out, "(1 commit(s))") {
		t.Fatalf("log --code --regex:\n%s", out)
	}
}

// Compare, reflog with recovery and a guided bisect from the command line.
func TestCompareReflogAndBisect(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	write := func(name, text string) { must(t, os.WriteFile(filepath.Join(api, name), []byte(text), 0o644)) }
	commit := func(name, text, msg string) {
		write(name, text)
		e.gitIn(api, "add", "--all")
		e.gitIn(api, "commit", "-q", "-m", msg)
	}
	commit("base.txt", "base\n", "base")
	e.gitIn(api, "switch", "-q", "-c", "feature")
	commit("f.txt", "feature\n", "feature work")
	e.gitIn(api, "switch", "-q", "-")
	commit("m.txt", "main\n", "main work")
	main := e.gitIn(api, "symbolic-ref", "--short", "HEAD")

	// compare: both modes, one file, JSON, refusals.
	out := e.gsIn(api, "compare", main, "feature")
	for _, want := range []string{"Only on " + main + " (1):", "main work", "Only on feature (1):", "feature work", "since they went apart", "A  f.txt  +1 -0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compare missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "m.txt") {
		t.Fatalf("since-fork must not list what only %s changed:\n%s", main, out)
	}
	if out = e.gsIn(api, "compare", main, "feature", "--direct"); !strings.Contains(out, "D  m.txt") || !strings.Contains(out, "A  f.txt") {
		t.Fatalf("compare --direct:\n%s", out)
	}
	var c struct {
		Mode    string
		OnlyB   []struct{ Subject string }
		Files   []struct{ Path, Status string }
		Same    bool
		Base    string
		AHash   string
		BHash   string
		Summary string
	}
	if err := json.Unmarshal([]byte(e.gsIn(api, "compare", main, "feature", "--json")), &c); err != nil {
		t.Fatal(err)
	}
	if c.Mode != "since-fork" || len(c.OnlyB) != 1 || c.OnlyB[0].Subject != "feature work" || len(c.Files) != 1 || c.Files[0].Path != "f.txt" || c.Files[0].Status != "added" || c.Base == "" {
		t.Fatalf("compare json: %+v", c)
	}
	if out = e.gsIn(api, "compare", main, "feature", "--file", "f.txt"); !strings.Contains(out, "+feature") {
		t.Fatalf("compare --file:\n%s", out)
	}
	write("m.txt", "changed on disk\n")
	if out = e.gsIn(api, "compare", "HEAD", "--working", "--direct"); !strings.Contains(out, "M  m.txt") {
		t.Fatalf("compare --working:\n%s", out)
	}
	e.gitIn(api, "checkout", "--", "m.txt")
	if out = e.gsIn(api, "compare", "HEAD"); !strings.Contains(out, "are the same") {
		t.Fatalf("compare HEAD HEAD:\n%s", out)
	}
	e.gsErr(api, "usage: gitswitch compare", "compare")
	e.gsErr(api, "usage: gitswitch compare", "compare", "--output=x")
	e.gsErr(api, "no branch, tag or commit called nope", "compare", "nope")

	// reflog: reset loses a commit, recover brings it back as a branch.
	commit("lost.txt", "x\n", "about to be lost")
	lost := e.gitIn(api, "rev-parse", "HEAD")
	e.gitIn(api, "reset", "-q", "--hard", "HEAD~1")
	out = e.gsIn(api, "reflog", "-n", "5")
	if !strings.Contains(out, "reset") || !strings.Contains(out, "about to be lost") || !strings.Contains(out, "[lost]") {
		t.Fatalf("reflog:\n%s", out)
	}
	out = e.gsIn(api, "reflog", "--lost")
	if !strings.Contains(out, lost[:7]) || strings.Contains(out, "main work") {
		t.Fatalf("reflog --lost:\n%s", out)
	}
	var rl struct {
		Entries []struct {
			Index  int
			Ref    string
			Hash   string
			Action string
			Lost   bool
		}
		More bool
	}
	if err := json.Unmarshal([]byte(e.gsIn(api, "reflog", "--json", "--lost")), &rl); err != nil {
		t.Fatal(err)
	}
	if len(rl.Entries) != 1 || rl.Entries[0].Hash != lost || !rl.Entries[0].Lost || rl.Entries[0].Action != "commit" {
		t.Fatalf("reflog json: %+v", rl)
	}
	head := e.gitIn(api, "rev-parse", "HEAD")
	out = e.gsIn(api, "reflog", "recover", fmt.Sprint(rl.Entries[0].Index), "rescued")
	if !strings.Contains(out, "Made branch rescued") || e.gitIn(api, "rev-parse", "rescued") != lost || e.gitIn(api, "rev-parse", "HEAD") != head {
		t.Fatalf("recover:\n%s", out)
	}
	e.gsErr(api, "already exists", "reflog", "recover", lost, "rescued")
	e.gsErr(api, "can't be a branch name", "reflog", "recover", "0", "bad name")
	if out = e.gsIn(api, "reflog", "--lost"); !strings.Contains(out, "No lost commits") {
		t.Fatalf("after recovery:\n%s", out)
	}

	// bisect: 12 commits, broken from the 7th.
	e.gitIn(api, "switch", "-q", "-c", "bis", main)
	var first string
	for i := 1; i <= 12; i++ {
		state := "ok"
		if i >= 7 {
			state = "broken"
		}
		write("state.txt", state+"\n")
		commit("n.txt", fmt.Sprint(i, "\n"), fmt.Sprintf("step %d", i))
		if i == 1 {
			first = e.gitIn(api, "rev-parse", "HEAD")
		}
	}
	write("state.txt", "dirty\n")
	e.gsErr(api, "commit, park or discard", "bisect", "start", "HEAD", first)
	e.gitIn(api, "checkout", "--", "state.txt")
	e.gsErr(api, "no bisect is running", "bisect", "good")
	out = e.gsIn(api, "bisect", "start", "HEAD", first)
	if !strings.Contains(out, "Test ") || !strings.Contains(out, "steps left") {
		t.Fatalf("bisect start:\n%s", out)
	}
	e.gsErr(api, "already running", "bisect", "start", "HEAD", first)
	if out = e.gsIn(api, "bisect", "status"); !strings.Contains(out, "So far:") || !strings.Contains(out, "bad") {
		t.Fatalf("bisect status:\n%s", out)
	}
	for i := 0; i < 10 && !strings.Contains(out, "The first bad commit is"); i++ {
		b, _ := os.ReadFile(filepath.Join(api, "state.txt"))
		mark := "good"
		if strings.TrimSpace(string(b)) == "broken" {
			mark = "bad"
		}
		out = e.gsIn(api, "bisect", mark)
	}
	if !strings.Contains(out, "The first bad commit is") || !strings.Contains(out, "step 7") || !strings.Contains(out, "by ") || !strings.Contains(out, "changed  state.txt") {
		t.Fatalf("bisect result:\n%s", out)
	}
	var st struct {
		Running  bool
		Culprit  struct{ Subject string }
		Original string
	}
	if err := json.Unmarshal([]byte(e.gsIn(api, "bisect", "status", "--json")), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Culprit.Subject != "step 7" || st.Original != "bis" {
		t.Fatalf("bisect json: %+v", st)
	}
	if out = e.gsIn(api, "bisect", "reset"); !strings.Contains(out, "back on bis") {
		t.Fatalf("bisect reset:\n%s", out)
	}
	if e.gitIn(api, "symbolic-ref", "--short", "HEAD") != "bis" {
		t.Fatal("reset did not return to the branch")
	}
	if out = e.gsIn(api, "bisect", "status"); !strings.Contains(out, "No bisect is running") {
		t.Fatalf("after reset:\n%s", out)
	}
	e.gsErr(api, "usage: gitswitch bisect", "bisect")
	e.gsErr(api, "usage: gitswitch bisect start", "bisect", "start", "HEAD")
}

// gitswitch remote: list, add, rename, set-url and remove. A URL is checked
// as on clone, the account it would use is named, and a remote that
// branches track is not removed without --force.
func TestRemoteCommands(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.gs("rule", "add", "owner:dipu", "me")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")

	out := e.gsIn(api, "remote")
	if !strings.Contains(out, "origin") || !strings.Contains(out, "[as client]") || !strings.Contains(out, "the current branch pulls from and pushes to it") || !strings.Contains(out, "tracked by main") {
		t.Fatalf("remote list:\n%s", out)
	}
	// A URL whose owner your rules give to another account than the repo's.
	e.gsIn(api, "use", "client")
	out = e.gsIn(api, "remote", "add", "fork", "git@github.com:dipu/notes.git")
	if !strings.Contains(out, "Added the remote fork at git@github.com:dipu/notes.git") || !strings.Contains(out, "uses the account") || !strings.Contains(out, "gitswitch use me") {
		t.Fatalf("add:\n%s", out)
	}
	if e.gitIn(api, "remote", "get-url", "fork") != "git@github.com:dipu/notes.git" {
		t.Fatal("not added")
	}
	e.gsErr(api, "already a remote called fork", "remote", "add", "fork", "git@github.com:dipu/other.git")
	e.gsErr(api, "not a repo URL", "remote", "add", "local", "/tmp/some/folder")
	e.gsErr(api, "not supported", "remote", "add", "local", "file:///tmp/x")
	e.gsErr(api, "can't be a remote name", "remote", "add", "bad name", "git@github.com:dipu/x.git")
	e.gsErr(api, "usage: gitswitch remote", "remote", "add", "only-a-name")
	if out := e.gsIn(api, "--dry-run", "remote", "add", "ghost", "git@github.com:dipu/ghost.git"); !strings.Contains(out, "[dry-run]") || strings.Contains(e.gitIn(api, "remote"), "ghost") {
		t.Fatalf("dry run:\n%s", out)
	}

	out = e.gsIn(api, "remote", "set-url", "fork", "git@github.com:dipu/notes2.git")
	if !strings.Contains(out, "now points at git@github.com:dipu/notes2.git (it was git@github.com:dipu/notes.git)") {
		t.Fatalf("set-url:\n%s", out)
	}
	e.gsErr(api, "already points at", "remote", "set-url", "fork", "git@github.com:dipu/notes2.git")
	e.gsErr(api, "no remote called", "remote", "set-url", "nope", "git@github.com:dipu/notes2.git")
	if out := e.gsIn(api, "remote", "rename", "fork", "mine"); !strings.Contains(out, "Renamed the remote fork to mine") {
		t.Fatalf("rename:\n%s", out)
	}
	var rs []struct {
		Name, FetchURL, Host, Account string
		Upstream                      bool
		Tracking                      []string
	}
	must(t, json.Unmarshal([]byte(e.gsIn(api, "remote", "list", "--json")), &rs))
	if len(rs) != 2 || rs[0].Name != "mine" || rs[0].Host != "github.com" || rs[1].Name != "origin" || !rs[1].Upstream || strings.Join(rs[1].Tracking, ",") != "main" {
		t.Fatalf("json: %+v", rs)
	}

	// Renaming the remote main tracks keeps it tracking.
	e.gsIn(api, "remote", "rename", "origin", "upstream")
	if up := e.gitIn(api, "rev-parse", "--abbrev-ref", "main@{upstream}"); up != "upstream/main" {
		t.Fatalf("upstream after rename = %q", up)
	}
	e.gsErr(api, "main pulls from upstream", "remote", "remove", "upstream")
	e.gsErr(api, "--force", "remote", "remove", "upstream")
	// Not a terminal: asking needs -y.
	e.gsErr(api, "add -y to confirm", "remote", "remove", "mine")
	if out := e.gsIn(api, "remote", "remove", "mine", "-y"); !strings.Contains(out, "Removed the remote mine") || !strings.Contains(out, "gitswitch remote add mine git@github.com:dipu/notes2.git") {
		t.Fatalf("remove:\n%s", out)
	}
	out = e.gsIn(api, "remote", "remove", "upstream", "--force", "-y")
	if !strings.Contains(out, "No upstream any more: main") || e.gitIn(api, "remote") != "" {
		t.Fatalf("forced remove:\n%s", out)
	}
	if out := e.gsIn(api, "remote"); !strings.Contains(out, "no remote") {
		t.Fatalf("empty list:\n%s", out)
	}
}

// branch delete-remote runs as the repo's account, asks first, and keeps
// away from the default branch and the branch you are on; fetch --prune
// forgets what the server deleted and says so; set fetch-prune does it on
// every fetch.
func TestDeleteRemoteBranchAndFetchPrune(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	bare := filepath.Join(e.server, "acme-corp", "api.git")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.gitIn(api, "push", "-q", "origin", "main")
	e.gitIn(api, "remote", "set-head", "origin", "main")

	e.gsIn(api, "branch", "new", "feature/x")
	e.commitFile(api, "x.txt", "x\n", "x")
	e.gsIn(api, "push")
	e.gsIn(api, "switch", "main")
	hash := e.gitIn(api, "rev-parse", "origin/feature/x")

	e.gsErr(api, "add -y to confirm", "branch", "delete-remote", "origin/feature/x")
	e.gsErr(api, "default branch", "branch", "delete-remote", "origin/main", "-y")
	e.gsErr(api, "--force", "branch", "delete-remote", "origin/main", "-y")
	e.gsErr(api, "not a remote branch", "branch", "delete-remote", "feature/x", "-y")
	e.gsErr(api, "no remote branch origin/ghost", "branch", "delete-remote", "origin/ghost", "-y")
	if e.gitIn(api, "ls-remote", "--heads", "origin", "feature/x") == "" {
		t.Fatal("a refusal deleted it")
	}
	// On the branch that pulls from it.
	e.gsIn(api, "switch", "feature/x")
	e.gsErr(api, "you are on feature/x", "branch", "delete-remote", "origin/feature/x", "-y")
	e.gsIn(api, "switch", "main")

	if out := e.gsIn(api, "--dry-run", "branch", "delete-remote", "origin/feature/x"); !strings.Contains(out, "[dry-run] delete the branch feature/x on origin as client") || e.gitIn(api, "ls-remote", "--heads", "origin", "feature/x") == "" {
		t.Fatalf("dry run:\n%s", out)
	}
	e.resetLog()
	out := e.gsIn(api, "branch", "delete-remote", "origin/feature/x", "-y")
	if !strings.Contains(out, "Deleted the branch feature/x on origin (it was at "+hash[:7]+")") || !strings.Contains(out, "feature/x pulled from it") ||
		!strings.Contains(out, "git push origin "+hash+":refs/heads/feature/x") {
		t.Fatalf("delete:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("deleted with keys %v, want the repo's account", k)
	}
	if e.gitIn(bare, "for-each-ref", "refs/heads/feature/x") != "" || e.gitIn(api, "for-each-ref", "refs/remotes/origin/feature/x") != "" {
		t.Fatal("still there")
	}
	// The restore line works.
	e.gitIn(api, "push", "-q", "origin", hash+":refs/heads/feature/x")
	if e.gitIn(bare, "rev-parse", "refs/heads/feature/x") != hash {
		t.Fatal("not restored")
	}

	// Prune: someone deletes branches on the server.
	e.gitIn(api, "branch", "--track", "gone1", "origin/feature/x")
	e.gitIn(api, "fetch", "-q")
	e.gitIn(bare, "update-ref", "-d", "refs/heads/feature/x")
	if out := e.gsIn(api, "fetch"); strings.Contains(out, "Forgot") || e.gitIn(api, "for-each-ref", "refs/remotes/origin/feature/x") == "" {
		t.Fatalf("a plain fetch pruned:\n%s", out)
	}
	e.resetLog()
	out = e.gsIn(api, "fetch", "--prune")
	if !strings.Contains(out, "Forgot 1 remote branch(es) the server no longer has: origin/feature/x") || !strings.Contains(out, "pulled from them: feature/x, gone1") {
		t.Fatalf("prune:\n%s", out)
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("pruned with keys %v", k)
	}
	var f struct{ Pruned, Gone []string }
	must(t, json.Unmarshal([]byte(e.gsIn(api, "fetch", "--prune", "--json")), &f))
	if len(f.Pruned) != 0 || f.Gone == nil {
		t.Fatalf("second prune json: %+v", f)
	}

	// The setting: off by default, global or per repo.
	e.gitIn(bare, "update-ref", "refs/heads/feature/y", hash)
	e.gitIn(api, "fetch", "-q")
	e.gitIn(bare, "update-ref", "-d", "refs/heads/feature/y")
	if v, err := e.gitMay(e.root, "config", "--global", "--get", "gitswitch.fetchPrune"); err == nil {
		t.Fatalf("on by default: %q", v)
	}
	e.gsErr(api, "is not a choice: on or off", "set", "fetch-prune", "maybe")
	e.gsErr(api, "usage: gitswitch set fetch-prune", "set", "fetch-prune")
	if out := e.gs("set", "fetch-prune", "on"); !strings.Contains(out, "also forgets the remote branches") || e.cfg(e.root, "gitswitch.fetchPrune") != "true" {
		t.Fatalf("set on:\n%s", out)
	}
	if out := e.gsIn(api, "fetch"); !strings.Contains(out, "origin/feature/y") {
		t.Fatalf("fetch with the setting on:\n%s", out)
	}
	e.gsIn(api, "set", "fetch-prune", "off", "--repo")
	if e.cfg(api, "gitswitch.fetchPrune") != "false" {
		t.Fatal("repo setting not saved")
	}
	e.gsIn(api, "set", "fetch-prune", "global", "--repo")
	e.gs("set", "fetch-prune", "off")
}

// push --force-with-lease lists the commits that would be lost, asks, and
// pushes only while the remote branch is where the last fetch saw it.
func TestForcePushWithLease(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	bare := filepath.Join(e.server, "acme-corp", "api.git")
	e.commitFile(api, "a.txt", "a\n", "base")
	e.commitFile(api, "b.txt", "b\n", "to be reworded")
	e.gitIn(api, "push", "-q", "origin", "main")
	remote := func() string { return e.gitIn(bare, "rev-parse", "refs/heads/main") }

	// Nothing to overwrite: a normal push is all it needs.
	e.gsErr(api, "normal push", "push", "--force-with-lease", "-y")
	e.gsErr(api, "usage: gitswitch push --force-with-lease", "push", "--force-with-lease", "--open")

	e.gitIn(api, "commit", "-q", "--amend", "-m", "reworded")
	old := remote()
	e.gsErr(api, "never forces", "push") // the ordinary push still refuses
	out, err := e.run(api, gsBin, "push", "--force-with-lease")
	if err == nil || !strings.Contains(out, "1 commit would be overwritten") || !strings.Contains(out, "to be reworded") || !strings.Contains(out, "reworded") ||
		!strings.Contains(out, "add -y to confirm") || remote() != old {
		t.Fatalf("without -y: err=%v\n%s", err, out)
	}
	if out := e.gsIn(api, "--dry-run", "push", "--force-with-lease"); !strings.Contains(out, "[dry-run] force push main to origin/main as client, only if it is still at "+old[:7]) || remote() != old {
		t.Fatalf("dry run:\n%s", out)
	}
	e.resetLog()
	out = e.gsIn(api, "push", "--force-with-lease", "-y")
	if !strings.Contains(out, "Force pushed 1 commit to origin/main as client") || !strings.Contains(out, "git push --force-with-lease origin "+old+":refs/heads/main") {
		t.Fatalf("force push:\n%s", out)
	}
	if remote() != e.gitIn(api, "rev-parse", "HEAD") {
		t.Fatal("remote not updated")
	}
	if k := e.keysUsed(); len(k) == 0 || k[len(k)-1] != "client" {
		t.Fatalf("pushed with keys %v", k)
	}

	// Someone pushes after your last fetch: the lease fails, nothing is overwritten.
	e.clone("git@github.com:acme-corp/api.git", "other")
	other := filepath.Join(e.root, "other")
	e.commitFile(other, "o.txt", "o\n", "from other")
	e.gitIn(other, "push", "-q")
	theirs := remote()
	e.gitIn(api, "commit", "-q", "--amend", "-m", "reworded again")
	out, err = e.run(api, gsBin, "push", "--force-with-lease", "-y")
	if err == nil || !strings.Contains(out, "someone pushed to origin/main since you fetched") || !strings.Contains(out, "fetch") || remote() != theirs {
		t.Fatalf("lease failure: err=%v\n%s", err, out)
	}
	// After fetching, the plan shows their commit, and the push goes.
	e.gsIn(api, "fetch")
	out = e.gsIn(api, "push", "--force-with-lease", "-y")
	if !strings.Contains(out, "from other") || !strings.Contains(out, "Force pushed") || remote() != e.gitIn(api, "rev-parse", "HEAD") {
		t.Fatalf("after fetch:\n%s", out)
	}
}

// checkout looks at a commit or tag with HEAD detached, says what that
// means, remembers the branch, and back returns to it; changes are asked
// about like a switch, and commits made while detached are not left behind
// silently.
func TestCheckoutDetachedAndBack(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:dipu", "me")
	e.clone("git@github.com:dipu/notes.git", "notes")
	n := filepath.Join(e.root, "notes")
	e.commitFile(n, "a.txt", "one\n", "first")
	e.gitIn(n, "tag", "v1")
	e.commitFile(n, "a.txt", "two\n", "second")
	second := e.gitIn(n, "rev-parse", "HEAD")

	e.gsErr(n, "usage: gitswitch checkout", "checkout")
	e.gsErr(n, "is a branch: switch to it", "checkout", "main")
	e.gsErr(n, "no commit, tag or remote branch", "checkout", "v9")
	e.gsErr(n, "not detached", "back")
	out := e.gsIn(n, "checkout", "v1")
	if !strings.Contains(out, "Checked out v1 (") || !strings.Contains(out, "not on a branch") || !strings.Contains(out, "gitswitch branch new <name>") || !strings.Contains(out, "gitswitch back") {
		t.Fatalf("checkout:\n%s", out)
	}
	if _, err := e.gitMay(n, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Fatal("HEAD is on a branch")
	}
	if e.cfg(n, "gitswitch.detachedFrom") != "main" {
		t.Fatalf("detachedFrom = %q", e.cfg(n, "gitswitch.detachedFrom"))
	}
	if out := e.gsIn(n, "status", "--json"); !strings.Contains(out, `"branch":""`) {
		t.Fatalf("status does not say detached:\n%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(n, "a.txt")); string(b) != "one\n" {
		t.Fatalf("files at v1: %q", b)
	}
	out = e.gsIn(n, "back")
	if !strings.Contains(out, "Back on main") || e.gitIn(n, "branch", "--show-current") != "main" || e.cfg(n, "gitswitch.detachedFrom") != "" {
		t.Fatalf("back:\n%s", out)
	}
	e.gsErr(n, "not detached", "back")

	// Uncommitted changes: asked about, like a switch.
	must(t, os.WriteFile(filepath.Join(n, "a.txt"), []byte("two, edited\n"), 0o644))
	e.gsErr(n, "say what to do with them", "checkout", "v1")
	// They clash with the files at v1: nothing changes.
	e.gsErr(n, "your changes clash with", "checkout", "v1", "--smart")
	if b, _ := os.ReadFile(filepath.Join(n, "a.txt")); string(b) != "two, edited\n" || e.gitIn(n, "branch", "--show-current") != "main" {
		t.Fatalf("a clash changed things: %q", b)
	}
	e.gitIn(n, "checkout", "-q", "--", "a.txt")
	// A new file is carried along.
	must(t, os.WriteFile(filepath.Join(n, "z.txt"), []byte("z\n"), 0o644))
	out = e.gsIn(n, "checkout", "v1", "--smart")
	if !strings.Contains(out, "taking your uncommitted changes along") {
		t.Fatalf("smart:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(n, "z.txt")); err != nil {
		t.Fatalf("changes not carried: %v", err)
	}
	e.gsErr(n, "say what to do with them", "back")
	must(t, os.Remove(filepath.Join(n, "z.txt")))
	e.gsIn(n, "back")
	must(t, os.WriteFile(filepath.Join(n, "a.txt"), []byte("two, edited\n"), 0o644))
	out = e.gsIn(n, "checkout", second[:7], "--park") // the commit main is at
	if !strings.Contains(out, "Parked your changes on main") || e.gitIn(n, "status", "--porcelain") != "" {
		t.Fatalf("park:\n%s", out)
	}
	out = e.gsIn(n, "back")
	if !strings.Contains(out, "Back on main") || !strings.Contains(out, "You parked changes on main") {
		t.Fatalf("back with parked changes:\n%s", out)
	}
	e.gsIn(n, "parked", "drop", "-y")

	// Commits made while detached are not left behind silently.
	e.gsIn(n, "checkout", "v1")
	e.commitFile(n, "d.txt", "d\n", "detached work")
	e.gsErr(n, "1 commit you made here", "back")
	if out := e.gsIn(n, "back", "--force"); !strings.Contains(out, "Back on main") {
		t.Fatalf("back --force:\n%s", out)
	}
	// ...and New branch here keeps them.
	e.gsIn(n, "checkout", "v1")
	e.commitFile(n, "e.txt", "e\n", "kept work")
	e.gsIn(n, "branch", "new", "rescue")
	if e.subjects(n, "rescue", 1) != "kept work" {
		t.Fatal("work not on the new branch")
	}
	if out := e.gsIn(n, "--dry-run", "checkout", "v1"); !strings.Contains(out, "[dry-run] check out v1") {
		t.Fatalf("dry run:\n%s", out)
	}
}

// Rebase onto a branch with real git: the plan, a clean rebase and its undo,
// a clash that stops it (continue, then undo; or abort), and the refusals
// (pushed commits, merge commits, uncommitted changes, nothing to do).
func TestRebaseOntoBranch(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.clone("git@github.com:acme-corp/api.git", "api")
	api := filepath.Join(e.root, "api")
	status := func() string { return e.gitIn(api, "status", "--porcelain") }
	head := func() string { return e.gitIn(api, "rev-parse", "HEAD") }
	e.commitFile(api, "a.txt", "base\n", "base")
	e.gitIn(api, "switch", "-q", "-c", "feature")
	e.commitFile(api, "f1.txt", "1\n", "f1")
	e.commitFile(api, "f2.txt", "2\n", "f2")
	orig := e.commitFile(api, "f3.txt", "3\n", "f3")
	e.gitIn(api, "switch", "-q", "main")
	e.commitFile(api, "m1.txt", "m\n", "m1")
	e.gitIn(api, "switch", "-q", "feature")

	// The plan lists what is replayed (oldest first) and what comes in.
	out := e.gsIn(api, "rebase", "--plan", "main")
	if !strings.Contains(out, "3 commits are replayed") || strings.Index(out, "f1") > strings.Index(out, "f3") || !strings.Contains(out, "m1") {
		t.Fatalf("plan:\n%s", out)
	}
	var plan map[string]any
	must(t, json.Unmarshal([]byte(e.gsIn(api, "rebase", "--plan", "main", "--json")), &plan))
	if plan["replayedCount"] != float64(3) || plan["incomingCount"] != float64(1) || plan["blocked"] != "" || head() != orig {
		t.Fatalf("plan --json: %v", plan)
	}
	e.gsErr(api, "add -y", "rebase", "main")
	e.gsErr(api, "usage:", "rebase")
	e.gsErr(api, "usage:", "rebase", "--force")
	if head() != orig {
		t.Fatal("a refused rebase moved HEAD")
	}

	// Clean: three commits on top of main; undo puts them back with their hashes.
	if out := e.gsIn(api, "rebase", "main", "-y"); !strings.Contains(out, "Rebased feature onto main") || !strings.Contains(out, "gitswitch undo") {
		t.Fatalf("rebase:\n%s", out)
	}
	if e.subjects(api, "HEAD", 5) != "f3,f2,f1,m1,base" || status() != "" {
		t.Fatalf("log: %s", e.subjects(api, "HEAD", 5))
	}
	if out := e.gsIn(api, "rebase", "main", "-y"); !strings.Contains(out, "nothing to rebase") {
		t.Fatalf("again:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if head() != orig || status() != "" {
		t.Fatalf("undo: %s %q", head(), status())
	}

	// A clash stops the rebase (never aborts): fix, continue, undo.
	e.gitIn(api, "reset", "-q", "--hard", "feature~3")
	e.commitFile(api, "a.txt", "feature\n", "feature edits a")
	e.commitFile(api, "f3.txt", "3\n", "f3")
	orig = head()
	e.gitIn(api, "switch", "-q", "main")
	e.commitFile(api, "a.txt", "main\n", "main edits a")
	e.gitIn(api, "switch", "-q", "feature")
	e.gsErr(api, "the rebase stopped: 1 file(s) have conflicts", "rebase", "main", "-y")
	if out := e.gsIn(api, "conflict"); !strings.Contains(out, "rebase") || !strings.Contains(out, "a.txt") {
		t.Fatalf("conflict list:\n%s", out)
	}
	e.gsErr(api, "in the middle of a rebase", "rebase", "main", "-y")
	e.gsErr(api, "still have conflicts", "continue")
	e.gsIn(api, "conflict", "a.txt", "--theirs")
	if out := e.gsIn(api, "continue"); !strings.Contains(out, "rebase is finished") {
		t.Fatalf("continue:\n%s", out)
	}
	if e.subjects(api, "HEAD", 3) != "f3,feature edits a,main edits a" || status() != "" {
		t.Fatalf("log: %s", e.subjects(api, "HEAD", 3))
	}
	if out := e.gsIn(api, "undo", "--show"); !strings.Contains(out, "rebase") {
		t.Fatalf("undo --show:\n%s", out)
	}
	e.gsIn(api, "undo", "-y")
	if head() != orig || status() != "" {
		t.Fatalf("undo after continue: %s %q", head(), status())
	}

	// Abort goes back exactly and records nothing.
	e.gsErr(api, "the rebase stopped", "rebase", "main", "-y")
	if out := e.gsIn(api, "abort", "-y"); !strings.Contains(out, "Aborted the rebase") || head() != orig || status() != "" {
		t.Fatalf("abort: %s %q\n%s", head(), status(), out)
	}
	e.gsErr(api, "nothing to undo", "undo", "-y")

	// Refusals, all before anything runs.
	must(t, os.WriteFile(filepath.Join(api, "a.txt"), []byte("dirty\n"), 0o644))
	e.gsErr(api, "commit, park or discard them first", "rebase", "main", "-y")
	e.gitIn(api, "checkout", "--", "a.txt")
	e.gitIn(api, "update-ref", "refs/remotes/origin/feature", orig)
	e.gsErr(api, "rewrite pushed commits", "rebase", "main", "-y")
	e.gitIn(api, "update-ref", "-d", "refs/remotes/origin/feature")
	e.gitIn(api, "switch", "-q", "-c", "side", "main~1")
	e.commitFile(api, "s.txt", "s\n", "s1")
	e.gitIn(api, "switch", "-q", "feature")
	e.gitIn(api, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	e.gsErr(api, "merge commit", "rebase", "main", "-y")
	if status() != "" || e.gitIn(api, "symbolic-ref", "--short", "HEAD") != "feature" {
		t.Fatalf("refusals changed the repo: %q", status())
	}
}

// Each submodule is fetched as the account its own URL's owner maps to,
// not the superproject's: init, update and update --remote through the
// real ssh hook, and the list says which account each one uses.
func TestSubmoduleCommandsUseEachOwnersAccount(t *testing.T) {
	e := newEnv(t)
	e.gs("rule", "add", "owner:acme-corp", "client")
	e.gs("rule", "add", "owner:dipu", "me")
	seed := filepath.Join(e.root, "subseed")
	must(t, os.MkdirAll(seed, 0o755))
	e.gitIn(seed, "init", "-q", "-b", "main")
	e.gitIn(seed, "config", "user.email", "s@x.example")
	e.gitIn(seed, "submodule", "add", "-q", "git@github.com:dipu/notes.git", "notes")
	e.gitIn(seed, "submodule", "add", "-q", "git@github.com:acme-corp/web.git", "web")
	e.gitIn(seed, "commit", "-q", "-m", "with submodules")
	e.serverRepo("acme-corp/sup")
	e.gitIn(seed, "push", "-q", "-f", filepath.Join(e.server, "acme-corp", "sup.git"), "HEAD:refs/heads/main")
	e.clone("git@github.com:acme-corp/sup.git", "sup")
	sup := filepath.Join(e.root, "sup")

	list := e.gsIn(sup, "submodule")
	if !strings.Contains(list, "not initialized") || !strings.Contains(list, "account me") || !strings.Contains(list, "account client") {
		t.Fatalf("list before init:\n%s", list)
	}
	e.resetLog()
	e.gsIn(sup, "submodule", "init")
	used := map[string]bool{}
	for _, k := range e.keysUsed() {
		used[k] = true
	}
	if !used["me"] || !used["client"] || len(used) != 2 {
		t.Fatalf("init used keys %v, want exactly me and client", e.keysUsed())
	}
	if got := e.cfg(filepath.Join(sup, "notes"), "user.email"); got != "dipu@me.example" {
		t.Fatalf("notes email %q", got)
	}
	if got := e.cfg(filepath.Join(sup, "web"), "user.email"); got != "dipu@client.example" {
		t.Fatalf("web email %q", got)
	}

	// New commits on both servers; --remote moves each, as its own account.
	for _, r := range []string{"dipu/notes", "acme-corp/web"} {
		w := filepath.Join(e.root, "w-"+strings.ReplaceAll(r, "/", "-"))
		e.git("clone", "-q", filepath.Join(e.server, filepath.FromSlash(r)+".git"), w)
		e.gitIn(w, "-c", "user.email=a@b.example", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "news")
		e.gitIn(w, "push", "-q", "origin", "main")
	}
	e.resetLog()
	out := e.gsIn(sup, "submodule", "update", "--remote", "-y")
	if !strings.Contains(out, "commit that change") {
		t.Fatalf("update --remote should say it changes the recorded commits:\n%s", out)
	}
	used = map[string]bool{}
	for _, k := range e.keysUsed() {
		used[k] = true
	}
	if !used["me"] || !used["client"] || len(used) != 2 {
		t.Fatalf("update --remote used keys %v, want exactly me and client", e.keysUsed())
	}
	if got := e.gitIn(filepath.Join(sup, "notes"), "log", "-1", "--format=%s"); got != "news" {
		t.Fatalf("notes at %q", got)
	}
	// A plain update puts them back at the recorded commits; changes inside block it.
	e.gsIn(sup, "submodule", "update")
	if got := e.gitIn(filepath.Join(sup, "notes"), "log", "-1", "--format=%s"); got == "news" {
		t.Fatal("plain update kept the remote's commit")
	}
	must(t, os.WriteFile(filepath.Join(sup, "notes", "scratch.txt"), []byte("x"), 0o644))
	e.gsErr(sup, "force", "submodule", "remove", "notes", "-y")
	e.gsIn(sup, "submodule", "remove", "notes", "--force", "-y")
	if _, err := os.Stat(filepath.Join(sup, "notes")); !os.IsNotExist(err) {
		t.Fatal("notes still there")
	}
}
