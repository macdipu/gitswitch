// Package application holds the rewrite use cases: list the commits that
// can be rewritten, rewrite them (reorder, squash, fixup, reword, drop),
// reword an older commit, fold staged changes into one, and record a
// rewrite that stopped on clashes for undo once it is finished. It depends
// only on domain ports. Import it as rewriteapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the rewrite use cases. Rewriting is local: no account or
// remote is involved.
type Service struct {
	Git     rewrite.Git
	Locator repo.Locator
	FS      *filesystem.FS   // dry-run: changes are only printed
	Undo    rewrite.Recorder // remembers each rewrite so it can be undone; may be nil
}

// Todo lists the commits that can be rewritten, oldest first. By default
// those not pushed yet (the commits no remote-tracking branch contains),
// from the last merge commit or the first commit of the repo on; with
// since (a commit or ref) the commits from since up to HEAD, refused when
// any is pushed or is a merge commit. Todo.Blocked says why a rewrite
// can't start now. A rewrite that stopped earlier and has been finished
// since is recorded for undo first.
func (s *Service) Todo(gitDir, since string) (rewrite.Todo, error) {
	t := rewrite.Todo{Commits: []rewrite.Commit{}}
	fin, _ := s.Finish(gitDir)
	t.Pending = fin.Pending
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return t, err
	}
	t.Branch = h.Branch
	if err := rewrite.Check(h); err != nil {
		t.Blocked = err.Error()
	}
	if h.Hash == "" || h.Branch == "" {
		return t, nil // nothing to list: Blocked says why
	}
	unpushed, err := s.Git.Unpushed(gitDir)
	if err != nil {
		return t, err
	}
	if strings.TrimSpace(since) == "" {
		cs, note := rewrite.Eligible(unpushed)
		t.Note = note
		if len(cs) == 0 {
			return t, nil
		}
		base := cs[0].Parents[0]
		// The run must be exactly what is between base and HEAD.
		rng, err := s.Git.Range(gitDir, base)
		if err != nil {
			return t, err
		}
		if !sameCommits(rng, cs) {
			return t, errors.New("the commits that are not pushed are not one straight run on this branch, so GitSwitch can't tell where to start: pass --since <commit> with the oldest commit to rewrite")
		}
		t.Base, t.Commits = base, cs
		return t, nil
	}
	hash, err := s.Git.Resolve(gitDir, since)
	if err != nil {
		return t, err
	}
	first, err := s.Git.Commit(gitDir, hash)
	if err != nil {
		return t, err
	}
	if err := rewrite.CheckSince([]rewrite.Commit{first}); err != nil {
		return t, err
	}
	if !s.Git.IsAncestor(gitDir, hash, h.Hash) {
		return t, fmt.Errorf("%s is not on %s, so there is nothing to rewrite from it", first.Short, h.Branch)
	}
	rng, err := s.Git.Range(gitDir, first.Parents[0])
	if err != nil {
		return t, err
	}
	if err := rewrite.CheckSince(rng); err != nil {
		return t, err
	}
	local := map[string]bool{}
	for _, c := range unpushed {
		local[c.Hash] = true
	}
	for _, c := range rng {
		if !local[c.Hash] {
			return t, rewrite.Pushed(c)
		}
	}
	t.Base, t.Commits = first.Parents[0], rng
	return t, nil
}

func sameCommits(a, b []rewrite.Commit) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, c := range a {
		set[c.Hash] = true
	}
	for _, c := range b {
		if !set[c.Hash] {
			return false
		}
	}
	return true
}

// Preview checks steps against the commits Todo lists and says what the
// rewrite would do, without changing anything.
func (s *Service) Preview(gitDir, since string, steps []rewrite.Step) (rewrite.Plan, rewrite.Todo, error) {
	t, err := s.Todo(gitDir, since)
	if err != nil {
		return rewrite.Plan{}, t, err
	}
	if t.Blocked != "" {
		return rewrite.Plan{}, t, errors.New(t.Blocked)
	}
	if len(t.Commits) == 0 {
		return rewrite.Plan{}, t, errors.New("there is nothing to rewrite: no commit that is not pushed yet (use --since to name where to start)")
	}
	resolved, err := rewrite.Resolve(t.Commits, steps)
	if err != nil {
		return rewrite.Plan{}, t, err
	}
	p, err := rewrite.Build(t.Commits, resolved)
	return p, t, err
}

