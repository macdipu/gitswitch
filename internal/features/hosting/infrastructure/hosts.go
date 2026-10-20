package infrastructure

import (
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// Hosts is the hosting API for every kind of host: it hands each call to
// the client for the kind of host it is for. It implements the hosting
// domain's API.
type Hosts struct {
	GitHub *GitHub
	GitLab *GitLab
	Gitea  *Gitea
}

// NewHosts makes the clients for every supported kind of host.
func NewHosts(version string) *Hosts {
	return &Hosts{GitHub: &GitHub{Version: version}, GitLab: &GitLab{Version: version}, Gitea: &Gitea{Version: version}}
}

func (h *Hosts) Whoami(kind hosting.Kind, base, token string) (hosting.User, hosting.Grant, error) {
	switch kind {
	case hosting.KindGitLab:
		return h.GitLab.Whoami(base, token)
	case hosting.KindGitea:
		return h.Gitea.Whoami(base, token)
	}
	return h.GitHub.Whoami(base, token)
}

func (h *Hosts) Repos(kind hosting.Kind, base, token string) ([]hosting.RemoteRepo, error) {
	switch kind {
	case hosting.KindGitLab:
		return h.GitLab.Repos(base, token)
	case hosting.KindGitea:
		return h.Gitea.Repos(base, token)
	}
	return h.GitHub.Repos(base, token)
}

func (h *Hosts) Inbox(kind hosting.Kind, base, token, login string) (toReview, mine, reviewed []hosting.PullRequest, err error) {
	switch kind {
	case hosting.KindGitLab:
		return h.GitLab.Inbox(base, token, login)
	case hosting.KindGitea:
		return h.Gitea.Inbox(base, token, login)
	}
	return h.GitHub.Inbox(base, token, login)
}

func (h *Hosts) BranchPR(kind hosting.Kind, base, token, repo, branch string) (*hosting.PullRequest, error) {
	switch kind {
	case hosting.KindGitLab:
		return h.GitLab.BranchPR(base, token, repo, branch)
	case hosting.KindGitea:
		return h.Gitea.BranchPR(base, token, repo, branch)
	}
	return h.GitHub.BranchPR(base, token, repo, branch)
}
