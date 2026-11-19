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

// Rebase through the API: plan, a clash that stops it, continue through
// /api/operation, undo; then abort; then a refusal; then finish after git
// itself continued.
func TestRebaseAPI(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + filepath.ToSlash(dir)
	git("switch", "-q", "main")
	write("main\n")
	git("commit", "-q", "-am", "main")
	git("switch", "-q", "side")
	orig := git("rev-parse", "HEAD")

	w, m := call(t, h, "GET", "/api/rebase/plan?"+q+"&onto=main", "")
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["branch"] != "side" || d["replayedCount"] != float64(1) || d["incomingCount"] != float64(1) ||
		fmt.Sprint(d["bothChanged"]) != "[f.txt]" || d["blocked"] != "" || d["upToDate"] != false || d["fastForward"] != false ||
		!strings.Contains(fmt.Sprint(d["summary"]), "estimate") || len(d["replayed"].([]any)) != 1 {
		t.Fatalf("plan = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "GET", "/api/rebase/plan?"+q+"&onto=nope", ""); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "no branch, tag or commit") {
		t.Fatalf("bad onto = %d %v", w.Code, m)
	}

	w, m = call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`)
	d, _ = m["data"].(map[string]any)
	if w.Code != 200 || d["outcome"] != "conflicts" || fmt.Sprint(d["conflicts"]) != "[f.txt]" || d["branch"] != "side" || d["onto"] != "main" {
		t.Fatalf("rebase = %d %v", w.Code, m)
	}
	w, m = call(t, h, "GET", "/api/operation?"+q, "")
	if d, _ := m["data"].(map[string]any); d["kind"] != "rebase" {
		t.Fatalf("operation = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/rebase/finish", `{`+gd+`}`); w.Code != 200 || m["data"].(map[string]any)["recorded"] != false {
		t.Fatalf("finish while stopped = %d %v", w.Code, m)
	}
	// Abort: the repo is as before, nothing to undo.
	if w, m := call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"abort"}`); w.Code != 200 || git("rev-parse", "HEAD") != orig {
		t.Fatalf("abort = %d %v", w.Code, m)
	}
	if w, _ := call(t, h, "GET", "/api/undo?"+q, ""); w.Code == 200 && strings.Contains(w.Body.String(), `"rebase"`) {
		t.Fatalf("aborted rebase offered for undo: %s", w.Body.String())
	}

	// Again, fix and continue through the API: undo works afterwards.
	call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`)
	if w, m := call(t, h, "POST", "/api/conflict", `{`+gd+`,"path":"f.txt","action":"save","text":"both\n"}`); w.Code != 200 {
		t.Fatalf("save = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"continue"}`); w.Code != 200 || m["data"].(map[string]any)["kind"] != "" {
		t.Fatalf("continue = %d %v", w.Code, m)
	}
	if git("log", "--format=%s", "-2") != "side\nmain" {
		t.Fatalf("log: %s", git("log", "--format=%s"))
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != orig || git("status", "--porcelain") != "" {
		t.Fatalf("undo = %d %v", w.Code, m)
	}

	// A refusal: uncommitted changes.
	write("dirty\n")
	if w, m := call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "commit, park or discard them first") {
		t.Fatalf("dirty = %d %v", w.Code, m)
	}
	git("checkout", "--", "f.txt")

	// A clean rebase, and up to date afterwards.
	git("reset", "-q", "--hard", orig)
	git("switch", "-q", "-c", "clean", "main~1")
	if err := os.WriteFile(filepath.Join(dir, "g.txt"), []byte("g\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "g.txt")
	git("commit", "-q", "-m", "g")
	w, m = call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`)
	d, _ = m["data"].(map[string]any)
	if w.Code != 200 || d["outcome"] != "done" || d["replayed"] != float64(1) || d["hash"] != git("rev-parse", "HEAD") || d["short"] == "" || fmt.Sprint(d["conflicts"]) != "[]" {
		t.Fatalf("clean = %d %v", w.Code, m)
	}
	w, m = call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["outcome"] != "up-to-date" {
		t.Fatalf("again = %d %v", w.Code, m)
	}
}

// Continued with plain git, /api/rebase/finish still records the rebase.
func TestRebaseFinishAPI(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	git("switch", "-q", "main")
	write("main\n")
	git("commit", "-q", "-am", "main")
	git("switch", "-q", "side")
	orig := git("rev-parse", "HEAD")
	call(t, h, "POST", "/api/rebase", `{`+gd+`,"onto":"main"}`)
	write("both\n")
	git("add", "f.txt")
	cmd := exec.Command("git", "-C", dir, "rebase", "--continue")
	cmd.Env = append(os.Environ(), "GIT_EDITOR=:")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	if w, m := call(t, h, "POST", "/api/rebase/finish", `{`+gd+`}`); w.Code != 200 || m["data"].(map[string]any)["recorded"] != true {
		t.Fatalf("finish = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != orig {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
}