// Run rewrites the commits Todo lists as steps say: the steps are the new
// history, oldest first. Nothing runs until the steps are checked (the
// same commits as Todo, once each; the first kept commit not a squash or
// fixup; a message for each reword) and the repo is ready (no operation in
// progress, no detached HEAD, no uncommitted changes to tracked files).
// When a commit clashes, the rebase stops, as asked: the result has
// Outcome Stopped and the files, and continue, skip or abort (the merge
// feature's Operation) finish it. Any other failure is cleaned up, so
// nothing is left half done. A finished rewrite is reported to Undo.
func (s *Service) Run(gitDir, since string, steps []rewrite.Step) (rewrite.Rewritten, error) {
	p, t, err := s.Preview(gitDir, since, steps)
	if err != nil {
		return rewrite.Rewritten{}, err
	}
	if p.NoChange {
		return rewrite.Rewritten{}, errors.New("these steps change nothing: the commits stay as they are, in the same order")
	}
	return s.execute(gitDir, t, p, job{action: undo.Rewrite})
}

type job struct {
	action undo.Action // how the finished rewrite is recorded
	mark   *undo.Mark  // where the repo was before (default: now)
	fixup  string      // the commit made from staged changes, rolled back on failure
	label  string
}

func (s *Service) execute(gitDir string, t rewrite.Todo, p rewrite.Plan, j job) (rewrite.Rewritten, error) {
	r := rewrite.Rewritten{Before: p.Before, After: p.After, Conflicts: []string{}}
	by := map[string]rewrite.Commit{}
	for _, c := range t.Commits {
		by[c.Hash] = c
	}
	r.Summary = p.SummaryLines(by)
	wt := s.Locator.WorktreeOf(gitDir)
	label := j.label
	if label == "" {
		label = rewrite.Label(t.Branch, p)
	}
	if s.FS.Dry(fmt.Sprintf("rewrite %s in %s: %s", t.Branch, wt, p.Summary())) {
		return r, nil
	}
	m := s.before(gitDir)
	if j.mark != nil {
		m = *j.mark
	}
	if m.Head == "" {
		m = s.mark(gitDir, wt)
	}
	if err := s.Git.Rebase(gitDir, wt, t.Base, p); err != nil {
		if s.Git.Op(gitDir) == "rebase" {
			files, _ := s.Git.Conflicted(gitDir)
			if len(files) > 0 {
				if serr := s.Git.SavePending(gitDir, rewrite.Pending{Head: m.Head, Branch: t.Branch, Worktree: wt,
					Label: label, Action: j.action, Fixup: j.fixup}); serr != nil {
					// Without the record the stop can't be undone later; the
					// user asked for a stop, so keep it and say so.
					r.Message = "(GitSwitch could not remember this rewrite for undo: " + serr.Error() + ") "
				}
				r.Outcome, r.Conflicts = rewrite.Stopped, files
				r.Message += fmt.Sprintf("The rewrite stopped: %d file(s) clash. Fix each one (gitswitch conflict <path> --ours|--theirs|--done), then gitswitch continue to go on, gitswitch skip to leave that commit out, or gitswitch abort to go back to how it was. Undo is available once it is finished.", len(files))
				return r, nil
			}
			// Stopped for another reason (an empty commit, a failing hook):
			// back out, so nothing is half done.
			if aerr := s.Git.Abort(wt); aerr != nil {
				return r, fmt.Errorf("the rewrite failed (%v) and could not be abandoned (%v); finish it with: git rebase --abort", err, aerr)
			}
		}
		s.Git.Cleanup(gitDir)
		return r, s.failed(wt, j, err)
	}
	s.Git.Cleanup(gitDir)
	h, _ := s.Git.Head(gitDir)
	r.Outcome, r.Hash, r.Short = rewrite.Done, h.Hash, h.Short
	r.Message = fmt.Sprintf("%s. Undo it with: gitswitch undo", p.Summary())
	if s.Undo != nil {
		s.Undo.Done(gitDir, m, j.action, label)
	}
	return r, nil
}

