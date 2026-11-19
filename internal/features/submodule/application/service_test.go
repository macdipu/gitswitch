package application

import (
	"errors"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	sub "github.com/macdipu/gitswitch/internal/features/submodule/domain"
)

const (
	h1 = "1111111111111111111111111111111111111111"
	h2 = "2222222222222222222222222222222222222222"
)

type fakeGit struct {
	modules, plain, cached string
	dirty                  map[string]int
	unpushed               int
	exists                 map[string]bool
	updateErr              error
	ran                    []string
	// after an update, the status the next listing returns
	plainAfter string
}

func (f *fakeGit) Modules(string) (string, error) { return f.modules, nil }
func (f *fakeGit) Status(_ string, cached bool) (string, error) {
	if cached {
		return f.cached, nil
	}
	return f.plain, nil
}
func (f *fakeGit) Origin(string) string                          { return "git@github.com:acme/sup.git" }
func (f *fakeGit) Dirty(_, p string) int                         { return f.dirty[p] }
func (f *fakeGit) Upstream(_, _, _, _ string) (string, int, int) { return "origin/main", 1, 2 }
func (f *fakeGit) Unpushed(string, string) int                   { return f.unpushed }
func (f *fakeGit) GitDir(_, p string) string                     { return "/gd/" + p }
func (f *fakeGit) PathExists(_, p string) bool                   { return f.exists[p] }
func (f *fakeGit) Update(_ string, ps []string, remote, rec bool) error {
	f.ran = append(f.ran, "update "+strings.Join(ps, ",")+flag(remote, " remote")+flag(rec, " recursive"))
	if f.updateErr == nil && f.plainAfter != "" {
		f.plain = f.plainAfter
	}
	return f.updateErr
}
func (f *fakeGit) Sync(string) error { f.ran = append(f.ran, "sync"); return nil }
func (f *fakeGit) Add(_, url, p, b string) error {
	f.ran = append(f.ran, "add "+url+" "+p+" "+b)
	return nil
}
func (f *fakeGit) Deinit(_, p string) error     { f.ran = append(f.ran, "deinit "+p); return nil }
func (f *fakeGit) RemovePath(_, p string) error { f.ran = append(f.ran, "rm "+p); return nil }
func (f *fakeGit) DropModule(_, n string) error { f.ran = append(f.ran, "drop "+n); return nil }

