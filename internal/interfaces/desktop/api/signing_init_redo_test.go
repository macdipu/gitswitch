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

func data(m map[string]any) map[string]any { d, _ := m["data"].(map[string]any); return d }

// Redo: GET /api/refs says what it would do next to "undo", POST /api/redo
// does it, and a used-up or blocked redo is an error.
func TestRedoRoute(t *testing.T) {
	dir, git, write := testRepo(t)
	h := Handler(bootstrap.New(false, nil), nil)
	gd := `"gitDir":"` + filepath.ToSlash(dir) + `"`
	refsRedo := func() any {
		t.Helper()
		_, m := call(t, h, "GET", "/api/refs?gitDir="+filepath.ToSlash(dir), "")
		return data(m)["redo"]
	}
	if w, m := call(t, h, "POST", "/api/redo", `{`+gd+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "nothing to redo") {
		t.Fatalf("redo with nothing = %d %v", w.Code, m)
	}
	if refsRedo() != nil {
		t.Fatalf("redo before an undo = %v", refsRedo())
	}
	write("mine\n")
	call(t, h, "POST", "/api/repo/commit", `{`+gd+`,"message":"Mine","all":true}`)
	c := git("rev-parse", "HEAD")
	if w, m := call(t, h, "POST", "/api/undo", `{`+gd+`}`); w.Code != 200 {
		t.Fatalf("undo = %d %v", w.Code, m)
	}
	r, _ := refsRedo().(map[string]any)
	if r["action"] != "commit" || r["blocked"] != "" || r["effect"] == "" || r["label"] == "" {
		t.Fatalf("refs redo = %v", r)
	}
	if w, m := call(t, h, "POST", "/api/redo", `{`+gd+`}`); w.Code != 200 || data(m)["label"] == "" || git("rev-parse", "HEAD") != c {
		t.Fatalf("redo = %d %v, HEAD %s want %s", w.Code, m, git("rev-parse", "HEAD"), c)
	}
	if refsRedo() != nil {
		t.Fatal("redo must be used up")
	}
	// Undo again, then move the branch: blocked, and the route says why.
	call(t, h, "POST", "/api/undo", `{`+gd+`}`)
	git("add", "-A")
	git("commit", "-q", "-m", "elsewhere")
	r, _ = refsRedo().(map[string]any)
	if r["blocked"] == "" || r["blocked"] == nil {
		t.Fatalf("blocked redo = %v", r)
	}
	if w, m := call(t, h, "POST", "/api/redo", `{`+gd+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "changed since the undo") {
		t.Fatalf("blocked redo = %d %v", w.Code, m)
	}
}

