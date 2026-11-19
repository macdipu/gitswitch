package application

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	watch "github.com/macdipu/gitswitch/internal/features/watch/domain"
)

type fakeStream struct {
	events chan watch.Event
	mu     sync.Mutex
	closed bool
}

func (f *fakeStream) Roots() watch.Roots {
	return watch.Roots{GitDir: "/r/.git", CommonDir: "/r/.git", Worktree: "/r"}
}
func (f *fakeStream) Events() <-chan watch.Event { return f.events }
func (f *fakeStream) Partial() bool              { return false }
func (f *fakeStream) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeStream) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

type fakeSource struct {
	mu      sync.Mutex
	started []*fakeStream
	err     error
}

func (f *fakeSource) Watch(string) (watch.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	s := &fakeStream{events: make(chan watch.Event, 16)}
	f.started = append(f.started, s)
	return s, nil
}

func (f *fakeSource) last() *fakeStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started[len(f.started)-1]
}

func (f *fakeSource) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func TestWait(t *testing.T) {
	src := &fakeSource{}
	s := &Service{Source: src, Quiet: 20 * time.Millisecond, MaxWait: 300 * time.Millisecond, Idle: time.Hour}
	defer s.Close()
	ctx := context.Background()

	// Without a number: the latest at once.
	c, err := s.Wait(ctx, "/r/.git", 0, false)
	if err != nil || len(c.Kinds) != 0 {
		t.Fatalf("first Wait = %+v %v", c, err)
	}
	start := c.Seq

	// A burst becomes one change with every kind in it.
	got := make(chan Change, 1)
	go func() {
		c, _ := s.Wait(ctx, "/r/.git", start, true)
		got <- c
	}()
	time.Sleep(20 * time.Millisecond)
	st := src.last()
	st.events <- watch.Event{Path: "/r/a.txt"}
	st.events <- watch.Event{Path: "/r/.git/index"}
	st.events <- watch.Event{Path: "/r/.git/objects/ab/cd"} // does not matter
	select {
	case c = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no change")
	}
	if c.Seq <= start || !reflect.DeepEqual(c.Kinds, []string{"files", "index"}) {
		t.Fatalf("change = %+v", c)
	}

	// Nothing new: the same number, no kinds, after MaxWait.
	t0 := time.Now()
	c2, err := s.Wait(ctx, "/r/.git", c.Seq, true)
	if err != nil || c2.Seq != c.Seq || len(c2.Kinds) != 0 || c2.Kinds == nil || time.Since(t0) < 250*time.Millisecond {
		t.Fatalf("timeout = %+v %v after %v", c2, err, time.Since(t0))
	}

	// A number from before this watcher: everything may have changed.
	if c3, _ := s.Wait(ctx, "/r/.git", start-1, true); len(c3.Kinds) != 5 {
		t.Fatalf("old number = %+v, want every kind", c3)
	}

	// A lost-events notice reports everything.
	st.events <- watch.Event{Lost: true}
	if c4, _ := s.Wait(ctx, "/r/.git", c.Seq, true); len(c4.Kinds) != 5 {
		t.Fatalf("lost events = %+v", c4)
	}

	// The request ending ends the wait.
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	cur, _ := s.Wait(ctx, "/r/.git", 0, false)
	if _, err := s.Wait(cctx, "/r/.git", cur.Seq, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Wait = %v", err)
	}

	// One watcher per repo.
	if src.count() != 1 {
		t.Fatalf("%d watchers started, want 1", src.count())
	}
}

func TestIdleStopAndRestart(t *testing.T) {
	src := &fakeSource{}
	s := &Service{Source: src, Quiet: 10 * time.Millisecond, MaxWait: 50 * time.Millisecond, Idle: 60 * time.Millisecond}
	defer s.Close()
	c, _ := s.Wait(context.Background(), "/r/.git", 0, false)
	first := src.last()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.Watching()) != 0 || !first.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("an idle watcher was not stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Started again on demand; the old number may have missed changes.
	c2, _ := s.Wait(context.Background(), "/r/.git", c.Seq, true)
	if src.count() != 2 || len(c2.Kinds) != 5 || c2.Seq <= c.Seq {
		t.Fatalf("after restart: %d watchers, change %+v", src.count(), c2)
	}
}

func TestStartErrorAndClose(t *testing.T) {
	src := &fakeSource{err: errors.New("no watching here")}
	s := &Service{Source: src}
	if _, err := s.Wait(context.Background(), "/r/.git", 0, true); err == nil || err.Error() != "no watching here" {
		t.Fatalf("Wait = %v, want the start error", err)
	}
	if len(s.Watching()) != 0 {
		t.Fatal("a watcher that failed to start is kept")
	}
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	s.MaxWait = time.Minute
	cur, err := s.Wait(context.Background(), "/r/.git", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Wait(context.Background(), "/r/.git", cur.Seq, true)
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Wait after Close = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not end the Wait")
	}
	if !src.last().isClosed() {
		t.Fatal("Close left a watcher running")
	}
	if _, err := s.Wait(context.Background(), "/r/.git", 0, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("Wait on a closed service = %v", err)
	}
}
