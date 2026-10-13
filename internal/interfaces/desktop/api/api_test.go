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

// quote writes s as a JSON string, so Windows paths keep their backslashes.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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
	if w, _ := call(t, h, "POST", "/api/repo/clone", `{"url":"ext::sh -c id","parent":`+quote(home)+`,"name":"x"}`); w.Code != 200 {
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
	_, m = call(t, h, "GET", "/api/refs?gitDir="+dir, "")
	data, _ = m["data"].(map[string]any)
	if u, _ := data["undo"].(map[string]any); u["action"] != "switch" || u["blocked"] != "" || u["effect"] != "Switches back to main." {
		t.Fatalf("refs undo = %v", data["undo"])
	}
	if d := post("/api/undo", `{`+gd+`}`); d["label"] != "Switched from main to side" || git("branch", "--show-current") != "main" {
		t.Fatalf("undo = %v, on %s", d, git("branch", "--show-current"))
	}
	fails("/api/undo", `{`+gd+`}`, "nothing to undo")
	post("/api/repo/switch", `{`+gd+`,"branch":"side"}`)
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

// testRepo makes a repo in a throwaway home with main and side, which
// both change f.txt, and returns git in it and a way to write f.txt.
func testRepo(t *testing.T) (dir string, git func(...string) string, write func(string)) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1",
		"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
		t.Setenv(k, v)
	}
	dir = filepath.Join(home, "r")
	git = func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write = func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	write("base\n")
	git("add", "f.txt")
	git("commit", "-q", "-m", "base")
	git("switch", "-q", "-c", "side")
	write("side\n")
	git("commit", "-q", "-am", "side")
	git("switch", "-q", "main")
	return dir, git, write
}

