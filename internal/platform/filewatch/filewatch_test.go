package filewatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// next waits for an event about path (or any lost-events event).
func next(t *testing.T, w *Watcher, path string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-w.Events():
			if !ok {
				t.Fatal("events closed")
			}
			if e.Lost || e.Path == path {
				return
			}
		case <-deadline:
			t.Fatalf("no event for %s", path)
		}
	}
}

// quiet expects no event about path for a while.
func quiet(t *testing.T, w *Watcher, path string) {
	t.Helper()
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case e := <-w.Events():
			if e.Path == path {
				t.Fatalf("unexpected event for %s", path)
			}
		case <-deadline:
			return
		}
	}
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAddTree(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	write(t, filepath.Join(root, "a", "b", "old.txt"))
	write(t, filepath.Join(root, "skipped", "x.txt"))
	w, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	skip := func(d string) bool { return filepath.Base(d) == "skipped" }
	if err := w.AddTree(root, skip); err != nil {
		t.Fatal(err)
	}

	// A file deep in the tree.
	p := filepath.Join(root, "a", "b", "new.txt")
	write(t, p)
	next(t, w, p)

	// A skipped folder is not watched.
	s := filepath.Join(root, "skipped", "y.txt")
	write(t, s)
	quiet(t, w, s)

	// A folder made later is watched too, with what is in it.
	d := filepath.Join(root, "c")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	next(t, w, d)
	time.Sleep(50 * time.Millisecond) // the watch is added after the event
	p = filepath.Join(d, "later.txt")
	write(t, p)
	next(t, w, p)

	// Closing ends the events.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for range w.Events() {
	}
}

func TestBudget(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "b", "c", "d"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.AddTree(root, nil); !errors.Is(err, ErrBudget) {
		t.Fatalf("AddTree over budget = %v, want ErrBudget", err)
	}
	if w.Used() != 0 {
		t.Fatalf("a tree over budget used %d watches, want none", w.Used())
	}
	if err := w.Add(root); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(filepath.Join(root, "missing")); err != nil {
		t.Fatalf("a missing folder = %v, want nothing to watch", err)
	}
}
