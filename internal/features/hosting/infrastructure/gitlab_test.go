package infrastructure

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func glServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *GitLab) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, &GitLab{Version: "test"}
}

func TestGitLabWhoami(t *testing.T) {
	srv, g := glServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" && r.Header.Get("Authorization") != "Bearer plain" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"username":"ana","name":"Ana"}`))
		case "/personal_access_tokens/self":
			if r.Header.Get("Authorization") == "Bearer plain" {
				w.WriteHeader(404) // a token kind with no such page
				return
			}
			_, _ = w.Write([]byte(`{"scopes":["read_api","read_repository"],"expires_at":"2027-01-01"}`))
		default:
			w.WriteHeader(404)
		}
	})
	u, grant, err := g.Whoami(srv.URL, "good")
	if err != nil || u.Login != "ana" || u.Name != "Ana" || grant.Expires != "2027-01-01" || strings.Join(grant.Scopes, ",") != "read_api,read_repository" {
		t.Fatalf("Whoami = %+v %+v %v", u, grant, err)
	}
	if u, grant, err := g.Whoami(srv.URL, "plain"); err != nil || u.Login != "ana" || len(grant.Scopes) != 0 || grant.Expires != "" {
		t.Errorf("a token without a scopes page still signs in: %+v %+v %v", u, grant, err)
	}
	if _, _, err := g.Whoami(srv.URL, "bad"); err == nil || !strings.Contains(err.Error(), "GitLab refused the token") {
		t.Errorf("a refused token: %v", err)
	}
	srv403, g403 := glServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	if _, _, err := g403.Whoami(srv403.URL, "x"); err == nil || !strings.Contains(err.Error(), "read_api") {
		t.Errorf("a token without read_api: %v", err)
	}
}

