package application

import (
	"errors"
	"fmt"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

func (s *Service) fileLog() (history.FileLog, error) {
	g, ok := s.Log.(history.FileLog)
	if !ok {
		return nil, errors.New("blame and file history are not available")
	}
	return g, nil
}

// Blame says who last changed each line of a file at a revision ("" is
// HEAD), with the gitswitch account behind each author. It stops at
// history.MaxBlameLines lines and refuses binary files.
func (s *Service) Blame(gitDir, rev, path string, o history.BlameOptions) (*history.Blame, error) {
	g, err := s.fileLog()
	if err != nil {
		return nil, err
	}
	if err := history.CheckPath(path); err != nil {
		return nil, err
	}
	rev = strings.TrimSpace(rev)
	if err := history.CheckRef(rev); err != nil {
		return nil, err
	}
	b, err := g.Blame(gitDir, rev, path, o)
	if err != nil {
		if errors.Is(err, history.ErrBinaryFile) {
			return nil, fmt.Errorf("%s is a binary file: there are no lines to blame", path)
		}
		return nil, err
	}
	c := s.Accounts.Load()
	acct := map[string]string{}
	for i := range b.Lines {
		l := &b.Lines[i]
		id, ok := acct[l.Email]
		if !ok {
			if a := c.AccountByEmail(l.Email); a != nil {
				id = a.ID
			}
			acct[l.Email] = id
		}
		l.Account = id
	}
	b.Chunks = history.BlameChunks(b.Lines)
	return &b, nil
}

// FileHistory lists the commits that changed one file, following renames,
// newest first, n at a time after skipping skip.
func (s *Service) FileHistory(gitDir, path string, n, skip int) (*history.FileHistory, error) {
	g, err := s.fileLog()
	if err != nil {
		return nil, err
	}
	if err := history.CheckPath(path); err != nil {
		return nil, err
	}
	if n < 1 || n > history.MaxFileHistory {
		n = 50
	}
	if skip < 0 {
		skip = 0
	}
	cs, more, err := g.FileHistory(gitDir, path, n, skip)
	if err != nil {
		return nil, err
	}
	c := s.Accounts.Load()
	for i := range cs {
		if a := c.AccountByEmail(cs[i].Email); a != nil {
			cs[i].Account = a.ID
		}
	}
	if cs == nil {
		cs = []history.FileCommit{}
	}
	return &history.FileHistory{Path: path, Commits: cs, More: more}, nil
}
