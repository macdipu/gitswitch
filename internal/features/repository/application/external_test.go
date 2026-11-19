package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

type fakeTools struct {
	blobs   map[string]string
	config  map[string]string
	started [][]string
}

func (f *fakeTools) Show(_, spec string) ([]byte, bool) {
	s, ok := f.blobs[spec]
	return []byte(s), ok
}
func (f *fakeTools) ConfigGet(_, key string) string { return f.config[key] }
func (f *fakeTools) StartGit(_ string, args []string) error {
	f.started = append(f.started, args)
	return nil
}

type lists struct{ c account.Config }

func (l *lists) Load() *account.Config               { return &l.c }
func (l *lists) AddRepoPath(string, string) error    { return nil }
func (l *lists) RemoveRepoPath(string, string) error { return nil }
func (l *lists) SetRepoGroup(string, string) error   { return nil }
func (l *lists) SetSetting(string, string) error     { return nil }

type loc struct{ wt string }

func (l loc) GitDirFor(p string) string { return p }
func (l loc) WorktreeOf(string) string  { return l.wt }

func newTools(t *testing.T) (*Service, *fakeTools, *lists, string) {
	t.Helper()
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "1")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ft := &fakeTools{blobs: map[string]string{}, config: map[string]string{}}
	l := &lists{}
	return &Service{Lists: l, Locator: loc{wt}, Tools: ft}, ft, l, wt
}

func TestOpenFileRefusesOutsideAndMissing(t *testing.T) {
	s, _, l, _ := newTools(t)
	for _, p := range []string{"../x", "/etc/passwd", "", "-rf", "nope.txt"} {
		if err := s.OpenFile("/g", p); err == nil {
			t.Errorf("%q must be refused", p)
		}
	}
	if err := s.OpenFile("/g", "a.txt"); err == nil || !strings.Contains(err.Error(), "no editor set") {
		t.Errorf("no editor: %v", err)
	}
	l.c.Editor = "code --wait"
	if err := s.OpenFile("/g", "./a.txt"); err != nil {
		t.Errorf("with an editor: %v", err)
	}
}

func TestOpenTerminalNeedsAFolder(t *testing.T) {
	s, _, _, _ := newTools(t)
	s.Locator = loc{filepath.Join(t.TempDir(), "gone")}
	if err := s.OpenTerminal("/x"); err == nil || !strings.Contains(err.Error(), "folder not found") {
		t.Errorf("%v", err)
	}
	s.Locator = loc{t.TempDir()}
	s.Lists.(*lists).c.Terminal = "myterm {dir}"
	if err := s.OpenTerminal("/x"); err != nil {
		t.Errorf("%v", err)
	}
}

func TestExternalDiffUsesGitsToolWhenNoTemplate(t *testing.T) {
	s, ft, _, _ := newTools(t)
	if err := s.ExternalDiff("/g", "a.txt", false); err == nil || !strings.Contains(err.Error(), "no diff tool") {
		t.Fatalf("no tool: %v", err)
	}
	ft.config["diff.tool"] = "meld"
	if err := s.ExternalDiff("/g", "a.txt", true); err != nil {
		t.Fatal(err)
	}
	if err := s.ExternalDiff("/g", "a.txt", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"difftool --no-prompt --staged -- a.txt", "difftool --no-prompt -- a.txt"}
	for i, w := range want {
		if got := strings.Join(ft.started[i], " "); got != w {
			t.Errorf("%d: %q want %q", i, got, w)
		}
	}
	if err := s.ExternalDiff("/g", "../a", false); err == nil {
		t.Error("outside the repo")
	}
}

func TestExternalDiffTemplateChecksPlaceholders(t *testing.T) {
	s, _, l, _ := newTools(t)
	l.c.DiffTool = "meld"
	if err := s.ExternalDiff("/g", "a.txt", false); err == nil || !strings.Contains(err.Error(), "{left}") {
		t.Errorf("%v", err)
	}
	l.c.DiffTool = `meld "unclosed`
	if err := s.ExternalDiff("/g", "a.txt", false); err == nil {
		t.Error("bad quote")
	}
	l.c.DiffTool = "meld {left} {right}"
	if err := s.ExternalDiff("/g", "a.txt", false); err != nil {
		t.Errorf("%v", err)
	}
	l.c.DiffTool = "meld {left} {right}"
	if err := s.ExternalDiff("/g", "a.txt", true); err != nil {
		t.Errorf("%v", err)
	}
}

func TestExternalMerge(t *testing.T) {
	s, ft, l, _ := newTools(t)
	if err := s.ExternalMerge("/g", "a.txt"); err == nil || !strings.Contains(err.Error(), "no merge tool") {
		t.Fatalf("%v", err)
	}
	ft.config["merge.tool"] = "meld"
	if err := s.ExternalMerge("/g", "a.txt"); err != nil || strings.Join(ft.started[0], " ") != "mergetool --no-prompt -- a.txt" {
		t.Fatalf("%v %v", err, ft.started)
	}
	l.c.MergeTool = "meld {left}"
	if err := s.ExternalMerge("/g", "a.txt"); err == nil || !strings.Contains(err.Error(), "{merged}") {
		t.Errorf("%v", err)
	}
	ft.blobs[":2:a.txt"] = "ours"
	l.c.MergeTool = "meld {left} {base} {right} --output {merged}"
	if err := s.ExternalMerge("/g", "a.txt"); err != nil {
		t.Errorf("%v", err)
	}
	if err := s.ExternalMerge("/g", "/abs"); err == nil {
		t.Error("outside the repo")
	}
}

func TestExternalDiffAt(t *testing.T) {
	s, ft, l, _ := newTools(t)
	if err := s.ExternalDiffAt("/g", "a.txt", "--output=x"); err == nil {
		t.Error("an option is not a commit")
	}
	ft.config["diff.tool"] = "meld"
	if err := s.ExternalDiffAt("/g", "a.txt", "abc1234"); err != nil || strings.Join(ft.started[0], " ") != "difftool --no-prompt abc1234^ abc1234 -- a.txt" {
		t.Fatalf("%v %v", err, ft.started)
	}
	l.c.DiffTool = "meld {left} {right}"
	ft.blobs["abc1234^:a.txt"] = "old"
	if err := s.ExternalDiffAt("/g", "a.txt", "abc1234"); err != nil {
		t.Fatal(err)
	}
}
