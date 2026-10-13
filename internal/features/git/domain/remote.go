// Package domain holds the rules for git remote URLs: how to read host and
// repo path from one, which hosts are the same server, and which URLs
// gitswitch agrees to clone. It is pure: no git, no files. Import it as
// remote.
package domain

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Parse splits a remote URL into host and repo path.
func Parse(u string) (host, path string) {
	u = strings.TrimSpace(u)
	if u == "" {
		return
	}
	if strings.Contains(u, "://") {
		pu, err := url.Parse(u)
		if err != nil {
			return
		}
		return strings.ToLower(pu.Hostname()), strings.Trim(pu.Path, "/")
	}
	if isDrivePath(u) {
		return // C:\src\repo or c:/src/repo is a folder, not host "c"
	}
	// scp-like: git@host:owner/repo.git. Like git, a host in brackets may
	// hold colons: git@[::1]:owner/repo or [git@host:2222]:owner/repo.
	from := 0
	if b := strings.Index(u, "@["); b >= 0 {
		from = b + 1
	}
	bracketed := false
	if u[from] == '[' {
		e := strings.Index(u[from:], "]")
		if e < 0 {
			return
		}
		bracketed = true
		from += e
	}
	i := strings.Index(u[from:], ":")
	if i < 0 {
		return
	}
	i += from
	if i == 0 || strings.Contains(u[:i], "/") {
		return
	}
	h := u[:i]
	if bracketed {
		h = strings.Replace(strings.Replace(h, "[", "", 1), "]", "", 1)
	}
	if j := strings.LastIndex(h, "@"); j >= 0 {
		h = h[j+1:]
	}
	if bracketed && strings.Count(h, ":") == 1 {
		if name, port, _ := strings.Cut(h, ":"); isPort(port) {
			h = name // [host:2222]
		}
	}
	return strings.ToLower(h), strings.Trim(u[i+1:], "/")
}

// isDrivePath reports a Windows path that starts with a drive letter.
func isDrivePath(u string) bool {
	return len(u) >= 3 && u[1] == ':' && (u[2] == '\\' || u[2] == '/') &&
		(u[0] >= 'a' && u[0] <= 'z' || u[0] >= 'A' && u[0] <= 'Z')
}

func isPort(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CanonicalHost maps the "SSH over port 443" hosts to their main host.
func CanonicalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	switch h {
	case "ssh.github.com":
		return "github.com"
	case "altssh.gitlab.com":
		return "gitlab.com"
	case "altssh.bitbucket.org":
		return "bitbucket.org"
	}
	return h
}

// SameHost compares hosts after mapping port-443 aliases.
func SameHost(a, b string) bool { return CanonicalHost(a) == CanonicalHost(b) }

// CheckCloneURL accepts the URLs gitswitch clones: SSH (git@host:owner/repo
// or ssh://) and HTTPS. Anything else, such as local paths, file:// or
// git's ext:: transport, is refused.
func CheckCloneURL(u string) error {
	u = strings.TrimSpace(u)
	bad := fmt.Errorf("not a repo URL: %q (use git@github.com:owner/repo.git or https://github.com/owner/repo)", u)
	if u == "" || strings.HasPrefix(u, "-") || strings.ContainsAny(u, " \t\n") {
		return bad
	}
	// git's <transport>::<address> form (ext::, fd::, ...). A bracketed
	// IPv6 host like git@[::1]:o/r has "@[" before its "::".
	if i := strings.Index(u, "::"); i >= 0 && !strings.ContainsAny(u[:i], "@[") {
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
	host, p := Parse(u)
	if host == "" || p == "" {
		return bad
	}
	// Git on Windows reads c:repo as a folder on drive C, not host "c".
	if !strings.Contains(u, "://") && len(host) == 1 {
		return bad
	}
	return nil
}

// RepoName is the folder name git clone would create for a URL.
func RepoName(u string) string {
	_, p := Parse(u)
	return strings.TrimSuffix(path.Base(strings.TrimSuffix(p, "/")), ".git")
}

// WebURL is the web page of the repo a remote points at, e.g.
// https://github.com/acme/api; "" when the remote is not on a server.
func WebURL(remoteURL string) string {
	host, p := Parse(remoteURL)
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	if host == "" || p == "" || strings.ContainsAny(host, " /\\") {
		return ""
	}
	return "https://" + CanonicalHost(host) + "/" + p
}

// CommitURL is the web page of one commit on the remote's host; "" when
// the remote is not on a server. GitLab and Bitbucket use their own paths;
// GitHub's is also used by Gitea, Forgejo and most others.
func CommitURL(remoteURL, hash string) string {
	base := WebURL(remoteURL)
	if base == "" {
		return ""
	}
	host, _ := Parse(remoteURL)
	switch h := CanonicalHost(host); {
	case strings.Contains(h, "gitlab"):
		return base + "/-/commit/" + hash
	case strings.Contains(h, "bitbucket"):
		return base + "/commits/" + hash
	}
	return base + "/commit/" + hash
}

// PullRequestURL is the page that opens a pull request (a merge request on
// GitLab) for a branch pushed to the remote, against the repo's default
// branch; "" for a host that is not GitHub, GitLab or Bitbucket.
func PullRequestURL(remoteURL, branch string) string {
	base := WebURL(remoteURL)
	if base == "" || branch == "" {
		return ""
	}
	host, _ := Parse(remoteURL)
	switch h := CanonicalHost(host); {
	case strings.Contains(h, "github"):
		return base + "/pull/new/" + branchPath(branch)
	case strings.Contains(h, "gitlab"):
		return base + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" + url.QueryEscape(branch)
	case strings.Contains(h, "bitbucket"):
		return base + "/pull-requests/new?source=" + url.QueryEscape(branch)
	}
	return ""
}

// branchPath escapes each part of a branch name for a URL path, keeping
// the slashes of feature/x.
func branchPath(branch string) string {
	parts := strings.Split(branch, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
