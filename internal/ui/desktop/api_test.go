package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/app"
)

func call(t *testing.T, h http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	m := map[string]any{}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatalf("%s %s: bad JSON %q", method, path, w.Body.String())
		}
	}
	return w, m
}

func TestHandler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", home+"/.gitconfig")
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "1")
	h := Handler(app.New(false, nil))

	if w, _ := call(t, h, "GET", "/", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>GitSwitch</title>") {
		t.Fatalf("GET / = %d", w.Code)
	}
	if w, _ := call(t, h, "GET", "/api/nope", ""); w.Code != 404 {
		t.Fatalf("unknown route = %d, want 404", w.Code)
	}
	if w, m := call(t, h, "POST", "/api/open", `{"url":"file:///etc/passwd"}`); w.Code != 400 || m["error"] == nil {
		t.Fatalf("open file:// = %d %v, want an error", w.Code, m)
	}
	w, m := call(t, h, "GET", "/api/state", "")
	data, _ := m["data"].(map[string]any)
	if w.Code != 200 || data["version"] != app.Version {
		t.Fatalf("GET /api/state = %d %v", w.Code, m)
	}
}
