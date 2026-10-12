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
	acc     *account.Account
	remotes []repo.Remote
}

func (f *fakeRepos) AccountFor(*account.Config, string) (*account.Account, string) {
	return f.acc, ""
}
func (f *fakeRepos) InfoFor(_ *account.Config, gitDir string) repo.Info {
	return repo.Info{GitDir: gitDir, Remotes: f.remotes}
}
func (f *fakeRepos) GitDirFor(string) string         { return "" }
func (f *fakeRepos) WorktreeOf(gitDir string) string { return strings.TrimSuffix(gitDir, "/.git") }

type fakeLog struct {
	commits  []history.Commit
	graph    string
	graphErr error
	local    []string
	span     []string // what Rewritten returns; nil: local
	emails   map[string]string
	headName string
	rewrote  []string // "worktree name email first wrong,emails"

	filter history.Filter // the last filter passed to Commits

	branch   string
	busy     string
	targets  map[string]history.Target // rev -> commit
	onBranch map[string]bool
	after    map[string][]string
	remote   map[string]bool // commits on a remote branch
	changed  []string        // "revert|drop|reset <hash>"
}

func (f *fakeLog) Resolve(_, rev string) (history.Target, error) {
	t, ok := f.targets[rev]
	if !ok {
		return t, errors.New("no commit " + rev)
	}
	return t, nil
}
func (f *fakeLog) Branch(string) string                   { return f.branch }
func (f *fakeLog) InProgress(string) string               { return f.busy }
func (f *fakeLog) OnBranch(_, hash string) bool           { return f.onBranch[hash] }
func (f *fakeLog) After(_, hash string) ([]string, error) { return f.after[hash], nil }
func (f *fakeLog) Revert(_ string, t history.Target) error {
	f.changed = append(f.changed, "revert "+t.Hash)
	return nil
}
func (f *fakeLog) Drop(_ string, t history.Target) error {
	f.changed = append(f.changed, "drop "+t.Hash)
	return nil
}

// Order knows the other branch: other <- other2.
func (f *fakeLog) Order(_ string, hashes []string) ([]string, error) {
	var out []string
	for _, h := range []string{"other", "other2"} {
		for _, x := range hashes {
			if x == h {
				out = append(out, h)
			}
		}
	}
	return out, nil
}
func (f *fakeLog) CherryPick(_ string, ts []history.Target) error {
	for _, t := range ts {
		f.changed = append(f.changed, "pick "+t.Hash)
	}
	return nil
}
func (f *fakeLog) Reset(_, hash string) error {
	f.changed = append(f.changed, "reset "+hash)
	return nil
}

