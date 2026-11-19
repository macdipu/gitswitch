// Package infrastructure starts file watchers for repos: the working
// folder (without what git ignores) and the parts of the git dir that say
// what is staged, where HEAD and the branches are and whether a merge or
// rebase is under way. git runs once per watcher, to list the ignored
// folders; never per event. Import it as watchinfra.
package infrastructure

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	watch "github.com/macdipu/gitswitch/internal/features/watch/domain"
	"github.com/macdipu/gitswitch/internal/platform/filewatch"
)

// Source implements the watch domain's Source.
type Source struct {
	Git    *gitinfra.Runner
	Budget int // watches per repo (0: filewatch.DefaultBudget)
}

// heavy are folders made while watching that are almost always build
// output or downloads; they are not watched even before git is told to
// ignore them. Folders that exist at start are judged by git alone.
var heavy = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	".gradle": true, ".next": true, ".cache": true, ".idea": true, "target": true}

// Watch starts watching gitDir's repo.
func (s *Source) Watch(gitDir string) (watch.Stream, error) {
	gitDir = filepath.Clean(gitDir)
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		return nil, errors.New("not a git dir: " + gitDir)
	}
	roots := watch.Roots{GitDir: gitDir, CommonDir: commonDir(gitDir)}
	if wt := s.Git.WorktreeOf(gitDir); wt != "" && wt != gitDir {
		roots.Worktree = filepath.Clean(wt)
	}
	w, err := filewatch.New(s.Budget)
	if err != nil {
		return nil, err
	}
	st := &stream{w: w, roots: roots, events: make(chan watch.Event, 64), done: make(chan struct{})}
	if err := s.add(st, roots); err != nil {
		w.Close()
		return nil, err
	}
	go st.forward()
	return st, nil
}

// add watches the git dir first (it must fit), then the working folder,
// whole if it fits, else only its top folder.
func (s *Source) add(st *stream, r watch.Roots) error {
	// Not objects/ (large, and every change there comes with a ref or
	// index change) and not hooks or info.
	for _, d := range []string{r.GitDir, filepath.Join(r.GitDir, "logs")} {
		if err := st.w.Add(d); err != nil {
			return err
		}
	}
	if r.CommonDir != r.GitDir {
		if err := st.w.Add(r.CommonDir); err != nil {
			return err
		}
	}
	if err := st.w.AddTree(filepath.Join(r.CommonDir, "refs"), nil); err != nil {
		return err
	}
	if r.Worktree == "" {
		return nil
	}
	ignored := s.ignored(r.Worktree)
	var started atomic.Bool
	skip := func(dir string) bool {
		base := filepath.Base(dir)
		if base == ".git" || ignored[dir] {
			return true
		}
		return started.Load() && heavy[base]
	}
	err := st.w.AddTree(r.Worktree, skip)
	started.Store(true)
	if errors.Is(err, filewatch.ErrBudget) {
		st.partial = true
		return st.w.Add(r.Worktree)
	}
	return err
}

// ignored lists the folders git ignores in a working folder, with full
// paths. Only reading, so no index lock.
func (s *Source) ignored(wt string) map[string]bool {
	out := map[string]bool{}
	list, err := s.Git.Run("--no-optional-locks", "-C", wt, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return out
	}
	for _, p := range strings.Split(list, "\x00") {
		if strings.HasSuffix(p, "/") {
			out[filepath.Join(wt, filepath.FromSlash(strings.TrimSuffix(p, "/")))] = true
		}
	}
	return out
}

// commonDir is where a linked worktree's repo keeps its refs: the
// "commondir" file in its git dir; otherwise the git dir itself.
func commonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	p := strings.TrimSpace(string(b))
	if p == "" {
		return gitDir
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(gitDir, p)
	}
	return filepath.Clean(p)
}

type stream struct {
	w       *filewatch.Watcher
	roots   watch.Roots
	partial bool
	events  chan watch.Event
	done    chan struct{}
	once    sync.Once
}

func (s *stream) Roots() watch.Roots         { return s.roots }
func (s *stream) Events() <-chan watch.Event { return s.events }
func (s *stream) Partial() bool              { return s.partial || s.w.Partial() }

func (s *stream) Close() error {
	s.once.Do(func() { close(s.done) })
	return s.w.Close()
}

func (s *stream) forward() {
	defer close(s.events)
	for e := range s.w.Events() {
		select {
		case s.events <- watch.Event{Path: e.Path, Lost: e.Lost}:
		case <-s.done:
			return
		}
	}
}
