package application

import (
	"errors"
	"strings"
	"testing"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

const oneFile = "diff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n x\n-y\n+Y\n z\n"

func TestDiffHunks(t *testing.T) {
	s, _, c := newChanges(modified)
	c.diff = oneFile
	d, err := s.Diff("/r/.git", "a.go", false)
	if err != nil || len(d.Hunks) != 1 || d.Fingerprint != workspace.Fingerprint(oneFile) || d.NoHunks != "" {
		t.Fatalf("%+v %v", d, err)
	}
	if d.Text != strings.TrimRight(oneFile, "\n") {
		t.Errorf("text kept as before: %q", d.Text)
	}
	if d, _ := s.Diff("/r/.git", "", false); len(d.Hunks) != 0 || d.Fingerprint != "" {
		t.Errorf("a diff of every file has no hunks: %+v", d)
	}
	c.diff = "diff --git a/i b/i\nBinary files a/i and b/i differ\n"
	if d, _ := s.Diff("/r/.git", "i", false); d.NoHunks == "" || len(d.Hunks) != 0 {
		t.Errorf("binary: %+v", d)
	}
	c.diff = ""
	if d, _ := s.Diff("/r/.git", "a.go", false); !d.Truncated || d.NoHunks == "" {
		t.Errorf("cut diff: %+v", d.NoHunks)
	}
}

func TestStageUnstageDiscardHunks(t *testing.T) {
	s, _, c := newChanges(modified, staged, added)
	c.diff = oneFile
	fp := workspace.Fingerprint(oneFile)
	sel := workspace.Selection{Hunks: []int{0}}

	r, err := s.StageHunks("/r/.git", "a.go", fp, sel)
	if err != nil || r.Lines != 2 {
		t.Fatalf("stage: %+v %v", r, err)
	}
	if _, err := s.StageHunks("/r/.git", "new.go", fp, sel); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UnstageHunks("/r/.git", "s.go", fp, workspace.Selection{Lines: map[int][]int{0: {1}}}); err != nil {
		t.Fatal(err)
	}
	r, err = s.DiscardHunks("/r/.git", "a.go", fp, sel)
	if err != nil || r.Stash != "gitswitch discard: main" {
		t.Fatalf("discard: %+v %v", r, err)
	}
	want := "apply {Cached:true Reverse:false Check:false}|intent new.go|apply {Cached:true Reverse:false Check:false}|" +
		"apply {Cached:true Reverse:true Check:false}|apply {Cached:false Reverse:true Check:true}|backup gitswitch discard: main a.go|apply {Cached:false Reverse:true Check:false}"
	if got := strings.Join(c.ran, "|"); got != want {
		t.Fatalf("ran\n%s\nwant\n%s", got, want)
	}
	// Reverse patches keep an unpicked + as context; here every change is picked.
	if !strings.Contains(c.patch, "-y\n+Y\n") {
		t.Errorf("patch: %q", c.patch)
	}
}

func TestHunksRefused(t *testing.T) {
	fp := workspace.Fingerprint(oneFile)
	sel := workspace.Selection{Hunks: []int{0}}
	for name, c := range map[string]struct {
		files []workspace.File
		busy  string
		hash  string
		fp    string
		diff  string
		do    func(*Service, string) error
		want  string
	}{
		"stale":             {files: []workspace.File{modified}, fp: "0000000000000000", do: stage, want: "the file changed since you looked: reload"},
		"no fingerprint":    {files: []workspace.File{modified}, fp: "x", do: nofp, want: "reload"},
		"rebasing":          {files: []workspace.File{modified}, busy: "rebase", fp: fp, do: stage, want: "rebase"},
		"conflict":          {files: []workspace.File{clash}, fp: fp, do: func(s *Service, fp string) error { _, err := s.StageHunks("/r/.git", "c.go", fp, sel); return err }, want: "conflict"},
		"nothing there":     {files: []workspace.File{modified}, fp: fp, do: func(s *Service, fp string) error { _, err := s.StageHunks("/r/.git", "q.go", fp, sel); return err }, want: "no uncommitted changes"},
		"unstage unstaged":  {files: []workspace.File{modified}, fp: fp, do: func(s *Service, fp string) error { _, err := s.UnstageHunks("/r/.git", "a.go", fp, sel); return err }, want: "is not staged"},
		"stage staged":      {files: []workspace.File{staged}, fp: fp, do: func(s *Service, fp string) error { _, err := s.StageHunks("/r/.git", "s.go", fp, sel); return err }, want: "no change that is not staged"},
		"discard new":       {files: []workspace.File{added}, fp: fp, do: func(s *Service, fp string) error { _, err := s.DiscardHunks("/r/.git", "new.go", fp, sel); return err }, want: "discard the whole file"},
		"discard no commit": {files: []workspace.File{modified}, hash: "-", fp: fp, do: func(s *Service, fp string) error { _, err := s.DiscardHunks("/r/.git", "a.go", fp, sel); return err }, want: "no commit yet"},
		"outside":           {files: []workspace.File{modified}, fp: fp, do: func(s *Service, fp string) error { _, err := s.StageHunks("/r/.git", "../x", fp, sel); return err }, want: "outside"},
		"bad hunk": {files: []workspace.File{modified}, fp: fp, do: func(s *Service, fp string) error {
			_, err := s.StageHunks("/r/.git", "a.go", fp, workspace.Selection{Hunks: []int{4}})
			return err
		}, want: "no hunk 5"},
		"binary": {files: []workspace.File{modified}, diff: "diff --git a/a.go b/a.go\nBinary files a/a.go and b/a.go differ\n", do: func(s *Service, fp string) error {
			_, err := s.StageHunks("/r/.git", "a.go", workspace.Fingerprint("diff --git a/a.go b/a.go\nBinary files a/a.go and b/a.go differ\n"), sel)
			return err
		}, want: "binary"},
	} {
		s, g, ch := newChanges(c.files...)
		ch.diff = oneFile
		if c.diff != "" {
			ch.diff = c.diff
		}
		g.head.Busy = c.busy
		if c.hash == "-" {
			g.head.Hash = ""
		}
		err := c.do(s, c.fp)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		for _, r := range ch.ran {
			if strings.HasPrefix(r, "apply") || strings.HasPrefix(r, "backup") || strings.HasPrefix(r, "intent") {
				t.Errorf("%s: git was changed: %v", name, ch.ran)
			}
		}
	}
}

func stage(s *Service, fp string) error {
	_, err := s.StageHunks("/r/.git", "a.go", fp, workspace.Selection{Hunks: []int{0}})
	return err
}

func nofp(s *Service, _ string) error {
	_, err := s.StageHunks("/r/.git", "a.go", "", workspace.Selection{Hunks: []int{0}})
	return err
}

func TestHunksApplyFailure(t *testing.T) {
	// A failed apply to a new file leaves it as it was (not tracked); a failed discard check keeps nothing.
	s, _, c := newChanges(added)
	c.diff = oneFile
	c.applyErr = errors.New("does not apply")
	if _, err := s.StageHunks("/r/.git", "new.go", workspace.Fingerprint(oneFile), workspace.Selection{Hunks: []int{0}}); err == nil {
		t.Fatal("want an error")
	}
	if got := strings.Join(c.ran, "|"); !strings.HasSuffix(got, "unstage /r new.go") {
		t.Errorf("the new file must be left untracked again: %q", got)
	}
	s, _, c = newChanges(modified)
	c.diff = oneFile
	c.applyErr = errors.New("does not apply")
	if _, err := s.DiscardHunks("/r/.git", "a.go", workspace.Fingerprint(oneFile), workspace.Selection{Hunks: []int{0}}); err == nil {
		t.Fatal("want an error")
	}
	for _, r := range c.ran {
		if strings.HasPrefix(r, "backup") {
			t.Errorf("no stash entry when the discard cannot apply: %v", c.ran)
		}
	}
}
