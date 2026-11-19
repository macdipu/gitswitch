package infrastructure

import (
	"errors"
	"testing"
)

func TestNoCommits(t *testing.T) {
	for _, m := range []string{
		"fatal: your current branch 'main' does not have any commits yet",
		"fatal: bad revision 'HEAD'",
		"fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.",
		"fatal: bad default revision 'HEAD'",
	} {
		if !NoCommits(errors.New(m)) {
			t.Errorf("NoCommits(%q) = false", m)
		}
	}
	for _, m := range []string{"fatal: not a git repository", "fatal: bad revision 'feature'", ""} {
		if NoCommits(errors.New(m)) {
			t.Errorf("NoCommits(%q) = true", m)
		}
	}
	if NoCommits(nil) {
		t.Error("NoCommits(nil) = true")
	}
}
