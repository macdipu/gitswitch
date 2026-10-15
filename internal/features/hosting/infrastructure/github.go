package infrastructure

import (
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
