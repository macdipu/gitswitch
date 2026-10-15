package application

import (
	"errors"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
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

// fakeAPI knows tokens by value: token -> login.
type fakeAPI struct {
	users map[string]string
	bases []string
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
}
