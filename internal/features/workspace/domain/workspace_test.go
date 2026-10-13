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
