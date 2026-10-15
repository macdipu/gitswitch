package infrastructure

import (
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
