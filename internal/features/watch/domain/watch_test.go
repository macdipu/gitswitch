package domain

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	wt := filepath.FromSlash("/work/repo")
	r := Roots{GitDir: filepath.Join(wt, ".git"), CommonDir: filepath.Join(wt, ".git"), Worktree: wt}
	for path, want := range map[string]Kinds{
		"src/main.go":               Files,
		"README.md":                 Files,
		".gitignore":                Files,
		".git":                      0,
		".git/index":                Index,
		".git/index.lock":           0,
		".git/HEAD":                 Head,
		".git/logs/HEAD":            Head,
		".git/refs/heads/main":      Refs,
		".git/refs/heads/main.lock": 0,
		".git/refs/remotes/o/main":  Refs,
		".git/packed-refs":          Refs,
		".git/FETCH_HEAD":           Refs,
		".git/MERGE_HEAD":           Operation,
		".git/rebase-merge/done":    Operation,
		".git/rebase-apply":         Operation,
		".git/CHERRY_PICK_HEAD":     Operation,
		".git/objects/ab/cdef":      0,
		".git/COMMIT_EDITMSG":       0,
		".git/ORIG_HEAD":            0,
	} {
		if got := r.Classify(filepath.Join(wt, filepath.FromSlash(path))); got != want {
			t.Errorf("Classify(%s) = %v, want %v", path, got.Names(), want.Names())
		}
	}
	if got := r.Classify(filepath.FromSlash("/elsewhere/file")); got != 0 {
		t.Errorf("a path outside the repo = %v", got.Names())
	}

	// A linked worktree: HEAD and index in its own git dir, refs in the
	// main one; the main worktree's HEAD is not this one's.
	main := filepath.FromSlash("/work/repo/.git")
	lt := Roots{GitDir: filepath.Join(main, "worktrees", "feat"), CommonDir: main, Worktree: filepath.FromSlash("/work/feat")}
	for path, want := range map[string]Kinds{
		"/work/repo/.git/worktrees/feat/HEAD":  Head,
		"/work/repo/.git/worktrees/feat/index": Index,
		"/work/repo/.git/refs/heads/x":         Refs,
		"/work/repo/.git/HEAD":                 0,
		"/work/repo/.git/index":                0,
		"/work/feat/a.txt":                     Files,
	} {
		if got := lt.Classify(filepath.FromSlash(path)); got != want {
			t.Errorf("linked worktree: Classify(%s) = %v, want %v", path, got.Names(), want.Names())
		}
	}

	// A bare repo has no working folder.
	bare := Roots{GitDir: filepath.FromSlash("/srv/r.git"), CommonDir: filepath.FromSlash("/srv/r.git")}
	if got := bare.Classify(filepath.FromSlash("/srv/r.git/refs/heads/main")); got != Refs {
		t.Errorf("bare refs = %v", got.Names())
	}
}

func TestNames(t *testing.T) {
	if got := (Refs | Files).Names(); !reflect.DeepEqual(got, []string{"files", "refs"}) {
		t.Errorf("Names = %v", got)
	}
	if got := Kinds(0).Names(); got == nil || len(got) != 0 {
		t.Errorf("no kinds = %#v, want an empty list", got)
	}
}

func TestDebounce(t *testing.T) {
	t0 := time.Unix(1000, 0)
	ms := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }
	d := Debounce{Quiet: 300 * time.Millisecond, Max: time.Second}
	if _, ok := d.Next(); ok {
		t.Fatal("nothing pending, yet a deadline")
	}
	d.Add(0, ms(0)) // nothing that matters
	if _, ok := d.Next(); ok {
		t.Fatal("kind 0 started a burst")
	}
	d.Add(Files, ms(0))
	d.Add(Index, ms(100))
	if at, _ := d.Next(); !at.Equal(ms(400)) {
		t.Fatalf("Next = %v, want 400ms", at.Sub(t0))
	}
	if k := d.Take(ms(399)); k != 0 {
		t.Fatalf("Take before the quiet time = %v", k.Names())
	}
	if k := d.Take(ms(400)); k != Files|Index {
		t.Fatalf("Take = %v", k.Names())
	}
	if k := d.Take(ms(2000)); k != 0 {
		t.Fatalf("Take again = %v", k.Names())
	}

	// Changes that never stop are still reported after Max.
	for i := 0; i <= 1500; i += 100 {
		d.Add(Files, ms(5000+i))
		if i == 900 {
			if k := d.Take(ms(5000 + i)); k != 0 {
				t.Fatalf("ready before Max: %v", k.Names())
			}
		}
		if i == 1000 {
			if k := d.Take(ms(5000 + i)); k != Files {
				t.Fatalf("not ready at Max: %v", k.Names())
			}
		}
	}
}

func TestLog(t *testing.T) {
	l := NewLog(10)
	if seq, k := l.Since(10); seq != 10 || k != 0 {
		t.Fatalf("Since(start) = %d %v", seq, k.Names())
	}
	if _, k := l.Since(3); k != All {
		t.Fatalf("a number from before the watcher = %v, want all", k.Names())
	}
	if _, k := l.Since(99); k != All {
		t.Fatalf("a number from the future = %v, want all", k.Names())
	}
	l.Add(12, Files)
	l.Add(15, Index|Files)
	l.Add(20, Head)
	if seq, k := l.Since(12); seq != 20 || k != Index|Files|Head {
		t.Fatalf("Since(12) = %d %v", seq, k.Names())
	}
	if _, k := l.Since(15); k != Head {
		t.Fatalf("Since(15) = %v", k.Names())
	}
	if _, k := l.Since(10); k != Files|Index|Head {
		t.Fatalf("Since(10) = %v", k.Names())
	}

	// Only the newest are kept; older numbers get everything.
	s := NewLog(0)
	s.Keep = 2
	s.Add(1, Files)
	s.Add(2, Index)
	s.Add(3, Refs)
	if _, k := s.Since(0); k != All {
		t.Fatalf("Since a forgotten change = %v, want all", k.Names())
	}
	if _, k := s.Since(1); k != Index|Refs {
		t.Fatalf("Since(1) = %v", k.Names())
	}
}
