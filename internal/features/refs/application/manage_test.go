package application

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

func (f *fakeRefs) Unique(_, b string) (int, error) { return f.unique[b], nil }
func (f *fakeRefs) RenameBranch(_, from, to string) error {
	f.ran = append(f.ran, "rename "+from+" "+to)
	return nil
}
func (f *fakeRefs) SetUpstream(_, b, up string) error {
	f.ran = append(f.ran, "upstream "+b+" "+up)
	return nil
}
func (f *fakeRefs) CheckTagName(n string) error { return f.CheckName(n) }
func (f *fakeRefs) CreateTag(_, n, c, m string) error {
	f.ran = append(f.ran, fmt.Sprintf("tag %s %s %q", n, c, m))
	return nil
}
func (f *fakeRefs) DeleteTag(_, n string) error { f.ran = append(f.ran, "tag -d "+n); return nil }

func (f *fakeRecorder) Renamed(_ string, m undo.Mark, from, to, hash, label string) {
	f.done = append(f.done, "rename "+m.Branch+": "+from+"→"+to+"@"+hash+" "+label)
}

func TestDeleteBranch(t *testing.T) {
	s, r := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	r.target = [2]string{"origin", "refs/heads/fix"}
	r.branches = []refs.Branch{
		{Name: "main", Current: true, Hash: "m"}, {Name: "fix", Hash: "f", Upstream: "origin/fix"},
		{Name: "wip", Hash: "w"}, {Name: "tree", Hash: "t", Elsewhere: true}, {Name: "origin/fix", Remote: true, Hash: "f"},
	}
	r.unique = map[string]int{"wip": 2}
	for name, want := range map[string]string{
		"": "not a branch", "-D": "not a branch", "nope": "no local branch", "origin/fix": "no local branch",
		"main": "is the current branch", "tree": "another working folder",
		"wip": "wip has 2 commits that are on no other branch or remote: once the branch is deleted, only Undo can bring them back",
	} {
		if _, err := s.DeleteBranch("/r/.git", name, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("DeleteBranch(%q) = %v, want %q", name, err, want)
		}
	}
	var u *refs.Unmerged
	if _, err := s.DeleteBranch("/r/.git", "wip", false); !errors.As(err, &u) || u.Commits != 2 {
		t.Errorf("not a *refs.Unmerged: %v", err)
	}
	if len(r.ran) != 0 || len(rec.done) != 0 {
		t.Fatalf("refused, yet ran %v recorded %v", r.ran, rec.done)
	}

	if d, err := s.DeleteBranch("/r/.git", "fix", false); err != nil || d.Hash != "f" || d.Only != 0 ||
		strings.Join(r.ran, ",") != "delete fix f" || strings.Join(rec.done, "|") != "prune main: Deleted the branch fix fix@f<origin refs/heads/fix" {
		t.Fatalf("delete fix: %+v %v %v %v", d, err, r.ran, rec.done)
	}
	r.ran, rec.done = nil, nil
	if d, err := s.DeleteBranch("/r/.git", "wip", true); err != nil || d.Only != 2 || strings.Join(r.ran, ",") != "delete wip w" || len(rec.done) != 1 {
		t.Fatalf("forced: %+v %v %v %v", d, err, r.ran, rec.done)
	}

	r.ran = nil
	var out bytes.Buffer
	s.FS = &filesystem.FS{DryRun: true, Out: &out}
	if _, err := s.DeleteBranch("/r/.git", "wip", true); err != nil || len(r.ran) != 0 || !strings.Contains(out.String(), "only undo brings them back") {
		t.Fatalf("dry run: %v %v %q", err, r.ran, out.String())
	}
}

func TestRenameBranch(t *testing.T) {
	s, r := newService()
	rec := &fakeRecorder{}
	s.Undo = rec
	r.branches = []refs.Branch{{Name: "main", Current: true, Hash: "m", Upstream: "origin/main"}, {Name: "fix", Hash: "f"}, {Name: "origin/main", Remote: true}}
	for _, c := range []struct{ from, to, want string }{
		{"nope", "x", "no local branch"}, {"fix", "fix", "already called that"}, {"fix", "main", "already a branch main"},
		{"fix", "bad name", "can't be a branch name"}, {"fix", "-x", "can't be a branch name"}, {"fix", "", "can't be a branch name"},
	} {
		if _, err := s.RenameBranch("/r/.git", c.from, c.to); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("RenameBranch(%q, %q) = %v, want %q", c.from, c.to, err, c.want)
		}
	}
	if len(r.ran) != 0 {
		t.Fatalf("refused, yet ran %v", r.ran)
	}
	// "" is the current branch; its upstream stays.
	if n, err := s.RenameBranch("/r/.git", "", "trunk"); err != nil || n.From != "main" || n.To != "trunk" || n.Upstream != "origin/main" ||
		strings.Join(r.ran, ",") != "rename main trunk" || strings.Join(rec.done, "|") != "rename main: main→trunk@m Renamed the branch main to trunk" {
		t.Fatalf("rename current: %+v %v %v %v", n, err, r.ran, rec.done)
	}
	r.head = refs.Head{Hash: "abc"}
	if _, err := s.RenameBranch("/r/.git", "", "x"); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Errorf("detached: %v", err)
	}
}

