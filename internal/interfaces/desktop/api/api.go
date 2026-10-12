// Package api serves the desktop app's page and the JSON API it calls.
// The window (package window) serves it in-process through Wails: there is
// no network port. It builds without cgo, so its tests run everywhere.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/interfaces/desktop/assets"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

type server struct {
	app        *bootstrap.App
	pickFolder func(title string) (string, error)

	mu    sync.Mutex
	clone *cloneJob // the latest clone; one runs at a time
}

// cloneJob is a clone running in the background; the page polls it.
type cloneJob struct {
	Running bool         `json:"running"`
	Line    string       `json:"line"` // git's latest progress line
	Error   string       `json:"error"`
	Hint    *remote.Hint `json:"hint"` // the error in plain words, when known
	Repo    *repo.Info   `json:"repo"`
}

// Handler serves the page at / and its JSON API under /api/. pickFolder
// shows the native folder picker ("" when cancelled); nil means there is
// none, and the page asks for a typed path instead.
func Handler(a *bootstrap.App, pickFolder func(title string) (string, error)) http.Handler {
	s := &server{app: a, pickFolder: pickFolder}
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(assets.Index)
			return
		}
		h, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if err := checkJSON(w, r); err != nil {
			writeJSON(w, nil, err)
			return
		}
		defer func() {
			if p := recover(); p != nil {
				writeJSON(w, nil, fmt.Errorf("bad request: %v", p))
			}
		}()
		v, err := h(r)
		writeJSON(w, v, err)
	})
}

type handler func(r *http.Request) (any, error)

