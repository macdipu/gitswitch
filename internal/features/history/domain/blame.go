package domain

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxBlameLines is how many lines of a file one blame returns; a longer
// file is cut there and flagged Truncated.
const MaxBlameLines = 5000

// maxBlameText cuts one very long line of text.
const maxBlameText = 4096

// MaxPickaxe is the longest text or regex a code search takes.
const MaxPickaxe = 500

// ErrBinaryFile is returned by blame for a file git treats as binary.
var ErrBinaryFile = errors.New("this file is binary, so there are no lines to blame")

// BlameOptions are the switches of git blame.
type BlameOptions struct {
	IgnoreWhitespace bool // -w: a whitespace-only change is not a new author
	Moves            bool // -M -C: follow lines moved or copied within the file and from other files
}

// BlameChunk is a run of lines from one commit (and one place in that
// commit), for drawing bands beside the file.
type BlameChunk struct {
	StartLine int    `json:"startLine"`
	Count     int    `json:"count"`
	Hash      string `json:"hash"`
	Short     string `json:"short"`
	Author    string `json:"author"`
	Email     string `json:"email"`
	Time      int64  `json:"time"`
	Summary   string `json:"summary"`
	OrigPath  string `json:"origPath"` // the file's name in that commit
	OrigLine  int    `json:"origLine"` // the first line's number in that commit
	Boundary  bool   `json:"boundary"` // the commit is the start of the history git looked at
	Account   string `json:"account"`
}

// BlameLine is one line of the file and the commit that last changed it.
type BlameLine struct {
	Line     int    `json:"line"`
	Hash     string `json:"hash"`
	Short    string `json:"short"`
	Author   string `json:"author"`
	Email    string `json:"email"`
	Time     int64  `json:"time"`
	Summary  string `json:"summary"`
	OrigPath string `json:"origPath"`
	OrigLine int    `json:"origLine"`
	Boundary bool   `json:"boundary"`
	Account  string `json:"account"`
	Text     string `json:"text"`
}

// Blame is who changed each line of a file at a revision.
type Blame struct {
	Path      string       `json:"path"`
	Rev       string       `json:"rev"`
	Lines     []BlameLine  `json:"lines"`
	Chunks    []BlameChunk `json:"chunks"`
	Truncated bool         `json:"truncated"` // the file has more lines than MaxBlameLines
}

// ParseBlame reads `git blame --porcelain` from r, up to max lines. It
// reports whether the output went on past max lines (the caller then stops
// git). Accounts are not set. Line ends are LF or CRLF; the CR is dropped.
func ParseBlame(r io.Reader, max int) (lines []BlameLine, truncated bool, err error) {
	type info struct {
		author, email, summary, file string
		time                         int64
		boundary                     bool
	}
	infos := map[string]*info{}
	br := bufio.NewReaderSize(r, 64<<10)
	var cur *info
	var curHash, curFile string
	var origLine, finalLine int
	for {
		raw, rerr := br.ReadString('\n')
		if raw == "" && rerr != nil {
			if rerr == io.EOF {
				return lines, false, nil
			}
			return lines, false, rerr
		}
		line := strings.TrimSuffix(raw, "\n")
		switch {
		case strings.HasPrefix(line, "\t"):
			if cur == nil {
				return lines, false, errors.New("unexpected git blame output")
			}
			if finalLine > max {
				return lines, true, nil
			}
			text := strings.TrimSuffix(line[1:], "\r")
			if len(text) > maxBlameText {
				text = text[:maxBlameText]
			}
			lines = append(lines, BlameLine{Line: finalLine, Hash: curHash, Short: shortHash(curHash), Author: cur.author,
				Email: cur.email, Time: cur.time, Summary: cur.summary, OrigPath: curFile, OrigLine: origLine,
				Boundary: cur.boundary, Text: text})
		case isBlameHeader(line):
			f := strings.Fields(line)
			curHash = f[0]
			origLine, _ = strconv.Atoi(f[1])
			finalLine, _ = strconv.Atoi(f[2])
			if infos[curHash] == nil {
				infos[curHash] = &info{}
			}
			cur = infos[curHash]
			curFile = cur.file
		case cur != nil:
			key, val, _ := strings.Cut(line, " ")
			val = strings.TrimSuffix(val, "\r")
			switch key {
			case "author":
				cur.author = val
			case "author-mail":
				cur.email = strings.TrimSuffix(strings.TrimPrefix(val, "<"), ">")
			case "author-time":
				cur.time, _ = strconv.ParseInt(val, 10, 64)
			case "summary":
				cur.summary = val
			case "boundary":
				cur.boundary = true
			case "filename":
				// Git repeats the name when a commit holds lines of more than
				// one file (moves and copies).
				cur.file, curFile = val, val
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return lines, false, nil
			}
			return lines, false, rerr
		}
	}
}

