package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tool settings are saved and the commands refuse what cannot work
// (nothing is started: the test environment never launches programs).
func TestExternalToolsAndFetchInterval(t *testing.T) {
	e := newEnv(t)
	e.clone("git@github.com:dipu/notes.git", "notes")
	notes := filepath.Join(e.root, "notes")

	e.gs("set", "terminal", "myterm --cwd {dir}")
	e.gs("set", "difftool", "meld {left} {right}")
	e.gs("set", "mergetool", "meld {left} {base} {right} --output {merged}")
	for k, want := range map[string]string{"gitswitch.terminal": "myterm --cwd {dir}", "gitswitch.difftool": "meld {left} {right}", "gitswitch.mergetool": "meld {left} {base} {right} --output {merged}"} {
		if got := e.cfg(notes, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	e.gsIn(notes, "terminal")
	e.gsErr(notes, "usage", "terminal", "a", "b")
	e.gsErr(notes, "usage", "difftool")
	e.gsErr(notes, "outside the repo", "difftool", "../x")
	e.gsErr(notes, "usage", "mergetool")

	e.gs("set", "fetch-interval", "15")
	if got := e.cfg(notes, "gitswitch.fetchInterval"); got != "15" {
		t.Errorf("fetchInterval = %q", got)
	}
	e.gsErr(notes, "choose off", "set", "fetch-interval", "7")
	e.gsErr(notes, "usage", "set", "fetch-interval", "soon")
	e.gs("set", "fetch-interval", "off")
	if got := e.cfg(notes, "gitswitch.fetchInterval"); got != "" {
		t.Errorf("off must remove it, got %q", got)
	}
	if out := e.gs("help"); !strings.Contains(out, "set fetch-interval") {
		t.Error("help must mention it")
	}
}