// failed explains a rewrite that did not run. A fixup commit made from
// staged changes is taken back, so they are staged again as before.
func (s *Service) failed(wt string, j job, err error) error {
	if j.fixup != "" && j.mark != nil {
		if rerr := s.Git.SoftReset(wt, j.mark.Head); rerr != nil {
			return fmt.Errorf("the rewrite failed (%v), and your staged changes are in a fixup commit on top of the branch: take it off with git reset --soft HEAD~1", err)
		}
	}
	return fmt.Errorf("the rewrite failed, nothing changed: %v", err)
}

func (s *Service) before(gitDir string) (m undo.Mark) {
	if s.Undo != nil {
		return s.Undo.Before(gitDir)
	}
	return m
}

// mark reads where the repo is, for a service without an Undo recorder.
func (s *Service) mark(gitDir, wt string) undo.Mark {
	h, _ := s.Git.Head(gitDir)
	return undo.Mark{Worktree: wt, Branch: h.Branch, Head: h.Hash}
}

// Reword gives an older commit a new message and keeps everything else:
// a rewrite of that commit and the ones after it. Same refusals as Run.
func (s *Service) Reword(gitDir, rev, message string) (rewrite.Rewritten, error) {
	if rewrite.Tidy(message) == "" {
		return rewrite.Rewritten{}, errors.New("the new message is empty: write what the commit should say")
	}
	if strings.TrimSpace(rev) == "" {
		return rewrite.Rewritten{}, errors.New("name the commit to reword, e.g. 1a2b3c4")
	}
	t, err := s.Todo(gitDir, rev)
	if err != nil {
		return rewrite.Rewritten{}, err
	}
	if t.Blocked != "" {
		return rewrite.Rewritten{}, errors.New(t.Blocked)
	}
	steps := make([]rewrite.Step, len(t.Commits))
	for i, c := range t.Commits {
		steps[i] = rewrite.Step{Hash: c.Hash, Action: rewrite.Pick}
	}
	steps[0].Action, steps[0].Message = rewrite.Reword, message
	p, err := rewrite.Build(t.Commits, steps)
	if err != nil {
		return rewrite.Rewritten{}, err
	}
	if p.NoChange {
		return rewrite.Rewritten{}, fmt.Errorf("%s already says that: nothing to change", t.Commits[0].Short)
	}
	label := fmt.Sprintf("Reworded %s %s", t.Commits[0].Short, t.Commits[0].Subject)
	return s.execute(gitDir, t, p, job{action: undo.Rewrite, label: label})
}

