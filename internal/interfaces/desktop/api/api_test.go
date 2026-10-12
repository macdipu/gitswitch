package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/macdipu/gitswitch/internal/bootstrap"
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
	h := Handler(bootstrap.New(false, nil), nil)

	if w, _ := call(t, h, "GET", "/", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>GitSwitch</title>") {
		t.Fatalf("GET / = %d", w.Code)
	}
	if w, _ := call(t, h, "GET", "/api/nope", ""); w.Code != 404 {
		t.Fatalf("unknown route = %d, want 404", w.Code)
	}
	if w, m := call(t, h, "POST", "/api/open", `{"url":"file:///etc/passwd"}`); w.Code != 400 || m["error"] == nil {
		t.Fatalf("open file:// = %d %v, want an error", w.Code, m)
	}
	// A clone runs in the background; a bad URL ends the job with an error.
	if w, _ := call(t, h, "POST", "/api/repo/clone", `{"url":"ext::sh -c id","parent":"`+home+`","name":"x"}`); w.Code != 200 {
		t.Fatalf("start clone = %d", w.Code)
	}
	var job map[string]any
	for i := 0; i < 100; i++ {
		_, m := call(t, h, "GET", "/api/repo/clone", "")
		if job, _ = m["data"].(map[string]any); job["running"] == false {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e, _ := job["error"].(string); !strings.Contains(e, "not a repo URL") {
		t.Fatalf("clone job = %v, want a URL error", job)
	}
	w, m := call(t, h, "GET", "/api/state", "")
	data, _ := m["data"].(map[string]any)
	if w.Code != 200 || data["version"] != bootstrap.Version {
		t.Fatalf("GET /api/state = %d %v", w.Code, m)
	}
}

// Bad JSON is refused before anything runs, and rules are removed by
// value, so an outdated page cannot remove the wrong rule.
func TestRulesAndBadJSON(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		t.Setenv(k, v)
	}
	t.Chdir(home) // not inside this source repo
	h := Handler(bootstrap.New(false, nil), nil)
	for _, b := range []string{`{"id":`, `[1,2]`, `"text"`} {
		if w, m := call(t, h, "POST", "/api/default", b); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "not valid JSON") {
			t.Fatalf("POST %s = %d %v, want a JSON error", b, w.Code, m)
		}
	}
	post := func(path, body string) (int, string) {
		w, m := call(t, h, "POST", path, body)
		e, _ := m["error"].(string)
		return w.Code, e
	}
	if c, e := post("/api/account", `{"mode":"new","id":"work","email":"w@x.example","host":"github.com"}`); c != 200 {
		t.Fatalf("add account = %d %s", c, e)
	}
	for _, p := range []string{"acme", "beta"} {
		if c, e := post("/api/rule", `{"kind":"owner","pattern":"`+p+`","account":"work"}`); c != 200 {
			t.Fatalf("add rule = %d %s", c, e)
		}
	}
	rules := func() []any {
		_, m := call(t, h, "GET", "/api/state", "")
		data, _ := m["data"].(map[string]any)
		cfg, _ := data["config"].(map[string]any)
		rs, _ := cfg["rules"].([]any)
		return rs
	}
	if c, _ := post("/api/rule/remove", `{"index":0}`); c != 400 {
		t.Fatalf("remove by index = %d, want 400", c)
	}
	if c, _ := post("/api/rule/remove", `{"kind":"owner","pattern":"gone","account":"work"}`); c != 400 || len(rules()) != 2 {
		t.Fatalf("removing a missing rule = %d, rules %v", c, rules())
	}
	if c, e := post("/api/rule/remove", `{"kind":"owner","pattern":"beta","account":"work"}`); c != 200 {
		t.Fatalf("remove rule = %d %s", c, e)
	}
	if rs := rules(); len(rs) != 1 || rs[0].(map[string]any)["pattern"] != "acme" {
		t.Fatalf("rules after remove = %v", rs)
	}
	if w, m := call(t, h, "GET", "/api/doctor", ""); w.Code != 200 || m["data"] == nil {
		t.Fatalf("GET /api/doctor = %d %v", w.Code, m)
	}
}

