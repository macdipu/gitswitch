package application

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

type fakeAccounts struct{ c account.Config }

func (f *fakeAccounts) Load() *account.Config { c := f.c; return &c }
func (f *fakeAccounts) SetUser(id, user string) error {
	for i := range f.c.Accounts {
		if f.c.Accounts[i].ID == id {
			f.c.Accounts[i].User = user
		}
	}
	return nil
}

type fakeSecrets struct {
	m   map[string]string
	err error // every call fails with it
}

func (f *fakeSecrets) Get(id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if t, ok := f.m[id]; ok {
		return t, nil
	}
	return "", hosting.ErrNoToken
}
func (f *fakeSecrets) Set(id, t string) error { f.m[id] = t; return f.err }
func (f *fakeSecrets) Delete(id string) error { delete(f.m, id); return f.err }

// fakeAPI knows tokens by value: token -> login, and token -> repos.
type fakeAPI struct {
	users  map[string]string
	bases  []string
	repos  map[string][]hosting.RemoteRepo
	calls  int
	err    error // Repos and Inbox fail with it
	logins []string
}

func (f *fakeAPI) Inbox(base, token, login string) ([]hosting.PullRequest, []hosting.PullRequest, []hosting.PullRequest, error) {
	f.calls++
	if f.err != nil {
		return nil, nil, nil, f.err
	}
	f.logins = append(f.logins, login)
	return []hosting.PullRequest{{Repo: "acme/api", Number: 7, Title: "Add billing", ReviewDecision: "REVIEW_REQUIRED"}},
		[]hosting.PullRequest{{Repo: "acme/web", Number: 3, Title: "Fix login", Mergeable: "CONFLICTING", ReviewDecision: "APPROVED"}},
		nil, nil
}

func (f *fakeAPI) Repos(base, token string) ([]hosting.RemoteRepo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]hosting.RemoteRepo{}, f.repos[token]...), nil
}

func (f *fakeAPI) Whoami(base, token string) (hosting.User, hosting.Grant, error) {
	f.bases = append(f.bases, base)
	if l, ok := f.users[token]; ok {
		return hosting.User{Login: l}, hosting.Grant{Expires: "2027-01-01"}, nil
	}
	return hosting.User{}, hosting.Grant{}, errors.New("GitHub refused the token")
}

func newService() (*Service, *fakeAccounts, *fakeSecrets, *fakeAPI) {
	a := &fakeAccounts{c: account.Config{Accounts: []account.Account{
		{ID: "work", Host: "github.com", User: "dipu-work"},
		{ID: "home", Host: "ssh.github.com"},
		{ID: "lab", Host: "gitlab.com"},
	}}}
	s := &fakeSecrets{m: map[string]string{}}
	api := &fakeAPI{users: map[string]string{"tok-work": "dipu-work", "tok-home": "dipu"}}
	return &Service{Accounts: a, Secrets: s, API: api}, a, s, api
}

func TestSignIn(t *testing.T) {
	s, a, sec, api := newService()
	if _, err := s.SignIn("work", "tok-home"); err == nil || !strings.Contains(err.Error(), "logs in as dipu-work") {
		t.Errorf("a token of another user: %v", err)
	}
	if _, err := s.SignIn("work", "nope"); err == nil {
		t.Error("a refused token must fail")
	}
	if len(sec.m) != 0 {
		t.Fatalf("nothing may be saved for refused tokens: %v", sec.m)
	}
	got, err := s.SignIn("WORK", " tok-work ")
	if err != nil || got.Account != "work" || got.Login != "dipu-work" || got.Expires != "2027-01-01" || sec.m["work"] != "tok-work" {
		t.Fatalf("SignIn = %+v, %v; saved %v", got, err, sec.m)
	}
	if _, err := s.SignIn("home", "tok-home"); err != nil || a.c.Accounts[1].User != "dipu" {
		t.Errorf("an untested account learns its user: err=%v user=%q", err, a.c.Accounts[1].User)
	}
	if api.bases[len(api.bases)-1] != "https://api.github.com" {
		t.Errorf("ssh.github.com must use api.github.com, got %v", api.bases)
	}
	if _, err := s.SignIn("lab", "tok-work"); err == nil || !strings.Contains(err.Error(), "GitHub only") {
		t.Errorf("GitLab: %v", err)
	}
	if _, err := s.SignIn("ghost", "tok-work"); err == nil {
		t.Error("an unknown account must fail")
	}
}

