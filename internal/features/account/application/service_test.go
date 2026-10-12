package application

import (
	"errors"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
)

// fakeStore keeps the settings in memory. Methods the tests don't need
// come from the embedded nil interface and panic if called.
type fakeStore struct {
	account.Store
	c       account.Config
	saves   int
	deleted []string
}

func (f *fakeStore) Load() *account.Config { c := f.c; return &c }
func (f *fakeStore) SuggestedName() string { return "Suggested Name" }
func (f *fakeStore) Save(a account.Account) error {
	f.saves++
	for i := range f.c.Accounts {
		if f.c.Accounts[i].ID == a.ID {
			f.c.Accounts[i] = a
			return nil
		}
	}
	f.c.Accounts = append(f.c.Accounts, a)
	return nil
}
func (f *fakeStore) Remove(id string) error {
	var keep []account.Account
	for _, a := range f.c.Accounts {
		if a.ID != id {
			keep = append(keep, a)
		}
	}
	f.c.Accounts = keep
	return nil
}
func (f *fakeStore) Rename(oldID, newID string) error {
	for i := range f.c.Accounts {
		if f.c.Accounts[i].ID == oldID {
			f.c.Accounts[i].ID = newID
		}
	}
	return nil
}
func (f *fakeStore) DeleteKeyFiles(a *account.Account) []string {
	f.deleted = append(f.deleted, a.Key)
	return []string{a.Key}
}
func (f *fakeStore) RemoveRule(r account.Rule) error {
	for i, x := range f.c.Rules {
		if x == r {
			f.c.Rules = append(f.c.Rules[:i], f.c.Rules[i+1:]...)
			return nil
		}
	}
	return errors.New("no such rule")
}

type fakeRepoAccounts struct {
	account.RepoAccounts
	saved   map[string]string // git dir -> account id
	applied map[string]string
}

func (f *fakeRepoAccounts) SetRepoAccount(gitDir string, a *account.Account) (string, error) {
	f.saved[gitDir] = a.ID
	return "", nil
}
func (f *fakeRepoAccounts) SaveRepoAccountID(gitDir, id string) error {
	f.saved[gitDir] = id
	return nil
}
func (f *fakeRepoAccounts) ForgetRepo(gitDir string) error { delete(f.saved, gitDir); return nil }
func (f *fakeRepoAccounts) ApplyIdentity(gitDir string, a *account.Account) {
	f.applied[gitDir] = a.Email
}

type fakeKeys struct {
	ssh.KeyTool
	generated []string
	fail      error
}

func (f *fakeKeys) Generate(a *account.Account, interactive bool, passphrase string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	f.generated = append(f.generated, a.ID)
	return "~/.ssh/id_ed25519_" + a.ID, nil
}

type fakeRepos struct {
	repo.Reader
	list []repo.Info
}

func (f *fakeRepos) List(*account.Config) []repo.Info { return f.list }

func newService() (*Service, *fakeStore, *fakeRepoAccounts, *fakeKeys) {
	st := &fakeStore{c: account.Config{Accounts: []account.Account{{ID: "work", Email: "w@x.com", Key: "~/.ssh/work"}}}}
	ra := &fakeRepoAccounts{saved: map[string]string{}, applied: map[string]string{}}
	k := &fakeKeys{}
	return &Service{Store: st, RepoAccounts: ra, Keys: k, Repos: &fakeRepos{}}, st, ra, k
}

func TestAdd(t *testing.T) {
	s, st, _, k := newService()
	if _, err := s.Add(account.Account{ID: "work", Email: "a@b.c"}, account.KeyChoice{}); err == nil {
		t.Error("a second account with the same id must be refused")
	}
	if _, err := s.Add(account.Account{ID: "me"}, account.KeyChoice{}); err == nil {
		t.Error("an account without a commit email must be refused")
	}
	acc, err := s.Add(account.Account{ID: "me", Email: "me@x.com"}, account.KeyChoice{New: true})
	if err != nil {
		t.Fatal(err)
	}
	if acc.Name != "Suggested Name" {
		t.Errorf("name = %q, want the suggested name", acc.Name)
	}
	if len(k.generated) != 1 || acc.Key != "~/.ssh/id_ed25519_me" {
		t.Errorf("key not generated and saved: generated=%v key=%q", k.generated, acc.Key)
	}
	if got := st.c.Account("me"); got == nil || got.Key != acc.Key {
		t.Errorf("stored account = %+v, want the new key saved", got)
	}

	k.fail = errors.New("ssh-keygen failed")
	if _, err := s.Add(account.Account{ID: "other", Email: "o@x.com"}, account.KeyChoice{New: true}); err == nil {
		t.Error("a key generation failure must be returned")
	}
	if st.c.Account("other") != nil {
		t.Error("a failed key generation must not leave the account behind")
	}
	k.fail = nil
	if _, err := s.Add(account.Account{ID: "other", Email: "o@x.com"}, account.KeyChoice{New: true}); err != nil {
		t.Errorf("adding again after a failed key generation: %v", err)
	}
}

