package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	subapp "github.com/macdipu/gitswitch/internal/features/submodule/application"
	sub "github.com/macdipu/gitswitch/internal/features/submodule/domain"
)

// lab is a superproject with two local-path submodules (git >= 2.38
// blocks file transport unless protocol.file.allow=always, set for the
// test through the environment, which the Runner passes on).
type lab struct {
	t        *testing.T
	root     string
	super    string
	libs, ui string // the submodules' origin repos
	g        *Git
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newLab(t *testing.T) *lab {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "4")
	for i, kv := range [][2]string{{"protocol.file.allow", "always"}, {"user.email", "t@x.example"}, {"user.name", "T"}, {"init.defaultBranch", "main"}} {
		t.Setenv("GIT_CONFIG_KEY_"+string(rune('0'+i)), kv[0])
		t.Setenv("GIT_CONFIG_VALUE_"+string(rune('0'+i)), kv[1])
	}
	l := &lab{t: t, root: root, g: &Git{Git: &gitinfra.Runner{}}}
	mk := func(name string) string {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o755)
		git(t, d, "init", "-q")
		os.WriteFile(filepath.Join(d, "f.txt"), []byte(name+"\n"), 0o644)
		git(t, d, "add", ".")
		git(t, d, "commit", "-q", "-m", name+" 1")
		return d
	}
	l.libs, l.ui = mk("libs"), mk("ui")
	l.super = mk("super")
	git(t, l.super, "submodule", "add", "-q", l.libs, "libs/core")
	git(t, l.super, "submodule", "add", "-q", "-b", "main", l.ui, "ui")
	git(t, l.super, "commit", "-q", "-m", "add submodules")
	return l
}

func (l *lab) list() []sub.Entry {
	l.t.Helper()
	raw, err := l.g.Modules(l.super)
	if err != nil {
		l.t.Fatal(err)
	}
	plain, _ := l.g.Status(l.super, false)
	cached, _ := l.g.Status(l.super, true)
	es := sub.Build(sub.ParseModules(raw), sub.ParseStatus(plain), sub.ParseStatus(cached))
	for i := range es {
		if es[i].Initialized {
			es[i].Dirty = l.g.Dirty(l.super, es[i].Path)
		}
	}
	return es
}

func TestModulesAndStatusAgainstRealGit(t *testing.T) {
	l := newLab(t)
	es := l.list()
	if len(es) != 2 || es[0].Path != "libs/core" || es[1].Path != "ui" || es[1].Branch != "main" {
		t.Fatalf("%+v", es)
	}
	for _, e := range es {
		if e.State != sub.UpToDate || !e.Initialized || e.Dirty != 0 || len(e.Recorded) != 40 || e.Recorded != e.CheckedOut {
			t.Errorf("%+v", e)
		}
	}
	// A clone has them not initialized.
	clone := filepath.Join(l.root, "clone")
	git(t, l.root, "clone", "-q", l.super, clone)
	l.super = clone
	for _, e := range l.list() {
		if e.State != sub.NotInitialized || e.Initialized {
			t.Errorf("clone: %+v", e)
		}
	}
	if err := l.g.Update(clone, []string{"ui"}, false, true); err != nil {
		t.Fatal(err)
	}
	es = l.list()
	if es[0].Initialized || !es[1].Initialized || es[1].State != sub.UpToDate {
		t.Fatalf("after updating one: %+v", es)
	}
	if gd := l.g.GitDir(clone, "ui"); !strings.Contains(filepath.ToSlash(gd), ".git/modules/ui") {
		t.Errorf("GitDir = %q", gd)
	}
	if gd := l.g.GitDir(clone, "libs/core"); gd != "" {
		t.Errorf("an empty folder has no git dir of its own, got %q", gd)
	}
}

func TestDirtyUpstreamAndUnpushed(t *testing.T) {
	l := newLab(t)
	ui := filepath.Join(l.super, "ui")
	os.WriteFile(filepath.Join(ui, "f.txt"), []byte("changed\n"), 0o644)
	os.WriteFile(filepath.Join(ui, "new.txt"), []byte("x\n"), 0o644)
	if n := l.g.Dirty(l.super, "ui"); n != 2 {
		t.Errorf("dirty = %d", n)
	}
	// The origin gets a new commit: the recorded one is behind it.
	os.WriteFile(filepath.Join(l.ui, "g.txt"), []byte("g\n"), 0o644)
	git(t, l.ui, "add", ".")
	git(t, l.ui, "commit", "-q", "-m", "ui 2")
	git(t, ui, "fetch", "-q", "origin")
	e := l.list()[1]
	up, ahead, behind := l.g.Upstream(l.super, "ui", e.Recorded, "main")
	if up != "origin/main" || ahead != 0 || behind != 1 {
		t.Errorf("upstream %q +%d -%d", up, ahead, behind)
	}
	if up, _, _ := l.g.Upstream(l.super, "ui", e.Recorded, "nope"); up != "origin/HEAD" && up != "origin/main" {
		t.Errorf("fallback to the default branch, got %q", up)
	}
	if n := l.g.Unpushed(l.super, "ui"); n != 0 {
		t.Errorf("unpushed = %d", n)
	}
	git(t, ui, "add", ".")
	git(t, ui, "commit", "-q", "-m", "only here")
	if n := l.g.Unpushed(l.super, "ui"); n != 1 {
		t.Errorf("unpushed = %d", n)
	}
}

