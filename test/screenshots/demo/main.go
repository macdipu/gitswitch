// Command demo serves the desktop app's page and API over HTTP with made-up
// accounts and repos, for the README screenshots (see ../run.sh). It
// builds its own home folder and changes nothing outside it.
//
//	demo <empty folder> <address>
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	"github.com/macdipu/gitswitch/internal/interfaces/desktop/api"
)

var home string

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: demo <empty folder> <address>")
	}
	home, _ = filepath.Abs(os.Args[1])
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME": filepath.Join(home, ".cache"), "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "GITSWITCH_TEST_NO_SYSTEM": "1"} {
		os.Setenv(k, v)
	}
	os.Unsetenv("GIT_CONFIG_GLOBAL")
	seed()
	log.Printf("demo GitSwitch %s on http://%s", bootstrap.Version, os.Args[2])
	log.Fatal(http.ListenAndServe(os.Args[2], api.Handler(bootstrap.New(false, io.Discard), nil)))
}

func git(dir string, env []string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(path, text string, mode os.FileMode) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		log.Fatal(err)
	}
}

type person struct{ id, name, email, user string }

var (
	work     = person{"work", "Alex Rivera", "alex@acme.dev", "alex-acme"}
	personal = person{"personal", "Alex Rivera", "alex.rivera@gmail.com", "alexrivera"}
	client   = person{"client", "Alex Rivera", "alex@globex.io", "alex-globex"}
)

