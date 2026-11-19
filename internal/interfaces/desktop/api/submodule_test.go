package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// The submodule routes against real git, with a local-path submodule (git
// blocks file transport unless protocol.file.allow is set).
func TestSubmoduleRoutes(t *testing.T) {
	dir, git, _ := testRepo(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":` + quote(dir)

	lib := filepath.Join(filepath.Dir(dir), "lib")
	os.MkdirAll(lib, 0o755)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "lib 1"}} {
		if out, err := exec.Command("git", append([]string{"-C", lib}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	list := func() []any {
		t.Helper()
		w, m := call(t, h, "GET", "/api/submodules?gitDir="+dir, "")
		if w.Code != 200 {
			t.Fatalf("list = %d %v", w.Code, m)
		}
		l, _ := m["data"].([]any)
		return l
	}
	if l := list(); l == nil || len(l) != 0 {
		t.Fatalf("no submodules should be an empty list, got %v", l)
	}
	// Add refuses a local path (only SSH and HTTPS URLs), an unsafe path, a taken one and a bad branch.
	for body, want := range map[string]string{
		`{` + gd + `,"url":` + quote(lib) + `,"path":"libs/lib"}`:              "not a repo URL",
		`{` + gd + `,"url":"git@github.com:o/r.git","path":"../up"}`:           "not a folder",
		`{` + gd + `,"url":"git@github.com:o/r.git","path":"f.txt"}`:           "already exists",
		`{` + gd + `,"url":"git@github.com:o/r.git","path":"x","branch":"-b"}`: "branch",
	} {
		if w, m := call(t, h, "POST", "/api/submodule/add", body); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), want) {
			t.Errorf("add %s = %d %v, want %q", body, w.Code, m, want)
		}
	}
	// A submodule added with git itself shows up in the list.
	git("submodule", "add", "-q", lib, "libs/lib")
	git("commit", "-q", "-m", "add lib")
	l := list()
	if len(l) != 1 {
		t.Fatalf("list = %v", l)
	}
	e := l[0].(map[string]any)
	if e["path"] != "libs/lib" || e["state"] != "up to date" || e["initialized"] != true || e["dirty"] != float64(0) || len(fmt.Sprint(e["recorded"])) != 40 {
		t.Fatalf("entry = %v", e)
	}
	// Changes inside block an update that would move it, and a removal.
	if err := os.WriteFile(filepath.Join(dir, "libs", "lib", "n.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, m := call(t, h, "POST", "/api/submodule/update", `{`+gd+`,"remote":true}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "libs/lib has 1 changed file(s)") {
		t.Fatalf("update = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/submodule/remove", `{`+gd+`,"path":"libs/lib"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "--force") {
		t.Fatalf("remove = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/submodule/update", `{`+gd+`,"path":"nope"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "not a submodule") {
		t.Fatalf("update nope = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/submodule/sync", `{`+gd+`}`); w.Code != 200 || data(m)["message"] == "" {
		t.Fatalf("sync = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/submodule/init", `{`+gd+`}`); w.Code != 200 || !strings.Contains(fmt.Sprint(data(m)["message"]), "already initialized") {
		t.Fatalf("init = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/submodule/remove", `{`+gd+`,"path":"libs/lib","force":true}`)
	if w.Code != 200 || data(m)["path"] != "libs/lib" || len(data(m)["deleted"].([]any)) < 3 {
		t.Fatalf("forced remove = %d %v", w.Code, m)
	}
	if _, err := os.Stat(filepath.Join(dir, "libs", "lib")); !os.IsNotExist(err) {
		t.Error("folder still there")
	}
	if l := list(); len(l) != 0 {
		t.Errorf("list after remove = %v", l)
	}
}
