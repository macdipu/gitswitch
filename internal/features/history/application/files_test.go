package application

import (
	"errors"
	"strings"
	"testing"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// fileLog is a log that also implements history.FileLog.
type fileLog struct {
	*fakeLog
	blame   history.Blame
	err     error
	commits []history.FileCommit
	gotRev  string
	gotN    int
	gotSkip int
}

func (f *fileLog) Blame(_, rev, path string, _ history.BlameOptions) (history.Blame, error) {
	f.gotRev = rev
	b := f.blame
	b.Path = path
	return b, f.err
}
func (f *fileLog) FileHistory(_, _ string, n, skip int) ([]history.FileCommit, bool, error) {
	f.gotN, f.gotSkip = n, skip
	return f.commits, false, f.err
}

func TestBlameMapsAuthorsToAccounts(t *testing.T) {
	s, _, l := newService()
	fl := &fileLog{fakeLog: l, blame: history.Blame{Lines: []history.BlameLine{
		{Line: 1, Hash: "aaaaaaa1", Email: "w@x.com", OrigLine: 1, OrigPath: "f"},
		{Line: 2, Hash: "aaaaaaa1", Email: "w@x.com", OrigLine: 2, OrigPath: "f"},
		{Line: 3, Hash: "bbbbbbb2", Email: "stranger@x.com", OrigLine: 1, OrigPath: "f"},
	}}}
	s.Log = fl
	b, err := s.Blame("/r/.git", "", "f", history.BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Lines[0].Account != "work" || b.Lines[2].Account != "" {
		t.Errorf("accounts: %+v", b.Lines)
	}
	if len(b.Chunks) != 2 || b.Chunks[0].Count != 2 || b.Chunks[0].Account != "work" {
		t.Errorf("chunks: %+v", b.Chunks)
	}
}

func TestBlameRefusals(t *testing.T) {
	s, _, l := newService()
	fl := &fileLog{fakeLog: l}
	s.Log = fl
	if _, err := s.Blame("/r/.git", "--output=x", "f", history.BlameOptions{}); err == nil {
		t.Error("a rev that looks like an option")
	}
	if _, err := s.Blame("/r/.git", "", "  ", history.BlameOptions{}); err == nil {
		t.Error("no path")
	}
	fl.err = history.ErrBinaryFile
	if _, err := s.Blame("/r/.git", "", "a.png", history.BlameOptions{}); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary: %v", err)
	}
	fl.err = errors.New("boom")
	if _, err := s.Blame("/r/.git", "", "f", history.BlameOptions{}); err == nil {
		t.Error("git error must pass")
	}
	// A log without the port.
	s.Log = l
	if _, err := s.Blame("/r/.git", "", "f", history.BlameOptions{}); err == nil {
		t.Error("log without FileLog")
	}
}

func TestFileHistoryAccountsAndPaging(t *testing.T) {
	s, _, l := newService()
	fl := &fileLog{fakeLog: l, commits: []history.FileCommit{{Hash: "h1", Email: "me@x.com"}, {Hash: "h2", Email: "who@x.com"}}}
	s.Log = fl
	fh, err := s.FileHistory("/r/.git", "f.txt", 0, -5)
	if err != nil {
		t.Fatal(err)
	}
	if fh.Commits[0].Account != "me" || fh.Commits[1].Account != "" || fl.gotN != 50 || fl.gotSkip != 0 {
		t.Errorf("%+v n=%d skip=%d", fh, fl.gotN, fl.gotSkip)
	}
	if _, err := s.FileHistory("/r/.git", "", 10, 0); err == nil {
		t.Error("no path")
	}
}

func TestBuildPassesCodeSearchAndChecksIt(t *testing.T) {
	s, _, l := newService()
	if _, err := s.Build("/r/.git", 10, history.Filter{Pickaxe: "needle", PickaxeRegex: true}); err != nil {
		t.Fatal(err)
	}
	if l.filter.Pickaxe != "needle" || !l.filter.PickaxeRegex {
		t.Errorf("filter %+v", l.filter)
	}
	if _, err := s.Build("/r/.git", 10, history.Filter{Pickaxe: strings.Repeat("x", 600)}); err == nil {
		t.Error("too long")
	}
}
