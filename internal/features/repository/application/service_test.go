package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

// fakeLists keeps the repo lists in memory.
type fakeLists struct {
	c     account.Config
	lists map[string][]string
}

func (f *fakeLists) Load() *account.Config {
	c := f.c
	c.Recent = nil
	for _, p := range f.lists[repo.ListRecent] { // stored newest last, loaded newest first
		c.Recent = append([]string{p}, c.Recent...)
	}
	return &c
}
func (f *fakeLists) AddRepoPath(list, path string) error {
	for _, p := range f.lists[list] {
		if p == path {
			return nil
		}
	}
	f.lists[list] = append(f.lists[list], path)
	return nil
}
func (f *fakeLists) RemoveRepoPath(list, path string) error {
	var keep []string
	for _, p := range f.lists[list] {
		if p != path {
			keep = append(keep, p)
		}
	}
	f.lists[list] = keep
	return nil
}

func (f *fakeLists) SetRepoGroup(path, name string) error {
	var keep []account.RepoGroup
	for _, g := range f.c.Groups {
		if g.Path != path {
			keep = append(keep, g)
		}
	}
	if name != "" {
		keep = append(keep, account.RepoGroup{Name: name, Path: path})
	}
	f.c.Groups = keep
	return nil
}
func (f *fakeLists) SetSetting(key, value string) error {
	if key != "repoGroups" {
		return errors.New("unexpected setting " + key)
	}
	f.c.RepoGroups = value
	return nil
}

// fakeGit knows which folders are repos: worktree -> git dir.
type fakeGit struct {
	repos   map[string]string
	scanned []string         // worktrees found in the project folders
	lists   *fakeLists       // List shows scanned + added - hidden
	cloned  []string         // "url -> dir (account)"
	onClone func(dir string) // makes the clone exist
	cloneFn func() error     // optional failure
}

func (g *fakeGit) GitDirFor(path string) string {
	for wt, gd := range g.repos {
		if path == wt || path == gd || strings.HasPrefix(path, wt+string(filepath.Separator)) {
			return gd
		}
	}
	return ""
}
func (g *fakeGit) WorktreeOf(gitDir string) string {
	for wt, gd := range g.repos {
		if gd == gitDir {
			return wt
		}
	}
	return gitDir
}
func (g *fakeGit) AccountFor(*account.Config, string) (*account.Account, string) { return nil, "" }
func (g *fakeGit) InfoFor(_ *account.Config, gitDir string) repo.Info {
	return repo.Info{Path: g.WorktreeOf(gitDir), GitDir: gitDir}
}
func (g *fakeGit) Scan(*account.Config, []string) []repo.Info {
	var out []repo.Info
	for _, wt := range g.scanned {
		out = append(out, repo.Info{Path: wt})
	}
	return out
}
func (g *fakeGit) List(*account.Config) []repo.Info {
	hidden := map[string]bool{}
	for _, p := range g.lists.lists[repo.ListHidden] {
		hidden[p] = true
	}
	var out []repo.Info
	for _, wt := range append(append([]string{}, g.scanned...), g.lists.lists[repo.ListAdded]...) {
		if !hidden[wt] {
			out = append(out, repo.Info{Path: wt})
		}
	}
	return out
}
func (g *fakeGit) Clone(parent, url, folder, accountID string, progress func(string)) error {
	if g.cloneFn != nil {
		return g.cloneFn()
	}
	dir := filepath.Join(parent, folder)
	g.cloned = append(g.cloned, url+" -> "+dir+" ("+accountID+")")
	progress("Cloning into '" + folder + "'...")
	if g.onClone != nil {
		g.onClone(dir)
	}
	return nil
}

type fakeRepoAccounts struct {
	account.RepoAccounts
	saved map[string]string
}

func (f *fakeRepoAccounts) SetRepoAccount(gitDir string, a *account.Account) (string, error) {
	f.saved[gitDir] = a.ID
	return "", nil
}

func newService(t *testing.T) (*Service, *fakeGit, *fakeLists, *fakeRepoAccounts) {
	t.Helper()
	l := &fakeLists{
		c:     account.Config{Accounts: []account.Account{{ID: "work", Host: "github.com"}}},
		lists: map[string][]string{},
	}
	g := &fakeGit{repos: map[string]string{}, lists: l}
	ra := &fakeRepoAccounts{saved: map[string]string{}}
	return &Service{Lists: l, Reader: g, Locator: g, Cloner: g, RepoAccounts: ra}, g, l, ra
}

