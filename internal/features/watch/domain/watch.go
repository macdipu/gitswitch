// Package domain describes live refresh: what kind of change a changed
// path is (working files, the index, HEAD, refs, a merge or rebase), how
// bursts of changes are gathered into one, and the numbered list of
// changes front ends ask about. It also declares the port to the file
// watcher. It is pure. Import it as watch.
package domain

import (
	"path/filepath"
	"strings"
	"time"
)

// Kinds is a set of kinds of change.
type Kinds uint8

const (
	Files     Kinds = 1 << iota // a file in the working folder
	Index                       // what is staged
	Head                        // HEAD moved or points at another branch
	Refs                        // a branch, tag or remote branch moved
	Operation                   // a merge, rebase, cherry-pick or revert started or ended

	All = Files | Index | Head | Refs | Operation
)

var names = []struct {
	k    Kinds
	name string
}{{Files, "files"}, {Index, "index"}, {Head, "head"}, {Refs, "refs"}, {Operation, "operation"}}

// Names lists the kinds by name, in a fixed order; never nil.
func (k Kinds) Names() []string {
	out := []string{}
	for _, n := range names {
		if k&n.k != 0 {
			out = append(out, n.name)
		}
	}
	return out
}

// Roots are the folders a repo's watcher watches: its git dir, the common
// git dir (a linked worktree keeps refs there; otherwise the same as
// GitDir) and the working folder ("" for a bare repo).
type Roots struct {
	GitDir, CommonDir, Worktree string
}

// Classify says what kind of change a changed path is; 0 for paths that do
// not matter (objects, logs, lock files, git's own scratch files).
func (r Roots) Classify(path string) Kinds {
	if rel, ok := under(r.GitDir, path); ok {
		return gitPath(rel)
	}
	if r.CommonDir != "" && r.CommonDir != r.GitDir {
		if rel, ok := under(r.CommonDir, path); ok {
			if k := gitPath(rel); k == Refs {
				return k // the rest there belongs to the main worktree
			}
			return 0
		}
	}
	if rel, ok := under(r.Worktree, path); ok && r.Worktree != "" {
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			return 0 // the git dir, watched as such; or a .git file
		}
		return Files
	}
	return 0
}

// gitPath classifies a path inside a git dir, given with forward slashes.
func gitPath(rel string) Kinds {
	if strings.HasSuffix(rel, ".lock") {
		return 0 // git renames the lock onto the file when it is done
	}
	first, _, _ := strings.Cut(rel, "/")
	switch {
	case rel == "index":
		return Index
	case rel == "HEAD", rel == "logs/HEAD":
		return Head
	case rel == "packed-refs", rel == "FETCH_HEAD", first == "refs":
		return Refs
	}
	switch first {
	case "MERGE_HEAD", "MERGE_MSG", "MERGE_MODE", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD",
		"rebase-merge", "rebase-apply", "sequencer", "BISECT_LOG", "AUTO_MERGE":
		return Operation
	}
	return 0
}

// under returns path relative to root with forward slashes, when path is
// root or inside it.
func under(root, path string) (string, bool) {
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// Debounce gathers a burst of changes into one: the kinds are ready once
// nothing has changed for Quiet, or Max after the first change of the
// burst even if changes keep coming.
type Debounce struct {
	Quiet, Max  time.Duration
	pending     Kinds
	first, last time.Time
}

// Add records a change at now.
func (d *Debounce) Add(k Kinds, now time.Time) {
	if k == 0 {
		return
	}
	if d.pending == 0 {
		d.first = now
	}
	d.pending |= k
	d.last = now
}

// Next is when the pending kinds are ready; false when none are pending.
func (d *Debounce) Next() (time.Time, bool) {
	if d.pending == 0 {
		return time.Time{}, false
	}
	at := d.last.Add(d.Quiet)
	if d.Max > 0 {
		if limit := d.first.Add(d.Max); limit.Before(at) {
			at = limit
		}
	}
	return at, true
}

// Take returns the pending kinds when they are ready at now (and forgets
// them), or 0.
func (d *Debounce) Take(now time.Time) Kinds {
	at, ok := d.Next()
	if !ok || now.Before(at) {
		return 0
	}
	k := d.pending
	d.pending = 0
	return k
}

// Log numbers the changes of one repo's watcher. Numbers come from a
// counter shared by every watcher, so a watcher started again never reuses
// one. Start is the number the watcher began at: a front end that knows
// only an older number may have missed changes made while nothing watched.
type Log struct {
	Start   uint64
	Keep    int // how many changes to remember (0: 256)
	seq     uint64
	dropped uint64 // the newest change forgotten
	entries []entry
}

type entry struct {
	seq   uint64
	kinds Kinds
}

// NewLog starts a log at start.
func NewLog(start uint64) *Log { return &Log{Start: start, seq: start} }

// Seq is the latest change's number (Start before any).
func (l *Log) Seq() uint64 { return l.seq }

// Add records a change with its number, which must be above Seq.
func (l *Log) Add(seq uint64, k Kinds) {
	l.seq = seq
	l.entries = append(l.entries, entry{seq, k})
	keep := l.Keep
	if keep <= 0 {
		keep = 256
	}
	if len(l.entries) > keep {
		l.dropped = l.entries[len(l.entries)-keep-1].seq
		l.entries = append(l.entries[:0], l.entries[len(l.entries)-keep:]...)
	}
}

// Since returns the latest number and every kind that changed after since.
// A number from before Start, or older than the changes remembered, or
// from the future (another run of the app), may hide changes: All.
func (l *Log) Since(since uint64) (uint64, Kinds) {
	switch {
	case since == l.seq:
		return l.seq, 0
	case since < l.Start || since > l.seq:
		return l.seq, All
	case since < l.dropped:
		return l.seq, All
	}
	var k Kinds
	for _, e := range l.entries {
		if e.seq > since {
			k |= e.kinds
		}
	}
	return l.seq, k
}

// Event is one changed path from the watcher. Lost means changes were
// missed (the system's queue overflowed).
type Event struct {
	Path string
	Lost bool
}

// Stream is a running watcher for one repo.
type Stream interface {
	Roots() Roots
	// Events sends changed paths; it is closed when the watcher stops.
	Events() <-chan Event
	// Partial reports that only part of the working folder is watched
	// (it has too many folders), so some file changes go unseen.
	Partial() bool
	Close() error
}

// Source starts watchers (port).
type Source interface {
	Watch(gitDir string) (Stream, error)
}
