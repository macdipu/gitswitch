package domain

import (
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func TestMessage(t *testing.T) {
	for in, want := range map[string]string{"  Fix login \r\n\r\nBody\r\n": "Fix login \n\nBody", " \n\t": ""} {
		if got := Message(in); got != want {
			t.Errorf("Message(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFiles(t *testing.T) {
	c := refs.Changes{Staged: 2, Changed: 3, New: 1}
	if Files(c, false) != 2 || Files(c, true) != 4 {
		t.Errorf("Files = %d, %d", Files(c, false), Files(c, true))
	}
}

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{"./a/b.go": "a/b.go", `a\b.go`: "a/b.go", "a//b/../c": "a/c", " x ": "x"} {
		if got, err := CleanPath(in); err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "..", "../x", "/etc/passwd", "-x", "a/../../b"} {
		if got, err := CleanPath(in); err == nil {
			t.Errorf("CleanPath(%q) = %q, want an error", in, got)
		}
	}
}

func TestFileStates(t *testing.T) {
	for _, c := range []struct {
		f               File
		staged, changed bool
		words           string
	}{
		{File{Path: "a", Staged: "M", Worktree: "."}, true, false, "modified, staged"},
		{File{Path: "a", Staged: ".", Worktree: "M"}, false, true, "modified"},
		{File{Path: "a", Staged: "A", Worktree: "M"}, true, true, "added, staged; changed again"},
		{File{Path: "a", Staged: ".", Worktree: "D"}, false, true, "deleted"},
		{File{Path: "b", Orig: "a", Staged: "R", Worktree: "."}, true, false, "renamed from a, staged"},
		{File{Path: "a", Staged: "?", Worktree: "?"}, false, true, "new"},
		{File{Path: "a", Staged: "U", Worktree: "U", Conflict: true}, false, false, "conflict"},
	} {
		if c.f.IsStaged() != c.staged || c.f.IsChanged() != c.changed || c.f.Describe() != c.words {
			t.Errorf("%+v: staged %v changed %v %q", c.f, c.f.IsStaged(), c.f.IsChanged(), c.f.Describe())
		}
	}
	st := Status{Files: []File{
		{Path: "s", Staged: "M", Worktree: "."}, {Path: "both", Staged: "M", Worktree: "M"},
		{Path: "n", Staged: "?", Worktree: "?"}, {Path: "c", Staged: "U", Worktree: "U", Conflict: true},
	}}
	if len(st.Staged()) != 2 || len(st.Changed()) != 2 || len(st.Conflicts()) != 1 {
		t.Errorf("staged %v changed %v conflicts %v", st.Staged(), st.Changed(), st.Conflicts())
	}
	if f, ok := st.Find("n"); !ok || !f.New() {
		t.Errorf("Find(n) = %+v %v", f, ok)
	}
	if DiscardMessage("") != "gitswitch discard: detached HEAD" || DiscardMessage("main") != "gitswitch discard: main" {
		t.Error(DiscardMessage(""), DiscardMessage("main"))
	}
}
