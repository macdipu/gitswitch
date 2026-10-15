package infrastructure

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

func TestWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("User-Agent") != "gitswitch/test" {
			http.Error(w, "bad request", 400)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
			w.Header().Set("GitHub-Authentication-Token-Expiration", "2027-01-01 00:00:00 UTC")
			_, _ = w.Write([]byte(`{"login":"dipu","name":"Dipu"}`))
		case "Bearer sso":
			w.WriteHeader(403)
		case "Bearer odd":
			_, _ = w.Write([]byte(`<html>`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()
	g := &GitHub{Version: "test"}
	u, gr, err := g.Whoami(srv.URL, "good")
	if err != nil || u.Login != "dipu" || strings.Join(gr.Scopes, ",") != "repo,read:org" || !strings.HasPrefix(gr.Expires, "2027") {
		t.Fatalf("Whoami = %+v %+v %v", u, gr, err)
	}
	for tok, want := range map[string]string{"bad": "wrong, expired or revoked", "sso": "403", "odd": "could not be read"} {
		if _, _, err := g.Whoami(srv.URL, tok); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("token %s: %v, want %q", tok, err, want)
		}
	}
}

func TestFileSecretsAndTokens(t *testing.T) {
	f := &fileSecrets{path: t.TempDir() + "/tokens.json"}
	if _, err := f.Get("work"); err != hosting.ErrNoToken {
		t.Fatalf("empty: %v", err)
	}
	if err := f.Set("Work", "tok"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get("work"); err != nil || got != "tok" {
		t.Fatalf("ids match without case: %q %v", got, err)
	}
	tk := Tokens{Secrets: f}
	if err := tk.Move("work", "job"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("work"); err != hosting.ErrNoToken {
		t.Errorf("the old id must be gone: %v", err)
	}
	if got, _ := f.Get("job"); got != "tok" {
		t.Errorf("moved token = %q", got)
	}
	if err := tk.Move("nobody", "x"); err != nil {
		t.Errorf("moving no token is fine: %v", err)
	}
	if err := tk.Delete("job"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("job"); err != hosting.ErrNoToken {
		t.Errorf("deleted: %v", err)
	}
}

func TestReposPages(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", `<`+srv.URL+`/user/repos?page=2>; rel="next", <`+srv.URL+`/user/repos?page=2>; rel="last"`)
			_, _ = w.Write([]byte(`[{"full_name":"acme/api","name":"api","owner":{"login":"acme"},"private":true,"language":"Go","ssh_url":"git@github.com:acme/api.git"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"full_name":"dipu/notes","name":"notes","owner":{"login":"dipu"}}]`))
	}))
	defer srv.Close()
	g := &GitHub{Version: "test"}
	rs, err := g.Repos(srv.URL, "good")
	if err != nil || len(rs) != 2 || rs[0].Owner != "acme" || !rs[0].Private || rs[1].FullName != "dipu/notes" {
		t.Fatalf("Repos = %+v, %v", rs, err)
	}
	if _, err := g.Repos(srv.URL, "bad"); err == nil || !strings.Contains(err.Error(), "Sign in again") {
		t.Errorf("a refused token: %v", err)
	}
}

func TestCache(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	if _, ok := c.Load("work"); ok {
		t.Fatal("empty cache")
	}
	if err := c.Save("Work", hosting.RepoList{Repos: []hosting.RemoteRepo{{FullName: "a/b"}}}); err != nil {
		t.Fatal(err)
	}
	if l, ok := c.Load("work"); !ok || len(l.Repos) != 1 {
		t.Fatalf("Load = %+v %v", l, ok)
	}
	if err := c.Delete("work"); err != nil || c.Delete("work") != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestNextLink(t *testing.T) {
	h := `<https://api.github.com/user/repos?affiliation=owner,collaborator&page=2>; rel="next", <https://api.github.com/user/repos?page=9>; rel="last"`
	if got := nextLink(h); got != "https://api.github.com/user/repos?affiliation=owner,collaborator&page=2" {
		t.Errorf("nextLink = %q", got)
	}
	if nextLink(`<https://x/?page=1>; rel="prev"`) != "" || nextLink("") != "" {
		t.Error("no next link")
	}
}

func TestInbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" || r.Method != "POST" {
			w.WriteHeader(404)
			return
		}
		var body struct{ Variables map[string]string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.Header.Get("Authorization") {
		case "Bearer nopr":
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by personal access token"}]}`))
		case "Bearer good":
			if !strings.Contains(body.Variables["toReview"], "review-requested:dipu") {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"data":{
			  "toReview":{"nodes":[{"number":7,"title":"Add billing","url":"https://github.com/acme/api/pull/7","author":{"login":"naser"},"repository":{"nameWithOwner":"acme/api"},"reviewDecision":"REVIEW_REQUIRED","mergeable":"MERGEABLE","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}}, {}]},
			  "mine":{"nodes":[{"number":3,"title":"Fix login","author":null,"repository":{"nameWithOwner":"acme/web"},"isDraft":true,"mergeable":"CONFLICTING","commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}}]},
			  "reviewed":{"nodes":[]}}}`))
		default:
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()
	g := &GitHub{Version: "test"}
	tr, mine, rv, err := g.Inbox(srv.URL, "good", "dipu")
	if err != nil || len(tr) != 1 || tr[0].Checks != "SUCCESS" || tr[0].Author != "naser" || tr[0].Repo != "acme/api" {
		t.Fatalf("toReview = %+v, %v", tr, err)
	}
	if len(mine) != 1 || !mine[0].Draft || mine[0].Author != "ghost" || mine[0].Checks != "" || mine[0].Mergeable != "CONFLICTING" || len(rv) != 0 {
		t.Fatalf("mine = %+v, reviewed = %+v", mine, rv)
	}
	if _, _, _, err := g.Inbox(srv.URL, "nopr", "dipu"); err == nil || !strings.Contains(err.Error(), "Pull requests: read") {
		t.Errorf("a token without PR access: %v", err)
	}
	if _, _, _, err := g.Inbox(srv.URL, "bad", "dipu"); err == nil || !strings.Contains(err.Error(), "Sign in again") {
		t.Errorf("a refused token: %v", err)
	}
}

func TestInboxCache(t *testing.T) {
	c := Cache{Dir: t.TempDir()}
	if _, ok := c.LoadInbox("work"); ok {
		t.Fatal("empty")
	}
	must(t, c.SaveInbox("Work", hosting.Inbox{Login: "dipu", Mine: []hosting.PullRequest{{Number: 1}}}))
	if in, ok := c.LoadInbox("work"); !ok || in.Login != "dipu" || len(in.Mine) != 1 {
		t.Fatalf("LoadInbox = %+v %v", in, ok)
	}
	if _, ok := c.Load("work"); ok {
		t.Error("an inbox is not a repo list")
	}
	must(t, c.DeleteInbox("work"))
	if _, ok := c.LoadInbox("work"); ok {
		t.Error("deleted")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
