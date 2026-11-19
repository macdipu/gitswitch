package application

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
)

// fakeInit makes the folder a repo in the fake git.
type fakeInit struct {
	g       *fakeGit
	version string
	err     error
	made    []string
}

func (f *fakeInit) Init(dir, branch string) error {
	if f.err != nil {
		return f.err
	}
	f.made = append(f.made, dir+" ("+branch+")")
	f.g.repos[dir] = filepath.Join(dir, ".git")
	return nil
}
func (f *fakeInit) Version() (string, error) {
	if f.version == "" {
		return "", errors.New("not found")
	}
	return f.version, nil
}

func newInitService(t *testing.T, accounts ...account.Account) (*Service, *fakeInit, *fakeLists, *fakeRepoAccounts) {
	s, g, l, ra := newService(t)
	l.c.Accounts = accounts
	fi := &fakeInit{g: g, version: "git version 2.43.0"}
	s.Initializer = fi
	return s, fi, l, ra
}

func TestInitUsesFlagRuleDefaultOrOnlyAccount(t *testing.T) {
	work, me := account.Account{ID: "work", Host: "github.com"}, account.Account{ID: "me", Host: "github.com"}
	dir := abs(t, "/src/new")

	// The flag.
	s, fi, l, ra := newInitService(t, work, me)
	info, err := s.Init(dir, "me", "trunk")
	if err != nil || info.Path != dir || ra.saved[filepath.Join(dir, ".git")] != "me" || fi.made[0] != dir+" (trunk)" {
		t.Fatalf("flag: %+v %v %v %v", info, err, ra.saved, fi.made)
	}
	if len(l.lists[repo.ListAdded]) != 1 || l.lists[repo.ListAdded][0] != dir {
		t.Errorf("the repo must be in the list: %v", l.lists)
	}

	// A folder rule.
	s, _, l, ra = newInitService(t, work, me)
	l.c.Rules = []account.Rule{{Kind: "folder", Pattern: filepath.ToSlash(abs(t, "/src")), Account: "work"}}
	if _, err := s.Init(dir, "", ""); err != nil || ra.saved[filepath.Join(dir, ".git")] != "work" {
		t.Fatalf("rule: %v %v", err, ra.saved)
	}

	// The default.
	s, _, l, ra = newInitService(t, work, me)
	l.c.Default = "me"
	if _, err := s.Init(dir, "", ""); err != nil || ra.saved[filepath.Join(dir, ".git")] != "me" {
		t.Fatalf("default: %v %v", err, ra.saved)
	}

	// The only account.
	s, _, _, ra = newInitService(t, work)
	if _, err := s.Init(dir, "", ""); err != nil || ra.saved[filepath.Join(dir, ".git")] != "work" {
		t.Fatalf("only: %v %v", err, ra.saved)
	}

	// No accounts: a plain repo, nothing saved.
	s, _, _, ra = newInitService(t)
	if info, err := s.Init(dir, "", ""); err != nil || info.Path != dir || len(ra.saved) != 0 {
		t.Fatalf("none: %+v %v %v", info, err, ra.saved)
	}
}

func TestInitAsksBeforeCreatingAnything(t *testing.T) {
	s, fi, _, _ := newInitService(t, account.Account{ID: "work", Host: "github.com"}, account.Account{ID: "me", Host: "github.com"})
	if _, err := s.Init(abs(t, "/src/new"), "", ""); !errors.Is(err, ErrChooseAccount) || len(fi.made) != 0 {
		t.Fatalf("err = %v, made %v", err, fi.made)
	}
	if _, err := s.Init(abs(t, "/src/new"), "ghost", ""); err == nil || !strings.Contains(err.Error(), "no account") || len(fi.made) != 0 {
		t.Fatalf("unknown account: %v %v", err, fi.made)
	}
}

func TestInitRefusals(t *testing.T) {
	work := account.Account{ID: "work", Host: "github.com"}
	dir := abs(t, "/src/exists")
	s, fi, _, _ := newInitService(t, work)
	fi.g.repos[dir] = filepath.Join(dir, ".git")
	// An existing folder is only seen as a repo when it exists on disk, so
	// use a real temp folder for that case.
	real := t.TempDir()
	fi.g.repos[real] = filepath.Join(real, ".git")
	if _, err := s.Init(real, "", ""); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("already a repo: %v", err)
	}
	for _, bad := range []string{"a b", "-x", "x..y", "x.lock", "a~b", ""} {
		if bad == "" {
			continue
		}
		if _, err := s.Init(abs(t, "/src/n"), "work", bad); err == nil {
			t.Errorf("branch %q must be refused", bad)
		}
	}
	fi.version = "git version 2.20.0"
	if _, err := s.Init(abs(t, "/src/n"), "work", ""); err == nil || !strings.Contains(err.Error(), "older than") {
		t.Errorf("old git: %v", err)
	}
	fi.version = ""
	if g := s.GitInfo(); g.Installed || g.OK || g.Why == "" {
		t.Errorf("no git: %+v", g)
	}
	if _, err := s.Init(abs(t, "/src/n"), "work", ""); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("no git: %v", err)
	}
	s.Initializer = nil
	if _, err := s.Init(abs(t, "/src/n"), "work", ""); err == nil {
		t.Error("no initializer")
	}
}
