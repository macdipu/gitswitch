package infrastructure

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gtServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Gitea) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &Gitea{Version: "test"}
}

func TestGiteaWhoami(t *testing.T) {
	srv, g := gtServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token good" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/user" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"login":"ana","full_name":"Ana"}`))
	})
	u, grant, err := g.Whoami(srv.URL, "good")
	if err != nil || u.Login != "ana" || u.Name != "Ana" || len(grant.Scopes) != 0 {
		t.Fatalf("Whoami = %+v %+v %v", u, grant, err)
	}
	if _, _, err := g.Whoami(srv.URL, "bad"); err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Errorf("a refused token: %v", err)
	}
}

func TestGiteaRepos(t *testing.T) {
	var srv *httptest.Server
	var pages []string
	srv, g := gtServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			w.WriteHeader(404)
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<`+srv.URL+`/user/repos?limit=50&page=2>; rel="next"`)
			_, _ = w.Write([]byte(`[{"full_name":"ana/notes","name":"notes","owner":{"login":"ana"},"description":"Notes","private":true,"language":"Go","default_branch":"main",
			  "ssh_url":"git@codeberg.org:ana/notes.git","clone_url":"https://codeberg.org/ana/notes.git","html_url":"https://codeberg.org/ana/notes","updated_at":"2026-10-01T00:00:00Z"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"full_name":"acme/site","name":"site","owner":{"login":"acme"},"fork":true,"archived":true}]`))
	})
	rs, err := g.Repos(srv.URL, "tok")
	if err != nil || len(rs) != 2 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("Repos = %+v, %v (pages %v)", rs, err, pages)
	}
	if a := rs[0]; a.FullName != "ana/notes" || a.Owner != "ana" || !a.Private || a.Language != "Go" || a.SSHURL == "" || a.HTTPSURL == "" || a.PushedAt == "" {
		t.Errorf("first: %+v", a)
	}
	if b := rs[1]; !b.Fork || !b.Archived {
		t.Errorf("second: %+v", b)
	}
}

// giteaFake answers the pull request calls the way Gitea does.
func giteaFake(t *testing.T) (*httptest.Server, *Gitea) {
	return gtServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch p := r.URL.Path; {
		case p == "/repos/issues/search":
			if q.Get("type") != "pulls" || q.Get("state") != "open" {
				w.WriteHeader(400)
				return
			}
			switch {
			case q.Get("review_requested") == "true":
				_, _ = w.Write([]byte(`[{"number":7,"title":"Add billing","html_url":"https://codeberg.org/acme/api/pulls/7","user":{"login":"naser"},"repository":{"full_name":"acme/api"},"pull_request":{"draft":false}},
				  {"number":1,"title":"An issue","repository":{"full_name":"acme/api"}}]`))
			case q.Get("created") == "true":
				_, _ = w.Write([]byte(`[{"number":3,"title":"Fix login","html_url":"https://codeberg.org/acme/web/pulls/3","user":{"login":"ana"},"repository":{"full_name":"acme/web"},"pull_request":{"draft":false}},
				  {"number":5,"title":"WIP","user":{"login":"ana"},"repository":{"full_name":"acme/web"},"pull_request":{"draft":true}}]`))
			case q.Get("reviewed") == "true":
				_, _ = w.Write([]byte(`[{"number":9,"title":"Theirs","user":{"login":"naser"},"repository":{"full_name":"acme/api"},"pull_request":{}},
				  {"number":3,"title":"Fix login","user":{"login":"ana"},"repository":{"full_name":"acme/web"},"pull_request":{}}]`))
			default:
				w.WriteHeader(400)
			}
		case p == "/repos/acme/web/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"mergeable":false,"head":{"sha":"abc"}}`))
		case p == "/repos/acme/web/pulls/3/reviews":
			_, _ = w.Write([]byte(`[{"state":"APPROVED","user":{"login":"naser"}},{"state":"REQUEST_CHANGES","user":{"login":"naser"}},{"state":"COMMENT","user":{"login":"sam"}}]`))
		case p == "/repos/acme/web/commits/abc/status":
			_, _ = w.Write([]byte(`{"state":"failure","total_count":2}`))
		case p == "/repos/acme/web/pulls/5":
			_, _ = w.Write([]byte(`{"number":5,"mergeable":true,"draft":true,"head":{"sha":"def"}}`))
		case p == "/repos/acme/web/pulls/5/reviews":
			_, _ = w.Write([]byte(`[]`))
		case p == "/repos/acme/web/commits/def/status":
			_, _ = w.Write([]byte(`{"state":"pending","total_count":0}`)) // no checks at all
		case p == "/repos/acme/api/pulls" && q.Get("state") == "open":
			_, _ = w.Write([]byte(`[
			  {"number":9,"title":"From a fork","head":{"ref":"billing","sha":"f00","repo":{"full_name":"fork/api"}}},
			  {"number":8,"title":"Other branch","head":{"ref":"other","sha":"f01","repo":{"full_name":"acme/api"}}},
			  {"number":42,"title":"Billing","html_url":"https://codeberg.org/acme/api/pulls/42","user":{"login":"ana"},"mergeable":true,"head":{"ref":"billing","sha":"cafe","repo":{"full_name":"Acme/API"}}}]`))
		case p == "/repos/acme/api/pulls/42/reviews":
			_, _ = w.Write([]byte(`[{"state":"APPROVED","user":{"login":"naser"}},{"state":"APPROVED","user":{"login":"sam"}},{"state":"APPROVED","dismissed":true,"user":{"login":"old"}}]`))
		case p == "/repos/acme/api/commits/cafe/status":
			_, _ = w.Write([]byte(`{"state":"success","total_count":1}`))
		default:
			w.WriteHeader(404)
		}
	})
}

