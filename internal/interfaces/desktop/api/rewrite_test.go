package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// Rewrite history through the API: the todo, a reorder with a reword and a
// squash, undo, reword, fixup-into, refusals, and a clash that stops then
// finishes through /api/operation and can be undone.
func TestRewriteAPI(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	q := "gitDir=" + filepath.ToSlash(dir)
	add := func(file, text, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	git("switch", "-q", "side") // "side" commit, then two more
	a := add("a.txt", "a\n", "add a")
	b := add("b.txt", "b\n", "add b")
	orig := b

	w, m := call(t, h, "GET", "/api/rewrite/todo?"+q+"&since="+a, "")
	d, _ := m["data"].(map[string]any)
	cs, _ := d["commits"].([]any)
	if w.Code != 200 || d["branch"] != "side" || d["blocked"] != "" || len(cs) != 2 || d["base"] == "" {
		t.Fatalf("todo = %d %v", w.Code, m)
	}
	c0, _ := cs[0].(map[string]any)
	if c0["hash"] != a || c0["subject"] != "add a" || c0["files"] != float64(1) || c0["merge"] != false || c0["author"] != "T" || c0["short"] == "" {
		t.Fatalf("commit = %v", c0)
	}
	if w, m := call(t, h, "GET", "/api/rewrite/todo?"+q+"&since=nope", ""); w.Code != 400 {
		t.Fatalf("bad since = %d %v", w.Code, m)
	}

	// reorder and reword
	body := `{` + gd + `,"since":"` + a + `","steps":[{"hash":"` + b + `","action":"pick"},{"hash":"` + a + `","action":"reword","message":"add a (reworded)"}]}`
	w, m = call(t, h, "POST", "/api/rewrite", body)
	d, _ = m["data"].(map[string]any)
	if w.Code != 200 || d["outcome"] != "done" || d["before"] != float64(2) || d["after"] != float64(2) || fmt.Sprint(d["conflicts"]) != "[]" ||
		d["hash"] != git("rev-parse", "HEAD") || len(d["summary"].([]any)) != 2 {
		t.Fatalf("rewrite = %d %v", w.Code, m)
	}
	if got := git("log", "--format=%s", "-2"); got != "add a (reworded)\nadd b" {
		t.Fatalf("log = %q", got)
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != orig {
		t.Fatalf("undo = %d %v", w.Code, m)
	}

	// refusals: missing step, squash first, nothing to change, empty steps
	for _, c := range []struct{ body, want string }{
		{`{` + gd + `,"since":"` + a + `","steps":[{"hash":"` + a + `","action":"pick"}]}`, "is missing"},
		{`{` + gd + `,"since":"` + a + `","steps":[{"hash":"` + a + `","action":"squash"},{"hash":"` + b + `","action":"pick"}]}`, "needs a commit to fold into"},
		{`{` + gd + `,"since":"` + a + `","steps":[{"hash":"` + a + `","action":"pick"},{"hash":"` + b + `","action":"pick"}]}`, "change nothing"},
		{`{` + gd + `,"since":"` + a + `","steps":[]}`, "steps is empty"},
	} {
		if w, m := call(t, h, "POST", "/api/rewrite", c.body); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), c.want) {
			t.Fatalf("%s: %d %v", c.want, w.Code, m)
		}
	}
	if git("rev-parse", "HEAD") != orig {
		t.Fatal("a refused rewrite moved the branch")
	}

	// reword an older commit
	w, m = call(t, h, "POST", "/api/rewrite/reword", `{`+gd+`,"hash":"`+a+`","message":"first, reworded"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["outcome"] != "done" || git("log", "--format=%s", "-2") != "add b\nfirst, reworded" {
		t.Fatalf("reword = %d %v", w.Code, m)
	}
	call(t, h, "POST", "/api/undo", `{`+gd+`}`)

	// fold staged changes into an older commit
	if w, m := call(t, h, "POST", "/api/rewrite/fixup-into", `{`+gd+`,"hash":"`+a+`"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing is staged") {
		t.Fatalf("fixup nothing staged = %d %v", w.Code, m)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	w, m = call(t, h, "POST", "/api/rewrite/fixup-into", `{`+gd+`,"hash":"`+a+`"}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["outcome"] != "done" || git("show", "HEAD~1:a.txt") != "a fixed" || git("status", "--porcelain") != "" {
		t.Fatalf("fixup-into = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != orig || git("status", "--porcelain") != "M a.txt" {
		t.Fatalf("undo fixup = %d %v", w.Code, m)
	}
	git("checkout", "--", "a.txt")

	// a clash stops; settle it, continue through the operation API, then
	// the rewrite is undoable (finish is a no-op afterwards).
	write("x\n") // f.txt
	git("commit", "-q", "-am", "x")
	c1 := git("rev-parse", "HEAD")
	write("x\ny\n")
	git("commit", "-q", "-am", "y")
	c2 := git("rev-parse", "HEAD")
	tip := c2
	body = `{` + gd + `,"since":"` + c1 + `","steps":[{"hash":"` + c2 + `","action":"pick"},{"hash":"` + c1 + `","action":"pick"}]}`
	w, m = call(t, h, "POST", "/api/rewrite", body)
	d, _ = m["data"].(map[string]any)
	if w.Code != 200 || d["outcome"] != "conflicts" || fmt.Sprint(d["conflicts"]) != "[f.txt]" {
		t.Fatalf("clash = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "GET", "/api/rewrite/todo?"+q, ""); w.Code != 200 || m["data"].(map[string]any)["pending"] != true || m["data"].(map[string]any)["blocked"] == "" {
		t.Fatalf("todo while stopped = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/rewrite/finish", `{`+gd+`}`); w.Code != 200 || m["data"].(map[string]any)["pending"] != true {
		t.Fatalf("finish while stopped = %d %v", w.Code, m)
	}
	for i := 0; i < 3; i++ {
		w, m = call(t, h, "GET", "/api/operation?"+q, "")
		if op, _ := m["data"].(map[string]any); op["kind"] != "rebase" {
			break
		}
		call(t, h, "POST", "/api/conflict", `{`+gd+`,"path":"f.txt","action":"save","text":"x\ny\n"}`)
		call(t, h, "POST", "/api/operation", `{`+gd+`,"action":"continue"}`)
	}
	if git("status", "--porcelain") != "" || gitOp(dir) {
		t.Fatalf("still busy: %s", git("status"))
	}
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 || git("rev-parse", "HEAD") != tip {
		t.Fatalf("undo after continue = %d %v (HEAD %s want %s)", w.Code, m, git("rev-parse", "HEAD"), tip)
	}
}

func gitOp(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git", "rebase-merge"))
	return err == nil
}
