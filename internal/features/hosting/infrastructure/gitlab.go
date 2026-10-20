package infrastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// GitLab talks to the GitLab REST API (gitlab.com).
type GitLab struct {
	Version string // sent as the User-Agent
	Client  *http.Client
}

// glBase lets tests point gitswitch at a fake API (the whole address,
// /api/v4 included). Only under tests: a token must never be sent
// anywhere else.
func glBase(base string) string {
	if v := os.Getenv("GITSWITCH_TEST_GITLAB_API"); v != "" && process.NoSystemChanges() {
		return v
	}
	return base
}

func (g *GitLab) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// get reads one GitLab endpoint into out and returns the response headers.
// Failures are explained in plain words.
func (g *GitLab) get(base, token, path string, q url.Values, out any) (http.Header, error) {
	u := glBase(base) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitswitch/"+g.Version)
	res, err := g.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %v. Check your network connection", req.URL.Host, err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errors.New("GitLab refused the token: it is wrong, expired or revoked. Create a new one and sign in again")
	case http.StatusForbidden:
		return nil, errors.New("GitLab refused the token (403): give it the read_api scope (and read_repository, write_repository for git over HTTPS), then sign in again")
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		return nil, fmt.Errorf("GitLab answered %s", res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return nil, errors.New("GitLab's answer could not be read; is this a GitLab server?")
	}
	return res.Header, nil
}

var errNotFound = errors.New("not found")

// Whoami reads GET /user, and the token's scopes and expiry from
// /personal_access_tokens/self when GitLab offers them.
func (g *GitLab) Whoami(base, token string) (hosting.User, hosting.Grant, error) {
	var u struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if _, err := g.get(base, token, "/user", nil, &u); err != nil {
		if errors.Is(err, errNotFound) {
			err = errors.New("GitLab answered 404: is this a GitLab server?")
		}
		return hosting.User{}, hosting.Grant{}, err
	}
	if u.Username == "" {
		return hosting.User{}, hosting.Grant{}, errors.New("GitLab's answer could not be read; is this a GitLab server?")
	}
	grant := hosting.Grant{Scopes: []string{}}
	var t struct {
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if _, err := g.get(base, token, "/personal_access_tokens/self", nil, &t); err == nil { // other token kinds have no such page
		grant.Scopes = append(grant.Scopes, t.Scopes...)
		if t.ExpiresAt != nil {
			grant.Expires = *t.ExpiresAt
		}
	}
	return hosting.User{Login: u.Username, Name: u.Name}, grant, nil
}

// glProject is the part of GitLab's project object GitSwitch reads.
type glProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
	Path              string `json:"path"`
	Namespace         struct {
		FullPath string `json:"full_path"`
	} `json:"namespace"`
	Description   string `json:"description"`
	Visibility    string `json:"visibility"`
	ForkedFrom    any    `json:"forked_from_project"`
	Archived      bool   `json:"archived"`
	DefaultBranch string `json:"default_branch"`
	SSHURL        string `json:"ssh_url_to_repo"`
	HTTPURL       string `json:"http_url_to_repo"`
	WebURL        string `json:"web_url"`
	LastActivity  string `json:"last_activity_at"`
}

// Repos reads GET /projects page by page: every project the user is a
// member of, their groups' included.
func (g *GitLab) Repos(base, token string) ([]hosting.RemoteRepo, error) {
	out := []hosting.RemoteRepo{}
	for page := 1; page > 0 && page <= maxPages; {
		var ps []glProject
		h, err := g.get(base, token, "/projects", url.Values{"membership": {"true"}, "simple": {"false"}, "per_page": {"100"},
			"order_by": {"path"}, "sort": {"asc"}, "page": {strconv.Itoa(page)}}, &ps)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			out = append(out, hosting.RemoteRepo{FullName: p.PathWithNamespace, Owner: p.Namespace.FullPath, Name: p.Path, Description: p.Description,
				Private: p.Visibility != "public", Fork: p.ForkedFrom != nil, Archived: p.Archived, DefaultBranch: p.DefaultBranch,
				SSHURL: p.SSHURL, HTTPSURL: p.HTTPURL, WebURL: p.WebURL, PushedAt: p.LastActivity})
		}
		page, _ = strconv.Atoi(h.Get("X-Next-Page")) // empty on the last page
	}
	return out, nil
}