// A smart switch that clashes is reported by /api/refs until it is
// finished or undone through /api/resolve.
func TestSmartSwitchAndResolve(t *testing.T) {
	dir, git, write := testRepo(t)
	write("mine\n")

	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	w, m := call(t, h, "POST", "/api/repo/switch", `{`+gd+`,"branch":"side","mode":"smart"}`)
	d, _ := m["data"].(map[string]any)
	if c, _ := d["clash"].(map[string]any); w.Code != 200 || c["from"] != "main" || c["to"] != "side" {
		t.Fatalf("smart switch = %d %v", w.Code, m)
	}
	_, m = call(t, h, "GET", "/api/refs?gitDir="+dir, "")
	d, _ = m["data"].(map[string]any)
	if c, _ := d["clash"].(map[string]any); c == nil || fmt.Sprint(c["files"]) != "[f.txt]" {
		t.Fatalf("refs clash = %v", d["clash"])
	}
	if w, m := call(t, h, "POST", "/api/resolve", `{`+gd+`,"action":"done"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "conflict markers") {
		t.Fatalf("done with markers = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/resolve", `{`+gd+`,"action":"abort"}`); w.Code != 200 {
		t.Fatalf("abort = %d %v", w.Code, m)
	}
	if git("branch", "--show-current") != "main" || git("status", "--porcelain") != "M f.txt" {
		t.Fatalf("after abort: on %s, %q", git("branch", "--show-current"), git("status", "--porcelain"))
	}
}

// Parked changes are listed by /api/refs, offered back by the switch to
// their branch and restored through /api/parked.
func TestParkAndRestore(t *testing.T) {
	dir, git, write := testRepo(t)
	write("mine\n")

	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	w, m := call(t, h, "POST", "/api/repo/switch", `{`+gd+`,"branch":"side","mode":"park"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["parked"] != true || git("status", "--porcelain") != "" {
		t.Fatalf("park = %d %v, status %q", w.Code, m, git("status", "--porcelain"))
	}
	_, m = call(t, h, "GET", "/api/refs?gitDir="+dir, "")
	d, _ := m["data"].(map[string]any)
	ps, _ := d["parked"].([]any)
	p, _ := ps[0].(map[string]any)
	if len(ps) != 1 || p["branch"] != "main" || fmt.Sprint(p["files"]) != "[f.txt]" {
		t.Fatalf("refs parked = %v", d["parked"])
	}
	_, m = call(t, h, "POST", "/api/repo/switch", `{`+gd+`,"branch":"main"}`)
	d, _ = m["data"].(map[string]any)
	if ws, _ := d["waiting"].([]any); len(ws) != 1 {
		t.Fatalf("switch back: waiting = %v", d["waiting"])
	}
	if w, m := call(t, h, "POST", "/api/parked", `{`+gd+`,"action":"restore","stash":"`+fmt.Sprint(p["stash"])+`"}`); w.Code != 200 {
		t.Fatalf("restore = %d %v", w.Code, m)
	}
	if git("status", "--porcelain") != "M f.txt" || git("stash", "list") != "" {
		t.Fatalf("after restore: %q, stash %q", git("status", "--porcelain"), git("stash", "list"))
	}
}

// The switch-changes setting is saved for every repo or one, and /api/refs
// reports the one in effect.
func TestSwitchSetting(t *testing.T) {
	dir, git, _ := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	if w, m := call(t, h, "POST", "/api/switch-setting", `{"mode":"sometimes"}`); w.Code != 400 {
		t.Fatalf("bad mode = %d %v", w.Code, m)
	}
	call(t, h, "POST", "/api/switch-setting", `{"mode":"smart"}`)
	_, m := call(t, h, "POST", "/api/switch-setting", `{`+gd+`,"mode":"park"}`)
	if d, _ := m["data"].(map[string]any); d["mode"] != "park" || d["global"] != "smart" {
		t.Fatalf("saved = %v", m)
	}
	if v := git("config", "--local", "gitswitch.switchChanges"); v != "park" {
		t.Fatalf("repo config = %q", v)
	}
	call(t, h, "POST", "/api/switch-setting", `{`+gd+`,"mode":""}`)
	_, m = call(t, h, "GET", "/api/refs?gitDir="+dir, "")
	d, _ := m["data"].(map[string]any)
	if st, _ := d["switchSetting"].(map[string]any); st["mode"] != "smart" || st["repo"] != "" {
		t.Fatalf("refs switchSetting = %v", d["switchSetting"])
	}
	_, m = call(t, h, "GET", "/api/state", "")
	if d, _ := m["data"].(map[string]any); d["switchChanges"] != "smart" {
		t.Fatalf("state switchChanges = %v", d["switchChanges"])
	}
}

// A new branch starts at HEAD or a commit and brings uncommitted changes;
// the name prefix is a setting the page puts in front.
func TestNewBranch(t *testing.T) {
	dir, git, write := testRepo(t)
	write("mine\n")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	if w, m := call(t, h, "POST", "/api/settings", `{"branchPrefix":"has space"}`); w.Code != 400 {
		t.Fatalf("bad prefix = %d %v", w.Code, m)
	}
	call(t, h, "POST", "/api/settings", `{"branchPrefix":"feature/"}`)
	if _, m := call(t, h, "GET", "/api/state", ""); fmt.Sprint(m["data"].(map[string]any)["branchPrefix"]) != "feature/" {
		t.Fatalf("state branchPrefix = %v", m)
	}
	w, m := call(t, h, "POST", "/api/branch/new", `{`+gd+`,"name":"feature/login"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["branch"] != "feature/login" || d["carried"] != true {
		t.Fatalf("new branch = %d %v", w.Code, m)
	}
	if git("branch", "--show-current") != "feature/login" || git("status", "--porcelain") != "M f.txt" {
		t.Fatalf("on %s, %q", git("branch", "--show-current"), git("status", "--porcelain"))
	}
	// side changes f.txt too, so the changes can't come along there.
	if w, m := call(t, h, "POST", "/api/branch/new", `{`+gd+`,"name":"from-side","from":"side"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing changed") {
		t.Fatalf("clash = %d %v", w.Code, m)
	}
	git("checkout", "--", "f.txt")
	if w, m := call(t, h, "POST", "/api/branch/new", `{`+gd+`,"name":"from-side","from":"side"}`); w.Code != 200 || git("rev-parse", "HEAD") != git("rev-parse", "side") {
		t.Fatalf("from side = %d %v", w.Code, m)
	}
}

// Push puts a branch that is not on the remote yet there, tracking it,
// and reports how many commits went.
func TestPush(t *testing.T) {
	dir, git, _ := testRepo(t)
	server := filepath.Join(filepath.Dir(dir), "server.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", server).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	git("remote", "add", "origin", server)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	w, m := call(t, h, "POST", "/api/repo/push", `{`+gd+`}`)
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["first"] != true || d["upstream"] != "origin/main" || fmt.Sprint(d["commits"]) != "1" {
		t.Fatalf("push = %d %v", w.Code, m)
	}
	if git("rev-parse", "--abbrev-ref", "main@{upstream}") != "origin/main" {
		t.Fatal("main does not track origin/main")
	}
	_, m = call(t, h, "POST", "/api/repo/push", `{`+gd+`}`)
	if d, _ := m["data"].(map[string]any); fmt.Sprint(d["commits"]) != "0" {
		t.Fatalf("second push = %v", m)
	}
}
