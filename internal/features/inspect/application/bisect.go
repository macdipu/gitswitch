package application

import (
	"errors"
	"fmt"
	"strings"

	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

// BisectStatus says whether a bisect is running, the log so far, the
// commit to test now or the first bad commit.
func (s *Service) BisectStatus(gitDir string) (inspect.State, error) {
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return inspect.State{}, err
	}
	return s.state(gitDir, h)
}

// BisectStart begins a search for the first bad commit between good (a
// commit where it still worked) and bad (where it is broken), and checks
// out the first commit to test. It refuses when a bisect already runs, git
// is in the middle of something, there are no commits or tracked files
// have uncommitted changes. Where you were is remembered, so reset (even
// after a restart) comes back to it.
func (s *Service) BisectStart(gitDir, bad, good string) (inspect.State, error) {
	bad, good = strings.TrimSpace(bad), strings.TrimSpace(good)
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return inspect.State{}, err
	}
	if err := inspect.CheckStart(h); err != nil {
		return inspect.State{}, err
	}
	if err := inspect.CheckSide(bad, false); err != nil {
		return inspect.State{}, fmt.Errorf("the bad commit: %w", err)
	}
	if err := inspect.CheckSide(good, false); err != nil {
		return inspect.State{}, fmt.Errorf("the good commit: %w", err)
	}
	badHash, err := s.Git.Resolve(gitDir, bad)
	if err != nil {
		return inspect.State{}, err
	}
	goodHash, err := s.Git.Resolve(gitDir, good)
	if err != nil {
		return inspect.State{}, err
	}
	switch {
	case badHash == goodHash:
		return inspect.State{}, fmt.Errorf("%s and %s are the same commit; the good one must be older", bad, good)
	case !s.Git.IsAncestor(gitDir, goodHash, badHash):
		return inspect.State{}, fmt.Errorf("%s is not older than %s in this history, so the problem can't have started between them; pick a good commit before the bad one", good, bad)
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("start a bisect in %s: bad %s, good %s", wt, bad, good)) {
		return inspect.State{}, nil
	}
	if err := s.Git.SaveOrigin(gitDir, h.Branch, h.Hash); err != nil {
		return inspect.State{}, err
	}
	if _, err := s.Git.BisectStart(wt, badHash, goodHash); err != nil {
		s.Git.ClearOrigin(gitDir)
		_ = s.Git.BisectReset(wt, originTo(h.Branch, h.Hash))
		return inspect.State{}, fmt.Errorf("could not start the bisect: %v", err)
	}
	return s.BisectStatus(gitDir)
}

// BisectMark says whether the commit now checked out is good, bad or
// skipped (you can't test it), and returns the next commit to test or the
// first bad commit.
func (s *Service) BisectMark(gitDir, mark string) (inspect.State, error) {
	mark, err := inspect.CheckMark(mark)
	if err != nil {
		return inspect.State{}, err
	}
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return inspect.State{}, err
	}
	if err := inspect.CheckRunning(h); err != nil {
		return inspect.State{}, err
	}
	st, err := s.state(gitDir, h)
	if err != nil {
		return st, err
	}
	if st.Culprit != nil || len(st.Maybe) > 0 {
		return st, errors.New("the search is over; read the result with `gitswitch bisect status`, then go back with `gitswitch bisect reset`")
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("mark %s %s in the bisect in %s", h.Short, mark, wt)) {
		return st, nil
	}
	if _, err := s.Git.BisectMark(wt, mark); err != nil {
		return st, fmt.Errorf("could not mark the commit %s: %v", mark, err)
	}
	return s.BisectStatus(gitDir)
}

// BisectReset ends the bisect and returns to the branch (or commit) you
// were on when it started.
func (s *Service) BisectReset(gitDir string) (inspect.State, error) {
	h, err := s.Git.Head(gitDir)
	if err != nil {
		return inspect.State{}, err
	}
	if err := inspect.CheckRunning(h); err != nil {
		return inspect.State{}, err
	}
	branch, hash := s.Git.Origin(gitDir)
	to := originTo(branch, hash)
	wt := s.Locator.WorktreeOf(gitDir)
	if s.FS.Dry(fmt.Sprintf("end the bisect in %s and go back to %s", wt, orStart(to))) {
		return inspect.State{}, nil
	}
	if err := s.Git.BisectReset(wt, to); err != nil {
		return inspect.State{}, fmt.Errorf("could not go back to %s: %v", orStart(to), err)
	}
	s.Git.ClearOrigin(gitDir)
	st := inspect.State{Steps: []inspect.Step{}, Maybe: []inspect.Commit{}}
	st.Message = "The bisect is over. You are back on " + orStart(to) + "."
	if branch == "" && hash != "" {
		st.Message = "The bisect is over. You are back at commit " + hash[:min(7, len(hash))] + " (not on a branch)."
	}
	return st, nil
}

func originTo(branch, hash string) string {
	if branch != "" {
		return branch
	}
	return hash
}

func orStart(to string) string {
	if to == "" {
		return "where you started"
	}
	return to
}

// state describes a repo's bisect from git's own records: the log, the
// commit checked out and how many commits are still in doubt.
func (s *Service) state(gitDir string, h refs.Head) (inspect.State, error) {
	st := inspect.State{Steps: []inspect.Step{}, Maybe: []inspect.Commit{}}
	if h.Busy != "bisect" {
		st.Message = st.Words()
		return st, nil
	}
	st.Running = true
	branch, hash := s.Git.Origin(gitDir)
	st.Original = originTo(branch, hash)
	if branch == "" && len(hash) > 7 {
		st.Original = hash[:7]
	}
	steps, culprit, maybe := inspect.ParseLog(s.Git.BisectLog(gitDir))
	if steps != nil {
		st.Steps = steps
	}
	st.Left = s.Git.Candidates(gitDir)
	st.StepsLeft = inspect.EstimateSteps(st.Left)
	switch {
	case culprit != "":
		c, err := s.Git.Info(gitDir, culprit)
		if err != nil {
			return st, err
		}
		files, more, _ := s.Git.CommitFiles(gitDir, culprit, inspect.MaxCulpritFiles)
		if files == nil {
			files = []string{}
		}
		st.Culprit = &inspect.Culprit{Commit: c, Files: files, FilesMore: more}
		st.StepsLeft = 0
	case len(maybe) > 0:
		for _, m := range maybe {
			if c, err := s.Git.Info(gitDir, m); err == nil {
				st.Maybe = append(st.Maybe, c)
			}
		}
	default:
		if h.Hash != "" {
			c, err := s.Git.Info(gitDir, h.Hash)
			if err != nil {
				return st, err
			}
			st.Next = &c
		}
	}
	st.Message = st.Words()
	return st, nil
}
