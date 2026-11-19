package application

import (
	"strings"
	"testing"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

type fakeIgnorer struct {
	files     map[string]string
	dirs      map[string]bool
	tracked   map[string][]string
	ignored   bool
	untracked []string
}

func (f *fakeIgnorer) IgnoreFile(gitDir string, local bool) (string, error) {
	if local {
		return gitDir + "/info/exclude", nil
	}
	return strings.TrimSuffix(gitDir, "/.git") + "/.gitignore", nil
}
func (f *fakeIgnorer) ReadIgnore(file string) (string, error) { return f.files[file], nil }
func (f *fakeIgnorer) WriteIgnore(file, content string) error {
	f.files[file] = content
	return nil
}
func (f *fakeIgnorer) IsDir(_, p string) bool { return f.dirs[p] }
func (f *fakeIgnorer) Tracked(_, p string) ([]string, error) {
	if t := f.tracked[p]; t != nil {
		return t, nil
	}
	return []string{}, nil
}
func (f *fakeIgnorer) Ignored(string, string) (bool, error) { return f.ignored, nil }
func (f *fakeIgnorer) Untrack(wt, p string) error {
	f.untracked = append(f.untracked, wt+" "+p)
	delete(f.tracked, p)
	return nil
}

func newIgnore() (*Service, *fakeIgnorer) {
	s, _, _, _ := newService()
	ig := &fakeIgnorer{files: map[string]string{}, dirs: map[string]bool{"dir": true}, tracked: map[string][]string{}, ignored: true}
	s.Ignorer = ig
	return s, ig
}

func TestIgnore(t *testing.T) {
	s, ig := newIgnore()
	ig.files["/r/.gitignore"] = "node_modules/"
	r, err := s.Ignore("/r/.git", "./build.log", workspace.IgnoreFile, false, false)
	if err != nil || !r.Added || r.Pattern != "/build.log" || r.Where != ".gitignore" || r.File != "/r/.gitignore" || !r.Ignored || r.Note != "" {
		t.Fatalf("ignore: %+v %v", r, err)
	}
	if got := ig.files["/r/.gitignore"]; got != "node_modules/\n/build.log\n" {
		t.Errorf(".gitignore = %q", got)
	}
	r, err = s.Ignore("/r/.git", "build.log", workspace.IgnoreFile, false, false)
	if err != nil || r.Added || !strings.Contains(r.Note, "already had the line /build.log") {
		t.Fatalf("again: %+v %v", r, err)
	}
	if got := ig.files["/r/.gitignore"]; got != "node_modules/\n/build.log\n" {
		t.Errorf("a line was added twice: %q", got)
	}

	r, err = s.Ignore("/r/.git", "dir/a.log", workspace.IgnoreExt, true, false)
	if err != nil || r.Pattern != "*.log" || r.Where != ".git/info/exclude" || ig.files["/r/.git/info/exclude"] != "*.log\n" {
		t.Fatalf("ext, local: %+v %v %q", r, err, ig.files["/r/.git/info/exclude"])
	}
	if r, err = s.Ignore("/r/.git", "dir/sub/x.go", workspace.IgnoreFolder, false, false); err != nil || r.Pattern != "/dir/sub/" {
		t.Fatalf("folder: %+v %v", r, err)
	}
	if r, err = s.Ignore("/r/.git", "dir", workspace.IgnoreFile, false, false); err != nil || r.Pattern != "/dir/" {
		t.Fatalf("a folder named as a file: %+v %v", r, err)
	}
	if r, err = s.Ignore("/r/.git", "out/", workspace.IgnoreFolder, false, false); err != nil || r.Pattern != "/out/" {
		t.Fatalf("a folder typed with /: %+v %v", r, err)
	}
}

func TestIgnoreTracked(t *testing.T) {
	s, ig := newIgnore()
	ig.tracked["secret.env"] = []string{"secret.env"}
	ig.ignored = false
	r, err := s.Ignore("/r/.git", "secret.env", workspace.IgnoreFile, false, false)
	if err != nil || r.Ignored || len(r.Tracked) != 1 || len(r.Untracked) != 0 || len(ig.untracked) != 0 ||
		!strings.Contains(r.Note, "secret.env is tracked by git") || !strings.Contains(r.Note, "git rm --cached") {
		t.Fatalf("tracked: %+v %v %v", r, err, ig.untracked)
	}
	ig.ignored = true
	r, err = s.Ignore("/r/.git", "secret.env", workspace.IgnoreFile, false, true)
	if err != nil || !r.Ignored || len(r.Tracked) != 0 || strings.Join(r.Untracked, ",") != "secret.env" ||
		strings.Join(ig.untracked, ",") != "/r secret.env" || !strings.Contains(r.Note, "Took 1 file(s) out of git") {
		t.Fatalf("untrack: %+v %v %v", r, err, ig.untracked)
	}
}

func TestIgnoreRefused(t *testing.T) {
	s, _ := newIgnore()
	for _, c := range []struct {
		path string
		kind workspace.IgnoreKind
		want string
	}{
		{"../x", workspace.IgnoreFile, "outside the repo"},
		{"/etc/passwd", workspace.IgnoreFile, "outside the repo"},
		{"", workspace.IgnoreFile, "name a file"},
		{".git/config", workspace.IgnoreFile, "git's own"},
		{"Makefile", workspace.IgnoreExt, "no extension"},
		{"top.txt", workspace.IgnoreFolder, "top of the repo"},
	} {
		if _, err := s.Ignore("/r/.git", c.path, c.kind, false, false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Ignore(%q, %s) = %v, want %q", c.path, c.kind, err, c.want)
		}
	}
	s.Ignorer = nil
	if _, err := s.Ignore("/r/.git", "a", workspace.IgnoreFile, false, false); err == nil {
		t.Error("no Ignorer: want an error")
	}
}

func TestIgnoreDryRun(t *testing.T) {
	s, ig := newIgnore()
	s.FS = &filesystem.FS{DryRun: true, Out: &strings.Builder{}}
	ig.tracked["a.txt"] = []string{"a.txt"}
	r, err := s.Ignore("/r/.git", "a.txt", workspace.IgnoreFile, false, true)
	if err != nil || len(ig.files) != 0 || len(ig.untracked) != 0 || strings.Join(r.Untracked, ",") != "a.txt" {
		t.Fatalf("dry run: %+v %v %v %v", r, err, ig.files, ig.untracked)
	}
}