func flag(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

type fakeStore struct {
	account.Store
	cfg *account.Config
}

func (s fakeStore) Load() *account.Config { return s.cfg }

type fakeRepos struct {
	repo.Reader
	saved map[string]string
}

func (r fakeRepos) InfoFor(_ *account.Config, gd string) repo.Info {
	if gd == "/super" {
		return repo.Info{Account: "work", Saved: "work"}
	}
	return repo.Info{Saved: r.saved[gd]}
}

type fakeLocator struct{}

func (fakeLocator) GitDirFor(string) string  { return "/super" }
func (fakeLocator) WorktreeOf(string) string { return "/wt" }

func cfg() *account.Config {
	return &account.Config{
		Accounts: []account.Account{
			{ID: "work", Host: "github.com"}, {ID: "client", Host: "github.com"}, {ID: "me", Host: "github.com"},
		},
		Rules: []account.Rule{
			{Kind: "owner", Pattern: "acme", Account: "client"},
			{Kind: "owner", Pattern: "dipu", Account: "me"},
		},
	}
}

func newSvc(f *fakeGit) *Service {
	return &Service{Git: f, Accounts: fakeStore{cfg: cfg()}, Repos: fakeRepos{saved: map[string]string{"/gd/apps/web": "work"}}, Locator: fakeLocator{}}
}

func twoModules() *fakeGit {
	return &fakeGit{
		modules: "submodule.core.path\nlibs/core\x00submodule.core.url\n../core.git\x00submodule.core.branch\nmain\x00" +
			"submodule.web.path\napps/web\x00submodule.web.url\ngit@github.com:dipu/web.git\x00" +
			"submodule.docs.path\ndocs\x00submodule.docs.url\ngit@github.com:other/docs.git\x00",
		plain:  " " + h1 + " libs/core (heads/main)\n+" + h2 + " apps/web (heads/x)\n-" + h1 + " docs\n",
		cached: " " + h1 + " libs/core (heads/main)\n " + h1 + " apps/web\n-" + h1 + " docs\n",
		dirty:  map[string]int{},
		exists: map[string]bool{},
	}
}

func TestListJoinsStateAndAccounts(t *testing.T) {
	f := twoModules()
	f.dirty["apps/web"] = 2
	es, err := newSvc(f).List("/super")
	if err != nil || len(es) != 3 {
		t.Fatalf("%v %+v", err, es)
	}
	core, web, docs := es[0], es[1], es[2]
	if core.State != sub.UpToDate || core.Account != "client" || core.AccountReason != "owner-rule" || core.HasRepoConfigAccount {
		t.Errorf("core (a relative URL resolves against the superproject's acme remote): %+v", core)
	}
	if core.Upstream != "origin/main" || core.Ahead != 1 || core.Behind != 2 || core.Branch != "main" {
		t.Errorf("core upstream: %+v", core)
	}
	if web.State != sub.OtherCommit || web.Recorded != h1 || web.CheckedOut != h2 || web.Dirty != 2 {
		t.Errorf("web: %+v", web)
	}
	// Its own saved account beats the owner rule.
	if web.Account != "work" || !web.HasRepoConfigAccount || web.AccountReason != "saved" {
		t.Errorf("web account: %+v", web)
	}
	// Not initialized: nothing is read inside it; an unmatched owner among 3 accounts asks.
	if docs.State != sub.NotInitialized || docs.Initialized || docs.Dirty != 0 || docs.Account != "" {
		t.Errorf("docs: %+v", docs)
	}
}

func TestListWithoutSubmodulesIsEmptyNotNull(t *testing.T) {
	es, err := newSvc(&fakeGit{}).List("/super")
	if err != nil || es == nil || len(es) != 0 {
		t.Fatalf("%v %#v", err, es)
	}
}

func TestUpdateRefusesWhereItWouldOverwrite(t *testing.T) {
	f := twoModules()
	f.dirty["apps/web"] = 2 // web is at another commit: the update moves it
	_, err := newSvc(f).Update("/super", "", false, true)
	if err == nil || !strings.Contains(err.Error(), "apps/web has 2 changed file(s)") {
		t.Fatalf("err = %v", err)
	}
	if len(f.ran) != 0 {
		t.Fatalf("git ran: %v", f.ran)
	}
	// Updating just another submodule is fine, and so is a dirty one staying put.
	f.dirty["libs/core"] = 5
	if _, err := newSvc(f).Update("/super", "libs/core", false, false); err != nil {
		t.Fatal(err)
	}
	// --remote moves every initialized submodule, so the dirty one blocks.
	if _, err := newSvc(f).Update("/super", "libs/core", true, false); err == nil {
		t.Fatal("remote update of a dirty submodule accepted")
	}
}

func TestUpdateReportsWhatMoved(t *testing.T) {
	f := twoModules()
	f.plainAfter = " " + h1 + " libs/core\n " + h1 + " apps/web\n " + h1 + " docs\n"
	r, err := newSvc(f).Update("/super", "", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.ran[0]; got != "update  remote recursive" && got != "update remote recursive" {
		t.Errorf("ran %q", got)
	}
	if len(r.Changed) != 2 || r.Changed[0].Path != "apps/web" || r.Changed[0].From != h2 || r.Changed[0].To != h1 {
		t.Errorf("changed: %+v", r.Changed)
	}
	if !r.RecordedChanged || !strings.Contains(r.Message, "commit that change") {
		t.Errorf("message: %+v", r)
	}
}

func TestUpdateErrorsAndBadPaths(t *testing.T) {
	f := twoModules()
	f.updateErr = errors.New("git said no")
	if _, err := newSvc(f).Update("/super", "docs", false, false); err == nil || err.Error() != "git said no" {
		t.Fatalf("err = %v", err)
	}
	if _, err := newSvc(f).Update("/super", "nope", false, false); err == nil || !strings.Contains(err.Error(), "not a submodule") {
		t.Fatalf("err = %v", err)
	}
	if _, err := newSvc(f).Update("/super", "../x", false, false); err == nil {
		t.Fatal("path above the repo accepted")
	}
}

func TestInitOnlyTouchesUninitialized(t *testing.T) {
	f := twoModules()
	s := newSvc(f)
	if _, err := s.Init("/super", ""); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || f.ran[0] != "update docs recursive" {
		t.Fatalf("ran %v", f.ran)
	}
	r, err := s.Init("/super", "libs/core")
	if err != nil || !strings.Contains(r.Message, "already initialized") || len(f.ran) != 1 {
		t.Fatalf("%v %+v %v", err, r, f.ran)
	}
}

func TestAddAccountNote(t *testing.T) {
	f := twoModules()
	s := newSvc(f)
	r, err := s.Add("/super", "git@github.com:dipu/tools.git", "vendor/tools", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Account != "me" || !strings.Contains(r.Note, "not this repo's work") {
		t.Errorf("%+v", r)
	}
	if f.ran[0] != "add git@github.com:dipu/tools.git vendor/tools dev" {
		t.Errorf("ran %v", f.ran)
	}
	f.exists["taken"] = true
	for _, c := range [][3]string{
		{"git@github.com:dipu/tools.git", "taken", "path"},
		{"/local/path", "x", "not a repo URL"},
		{"git@github.com:dipu/tools.git", "../x", "not a folder"},
		{"git@github.com:dipu/tools.git", "y", "branch"},
	} {
		br := ""
		if c[2] == "branch" {
			br = "-bad"
		}
		if _, err := s.Add("/super", c[0], c[1], br); err == nil || !strings.Contains(err.Error(), c[2]) {
			t.Errorf("%v: err = %v", c, err)
		}
	}
	if len(f.ran) != 1 {
		t.Errorf("a refused add ran git: %v", f.ran)
	}
}

func TestRemoveRefusesLoss(t *testing.T) {
	f := twoModules()
	f.dirty["libs/core"] = 1
	f.unpushed = 2
	s := newSvc(f)
	plan, err := s.PlanRemove("/super", "libs/core", false)
	if err != nil || !strings.Contains(plan.Blocked, "1 changed file(s) and 2 commit(s)") || len(plan.Deleted) < 4 {
		t.Fatalf("%v %+v", err, plan)
	}
	if _, err := s.Remove("/super", "libs/core", false); err == nil {
		t.Fatal("removed with changes inside")
	}
	if len(f.ran) != 0 {
		t.Fatalf("git ran: %v", f.ran)
	}
	r, err := s.Remove("/super", "libs/core", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, "|") != "deinit libs/core|rm libs/core|drop core" || len(r.Deleted) == 0 {
		t.Errorf("ran %v, %+v", f.ran, r)
	}
	if _, err := s.Remove("/super", "", false); err == nil {
		t.Error("remove with no path accepted")
	}
}

func TestRegisterAndSync(t *testing.T) {
	f := twoModules()
	s := newSvc(f)
	if p, err := s.Register("/super", "libs/core"); err != nil || p != "/wt/libs/core" {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := s.Register("/super", "docs"); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Sync("/super"); err != nil || f.ran[0] != "sync" {
		t.Fatalf("%v %v", err, f.ran)
	}
}
