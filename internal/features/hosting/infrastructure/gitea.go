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
	"sync"
	"time"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Gitea talks to the REST API of Gitea and Forgejo (codeberg.org).
type Gitea struct {
	Version string // sent as the User-Agent
	Client  *http.Client
}

// gtBase lets tests point gitswitch at a fake API (the whole address,
// /api/v1 included). Only under tests: a token must never be sent
// anywhere else.
func gtBase(base string) string {
	if v := os.Getenv("GITSWITCH_TEST_GITEA_API"); v != "" && process.NoSystemChanges() {
		return v
	}
	return base
}

func (g *Gitea) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// get reads one endpoint (a path under the API address, or a full URL
// from a Link header) into out and returns the response headers.
func (g *Gitea) get(base, token, path string, q url.Values, out any) (http.Header, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = gtBase(base) + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
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
		return nil, errors.New("the host refused the token: it is wrong, expired or revoked. Create a new one and sign in again")
	case http.StatusForbidden:
		return nil, errors.New("the host refused the token (403): give it read access to user, repository and issue, then sign in again")
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		return nil, fmt.Errorf("the host answered %s", res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return nil, errors.New("the host's answer could not be read; is this a Gitea or Forgejo server?")
	}
	return res.Header, nil
}

// Whoami reads GET /user. Gitea does not say a token's scopes or expiry.
func (g *Gitea) Whoami(base, token string) (hosting.User, hosting.Grant, error) {
	var u struct {
		Login    string `json:"login"`
		FullName string `json:"full_name"`
	}
	if _, err := g.get(base, token, "/user", nil, &u); err != nil {
		if errors.Is(err, errNotFound) {
			err = errors.New("the host answered 404: is this a Gitea or Forgejo server?")
		}
		return hosting.User{}, hosting.Grant{}, err
	}
	if u.Login == "" {
		return hosting.User{}, hosting.Grant{}, errors.New("the host's answer could not be read; is this a Gitea or Forgejo server?")
	}
	return hosting.User{Login: u.Login, Name: u.FullName}, hosting.Grant{Scopes: []string{}}, nil
}

type gtRepo struct {
	FullName      string                 `json:"full_name"`
	Name          string                 `json:"name"`
	Owner         struct{ Login string } `json:"owner"`
	Description   string                 `json:"description"`
	Private       bool                   `json:"private"`
	Fork          bool                   `json:"fork"`
	Archived      bool                   `json:"archived"`
	Language      string                 `json:"language"`
	DefaultBranch string                 `json:"default_branch"`
	SSHURL        string                 `json:"ssh_url"`
	CloneURL      string                 `json:"clone_url"`
	HTMLURL       string                 `json:"html_url"`
	UpdatedAt     string                 `json:"updated_at"`
}

// Repos reads GET /user/repos page by page, following the Link header.
func (g *Gitea) Repos(base, token string) ([]hosting.RemoteRepo, error) {
	out := []hosting.RemoteRepo{}
	path, q := "/user/repos", url.Values{"limit": {"50"}, "page": {"1"}}
	for page := 0; path != "" && page < maxPages; page++ {
		var rs []gtRepo
		h, err := g.get(base, token, path, q, &rs)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, hosting.RemoteRepo{FullName: r.FullName, Owner: r.Owner.Login, Name: r.Name, Description: r.Description,
				Private: r.Private, Fork: r.Fork, Archived: r.Archived, Language: r.Language, DefaultBranch: r.DefaultBranch,
				SSHURL: r.SSHURL, HTTPSURL: r.CloneURL, WebURL: r.HTMLURL, PushedAt: r.UpdatedAt})
		}
		path, q = nextLink(h.Get("Link")), nil
	}
	return out, nil
}