func (s *server) routes() map[string]handler {
	a := s.app
	return map[string]handler{
		"GET /api/state":           s.state,
		"POST /api/account":        s.saveAccount,
		"POST /api/account/remove": s.removeAccount,
		"POST /api/account/rename": func(r *http.Request) (any, error) {
			b := body(r)
			n, err := a.Accounts.Rename(str(b, "id"), str(b, "newId"))
			return map[string]int{"repos": n}, err
		},
		"GET /api/keys":     func(r *http.Request) (any, error) { return a.Keys.Existing(), nil },
		"POST /api/default": func(r *http.Request) (any, error) { return nil, a.Accounts.SetDefault(str(body(r), "id")) },
		"POST /api/test": func(r *http.Request) (any, error) {
			res, err := a.Keys.Test(str(body(r), "id"), true)
			var hint *remote.Hint
			if !res.OK {
				hint = remote.Explain(res.Output)
			}
			return struct {
				ssh.TestResult
				Hint *remote.Hint `json:"hint"`
			}{res, hint}, err
		},
		"GET /api/pubkey": func(r *http.Request) (any, error) {
			k, help, err := a.Keys.PublicKey(r.URL.Query().Get("id"))
			return map[string]any{"key": k, "page": help.Page, "steps": help.Steps}, err
		},
		"POST /api/open": func(r *http.Request) (any, error) {
			u := str(body(r), "url")
			if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
				return nil, errors.New("only web links can be opened")
			}
			return nil, process.OpenBrowser(u)
		},
		"POST /api/copy": func(r *http.Request) (any, error) {
			return map[string]bool{"ok": process.CopyToClipboard(str(body(r), "text"))}, nil
		},
		"POST /api/rule": func(r *http.Request) (any, error) {
			b := body(r)
			return nil, a.Accounts.AddRule(account.Rule{Kind: str(b, "kind"), Pattern: str(b, "pattern"), Account: str(b, "account")})
		},
		// The rule is named by its values, not its place in the list, so a
		// page showing an older list cannot remove a different rule.
		"POST /api/rule/remove": func(r *http.Request) (any, error) {
			b := body(r)
			rule := account.Rule{Kind: str(b, "kind"), Pattern: str(b, "pattern"), Account: str(b, "account")}
			if rule.Kind == "" || rule.Pattern == "" || rule.Account == "" {
				return nil, errors.New("say which rule to remove: kind, pattern and account")
			}
			for _, x := range a.Accounts.Config().Rules {
				if x == rule {
					return nil, a.Accounts.RemoveRule(rule)
				}
			}
			return nil, errors.New("that rule is no longer there; the list has been reloaded")
		},
		"POST /api/settings":      s.settings,
		"POST /api/folder":        func(r *http.Request) (any, error) { return nil, a.Accounts.AddScanFolder(str(body(r), "path")) },
		"POST /api/folder/remove": func(r *http.Request) (any, error) { return nil, a.Accounts.RemoveScanFolder(str(body(r), "path")) },
		"GET /api/repos":          func(r *http.Request) (any, error) { return a.Repos.List(), nil },
		"POST /api/repo/add":      func(r *http.Request) (any, error) { return a.Repos.Add(str(body(r), "path")) },
		"POST /api/repo/remove":   func(r *http.Request) (any, error) { return nil, a.Repos.Remove(str(body(r), "path")) },
		"POST /api/repo/pin": func(r *http.Request) (any, error) {
			b := body(r)
			on, _ := b["on"].(bool)
			return nil, a.Repos.Pin(str(b, "path"), on)
		},
		"POST /api/repo/open": func(r *http.Request) (any, error) {
			b := body(r)
			ed, _ := b["editor"].(bool)
			return nil, a.Repos.Open(str(b, "path"), ed)
		},
		"POST /api/repo/clone": s.startClone,
		"GET /api/repo/clone": func(r *http.Request) (any, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.clone == nil {
				return cloneJob{}, nil
			}
			return *s.clone, nil
		},
		"POST /api/pick-folder": func(r *http.Request) (any, error) {
			if s.pickFolder == nil {
				return map[string]any{"supported": false}, nil
			}
			p, err := s.pickFolder(str(body(r), "title"))
			return map[string]any{"supported": true, "path": p}, err
		},
		"POST /api/repo/use": func(r *http.Request) (any, error) {
			b := body(r)
			gd, err := s.gitDir(str(b, "gitDir"))
			if err != nil {
				return nil, err
			}
			if _, err := a.Accounts.SetRepoAccount(gd, str(b, "account")); err != nil {
				return nil, err
			}
			return a.Repos.Info(gd), nil
		},
		"GET /api/history": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			n := 300
			fmt.Sscan(r.URL.Query().Get("n"), &n)
			if n < 1 || n > 2000 {
				n = 300
			}
			q := r.URL.Query()
			return a.History.Build(gd, n, history.Filter{Author: strings.TrimSpace(q.Get("author")), Text: strings.TrimSpace(q.Get("text")),
				Since: strings.TrimSpace(q.Get("since")), Until: strings.TrimSpace(q.Get("until")), Account: strings.TrimSpace(q.Get("account"))})
		},
		"POST /api/repo/fix": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(str(body(r), "gitDir"))
			if err != nil {
				return nil, err
			}
			n, err := a.History.FixAuthor(gd)
			return map[string]int{"fixed": n}, err
		},
		"GET /api/refs": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			h, err := a.Refs.Head(gd)
			if err != nil {
				return nil, err
			}
			bs, err := a.Refs.Branches(gd)
			if bs == nil {
				bs = []refs.Branch{}
			}
			return map[string]any{"head": h, "branches": bs}, err
		},
		"POST /api/repo/switch": s.withRepo(func(gd string, b map[string]any) (any, error) {
			name, err := a.Refs.Switch(gd, str(b, "branch"))
			return map[string]string{"branch": name}, err
		}),
		"POST /api/repo/update": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.Update(gd, func(string) {})
		}),
		"POST /api/commit/revert": s.withRepo(func(gd string, b map[string]any) (any, error) {
			t, err := a.History.Revert(gd, str(b, "hash"))
			return map[string]string{"commit": t.Label()}, err
		}),
		"POST /api/commit/drop": s.withRepo(func(gd string, b map[string]any) (any, error) {
			t, err := a.History.Drop(gd, str(b, "hash"))
			return map[string]string{"commit": t.Label()}, err
		}),
		"POST /api/commit/rollback": s.withRepo(func(gd string, b map[string]any) (any, error) {
			n, t, err := a.History.Rollback(gd, str(b, "hash"))
			return map[string]any{"commit": t.Label(), "undone": n}, err
		}),
		"POST /api/commit/cherry-pick": s.withRepo(func(gd string, b map[string]any) (any, error) {
			var hashes []string
			list, _ := b["hashes"].([]any)
			for _, h := range list {
				if v, ok := h.(string); ok {
					hashes = append(hashes, v)
				}
			}
			ts, err := a.History.CherryPick(gd, hashes)
			return map[string]int{"picked": len(ts)}, err
		}),
		"POST /api/commit/browse": s.withRepo(func(gd string, b map[string]any) (any, error) {
			u, err := a.History.WebURL(gd, str(b, "hash"))
			if err != nil {
				return nil, err
			}
			return map[string]string{"url": u}, process.OpenBrowser(u)
		}),
		// The health checks start several programs, so the page asks for
		// them only on the Settings page.
		"GET /api/doctor":      func(r *http.Request) (any, error) { return a.Doctor.Run(false), nil },
		"POST /api/doctor/fix": func(r *http.Request) (any, error) { return nil, a.Doctor.Fix(str(body(r), "id")) },
		"POST /api/install":    func(r *http.Request) (any, error) { return nil, a.Setup.Install(nil) },
	}
}