func (f *fakeLog) Commits(_ string, _ int, fl history.Filter) ([]history.Commit, error) {
	f.filter = fl
	return f.commits, nil
}
func (f *fakeLog) Unpushed(string) map[string]bool {
	set := map[string]bool{}
	for _, h := range f.local {
		set[h] = true
	}
	return set
}
func (f *fakeLog) OnRemote(_, hash string) bool            { return f.remote[hash] }
func (f *fakeLog) Graph(string, int, bool) (string, error) { return f.graph, f.graphErr }
func (f *fakeLog) LocalCommits(string) ([]string, error)   { return f.local, nil }
func (f *fakeLog) AuthorEmail(_, hash string) string       { return f.emails[hash] }
func (f *fakeLog) HeadAuthorName(string) string            { return f.headName }
func (f *fakeLog) Rewritten(string, string) ([]string, error) {
	if f.span != nil {
		return f.span, nil
	}
	return f.local, nil
}
func (f *fakeLog) RewriteAuthor(wt, name, email, first string, wrong []string) error {
	f.rewrote = append(f.rewrote, strings.Join([]string{wt, name, email, first, strings.Join(wrong, ",")}, " "))
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
	lines, hist, err := s.Lines("/r/.git", 10, false, false, history.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "[work]") || !strings.Contains(lines[0], "first") || hist.Repo.GitDir != "/r/.git" {
		t.Errorf("lines = %q", lines)
	}

	l.graphErr = history.ErrNoCommits
	if lines, _, err := s.Lines("/r/.git", 10, false, false, history.Filter{}); err != nil || len(lines) != 1 || lines[0] != "This repo has no commits yet." {
		t.Errorf("empty repo: lines=%q err=%v", lines, err)
	}
	l.graphErr = errors.New("git failed")
	if _, _, err := s.Lines("/r/.git", 10, false, false, history.Filter{}); err == nil {
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
	if len(l.rewrote) != 1 || l.rewrote[0] != "/r Head Author w@x.com c1 me@x.com,old@laptop" {
		t.Errorf("rewrote = %q, want the head author's name (the account has none) from the oldest commit", l.rewrote)
	}

	l.rewrote = nil
	l.emails = map[string]string{"c1": "w@x.com", "c2": "W@X.com", "c3": "w@x.com"}
	if n, err := s.FixAuthor("/r/.git"); err != nil || n != 0 || len(l.rewrote) != 0 {
		t.Errorf("nothing to fix: n=%d err=%v rewrote=%v", n, err, l.rewrote)
	}

	// A merge brought a pushed commit into the range the rebase replays.
	l.rewrote = nil
	l.emails = map[string]string{"c1": "me@x.com", "c2": "w@x.com", "c3": "w@x.com"}
	l.span = []string{"c1", "teammate", "c2", "c3"}
	if _, err := s.FixAuthor("/r/.git"); err == nil || !strings.Contains(err.Error(), "already pushed") || len(l.rewrote) != 0 {
		t.Errorf("pushed commit in range: err=%v rewrote=%v", err, l.rewrote)
	}
	l.span = nil

	r.acc = nil
	if _, err := s.FixAuthor("/r/.git"); err == nil || !strings.Contains(err.Error(), "gitswitch use") {
		t.Errorf("a repo without an account: err=%v", err)
	}
}

// branchLog is a branch root <- pushed <- mine1 <- mine2 (HEAD), where
// mine1 and mine2 are not pushed, plus a merge commit and a commit on
// another branch.
func branchLog(s *Service, l *fakeLog) {
	t := func(h string, parents ...string) history.Target {
		return history.Target{Hash: h, Short: h, Subject: "subject " + h, Parents: parents}
	}
	l.branch = "main"
	l.targets = map[string]history.Target{
		"root": t("root"), "pushed": t("pushed", "root"), "mine1": t("mine1", "pushed"),
		"mine2": t("mine2", "mine1"), "merge": t("merge", "pushed", "side"), "other": t("other", "root"),
		"other2": t("other2", "other"), "sidemerge": t("sidemerge", "other", "x"), "tagonly": t("tagonly", "root"),
	}
	l.onBranch = map[string]bool{"root": true, "pushed": true, "mine1": true, "mine2": true, "merge": true}
	l.local = []string{"mine1", "mine2"}
	l.remote = map[string]bool{"root": true, "pushed": true, "other": true, "other2": true}
	l.after = map[string][]string{"root": {"mine2", "mine1", "pushed"}, "pushed": {"mine2", "mine1"}, "mine1": {"mine2"}, "mine2": nil}
	s.FS = &filesystem.FS{}
}

func TestCommitActionsCheckTheRepoFirst(t *testing.T) {
	s, _, l := newService()
	branchLog(s, l)
	actions := map[string]func(rev string) error{
		"revert":   func(rev string) error { _, err := s.Revert("/r/.git", rev); return err },
		"drop":     func(rev string) error { _, err := s.Drop("/r/.git", rev); return err },
		"rollback": func(rev string) error { _, _, err := s.Rollback("/r/.git", rev); return err },
	}
	for name, do := range actions {
		for _, c := range []struct {
			setup func()
			rev   string
			want  string
		}{
			{func() {}, "", "not a commit"},
			{func() {}, "--hard", "not a commit"},
			{func() {}, "nope", "no commit"},
			{func() {}, "other", "not on the current branch"},
			{func() { l.busy = "rebase" }, "mine2", "middle of a rebase"},
			{func() { l.branch = "" }, "mine2", "detached"},
		} {
			branchLog(s, l)
			l.busy = ""
			c.setup()
			if err := do(c.rev); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s(%q) err = %v, want %q", name, c.rev, err, c.want)
			}
		}
	}
	if len(l.changed) != 0 {
		t.Errorf("nothing may change when refused: %v", l.changed)
	}
}

