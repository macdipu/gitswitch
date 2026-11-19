package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if w, m := call(t, h, "POST", "/api/repo/opened", `{"gitDir":`+quote(home)+`}`); w.Code != 400 || m["error"] == nil {
		t.Fatalf("opened a folder that is not a repo = %d %v, want an error", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/group", `{"path":`+quote(home)+`,"name":"Client"}`); w.Code != 400 || m["error"] == nil {
		t.Fatalf("grouping a folder that is not listed = %d %v, want an error", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/settings", `{"repoGroups":"colour"}`); w.Code != 400 || m["error"] == nil {
		t.Fatalf("an unknown way to group = %d %v, want an error", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/settings", `{"repoGroups":"owner"}`); w.Code != 200 {
		t.Fatalf("group by owner = %d %v", w.Code, m)
	}
	if _, m := call(t, h, "GET", "/api/repo/groups", ""); m["data"].(map[string]any)["by"] != "owner" {
		t.Fatalf("GET /api/repo/groups = %v, want by owner", m)
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
	if _, m := call(t, h, "GET", "/api/state", ""); m["data"].(map[string]any)["fetchOnOpen"] != false {
		t.Fatalf("fetchOnOpen is off by default: %v", m)
	}
	call(t, h, "POST", "/api/settings", `{"fetchOnOpen":true}`)
	if _, m := call(t, h, "GET", "/api/state", ""); m["data"].(map[string]any)["fetchOnOpen"] != true {
		t.Fatalf("state fetchOnOpen = %v", m)
	}
	call(t, h, "POST", "/api/settings", `{"fetchOnOpen":false}`)
	if _, m := call(t, h, "GET", "/api/state", ""); m["data"].(map[string]any)["fetchOnOpen"] != false {
		t.Fatalf("state fetchOnOpen after off = %v", m)
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

// The account check before a push finds not-pushed commits made as
// another account and a repo email that is not the account's.
func TestAccountCheck(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	for _, a := range []string{`{"mode":"new","id":"work","host":"github.com","name":"W","email":"w@work.example"}`, `{"mode":"new","id":"home","host":"github.com","name":"H","email":"h@home.example"}`} {
		if w, m := call(t, h, "POST", "/api/account", a); w.Code != 200 {
			t.Fatalf("add account = %d %v", w.Code, m)
		}
	}
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	call(t, h, "POST", "/api/repo/use", `{`+gd+`,"account":"work"}`)
	git("config", "user.email", "h@home.example")
	write("home\n")
	t.Setenv("GIT_AUTHOR_EMAIL", "h@home.example") // the test's environment sets the author
	git("commit", "-q", "-am", "made as home")
	_, m := call(t, h, "GET", "/api/account-check?gitDir="+dir, "")
	d, _ := m["data"].(map[string]any)
	if d["account"] != "work" || d["emailBad"] != true || fmt.Sprint(d["wrong"]) != "2" || fmt.Sprint(d["wrongEmails"]) != "[t@x.example h@home.example]" {
		t.Fatalf("check = %v", m)
	}
	call(t, h, "POST", "/api/repo/use", `{`+gd+`,"account":"work","once":true}`)
	if git("config", "user.email") != "w@work.example" {
		t.Fatalf("use once: email %q", git("config", "user.email"))
	}
}

// Commits made on the wrong branch move to a new one: the old branch goes
// back to its upstream, and undo puts them back.
func TestMoveToNewBranch(t *testing.T) {
	dir, git, write := testRepo(t)
	git("remote", "add", "origin", "https://example.com/r.git")
	git("update-ref", "refs/remotes/origin/main", "main")
	git("branch", "-q", "--set-upstream-to=origin/main")
	pushed := git("rev-parse", "HEAD")
	write("one\n")
	git("commit", "-q", "-am", "one")
	write("two\n")
	git("commit", "-q", "-am", "two")
	tip := git("rev-parse", "HEAD")
	write("mine\n")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	if w, m := call(t, h, "POST", "/api/branch/move", `{`+gd+`,"name":"side"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "already a branch side") {
		t.Fatalf("taken name = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/branch/move", `{`+gd+`,"name":"login"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["branch"] != "login" || d["from"] != "main" || fmt.Sprint(d["commits"]) != "2" {
		t.Fatalf("move = %d %v", w.Code, m)
	}
	if git("branch", "--show-current") != "login" || git("rev-parse", "HEAD") != tip || git("rev-parse", "main") != pushed || git("status", "--porcelain") != "M f.txt" {
		t.Fatalf("after move: on %s, main at %s, %q", git("branch", "--show-current"), git("rev-parse", "main"), git("status", "--porcelain"))
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
	if git("branch", "--show-current") != "main" || git("rev-parse", "main") != tip || git("branch", "--list", "login") != "" || git("status", "--porcelain") != "M f.txt" {
		t.Fatalf("after undo: on %s, main at %s, branches %q", git("branch", "--show-current"), git("rev-parse", "main"), git("branch"))
	}
}

// Merged lists the branches prune deletes: those merged into main when the
// remote's default branch isn't known.
func TestPrune(t *testing.T) {
	dir, git, _ := testRepo(t)
	git("branch", "done")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	w, m := call(t, h, "POST", "/api/branch/merged", `{`+gd+`}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["into"] != "main" || fmt.Sprint(d["branches"]) == "[]" {
		t.Fatalf("merged = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/branch/prune", `{`+gd+`}`); w.Code != 200 {
		t.Fatalf("prune = %d %v", w.Code, m)
	}
	// side has its own commit: not merged, so it stays.
	if got := git("branch", "--format=%(refname:short)"); got != "main\nside" {
		t.Fatalf("branches = %q", got)
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || !strings.Contains(git("branch"), "done") {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
}

// A commit from the page: the staged file, or every change with all.
func TestCommit(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	write("mine\n")
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"x"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing is staged") {
		t.Fatalf("nothing staged = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Mine","all":true}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["subject"] != "Mine" || d["all"] != true || git("status", "--porcelain") != "" {
		t.Fatalf("commit all = %d %v", w.Code, m)
	}
}

// Amend from the page: what the page shows first, a new message, every
// change, undo, and the refusal once the commit is pushed.
func TestAmend(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + url.QueryEscape(dir)
	w, m := call(t, h, "GET", "/api/repo/amendable?"+q, "")
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["can"] != true || d["why"] != "" || d["subject"] != "base" || d["message"] != "base" {
		t.Fatalf("amendable = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"amend":true}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing to amend") {
		t.Fatalf("nothing to amend = %d %v", w.Code, m)
	}
	base := git("rev-parse", "HEAD")
	w, m = call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"amend":true,"message":"Base, better"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["subject"] != "Base, better" || d["amended"] != base[:7] || git("log", "-1", "--format=%s") != "Base, better" {
		t.Fatalf("amend message = %d %v", w.Code, m)
	}
	write("more\n")
	w, m = call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"amend":true,"all":true}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["subject"] != "Base, better" || git("status", "--porcelain") != "" {
		t.Fatalf("amend all = %d %v", w.Code, m)
	}
	// Undo puts the commit back as it was before that amend; what the
	// amend added stays in the file, uncommitted.
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("status", "--porcelain") != "M f.txt" || git("log", "-1", "--format=%s") != "Base, better" {
		t.Fatalf("undo = %d %v, status %q", w.Code, m, git("status", "--porcelain"))
	}
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	w, m = call(t, h, "GET", "/api/repo/amendable?"+q, "")
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["can"] != false || !strings.Contains(fmt.Sprint(d["why"]), "already pushed") {
		t.Fatalf("amendable after push = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"amend":true,"all":true}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "already pushed") {
		t.Fatalf("amend pushed = %d %v", w.Code, m)
	}
}

// Compare lists the commits to push to the upstream.
func TestCompare(t *testing.T) {
	dir, git, write := testRepo(t)
	git("remote", "add", "origin", "https://example.com/r.git")
	git("update-ref", "refs/remotes/origin/main", "main")
	git("branch", "-q", "--set-upstream-to=origin/main")
	write("one\n")
	git("commit", "-q", "-am", "one")
	h := Handler(bootstrap.New(false, nil), nil)
	w, m := call(t, h, "POST", "/api/branch/compare", `{"gitDir":"`+filepath.ToSlash(dir)+`"}`)
	d, _ := m["data"].(map[string]any)
	push, _ := d["toPush"].([]any)
	if w.Code != 200 || len(push) != 1 || d["upstream"] != "origin/main" || d["toPull"] != nil {
		t.Fatalf("compare = %d %v", w.Code, m)
	}
}

// The changes list from the page: list, diff, stage, unstage, discard.
func TestChanges(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + url.QueryEscape(dir)
	write("changed\n")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, m := call(t, h, "GET", "/api/repo/changes?"+q, "")
	d, _ := m["data"].(map[string]any)
	if files, _ := d["files"].([]any); w.Code != 200 || len(files) != 2 {
		t.Fatalf("changes = %d %v", w.Code, m)
	}
	w, m = call(t, h, "GET", "/api/repo/diff?"+q+"&path=new.txt", "")
	if d, _ := m["data"].(map[string]any); w.Code != 200 || !strings.Contains(fmt.Sprint(d["text"]), "+fresh") {
		t.Fatalf("diff = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/stage", `{`+gd+`,"paths":["f.txt"]}`); w.Code != 200 || git("diff", "--cached", "--name-only") != "f.txt" {
		t.Fatalf("stage = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/unstage", `{`+gd+`,"all":true}`); w.Code != 200 || git("diff", "--cached", "--name-only") != "" {
		t.Fatalf("unstage = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/stage", `{`+gd+`,"paths":["nope.txt"]}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "no uncommitted changes") {
		t.Fatalf("stage a clean file = %d %v", w.Code, m)
	}
	w, m = call(t, h, "POST", "/api/repo/discard", `{`+gd+`,"all":true}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || git("status", "--porcelain") != "" || !strings.Contains(fmt.Sprint(d["stash"]), "gitswitch discard: main") {
		t.Fatalf("discard = %d %v status %q", w.Code, m, git("status", "--porcelain"))
	}
	if !strings.Contains(git("stash", "list"), "gitswitch discard: main") {
		t.Fatal("the discarded changes are not in the stash")
	}
}

// Ignore: a new file goes into .gitignore and is ignored; a tracked one is
// reported, and untracked only when asked; a bad kind and a path outside
// the repo are refused.
func TestRepoIgnore(t *testing.T) {
	dir, git, _ := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	if err := os.WriteFile(filepath.Join(dir, "build.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, m := call(t, h, "POST", "/api/repo/ignore", `{`+gd+`,"path":"build.log","kind":"ext"}`)
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["pattern"] != "*.log" || d["where"] != ".gitignore" || d["added"] != true || d["ignored"] != true {
		t.Fatalf("ignore = %d %v", w.Code, m)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(b) != "*.log\n" {
		t.Fatalf(".gitignore = %q", b)
	}
	w, m = call(t, h, "POST", "/api/repo/ignore", `{`+gd+`,"path":"f.txt","local":true}`)
	d, _ = m["data"].(map[string]any)
	if tr, _ := d["tracked"].([]any); w.Code != 200 || d["where"] != ".git/info/exclude" || d["ignored"] != false || len(tr) != 1 || !strings.Contains(fmt.Sprint(d["note"]), "tracked by git") {
		t.Fatalf("ignore a tracked file = %d %v", w.Code, m)
	}
	if git("ls-files", "f.txt") != "f.txt" {
		t.Fatal("untracked without untrack")
	}
	w, m = call(t, h, "POST", "/api/repo/ignore", `{`+gd+`,"path":"f.txt","local":true,"untrack":true}`)
	d, _ = m["data"].(map[string]any)
	if un, _ := d["untracked"].([]any); w.Code != 200 || d["added"] != false || d["ignored"] != true || len(un) != 1 || git("ls-files", "f.txt") != "" {
		t.Fatalf("untrack = %d %v", w.Code, m)
	}
	if _, err := os.Stat(filepath.Join(dir, "f.txt")); err != nil {
		t.Fatalf("untrack removed the file: %v", err)
	}
	if w, m := call(t, h, "POST", "/api/repo/ignore", `{`+gd+`,"path":"../x"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "outside the repo") {
		t.Fatalf("outside = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/ignore", `{`+gd+`,"path":"build.log","kind":"glob"}`); w.Code != 400 {
		t.Fatalf("bad kind = %d %v", w.Code, m)
	}
}

// Branches are deleted (side only with force: its commit is nowhere
// else), renamed and given an upstream; delete and rename are undone.
// Tags are made, listed, pushed and deleted.
func TestBranchAndTagManagement(t *testing.T) {
	dir, git, _ := testRepo(t)
	server := filepath.Join(filepath.Dir(dir), "server.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", server).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	git("remote", "add", "origin", server)
	git("push", "-q", "origin", "main")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	post := func(path, b string) (int, map[string]any, string) {
		w, m := call(t, h, "POST", path, `{`+gd+b+`}`)
		d, _ := m["data"].(map[string]any)
		return w.Code, d, fmt.Sprint(m["error"])
	}

	if c, _, e := post("/api/branch/delete", `,"name":"main"`); c != 400 || !strings.Contains(e, "current branch") {
		t.Fatalf("delete current = %d %s", c, e)
	}
	if c, _, e := post("/api/branch/delete", `,"name":"side"`); c != 400 || !strings.Contains(e, "side has 1 commit that is on no other branch or remote") {
		t.Fatalf("delete side = %d %s", c, e)
	}
	side := git("rev-parse", "side")
	if c, d, e := post("/api/branch/delete", `,"name":"side","force":true`); c != 200 || d["branch"] != "side" || d["hash"] != side || fmt.Sprint(d["only"]) != "1" {
		t.Fatalf("force delete = %d %v %s", c, d, e)
	}
	if git("branch", "--list", "side") != "" {
		t.Fatal("side is still there")
	}
	if c, _, e := post("/api/undo", ``); c != 200 || git("rev-parse", "side") != side {
		t.Fatalf("undo delete = %d %s", c, e)
	}

	if c, d, e := post("/api/branch/upstream", `,"branch":"","upstream":"origin/main"`); c != 200 || d["branch"] != "main" || d["upstream"] != "origin/main" ||
		git("rev-parse", "--abbrev-ref", "main@{upstream}") != "origin/main" {
		t.Fatalf("upstream = %d %v %s", c, d, e)
	}
	if c, d, e := post("/api/branch/rename", `,"from":"main","to":"trunk"`); c != 200 || d["from"] != "main" || d["to"] != "trunk" || d["upstream"] != "origin/main" ||
		git("branch", "--show-current") != "trunk" || git("rev-parse", "--abbrev-ref", "trunk@{upstream}") != "origin/main" {
		t.Fatalf("rename = %d %v %s", c, d, e)
	}
	if c, _, e := post("/api/branch/rename", `,"from":"side","to":"trunk"`); c != 400 || !strings.Contains(e, "already a branch trunk") {
		t.Fatalf("rename onto trunk = %d %s", c, e)
	}
	if c, _, e := post("/api/undo", ``); c != 200 || git("branch", "--show-current") != "main" {
		t.Fatalf("undo rename = %d %s, on %s", c, e, git("branch", "--show-current"))
	}
	if c, d, e := post("/api/branch/upstream", `,"branch":"main","upstream":""`); c != 200 || d["upstream"] != "" || d["was"] != "origin/main" {
		t.Fatalf("unset upstream = %d %v %s", c, d, e)
	}

	if c, d, e := post("/api/tag/new", `,"name":"v1","rev":"side","message":"Release 1"`); c != 200 || d["tag"] != "v1" || d["annotated"] != true || d["hash"] != side ||
		git("cat-file", "-t", "v1") != "tag" {
		t.Fatalf("annotated tag = %d %v %s", c, d, e)
	}
	if c, d, e := post("/api/tag/new", `,"name":"v0"`); c != 200 || d["annotated"] != false || git("cat-file", "-t", "v0") != "commit" {
		t.Fatalf("light tag = %d %v %s", c, d, e)
	}
	if c, _, e := post("/api/tag/new", `,"name":"v0"`); c != 400 || !strings.Contains(e, "already a tag v0") {
		t.Fatalf("tag again = %d %s", c, e)
	}
	_, m := call(t, h, "GET", "/api/tags?gitDir="+url.QueryEscape(dir), "")
	if tags := fmt.Sprint(m["data"]); tags != "[v0 v1]" && tags != "[v1 v0]" {
		t.Fatalf("tags = %v", m)
	}
	if c, d, e := post("/api/tag/push", `,"name":"v1"`); c != 200 || d["tag"] != "v1" || d["remote"] != "origin" {
		t.Fatalf("push tag = %d %v %s", c, d, e)
	}
	if out, err := exec.Command("git", "--git-dir", server, "rev-parse", "v1^{commit}").Output(); err != nil || strings.TrimSpace(string(out)) != side {
		t.Fatalf("the server has no v1: %v %s", err, out)
	}
	if c, d, e := post("/api/tag/delete", `,"name":"v1"`); c != 200 || d["tag"] != "v1" || git("tag", "--list", "v1") != "" {
		t.Fatalf("delete tag = %d %v %s", c, d, e)
	}
	// Another v1 at another commit: the push is refused, not forced.
	post("/api/tag/new", `,"name":"v1"`)
	if c, _, e := post("/api/tag/push", `,"name":"v1"`); c != 400 || !strings.Contains(e, "never forces") {
		t.Fatalf("push a different v1 = %d %s", c, e)
	}
}

// Live refresh: a long poll on /api/watch answers when a file changes,
// and with the same seq and no kinds when nothing did.
func TestWatch(t *testing.T) {
	dir, _, write := testRepo(t)
	app := bootstrap.New(false, nil)
	app.Watch.MaxWait = 400 * time.Millisecond
	app.Watch.Quiet = 30 * time.Millisecond
	defer app.Watch.Close()
	h := Handler(app, nil)
	q := "/api/watch?gitDir=" + url.QueryEscape(dir)

	w, m := call(t, h, "GET", q, "")
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["seq"] == nil {
		t.Fatalf("start = %d %v", w.Code, m)
	}
	seq := fmt.Sprint(d["seq"])

	// Nothing changes: the wait runs out.
	t0 := time.Now()
	_, m = call(t, h, "GET", q+"&since="+seq, "")
	d, _ = m["data"].(map[string]any)
	if kinds, ok := d["kinds"].([]any); !ok || len(kinds) != 0 || fmt.Sprint(d["seq"]) != seq || time.Since(t0) < 300*time.Millisecond {
		t.Fatalf("timeout = %v after %v", m, time.Since(t0))
	}

	// A file changes while the page waits.
	app.Watch.MaxWait = 10 * time.Second
	got := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", q+"&since="+seq, nil))
		got <- rec
	}()
	time.Sleep(100 * time.Millisecond)
	write("touched\n")
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the long poll did not answer a file change")
	}
	var res struct {
		Data struct {
			Seq     uint64   `json:"seq"`
			Kinds   []string `json:"kinds"`
			Partial bool     `json:"partial"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != 200 {
		t.Fatalf("change = %d %s", rec.Code, rec.Body)
	}
	if fmt.Sprint(res.Data.Seq) == seq || !strings.Contains(strings.Join(res.Data.Kinds, ","), "files") || res.Data.Partial {
		t.Fatalf("change = %+v, want files", res.Data)
	}

	if w, m := call(t, h, "GET", q+"&since=x", ""); w.Code != 400 || m["error"] == nil {
		t.Fatalf("a bad since = %d %v", w.Code, m)
	}
	if w, _ := call(t, h, "GET", "/api/watch?gitDir="+url.QueryEscape(t.TempDir()), ""); w.Code != 400 {
		t.Fatalf("a folder that is not a repo = %d", w.Code)
	}
}

// The commit box's extras: recent messages, co-author suggestions, the
// sign-off setting, a commit with trailers, and the draft a refused
// commit leaves.
func TestCommitInfoAndTrailers(t *testing.T) {
	dir, git, write := testRepo(t)
	git("config", "user.name", "T")
	git("config", "user.email", "t@x.example")
	git("-c", "user.name=Ada", "-c", "user.email=ada@x.example", "commit", "-q", "--allow-empty", "--author=Ada <ada@x.example>", "-m", "hers")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":` + quote(dir)
	q := "gitDir=" + url.QueryEscape(dir)

	w, m := call(t, h, "GET", "/api/repo/commit-info?"+q, "")
	d, _ := m["data"].(map[string]any)
	recent, _ := d["recent"].([]any)
	co, _ := d["coauthors"].([]any)
	id, _ := d["identity"].(map[string]any)
	if w.Code != 200 || len(recent) != 2 || d["draft"] != "" || d["signoff"] != false || id["email"] != "t@x.example" || id["name"] != "T" ||
		len(co) != 1 || co[0].(map[string]any)["email"] != "ada@x.example" || co[0].(map[string]any)["commits"] != float64(1) {
		t.Fatalf("commit-info = %d %v", w.Code, m)
	}
	if r0, _ := recent[0].(map[string]any); r0["subject"] == nil || r0["message"] == nil || r0["time"] == nil {
		t.Fatalf("recent[0] = %v", recent[0])
	}
	if st, _ := d["signoffSetting"].(map[string]any); st["global"] != false || st["repo"] != "" || st["mode"] != false {
		t.Fatalf("signoffSetting = %v", d["signoffSetting"])
	}

	// A refused commit keeps its message as the draft.
	write("mine\n")
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Keep me"}`); w.Code != 400 {
		t.Fatalf("refused = %d %v", w.Code, m)
	}
	_, m = call(t, h, "GET", "/api/repo/commit-info?"+q, "")
	if d, _ := m["data"].(map[string]any); d["draft"] != "Keep me" {
		t.Fatalf("draft = %v", m)
	}
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Mine","all":true,"coauthors":["Ada"]}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "Name <email>") {
		t.Fatalf("bad co-author = %d %v", w.Code, m)
	}

	w, m = call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Mine","all":true,"signoff":true,"coauthors":["Ada <ada@x.example>"]}`)
	if w.Code != 200 || git("log", "-1", "--format=%(trailers:only,unfold)") != "Co-authored-by: Ada <ada@x.example>\nSigned-off-by: T <t@x.example>" {
		t.Fatalf("commit with trailers = %d %v\n%s", w.Code, m, git("log", "-1", "--format=%B"))
	}
	_, m = call(t, h, "GET", "/api/repo/commit-info?"+q, "")
	if d, _ := m["data"].(map[string]any); d["draft"] != "" || d["recent"].([]any)[0].(map[string]any)["message"] != "Mine" {
		t.Fatalf("after the commit = %v", m)
	}

	// The setting: every repo on, this repo off, then following again.
	if w, m := call(t, h, "POST", "/api/signoff-setting", `{"value":"sometimes"}`); w.Code != 400 {
		t.Fatalf("bad value = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/signoff-setting", `{"value":"on"}`); w.Code != 200 || m["data"].(map[string]any)["global"] != true {
		t.Fatalf("global on = %d %v", w.Code, m)
	}
	_, m = call(t, h, "POST", "/api/signoff-setting", `{`+gd+`,"value":"off"}`)
	if d, _ := m["data"].(map[string]any); d["mode"] != false || d["repo"] != "off" || d["global"] != true {
		t.Fatalf("repo off = %v", m)
	}
	_, m = call(t, h, "POST", "/api/signoff-setting", `{`+gd+`,"value":""}`)
	if d, _ := m["data"].(map[string]any); d["mode"] != true || d["repo"] != "" {
		t.Fatalf("repo follows = %v", m)
	}
	write("more\n")
	if w, m := call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Signed by the setting","all":true}`); w.Code != 200 ||
		git("log", "-1", "--format=%(trailers:only,unfold)") != "Signed-off-by: T <t@x.example>" {
		t.Fatalf("setting = %d %v", w.Code, m)
	}
}

// Merge from the page: the plan, a merge that stops on a clash, the
// operation and the conflict (three versions), resolving and continuing,
// undo of the merge, and abort.
func TestMergeOperationConflict(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + url.QueryEscape(dir)
	write("main\n")
	git("commit", "-q", "-am", "main")
	before := git("rev-parse", "HEAD")

	w, m := call(t, h, "GET", "/api/merge/plan?"+q+"&branch=side", "")
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["fastForward"] != false || d["incomingCount"] != float64(1) || d["localCount"] != float64(1) ||
		fmt.Sprint(d["bothChanged"]) != "[f.txt]" || d["blocked"] != "" || !strings.Contains(fmt.Sprint(d["summary"]), "gone apart") {
		t.Fatalf("plan = %d %v", w.Code, m)
	}
	if d["predicted"] == true && fmt.Sprint(d["conflicts"]) != "[f.txt]" {
		t.Fatalf("predicted conflicts = %v", d["conflicts"])
	}
	if w, m := call(t, h, "POST", "/api/merge", `{`+gd+`,"branch":"side","mode":"ff"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "can't fast-forward") {
		t.Fatalf("ff-only = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/merge", `{`+gd+`,"branch":"side","mode":"octopus"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "unknown merge mode") {
		t.Fatalf("bad mode = %d %v", w.Code, m)
	}
	w, m = call(t, h, "POST", "/api/merge", `{`+gd+`,"branch":"side","mode":"merge"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["outcome"] != "conflicts" || fmt.Sprint(d["conflicts"]) != "[f.txt]" {
		t.Fatalf("merge = %d %v", w.Code, m)
	}
	w, m = call(t, h, "GET", "/api/operation?"+q, "")
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["kind"] != "merge" || d["canContinue"] != false || fmt.Sprint(d["actions"]) != "[continue abort]" {
		t.Fatalf("operation = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"continue"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "still have conflicts") {
		t.Fatalf("continue too early = %d %v", w.Code, m)
	}
	w, m = call(t, h, "GET", "/api/conflict?"+q+"&path=f.txt", "")
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["base"] != "base\n" || d["ours"] != "main\n" || d["theirs"] != "side\n" ||
		d["markers"] != true || !strings.Contains(fmt.Sprint(d["text"]), "<<<<<<<") || d["kind"] != "both changed" {
		t.Fatalf("conflict = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/conflict", `{`+gd+`,"path":"f.txt","action":"done"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "conflict markers") {
		t.Fatalf("done with markers = %d %v", w.Code, m)
	}
	w, m = call(t, h, "POST", "/api/conflict", `{`+gd+`,"path":"f.txt","action":"save","text":"main and side\n"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["canContinue"] != true || d["conflicts"] != nil {
		t.Fatalf("save = %d %v", w.Code, m)
	}
	w, m = call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"continue"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["kind"] != "" || len(strings.Fields(git("log", "-1", "--format=%P"))) != 2 ||
		git("show", "HEAD:f.txt") != "main and side" {
		t.Fatalf("continue = %d %v", w.Code, m)
	}
	// Undo takes the merge commit off again.
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != before || git("status", "--porcelain") != "" {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
	// Merge again, keep theirs... then abort instead.
	call(t, h, "POST", "/api/merge", `{`+gd+`,"branch":"side"}`)
	if w, m := call(t, h, "POST", "/api/conflict", `{`+gd+`,"path":"f.txt","action":"theirs"}`); w.Code != 200 {
		t.Fatalf("theirs = %d %v", w.Code, m)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(b) != "side\n" {
		t.Fatalf("f.txt = %q", b)
	}
	if w, m := call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"skip"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "can't be skipped") {
		t.Fatalf("skip = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"abort"}`); w.Code != 200 || git("rev-parse", "HEAD") != before || git("status", "--porcelain") != "" {
		t.Fatalf("abort = %d %v", w.Code, m)
	}
	// Uncommitted changes: refused, nothing changed.
	write("dirty\n")
	if w, m := call(t, h, "POST", "/api/merge", `{`+gd+`,"branch":"side"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "commit, park or discard them first") {
		t.Fatalf("dirty = %d %v", w.Code, m)
	}
}

// The stash manager: save (with a message, chosen paths, new files), list,
// show a file's diff, refuse a stale hash, apply, pop, branch, drop and undo.
func TestStashManager(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("changed\n")

	if w, m := call(t, h, "POST", "/api/stash/save", `{`+gd+`,"paths":["../x"]}`); w.Code != 400 {
		t.Fatalf("bad path = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/stash/save", `{`+gd+`,"message":"my stash"}`)
	st, _ := m["data"].(map[string]any)
	if w.Code != 200 || st["message"] != "my stash" || st["kind"] != "stash" || st["branch"] != "main" || git("status", "--porcelain") != "" {
		t.Fatalf("save = %d %v", w.Code, m)
	}
	hash := fmt.Sprint(st["hash"])
	if w, m := call(t, h, "POST", "/api/stash/save", `{`+gd+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing to stash") {
		t.Fatalf("nothing = %d %v", w.Code, m)
	}

	_, m = call(t, h, "GET", "/api/stashes?gitDir="+url.QueryEscape(dir), "")
	list, _ := m["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("stashes = %v", m)
	}
	files, _ := list[0].(map[string]any)["files"].([]any)
	if len(files) != 2 || files[1].(map[string]any)["untracked"] != true || files[0].(map[string]any)["status"] != "M" {
		t.Fatalf("files = %v", files)
	}
	_, m = call(t, h, "GET", "/api/stash/show?gitDir="+url.QueryEscape(dir)+"&hash="+hash+"&path=new.txt", "")
	if d, _ := m["data"].(map[string]any); d["untracked"] != true || !strings.Contains(fmt.Sprint(d["text"]), "+hello") {
		t.Fatalf("show new.txt = %v", m)
	}
	_, m = call(t, h, "GET", "/api/stash/show?gitDir="+url.QueryEscape(dir)+"&hash="+hash, "")
	if d, _ := m["data"].(map[string]any); d["hash"] != hash {
		t.Fatalf("show = %v", m)
	}
	if w, m := call(t, h, "GET", "/api/stash/show?gitDir="+url.QueryEscape(dir)+"&hash=nope", ""); w.Code != 400 || m["error"] != "the stash list changed; reload" {
		t.Fatalf("stale show = %d %v", w.Code, m)
	}

	// Apply keeps it, pop removes it; both need a clean folder.
	if w, m := call(t, h, "POST", "/api/stash/apply", `{`+gd+`,"hash":"`+hash+`"}`); w.Code != 200 {
		t.Fatalf("apply = %d %v", w.Code, m)
	}
	if git("status", "--porcelain") == "" || git("stash", "list") == "" {
		t.Fatal("apply must keep the stash and change the files")
	}
	if w, m := call(t, h, "POST", "/api/stash/pop", `{`+gd+`,"hash":"`+hash+`"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "uncommitted changes") {
		t.Fatalf("pop on dirty = %d %v", w.Code, m)
	}
	git("add", "-A")
	git("stash", "push", "-q", "-m", "second") // folder clean again
	_, m = call(t, h, "GET", "/api/stashes?gitDir="+url.QueryEscape(dir), "")
	list, _ = m["data"].([]any)
	second := fmt.Sprint(list[0].(map[string]any)["hash"])
	if w, m := call(t, h, "POST", "/api/stash/pop", `{`+gd+`,"hash":"`+hash+`"}`); w.Code != 200 {
		t.Fatalf("pop = %d %v", w.Code, m)
	}
	if git("stash", "list", "--format=%H") != second {
		t.Fatalf("pop must remove only that stash: %q", git("stash", "list", "--format=%H"))
	}
	git("add", "-A")
	git("commit", "-q", "-m", "popped")

	// Branch from the remaining stash; the stale hash is refused after.
	if w, m := call(t, h, "POST", "/api/stash/branch", `{`+gd+`,"hash":"`+second+`","name":"from-stash"}`); w.Code != 200 || git("branch", "--show-current") != "from-stash" {
		t.Fatalf("branch = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/stash/drop", `{`+gd+`,"hash":"`+second+`"}`); w.Code != 400 || m["error"] != "the stash list changed; reload" {
		t.Fatalf("stale drop = %d %v", w.Code, m)
	}

	// Drop and undo: the stash comes back, with its message.
	git("add", "-A")
	git("commit", "-q", "-m", "on the new branch")
	write("third\n")
	w, m = call(t, h, "POST", "/api/stash/save", `{`+gd+`,"message":"third"}`)
	st, _ = m["data"].(map[string]any)
	third := fmt.Sprint(st["hash"])
	if w.Code != 200 || third == "" {
		t.Fatalf("save third = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/stash/drop", `{`+gd+`,"hash":"`+third+`"}`); w.Code != 200 || git("stash", "list") != "" {
		t.Fatalf("drop = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
	if got := git("stash", "list", "--format=%H %gs"); got != third+" On from-stash: third" {
		t.Fatalf("after undo: %q", got)
	}
}

// Hunks and lines: the diff returns them with a fingerprint; stage, unstage
// and discard take hunk and line indexes and refuse a stale fingerprint.
func TestHunkRoutes(t *testing.T) {
	dir, git, _ := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + url.QueryEscape(dir)
	var base []string
	for i := 1; i <= 30; i++ {
		base = append(base, fmt.Sprintf("row %d", i))
	}
	put := func(lines []string) {
		if err := os.WriteFile(filepath.Join(dir, "h.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(base)
	git("add", "h.txt")
	git("commit", "-q", "-m", "h")
	edit := append([]string{}, base...)
	edit[1], edit[14], edit[27] = "ROW 2", "ROW 15", "ROW 28"
	put(edit)

	diff := func(staged string) map[string]any {
		t.Helper()
		w, m := call(t, h, "GET", "/api/repo/diff?"+q+"&path=h.txt"+staged, "")
		d, _ := m["data"].(map[string]any)
		if w.Code != 200 {
			t.Fatalf("diff = %d %v", w.Code, m)
		}
		return d
	}
	d := diff("")
	hunks, _ := d["hunks"].([]any)
	fp, _ := d["fingerprint"].(string)
	if len(hunks) != 3 || fp == "" || !strings.Contains(fmt.Sprint(d["text"]), "+ROW 15") {
		t.Fatalf("diff: %v", d)
	}
	first, _ := hunks[0].(map[string]any)
	lines, _ := first["lines"].([]any)
	l0, _ := lines[0].(map[string]any)
	if first["index"].(float64) != 0 || !strings.HasPrefix(fmt.Sprint(first["header"]), "@@ ") || l0["kind"] != "context" || l0["old"].(float64) != 1 {
		t.Fatalf("hunk 0: %v", first)
	}

	// A stale fingerprint is refused and changes nothing.
	w, m := call(t, h, "POST", "/api/repo/stage-hunks", `{`+gd+`,"path":"h.txt","fingerprint":"0000","hunks":[0]}`)
	if w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "the file changed since you looked: reload") || git("diff", "--cached", "--name-only") != "" {
		t.Fatalf("stale = %d %v", w.Code, m)
	}
	// Stage hunk 1, then only the added line of hunk 2.
	if w, m := call(t, h, "POST", "/api/repo/stage-hunks", `{`+gd+`,"path":"h.txt","fingerprint":"`+fp+`","hunks":[1]}`); w.Code != 200 || !strings.Contains(git("diff", "--cached"), "+ROW 15") {
		t.Fatalf("stage hunk = %d %v", w.Code, m)
	}
	// The same fingerprint is stale now: the diff changed.
	if w, _ := call(t, h, "POST", "/api/repo/stage-hunks", `{`+gd+`,"path":"h.txt","fingerprint":"`+fp+`","hunks":[0]}`); w.Code != 400 {
		t.Fatalf("a used fingerprint must be refused, got %d", w.Code)
	}
	d = diff("")
	fp, _ = d["fingerprint"].(string)
	hs, _ := d["hunks"].([]any)
	last, _ := hs[1].(map[string]any)
	add := -1
	for j, l := range last["lines"].([]any) {
		if l.(map[string]any)["kind"] == "add" {
			add = j
		}
	}
	body := fmt.Sprintf(`{%s,"path":"h.txt","fingerprint":%q,"lines":{"1":[%d]}}`, gd, fp, add)
	if w, m := call(t, h, "POST", "/api/repo/stage-hunks", body); w.Code != 200 || !strings.Contains(git("diff", "--cached"), "+ROW 28") || strings.Contains(git("diff", "--cached"), "-row 28") {
		t.Fatalf("stage line = %d %v\n%s", w.Code, m, git("diff", "--cached"))
	}
	// Unstage hunk 0 of the staged diff.
	sd := diff("&staged=1")
	sfp, _ := sd["fingerprint"].(string)
	if w, m := call(t, h, "POST", "/api/repo/unstage-hunks", `{`+gd+`,"path":"h.txt","fingerprint":"`+sfp+`","hunks":[0]}`); w.Code != 200 || strings.Contains(git("diff", "--cached"), "ROW 15") {
		t.Fatalf("unstage hunk = %d %v", w.Code, m)
	}
	// Discard hunk 0 of the unstaged diff: kept in a stash, with the way back.
	d = diff("")
	fp, _ = d["fingerprint"].(string)
	w, m = call(t, h, "POST", "/api/repo/discard-hunks", `{`+gd+`,"path":"h.txt","fingerprint":"`+fp+`","hunks":[0]}`)
	r, _ := m["data"].(map[string]any)
	if w.Code != 200 || !strings.Contains(fmt.Sprint(r["stash"]), "gitswitch discard:") || !strings.Contains(fmt.Sprint(r["restore"]), "git restore --source=") {
		t.Fatalf("discard hunk = %d %v", w.Code, m)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "h.txt")); strings.Contains(string(b), "ROW 2\n") {
		t.Fatalf("hunk 0 is still in the file:\n%s", b)
	}
	if !strings.Contains(git("stash", "list"), "gitswitch discard:") || !strings.Contains(git("show", "stash@{0}:h.txt"), "ROW 2") {
		t.Fatal("the discarded hunk is not in the stash")
	}
	// Bad requests are refused: no hunk, a context line, a new file's discard.
	d = diff("")
	fp, _ = d["fingerprint"].(string)
	for _, b := range []string{
		`{` + gd + `,"path":"h.txt","fingerprint":"` + fp + `"}`,
		`{` + gd + `,"path":"h.txt","fingerprint":"` + fp + `","hunks":[9]}`,
		`{` + gd + `,"path":"h.txt","fingerprint":"` + fp + `","lines":{"0":[0]}}`,
		`{` + gd + `,"path":"h.txt","fingerprint":"` + fp + `","lines":{"x":[1]}}`,
		`{` + gd + `,"path":"../h.txt","fingerprint":"` + fp + `","hunks":[0]}`,
	} {
		if w, m := call(t, h, "POST", "/api/repo/stage-hunks", b); w.Code != 400 || m["error"] == nil {
			t.Errorf("%s = %d %v, want an error", b, w.Code, m)
		}
	}
	// A new file: lines can be staged; discarding in parts is refused.
	if err := os.WriteFile(filepath.Join(dir, "n.txt"), []byte("n1\nn2\nn3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, m = call(t, h, "GET", "/api/repo/diff?"+q+"&path=n.txt", "")
	nd, _ := m["data"].(map[string]any)
	nfp, _ := nd["fingerprint"].(string)
	if w, m := call(t, h, "POST", "/api/repo/discard-hunks", `{`+gd+`,"path":"n.txt","fingerprint":"`+nfp+`","hunks":[0]}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "whole file") {
		t.Fatalf("discard part of a new file = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/stage-hunks", `{`+gd+`,"path":"n.txt","fingerprint":"`+nfp+`","lines":{"0":[0,2]}}`); w.Code != 200 || git("show", ":n.txt") != "n1\nn3" {
		t.Fatalf("stage lines of a new file = %d %v", w.Code, m)
	}
}