func TestGiteaInbox(t *testing.T) {
	srv, g := giteaFake(t)
	tr, mine, rv, err := g.Inbox(srv.URL, "tok", "ana")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != 1 || tr[0].Repo != "acme/api" || tr[0].Number != 7 || tr[0].Author != "naser" || tr[0].ReviewDecision != "REVIEW_REQUIRED" || tr[0].Kind != "gitea" {
		t.Errorf("to review (an issue is not a pull request): %+v", tr)
	}
	if len(mine) != 2 {
		t.Fatalf("mine: %+v", mine)
	}
	if m := mine[0]; m.Mergeable != "CONFLICTING" || m.Checks != "FAILURE" || m.ReviewDecision != "CHANGES_REQUESTED" || m.Approvals != 0 {
		t.Errorf("a conflict, failing checks, a reviewer who changed their mind to request changes: %+v", m)
	}
	if m := mine[1]; !m.Draft || m.Mergeable != "MERGEABLE" || m.Checks != "" {
		t.Errorf("a draft without checks: %+v", m)
	}
	if len(rv) != 1 || rv[0].Number != 9 {
		t.Errorf("reviewed lately leaves out your own: %+v", rv)
	}
}

func TestGiteaBranchPR(t *testing.T) {
	srv, g := giteaFake(t)
	p, err := g.BranchPR(srv.URL, "tok", "acme/api", "billing")
	if err != nil || p == nil || p.Number != 42 || p.Approvals != 2 || p.Checks != "SUCCESS" || p.ReviewDecision != "APPROVED" || p.Mergeable != "MERGEABLE" || p.Author != "ana" {
		t.Fatalf("BranchPR = %+v, %v (a fork's pull request, a dismissed review and another branch must not count)", p, err)
	}
	if p, err := g.BranchPR(srv.URL, "tok", "acme/api", "none"); p != nil || err != nil {
		t.Errorf("no pull request: %+v %v", p, err)
	}
	if p, err := g.BranchPR(srv.URL, "tok", "acme/hidden", "x"); p != nil || err != nil {
		t.Errorf("a repo the token cannot see: %+v %v", p, err)
	}
}
