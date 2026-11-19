// Package application runs live refresh: one file watcher per repo,
// started when a front end first asks about the repo and stopped when
// nobody has asked for a while. Front ends wait for the next change with
// Wait (a long poll) instead of asking git again and again. It depends
// only on domain ports. Import it as watchapp.
package application

import (
	"context"
	"errors"
	"sync"
	"time"

	watch "github.com/macdipu/gitswitch/internal/features/watch/domain"
)

// Defaults for the Service's durations.
const (
	DefaultQuiet   = 300 * time.Millisecond
	DefaultMaxWait = 25 * time.Second
	DefaultIdle    = 2 * time.Minute
	defaultMax     = 2 * time.Second
)

// Service keeps at most one watcher per repo. The zero durations take the
// defaults above.
type Service struct {
	Source watch.Source

	Quiet    time.Duration // a burst ends after this long without changes
	MaxDelay time.Duration // report a burst that never ends after this long
	MaxWait  time.Duration // how long Wait waits for a change
	Idle     time.Duration // stop a watcher nobody asked about for this long

	mu      sync.Mutex
	seq     uint64 // shared by every repo's log; guarded by mu
	hubs    map[string]*hub
	done    chan struct{}
	closed  bool
	running sync.WaitGroup
}

// Change is what Wait returns: the latest change number, the kinds that
// changed after the number asked about (none when the wait ran out) and
// whether only part of the working folder is watched.
type Change struct {
	Seq     uint64   `json:"seq"`
	Kinds   []string `json:"kinds"`
	Partial bool     `json:"partial"`
}

// ErrClosed is returned once the Service has been closed.
var ErrClosed = errors.New("live refresh has stopped")

// hub is one repo's watcher and its log.
type hub struct {
	gitDir string
	ready  chan struct{} // closed once the watcher started (or failed)
	err    error         // why it failed to start
	quit   chan struct{} // closed to stop it

	// guarded by Service.mu
	waiters  int
	lastUsed time.Time

	mu      sync.Mutex // guards what follows
	log     *watch.Log
	changed chan struct{} // closed and replaced on every change
	partial bool
}

// Wait returns the kinds of change in gitDir's repo after since. When
// there are none yet it waits for one, up to MaxWait, and then returns
// the same number with no kinds. Without since (hasSince false) it returns
// the latest number at once, to start from. A since from before the
// watcher started (or from an earlier run) returns every kind: changes
// may have been missed.
func (s *Service) Wait(ctx context.Context, gitDir string, since uint64, hasSince bool) (Change, error) {
	h, done, err := s.acquire(gitDir)
	if err != nil {
		return Change{}, err
	}
	defer s.release(h)
	select {
	case <-h.ready:
	case <-ctx.Done():
		return Change{}, ctx.Err()
	case <-done:
		return Change{}, ErrClosed
	}
	if h.err != nil {
		return Change{}, h.err
	}
	timer := time.NewTimer(or(s.MaxWait, DefaultMaxWait))
	defer timer.Stop()
	for {
		h.mu.Lock()
		seq, kinds := h.log.Since(since)
		partial, changed := h.partial, h.changed
		h.mu.Unlock()
		if !hasSince {
			kinds = 0
		}
		c := Change{Seq: seq, Kinds: kinds.Names(), Partial: partial}
		if !hasSince || kinds != 0 {
			return c, nil
		}
		select {
		case <-changed:
		case <-timer.C:
			return c, nil
		case <-ctx.Done():
			return c, ctx.Err()
		case <-done:
			return c, ErrClosed
		}
	}
}

// Close stops every watcher and ends every Wait. The Service cannot be
// used again.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.done == nil {
		s.done = make(chan struct{})
	}
	close(s.done)
	for k, h := range s.hubs {
		close(h.quit)
		delete(s.hubs, k)
	}
	s.mu.Unlock()
	s.running.Wait()
}

// Watching lists the repos with a running watcher (for tests and status).
func (s *Service) Watching() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.hubs {
		out = append(out, k)
	}
	return out
}

func (s *Service) acquire(gitDir string) (*hub, chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrClosed
	}
	if s.done == nil {
		s.done = make(chan struct{})
	}
	if s.hubs == nil {
		s.hubs = map[string]*hub{}
	}
	h := s.hubs[gitDir]
	if h == nil {
		s.seq++
		h = &hub{gitDir: gitDir, ready: make(chan struct{}), quit: make(chan struct{}),
			log: watch.NewLog(s.seq), changed: make(chan struct{})}
		s.hubs[gitDir] = h
		s.running.Add(1)
		go s.run(h)
	}
	h.waiters++
	h.lastUsed = time.Now()
	return h, s.done, nil
}

func (s *Service) release(h *hub) {
	s.mu.Lock()
	h.waiters--
	h.lastUsed = time.Now()
	s.mu.Unlock()
}

// drop forgets h, unless another hub has taken its place.
func (s *Service) drop(h *hub) {
	s.mu.Lock()
	if s.hubs[h.gitDir] == h {
		delete(s.hubs, h.gitDir)
	}
	s.mu.Unlock()
}

// run starts h's watcher and turns its events into numbered changes until
// it is idle, stopped or the watcher ends.
func (s *Service) run(h *hub) {
	defer s.running.Done()
	stream, err := s.Source.Watch(h.gitDir)
	if err != nil {
		h.err = err
		s.drop(h)
		close(h.ready)
		return
	}
	defer stream.Close()
	roots := stream.Roots()
	h.mu.Lock()
	h.partial = stream.Partial()
	h.mu.Unlock()
	close(h.ready)

	idle := or(s.Idle, DefaultIdle)
	check := time.NewTicker(min(idle/4+time.Millisecond, 30*time.Second))
	defer check.Stop()
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	deb := watch.Debounce{Quiet: or(s.Quiet, DefaultQuiet), Max: or(s.MaxDelay, defaultMax)}
	events := stream.Events()
	for {
		var fire <-chan time.Time
		if at, ok := deb.Next(); ok {
			timer.Reset(time.Until(at))
			fire = timer.C
		}
		select {
		case e, ok := <-events:
			if !ok {
				// The watcher died: report everything and let the next
				// Wait start a new one.
				s.drop(h)
				s.publish(h, watch.All, stream.Partial())
				return
			}
			k := roots.Classify(e.Path)
			if e.Lost {
				k = watch.All
			}
			deb.Add(k, time.Now())
		case <-fire:
			if k := deb.Take(time.Now()); k != 0 {
				s.publish(h, k, stream.Partial())
			}
		case <-check.C:
			s.mu.Lock()
			stop := h.waiters == 0 && time.Since(h.lastUsed) >= idle && s.hubs[h.gitDir] == h
			if stop {
				delete(s.hubs, h.gitDir)
			}
			s.mu.Unlock()
			if stop {
				return
			}
		case <-h.quit:
			return
		}
	}
}

func (s *Service) publish(h *hub, k watch.Kinds, partial bool) {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	h.mu.Lock()
	h.log.Add(seq, k)
	h.partial = h.partial || partial
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
}

func or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
