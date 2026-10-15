package application

import (
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeGit struct {
	head refs.Head
	ran  []string
}

func (f *fakeGit) Head(string) (refs.Head, error) { return f.head, nil }
func (f *fakeGit) Commit(_, msg string, all bool) error {
	f.ran = append(f.ran, map[bool]string{true: "commit --all ", false: "commit "}[all]+msg)
	f.head.Hash, f.head.Short, f.head.Subject, f.head.Changes = "def", "def", msg, refs.Changes{}
	return nil
}

type fakeStore struct{ account.Store }

func (fakeStore) Load() *account.Config { return &account.Config{} }

type fakeRepos struct {
	repo.Reader
	acc   *account.Account
	email string
}

func (f *fakeRepos) AccountFor(*account.Config, string) (*account.Account, string) { return f.acc, "" }
func (f *fakeRepos) InfoFor(_ *account.Config, gitDir string) repo.Info {
	return repo.Info{GitDir: gitDir, Email: f.email}
}
func (f *fakeRepos) GitDirFor(string) string         { return "" }
func (f *fakeRepos) WorktreeOf(gitDir string) string { return strings.TrimSuffix(gitDir, "/.git") }

type fakeRecorder struct{ done []string }

func (f *fakeRecorder) Before(string) undo.Mark { return undo.Mark{Head: "abc"} }
func (f *fakeRecorder) Done(_ string, m undo.Mark, a undo.Action, label string) {
	f.done = append(f.done, string(a)+" "+m.Head+": "+label)
}
func (f *fakeRecorder) Pruned(string, undo.Mark, []undo.Deleted, string) {}
func (f *fakeRecorder) Updated(string, undo.Mark, string, string)        {}

func newService() (*Service, *fakeGit, *fakeRepos, *fakeRecorder) {
	g := &fakeGit{head: refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{Staged: 1, Changed: 2, New: 1}}}
	r := &fakeRepos{acc: &account.Account{ID: "work", Email: "me@work.example"}, email: "Me@Work.example"}
	rec := &fakeRecorder{}
	return &Service{Git: g, Accounts: fakeStore{}, Repos: r, Locator: r, FS: &filesystem.FS{}, Undo: rec}, g, r, rec
}

func TestCommit(t *testing.T) {
	s, g, _, rec := newService()
	got, err := s.Commit("/r/.git", "  Fix login\n", false)
	if err != nil || got.Short != "def" || got.Files != 1 || got.Account != "work" || got.Branch != "main" || got.All ||
		strings.Join(g.ran, ",") != "commit Fix login" || strings.Join(rec.done, "|") != "commit abc: Committed def Fix login" {
		t.Fatalf("staged: %+v %v %v %v", got, err, g.ran, rec.done)
	}

	s, g, _, _ = newService()
	if got, err := s.Commit("/r/.git", "All of it", true); err != nil || !got.All || got.Files != 3 || strings.Join(g.ran, ",") != "commit --all All of it" {
		t.Fatalf("all: %+v %v %v", got, err, g.ran)
	}
}

func TestCommitRefused(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*fakeGit, *fakeRepos)
		msg  string
		all  bool
		want string
	}{
		"no message":     {func(*fakeGit, *fakeRepos) {}, " \n", false, "write a commit message"},
		"mid-rebase":     {func(g *fakeGit, _ *fakeRepos) { g.head.Busy = "rebase" }, "m", false, "rebase"},
		"conflicts":      {func(g *fakeGit, _ *fakeRepos) { g.head.Changes.Conflicts = 1 }, "m", true, "still have conflicts"},
		"nothing":        {func(g *fakeGit, _ *fakeRepos) { g.head.Changes = refs.Changes{} }, "m", true, "nothing to commit"},
		"nothing staged": {func(g *fakeGit, _ *fakeRepos) { g.head.Changes.Staged = 0 }, "m", false, "--all"},
		"wrong email":    {func(_ *fakeGit, r *fakeRepos) { r.email = "me@home.example" }, "m", false, "gitswitch use work --once"},
	} {
		s, g, r, rec := newService()
		c.edit(g, r)
		if _, err := s.Commit("/r/.git", c.msg, c.all); err == nil || !strings.Contains(err.Error(), c.want) || len(g.ran) != 0 || len(rec.done) != 0 {
			t.Errorf("%s: %v %v, want %q", name, err, g.ran, c.want)
		}
	}
	// A repo without an account commits with whatever git has.
	s, g, r, _ := newService()
	r.acc = nil
	if got, err := s.Commit("/r/.git", "m", false); err != nil || got.Account != "" || len(g.ran) != 1 {
		t.Errorf("no account: %+v %v", got, err)
	}
}