func TestGitLabRepos(t *testing.T) {
	var pages []string
	srv, g := glServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects" || r.URL.Query().Get("membership") != "true" {
			w.WriteHeader(404)
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"path_with_namespace":"acme/team/api","path":"api","namespace":{"full_path":"acme/team"},"description":"The API","visibility":"private",
			  "default_branch":"main","ssh_url_to_repo":"git@gitlab.com:acme/team/api.git","http_url_to_repo":"https://gitlab.com/acme/team/api.git","web_url":"https://gitlab.com/acme/team/api","last_activity_at":"2026-10-01T00:00:00Z"}]`))
			return
		}
		w.Header().Set("X-Next-Page", "")
		_, _ = w.Write([]byte(`[{"path_with_namespace":"ana/notes","path":"notes","namespace":{"full_path":"ana"},"visibility":"public","archived":true,"forked_from_project":{"id":1}},
		  {"path_with_namespace":"acme/wiki","path":"wiki","namespace":{"full_path":"acme"},"visibility":"internal"}]`))
	})
	rs, err := g.Repos(srv.URL, "tok")
	if err != nil || len(rs) != 3 || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("Repos = %+v, %v (pages %v)", rs, err, pages)
	}
	a := rs[0]
	if a.FullName != "acme/team/api" || a.Owner != "acme/team" || a.Name != "api" || !a.Private || a.DefaultBranch != "main" || a.SSHURL == "" || a.HTTPSURL == "" || a.WebURL == "" || a.PushedAt == "" {
		t.Errorf("first repo: %+v", a)
	}
	if n := rs[1]; n.Private || !n.Archived || !n.Fork {
		t.Errorf("a public archived fork: %+v", n)
	}
	if !rs[2].Private {
		t.Errorf("internal is not public: %+v", rs[2])
	}
}

func TestGitLabInbox(t *testing.T) {
	var seen []string
	srv, g := glServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/merge_requests" || r.URL.Query().Get("state") != "opened" || r.URL.Query().Get("scope") != "all" {
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("reviewer_username") == "ana":
			seen = append(seen, "reviewer")
			_, _ = w.Write([]byte(`[{"iid":7,"title":"Add billing","web_url":"https://gitlab.com/acme/api/-/merge_requests/7","author":{"username":"naser"},
			  "references":{"full":"acme/api!7"},"detailed_merge_status":"not_approved","updated_at":"2026-10-19T09:00:00Z"}]`))
		case q.Get("author_username") == "ana":
			seen = append(seen, "author")
			_, _ = w.Write([]byte(`[
			  {"iid":3,"title":"Fix login","author":{"username":"ana"},"references":{"full":"acme/team/web!3"},"detailed_merge_status":"conflict","has_conflicts":true,"head_pipeline":{"status":"failed"}},
			  {"iid":4,"title":"Ship it","author":{"username":"ana"},"references":{"full":"acme/api!4"},"detailed_merge_status":"mergeable","head_pipeline":{"status":"success"}},
			  {"iid":5,"title":"Draft: dark mode","draft":true,"author":{"username":"ana"},"references":{"full":"acme/web!5"},"detailed_merge_status":"draft_status"},
			  {"iid":6,"title":"Running","author":{"username":"ana"},"references":{"full":"acme/web!6"},"detailed_merge_status":"ci_still_running"},
			  {"iid":8,"title":"Asked changes","author":{"username":"ana"},"references":{"full":"acme/web!8"},"detailed_merge_status":"requested_changes"}]`))
		case len(q["approved_by_usernames[]"]) == 1 && q["approved_by_usernames[]"][0] == "ana":
			seen = append(seen, "approved")
			_, _ = w.Write([]byte(`[{"iid":9,"title":"Theirs","author":{"username":"naser"},"references":{"full":"acme/api!9"},"detailed_merge_status":"mergeable"},
			  {"iid":4,"title":"Ship it","author":{"username":"ana"},"references":{"full":"acme/api!4"}}]`))
		default:
			w.WriteHeader(400)
		}
	})
	tr, mine, rv, err := g.Inbox(srv.URL, "tok", "ana")
	if err != nil || strings.Join(seen, ",") != "reviewer,author,approved" {
		t.Fatalf("Inbox: %v (asked %v)", err, seen)
	}
	if len(tr) != 1 || tr[0].Repo != "acme/api" || tr[0].Number != 7 || tr[0].Author != "naser" || tr[0].ReviewDecision != "REVIEW_REQUIRED" || tr[0].Kind != "gitlab" {
		t.Errorf("to review: %+v", tr)
	}
	if len(mine) != 5 {
		t.Fatalf("mine: %+v", mine)
	}
	if m := mine[0]; m.Repo != "acme/team/web" || m.Mergeable != "CONFLICTING" || m.Checks != "FAILURE" {
		t.Errorf("conflict and failed pipeline: %+v", m)
	}
	if m := mine[1]; !m.ReadyToMerge || m.Checks != "SUCCESS" || m.Mergeable != "MERGEABLE" {
		t.Errorf("mergeable: %+v", m)
	}
	if !mine[2].Draft || mine[3].Checks != "PENDING" || mine[4].ReviewDecision != "CHANGES_REQUESTED" {
		t.Errorf("draft, running, changes requested: %+v", mine[2:])
	}
	if len(rv) != 1 || rv[0].Number != 9 {
		t.Errorf("reviewed lately leaves out your own: %+v", rv)
	}
}

func TestGitLabBranchPR(t *testing.T) {
	srv, g := glServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		switch {
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests" && r.URL.Query().Get("source_branch") == "billing":
			_, _ = w.Write([]byte(`[{"iid":9,"source_project_id":77,"target_project_id":5},{"iid":42,"source_project_id":5,"target_project_id":5}]`))
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests" && r.URL.Query().Get("source_branch") == "none":
			_, _ = w.Write([]byte(`[]`))
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests/42":
			_, _ = w.Write([]byte(`{"iid":42,"title":"Billing","web_url":"https://gitlab.com/acme/team/api/-/merge_requests/42","author":{"username":"ana"},
			  "references":{"full":"acme/team/api!42"},"detailed_merge_status":"mergeable","head_pipeline":{"status":"success"}}`))
		case path == "/projects/acme%2Fteam%2Fapi/merge_requests/42/approvals":
			_, _ = w.Write([]byte(`{"approved_by":[{"user":{"username":"naser"}},{"user":{"username":"sam"}}]}`))
		default:
			w.WriteHeader(404)
		}
	})
	p, err := g.BranchPR(srv.URL, "tok", "acme/team/api", "billing")
	if err != nil || p == nil || p.Number != 42 || p.Approvals != 2 || p.Checks != "SUCCESS" || !p.ReadyToMerge || p.Repo != "acme/team/api" || p.Author != "ana" {
		t.Fatalf("BranchPR = %+v, %v (a fork's merge request must be skipped)", p, err)
	}
	if p, err := g.BranchPR(srv.URL, "tok", "acme/team/api", "none"); p != nil || err != nil {
		t.Errorf("no merge request: %+v %v", p, err)
	}
	if p, err := g.BranchPR(srv.URL, "tok", "acme/other", "x"); p != nil || err != nil {
		t.Errorf("a project the token cannot see: %+v %v", p, err)
	}
}
