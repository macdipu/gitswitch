package infrastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
