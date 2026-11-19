package infrastructure

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Log also implements history.FileLog (blame and file history).
var _ history.FileLog = (*Log)(nil)

// isBinary reads the start of the file at rev:path the way git decides: a
// NUL byte in the first 8000 bytes. It also fails when there is no such
// file or it is a folder.
func (l *Log) isBinary(gitDir, rev, path string) (bool, error) {
	cmd := exec.Command(gitinfra.Program(), "--git-dir="+gitDir, "--literal-pathspecs", "cat-file", "blob", rev+":"+path)
	process.HideWindow(cmd)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	head, _ := io.ReadAll(io.LimitReader(out, 8000))
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if len(head) == 0 && errb.Len() > 0 {
		return false, fmt.Errorf("there is no file %s at %s", path, rev)
	}
	return bytes.IndexByte(head, 0) >= 0, nil
}

func (l *Log) Blame(gitDir, rev, path string, o history.BlameOptions) (history.Blame, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := history.CheckRef(rev); err != nil {
		return history.Blame{}, err
	}
	b := history.Blame{Path: path, Rev: rev, Lines: []history.BlameLine{}, Chunks: []history.BlameChunk{}}
	if bin, err := l.isBinary(gitDir, rev, path); err != nil {
		return b, err
	} else if bin {
		return b, history.ErrBinaryFile
	}
	args := []string{"--git-dir=" + gitDir, "--literal-pathspecs", "blame", "--porcelain"}
	if o.IgnoreWhitespace {
		args = append(args, "-w")
	}
	if o.Moves {
		args = append(args, "-M", "-C")
	}
	// git blame does not know --end-of-options; the rev cannot start with a dash.
	args = append(args, rev, "--", path)
	cmd := exec.Command(gitinfra.Program(), args...)
	process.HideWindow(cmd)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return b, err
	}
	if err := cmd.Start(); err != nil {
		return b, err
	}
	lines, truncated, perr := history.ParseBlame(out, history.MaxBlameLines)
	if truncated {
		_ = cmd.Process.Kill() // the rest is not wanted
	} else {
		_, _ = io.Copy(io.Discard, out)
	}
	werr := cmd.Wait()
	if !truncated && werr != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = werr.Error()
		}
		return b, errors.New(msg)
	}
	if perr != nil {
		return b, perr
	}
	b.Lines, b.Truncated = lines, truncated
	if b.Lines == nil {
		b.Lines = []history.BlameLine{}
	}
	return b, nil
}

func (l *Log) FileHistory(gitDir, path string, n, skip int) ([]history.FileCommit, bool, error) {
	args := []string{"--git-dir=" + gitDir, "--literal-pathspecs", "-c", "core.quotepath=false", "log", "--follow", "--name-status", "-M", "--no-color",
		"--format=%x1e%H%x1f%ae%x1f%an%x1f%at%x1f%s", "-n", strconv.Itoa(skip + n + 1)}
	// No --skip: git follows a rename only in the commits it shows, so a
	// skipped commit would lose the old name. It reads skip+n+1 and cuts.
	args = append(args, "--end-of-options", "HEAD", "--", path)
	out, err := l.Git.Run(args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return []history.FileCommit{}, false, nil
		}
		return nil, false, err
	}
	cs := history.ParseFileLog(out, path)
	if skip > len(cs) {
		skip = len(cs)
	}
	cs = cs[skip:]
	more := len(cs) > n
	if more {
		cs = cs[:n]
	}
	return cs, more, nil
}