// Signatures: a signed commit verifies with the account's allowed signers
// file, an unsigned one says N, unknown hashes are left out and at most
// 200 are asked about.
func TestSignaturesRoute(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir, git, write := testRepo(t)
	home := filepath.Dir(dir)
	key := filepath.Join(home, "sign")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t@x", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	f := strings.Fields(string(pub))
	signers := filepath.Join(home, "allowed")
	if err := os.WriteFile(signers, []byte(`t@x.example namespaces="git" `+f[0]+" "+f[1]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unsigned := git("rev-parse", "HEAD")
	git("config", "gpg.format", "ssh")
	git("config", "user.signingkey", key)
	git("config", "gpg.ssh.allowedSignersFile", signers)
	write("signed\n")
	git("commit", "-q", "-a", "-S", "-m", "Signed")
	signed := git("rev-parse", "HEAD")

	h := Handler(bootstrap.New(false, nil), nil)
	q := "/api/signatures?gitDir=" + filepath.ToSlash(dir) + "&hashes="
	w, m := call(t, h, "GET", q+signed+","+unsigned+",deadbeefdeadbeef,zzz", "")
	d := data(m)
	s, _ := d[signed].(map[string]any)
	u, _ := d[unsigned].(map[string]any)
	if w.Code != 200 || s["status"] != "G" || s["verified"] != true || s["signer"] != "t@x.example" || s["key"] == "" {
		t.Fatalf("signed = %d %v", w.Code, m)
	}
	if u["status"] != "N" || u["verified"] != false {
		t.Fatalf("unsigned = %v", u)
	}
	if _, ok := d["deadbeefdeadbeef"]; ok || len(d) != 2 {
		t.Fatalf("unknown hashes must be left out: %v", d)
	}
	// The short hash is answered under the short hash.
	if _, m := call(t, h, "GET", q+signed[:8], ""); data(m)[signed[:8]] == nil {
		t.Fatalf("short hash: %v", m)
	}
	// Capped at 200: the real hash comes after 200 others and is not asked.
	var many []string
	for i := 0; i < 200; i++ {
		many = append(many, fmt.Sprintf("%040x", i+1))
	}
	if _, m := call(t, h, "GET", q+strings.Join(many, ",")+","+signed, ""); len(data(m)) != 0 {
		t.Fatalf("more than 200 hashes were asked: %v", data(m))
	}
	if w, m := call(t, h, "GET", "/api/signatures?gitDir="+filepath.ToSlash(home)+"&hashes="+signed, ""); w.Code != 400 {
		t.Fatalf("not a repo = %d %v", w.Code, m)
	}
}

// Set an account's signing over the API: the public key and where to add it
// come back, the account lists it, and off clears it.
func TestAccountSigningRoute(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		t.Setenv(k, v)
	}
	key := filepath.Join(home, "k")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	h := Handler(bootstrap.New(false, nil), nil)
	if w, m := call(t, h, "POST", "/api/account", `{"mode":"new","id":"me","email":"me@x.example","name":"Me","host":"github.com"}`); w.Code != 200 {
		t.Fatalf("add = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/account/signing", `{"id":"me","format":"ssh"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "needs a key") {
		t.Fatalf("ssh without any key = %d %v", w.Code, m)
	}
	w, m := call(t, h, "POST", "/api/account/signing", `{"id":"me","key":`+quote(key+".pub")+`}`)
	d := data(m)
	acc, _ := d["account"].(map[string]any)
	if w.Code != 200 || acc["signingFormat"] != "ssh" || !strings.HasPrefix(fmt.Sprint(d["public"]), "ssh-ed25519 ") ||
		!strings.Contains(fmt.Sprint(d["page"]), "github.com/settings/ssh/new") {
		t.Fatalf("set signing = %d %v", w.Code, m)
	}
	if strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("private key material in the response")
	}
	// Editing the account in the page keeps how it signs.
	call(t, h, "POST", "/api/account", `{"id":"me","email":"me2@x.example","name":"Me","host":"github.com"}`)
	_, m = call(t, h, "GET", "/api/state", "")
	var found map[string]any
	for _, a := range data(m)["accounts"].([]any) {
		if am := a.(map[string]any); am["id"] == "me" {
			found = am
		}
	}
	if found["signingFormat"] != "ssh" || found["email"] != "me2@x.example" {
		t.Fatalf("state account = %v", found)
	}
	if w, m := call(t, h, "POST", "/api/account/signing", `{"id":"me","format":"off"}`); w.Code != 200 || data(m)["public"] != "" {
		t.Fatalf("off = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/account/signing", `{"id":"me","format":"pgp-ish"}`); w.Code != 400 {
		t.Fatalf("bad format = %d %v", w.Code, m)
	}
}

// Start a repo and ask about git.
func TestRepoInitAndGitInfo(t *testing.T) {
	home := t.TempDir()
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM": "1", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		t.Setenv(k, v)
	}
	h := Handler(bootstrap.New(false, nil), nil)
	_, m := call(t, h, "GET", "/api/git-info", "")
	if g := data(m); g["installed"] != true || g["ok"] != true || g["version"] == "" || g["why"] != "" {
		t.Fatalf("git-info = %v", m)
	}
	call(t, h, "POST", "/api/account", `{"mode":"new","id":"a","email":"a@x.example","name":"A","host":"github.com"}`)
	call(t, h, "POST", "/api/account", `{"mode":"new","id":"b","email":"b@x.example","name":"B","host":"github.com"}`)
	dir := filepath.Join(home, "proj", "new")
	if w, m := call(t, h, "POST", "/api/repo/init", `{"path":`+quote(dir)+`}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "account") {
		t.Fatalf("two accounts, none chosen = %d %v", w.Code, m)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("nothing may be created before the account is chosen")
	}
	w, m := call(t, h, "POST", "/api/repo/init", `{"path":`+quote(dir)+`,"account":"b","branch":"trunk"}`)
	d := data(m)
	if w.Code != 200 || d["account"] != "b" || d["email"] != "b@x.example" || d["branch"] != "trunk" {
		t.Fatalf("init = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/init", `{"path":`+quote(dir)+`,"account":"b"}`); w.Code != 400 || !strings.Contains(fmt.Sprint(m["error"]), "already") {
		t.Fatalf("init again = %d %v", w.Code, m)
	}
	if w, m := call(t, h, "POST", "/api/repo/init", `{"path":`+quote(filepath.Join(home, "x"))+`,"account":"b","branch":"bad name"}`); w.Code != 400 {
		t.Fatalf("bad branch = %d %v", w.Code, m)
	}
}
