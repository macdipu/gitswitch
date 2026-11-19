package api

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// Worktrees: list, add (new branch, existing branch refused when checked
// out, nested path refused), dirty remove refused, force remove, lock,
// prune with a dry run first.
func TestWorktreesAPI(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":` + quote(filepath.ToSlash(dir))
	base := t.TempDir()
	feat, other := filepath.Join(base, "feat"), filepath.Join(base, "other")

	_, m := call(t, h, "GET", "/api/worktrees?gitDir="+url.QueryEscape(dir), "")
	list, _ := m["data"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["main"] != true || list[0].(map[string]any)["branch"] != "main" {
		t.Fatalf("list = %v", m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(filepath.Join(dir, "inner"))+`,"newBranch":"x"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "inside this repo's own folder") {
		t.Fatalf("nested = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(feat)+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "say what to check out") {
		t.Fatalf("no branch = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(feat)+`,"branch":"main"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "already checked out in") {
		t.Fatalf("main again = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(feat)+`,"newBranch":"feat","from":"main"}`)
	d, _ := m["data"].(map[string]any)
	if w.Code != 200 || d["branch"] != "feat" || d["registered"] != false || d["warnings"] == nil {
		t.Fatalf("add = %d %v", w.Code, m)
	}
	// asking through the worktree folder lists the same repo
	_, m = call(t, h, "GET", "/api/worktrees?gitDir="+url.QueryEscape(feat), "")
	list, _ = m["data"].([]any)
	if len(list) != 2 || list[1].(map[string]any)["branch"] != "feat" || list[1].(map[string]any)["changes"] == nil {
		t.Fatalf("list after add = %v", m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(other)+`,"branch":"feat"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "already checked out in") {
		t.Fatalf("feat again = %d %v", w.Code, m)
	}
	// detached, registered in the repo list
	w, m = call(t, h, "POST", "/api/worktree/add", `{`+gd+`,"path":`+quote(other)+`,"detach":"HEAD","register":true}`)
	if d, _ := m["data"].(map[string]any); w.Code != 200 || d["registered"] != true {
		t.Fatalf("add detached = %d %v", w.Code, m)
	}
	_ = write

	// dirty: refused with what would be lost, force removes
	if err := os.WriteFile(filepath.Join(feat, "n.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, m := call(t, h, "POST", "/api/worktree/remove", `{`+gd+`,"path":`+quote(feat)+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "would be lost") || !strings.Contains(fmt.Sprint(m["error"]), "1 new") {
		t.Fatalf("dirty remove = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/lock", `{`+gd+`,"path":`+quote(feat)+`,"reason":"usb"}`); w.Code != 200 {
		t.Fatalf("lock = %d %v", w.Code, m)
	}
	_, m = call(t, h, "GET", "/api/worktrees?gitDir="+url.QueryEscape(dir), "")
	if e := m["data"].([]any)[1].(map[string]any); e["locked"] != true || e["lockReason"] != "usb" {
		t.Fatalf("locked = %v", e)
	}
	if w, m := call(t, h, "POST", "/api/worktree/remove", `{`+gd+`,"path":`+quote(feat)+`,"force":true}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "unlock it first") {
		t.Fatalf("locked remove = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/unlock", `{`+gd+`,"path":`+quote(feat)+`}`); w.Code != 200 {
		t.Fatalf("unlock = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/worktree/remove", `{`+gd+`,"path":`+quote(feat)+`,"force":true}`); w.Code != 200 {
		t.Fatalf("force remove = %d %v", w.Code, m)
	}
	if git("branch", "--list", "feat") == "" {
		t.Error("the branch was deleted with the worktree")
	}
	// prune: dry run lists, a real run forgets
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	_, m = call(t, h, "POST", "/api/worktree/prune", `{`+gd+`,"dryRun":true}`)
	d, _ = m["data"].(map[string]any)
	if p, _ := d["pruned"].([]any); len(p) != 1 || d["dryRun"] != true {
		t.Fatalf("prune plan = %v", m)
	}
	if n := strings.Count(git("worktree", "list"), "\n"); n != 1 {
		t.Fatalf("a dry run changed the list (%d lines)", n+1)
	}
	_, m = call(t, h, "POST", "/api/worktree/prune", `{`+gd+`}`)
	d, _ = m["data"].(map[string]any)
	if p, _ := d["pruned"].([]any); len(p) != 1 || d["dryRun"] != false || git("worktree", "list") != strings.Split(git("worktree", "list"), "\n")[0] {
		t.Fatalf("prune = %v / %s", m, git("worktree", "list"))
	}
	_, m = call(t, h, "POST", "/api/worktree/prune", `{`+gd+`}`)
	if d, _ := m["data"].(map[string]any); fmt.Sprint(d["pruned"]) != "[]" {
		t.Fatalf("nothing left to prune = %v", m)
	}
}