// The history page's branch and commit actions, against a real repo.
func TestHistoryActions(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1",
		"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
		t.Setenv(k, v)
	}
	dir := filepath.Join(home, "r")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(file, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	base := commit("a", "base")
	git("switch", "-q", "-c", "side")
	s1, s2 := commit("s1", "side one"), commit("s2", "side two")
	git("switch", "-q", "main")
	m1 := commit("m1", "main one")
	commit("m2", "main two")

	h := Handler(bootstrap.New(false, nil), nil)
	post := func(path, body string) map[string]any {
		t.Helper()
		w, m := call(t, h, "POST", path, body)
		if w.Code != 200 {
			t.Fatalf("POST %s %s = %d %v", path, body, w.Code, m)
		}
		d, _ := m["data"].(map[string]any)
		return d
	}
	fails := func(path, body, want string) {
		t.Helper()
		w, m := call(t, h, "POST", path, body)
		if e, _ := m["error"].(string); w.Code != 400 || !strings.Contains(e, want) {
			t.Fatalf("POST %s %s = %d %v, want an error with %q", path, body, w.Code, m, want)
		}
	}
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`

	_, m := call(t, h, "GET", "/api/refs?gitDir="+dir, "")
	data, _ := m["data"].(map[string]any)
	head, _ := data["head"].(map[string]any)
	if branches, _ := data["branches"].([]any); head["branch"] != "main" || len(branches) != 2 {
		t.Fatalf("refs = %v", m)
	}
	fails("/api/commit/revert", `{"gitDir":"`+filepath.ToSlash(home)+`","hash":"`+m1+`"}`, "not a git repo")

	if d := post("/api/commit/cherry-pick", `{`+gd+`,"hashes":["`+s2+`","`+s1+`"]}`); d["picked"] != float64(2) {
		t.Fatalf("cherry-pick = %v", d)
	}
	if got := git("log", "-3", "--format=%s"); got != "side two\nside one\nmain two" {
		t.Fatalf("after cherry-pick:\n%s", got)
	}
	picked1 := git("rev-parse", "HEAD~1") // the copy of "side one"
	post("/api/commit/revert", `{`+gd+`,"hash":"`+m1+`"}`)
	fails("/api/commit/drop", `{`+gd+`,"hash":"`+s1+`"}`, "not on the current branch")
	post("/api/commit/drop", `{`+gd+`,"hash":"`+picked1[:7]+`"}`)
	if got := git("log", "-3", "--format=%s"); got != "Revert \"main one\"\nside two\nmain two" {
		t.Fatalf("after revert and drop:\n%s", got)
	}
	if d := post("/api/commit/rollback", `{`+gd+`,"hash":"`+base+`"}`); d["undone"] != float64(4) {
		t.Fatalf("rollback = %v", d)
	}
	if git("rev-parse", "HEAD") != base || git("status", "--porcelain") == "" {
		t.Fatal("rollback should keep the undone changes as uncommitted changes")
	}
	git("stash", "-q", "-u")
	if d := post("/api/repo/switch", `{`+gd+`,"branch":"side"}`); d["branch"] != "side" {
		t.Fatalf("switch = %v", d)
	}
	fails("/api/repo/update", `{`+gd+`}`, "not on a remote")
	fails("/api/commit/browse", `{`+gd+`,"hash":""}`, "no remote")
	fails("/api/commit/drop", `{`+gd+`,"hash":"--force"}`, "not a commit")
}

// A git or ssh error the server knows comes with a plain-words hint.
func TestErrorsCarryAHint(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, nil, fmt.Errorf("git@github.com: Permission denied (publickey)."))
	var got struct {
		Error string
		Hint  *struct{ Problem, Fix, Action string }
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || got.Hint == nil || got.Hint.Action != "pubkey" || got.Hint.Fix == "" {
		t.Errorf("code=%d body=%+v", rec.Code, got)
	}
}