func abs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAddRecordsTheRepoRoot(t *testing.T) {
	s, g, l, _ := newService(t)
	wt := abs(t, "/src/api")
	g.repos[wt] = filepath.Join(wt, ".git")
	l.lists[repo.ListHidden] = []string{wt}

	info, err := s.Add(filepath.Join(wt, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != wt || len(l.lists[repo.ListAdded]) != 1 || l.lists[repo.ListAdded][0] != wt {
		t.Errorf("info=%+v added=%v", info, l.lists[repo.ListAdded])
	}
	if len(l.lists[repo.ListHidden]) != 0 {
		t.Errorf("adding a repo must unhide it: hidden=%v", l.lists[repo.ListHidden])
	}
	if _, err := s.Add(abs(t, "/not/a/repo")); err == nil || !strings.Contains(err.Error(), "not a git repo") {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveHidesScannedRepos(t *testing.T) {
	s, g, l, _ := newService(t)
	scanned, added := abs(t, "/src/scanned"), abs(t, "/src/added")
	g.repos[scanned] = filepath.Join(scanned, ".git")
	g.repos[added] = filepath.Join(added, ".git")
	g.scanned = []string{scanned}
	l.lists[repo.ListAdded] = []string{added}
	l.lists[repo.ListPinned] = []string{added}

	if err := s.Remove(added); err != nil {
		t.Fatal(err)
	}
	if len(l.lists[repo.ListAdded]) != 0 || len(l.lists[repo.ListPinned]) != 0 || len(l.lists[repo.ListHidden]) != 0 {
		t.Errorf("a repo added by hand is only taken off the lists: %v", l.lists)
	}
	if err := s.Remove(scanned); err != nil {
		t.Fatal(err)
	}
	if h := l.lists[repo.ListHidden]; len(h) != 1 || h[0] != scanned {
		t.Errorf("a scanned repo must be hidden: %v", h)
	}
	if err := s.Remove(scanned); err == nil {
		t.Error("removing a repo that is not listed must fail")
	}
}

func TestPinNeedsAListedRepo(t *testing.T) {
	s, g, l, _ := newService(t)
	wt := abs(t, "/src/api")
	g.repos[wt] = filepath.Join(wt, ".git")
	if err := s.Pin(wt, true); err == nil || !strings.Contains(err.Error(), "repo add") {
		t.Errorf("pinning an unlisted repo: err=%v", err)
	}
	l.lists[repo.ListAdded] = []string{wt}
	if err := s.Pin(wt, true); err != nil || len(l.lists[repo.ListPinned]) != 1 {
		t.Errorf("err=%v pinned=%v", err, l.lists[repo.ListPinned])
	}
	if err := s.Pin(wt, false); err != nil || len(l.lists[repo.ListPinned]) != 0 {
		t.Errorf("err=%v pinned=%v", err, l.lists[repo.ListPinned])
	}
}

func TestOpenedKeepsTheNewestFirst(t *testing.T) {
	s, g, l, _ := newService(t)
	var wts []string
	for i := 0; i < repo.RecentMax+2; i++ {
		wt := abs(t, "/src/r"+string(rune('a'+i)))
		g.repos[wt] = filepath.Join(wt, ".git")
		wts = append(wts, wt)
	}
	if err := s.Opened(filepath.Join(wts[0], "cmd")); err != nil {
		t.Fatal(err)
	}
	if got := l.lists[repo.ListRecent]; len(got) != 1 || got[0] != wts[0] {
		t.Fatalf("a folder inside a repo must record the repo: %v", got)
	}
	for _, wt := range wts {
		if err := s.Opened(wt); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Opened(wts[3]); err != nil { // opened again: moves to the front
		t.Fatal(err)
	}
	r := s.Recent()
	if len(r) != repo.RecentMax || r[0].Path != wts[3] || r[1].Path != wts[len(wts)-1] {
		t.Errorf("recent = %v", r)
	}
	for _, x := range r {
		if x.Path == wts[0] || x.Path == wts[1] {
			t.Errorf("the oldest must be forgotten past %d: %v", repo.RecentMax, r)
		}
	}
	if err := s.Opened(abs(t, "/not/a/repo")); err == nil {
		t.Error("a folder that is not a repo must not be recorded")
	}

	delete(g.repos, wts[3]) // its folder is gone
	if r := s.Recent(); len(r) != repo.RecentMax-1 || r[0].Path != wts[len(wts)-1] {
		t.Errorf("a repo that is gone must be left out: %v", r)
	}
}

func TestRemoveForgetsARecentRepo(t *testing.T) {
	s, g, l, _ := newService(t)
	wt := abs(t, "/src/api")
	g.repos[wt] = filepath.Join(wt, ".git")
	l.lists[repo.ListAdded] = []string{wt}
	if err := s.Opened(wt); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(wt); err != nil {
		t.Fatal(err)
	}
	if len(l.lists[repo.ListRecent]) != 0 {
		t.Errorf("recent = %v", l.lists[repo.ListRecent])
	}
}

func TestGroups(t *testing.T) {
	s, g, l, _ := newService(t)
	api, web := abs(t, "/src/api"), abs(t, "/src/web")
	g.repos[api], g.repos[web] = filepath.Join(api, ".git"), filepath.Join(web, ".git")
	l.lists[repo.ListAdded] = []string{api}

	if err := s.SetGroup(web, "Client"); err == nil || !strings.Contains(err.Error(), "repo add") {
		t.Errorf("grouping an unlisted repo: %v", err)
	}
	if err := s.SetGroup(api, "a=b"); err == nil {
		t.Error("a bad group name must be refused")
	}
	l.lists[repo.ListAdded] = []string{api, web}
	if err := s.SetGroup(api, " Client "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroup(web, "client"); err != nil { // same group, other case
		t.Fatal(err)
	}
	if got := s.GroupNames(); len(got) != 1 || got[0] != "Client" {
		t.Errorf("group names = %v, want one Client", got)
	}
	if err := s.SetGroup(web, ""); err != nil || len(l.c.Groups) != 1 {
		t.Errorf("taking web out: err=%v groups=%v", err, l.c.Groups)
	}

	if s.GroupBy() != repo.ByNone {
		t.Errorf("group by starts as none, got %q", s.GroupBy())
	}
	if err := s.SetGroupBy("colour"); err == nil {
		t.Error("an unknown way to group must be refused")
	}
	if err := s.SetGroupBy("Folder"); err != nil || s.GroupBy() != repo.ByFolder {
		t.Errorf("err=%v by=%q", err, s.GroupBy())
	}
	l.c.RepoGroups = "from-a-newer-version"
	if s.GroupBy() != repo.ByNone {
		t.Errorf("an unknown saved value must read as none, got %q", s.GroupBy())
	}
	if gs, err := s.Groups(repo.ByFolder, ""); err != nil || len(gs) != 1 || len(gs[0].Repos) != 2 {
		t.Errorf("groups = %+v, %v", gs, err)
	}

	if err := s.Remove(api); err != nil || len(l.c.Groups) != 0 {
		t.Errorf("removing a repo must take it out of its group: err=%v groups=%v", err, l.c.Groups)
	}
}

func TestCloneRefusesBadInput(t *testing.T) {
	s, g, _, _ := newService(t)
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ url, dir, acc, want string }{
		{"file:///etc", t.TempDir(), "", "file:// links are not supported"},
		{"ext::sh -c touch% /tmp/x", t.TempDir(), "", "not a repo URL"},
		{"git@github.com:acme/api.git", t.TempDir(), "ghost", "no account"},
		{"git@gitlab.com:acme/api.git", t.TempDir(), "work", "is an account on github.com"},
		{"git@github.com:acme/api.git", " ", "", "choose a folder"},
		{"git@github.com:acme/api.git", full, "", "not empty"},
	}
	for _, c := range cases {
		_, err := s.Clone(c.url, c.dir, c.acc, func(string) {})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Clone(%q, %q, %q) err = %v, want %q", c.url, c.dir, c.acc, err, c.want)
		}
	}
	if len(g.cloned) != 0 {
		t.Errorf("git must not run for refused input: %v", g.cloned)
	}
}

func TestCloneSavesTheAccountAndAddsTheRepo(t *testing.T) {
	s, g, l, ra := newService(t)
	dir := filepath.Join(t.TempDir(), "api")
	g.onClone = func(d string) { g.repos[d] = filepath.Join(d, ".git") }
	var progress []string

	info, err := s.Clone(" git@ssh.github.com:acme/api.git ", dir, "WORK", func(l string) { progress = append(progress, l) })
	if err != nil {
		t.Fatal(err)
	}
	if len(g.cloned) != 1 || !strings.HasSuffix(g.cloned[0], "(work)") || strings.HasPrefix(g.cloned[0], " ") {
		t.Errorf("cloned = %v, want a trimmed URL cloned with the account id", g.cloned)
	}
	if len(progress) != 1 {
		t.Errorf("progress = %v", progress)
	}
	if ra.saved[filepath.Join(dir, ".git")] != "work" {
		t.Errorf("account not saved in the clone: %v", ra.saved)
	}
	if info.Path != dir || len(l.lists[repo.ListAdded]) != 1 {
		t.Errorf("info=%+v added=%v", info, l.lists[repo.ListAdded])
	}
}

func TestCloneDryRunAndErrors(t *testing.T) {
	s, g, l, _ := newService(t)
	s.DryRun = true
	dir := filepath.Join(t.TempDir(), "api")
	if info, err := s.Clone("https://github.com/acme/api", dir, "", func(string) {}); err != nil || info.Path != dir || len(l.lists[repo.ListAdded]) != 0 {
		t.Errorf("dry run: info=%+v err=%v added=%v", info, err, l.lists[repo.ListAdded])
	}

	s.DryRun = false
	g.cloneFn = func() error { return os.ErrPermission }
	if _, err := s.Clone("https://github.com/acme/api", filepath.Join(t.TempDir(), "x"), "", func(string) {}); err == nil || !strings.Contains(err.Error(), "cannot access it") {
		t.Errorf("a permission error must carry the access hint: %v", err)
	}
	g.cloneFn = nil
	if _, err := s.Clone("https://github.com/acme/api", filepath.Join(t.TempDir(), "y"), "", func(string) {}); err == nil || !strings.Contains(err.Error(), "is not a repo") {
		t.Errorf("a clone that left no repo must fail: %v", err)
	}
}
