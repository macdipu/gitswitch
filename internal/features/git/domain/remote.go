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
	// scp-like: git@host:owner/repo.git
	if i := strings.Index(u, ":"); i > 0 && !strings.Contains(u[:i], "/") {
		h := u[:i]
		if j := strings.LastIndex(h, "@"); j >= 0 {
			h = h[j+1:]
		}
		return strings.ToLower(h), strings.Trim(u[i+1:], "/")
	}
	return
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
	if host, p := Parse(u); host == "" || p == "" {
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