func TestStatusCheckAndSignOut(t *testing.T) {
	s, _, sec, _ := newService()
	sec.m["work"] = "tok-work"
	st := s.Status()
	if len(st) != 3 || !st[0].SignedIn || st[1].SignedIn || st[2].Supported {
		t.Errorf("status = %+v", st)
	}
	if got, err := s.Check("work"); err != nil || got.Login != "dipu-work" {
		t.Errorf("Check = %+v, %v", got, err)
	}
	if _, err := s.Check("home"); err == nil || !strings.Contains(err.Error(), "gitswitch signin home") {
		t.Errorf("Check without a token: %v", err)
	}
	if err := s.SignOut("work"); err != nil || len(sec.m) != 0 {
		t.Errorf("SignOut: %v %v", err, sec.m)
	}
	if err := s.SignOut("work"); err != nil {
		t.Errorf("signing out twice: %v", err)
	}
	sec.err = errors.New("keychain locked")
	if st := s.Status(); st[0].Error != "keychain locked" {
		t.Errorf("a keychain error must show: %+v", st[0])
	}
	sec.err = fmt.Errorf("%w: install secret-tool", hosting.ErrNoKeychain)
	if st := s.Status(); st[0].Error != "" || st[0].SignedIn {
		t.Errorf("no keychain at all is not an error, just nobody signed in: %+v", st[0])
	}
}

type fakeHelper struct {
	hook     string
	entries  []hosting.HelperEntry
	sections map[string][]string
}

func (f *fakeHelper) Hook() string                            { return f.hook }
func (f *fakeHelper) Entries() ([]hosting.HelperEntry, error) { return f.entries, nil }
func (f *fakeHelper) Write(host string, v []string) error     { f.sections[host] = v; return nil }
func (f *fakeHelper) Remove(host string) error                { delete(f.sections, host); return nil }

type fakeRepos struct {
	current string
	account map[string]string // git dir -> account id
}

func (f *fakeRepos) Current() string { return f.current }
func (f *fakeRepos) List(*account.Config) []repo.Info {
	return []repo.Info{{Path: "/src/api", Remote: "https://github.com/Acme/api"}}
}
func (f *fakeRepos) AccountFor(c *account.Config, gd string) *account.Account {
	return c.Account(f.account[gd])
}

func TestHelperFollowsSignIns(t *testing.T) {
	s, _, _, _ := newService()
	h := &fakeHelper{hook: `"/bin/gitswitch" ssh`, entries: []hosting.HelperEntry{{URL: "", Value: "osxkeychain"}}, sections: map[string][]string{}}
	s.Helper = h
	r, err := s.SignIn("work", "tok-work")
	if err != nil || r.HTTPS != "" {
		t.Fatalf("SignIn = %+v, %v", r, err)
	}
	if got := h.sections["github.com"]; !reflect.DeepEqual(got, []string{"", `!"/bin/gitswitch" credential`, "osxkeychain"}) {
		t.Fatalf("section = %q", got)
	}
	if _, err := s.SignIn("home", "tok-home"); err != nil {
		t.Fatal(err)
	}
	if err := s.SignOut("work"); err != nil || h.sections["github.com"] == nil {
		t.Fatalf("home is still signed in on github.com: %v %v", err, h.sections)
	}
	if err := s.SignOut("home"); err != nil || h.sections["github.com"] != nil {
		t.Fatalf("nobody is signed in: %v %v", err, h.sections)
	}
	h.hook = "ssh"
	if r, _ := s.SignIn("work", "tok-work"); !strings.Contains(r.HTTPS, "gitswitch install") || h.sections["github.com"] != nil {
		t.Errorf("not installed: %+v %v", r, h.sections)
	}
}

