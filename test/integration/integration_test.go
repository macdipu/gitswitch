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
