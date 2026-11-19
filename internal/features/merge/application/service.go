// Package application holds the merge use cases: plan and run a merge of
// a branch into the current one, describe an operation git stopped in the
// middle of and continue, skip or abort it, and read and resolve a
// conflicted file. Merging is local: no account or remote is involved.
// It depends only on domain ports. Import it as mergeapp.
package application

import (
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Service runs the merge, operation and conflict use cases.
type Service struct {
	Git     merge.Git
	Locator repo.Locator
	FS      *filesystem.FS // dry-run: changes are only printed
	Undo    merge.Recorder // remembers each merge so it can be undone; may be nil
	// OnFinished, when set, is told (kind, action) after a stopped
	// operation ended for good: continued to the end, skipped to the end or
	// aborted. The rebase feature uses it to record a rebase for undo.
	OnFinished func(gitDir, kind, action string)
}

// Plan says what merging from into the current branch will do, without
// changing anything. Plan.Blocked says why the merge can't start now; an
// error means from names nothing that can be merged.
func (s *Service) Plan(gitDir, from string) (merge.Plan, error) {
	p := merge.Plan{From: from}
	if err := merge.CheckName(from); err != nil {
		return p, err
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return p, err
	}
	p.Into = h.Branch
	if h.Hash == "" {
		return p, merge.Check(h)
	}
	if p.Hash, err = s.Git.Resolve(gitDir, from); err != nil {
		return p, err
	}
	if h.Branch != "" && from == h.Branch {
		return p, fmt.Errorf("%s is the current branch; name another branch to merge into it", from)
	}
	if p.Base = s.Git.MergeBase(gitDir, h.Hash, p.Hash); p.Base == "" {
		return p, fmt.Errorf("%s and %s share no history, so GitSwitch won't merge them; git merge --allow-unrelated-histories can, in a terminal", orHead(h.Branch), from)
	}
	p.UpToDate = p.Base == p.Hash
	p.FastForward = !p.UpToDate && p.Base == h.Hash
	if p.Incoming, p.IncomingCount, err = s.Git.Commits(gitDir, p.Hash, h.Hash, merge.MaxCommits); err != nil {
		return p, err
	}
	if p.Local, p.LocalCount, err = s.Git.Commits(gitDir, h.Hash, p.Hash, merge.MaxCommits); err != nil {
		return p, err
	}
	if !p.UpToDate && !p.FastForward {
		ours, err := s.Git.Changed(gitDir, p.Base, h.Hash)
		if err != nil {
			return p, err
		}
		theirs, err := s.Git.Changed(gitDir, p.Base, p.Hash)
		if err != nil {
			return p, err
		}
		p.BothChanged = both(ours, theirs)
		p.Conflicts, p.Predicted = s.Git.Predict(gitDir, h.Hash, p.Hash)
	}
	if err := merge.Check(h); err != nil {
		p.Blocked = err.Error()
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

// Merge merges from into the current branch: Auto fast-forwards when it
// can, else makes a merge commit; FastForward refuses when the branches
// have gone apart; Commit always makes a merge commit; Squash stages the
// changes without committing. It refuses mid-operation, on a detached
// HEAD and with uncommitted changes to tracked files. When files clash,
// the merge stops (Outcome Stopped) for them to be fixed, then continued
// or aborted (Operation); a squash that clashes is rolled back instead,
// since git keeps no merge to continue. A merge that moves the branch is
// reported to Undo.
func (s *Service) Merge(gitDir, from string, mode merge.Mode) (merge.Merged, error) {
	r := merge.Merged{From: from, Mode: mode}
	p, err := s.Plan(gitDir, from)
	r.Into = p.Into
	if err != nil {
		return r, err
	}
	if p.Blocked != "" {
		return r, errors.New(p.Blocked)
	}
	if p.UpToDate {
		r.Outcome, r.Message = merge.UpToDate, p.Summary
		return r, nil
	}
	if mode == merge.FastForward && !p.FastForward {
		return r, fmt.Errorf("%s can't fast-forward to %s: they have gone apart (%d commit(s) only on %s). Merge with a merge commit or squash instead", p.Into, from, p.LocalCount, p.Into)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("merge %s into %s in %s (%s): %s", from, p.Into, wt, modeName(mode), p.Summary)) {
		return r, nil
	}
	m := s.before(gitDir)
	if err := s.Git.Merge(wt, from, mode); err != nil {
		files, _ := s.Git.Conflicted(gitDir)
		if len(files) == 0 {
			return r, fmt.Errorf("the merge failed, nothing changed: %v", err)
		}
		if mode == merge.Squash || s.Git.Op(gitDir) != merge.OpMerge {
			if rerr := s.Git.ResetMerge(wt); rerr != nil {
				return r, fmt.Errorf("the squash clashed in %s and could not be rolled back (%v); put the files back with: git reset --merge", strings.Join(files, ", "), rerr)
			}
			return r, fmt.Errorf("nothing changed: squashing %s clashes in %d file(s): %s. Merge it instead (it stops so you can fix them), or fix the clash on %s first", from, len(files), strings.Join(files, ", "), from)
		}
		r.Outcome, r.Conflicts = merge.Stopped, files
		r.Message = merge.Describe(merge.OpMerge, files)
		return r, nil
	}
	h, _ := s.Git.Head(gitDir)
	r.Hash, r.Short = h.Hash, h.Short
	switch {
	case mode == merge.Squash:
		r.Outcome = merge.Squashed
		r.Message = fmt.Sprintf("The changes from %s are staged on %s, not committed yet: review them and commit (gitswitch commit -m <message>), or discard them.", from, p.Into)
		return r, nil // HEAD did not move: nothing to undo
	case h.Hash == p.Hash:
		r.Outcome = merge.FastForwards
		r.Message = fmt.Sprintf("%s fast-forwarded to %s (%d commit(s)).", p.Into, from, p.IncomingCount)
	default:
		r.Outcome = merge.MergeCommit
		r.Message = fmt.Sprintf("Merged %s into %s with merge commit %s.", from, p.Into, r.Short)
	}
	if s.Undo != nil {
		s.Undo.Merged(gitDir, m, p.Hash, from, merge.Label(r))
	}
	return r, nil
}

func modeName(m merge.Mode) string {
	switch m {
	case merge.FastForward:
		return "fast-forward only"
	case merge.Commit:
		return "always a merge commit"
	case merge.Squash:
		return "squash, not committed"
	}
	return "fast-forward when possible"
}

func (s *Service) before(gitDir string) (m undo.Mark) {
	if s.Undo != nil {
		return s.Undo.Before(gitDir)
	}
	return m
}

// Operation describes what git is in the middle of: its kind, the files
// still in conflict and the actions it allows. Kind "" means nothing.
func (s *Service) Operation(gitDir string) (merge.Operation, error) {
	o := merge.Operation{Kind: s.Git.Op(gitDir)}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return o, err
	}
	o.Branch = h.Branch
	if o.Conflicts, err = s.Git.Conflicted(gitDir); err != nil {
		return o, err
	}
	merging := ""
	if o.Kind == merge.OpMerge {
		_, merging = s.Git.Merging(gitDir)
	}
	o.Ours, o.Theirs = merge.Sides(o.Kind, h.Branch, merging)
	o.Actions = merge.ActionsFor(o.Kind)
	switch {
	case len(o.Actions) == 0:
		o.Why = merge.Describe(o.Kind, o.Conflicts)
	case len(o.Conflicts) > 0:
		o.Why = merge.Unresolved(o.Conflicts).Error()
	default:
		o.CanContinue = true
	}
	o.Message = merge.Describe(o.Kind, o.Conflicts)
	return o, nil
}

// Act continues, skips or aborts the operation in progress. Continue
// refuses while files are still in conflict and keeps the message git
// wrote; skip is for rebases, cherry-picks and reverts. A rebase or a
// series of picks may stop again on the next commit: the operation
// returned then is the new stop. Finishing a merge is reported to Undo.
func (s *Service) Act(gitDir, action string) (merge.Operation, error) {
	o, err := s.Operation(gitDir)
	if err != nil {
		return o, err
	}
	switch {
	case action != merge.Continue && action != merge.Skip && action != merge.Abort:
		return o, fmt.Errorf("unknown action %q: use continue, skip or abort", action)
	case o.Kind == "":
		return o, errors.New("nothing is in progress in this repo: there is nothing to " + action)
	case len(o.Actions) == 0:
		return o, errors.New(o.Message)
	case !o.Allowed(action):
		return o, fmt.Errorf("a %s can't be skipped: continue or abort it", o.Kind)
	case action == merge.Continue && !o.CanContinue:
		return o, merge.Unresolved(o.Conflicts)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("%s the %s in %s", action, o.Kind, wt)) {
		return o, nil
	}
	kind := o.Kind
	var m undo.Mark
	theirs, name := "", ""
	if kind == merge.OpMerge && action == merge.Continue {
		m = s.before(gitDir)
		theirs, name = s.Git.Merging(gitDir)
	}
	runErr := s.Git.Run(wt, kind, action)
	after, err := s.Operation(gitDir)
	if err != nil {
		return after, err
	}
	if runErr != nil {
		if after.Kind != "" && len(after.Conflicts) > 0 && action != merge.Abort {
			// Stopped again, on the next commit: say so, not as a failure.
			after.Message = fmt.Sprintf("The %s went on and stopped again. %s", kind, after.Message)
			return after, nil
		}
		return after, fmt.Errorf("could not %s the %s: %v", action, kind, runErr)
	}
	if kind == merge.OpMerge && action == merge.Continue && s.Undo != nil && after.Kind == "" {
		if name == "" {
			name = short(theirs)
		}
		h, _ := s.Git.Head(gitDir)
		s.Undo.Merged(gitDir, m, theirs, name, merge.Label(merge.Merged{Into: h.Branch, From: name, Outcome: merge.MergeCommit}))
	}
	if after.Kind == "" && s.OnFinished != nil {
		s.OnFinished(gitDir, kind, action)
	}
	return after, nil
}

func short(h string) string { return h[:min(7, len(h))] }

// Conflict reads a conflicted file: its base, ours and theirs versions
// and the working file with conflict markers. It refuses a file that has
// no conflict, a binary file and one bigger than merge.MaxConflictBytes.
func (s *Service) Conflict(gitDir, path string) (merge.Conflict, error) {
	c := merge.Conflict{Path: path}
	if err := s.conflicted(gitDir, path); err != nil {
		return c, err
	}
	c.Op = s.Git.Op(gitDir)
	h, _ := s.Git.Head(gitDir)
	merging := ""
	if c.Op == merge.OpMerge {
		_, merging = s.Git.Merging(gitDir)
	}
	c.OursLabel, c.TheirsLabel = merge.Sides(c.Op, h.Branch, merging)
	versions := []**string{&c.Base, &c.Ours, &c.Theirs}
	for i, v := range versions {
		b, ok, err := s.Git.Stage(gitDir, path, i+1)
		if err != nil {
			return c, err
		}
		if !ok {
			continue
		}
		if err := fits(path, b); err != nil {
			return c, err
		}
		text := string(b)
		*v = &text
	}
	b, ok, err := s.Git.ReadFile(s.Locator.WorktreeOf(gitDir), path)
	if err != nil {
		return c, err
	}
	if ok {
		if err := fits(path, b); err != nil {
			return c, err
		}
	}
	c.Text, c.Exists, c.Markers = string(b), ok, ok && merge.HasMarkers(string(b))
	c.Kind = merge.KindOf(c.Base != nil, c.Ours != nil, c.Theirs != nil)
	return c, nil
}

func fits(path string, b []byte) error {
	if merge.Binary(b) {
		return fmt.Errorf("%s is a binary file, so it can't be shown or edited here; keep ours or theirs for the whole file", path)
	}
	if len(b) > merge.MaxConflictBytes {
		return fmt.Errorf("%s is too big to fix here (%d KB, more than %d KB); fix it in your editor, then mark it resolved", path, len(b)>>10, merge.MaxConflictBytes>>10)
	}
	return nil
}

// conflicted refuses a path that is not in conflict (any path that is,
// came from git, so it is safe to read and write).
func (s *Service) conflicted(gitDir, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("name the conflicted file, e.g. src/app.go")
	}
	files, err := s.Git.Conflicted(gitDir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f == path {
			return nil
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("%s has no conflict: no file in this repo has one", path)
	}
	return fmt.Errorf("%s has no conflict; the files with conflicts are: %s", path, strings.Join(files, ", "))
}

// Resolve fixes a conflicted file and marks it resolved: action "ours" or
// "theirs" keeps that side's whole file; "done" marks it resolved as it
// is in the working folder, refused while conflict markers remain; "save"
// writes text as the file and marks it resolved, refused when text still
// has markers, is binary or too big. It returns the operation as it is
// afterwards. During a smart switch, GitSwitch's resolve finishes it
// instead.
func (s *Service) Resolve(gitDir, path, action, text string) (merge.Operation, error) {
	if err := s.conflicted(gitDir, path); err != nil {
		return merge.Operation{}, err
	}
	if op := s.Git.Op(gitDir); op == remote.SmartSwitch {
		return merge.Operation{}, remote.Busy(op)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	switch action {
	case "ours", "theirs":
	case "done":
		b, ok, err := s.Git.ReadFile(wt, path)
		if err != nil {
			return merge.Operation{}, err
		}
		if ok && merge.HasMarkers(string(b)) {
			return merge.Operation{}, merge.MarkersLeft(path)
		}
	case "save":
		if err := fits(path, []byte(text)); err != nil {
			return merge.Operation{}, err
		}
		if merge.HasMarkers(text) {
			return merge.Operation{}, merge.MarkersLeft(path)
		}
	default:
		return merge.Operation{}, fmt.Errorf("unknown action %q: use ours, theirs, done or save", action)
	}
	if s.FS.Dry(fmt.Sprintf("resolve %s in %s: %s", path, wt, action)) {
		return s.Operation(gitDir)
	}
	var err error
	switch action {
	case "ours", "theirs":
		err = s.Git.Take(wt, path, merge.Side(action))
	case "save":
		if err = s.Git.WriteFile(wt, path, []byte(text)); err == nil {
			err = s.Git.Add(wt, path)
		}
	default:
		err = s.Git.Add(wt, path)
	}
	if err != nil {
		return merge.Operation{}, err
	}
	return s.Operation(gitDir)
}
