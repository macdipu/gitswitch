// Package web is the browser front end: a small local server for one page.
// It is not a background service: it starts when opened and exits about
// 30 seconds after the page closes.
package web

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/app"
	"github.com/macdipu/gitswitch/internal/keys"
	"github.com/macdipu/gitswitch/internal/sys"
)

//go:embed assets/index.html
var indexHTML []byte

type server struct{ app *app.App }

// Run serves the app on 127.0.0.1 with a one-time token until the page closes.
func Run(a *app.App, noOpen bool) error {
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	token := hex.EncodeToString(tok)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	var mu sync.Mutex
	lastPing := time.Now().Add(2 * time.Minute) // grace period while the browser opens
	done := make(chan struct{})
	var once sync.Once
	s := &server{app: a}
	routes := s.routes()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr {
			http.Error(w, "forbidden", http.StatusForbidden) // blocks DNS rebinding
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:")
			_, _ = w.Write(indexHTML)
			return
		}
		if r.Header.Get("X-Token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mu.Lock()
		lastPing = time.Now()
		mu.Unlock()
		key := r.Method + " " + r.URL.Path
		switch key {
		case "POST /api/ping":
			writeJSON(w, map[string]string{"version": app.Version}, nil)
			return
		case "POST /api/quit":
			writeJSON(w, nil, nil)
			once.Do(func() { close(done) })
			return
		}
		h, ok := routes[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					writeJSON(w, nil, fmt.Errorf("bad request: %v", p))
				}
			}()
			v, err := h(r)
			writeJSON(w, v, err)
		}()
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	url := "http://" + addr + "/#" + token
	fmt.Println("GitSwitch is open at", url)
	fmt.Println("It closes by itself shortly after you close the browser tab (or press Ctrl+C).")
	if !noOpen {
		_ = sys.OpenBrowser(url)
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return srv.Close()
		case <-tick.C:
			mu.Lock()
			idle := time.Since(lastPing)
			mu.Unlock()
			if idle > 30*time.Second {
				fmt.Println("Page closed; GitSwitch app stopped.")
				return srv.Close()
			}
		}
	}
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
	_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&m)
	return m
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}
