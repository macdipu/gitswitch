package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckGit(t *testing.T) {
	for _, c := range []struct {
		out     string
		err     error
		install bool
		ok      bool
		version string
		why     string
	}{
		{"git version 2.43.0", nil, true, true, "2.43.0", ""},
		{"git version 2.30.0", nil, true, true, "2.30.0", ""},
		{"git version 2.39.5 (Apple Git-154)", nil, true, true, "2.39.5", ""},
		{"git version 2.29.2", nil, true, false, "2.29.2", "older than 2.30"},
		{"git version 1.8.3.1", nil, true, false, "1.8.3", "older than 2.30"},
		{"git version 3.0.0", nil, true, true, "3.0.0", ""},
		{"git version 2.51.0.windows.1", nil, true, true, "2.51.0", ""},
		{"", errors.New(`exec: "git": executable file not found`), false, false, "", "not installed"},
		{"weird output", nil, true, false, "", "could not be read"},
	} {
		g := CheckGit(c.out, c.err)
		if g.Installed != c.install || g.OK != c.ok || g.Version != c.version || (c.why == "") != (g.Why == "") || !strings.Contains(g.Why, c.why) {
			t.Errorf("CheckGit(%q, %v) = %+v", c.out, c.err, g)
		}
	}
}
