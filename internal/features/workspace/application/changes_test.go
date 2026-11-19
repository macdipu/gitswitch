package application

import (
	"fmt"
	"strings"
	"testing"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

type fakeChanges struct {
	files    []workspace.File
	ran      []string
	diff     string // what Diff returns; "" a huge one
	patch    string
	applyErr error
}

func (f *fakeChanges) Status(string) ([]workspace.File, error) { return f.files, nil }
func (f *fakeChanges) Diff(_ string, staged bool, paths []string) (string, error) {
	if f.diff != "" {
		return f.diff, nil
	}
	return strings.Repeat("x", workspace.MaxDiffBytes+1), nil
}
func (f *fakeChanges) Stage(wt string, paths []string) error {
	f.ran = append(f.ran, "stage "+wt+" "+strings.Join(paths, ","))
	return nil
}
func (f *fakeChanges) Unstage(wt string, paths []string) error {
	f.ran = append(f.ran, "unstage "+wt+" "+strings.Join(paths, ","))
	return nil
}
func (f *fakeChanges) Discard(wt, msg string, paths []string) error {
	f.ran = append(f.ran, "discard "+wt+" "+msg+" "+strings.Join(paths, ","))
	return nil
}

func (f *fakeChanges) Apply(wt, patch string, o workspace.ApplyOptions) error {
	f.ran = append(f.ran, fmt.Sprintf("apply %+v", o))
	f.patch = patch
	return f.applyErr
}
func (f *fakeChanges) IntentToAdd(wt, path string) error {
	f.ran = append(f.ran, "intent "+path)
	return nil
}
func (f *fakeChanges) Backup(wt, msg, path string) error {
	f.ran = append(f.ran, "backup "+msg+" "+path)
	return nil
}

func newChanges(files ...workspace.File) (*Service, *fakeGit, *fakeChanges) {
	s, g, _, _ := newService()
	c := &fakeChanges{files: files}
	s.Changes = c
	return s, g, c
}

var (
	modified = workspace.File{Path: "a.go", Staged: ".", Worktree: "M"}
	staged   = workspace.File{Path: "s.go", Staged: "M", Worktree: "."}
	added    = workspace.File{Path: "new.go", Staged: "?", Worktree: "?"}
	clash    = workspace.File{Path: "c.go", Staged: "U", Worktree: "U", Conflict: true}
)

func TestStageUnstageDiscard(t *testing.T) {
	s, _, c := newChanges(modified, staged, added)
	if got, err := s.Stage("/r/.git", []string{"./a.go", "new.go", "a.go"}, false); err != nil || strings.Join(got, ",") != "a.go,new.go" {
		t.Fatalf("stage: %v %v", got, err)
	}
	if got, err := s.Stage("/r/.git", nil, true); err != nil || len(got) != 2 {
		t.Fatalf("stage all: %v %v", got, err)
	}
	if got, err := s.Unstage("/r/.git", []string{"s.go"}, false); err != nil || strings.Join(got, ",") != "s.go" {
		t.Fatalf("unstage: %v %v", got, err)
	}
	d, err := s.Discard("/r/.git", []string{"a.go"}, false)
	if err != nil || d.Stash != "gitswitch discard: main" || strings.Join(d.Files, ",") != "a.go" {
		t.Fatalf("discard: %+v %v", d, err)
	}
	want := "stage /r a.go,new.go|stage /r |unstage /r s.go|discard /r gitswitch discard: main a.go"
	if got := strings.Join(c.ran, "|"); got != want {
		t.Fatalf("ran %q\nwant %q", got, want)
	}
}

func TestChangesRefused(t *testing.T) {
	for name, c := range map[string]struct {
		files []workspace.File
		busy  string
		hash  string
		do    func(*Service) error
		want  string
	}{
		"stage a staged-only file": {files: []workspace.File{staged}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", []string{"s.go"}, false)
			return err
		}, want: "s.go has nothing to stage"},
		"stage a file without changes": {files: []workspace.File{modified}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", []string{"other.go"}, false)
			return err
		}, want: "other.go has no uncommitted changes"},
		"stage outside the repo": {files: []workspace.File{modified}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", []string{"../x"}, false)
			return err
		}, want: "outside the repo"},
		"stage a conflict": {files: []workspace.File{clash}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", []string{"c.go"}, false)
			return err
		}, want: "c.go has a conflict"},
		"stage all with a conflict": {files: []workspace.File{modified, clash}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", nil, true)
			return err
		}, want: "1 file(s) have conflicts"},
		"stage nothing named": {files: []workspace.File{modified}, do: func(s *Service) error {
			_, err := s.Stage("/r/.git", nil, false)
			return err
		}, want: "name the files"},
		"unstage with nothing staged": {files: []workspace.File{modified}, do: func(s *Service) error {
			_, err := s.Unstage("/r/.git", nil, true)
			return err
		}, want: "nothing is staged"},
		"unstage all mid-merge": {files: []workspace.File{staged}, busy: "merge", do: func(s *Service) error {
			_, err := s.Unstage("/r/.git", nil, true)
			return err
		}, want: "unstage files by name"},
		"discard mid-rebase": {files: []workspace.File{modified}, busy: "rebase", do: func(s *Service) error {
			_, err := s.Discard("/r/.git", nil, true)
			return err
		}, want: "rebase"},
		"discard before the first commit": {files: []workspace.File{added}, hash: "-", do: func(s *Service) error {
			_, err := s.Discard("/r/.git", nil, true)
			return err
		}, want: "no commit yet"},
		"discard nothing": {do: func(s *Service) error {
			_, err := s.Discard("/r/.git", nil, true)
			return err
		}, want: "nothing to discard"},
	} {
		s, g, ch := newChanges(c.files...)
		g.head.Busy = c.busy
		if c.hash == "-" {
			g.head.Hash = ""
		}
		err := c.do(s)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		if len(ch.ran) > 0 {
			t.Errorf("%s: ran %v", name, ch.ran)
		}
	}
}

func TestDiffAndRepoPath(t *testing.T) {
	s, _, _ := newChanges(modified)
	d, err := s.Diff("/r/.git", "./a.go", true)
	if err != nil || d.Path != "a.go" || !d.Staged || !d.Truncated || len(d.Text) != workspace.MaxDiffBytes {
		t.Fatalf("diff: %v %v %d", d.Path, err, len(d.Text))
	}
	if _, err := s.Diff("/r/.git", "../x", false); err == nil {
		t.Fatal("diff outside the repo")
	}
	for _, c := range [][3]string{{"/r/sub", "a.go", "sub/a.go"}, {"/r", "/r/b/c.go", "b/c.go"}, {"/elsewhere", "x", "../elsewhere/x"}} {
		if got := s.RepoPath("/r/.git", c[0], c[1]); got != c[2] {
			t.Errorf("RepoPath(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	s, _, c := newChanges(modified)
	s.FS.DryRun = true
	s.FS.Out = &strings.Builder{}
	if got, err := s.Stage("/r/.git", []string{"a.go"}, false); err != nil || len(got) != 1 || len(c.ran) != 0 {
		t.Fatalf("dry stage: %v %v %v", got, err, c.ran)
	}
}