func TestRevert(t *testing.T) {
	s, _, l := newService()
	branchLog(s, l)
	if got, err := s.Revert("/r/.git", "pushed"); err != nil || got.Hash != "pushed" {
		t.Fatalf("a pushed commit can be reverted: %+v %v", got, err)
	}
	if _, err := s.Revert("/r/.git", "merge"); err != nil {
		t.Fatalf("a merge can be reverted: %v", err)
	}
	if strings.Join(l.changed, ",") != "revert pushed,revert merge" {
		t.Errorf("changed = %v", l.changed)
	}
}

func TestDropOnlyNotPushedCommits(t *testing.T) {
	s, _, l := newService()
	branchLog(s, l)
	for rev, want := range map[string]string{"pushed": "already pushed", "merge": "merge commit", "root": "first commit"} {
		if _, err := s.Drop("/r/.git", rev); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Drop(%s) err = %v, want %q", rev, err, want)
		}
	}
	if _, err := s.Drop("/r/.git", "mine1"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	if _, err := s.Drop("/r/.git", "mine2"); err != nil || !strings.Contains(out.String(), "drop mine2") {
		t.Errorf("dry run: err=%v out=%q", err, out.String())
	}
	if strings.Join(l.changed, ",") != "drop mine1" {
		t.Errorf("changed = %v", l.changed)
	}
}

func TestRollbackOnlyOverNotPushedCommits(t *testing.T) {
	s, _, l := newService()
	branchLog(s, l)
	if _, _, err := s.Rollback("/r/.git", "root"); err == nil || !strings.Contains(err.Error(), "already pushed") {
		t.Errorf("rolling back over a pushed commit: err=%v", err)
	}
	if _, _, err := s.Rollback("/r/.git", "mine2"); err == nil || !strings.Contains(err.Error(), "latest commit") {
		t.Errorf("rolling back to HEAD: err=%v", err)
	}
	n, target, err := s.Rollback("/r/.git", "pushed")
	if err != nil || n != 2 || target.Hash != "pushed" {
		t.Fatalf("n=%d target=%+v err=%v", n, target, err)
	}
	if strings.Join(l.changed, ",") != "reset pushed" {
		t.Errorf("changed = %v", l.changed)
	}
}

func TestRowsKeepTheCommitOfEachLine(t *testing.T) {
	s, _, l := newService()
	l.commits = []history.Commit{{Hash: "aaaa", Short: "aaaa"}, {Hash: "bbbb", Short: "bbbb"}}
	l.graph = "* \x1faaaa\n|\n* \x1fbbbb"
	rows, _, err := s.Rows("/r/.git", 10, false, false, history.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Hash != "aaaa" || rows[1].Hash != "" || rows[2].Hash != "bbbb" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestCherryPick(t *testing.T) {
	s, _, l := newService()
	branchLog(s, l)
	for _, c := range []struct {
		revs []string
		want string
	}{
		{nil, "choose one or more"},
		{[]string{"other", "mine1"}, "already on the current branch"},
		{[]string{"sidemerge"}, "merge commit"},
		{[]string{"nope"}, "no commit"},
	} {
		if _, err := s.CherryPick("/r/.git", c.revs); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("CherryPick(%v) err = %v, want %q", c.revs, err, c.want)
		}
	}
	if len(l.changed) != 0 {
		t.Fatalf("nothing may change when refused: %v", l.changed)
	}
	// newest first, as picked from the graph, with a duplicate
	ts, err := s.CherryPick("/r/.git", []string{"other2", "other", "other2"})
	if err != nil || len(ts) != 2 {
		t.Fatalf("ts=%v err=%v", ts, err)
	}
	if got := strings.Join(l.changed, ","); got != "pick other,pick other2" {
		t.Errorf("picked %s, want oldest first without duplicates", got)
	}
	l.branch = ""
	if _, err := s.CherryPick("/r/.git", []string{"other"}); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Errorf("detached HEAD: err=%v", err)
	}
}

