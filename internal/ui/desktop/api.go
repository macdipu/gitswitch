// Package desktop is the desktop window: the app page in a native OS
// webview (Wails). The page talks to the app through the JSON API in this
// file, which the window serves in-process: there is no network port.
package desktop

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/app"
	"github.com/macdipu/gitswitch/internal/keys"
	"github.com/macdipu/gitswitch/internal/sys"
)

//go:embed assets/index.html
var indexHTML []byte

type server struct{ app *app.App }

// Handler serves the page at / and its JSON API under /api/.
func Handler(a *app.App) http.Handler {
	s := &server{app: a}
	routes := s.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(indexHTML)
			return
		}
		h, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
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
			n, err := a.RenameAccount(str(b, "id"), str(b, "newId"))
			return map[string]int{"repos": n}, err
		},
		"GET /api/keys":     func(r *http.Request) (any, error) { return a.ExistingKeys(), nil },
		"POST /api/default": func(r *http.Request) (any, error) { return nil, a.SetDefault(str(body(r), "id")) },
		"POST /api/test":    func(r *http.Request) (any, error) { return a.TestAccount(str(body(r), "id"), true) },
		"GET /api/pubkey": func(r *http.Request) (any, error) {
			k, help, err := a.PublicKey(r.URL.Query().Get("id"))
			return map[string]any{"key": k, "page": help.Page, "steps": help.Steps}, err
		},
		"POST /api/open": func(r *http.Request) (any, error) {
			u := str(body(r), "url")
			if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
				return nil, errors.New("only web links can be opened")
			}
			return nil, sys.OpenBrowser(u)
		},
		"POST /api/copy": func(r *http.Request) (any, error) {
			return map[string]bool{"ok": sys.CopyToClipboard(str(body(r), "text"))}, nil
		},
		"POST /api/rule": func(r *http.Request) (any, error) {
			b := body(r)
			return nil, a.AddRule(account.Rule{Kind: str(b, "kind"), Pattern: str(b, "pattern"), Account: str(b, "account")})
		},
		"POST /api/rule/remove": func(r *http.Request) (any, error) {
			i, _ := body(r)["index"].(float64)
			return nil, a.RemoveRuleAt(int(i))
		},
		"POST /api/settings":      s.settings,
		"POST /api/folder":        func(r *http.Request) (any, error) { return nil, a.AddScanFolder(str(body(r), "path")) },
		"POST /api/folder/remove": func(r *http.Request) (any, error) { return nil, a.RemoveScanFolder(str(body(r), "path")) },
		"GET /api/repos":          func(r *http.Request) (any, error) { return a.Repos(), nil },
		"POST /api/repo/use": func(r *http.Request) (any, error) {
			b := body(r)
			gd, err := s.gitDir(str(b, "gitDir"))
			if err != nil {
				return nil, err
			}
			if _, err := a.SetRepoAccount(gd, str(b, "account")); err != nil {
				return nil, err
			}
			return a.RepoInfo(gd), nil
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
			return a.History(gd, n)
		},
		"POST /api/repo/fix": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(str(body(r), "gitDir"))
			if err != nil {
				return nil, err
			}
			n, err := a.FixAuthor(gd)
			return map[string]int{"fixed": n}, err
		},
		"POST /api/doctor/fix": func(r *http.Request) (any, error) { return nil, a.Fix(str(body(r), "id")) },
		"POST /api/install":    func(r *http.Request) (any, error) { return nil, a.Install(nil) },
	}
}

type uiAccount struct {
	account.Account
	KeyOK   bool   `json:"keyOk"`
	KeyPage string `json:"keyPage"`
}

func (s *server) state(r *http.Request) (any, error) {
	c := s.app.Config()
	accts := []uiAccount{}
	for _, acc := range c.Accounts {
		acc := acc
		accts = append(accts, uiAccount{acc, keys.Exists(&acc), keys.AddKeyPage(acc.Host)})
	}
	return map[string]any{
		"version":   app.Version,
		"config":    c,
		"accounts":  accts,
		"installed": s.app.Installed(),
		"doctor":    s.app.Checks(false),
		"home":      sys.HomeDir(),
	}, nil
}

func (s *server) saveAccount(r *http.Request) (any, error) {
	b := body(r)
	acc := account.Account{ID: str(b, "id"), Host: str(b, "host"), Name: str(b, "name"), Email: str(b, "email"), Key: str(b, "key")}
	if str(b, "mode") != "new" {
		return acc, s.app.UpdateAccount(acc)
	}
	gen, _ := b["generateKey"].(bool)
	acc, err := s.app.AddAccount(acc, app.KeyChoice{New: gen, Passphrase: str(b, "passphrase")})
	if err != nil {
		return nil, err
	}
	if d, _ := b["makeDefault"].(bool); d {
		_ = s.app.SetDefault(acc.ID)
	}
	return acc, nil
}

func (s *server) removeAccount(r *http.Request) (any, error) {
	b := body(r)
	del, _ := b["deleteKeys"].(bool)
	deleted, err := s.app.RemoveAccount(str(b, "id"), del)
	return map[string]any{"deleted": deleted}, err
}

func (s *server) settings(r *http.Request) (any, error) {
	b := body(r)
	if v, ok := b["guiPrompt"].(bool); ok {
		if err := s.app.SetSetting("guiPrompt", fmt.Sprint(v)); err != nil {
			return nil, err
		}
	}
	if v, ok := b["promptTimeout"].(float64); ok && v >= 5 {
		if err := s.app.SetSetting("promptTimeout", fmt.Sprint(int(v))); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// gitDir accepts only paths that really are git repos.
func (s *server) gitDir(p string) (string, error) { return s.app.RepoFor(p) }

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func body(r *http.Request) map[string]any {
	m := map[string]any{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&m)
	}
	return m
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}