// FixupInto folds what is staged into an older commit: the staged changes
// are committed as a fixup of it, and that commit is moved next to it and
// folded in (what git rebase -i --autosquash does, ordered by the commit's
// hash, not its subject). The commits after it are rewritten on top. It
// refuses when nothing is staged or other tracked changes are not staged
// (they would be in the way), and for pushed or merge commits. If the
// rewrite clashes, it stops like any other; if it fails, the staged
// changes are staged again as before. Undo takes everything back: the
// changes come back uncommitted.
func (s *Service) FixupInto(gitDir, rev string) (rewrite.Rewritten, error) {
	if strings.TrimSpace(rev) == "" {
		return rewrite.Rewritten{}, errors.New("name the commit to fold the staged changes into, e.g. 1a2b3c4")
	}
	t, err := s.Todo(gitDir, rev)
	if err != nil {
		return rewrite.Rewritten{}, err
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return rewrite.Rewritten{}, err
	}
	if err := rewrite.CheckFold(h); err != nil {
		return rewrite.Rewritten{}, err
	}
	target := t.Commits[0]
	wt := s.Locator.WorktreeOf(gitDir)
	label := fmt.Sprintf("Folded staged changes into %s %s", target.Short, target.Subject)
	if s.FS.Dry(fmt.Sprintf("commit the staged changes as a fixup of %s, then fold it into that commit (rewriting the %d commit(s) after it) in %s", target.Short, len(t.Commits)-1, wt)) {
		return rewrite.Rewritten{Conflicts: []string{}}, nil
	}
	m := s.before(gitDir)
	if m.Head == "" {
		m = s.mark(gitDir, wt)
	}
	fix, err := s.Git.CommitFixup(wt, target.Hash)
	if err != nil {
		return rewrite.Rewritten{}, fmt.Errorf("could not commit the staged changes, nothing changed: %v", err)
	}
	// The todo: the same commits, the fixup commit right after its target.
	fc, err := s.Git.Commit(gitDir, fix)
	if err != nil {
		_ = s.Git.SoftReset(wt, m.Head)
		return rewrite.Rewritten{}, err
	}
	all := append(append([]rewrite.Commit(nil), t.Commits...), fc)
	steps := []rewrite.Step{{Hash: target.Hash, Action: rewrite.Pick}, {Hash: fix, Action: rewrite.Fixup}}
	for _, c := range t.Commits[1:] {
		steps = append(steps, rewrite.Step{Hash: c.Hash, Action: rewrite.Pick})
	}
	p, err := rewrite.Build(all, steps)
	if err != nil {
		_ = s.Git.SoftReset(wt, m.Head)
		return rewrite.Rewritten{}, err
	}
	t.Commits = all
	p.Before = len(all) - 1 // the fixup commit is not one of the commits the user had
	res, err := s.execute(gitDir, t, p, job{action: undo.Amend, mark: &m, fixup: fix, label: label})
	if err == nil && res.Outcome == rewrite.Done {
		res.Message = fmt.Sprintf("The staged changes are now part of %s. Undo it with: gitswitch undo (the changes come back uncommitted)", target.Short)
	}
	return res, err
}

// Finish records a rewrite that stopped on clashes once the stopped
// rebase is over (continued to the end, skipped, or aborted), so Undo can
// take it back; with the rebase still in progress it says so and changes
// nothing. Without a pending rewrite it does nothing. It is safe to call
// at any time; Todo calls it first.
func (s *Service) Finish(gitDir string) (rewrite.Finished, error) {
	p, ok := s.Git.Pending(gitDir)
	if !ok {
		return rewrite.Finished{}, nil
	}
	if s.Git.Op(gitDir) == "rebase" {
		return rewrite.Finished{Pending: true, Message: "The rewrite is stopped: continue, skip or abort it."}, nil
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return rewrite.Finished{}, err
	}
	var f rewrite.Finished
	switch {
	case h.Branch != p.Branch || h.Hash == p.Head:
		f.Message = "The rewrite was aborted: the branch is as it was before."
	case p.Fixup != "" && h.Hash == p.Fixup:
		// Aborted after the staged changes were committed as a fixup.
		f.Message = "The rewrite was aborted. Your staged changes are in a fixup commit on top of the branch; undo takes it off and keeps the changes."
		f.Recorded = s.record(gitDir, p, "Committed a fixup of the staged changes (the rewrite was aborted)")
	default:
		f.Message = "The rewrite is finished."
		f.Recorded = s.record(gitDir, p, p.Label)
		if f.Recorded {
			f.Message += " Undo it with: gitswitch undo"
		}
	}
	s.Git.ClearPending(gitDir)
	return f, nil
}

func (s *Service) record(gitDir string, p rewrite.Pending, label string) bool {
	if s.Undo == nil {
		return false
	}
	s.Undo.Done(gitDir, undo.Mark{Worktree: p.Worktree, Branch: p.Branch, Head: p.Head}, p.Action, label)
	return true
}