type gtPull struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	HTMLURL   string `json:"html_url"`
	Draft     bool   `json:"draft"`
	Mergeable *bool  `json:"mergeable"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	User      *struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref  string `json:"ref"`
		Sha  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

func (p gtPull) domain(repo string) hosting.PullRequest {
	out := hosting.PullRequest{Kind: hosting.KindGitea, Repo: repo, Number: p.Number, Title: p.Title, URL: p.HTMLURL, Draft: p.Draft,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Author: "ghost"}
	if p.User != nil {
		out.Author = p.User.Login
	}
	switch {
	case p.Mergeable == nil:
	case *p.Mergeable:
		out.Mergeable = "MERGEABLE"
	default:
		out.Mergeable = "CONFLICTING"
	}
	return out
}

// details adds what a pull request's own page knows and a search does not:
// whether it merges, the verdict of its reviewers, and its checks. A call
// that fails leaves that part unknown.
func (g *Gitea) details(base, token string, p *hosting.PullRequest, sha string) {
	repo, n := p.Repo, strconv.Itoa(p.Number)
	var reviews []struct {
		State     string `json:"state"`
		Dismissed bool   `json:"dismissed"`
		User      *struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if _, err := g.get(base, token, "/repos/"+repo+"/pulls/"+n+"/reviews", nil, &reviews); err == nil {
		latest := map[string]string{} // reviewer -> last opinion
		for _, r := range reviews {
			if r.User == nil || r.Dismissed || (r.State != "APPROVED" && r.State != "REQUEST_CHANGES") {
				continue
			}
			latest[r.User.Login] = r.State
		}
		changes := false
		for _, st := range latest {
			switch st {
			case "APPROVED":
				p.Approvals++
			case "REQUEST_CHANGES":
				changes = true
			}
		}
		switch {
		case changes:
			p.ReviewDecision = "CHANGES_REQUESTED"
		case p.Approvals > 0:
			p.ReviewDecision = "APPROVED"
		}
	}
	if sha == "" {
		return
	}
	var st struct {
		State      string `json:"state"`
		TotalCount int    `json:"total_count"`
	}
	if _, err := g.get(base, token, "/repos/"+repo+"/commits/"+url.PathEscape(sha)+"/status", nil, &st); err == nil && st.TotalCount > 0 {
		switch st.State {
		case "success":
			p.Checks = "SUCCESS"
		case "failure", "error":
			p.Checks = "FAILURE"
		case "pending":
			p.Checks = "PENDING"
		}
	}
}

// search reads open pull requests for the signed-in user with one filter
// (review_requested, created or reviewed), newest activity first.
func (g *Gitea) search(base, token, filter string, limit int) ([]hosting.PullRequest, error) {
	var issues []struct {
		Number     int    `json:"number"`
		Title      string `json:"title"`
		HTMLURL    string `json:"html_url"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
		User       *struct{ Login string }
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		PullRequest *struct {
			Draft bool `json:"draft"`
		} `json:"pull_request"`
	}
	if _, err := g.get(base, token, "/repos/issues/search", url.Values{"type": {"pulls"}, "state": {"open"}, filter: {"true"}, "limit": {strconv.Itoa(limit)}}, &issues); err != nil {
		return nil, err
	}
	out := []hosting.PullRequest{}
	for _, i := range issues {
		if i.PullRequest == nil {
			continue
		}
		p := hosting.PullRequest{Kind: hosting.KindGitea, Repo: i.Repository.FullName, Number: i.Number, Title: i.Title, URL: i.HTMLURL,
			Draft: i.PullRequest.Draft, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, Author: "ghost"}
		if i.User != nil {
			p.Author = i.User.Login
		}
		out = append(out, p)
	}
	return out, nil
}

// detailLimit is how many of your own pull requests get their reviews and
// checks read (a few calls each); the rest show without them.
const detailLimit = 10

// Inbox reads three lists of open pull requests: waiting for the user's
// review, the user's own (with reviews and checks for the newest ones), and
// ones the user reviewed.
func (g *Gitea) Inbox(base, token, login string) (toReview, mine, reviewed []hosting.PullRequest, err error) {
	if toReview, err = g.search(base, token, "review_requested", 50); err != nil {
		return nil, nil, nil, err
	}
	for i := range toReview {
		toReview[i].ReviewDecision = "REVIEW_REQUIRED"
	}
	if mine, err = g.search(base, token, "created", 50); err != nil {
		return nil, nil, nil, err
	}
	all, err := g.search(base, token, "reviewed", 30)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range all {
		if !strings.EqualFold(p.Author, login) {
			reviewed = append(reviewed, p)
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i := range mine {
		if i >= detailLimit {
			break
		}
		wg.Add(1)
		go func(p *hosting.PullRequest) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var full gtPull
			if _, err := g.get(base, token, "/repos/"+p.Repo+"/pulls/"+strconv.Itoa(p.Number), nil, &full); err != nil {
				return
			}
			d := full.domain(p.Repo)
			p.Mergeable, p.Draft = d.Mergeable, p.Draft || d.Draft
			g.details(base, token, p, full.Head.Sha)
		}(&mine[i])
	}
	wg.Wait()
	return toReview, mine, reviewed, nil
}

// BranchPR reads the open pull request whose head is branch of the repo
// itself (not of a fork), with its reviews and checks.
func (g *Gitea) BranchPR(base, token, repo, branch string) (*hosting.PullRequest, error) {
	var ps []gtPull
	if _, err := g.get(base, token, "/repos/"+repo+"/pulls", url.Values{"state": {"open"}, "limit": {"50"}, "sort": {"recentupdate"}}, &ps); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil // not visible to this token
		}
		return nil, err
	}
	for _, pr := range ps {
		if pr.Head.Ref != branch || pr.Head.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, repo) {
			continue // another branch, or a fork that happens to use the same name
		}
		p := pr.domain(repo)
		g.details(base, token, &p, pr.Head.Sha)
		return &p, nil
	}
	return nil, nil
}