func TestUpdateKeepsIDAndUser(t *testing.T) {
	s, st, _, _ := newService()
	st.c.Accounts[0].User = "octo"
	if err := s.Update(account.Account{ID: "WORK", Email: "new@x.com"}); err != nil {
		t.Fatal(err)
	}
	got := st.c.Accounts[0]
	if got.ID != "work" || got.User != "octo" || got.Email != "new@x.com" {
		t.Errorf("updated account = %+v", got)
	}
	if err := s.Update(account.Account{ID: "ghost"}); err == nil {
		t.Error("updating an unknown account must fail")
	}
}

func TestRemoveDeletesKeysOnlyWhenAsked(t *testing.T) {
	s, st, _, _ := newService()
	st.c.Accounts = append(st.c.Accounts, account.Account{ID: "me", Key: "~/.ssh/me"})
	if files, err := s.Remove("work", false); err != nil || files != nil || len(st.deleted) != 0 {
		t.Errorf("remove without keys: files=%v err=%v deleted=%v", files, err, st.deleted)
	}
	if files, err := s.Remove("me", true); err != nil || len(files) != 1 || files[0] != "~/.ssh/me" {
		t.Errorf("remove with keys: files=%v err=%v", files, err)
	}
	if len(st.c.Accounts) != 0 {
		t.Errorf("accounts left: %+v", st.c.Accounts)
	}
}

func TestRenameUpdatesSavedRepos(t *testing.T) {
	s, _, ra, _ := newService()
	s.Repos = &fakeRepos{list: []repo.Info{
		{GitDir: "/a/.git", Saved: "Work"},
		{GitDir: "/b/.git", Saved: "me"},
		{GitDir: "/c/.git", Saved: "work"},
	}}
	n, err := s.Rename("WORK", "job")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || ra.saved["/a/.git"] != "job" || ra.saved["/c/.git"] != "job" || ra.saved["/b/.git"] != "" {
		t.Errorf("updated %d repos: %v", n, ra.saved)
	}
	if _, err := s.Rename("ghost", "x"); err == nil {
		t.Error("renaming an unknown account must fail")
	}
}

func TestRemoveRuleAt(t *testing.T) {
	s, st, _, _ := newService()
	st.c.Rules = []account.Rule{{Kind: "owner", Pattern: "a", Account: "work"}, {Kind: "owner", Pattern: "b", Account: "work"}}
	for _, i := range []int{-1, 2} {
		if err := s.RemoveRuleAt(i); err == nil {
			t.Errorf("RemoveRuleAt(%d) must fail", i)
		}
	}
	if err := s.RemoveRuleAt(1); err != nil || len(st.c.Rules) != 1 || st.c.Rules[0].Pattern != "a" {
		t.Errorf("err=%v rules=%v", err, st.c.Rules)
	}
}

func TestRepoAccount(t *testing.T) {
	s, _, ra, _ := newService()
	if _, err := s.SetRepoAccount("/r/.git", "work"); err != nil || ra.saved["/r/.git"] != "work" {
		t.Fatalf("err=%v saved=%v", err, ra.saved)
	}
	if _, err := s.SetRepoAccount("/r/.git", ""); err != nil || ra.saved["/r/.git"] != "" {
		t.Errorf("an empty id must forget the repo's account: err=%v saved=%v", err, ra.saved)
	}
	if _, err := s.SetRepoAccount("/r/.git", "ghost"); err == nil || !strings.Contains(err.Error(), "no account") {
		t.Errorf("unknown account: err=%v", err)
	}
	if err := s.UseOnce("/r/.git", "work"); err != nil || ra.applied["/r/.git"] != "w@x.com" || ra.saved["/r/.git"] != "" {
		t.Errorf("use once must apply the identity without saving: err=%v applied=%v saved=%v", err, ra.applied, ra.saved)
	}
}
