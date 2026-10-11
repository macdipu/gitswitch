package infrastructure

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Stream runs git in dir and passes each progress line to progress (git
// redraws progress with \r, so both \r and \n end a line). On failure the
// error holds git's last lines.
func (r *Runner) Stream(dir string, progress func(string), args ...string) error {
	if r != nil && r.DryRun {
		progress("[dry-run] git " + ShellQuote(args))
		return nil
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	process.HideWindow(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	var last []string
	sc := bufio.NewScanner(stderr)
	sc.Split(splitProgress)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		progress(l)
		if last = append(last, l); len(last) > 6 {
			last = last[1:]
		}
	}
	if err := cmd.Wait(); err != nil {
		if len(last) == 0 {
			return err
		}
		return errors.New(strings.Join(last, "\n"))
	}
	return nil
}

func splitProgress(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
