package application

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeStore struct {
	account.Store
	c account.Config
}

func (f *fakeStore) Load() *account.Config { c := f.c; return &c }

// fakeRepos resolves every repo to one account.
type fakeRepos struct {
	repo.Reader
	acc *account.Account
}

func (f *fakeRepos) AccountFor(*account.Config, string) (*account.Account, string) {
	return f.acc, ""
}
func (f *fakeRepos) InfoFor(_ *account.Config, gitDir string) repo.Info {
	return repo.Info{GitDir: gitDir}
}
func (f *fakeRepos) GitDirFor(string) string         { return "" }
func (f *fakeRepos) WorktreeOf(gitDir string) string { return strings.TrimSuffix(gitDir, "/.git") }

type fakeLog struct {
	commits  []history.Commit
	graph    string
	graphErr error
	local    []string
	emails   map[string]string
	headName string
	rewrote  []string // "worktree name email first"
}

func (f *fakeLog) Commits(string, int) ([]history.Commit, error) { return f.commits, nil }
func (f *fakeLog) Unpushed(string) map[string]bool               { return map[string]bool{} }
func (f *fakeLog) Graph(string, int, bool) (string, error)       { return f.graph, f.graphErr }
func (f *fakeLog) LocalCommits(string) ([]string, error)         { return f.local, nil }
func (f *fakeLog) AuthorEmail(_, hash string) string             { return f.emails[hash] }
func (f *fakeLog) HeadAuthorName(string) string                  { return f.headName }
func (f *fakeLog) RewriteAuthor(wt, name, email, first string) error {
	f.rewrote = append(f.rewrote, strings.Join([]string{wt, name, email, first}, " "))
	return nil
}

func newService() (*Service, *fakeRepos, *fakeLog) {
	work := account.Account{ID: "work", Email: "w@x.com"}
	st := &fakeStore{c: account.Config{Accounts: []account.Account{work, {ID: "me", Email: "me@x.com"}}}}
	r := &fakeRepos{acc: &work}
	l := &fakeLog{emails: map[string]string{}}
	return &Service{Accounts: st, Repos: r, Locator: r, Log: l}, r, l
}

func TestLines(t *testing.T) {
	s, _, l := newService()
	l.commits = []history.Commit{{Hash: "aaaa", Short: "aaaa", Email: "w@x.com", Subject: "first"}}
	l.graph = "* \x1faaaa"
	lines, hist, err := s.Lines("/r/.git", 10, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "[work]") || !strings.Contains(lines[0], "first") || hist.Repo.GitDir != "/r/.git" {
		t.Errorf("lines = %q", lines)
	}

	l.graphErr = history.ErrNoCommits
	if lines, _, err := s.Lines("/r/.git", 10, false, false); err != nil || len(lines) != 1 || lines[0] != "This repo has no commits yet." {
		t.Errorf("empty repo: lines=%q err=%v", lines, err)
	}
	l.graphErr = errors.New("git failed")
	if _, _, err := s.Lines("/r/.git", 10, false, false); err == nil {
		t.Error("a git error must be returned")
	}
}

func TestFixAuthor(t *testing.T) {
	s, r, l := newService()
	l.local = []string{"c1", "c2", "c3"}
	l.emails = map[string]string{"c1": "w@x.com", "c2": "me@x.com", "c3": "old@laptop"}
	l.headName = "Head Author"

	var out bytes.Buffer
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	if n, err := s.FixAuthor("/r/.git"); err != nil || n != 2 || len(l.rewrote) != 0 || !strings.Contains(out.String(), "2 not-pushed commit(s)") {
		t.Errorf("dry run: n=%d err=%v rewrote=%v out=%q", n, err, l.rewrote, out.String())
	}

	s.FS = &filesystem.FS{}
	if n, err := s.FixAuthor("/r/.git"); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(l.rewrote) != 1 || l.rewrote[0] != "/r Head Author w@x.com c1" {
		t.Errorf("rewrote = %q, want the head author's name (the account has none) from the oldest commit", l.rewrote)
	}

	l.rewrote = nil
	l.emails = map[string]string{"c1": "w@x.com", "c2": "W@X.com", "c3": "w@x.com"}
	if n, err := s.FixAuthor("/r/.git"); err != nil || n != 0 || len(l.rewrote) != 0 {
		t.Errorf("nothing to fix: n=%d err=%v rewrote=%v", n, err, l.rewrote)
	}

	r.acc = nil
	if _, err := s.FixAuthor("/r/.git"); err == nil || !strings.Contains(err.Error(), "gitswitch use") {
		t.Errorf("a repo without an account: err=%v", err)
	}
}