type uiAccount struct {
	account.Account
	KeyOK   bool   `json:"keyOk"`
	KeyPage string `json:"keyPage"`
}

func (s *server) state(r *http.Request) (any, error) {
	c := s.app.Accounts.Config()
	accts := []uiAccount{}
	for _, acc := range c.Accounts {
		acc := acc
		accts = append(accts, uiAccount{acc, s.app.Keys.HasKey(&acc), ssh.AddKeyPage(acc.Host)})
	}
	return map[string]any{
		"version":   bootstrap.Version,
		"config":    c,
		"accounts":  accts,
		"installed": s.app.Setup.Installed(),
		"home":      filesystem.HomeDir(),
	}, nil
}

func (s *server) saveAccount(r *http.Request) (any, error) {
	b := body(r)
	acc := account.Account{ID: str(b, "id"), Host: str(b, "host"), Name: str(b, "name"), Email: str(b, "email"), Key: str(b, "key")}
	if str(b, "mode") != "new" {
		return acc, s.app.Accounts.Update(acc)
	}
	gen, _ := b["generateKey"].(bool)
	acc, err := s.app.Accounts.Add(acc, account.KeyChoice{New: gen, Passphrase: str(b, "passphrase")})
	if err != nil {
		return nil, err
	}
	if d, _ := b["makeDefault"].(bool); d {
		_ = s.app.Accounts.SetDefault(acc.ID)
	}
	return acc, nil
}

// startClone starts cloning in the background and returns at once.
func (s *server) startClone(r *http.Request) (any, error) {
	b := body(r)
	url, account := str(b, "url"), str(b, "account")
	dir := filesystem.ExpandHome(str(b, "parent"))
	if name := str(b, "name"); dir != "" && name != "" {
		dir = filepath.Join(dir, name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clone != nil && s.clone.Running {
		return nil, errors.New("a clone is already running; wait for it to finish")
	}
	job := &cloneJob{Running: true, Line: "Starting…"}
	s.clone = job
	go func() {
		info, err := s.app.Repos.Clone(url, dir, account, func(l string) {
			s.mu.Lock()
			job.Line = l
			s.mu.Unlock()
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		job.Running = false
		if err != nil {
			job.Error = err.Error()
			job.Hint = remote.Explain(job.Error)
		} else {
			job.Repo = &info
		}
	}()
	return *job, nil
}

func (s *server) removeAccount(r *http.Request) (any, error) {
	b := body(r)
	del, _ := b["deleteKeys"].(bool)
	deleted, err := s.app.Accounts.Remove(str(b, "id"), del)
	return map[string]any{"deleted": deleted}, err
}

func (s *server) settings(r *http.Request) (any, error) {
	b := body(r)
	if v, ok := b["guiPrompt"].(bool); ok {
		if err := s.app.Accounts.SetSetting("guiPrompt", fmt.Sprint(v)); err != nil {
			return nil, err
		}
	}
	if v, ok := b["promptTimeout"].(float64); ok && v >= 5 {
		if err := s.app.Accounts.SetSetting("promptTimeout", fmt.Sprint(int(v))); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// withRepo reads a JSON body whose gitDir must be a git repo.
func (s *server) withRepo(f func(gitDir string, b map[string]any) (any, error)) handler {
	return func(r *http.Request) (any, error) {
		b := body(r)
		gd, err := s.gitDir(str(b, "gitDir"))
		if err != nil {
			return nil, err
		}
		return f(gd, b)
	}
}

// gitDir accepts only paths that really are git repos.
func (s *server) gitDir(p string) (string, error) { return s.app.Repos.For(p) }

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		// hint explains a git or ssh error in plain words; the page shows
		// it above the raw message.
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "hint": remote.Explain(err.Error())})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

// checkJSON rejects a request body that is not a JSON object, before any
// handler acts on it; an empty body counts as {}. The body is put back for
// body() to read.
func checkJSON(w http.ResponseWriter, r *http.Request) error {
	if r.Body == nil || r.Method == http.MethodGet {
		return nil
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return errors.New("the request is too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("the request is not valid JSON: %v", err)
	}
	return nil
}

// body is the request's JSON object; checkJSON has already turned away
// anything else.
func body(r *http.Request) map[string]any {
	m := map[string]any{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&m)
	}
	if m == nil { // the body was null
		m = map[string]any{}
	}
	return m
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}
