// Package filewatch reports changed paths under folders, through the
// operating system's file events (inotify, kqueue, ReadDirectoryChangesW
// via fsnotify). It knows nothing about git: callers say which folders to
// watch and which to skip.
//
// The system watches one folder at a time, so AddTree walks a folder tree
// and watches each folder, and adds folders created later. Watches cost
// kernel resources (on macOS and BSD one open file per folder and per file
// in it), so a Watcher has a budget: a tree that does not fit is not
// watched, and the caller falls back to something smaller.
package filewatch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// DefaultBudget is how many watches a Watcher may use when New is given 0.
const DefaultBudget = 4000

// ErrBudget means a tree has more folders (or files, where each file costs
// a watch) than the budget allows.
var ErrBudget = errors.New("too many folders to watch")

// Event is one changed path. Lost means the system dropped events (its
// queue overflowed), so anything may have changed.
type Event struct {
	Path string
	Lost bool
}

// Watcher watches folders until Close.
type Watcher struct {
	fs     *fsnotify.Watcher
	events chan Event
	done   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	budget  int
	used    int
	watched map[string]int // folder -> its cost
	trees   []tree         // recursive roots, for folders created later
	partial bool           // a folder created later did not fit
	closed  bool
}

type tree struct {
	root string
	skip func(dir string) bool
}

// New starts a Watcher that may use up to budget watches (0: DefaultBudget).
func New(budget int) (*Watcher, error) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	f, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{fs: f, events: make(chan Event, 256), done: make(chan struct{}), budget: budget, watched: map[string]int{}}
	w.wg.Add(1)
	go w.loop()
	return w, nil
}

// Events sends changed paths until Close; it is closed then.
func (w *Watcher) Events() <-chan Event { return w.events }

// Partial reports whether a folder created after AddTree could not be
// watched because the budget ran out.
func (w *Watcher) Partial() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.partial
}

// Used is how many watches are in use.
func (w *Watcher) Used() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.used
}

// Add watches one folder (not its subfolders). A missing folder is not an
// error: there is nothing to watch yet.
func (w *Watcher) Add(dir string) error {
	c, err := cost(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addLocked(dir, c)
}

// AddTree watches root and every folder under it, except those skip
// (which may be nil) refuses, and keeps adding folders created under it
// later. When the whole tree does not fit the budget nothing is added and
// ErrBudget is returned.
func (w *Watcher) AddTree(root string, skip func(dir string) bool) error {
	dirs, total, err := walk(root, skip, w.remaining())
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.used+total > w.budget {
		return ErrBudget
	}
	for _, d := range dirs {
		if err := w.addLocked(d.path, d.cost); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	w.trees = append(w.trees, tree{root: filepath.Clean(root), skip: skip})
	return nil
}

func (w *Watcher) remaining() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.budget - w.used
}

func (w *Watcher) addLocked(dir string, c int) error {
	if w.closed {
		return errors.New("the watcher is closed")
	}
	dir = filepath.Clean(dir)
	if _, ok := w.watched[dir]; ok {
		return nil
	}
	if w.used+c > w.budget {
		return ErrBudget
	}
	if err := w.fs.Add(dir); err != nil {
		return err
	}
	w.watched[dir] = c
	w.used += c
	return nil
}

// Close stops watching and closes Events.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	close(w.done)
	err := w.fs.Close()
	w.wg.Wait()
	return err
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	defer close(w.events)
	for {
		select {
		case <-w.done:
			return
		case e, ok := <-w.fs.Events:
			if !ok {
				return
			}
			path := filepath.Clean(e.Name)
			if e.Has(fsnotify.Remove) || e.Has(fsnotify.Rename) {
				w.forget(path)
			}
			if e.Has(fsnotify.Create) {
				w.grow(path)
			}
			if !w.send(Event{Path: path}) {
				return
			}
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// An overflow, or a folder that could not be read: either way
			// changes may have been missed.
			_ = err
			if !w.send(Event{Lost: true}) {
				return
			}
		}
	}
}

func (w *Watcher) send(e Event) bool {
	select {
	case w.events <- e:
		return true
	case <-w.done:
		return false
	}
}

// grow watches a folder created inside a recursive tree, with what is
// already in it.
func (w *Watcher) grow(path string) {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return
	}
	w.mu.Lock()
	var skip func(string) bool
	found := false
	for _, t := range w.trees {
		if within(t.root, path) {
			skip, found = t.skip, true
			break
		}
	}
	w.mu.Unlock()
	if !found || (skip != nil && skip(path)) {
		return
	}
	dirs, _, err := walk(path, skip, -1)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, d := range dirs {
		if err := w.addLocked(d.path, d.cost); errors.Is(err, ErrBudget) {
			w.partial = true
			return
		}
	}
}

// forget drops a removed or renamed folder (and what was under it) from
// the budget; the system has already dropped its watches, or will.
func (w *Watcher) forget(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[path]; !ok {
		return // a file, or a folder that was never watched
	}
	for d, c := range w.watched {
		if within(path, d) {
			_ = w.fs.Remove(d) // already gone on most systems
			delete(w.watched, d)
			w.used -= c
		}
	}
}

type dir struct {
	path string
	cost int
}

// walk lists the folders of a tree with what each costs to watch. It stops
// with ErrBudget once the total passes limit (limit < 0: no limit).
func walk(root string, skip func(string) bool, limit int) ([]dir, int, error) {
	var out []dir
	total := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return filepath.SkipDir // unreadable: leave it out
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && skip != nil && skip(p) {
			return filepath.SkipDir
		}
		c, err := cost(p)
		if err != nil {
			return filepath.SkipDir
		}
		total += c
		if limit >= 0 && total > limit {
			return ErrBudget
		}
		out = append(out, dir{p, c})
		return nil
	})
	return out, total, err
}

// perFile reports whether the system needs a watch for every file too
// (kqueue opens each file in a watched folder).
var perFile = map[string]bool{"darwin": true, "freebsd": true, "openbsd": true, "netbsd": true, "dragonfly": true}[runtime.GOOS]

// cost is how many watches a folder takes.
func cost(dir string) (int, error) {
	if !perFile {
		if st, err := os.Stat(dir); err != nil {
			return 0, err
		} else if !st.IsDir() {
			return 0, errors.New(dir + " is not a folder")
		}
		return 1, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 1
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n, nil
}

// within reports whether p is root or inside it.
func within(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}
