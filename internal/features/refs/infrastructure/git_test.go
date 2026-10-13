package infrastructure

import (
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func TestParseStatus(t *testing.T) {
	entries := []string{
		"1 M. N... 100644 100644 100644 aaa bbb staged.go",
		"1 .M N... 100644 100644 100644 aaa aaa changed.go",
		"1 MM N... 100644 100644 100644 aaa bbb both.go",
		"1 .D N... 100644 100644 000000 aaa aaa gone.go",
		"2 R. N... 100644 100644 100644 aaa aaa R100 new name.go", "old name.go",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc clash.go",
		"? notes.txt",
		"? dir/with space.txt",
	}
	got := parseStatus(strings.Join(entries, "\x00") + "\x00")
	want := refs.Changes{Staged: 3, Changed: 3, New: 2, Conflicts: 1}
	if got != want {
		t.Errorf("parseStatus = %+v, want %+v", got, want)
	}
	if got := parseStatus(""); got.Any() {
		t.Errorf("empty status = %+v", got)
	}
}