func seed() {
	g := func(args ...string) { git(home, nil, append([]string{"config", "--global"}, args...)...) }
	// Connected to git, as after `gitswitch install`: the hook program and
	// the desktop app sit where install puts them.
	bin := filepath.Join(home, ".local", "bin")
	write(filepath.Join(bin, "gitswitch"), "", 0o755)
	write(filepath.Join(bin, "gitswitch-app"), "", 0o755)
	g("core.sshCommand", `"`+filepath.ToSlash(filepath.Join(bin, "gitswitch"))+`" ssh`)
	g("user.useConfigOnly", "true")
	g("init.defaultBranch", "main")
	for _, p := range []person{work, personal, client} {
		key := filepath.Join(home, ".ssh", "id_ed25519_gitswitch_"+p.id)
		write(key, "demo key\n", 0o600)
		write(key+".pub", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoKeyOnlyForScreenshots "+p.email+"\n", 0o644)
		for k, v := range map[string]string{"name": p.name, "email": p.email, "host": "github.com", "key": key, "user": p.user} {
			g("gitswitch-account."+p.id+"."+k, v)
		}
	}
	// GitHub's published host key, as after a first `gitswitch test`.
	write(filepath.Join(home, ".ssh", "known_hosts"), "github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n", 0o644)
	g("gitswitch.default", "work")
	g("--add", "gitswitch.rule", "owner:acme-corp=work")
	g("--add", "gitswitch.rule", "owner:globex=client")
	g("--add", "gitswitch.rule", "folder:"+filepath.Join(home, "personal")+"=personal")
	code := filepath.Join(home, "code")
	g("--add", "gitswitch.scanFolder", code)

	acmeAPI(filepath.Join(code, "acme-api"))
	simple(filepath.Join(code, "acme-web"), "git@github.com:acme-corp/web.git", work, "Landing page")
	simple(filepath.Join(code, "globex-portal"), "git@github.com:globex/portal.git", client, "Customer portal")
	// Outside the projects folder, added by hand; the folder rule picks its account.
	dot := filepath.Join(home, "personal", "dotfiles")
	simple(dot, "git@github.com:alexrivera/dotfiles.git", personal, "zsh and git settings")
	git(dot, nil, "config", "--unset", "gitswitch.account")
	g("--add", "gitswitch.repo", dot)
}

// commit makes a commit as p, days ago, with an optional body.
func commit(dir string, p person, ago time.Duration, file, subject string, body ...string) string {
	write(filepath.Join(dir, file), subject+"\n", 0o644)
	git(dir, nil, "add", file)
	when := time.Now().Add(-ago).Format(time.RFC3339)
	msg := subject
	if len(body) > 0 {
		msg += "\n\n" + strings.Join(body, "\n")
	}
	env := []string{"GIT_AUTHOR_NAME=" + p.name, "GIT_AUTHOR_EMAIL=" + p.email, "GIT_COMMITTER_NAME=" + p.name,
		"GIT_COMMITTER_EMAIL=" + p.email, "GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}
	git(dir, env, "commit", "-q", "-m", msg)
	return git(dir, nil, "rev-parse", "HEAD")
}

func repo(dir, url string, p person) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	git(dir, nil, "init", "-q")
	git(dir, nil, "remote", "add", "origin", url)
	git(dir, nil, "config", "gitswitch.account", p.id)
	git(dir, nil, "config", "user.name", p.name)
	git(dir, nil, "config", "user.email", p.email)
}

// pushed marks commits as pushed: origin's branch points at them.
func pushed(dir, branch, hash string) {
	git(dir, nil, "update-ref", "refs/remotes/origin/"+branch, hash)
	git(dir, nil, "branch", "-q", "--set-upstream-to=origin/"+branch, branch)
}

func simple(dir, url string, p person, subject string) {
	repo(dir, url, p)
	commit(dir, p, 90*24*time.Hour, "README.md", "Initial commit")
	h := commit(dir, p, 20*24*time.Hour, "index.md", subject)
	pushed(dir, "main", h)
}

func acmeAPI(dir string) {
	day := 24 * time.Hour
	repo(dir, "git@github.com:acme-corp/api.git", work)
	commit(dir, work, 400*day, "main.go", "Initial API skeleton")
	commit(dir, work, 395*day, "health.go", "Add health endpoint")
	commit(dir, work, 390*day, "go.mod", "Bump Go to 1.27")
	git(dir, nil, "switch", "-q", "-c", "feature/auth")
	commit(dir, work, 385*day, "auth.go", "Add JWT middleware",
		"Every route under /v1 now needs a bearer token. Tokens are", "checked against the signing key in AUTH_KEY.")
	commit(dir, personal, 382*day, "token.go", "Fix token expiry check")
	commit(dir, work, 380*day, "auth_test.go", "Add login handler tests")
	git(dir, nil, "switch", "-q", "main")
	when := time.Now().Add(-378 * day).Format(time.RFC3339)
	git(dir, []string{"GIT_AUTHOR_NAME=" + work.name, "GIT_AUTHOR_EMAIL=" + work.email, "GIT_COMMITTER_NAME=" + work.name,
		"GIT_COMMITTER_EMAIL=" + work.email, "GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when},
		"merge", "-q", "--no-ff", "-m", "Merge branch 'feature/auth'", "feature/auth")
	git(dir, nil, "update-ref", "refs/remotes/origin/feature/auth", git(dir, nil, "rev-parse", "feature/auth"))
	git(dir, nil, "switch", "-q", "-c", "feature/billing")
	commit(dir, work, 370*day, "invoice.go", "Add invoice model")
	b := commit(dir, work, 365*day, "email.go", "Send invoice emails")
	git(dir, nil, "update-ref", "refs/remotes/origin/feature/billing", b)
	git(dir, nil, "switch", "-q", "main")
	m := git(dir, nil, "rev-parse", "HEAD")
	pushed(dir, "main", m)
	commit(dir, work, 2*time.Hour, "ratelimit.go", "Rate-limit public routes",
		"Anonymous clients get 60 requests a minute per IP; signed-in", "clients get 600. Limits are kept in memory and reset on restart.",
		"", "- 429 responses carry Retry-After", "- /health is never limited")
	commit(dir, personal, 40*time.Minute, "README.md", "Update README badges")
	fmt.Println("demo repo:", dir)
}
