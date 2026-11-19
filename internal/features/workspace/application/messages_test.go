package application

import (
	"errors"
	"strings"
	"testing"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeMessages struct {
	recent       []workspace.Recent
	authors      []workspace.Person
	identity     workspace.Person
	draft        string
	global, repo string
	askedEmail   string
	err          error
}

func (f *fakeMessages) Recent(_, email string, max int) ([]workspace.Recent, error) {
	f.askedEmail = email
	return f.recent, f.err
}
func (f *fakeMessages) Authors(string, int) ([]workspace.Person, error) { return f.authors, nil }
func (f *fakeMessages) Identity(string) workspace.Person                { return f.identity }
func (f *fakeMessages) Draft(string) string                             { return f.draft }
func (f *fakeMessages) SaveDraft(_, m string) error                     { f.draft = m; return nil }
func (f *fakeMessages) SignoffValues(gitDir string) (string, string) {
	if gitDir == "" {
		return f.global, ""
	}
	return f.global, f.repo
}
func (f *fakeMessages) SaveSignoff(gitDir, v string) error {
	if gitDir == "" {
		f.global = v
	} else {
		f.repo = v
	}
	return nil
}

type accountsStore struct {
	account.Store
	cfg *account.Config
}

func (s accountsStore) Load() *account.Config { return s.cfg }

func newMessagesService() (*Service, *fakeGit, *fakeRepos, *fakeMessages) {
	s, g, r, _ := newService()
	r.acc.Name = "Me Work"
	m := &fakeMessages{identity: workspace.Person{Name: "Global Me", Email: "global@me.example"}}
	s.Messages = m
	return s, g, r, m
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

func TestCommitWithTrailers(t *testing.T) {
	s, g, _, m := newMessagesService()
	got, err := s.CommitWith("/r/.git", "Fix login\n\nBody.", false, workspace.CommitOptions{Signoff: yes(), CoAuthors: []string{"Ada <ada@x.example>", ""}})
	want := "commit Fix login\n\nBody.\n\nCo-authored-by: Ada <ada@x.example>\nSigned-off-by: Me Work <Me@Work.example>"
	if err != nil || strings.Join(g.ran, ",") != want {
		t.Fatalf("CommitWith = %+v %v %q", got, err, g.ran)
	}

	// The setting signs off a plain Commit (the terminal app's); an
	// explicit no wins.
	s, g, _, m = newMessagesService()
	m.global = "true"
	if _, err := s.Commit("/r/.git", "Fix", false); err != nil || !strings.HasSuffix(strings.Join(g.ran, ","), "\n\nSigned-off-by: Me Work <Me@Work.example>") {
		t.Fatalf("setting: %v %q", err, g.ran)
	}
	s, g, _, m = newMessagesService()
	m.global, m.repo = "true", "false"
	if _, err := s.Commit("/r/.git", "Fix", false); err != nil || strings.Join(g.ran, ",") != "commit Fix" {
		t.Fatalf("repo off: %v %q", err, g.ran)
	}
	s, g, _, m = newMessagesService()
	m.global = "true"
	if _, err := s.CommitWith("/r/.git", "Fix", false, workspace.CommitOptions{Signoff: no()}); err != nil || strings.Join(g.ran, ",") != "commit Fix" {
		t.Fatalf("override off: %v %q", err, g.ran)
	}

	// No account and no repo identity: git's own is signed with.
	s, g, r, _ := newMessagesService()
	r.acc, r.email = nil, ""
	if _, err := s.CommitWith("/r/.git", "Fix", false, workspace.CommitOptions{Signoff: yes()}); err != nil ||
		!strings.HasSuffix(strings.Join(g.ran, ","), "Signed-off-by: Global Me <global@me.example>") {
		t.Fatalf("git identity: %v %q", err, g.ran)
	}
}

func TestCommitDraft(t *testing.T) {
	s, g, _, m := newMessagesService()
	m.draft = "Old draft"
	if _, err := s.Commit("/r/.git", "Fix", false); err != nil || m.draft != "" {
		t.Fatalf("a commit clears the draft: %v %q", err, m.draft)
	}
	for name, c := range map[string]struct {
		edit func(*fakeGit)
		o    workspace.CommitOptions
		want string
	}{
		"nothing staged": {func(g *fakeGit) { g.head.Changes = refs.Changes{Changed: 1} }, workspace.CommitOptions{}, "nothing is staged"},
		"bad co-author":  {func(*fakeGit) {}, workspace.CommitOptions{CoAuthors: []string{"Ada"}}, "Name <email>"},
		"myself":         {func(*fakeGit) {}, workspace.CommitOptions{CoAuthors: []string{"Me <me@work.example>"}}, "is you"},
	} {
		s, g, _, m = newMessagesService()
		c.edit(g)
		if _, err := s.CommitWith("/r/.git", " Keep me \n", false, c.o); err == nil || !strings.Contains(err.Error(), c.want) || len(g.ran) != 0 || m.draft != "Keep me" {
			t.Errorf("%s: %v %q draft %q", name, err, g.ran, m.draft)
		}
	}
	// An empty message keeps the draft there was.
	s, _, _, m = newMessagesService()
	m.draft = "Earlier"
	if _, err := s.Commit("/r/.git", " ", false); err == nil || m.draft != "Earlier" {
		t.Errorf("empty: %v %q", err, m.draft)
	}
	// A dry run keeps nothing.
	s, _, _, m = newMessagesService()
	s.FS = &filesystem.FS{DryRun: true, Out: &strings.Builder{}}
	m.draft = "Earlier"
	if _, err := s.Commit("/r/.git", "Fix", false); err != nil || m.draft != "Earlier" {
		t.Errorf("dry run: %v %q", err, m.draft)
	}
	_ = g
}

func TestAmendWithTrailers(t *testing.T) {
	// Kept message, sign-off asked for: added to the last commit's.
	s, g, _, _ := newMessagesService()
	g.head.Changes = refs.Changes{}
	if _, err := s.AmendWith("/r/.git", "", false, workspace.CommitOptions{Signoff: yes()}); err != nil ||
		strings.Join(g.ran, ",") != "amend Old\n\nbody\n\nSigned-off-by: Me Work <Me@Work.example>" {
		t.Fatalf("kept message: %v %q", err, g.ran)
	}
	// The setting leaves a kept message alone.
	s, g, _, m := newMessagesService()
	m.global = "on"
	if _, err := s.Amend("/r/.git", "", false); err != nil || strings.Join(g.ran, ",") != "amend " {
		t.Fatalf("setting, kept message: %v %q", err, g.ran)
	}
	// ...and signs a new one.
	s, g, _, m = newMessagesService()
	m.global = "on"
	if _, err := s.Amend("/r/.git", "New", false); err != nil || strings.Join(g.ran, ",") != "amend New\n\nSigned-off-by: Me Work <Me@Work.example>" {
		t.Fatalf("setting, new message: %v %q", err, g.ran)
	}
	// Already signed off, nothing staged: nothing to amend.
	s, g, _, _ = newMessagesService()
	g.head.Changes = refs.Changes{}
	g.last.Message = "Old\n\nSigned-off-by: Me Work <me@work.example>"
	if _, err := s.AmendWith("/r/.git", "", false, workspace.CommitOptions{Signoff: yes()}); err == nil || !strings.Contains(err.Error(), "nothing to amend") {
		t.Fatalf("already signed: %v %q", err, g.ran)
	}
	s, g, _, _ = newMessagesService()
	if _, err := s.AmendWith("/r/.git", "x", false, workspace.CommitOptions{CoAuthors: []string{"bad"}}); err == nil || len(g.ran) != 0 {
		t.Fatalf("bad co-author: %v %q", err, g.ran)
	}
}

func TestCommitInfo(t *testing.T) {
	s, _, _, m := newMessagesService()
	s.Accounts = accountsStore{cfg: &account.Config{Accounts: []account.Account{{ID: "work", Email: "me@work.example"}, {ID: "home", Email: "me@home.example"}}}}
	at := func(h int) time.Time { return time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC) }
	m.recent = []workspace.Recent{{Message: "B", Time: at(2)}, {Message: "A\n\nSigned-off-by: x <x@y>", Time: at(3)}, {Message: "A", Time: at(1)}}
	m.authors = []workspace.Person{{Name: "Ada", Email: "ada@x"}, {Name: "Me", Email: "Me@Work.example"}, {Name: "Home me", Email: "me@home.example"}, {Name: "Ada", Email: "ada@x"}, {Name: "Bob", Email: "bob@x"}}
	m.draft, m.global, m.repo = "Draft", "false", "true"
	info, err := s.CommitInfo("/r/.git", 0)
	if err != nil || m.askedEmail != "Me@Work.example" || len(info.Recent) != 2 || info.Recent[0].Message != "A" || info.Recent[1].Message != "B" ||
		info.Draft != "Draft" || !info.Signoff || info.SignoffSetting.Repo != "on" || info.SignoffSetting.Global ||
		len(info.CoAuthors) != 2 || info.CoAuthors[0] != (workspace.CoAuthor{Name: "Ada", Email: "ada@x", Commits: 2}) || info.CoAuthors[1].Email != "bob@x" ||
		info.Identity != (workspace.Person{Name: "Me Work", Email: "Me@Work.example"}) {
		t.Fatalf("CommitInfo = %+v, %v", info, err)
	}
	if info, _ := s.CommitInfo("/r/.git", 1); len(info.Recent) != 1 {
		t.Errorf("n=1: %+v", info.Recent)
	}
	m.err = errors.New("boom")
	if _, err := s.CommitInfo("/r/.git", 0); err == nil {
		t.Error("error swallowed")
	}

	// Without the port: empty lists, not nil.
	s.Messages = nil
	if info, err := s.CommitInfo("/r/.git", 0); err != nil || info.Recent == nil || info.CoAuthors == nil {
		t.Errorf("no port: %+v %v", info, err)
	}
}

func TestSignoffSettingSave(t *testing.T) {
	s, _, _, m := newMessagesService()
	if err := s.SetSignoffSetting("", "on"); err != nil || m.global != "true" || !s.SignoffSetting("").Mode {
		t.Fatalf("global on: %v %+v", err, m)
	}
	if err := s.SetSignoffSetting("/r/.git", "off"); err != nil || m.repo != "false" || s.SignoffSetting("/r/.git").Describe() != "off (this repo)" {
		t.Fatalf("repo off: %v %+v", err, m)
	}
	if err := s.SetSignoffSetting("/r/.git", ""); err != nil || m.repo != "" || s.SignoffSetting("/r/.git").Describe() != "on (every repo)" {
		t.Fatalf("repo follows: %v %+v", err, m)
	}
	for _, c := range [][2]string{{"", ""}, {"", "maybe"}, {"/r/.git", "maybe"}} {
		if err := s.SetSignoffSetting(c[0], c[1]); err == nil || !strings.Contains(err.Error(), "on or off") {
			t.Errorf("SetSignoffSetting(%q, %q) = %v", c[0], c[1], err)
		}
	}
}