func TestCredential(t *testing.T) {
	s, _, sec, _ := newService()
	repos := &fakeRepos{current: "/src/api/.git", account: map[string]string{"/src/api/.git": "work", "/src/web/.git": "home"}}
	s.Repos = repos
	sec.m["work"] = "tok-work"
	req := hosting.Request{Protocol: "https", Host: "github.com"}
	if u, p, ok := s.Credential(req); !ok || u != "dipu-work" || p != "tok-work" {
		t.Fatalf("Credential = %q %q %v", u, p, ok)
	}
	cases := map[string]func(){
		"http":          func() { req.Protocol = "http" },
		"another host":  func() { req.Host = "gitlab.com" },
		"another user":  func() { req.Username = "someone" },
		"not signed in": func() { repos.current = "/src/web/.git" },
		"no repo":       func() { repos.current = "" },
		"no account":    func() { repos.current = "/src/other/.git" },
	}
	for name, change := range cases {
		req = hosting.Request{Protocol: "https", Host: "github.com"}
		repos.current = "/src/api/.git"
		change()
		if _, _, ok := s.Credential(req); ok {
			t.Errorf("%s: must not answer", name)
		}
	}
	req = hosting.Request{Protocol: "https", Host: "ssh.github.com", Username: "Dipu-Work"}
	repos.current = "/src/api/.git"
	if _, _, ok := s.Credential(req); !ok {
		t.Error("the same user in another case, on a port-443 host, must be answered")
	}
}

type memCache map[string]hosting.RepoList

func (m memCache) Load(id string) (hosting.RepoList, bool)  { l, ok := m[id]; return l, ok }
func (m memCache) Save(id string, l hosting.RepoList) error { m[id] = l; return nil }
func (m memCache) Delete(id string) error                   { delete(m, id); return nil }

func TestBrowse(t *testing.T) {
	s, _, sec, api := newService()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cache := memCache{}
	s.Cache, s.Now, s.Repos = cache, func() time.Time { return now }, &fakeRepos{}
	sec.m["work"], sec.m["home"] = "tok-work", "tok-home"
	api.repos = map[string][]hosting.RemoteRepo{
		"tok-work": {{FullName: "acme/api", Owner: "acme", SSHURL: "git@github.com:acme/api.git"}, {FullName: "acme/web", Owner: "acme"}},
		"tok-home": {{FullName: "dipu/notes", Owner: "dipu"}, {FullName: "acme/api", Owner: "acme"}},
	}

	if b := s.Browse(FetchNone); len(b.Repos) != 0 || len(b.Accounts) != 2 || api.calls != 0 {
		t.Fatalf("nothing cached and no fetch: %+v, %d calls", b, api.calls)
	}
	b := s.Browse(FetchStale)
	if api.calls != 2 || len(b.Repos) != 3 || b.Repos[0].FullName != "acme/api" || b.Repos[0].Account != "work" {
		t.Fatalf("fetched: %+v, %d calls", b.Repos, api.calls)
	}
	if c := b.Repos[0].Cloned; len(c) != 1 || c[0] != "/src/api" {
		t.Errorf("acme/api is cloned at /src/api: %v", c)
	}
	if b.Repos[1].Cloned == nil || len(b.Repos[1].Cloned) != 0 {
		t.Errorf("acme/web is not cloned: %v", b.Repos[1].Cloned)
	}
	s.Browse(FetchStale)
	if api.calls != 2 {
		t.Errorf("a fresh cache must not be read again: %d calls", api.calls)
	}
	now = now.Add(2 * time.Hour)
	if b := s.Browse(FetchNone); !b.Accounts[0].Stale || len(b.Repos) != 3 {
		t.Errorf("old lists are shown, marked stale: %+v", b.Accounts)
	}
	api.err = errors.New("network down")
	b = s.Browse(FetchAll)
	if b.Accounts[0].Error != "network down" || len(b.Repos) != 3 {
		t.Errorf("a failed read keeps the last list: %+v", b)
	}
	if err := s.SignOut("home"); err != nil || len(cache) != 1 {
		t.Errorf("signing out forgets what the account saw: %v %v", err, cache)
	}
}