func TestWebURL(t *testing.T) {
	s, r, l := newService()
	branchLog(s, l)
	if _, err := s.WebURL("/r/.git", "pushed"); err == nil || !strings.Contains(err.Error(), "no remote") {
		t.Errorf("no remote: err=%v", err)
	}
	r.remotes = []repo.Remote{{Name: "backup", URL: "git@gitlab.com:me/api.git"}, {Name: "origin", URL: "git@github.com:acme/api.git"}}
	if u, err := s.WebURL("/r/.git", ""); err != nil || u != "https://github.com/acme/api" {
		t.Errorf("repo page: %q %v", u, err)
	}
	if u, err := s.WebURL("/r/.git", "pushed"); err != nil || u != "https://github.com/acme/api/commit/pushed" {
		t.Errorf("commit page: %q %v", u, err)
	}
	if _, err := s.WebURL("/r/.git", "mine1"); err == nil || !strings.Contains(err.Error(), "not pushed") {
		t.Errorf("not-pushed commit: err=%v", err)
	}
	if _, err := s.WebURL("/r/.git", "tagonly"); err == nil || !strings.Contains(err.Error(), "not pushed") {
		t.Errorf("a commit on no branch and no remote (only a tag) must be refused: err=%v", err)
	}
}

func TestFilter(t *testing.T) {
	s, _, l := newService()
	l.commits = []history.Commit{
		{Hash: "aaaa", Short: "aaaa", Email: "w@x.com", Author: "Wanda", Subject: "work", Parents: []string{"bbbb"}},
		{Hash: "bbbb", Short: "bbbb", Email: "me@x.com", Author: "Me", Subject: "mine"},
	}
	l.graph = "* \x1faaaa\n* \x1fbbbb"

	f := history.Filter{Account: "me", Since: "2024-01-01"}
	rows, h, err := s.Rows("/r/.git", 10, false, false, f)
	if err != nil {
		t.Fatal(err)
	}
	if l.filter.Author != "me@x.com" || !strings.HasPrefix(l.filter.Since, "@") {
		t.Errorf("git filter = %+v, want the account's email as author and an exact time", l.filter)
	}
	if len(h.Commits) != 1 || h.Commits[0].Hash != "bbbb" || h.Lanes != 1 || len(h.Commits[0].Edges) != 0 {
		t.Errorf("filtered history = %+v", h)
	}
	if len(rows) != 1 || rows[0].Hash != "bbbb" || !strings.Contains(rows[0].Text, "[me]") || strings.Contains(rows[0].Text, "*") {
		t.Errorf("a filtered history is a flat list: %+v", rows)
	}

	// A given author wins over the account's email; the account still filters.
	if _, h, _ := s.Rows("/r/.git", 10, false, false, history.Filter{Author: "Wanda", Account: "me"}); l.filter.Author != "Wanda" || len(h.Commits) != 1 {
		t.Errorf("filter=%+v commits=%d", l.filter, len(h.Commits))
	}
	if rows, _, _ := s.Rows("/r/.git", 10, false, false, history.Filter{Account: "nobody"}); len(rows) != 1 || rows[0].Hash != "" {
		t.Errorf("no match: %+v", rows)
	}
	if _, h, _ := s.Rows("/r/.git", 10, false, false, history.Filter{}); h.Lanes == 1 && len(h.Commits) == 2 && len(h.Commits[0].Edges) == 0 {
		t.Errorf("without a filter the graph is laid out: %+v", h.Commits)
	}
}

// More tells the front ends to offer "load more": git returned as many
// commits as were asked for, so there may be older ones.
func TestBuildMore(t *testing.T) {
	s, _, l := newService()
	l.commits = []history.Commit{{Hash: "bbbb", Parents: []string{"aaaa"}}, {Hash: "aaaa"}}
	for limit, want := range map[int]bool{2: true, 3: false} {
		h, err := s.Build("/r/.git", limit, history.Filter{})
		if err != nil || h.More != want {
			t.Errorf("Build(limit %d).More = %v, %v; want %v", limit, h.More, err, want)
		}
	}
}
