package infrastructure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// GitHub talks to the GitHub REST API (github.com or GitHub Enterprise).
type GitHub struct {
	Version string // sent as the User-Agent
	Client  *http.Client
}

// baseFor lets tests point gitswitch at a fake API. Only under tests: a
// token must never be sent anywhere else.
func baseFor(base string) string {
	if v := os.Getenv("GITSWITCH_TEST_GITHUB_API"); v != "" && process.NoSystemChanges() {
		return v
	}
	return base
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Whoami reads GET /user with the token.
func (g *GitHub) Whoami(base, token string) (hosting.User, hosting.Grant, error) {
	req, err := http.NewRequest("GET", baseFor(base)+"/user", nil)
	if err != nil {
		return hosting.User{}, hosting.Grant{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "gitswitch/"+g.Version)
	res, err := g.client().Do(req)
	if err != nil {
		return hosting.User{}, hosting.Grant{}, fmt.Errorf("could not reach %s: %v. Check your network connection", req.URL.Host, err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return hosting.User{}, hosting.Grant{}, errors.New("GitHub refused the token: it is wrong, expired or revoked. Create a new one and sign in again")
	case http.StatusForbidden:
		return hosting.User{}, hosting.Grant{}, errors.New("GitHub refused the token (403): your organization may require approving it, or you hit the rate limit. Try again later")
	default:
		return hosting.User{}, hosting.Grant{}, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var u hosting.User
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil || u.Login == "" {
		return hosting.User{}, hosting.Grant{}, errors.New("GitHub's answer could not be read; is this a GitHub server?")
	}
	g2 := hosting.Grant{Scopes: []string{}, Expires: res.Header.Get("GitHub-Authentication-Token-Expiration")}
	for _, s := range strings.Split(res.Header.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			g2.Scopes = append(g2.Scopes, s)
		}
	}
	return u, g2, nil
}

// maxPages caps how many pages of 100 repos are read for one account.
const maxPages = 30

// ghRepo is the part of GitHub's repo object GitSwitch reads.
type ghRepo struct {
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
	PushedAt      string                 `json:"pushed_at"`
}

// Repos reads GET /user/repos page by page, following the Link header.
func (g *GitHub) Repos(base, token string) ([]hosting.RemoteRepo, error) {
	next := baseFor(base) + "/user/repos?per_page=100&sort=full_name&affiliation=owner%2Ccollaborator%2Corganization_member"
	out := []hosting.RemoteRepo{}
	for page := 0; next != "" && page < maxPages; page++ {
		req, err := http.NewRequest("GET", next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "gitswitch/"+g.Version)
		res, err := g.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("could not reach %s: %v. Check your network connection", req.URL.Host, err)
		}
		var rs []ghRepo
		err = json.NewDecoder(res.Body).Decode(&rs)
		res.Body.Close()
		switch {
		case res.StatusCode == http.StatusUnauthorized:
			return nil, errors.New("GitHub refused the token: it is wrong, expired or revoked. Sign in again")
		case res.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("GitHub answered %s", res.Status)
		case err != nil:
			return nil, errors.New("GitHub's answer could not be read")
		}
		for _, r := range rs {
			out = append(out, hosting.RemoteRepo{FullName: r.FullName, Owner: r.Owner.Login, Name: r.Name, Description: r.Description,
				Private: r.Private, Fork: r.Fork, Archived: r.Archived, Language: r.Language, DefaultBranch: r.DefaultBranch,
				SSHURL: r.SSHURL, HTTPSURL: r.CloneURL, WebURL: r.HTMLURL, PushedAt: r.PushedAt})
		}
		next = nextLink(res.Header.Get("Link"))
	}
	return out, nil
}

// nextRe finds rel="next" in a Link header. The URLs may hold commas
// (affiliation=owner,collaborator), so the header is not split on them.
var nextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

func nextLink(h string) string {
	if m := nextRe.FindStringSubmatch(h); m != nil {
		return m[1]
	}
	return ""
}

// prFields are the pull request fields the inbox reads.
const prFields = `... on PullRequest { number title url isDraft createdAt updatedAt author { login }
  repository { nameWithOwner } reviewDecision mergeable
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } }`

// inboxQuery reads the three inbox searches in one request.
const inboxQuery = `query($toReview: String!, $mine: String!, $reviewed: String!) {
  toReview: search(query: $toReview, type: ISSUE, first: 50) { nodes { ` + prFields + ` } }
  mine: search(query: $mine, type: ISSUE, first: 50) { nodes { ` + prFields + ` } }
  reviewed: search(query: $reviewed, type: ISSUE, first: 30) { nodes { ` + prFields + ` } }
}`

type ghPR struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	IsDraft   bool   `json:"isDraft"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Author    *struct {
		Login string `json:"login"`
	} `json:"author"` // null for a deleted user
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReviewDecision string `json:"reviewDecision"`
	Mergeable      string `json:"mergeable"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (p ghPR) domain() hosting.PullRequest {
	out := hosting.PullRequest{Repo: p.Repository.NameWithOwner, Number: p.Number, Title: p.Title, URL: p.URL, Draft: p.IsDraft,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, ReviewDecision: p.ReviewDecision, Mergeable: p.Mergeable}
	if p.Author != nil {
		out.Author = p.Author.Login
	} else {
		out.Author = "ghost"
	}
	if n := p.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
		out.Checks = n[0].Commit.StatusCheckRollup.State
	}
	return out
}

// Inbox runs the three inbox searches in one GraphQL request.
func (g *GitHub) Inbox(base, token, login string) (toReview, mine, reviewed []hosting.PullRequest, err error) {
	q1, q2, q3 := hosting.InboxQueries(login)
	body, _ := json.Marshal(map[string]any{"query": inboxQuery, "variables": map[string]string{"toReview": q1, "mine": q2, "reviewed": q3}})
	req, err := http.NewRequest("POST", hosting.GraphQLURL(baseFor(base)), bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gitswitch/"+g.Version)
	res, err := g.client().Do(req)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not reach %s: %v. Check your network connection", req.URL.Host, err)
	}
	defer res.Body.Close()
	var r struct {
		Data map[string]struct {
			Nodes []ghPR `json:"nodes"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	decErr := json.NewDecoder(res.Body).Decode(&r)
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return nil, nil, nil, errors.New("GitHub refused the token: it is wrong, expired or revoked. Sign in again")
	case res.StatusCode != http.StatusOK:
		return nil, nil, nil, fmt.Errorf("GitHub answered %s", res.Status)
	case decErr != nil:
		return nil, nil, nil, errors.New("GitHub's answer could not be read")
	}
	if len(r.Errors) > 0 && r.Data == nil {
		msg := r.Errors[0].Message
		if r.Errors[0].Type == "FORBIDDEN" || strings.Contains(msg, "not accessible") {
			return nil, nil, nil, errors.New("the token cannot read pull requests: give it \"Pull requests: read\" on GitHub (Settings → Developer settings → Personal access tokens), then sign in again")
		}
		return nil, nil, nil, fmt.Errorf("GitHub: %s", msg)
	}
	conv := func(key string) []hosting.PullRequest {
		out := []hosting.PullRequest{}
		for _, n := range r.Data[key].Nodes {
			if n.Number != 0 { // search hits that are not pull requests come back empty
				out = append(out, n.domain())
			}
		}
		return out
	}
	return conv("toReview"), conv("mine"), conv("reviewed"), nil
}