func TestSetUpstream(t *testing.T) {
	s, r := newService()
	r.branches = append(r.branches, refs.Branch{Name: "upstream/main", Remote: true})
	for up, want := range map[string]string{"fix": "did you mean origin/fix", "origin/nope": "no remote branch \"origin/nope\"", "-x": "not a remote branch"} {
		if _, err := s.SetUpstream("/r/.git", "main", up); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("SetUpstream(%q) = %v, want %q", up, err, want)
		}
	}
	if u, err := s.SetUpstream("/r/.git", "", "upstream/main"); err != nil || u.Branch != "main" || u.Was != "origin/main" || u.Upstream != "upstream/main" ||
		strings.Join(r.ran, ",") != "upstream main upstream/main" {
		t.Fatalf("set: %+v %v %v", u, err, r.ran)
	}
	// Unsetting: only when one is set.
	r.ran = nil
	if _, err := s.SetUpstream("/r/.git", "fix", ""); err != nil || len(r.ran) != 0 {
		t.Fatalf("unset nothing: %v %v", err, r.ran)
	}
	r.target = [2]string{"origin", "refs/heads/main"}
	if _, err := s.SetUpstream("/r/.git", "main", ""); err != nil || strings.Join(r.ran, ",") != "upstream main " {
		t.Fatalf("unset: %v %v", err, r.ran)
	}
}

func TestTags(t *testing.T) {
	s, r := newService()
	r.tags = []string{"v1", "v2"}
	for _, c := range []struct{ name, rev, want string }{
		{"", "", "can't be a tag name"}, {"-f", "", "can't be a tag name"}, {"v 3", "", "can't be a tag name"},
		{"v1", "", "already a tag v1"}, {"v3", "nope", "no commit, branch or tag \"nope\""}, {"v3", "-x", "not a commit"},
	} {
		if _, err := s.NewTag("/r/.git", c.name, c.rev, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("NewTag(%q, %q) = %v, want %q", c.name, c.rev, err, c.want)
		}
	}
	if tg, err := s.NewTag("/r/.git", "v3", "", ""); err != nil || tg.Annotated || tg.Hash != "hash-of-HEAD" {
		t.Fatalf("light: %+v %v", tg, err)
	}
	if tg, err := s.NewTag("/r/.git", "v4", "fix", " Release 4 "); err != nil || !tg.Annotated {
		t.Fatalf("annotated: %+v %v", tg, err)
	}
	if got := strings.Join(r.ran, ","); got != `tag v3 hash-of-HEAD "",tag v4 hash-of-fix "Release 4"` {
		t.Fatalf("ran %v", got)
	}

	r.ran = nil
	if err := s.DeleteTag("/r/.git", "v9"); err == nil || !strings.Contains(err.Error(), "no tag \"v9\"") {
		t.Errorf("delete missing: %v", err)
	}
	if err := s.DeleteTag("/r/.git", "v2"); err != nil || strings.Join(r.ran, ",") != "tag -d v2" {
		t.Fatalf("delete: %v %v", err, r.ran)
	}

	r.ran = nil
	if _, err := s.PushTag("/r/.git", "v2", func(string) {}); err == nil || !strings.Contains(err.Error(), "no remote to push to") {
		t.Errorf("no remote: %v", err)
	}
	r.remotes = []string{"origin"}
	s.Repos, s.Accounts = pushRepos{}, noAccounts{}
	var lines []string
	p, err := s.PushTag("/r/.git", "v2", func(l string) { lines = append(lines, l) })
	if err != nil || p.Remote != "origin" || p.Tag != "v2" || strings.Join(r.ran, ",") != "push origin refs/tags/v2:refs/tags/v2 -u=false" || len(lines) != 1 {
		t.Fatalf("push: %+v %v %v %v", p, err, r.ran, lines)
	}
	r.pushErr = errors.New(" ! [rejected]        v2 -> v2 (already exists)")
	if _, err := s.PushTag("/r/.git", "v2", func(string) {}); err == nil || !strings.Contains(err.Error(), "never forces") {
		t.Errorf("rejected: %v", err)
	}
}