// isBlameHeader: "<hash> <original line> <final line> [<group size>]".
func isBlameHeader(s string) bool {
	f := strings.Fields(s)
	if len(f) < 3 || len(f) > 4 || (len(f[0]) != 40 && len(f[0]) != 64) {
		return false
	}
	for _, c := range f[0] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	_, e1 := strconv.Atoi(f[1])
	_, e2 := strconv.Atoi(f[2])
	return e1 == nil && e2 == nil
}

func shortHash(h string) string {
	if len(h) >= 7 {
		return h[:7]
	}
	return h
}

// BlameChunks groups consecutive lines of the same commit and place.
func BlameChunks(lines []BlameLine) []BlameChunk {
	chunks := []BlameChunk{}
	for i, l := range lines {
		if n := len(chunks); n > 0 && i > 0 {
			p, c := lines[i-1], &chunks[n-1]
			if p.Hash == l.Hash && p.OrigPath == l.OrigPath && l.OrigLine == p.OrigLine+1 && l.Line == p.Line+1 {
				c.Count++
				continue
			}
		}
		chunks = append(chunks, BlameChunk{StartLine: l.Line, Count: 1, Hash: l.Hash, Short: l.Short, Author: l.Author,
			Email: l.Email, Time: l.Time, Summary: l.Summary, OrigPath: l.OrigPath, OrigLine: l.OrigLine,
			Boundary: l.Boundary, Account: l.Account})
	}
	return chunks
}

// CheckPath refuses what cannot be a file path of the repo.
func CheckPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("say which file")
	}
	if strings.ContainsAny(p, "\x00\n") {
		return fmt.Errorf("%q is not a file path", p)
	}
	return nil
}

// FileCommit is one commit that changed a file, with the file's name there.
type FileCommit struct {
	Hash       string `json:"hash"`
	Short      string `json:"short"`
	Subject    string `json:"subject"`
	Author     string `json:"author"`
	Email      string `json:"email"`
	Time       int64  `json:"time"`
	Account    string `json:"account"`
	Path       string `json:"path"`       // the file's name at this commit
	Status     string `json:"status"`     // A added, M modified, D deleted, R renamed
	Similarity int    `json:"similarity"` // percent, for R
	OldPath    string `json:"oldPath"`    // for R: the name before this commit
}

// FileHistory is the commits that changed one file, newest first.
type FileHistory struct {
	Path    string       `json:"path"`
	Commits []FileCommit `json:"commits"`
	More    bool         `json:"more"` // older commits exist: ask again with skip
}

// MaxFileHistory is the most commits one request returns.
const MaxFileHistory = 1000

// ParseFileLog reads `git log --follow --name-status -M` with the format
// "\x1e%H\x1f%ae\x1f%an\x1f%at\x1f%s": one record per commit, then its
// status line. Commits with no status line (merges) keep the name the
// previous commit had, starting from path.
func ParseFileLog(out, path string) []FileCommit {
	cs := []FileCommit{}
	name := path
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		head, stat, _ := strings.Cut(rec, "\n")
		f := strings.SplitN(head, "\x1f", 5)
		if len(f) < 5 {
			continue
		}
		t, _ := strconv.ParseInt(f[3], 10, 64)
		c := FileCommit{Hash: f[0], Short: shortHash(f[0]), Email: f[1], Author: f[2], Time: t, Subject: f[4], Path: name, Status: "M"}
		for _, line := range strings.Split(stat, "\n") {
			p := strings.Split(strings.TrimRight(line, "\r"), "\t")
			if len(p) < 2 || p[0] == "" {
				continue
			}
			switch p[0][0] {
			case 'R', 'C':
				if len(p) < 3 {
					continue
				}
				c.Status, c.OldPath, c.Path = "R", unquoteGitPath(p[1]), unquoteGitPath(p[2])
				c.Similarity, _ = strconv.Atoi(p[0][1:])
				name = c.OldPath
			default:
				c.Status, c.Path = p[0][:1], unquoteGitPath(p[1])
			}
			break
		}
		cs = append(cs, c)
	}
	return cs
}

// unquoteGitPath undoes git's C-style quoting of unusual file names.
func unquoteGitPath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

// FileLog is the part of git that blame and file history need. It is a
// separate port, so fakes of Log need nothing; the application checks for
// it at run time.
type FileLog interface {
	// Blame runs git blame of path at rev. It returns ErrBinaryFile for a
	// binary file. Accounts are not set.
	Blame(gitDir, rev, path string, o BlameOptions) (Blame, error)
	// FileHistory lists commits that changed path, following renames,
	// newest first: n of them after skipping skip, and whether more exist.
	FileHistory(gitDir, path string, n, skip int) ([]FileCommit, bool, error)
}

// CheckPickaxe refuses a code search git cannot take.
func (f Filter) CheckPickaxe() error {
	if f.Pickaxe == "" {
		return nil
	}
	if len(f.Pickaxe) > MaxPickaxe {
		return fmt.Errorf("the code search is too long (at most %d characters)", MaxPickaxe)
	}
	if strings.ContainsAny(f.Pickaxe, "\x00") {
		return errors.New("the code search has a character git cannot take")
	}
	return nil
}