// The whole use case against real git: update moves a submodule to the
// recorded commit, refuses while changes would be overwritten, --remote
// moves to the branch tip, remove cleans everything.
func TestServiceAgainstRealGit(t *testing.T) {
	l := newLab(t)
	gd := filepath.Join(l.super, ".git")
	s := &subapp.Service{Git: l.g, Accounts: noAccounts{}, Repos: nil, Locator: loc{l.super}}
	ui := filepath.Join(l.super, "ui")

	// The origin moves on; --remote takes the superproject there.
	os.WriteFile(filepath.Join(l.ui, "g.txt"), []byte("g\n"), 0o644)
	git(t, l.ui, "add", ".")
	git(t, l.ui, "commit", "-q", "-m", "ui 2")
	newTip := git(t, l.ui, "rev-parse", "HEAD")

	os.WriteFile(filepath.Join(ui, "f.txt"), []byte("mine\n"), 0o644)
	if _, err := s.Update(gd, "ui", true, false); err == nil || !strings.Contains(err.Error(), "ui has 1 changed file(s)") {
		t.Fatalf("err = %v", err)
	}
	git(t, ui, "checkout", "-q", "--", "f.txt")
	r, err := s.Update(gd, "ui", true, false)
	if err != nil || len(r.Changed) != 1 || r.Changed[0].To != newTip || !r.RecordedChanged {
		t.Fatalf("%v %+v", err, r)
	}
	if got := git(t, l.super, "status", "--porcelain"); !strings.Contains(got, "ui") {
		t.Errorf("the superproject should show ui as changed: %q", got)
	}
	// A plain update puts it back at the recorded commit.
	if r, err = s.Update(gd, "", false, true); err != nil || len(r.Changed) != 1 || r.Changed[0].To == newTip {
		t.Fatalf("%v %+v", err, r)
	}

	// Add, then refuse an existing path.
	if err := l.g.Add(l.super, l.libs, "libs/more", "main"); err != nil {
		t.Fatal(err)
	}
	if !l.g.PathExists(l.super, "libs/more") || l.g.PathExists(l.super, "nothing") {
		t.Error("PathExists")
	}
	if got := git(t, l.super, "config", "-f", ".gitmodules", "submodule.libs/more.branch"); got != "main" {
		t.Errorf("branch = %q", got)
	}

	// Remove: refused with a commit that exists nowhere else, then forced.
	more := filepath.Join(l.super, "libs", "more")
	os.WriteFile(filepath.Join(more, "x.txt"), []byte("x\n"), 0o644)
	git(t, more, "add", ".")
	git(t, more, "commit", "-q", "-m", "local only")
	if _, err := s.Remove(gd, "libs/more", false); err == nil || !strings.Contains(err.Error(), "1 commit(s) that no remote has") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(more); err != nil {
		t.Fatal("refused removal deleted the folder")
	}
	if _, err := s.Remove(gd, "libs/more", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(more); !os.IsNotExist(err) {
		t.Error("folder still there")
	}
	if _, err := os.Stat(filepath.Join(gd, "modules", "libs", "more")); !os.IsNotExist(err) {
		t.Error(".git/modules entry still there")
	}
	if out := git(t, l.super, "config", "-f", ".gitmodules", "--get-regexp", "path"); strings.Contains(out, "libs/more") {
		t.Errorf(".gitmodules still lists it: %s", out)
	}
	if es, _ := s.List(gd); len(es) != 2 {
		t.Errorf("list after remove: %+v", es)
	}
	if err := l.g.Sync(l.super); err != nil {
		t.Error(err)
	}
	if p, err := s.Register(gd, "ui"); err != nil || p != ui {
		t.Errorf("%q %v", p, err)
	}
}

type noAccounts struct{ account.Store }

func (noAccounts) Load() *account.Config { return &account.Config{} }

type loc struct{ wt string }

func (loc) GitDirFor(string) string    { return "" }
func (l loc) WorktreeOf(string) string { return l.wt }

var _ repo.Locator = loc{}
