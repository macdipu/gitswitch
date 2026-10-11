package gitcli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path"
	"strings"

	"github.com/macdipu/gitswitch/internal/sys"
)

// CheckCloneURL accepts the URLs gitswitch clones: SSH (git@host:owner/repo
// or ssh://) and HTTPS. Anything else, such as local paths, file:// or
// git's ext:: transport, is refused.
func CheckCloneURL(u string) error {
	u = strings.TrimSpace(u)
	bad := fmt.Errorf("not a repo URL: %q (use git@github.com:owner/repo.git or https://github.com/owner/repo)", u)
	if u == "" || strings.HasPrefix(u, "-") || strings.Contains(u, "::") || strings.ContainsAny(u, " \t\n") {
		return bad
	}
	if strings.Contains(u, "://") {
		pu, err := url.Parse(u)
		if err != nil {
			return bad
		}
		switch pu.Scheme {
		case "ssh", "git+ssh", "https", "http":
		default:
			return fmt.Errorf("%s:// links are not supported; use an SSH or HTTPS URL", pu.Scheme)
		}
	}
	if host, p := ParseRemote(u); host == "" || p == "" {
		return bad
	}
	return nil
}

// RepoName is the folder name git clone would create for a URL.
func RepoName(u string) string {
	_, p := ParseRemote(u)
	return strings.TrimSuffix(path.Base(strings.TrimSuffix(p, "/")), ".git")
}

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
	sys.HideWindow(cmd)
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
