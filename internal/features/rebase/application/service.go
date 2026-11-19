// Package application holds the rebase use cases: plan and run a rebase of
// the current branch onto another branch, tag or commit, and finish one
// that stopped on clashes (recording it for undo). A stopped rebase is
// continued, skipped or aborted by the merge feature's operation use
// cases; this feature only learns, afterwards, how it ended. Rebasing is
// local: no account or remote is involved. It depends only on domain
// ports. Import it as rebaseapp.
package application

import (
	"errors"
	"fmt"
	"time"

	rebase "github.com/macdipu/gitswitch/internal/features/rebase/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// opRebase is the name of the operation git is in the middle of.
const opRebase = "rebase"

// Service runs the rebase use cases.
type Service struct {
	Git     rebase.Git
	Store   rebase.Store
	Locator repo.Locator
	FS      *filesystem.FS // dry-run: changes are only printed
	Undo    rebase.Recorder
	Now     func() time.Time
}

// Plan says what rebasing the current branch onto onto will do, without
// changing anything. Plan.Blocked says why the rebase can't start now; an
// error means onto names nothing that can be rebased onto. A rebase that
// stopped earlier and was finished since is recorded for undo first.
func (s *Service) Plan(gitDir, onto string) (rebase.Plan, error) {
	_, _ = s.Finish(gitDir)
	p := rebase.Plan{Onto: onto}
	if err := rebase.CheckName(onto); err != nil {
		return p, err
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return p, err
	}
	p.Branch, p.Head = h.Branch, h.Hash
	if h.Hash == "" {
		return p, rebase.Check(h)
	}
	if p.Hash, err = s.Git.Resolve(gitDir, onto); err != nil {
		return p, err
	}
	if h.Branch != "" && onto == h.Branch {
		return p, fmt.Errorf("%s is the current branch; name another branch, tag or commit to rebase onto", onto)
	}
	blocked := func(err error) {
		if p.Blocked == "" && err != nil {
			p.Blocked = err.Error()
		}
	}
	blocked(rebase.Check(h))
	if p.Base = s.Git.MergeBase(gitDir, h.Hash, p.Hash); p.Base == "" {
		blocked(fmt.Errorf("%s and %s share no history, so there is nothing to put one on top of the other; GitSwitch won't rebase unrelated histories", orHead(h.Branch), onto))
		p.Summary = p.Blocked
		return p, nil
	}
	p.UpToDate = p.Base == p.Hash
	p.FastForward = !p.UpToDate && p.Base == h.Hash
	if p.Incoming, p.IncomingCount, err = s.Git.Commits(gitDir, p.Hash, h.Hash, rebase.MaxCommits, false); err != nil {
		return p, err
	}
	if !p.UpToDate && !p.FastForward {
		if p.Replayed, p.ReplayedCount, err = s.Git.Commits(gitDir, h.Hash, p.Hash, rebase.MaxCommits, true); err != nil {
			return p, err
		}
		ours, err := s.Git.Changed(gitDir, p.Base, h.Hash)
		if err != nil {
			return p, err
		}
		theirs, err := s.Git.Changed(gitDir, p.Base, p.Hash)
		if err != nil {
			return p, err
		}
		p.BothChanged = both(ours, theirs)
		if n := s.Git.Merges(gitDir, p.Hash, h.Hash); n > 0 {
			blocked(rebase.MergesError(h.Branch, n))
		}
		pushed, err := s.Git.Pushed(gitDir, p.Hash, h.Hash)
		if err != nil {
			return p, err
		}
		if len(pushed) > 0 {
			blocked(rebase.PushedError(h.Branch, pushed))
		}
	}
	p.Summary = p.Describe()
	return p, nil
}

func both(a, b []string) []string {
	in := map[string]bool{}
	for _, f := range a {
		in[f] = true
	}
	var out []string
	for _, f := range b {
		if in[f] {
			out = append(out, f)
		}
	}
	return out
}

func orHead(b string) string {
	if b == "" {
		return "HEAD"
	}
	return b
}

// Run rebases the current branch onto onto: its own commits are replayed
// on top, one by one. It refuses (before anything runs) mid-operation, on
// a detached HEAD, with uncommitted changes to tracked files, when a
// commit to replay is already pushed or is a merge commit, and when the
// histories are unrelated. A branch with no commits of its own
// fast-forwards. When commits clash the rebase stops (Outcome Stopped,
// git's own rebase state is left) for the files to be fixed and the
// rebase continued, skipped or aborted (the merge feature's Operation and
// Act); it is never aborted behind your back. Any other failure puts the
// repo back as it was. A rebase that moves the branch is reported to Undo,
// when it finishes: at once, or through Finish after a stop.
func (s *Service) Run(gitDir, onto string) (rebase.Rebased, error) {
	r := rebase.Rebased{Onto: onto}
	p, err := s.Plan(gitDir, onto)
	r.Branch = p.Branch
	if err != nil {
		return r, err
	}
	if p.Blocked != "" {
		return r, errors.New(p.Blocked)
	}
	if p.UpToDate {
		r.Outcome, r.Hash, r.Message = rebase.UpToDate, p.Head, p.Summary
		r.Short = short(p.Head)
		return r, nil
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("rebase %s onto %s in %s: %s", p.Branch, onto, wt, p.Summary)) {
		return r, nil
	}
	var before undo.Mark
	if s.Undo != nil {
		before = s.Undo.Before(gitDir)
	}
	pend := rebase.Pending{Branch: p.Branch, Orig: p.Head, Onto: p.Hash, OntoName: onto, Worktree: wt, Count: p.ReplayedCount, Time: s.now().Unix()}
	if s.Store != nil {
		if err := s.Store.Clear(gitDir); err != nil {
			return r, fmt.Errorf("could not prepare the rebase, nothing changed: %v", err)
		}
		if !p.FastForward {
			if err := s.Store.Save(gitDir, pend); err != nil {
				_ = s.Store.Clear(gitDir)
				return r, fmt.Errorf("could not prepare the rebase, nothing changed: %v", err)
			}
		}
	}
	if err := s.Git.Rebase(wt, p.Hash); err != nil {
		files, _ := s.Git.Conflicted(gitDir)
		if s.Git.Op(gitDir) == opRebase && len(files) > 0 {
			// Stopped on clashes: the user asked for this rebase, so it is
			// not aborted. git's state stays for continue, skip and abort,
			// and the pending record for undo once it is finished.
			r.Outcome, r.Conflicts = rebase.Stopped, files
			r.Message = fmt.Sprintf("The rebase of %s onto %s stopped: %d file(s) clash. Fix them, then continue; or abort to go back exactly as before. %s", p.Branch, onto, len(files), stoppedHelp)
			return r, nil
		}
		if s.Git.Op(gitDir) == opRebase {
			if aerr := s.Git.Abort(wt); aerr != nil {
				return r, fmt.Errorf("the rebase failed (%v) and could not be put back (%v); finish or abort it in a terminal: git rebase --abort", err, aerr)
			}
		}
		if s.Store != nil {
			_ = s.Store.Clear(gitDir)
		}
		return r, fmt.Errorf("the rebase failed, nothing changed: %v", err)
	}
	h, _ := s.Git.Head(gitDir)
	r.Hash, r.Short, r.Replayed = h.Hash, h.Short, p.ReplayedCount
	if p.FastForward {
		r.Outcome = rebase.FastForwards
		r.Message = fmt.Sprintf("%s fast-forwarded to %s (%s).", p.Branch, onto, commits(p.IncomingCount))
	} else {
		r.Outcome = rebase.Done
		r.Message = fmt.Sprintf("Rebased %s onto %s: %s replayed on top of %s.", p.Branch, onto, commits(p.ReplayedCount), short(p.Hash))
	}
	if s.Undo != nil {
		s.Undo.Rebased(gitDir, before, p.Hash, rebase.Label(r))
	}
	if s.Store != nil {
		_ = s.Store.Clear(gitDir)
	}
	return r, nil
}

const stoppedHelp = "(gitswitch conflict <path>, gitswitch continue, gitswitch abort)"

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

func short(h string) string { return h[:min(7, len(h))] }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Finish learns how a rebase GitSwitch started ended. When it is still
// stopped it changes nothing. When it is over and moved the branch (it was
// continued, or finished by skipping), it records the rebase for undo and
// reports true. When it was aborted, or the repo is no longer where the
// rebase left it, the pending record is dropped and nothing is recorded.
func (s *Service) Finish(gitDir string) (recorded bool, err error) {
	if s.Store == nil {
		return false, nil
	}
	pend, ok := s.Store.Load(gitDir)
	if !ok {
		return false, nil
	}
	if s.Git.Op(gitDir) == opRebase {
		if orig, onto := s.Git.Stopped(gitDir); orig == pend.Orig && onto == pend.Onto {
			return false, nil // still stopped
		}
	}
	h, herr := s.Git.Head(gitDir)
	if herr != nil {
		return false, herr
	}
	defer func() { _ = s.Store.Clear(gitDir) }()
	// Where the rebase should have left the branch: on the same branch,
	// moved, with onto below it, and no more than the replayed commits
	// (some are left out by a skip). Anything else is another action.
	if s.Undo == nil || h.Busy != "" || h.Branch != pend.Branch || h.Hash == pend.Orig ||
		s.Git.Count(gitDir, h.Hash, pend.Onto) != 0 || s.Git.Count(gitDir, pend.Onto, h.Hash) > pend.Count {
		return false, nil
	}
	s.Undo.Rebased(gitDir, undo.Mark{Worktree: pend.Worktree, Branch: pend.Branch, Head: pend.Orig}, pend.Onto,
		rebase.Label(rebase.Rebased{Branch: pend.Branch, Onto: pend.OntoName, Outcome: rebase.Done}))
	return true, nil
}

// Status says whether a rebase GitSwitch started is waiting to be
// finished. A rebase that ended since is finished first (Recorded says it
// was recorded for undo).
func (s *Service) Status(gitDir string) (rebase.Status, error) {
	rec, err := s.Finish(gitDir)
	st := rebase.Status{Recorded: rec}
	if err != nil {
		return st, err
	}
	if s.Store != nil {
		if p, ok := s.Store.Load(gitDir); ok {
			st.Pending, st.Branch, st.Onto = true, p.Branch, p.OntoName
		}
	}
	return st, nil
}