// glMR is the part of GitLab's merge request object GitSwitch reads.
type glMR struct {
	IID       int    `json:"iid"`
	Title     string `json:"title"`
	WebURL    string `json:"web_url"`
	Draft     bool   `json:"draft"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Author    *struct {
		Username string `json:"username"`
	} `json:"author"`
	References struct {
		Full string `json:"full"` // group/project!7
	} `json:"references"`
	DetailedMergeStatus string `json:"detailed_merge_status"`
	HasConflicts        bool   `json:"has_conflicts"`
	HeadPipeline        *struct {
		Status string `json:"status"`
	} `json:"head_pipeline"`
	SourceProjectID int `json:"source_project_id"`
	TargetProjectID int `json:"target_project_id"`
}

func (m glMR) domain() hosting.PullRequest {
	repo, _, _ := strings.Cut(m.References.Full, "!")
	p := hosting.PullRequest{Kind: hosting.KindGitLab, Repo: repo, Number: m.IID, Title: m.Title, URL: m.WebURL,
		Draft: m.Draft || m.DetailedMergeStatus == "draft_status", CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Author: "ghost"}
	if m.Author != nil {
		p.Author = m.Author.Username
	}
	switch m.DetailedMergeStatus {
	case "requested_changes":
		p.ReviewDecision = "CHANGES_REQUESTED"
	case "not_approved":
		p.ReviewDecision = "REVIEW_REQUIRED"
	}
	switch {
	case m.HasConflicts || m.DetailedMergeStatus == "conflict":
		p.Mergeable = "CONFLICTING"
	case m.DetailedMergeStatus == "checking" || m.DetailedMergeStatus == "unchecked":
		p.Mergeable = "UNKNOWN"
	default:
		p.Mergeable = "MERGEABLE"
	}
	if m.HeadPipeline != nil {
		switch m.HeadPipeline.Status {
		case "success":
			p.Checks = "SUCCESS"
		case "failed":
			p.Checks = "FAILURE"
		case "running", "pending", "created", "preparing", "waiting_for_resource", "scheduled":
			p.Checks = "PENDING"
		}
	}
	if p.Checks == "" {
		switch m.DetailedMergeStatus {
		case "ci_still_running":
			p.Checks = "PENDING"
		case "ci_must_pass":
			p.Checks = "FAILURE"
		}
	}
	p.ReadyToMerge = m.DetailedMergeStatus == "mergeable"
	return p
}

// mrList reads open merge requests, newest activity first, for one filter.
func (g *GitLab) mrList(base, token string, filter url.Values, limit int) ([]hosting.PullRequest, error) {
	q := url.Values{"state": {"opened"}, "scope": {"all"}, "order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(limit)}}
	for k, v := range filter {
		q[k] = v
	}
	var ms []glMR
	if _, err := g.get(base, token, "/merge_requests", q, &ms); err != nil {
		return nil, err
	}
	out := []hosting.PullRequest{}
	for _, m := range ms {
		out = append(out, m.domain())
	}
	return out, nil
}

// Inbox reads three lists of open merge requests: waiting for login's
// review, login's own, and ones login approved (reviewed lately).
func (g *GitLab) Inbox(base, token, login string) (toReview, mine, reviewed []hosting.PullRequest, err error) {
	if toReview, err = g.mrList(base, token, url.Values{"reviewer_username": {login}}, 50); err != nil {
		return nil, nil, nil, err
	}
	if mine, err = g.mrList(base, token, url.Values{"author_username": {login}}, 50); err != nil {
		return nil, nil, nil, err
	}
	all, err := g.mrList(base, token, url.Values{"approved_by_usernames[]": {login}}, 30)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range all {
		if !strings.EqualFold(p.Author, login) {
			reviewed = append(reviewed, p)
		}
	}
	return toReview, mine, reviewed, nil
}

// BranchPR reads the open merge request whose source is branch of the
// project itself (not of a fork): its pipeline, conflicts and approvals.
func (g *GitLab) BranchPR(base, token, repo, branch string) (*hosting.PullRequest, error) {
	proj := "/projects/" + url.PathEscape(repo)
	var ms []glMR
	if _, err := g.get(base, token, proj+"/merge_requests", url.Values{"state": {"opened"}, "source_branch": {branch}, "per_page": {"20"}}, &ms); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil // not visible to this token
		}
		return nil, err
	}
	for _, m := range ms {
		if m.SourceProjectID != m.TargetProjectID {
			continue // from a fork that happens to use the same branch name
		}
		var full glMR // the list has no pipeline; the one merge request does
		if _, err := g.get(base, token, proj+"/merge_requests/"+strconv.Itoa(m.IID), nil, &full); err != nil {
			full = m
		}
		if full.References.Full == "" {
			full.References.Full = repo + "!" + strconv.Itoa(m.IID)
		}
		p := full.domain()
		var ap struct {
			ApprovedBy []struct{} `json:"approved_by"`
		}
		if _, err := g.get(base, token, proj+"/merge_requests/"+strconv.Itoa(m.IID)+"/approvals", nil, &ap); err == nil {
			p.Approvals = len(ap.ApprovedBy)
		}
		return &p, nil
	}
	return nil, nil
}
