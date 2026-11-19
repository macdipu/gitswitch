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
	"strconv"
	"strings"
	"sync"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	hostingapp "github.com/macdipu/gitswitch/internal/features/hosting/application"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
	merge "github.com/macdipu/gitswitch/internal/features/merge/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
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
		"POST /api/signin": func(r *http.Request) (any, error) {
			b := body(r)
			return s.app.Hosting.SignIn(str(b, "id"), str(b, "token"))
		},
		"POST /api/signout": func(r *http.Request) (any, error) { return nil, s.app.Hosting.SignOut(str(body(r), "id")) },
		"GET /api/reviews": func(r *http.Request) (any, error) {
			fetch := map[string]hostingapp.Fetch{"none": hostingapp.FetchNone, "stale": hostingapp.FetchStale, "all": hostingapp.FetchAll}[r.URL.Query().Get("fetch")]
			return s.app.Hosting.Reviews(fetch), nil
		},
		"GET /api/github": func(r *http.Request) (any, error) {
			fetch := map[string]hostingapp.Fetch{"none": hostingapp.FetchNone, "stale": hostingapp.FetchStale, "all": hostingapp.FetchAll}[r.URL.Query().Get("fetch")]
			b := s.app.Hosting.Browse(fetch)
			owners, langs := hosting.Facets(b.Repos)
			return map[string]any{"accounts": b.Accounts, "repos": b.Repos, "owners": owners, "languages": langs}, nil
		},
		"POST /api/signin/check": func(r *http.Request) (any, error) { return s.app.Hosting.Check(str(body(r), "id")) },
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
		"GET /api/repo/groups": func(r *http.Request) (any, error) {
			gs, err := a.Repos.Groups(r.URL.Query().Get("by"), "")
			return map[string]any{"by": a.Repos.GroupBy(), "sort": a.Repos.SortBy(), "groups": gs}, err
		},
		"POST /api/repo/group": func(r *http.Request) (any, error) {
			b := body(r)
			return nil, a.Repos.SetGroup(str(b, "path"), str(b, "name"))
		},
		"POST /api/repo/opened": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(str(body(r), "gitDir"))
			if err != nil {
				return nil, err
			}
			return nil, a.Repos.Opened(gd)
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
			// once: only make new commits with the account's name and
			// email; the repo's saved choice stays as it is.
			if once, _ := b["once"].(bool); once {
				if err := a.Accounts.UseOnce(gd, str(b, "account")); err != nil {
					return nil, err
				}
			} else if _, err := a.Accounts.SetRepoAccount(gd, str(b, "account")); err != nil {
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
			if n < 1 || n > 20000 {
				n = 300
			}
			q := r.URL.Query()
			return a.History.Build(gd, n, history.Filter{Author: strings.TrimSpace(q.Get("author")), Text: strings.TrimSpace(q.Get("text")),
				Since: strings.TrimSpace(q.Get("since")), Until: strings.TrimSpace(q.Get("until")), Account: strings.TrimSpace(q.Get("account")),
				Ref: strings.TrimSpace(q.Get("ref")), Path: strings.TrimSpace(q.Get("path")),
				Order: q.Get("order"), FirstParent: q.Get("firstParent") == "1", NoMerges: q.Get("noMerges") == "1", Cherry: q.Get("cherry") == "1",
				Pickaxe: q.Get("code"), PickaxeRegex: q.Get("codeRegex") == "1", PickaxeIgnoreCase: q.Get("codeIgnoreCase") == "1"})
		},
		// Blame: who last changed each line of a file at a revision.
		"GET /api/blame": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.History.Blame(gd, q.Get("rev"), q.Get("path"), history.BlameOptions{IgnoreWhitespace: q.Get("ignoreWs") == "1", Moves: q.Get("moves") == "1"})
		},
		// The commits that changed one file, following renames.
		"GET /api/file/history": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			n, skip := 50, 0
			fmt.Sscan(q.Get("n"), &n)
			fmt.Sscan(q.Get("skip"), &skip)
			return a.History.FileHistory(gd, q.Get("path"), n, skip)
		},
		"GET /api/commit/show": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.History.Show(gd, r.URL.Query().Get("hash"))
		},
		"GET /api/commit/diff": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.History.Diff(gd, r.URL.Query().Get("hash"), r.URL.Query().Get("path"))
		},
		// Before a push: do the commits show the account that pushes?
		"GET /api/account-check": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.History.CheckAccount(gd)
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
			// The last change GitSwitch made here, for the Undo button.
			var last any
			if st, uerr := a.Undo.Last(gd); uerr == nil {
				last = st
			}
			clash, cerr := a.Refs.Clash(gd)
			if err == nil {
				err = cerr
			}
			parked, perr := a.Refs.Parked(gd)
			if err == nil {
				err = perr
			}
			if parked == nil {
				parked = []refs.Parked{}
			}
			tags, _ := a.Refs.Tags(gd)
			if tags == nil {
				tags = []string{}
			}
			var redo any
			if st, rerr := a.Undo.NextRedo(gd); rerr == nil {
				redo = st
			}
			return map[string]any{"head": h, "branches": bs, "tags": tags, "undo": last, "redo": redo, "clash": clash, "parked": parked,
				"switchSetting": a.Refs.SwitchSetting(gd)}, err
		},
		"POST /api/undo": s.withRepo(func(gd string, b map[string]any) (any, error) {
			r, err := a.Undo.Undo(gd)
			return map[string]string{"label": r.Label}, err
		}),
		// Redo does again what the last Undo took back (same refusals as
		// Undo); GET /api/refs says what it would do in "redo".
		"POST /api/redo": s.withRepo(func(gd string, b map[string]any) (any, error) {
			r, err := a.Undo.Redo(gd)
			return map[string]string{"label": r.Label}, err
		}),
		// Commit signing: set how an account signs ("format" ssh, gpg or
		// off; "key" the ssh public key file or gpg key id), and the
		// signature status of commits ({hash: {status, signer, key, ...}},
		// at most 200 hashes).
		"POST /api/account/signing": func(r *http.Request) (any, error) {
			b := body(r)
			return a.Accounts.SetSigning(str(b, "id"), str(b, "format"), str(b, "key"))
		},
		"GET /api/signatures": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.History.Signatures(gd, strings.Split(q.Get("hashes"), ","))
		},
		// Start a repo: git init, an account's identity, a place in the list.
		// "account" "" lets the rules and the default decide; with several
		// accounts and none decided the error says to choose one.
		"POST /api/repo/init": func(r *http.Request) (any, error) {
			b := body(r)
			return a.Repos.Init(str(b, "path"), str(b, "account"), str(b, "branch"))
		},
		"GET /api/git-info": func(r *http.Request) (any, error) { return a.Repos.GitInfo(), nil },
		// mode "smart" takes uncommitted changes along (and may leave a
		// clash), "park" leaves them on the current branch. Changes parked
		// on the new branch come back in "waiting".
		"POST /api/repo/switch": s.withRepo(func(gd string, b map[string]any) (any, error) {
			switch str(b, "mode") {
			case "smart":
				return a.Refs.SmartSwitch(gd, str(b, "branch"))
			case "park":
				return a.Refs.Park(gd, str(b, "branch"))
			}
			name, err := a.Refs.Switch(gd, str(b, "branch"))
			r := refs.Switched{Branch: name}
			if err == nil {
				r.Waiting, _ = a.Refs.Waiting(gd)
			}
			return r, err
		}),
		// name is the whole name (the page puts the prefix in front); from
		// is a commit, or "" for the current one.
		"POST /api/branch/new": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.NewBranch(gd, str(b, "name"), str(b, "from"))
		}),
		// name is the whole name, as for a new branch; the commits not
		// pushed move there from the current branch.
		"POST /api/branch/move": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.MoveToNewBranch(gd, str(b, "name"))
		}),
		// pr is the open pull request of the repo's current branch, read
		// with the repo's account; quiet (found: false) when there is none.
		"GET /api/repo/pr": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Hosting.BranchPR(gd), nil
		},
		"POST /api/branch/compare": s.withRepo(func(gd string, _ map[string]any) (any, error) {
			return a.Refs.Compare(gd)
		}),
		// merged lists what prune would delete; prune deletes it.
		"POST /api/branch/merged": s.withRepo(func(gd string, _ map[string]any) (any, error) {
			return a.Refs.Merged(gd)
		}),
		"POST /api/branch/prune": s.withRepo(func(gd string, _ map[string]any) (any, error) {
			return a.Refs.Prune(gd)
		}),
		// Branch and tag management. Delete refuses a branch whose commits
		// are on no other branch or remote unless force; rename and delete
		// can be undone. upstream "" unsets; from/branch "" is the current
		// branch. rev "" tags the current commit; a message makes the tag
		// annotated.
		"POST /api/branch/delete": s.withRepo(func(gd string, b map[string]any) (any, error) {
			force, _ := b["force"].(bool)
			return a.Refs.DeleteBranch(gd, str(b, "name"), force)
		}),
		"POST /api/branch/rename": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.RenameBranch(gd, str(b, "from"), str(b, "to"))
		}),
		"POST /api/branch/upstream": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.SetUpstream(gd, str(b, "branch"), str(b, "upstream"))
		}),
		"GET /api/tags": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			tags, err := a.Refs.Tags(gd)
			if tags == nil {
				tags = []string{}
			}
			return tags, err
		},
		"POST /api/tag/new": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.NewTag(gd, str(b, "name"), str(b, "rev"), str(b, "message"))
		}),
		"POST /api/tag/delete": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return map[string]string{"tag": str(b, "name")}, a.Refs.DeleteTag(gd, str(b, "name"))
		}),
		"POST /api/tag/push": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.PushTag(gd, str(b, "name"), func(string) {})
		}),
		// action "done" finishes a smart switch that clashed, "abort" goes
		// back, "park" goes back and parks the changes instead.
		"POST /api/resolve": s.withRepo(func(gd string, b map[string]any) (any, error) {
			switch str(b, "action") {
			case "done":
				return nil, a.Refs.ResolveDone(gd)
			case "abort":
				return nil, a.Refs.ResolveAbort(gd)
			case "park":
				return a.Refs.ResolvePark(gd)
			}
			return nil, errors.New(`action must be "done", "abort" or "park"`)
		}),
		// mode ask, smart or park: what switching does with uncommitted
		// changes, in every repo, or in the repo gitDir names ("" there
		// follows every repo's again).
		"POST /api/switch-setting": func(r *http.Request) (any, error) {
			b := body(r)
			gd := ""
			if p := str(b, "gitDir"); p != "" {
				var err error
				if gd, err = s.gitDir(p); err != nil {
					return nil, err
				}
			}
			if err := a.Refs.SetSwitchSetting(gd, str(b, "mode")); err != nil {
				return nil, err
			}
			return a.Refs.SwitchSetting(gd), nil
		},
		// action "restore" or "drop" on the parked changes held by stash.
		"POST /api/parked": s.withRepo(func(gd string, b map[string]any) (any, error) {
			switch str(b, "action") {
			case "restore":
				return nil, a.Refs.RestoreParked(gd, str(b, "stash"))
			case "drop":
				return nil, a.Refs.DropParked(gd, str(b, "stash"))
			}
			return nil, errors.New(`action must be "restore" or "drop"`)
		}),
		"POST /api/repo/update": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.UpdateWith(gd, str(b, "mode"), func(string) {})
		}),
		// fetch reads the remote's new commits; no branch or file changes.
		// prune: true also forgets the remote branches the server deleted
		// (listed in "pruned"); without it the fetch-prune setting decides.
		"POST /api/repo/fetch": s.withRepo(func(gd string, b map[string]any) (any, error) {
			if prune, _ := b["prune"].(bool); prune {
				return a.Refs.FetchWith(gd, true, func(string) {})
			}
			return a.Refs.Fetch(gd, func(string) {})
		}),
		// The working folder's uncommitted changes, file by file.
		"GET /api/repo/changes": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Workspace.Status(gd)
		},
		// One file's uncommitted change ("" path: every file's); staged=1
		// for what is staged.
		"GET /api/repo/diff": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Workspace.Diff(gd, q.Get("path"), q.Get("staged") == "1")
		},
		// paths (repo-relative) or all; discard keeps the changes in a stash.
		"POST /api/repo/stage": s.withRepo(func(gd string, b map[string]any) (any, error) {
			paths, all := pathList(b)
			return a.Workspace.Stage(gd, paths, all)
		}),
		"POST /api/repo/unstage": s.withRepo(func(gd string, b map[string]any) (any, error) {
			paths, all := pathList(b)
			return a.Workspace.Unstage(gd, paths, all)
		}),
		"POST /api/repo/discard": s.withRepo(func(gd string, b map[string]any) (any, error) {
			paths, all := pathList(b)
			return a.Workspace.Discard(gd, paths, all)
		}),
		// Single hunks and lines of one file: {gitDir, path, fingerprint (from
		// GET /api/repo/diff), hunks:[0,2] and/or lines:{"1":[3,4]}} (line
		// indexes count every line of the hunk). Stage and discard read the
		// change that is not staged, unstage the staged one. discard keeps the
		// file as it was in a stash first and returns how to restore it.
		"POST /api/repo/stage-hunks": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Workspace.StageHunks(gd, str(b, "path"), str(b, "fingerprint"), hunkSelection(b))
		}),
		"POST /api/repo/unstage-hunks": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Workspace.UnstageHunks(gd, str(b, "path"), str(b, "fingerprint"), hunkSelection(b))
		}),
		"POST /api/repo/discard-hunks": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Workspace.DiscardHunks(gd, str(b, "path"), str(b, "fingerprint"), hunkSelection(b))
		}),
		// Ignore a file (kind file), its extension (ext) or its folder
		// (folder) in .gitignore, or with local in .git/info/exclude; untrack
		// also takes tracked files out of git (git rm --cached), keeping them.
		"POST /api/repo/ignore": s.withRepo(func(gd string, b map[string]any) (any, error) {
			kind, err := workspace.ParseIgnoreKind(str(b, "kind"))
			if err != nil {
				return nil, err
			}
			local, _ := b["local"].(bool)
			untrack, _ := b["untrack"].(bool)
			return a.Workspace.Ignore(gd, str(b, "path"), kind, local, untrack)
		}),
		// all commits every change, new files too; otherwise what is staged.
		// amend amends the last commit instead ("" message keeps its own).
		// signoff (true/false; absent: the setting) and coauthors
		// ("Name <email>" each) add trailers to the message.
		"POST /api/repo/commit": s.withRepo(func(gd string, b map[string]any) (any, error) {
			all, _ := b["all"].(bool)
			o := commitOptions(b)
			if amend, _ := b["amend"].(bool); amend {
				return a.Workspace.AmendWith(gd, str(b, "message"), all, o)
			}
			return a.Workspace.CommitWith(gd, str(b, "message"), all, o)
		}),
		// What the commit box offers: recent messages, the kept draft, the
		// sign-off setting, co-author suggestions, the identity.
		"GET /api/repo/commit-info": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Workspace.CommitInfo(gd, 0)
		},
		// value "on" or "off": whether commits get Signed-off-by, in every
		// repo, or in the repo gitDir names ("" there follows every repo's).
		"POST /api/signoff-setting": func(r *http.Request) (any, error) {
			b := body(r)
			gd := ""
			if p := str(b, "gitDir"); p != "" {
				var err error
				if gd, err = s.gitDir(p); err != nil {
					return nil, err
				}
			}
			if err := a.Workspace.SetSignoffSetting(gd, str(b, "value")); err != nil {
				return nil, err
			}
			return a.Workspace.SignoffSetting(gd), nil
		},
		// Whether the last commit may be amended (why not), with its message.
		"GET /api/repo/amendable": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Workspace.Amendable(gd), nil
		},
		// force "lease" replaces the remote branch's commits with this
		// branch's (see GET /api/repo/push-plan, which lists the ones that
		// would be lost): it needs confirm: true, refuses wrong-account
		// commits unless accountOk: true, and fails when the remote moved
		// since the fetch. expect is the plan's "expect".
		"POST /api/repo/push": s.withRepo(func(gd string, b map[string]any) (any, error) {
			switch str(b, "force") {
			case "":
				return a.Refs.Push(gd, func(string) {})
			case "lease":
				if ok, _ := b["accountOk"].(bool); !ok {
					if ch, err := a.History.CheckAccount(gd); err == nil && ch.Wrong > 0 {
						return nil, errors.New(ch.Describe() + ". Fix them first, or push anyway (accountOk)")
					}
				}
				confirm, _ := b["confirm"].(bool)
				return a.Refs.ForcePushLease(gd, str(b, "expect"), confirm, func(string) {})
			}
			return nil, errors.New(`force must be "lease": GitSwitch never forces a push without a lease`)
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
		// Live refresh, a long poll: what changed in the repo after the
		// change numbered since ("files", "index", "head", "refs",
		// "operation"), waiting up to ~25 s for a change when there is
		// none yet (then the same seq and no kinds). Without since: the
		// latest seq at once. partial: only part of the working folder is
		// watched, so file changes may go unseen.
		"GET /api/watch": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			var since uint64
			has := q.Get("since") != ""
			if has {
				if _, err := fmt.Sscan(q.Get("since"), &since); err != nil {
					return nil, fmt.Errorf("since is not a change number: %q", q.Get("since"))
				}
			}
			return a.Watch.Wait(r.Context(), gd, since, has)
		},
		// The health checks start several programs, so the page asks for
		// them only on the Settings page.
		"GET /api/doctor":      func(r *http.Request) (any, error) { return a.Doctor.Run(false), nil },
		"POST /api/doctor/fix": func(r *http.Request) (any, error) { return nil, a.Doctor.Fix(str(body(r), "id")) },
		"POST /api/install":    func(r *http.Request) (any, error) { return nil, a.Setup.Install(nil) },
		// Inspect: compare two refs (commits only on each side, changed files,
		// one file's diff), the reflog with a "recover" that makes a branch,
		// and a guided bisect. b may be ":working" (the files on disk).
		"GET /api/compare": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			mode, err := inspect.ParseMode(q.Get("mode"))
			if err != nil {
				return nil, err
			}
			return a.Inspect.Compare(gd, q.Get("a"), q.Get("b"), mode)
		},
		"GET /api/compare/file": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			mode, err := inspect.ParseMode(q.Get("mode"))
			if err != nil {
				return nil, err
			}
			return a.Inspect.CompareFile(gd, q.Get("a"), q.Get("b"), mode, q.Get("path"))
		},
		"GET /api/reflog": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			n, _ := strconv.Atoi(q.Get("n"))
			skip, _ := strconv.Atoi(q.Get("skip"))
			return a.Inspect.Reflog(gd, q.Get("ref"), n, skip, q.Get("lost") == "1")
		},
		"POST /api/reflog/recover": s.withRepo(func(gd string, b map[string]any) (any, error) {
			index := -1
			if f, ok := b["index"].(float64); ok && f >= 0 {
				index = int(f)
			}
			return a.Inspect.Recover(gd, str(b, "ref"), index, str(b, "hash"), str(b, "name"))
		}),
		"GET /api/bisect": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Inspect.BisectStatus(gd)
		},
		"POST /api/bisect": s.withRepo(func(gd string, b map[string]any) (any, error) {
			switch act := str(b, "action"); act {
			case "start":
				return a.Inspect.BisectStart(gd, str(b, "bad"), str(b, "good"))
			case "good", "bad", "skip":
				return a.Inspect.BisectMark(gd, act)
			case "reset":
				return a.Inspect.BisectReset(gd)
			}
			return nil, errors.New("action must be start, good, bad, skip or reset")
		}),
		// Merge a branch into the current one (plan first: blocked says
		// why not now), the operation git stopped in the middle of
		// (continue, skip, abort) and one conflicted file (ours, theirs,
		// done once its markers are gone, or save text as the result).
		"GET /api/merge/plan": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Merge.Plan(gd, q.Get("branch"))
		},
		"POST /api/merge": s.withRepo(func(gd string, b map[string]any) (any, error) {
			mode, err := merge.ParseMode(str(b, "mode"))
			if err != nil {
				return nil, err
			}
			return a.Merge.Merge(gd, str(b, "branch"), mode)
		}),
		// Stash manager: every stash, parked and discarded ones too. A stash
		// is named by its commit hash (indexes shift); a hash no longer in
		// the list is refused with "the stash list changed; reload".
		"GET /api/stashes": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			all, err := a.Refs.Stashes(gd)
			if all == nil {
				all = []refs.Stash{}
			}
			return all, err
		},
		// One stash with its files (no path), or the diff of one file.
		"GET /api/stash/show": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			if q.Get("path") != "" {
				return a.Refs.StashDiff(gd, q.Get("hash"), q.Get("path"))
			}
			return a.Refs.StashShow(gd, q.Get("hash"))
		},
		"POST /api/stash/save": s.withRepo(func(gd string, b map[string]any) (any, error) {
			o := refs.StashOptions{Message: str(b, "message"), Untracked: true}
			if v, ok := b["untracked"].(bool); ok {
				o.Untracked = v
			}
			o.KeepIndex, _ = b["keepIndex"].(bool)
			if ps, ok := b["paths"].([]any); ok {
				for _, p := range ps {
					if t, ok := p.(string); ok {
						o.Paths = append(o.Paths, t)
					}
				}
			}
			return a.Refs.StashSave(gd, o)
		}),
		"POST /api/stash/apply": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return nil, a.Refs.StashApply(gd, str(b, "hash"))
		}),
		"POST /api/stash/pop": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return nil, a.Refs.StashPop(gd, str(b, "hash"))
		}),
		"POST /api/stash/drop": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return nil, a.Refs.StashDrop(gd, str(b, "hash"))
		}),
		"POST /api/stash/branch": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return nil, a.Refs.StashBranch(gd, str(b, "hash"), str(b, "name"))
		}),
		"GET /api/operation": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Merge.Operation(gd)
		},
		"POST /api/operation": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Merge.Act(gd, str(b, "action"))
		}),
		"GET /api/conflict": func(r *http.Request) (any, error) {
			q := r.URL.Query()
			gd, err := s.gitDir(q.Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Merge.Conflict(gd, q.Get("path"))
		},
		"POST /api/conflict": s.withRepo(func(gd string, b map[string]any) (any, error) {
			text, _ := b["text"].(string) // as typed: not trimmed
			return a.Merge.Resolve(gd, str(b, "path"), str(b, "action"), text)
		}),

		// Remotes, remote branches, force push with lease, detached HEAD.
		// remotes: [{name, fetchURL, pushURL, host, account, upstream, tracking}].
		"GET /api/remotes": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Refs.Remotes(gd)
		},
		// The four results are {name, newName, url, was, host, account,
		// branches, note}; remove refuses a remote branches track unless
		// force: true.
		"POST /api/remote/add": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.AddRemote(gd, str(b, "name"), str(b, "url"))
		}),
		"POST /api/remote/remove": s.withRepo(func(gd string, b map[string]any) (any, error) {
			force, _ := b["force"].(bool)
			return a.Refs.RemoveRemote(gd, str(b, "name"), force)
		}),
		"POST /api/remote/rename": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.RenameRemote(gd, str(b, "name"), str(b, "newName"))
		}),
		"POST /api/remote/set-url": s.withRepo(func(gd string, b map[string]any) (any, error) {
			return a.Refs.SetRemoteURL(gd, str(b, "name"), str(b, "url"))
		}),
		// ref is the remote branch as listed, e.g. origin/feature/x. preview:
		// true only checks and describes it (ask first); the default branch
		// and the one the current branch pulls from need force: true.
		"POST /api/remote-branch/delete": s.withRepo(func(gd string, b map[string]any) (any, error) {
			force, _ := b["force"].(bool)
			if preview, _ := b["preview"].(bool); preview {
				return a.Refs.CanDeleteRemoteBranch(gd, str(b, "ref"), force)
			}
			return a.Refs.DeleteRemoteBranch(gd, str(b, "ref"), force, func(string) {})
		}),
		// push-plan: what a force push with lease would overwrite and send,
		// as of the last fetch.
		"GET /api/repo/push-plan": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Refs.PushPlan(gd)
		},
		// mode "" / "smart" / "park" as for a switch; force: true leaves a
		// detached HEAD that has commits on no branch.
		"POST /api/repo/checkout": s.withRepo(func(gd string, b map[string]any) (any, error) {
			force, _ := b["force"].(bool)
			return a.Refs.CheckoutDetached(gd, str(b, "rev"), str(b, "mode"), force)
		}),
		"POST /api/repo/back": s.withRepo(func(gd string, b map[string]any) (any, error) {
			force, _ := b["force"].(bool)
			return a.Refs.Back(gd, str(b, "mode"), force)
		}),
		// detached: {detached, hash, short, back, orphans, words}.
		"GET /api/repo/detached": func(r *http.Request) (any, error) {
			gd, err := s.gitDir(r.URL.Query().Get("gitDir"))
			if err != nil {
				return nil, err
			}
			return a.Refs.DetachedState(gd)
		},
		// value "on" or "off"; with a gitDir for that repo only, where ""
		// follows every repo's. Returns {on, repo, global}.
		"POST /api/fetch-prune-setting": func(r *http.Request) (any, error) {
			b := body(r)
			gd := ""
			if p := str(b, "gitDir"); p != "" {
				var err error
				if gd, err = s.gitDir(p); err != nil {
					return nil, err
				}
			}
			if err := a.Refs.SetPruneSetting(gd, str(b, "value")); err != nil {
				return nil, err
			}
			return a.Refs.PruneSetting(gd), nil
		},
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
		// what switching does with uncommitted changes in every repo
		"switchChanges": s.app.Refs.SwitchSetting("").Global,
		"pull":          s.app.Refs.PullSetting("").Global, // how Update works in every repo
		"branchPrefix":  s.app.Refs.BranchPrefix(),
		"fetchOnOpen":   s.app.Refs.FetchOnOpen(), // History reads the remote's new commits when a repo opens
		"home":          filesystem.HomeDir(),
		"hosting":       s.app.Hosting.Status(), // signed in to GitHub, from the keychain
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
	if v, ok := b["pull"].(string); ok && v != "" {
		if err := s.app.Refs.SetPullSetting("", v); err != nil {
			return nil, err
		}
	}
	if v, ok := b["repoSort"].(string); ok {
		if err := s.app.Repos.SetSortBy(v); err != nil {
			return nil, err
		}
	}
	if v, ok := b["repoGroups"].(string); ok {
		if err := s.app.Repos.SetGroupBy(v); err != nil {
			return nil, err
		}
	}
	if v, ok := b["fetchOnOpen"].(bool); ok && v != s.app.Refs.FetchOnOpen() {
		if err := s.app.Refs.SetFetchOnOpen(v); err != nil {
			return nil, err
		}
	}
	if v, ok := b["branchPrefix"].(string); ok && v != s.app.Refs.BranchPrefix() {
		if err := s.app.Refs.SetBranchPrefix(v); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// commitOptions reads signoff and coauthors from a commit's body.
func commitOptions(b map[string]any) workspace.CommitOptions {
	var o workspace.CommitOptions
	if v, ok := b["signoff"].(bool); ok {
		o.Signoff = &v
	}
	list, _ := b["coauthors"].([]any)
	for _, c := range list {
		if v, ok := c.(string); ok {
			o.CoAuthors = append(o.CoAuthors, v)
		}
	}
	return o
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

// pathList reads "paths": [...] and "all": true from a request body.
func pathList(b map[string]any) (paths []string, all bool) {
	all, _ = b["all"].(bool)
	list, _ := b["paths"].([]any)
	for _, p := range list {
		if s, ok := p.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths, all
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

// hunkSelection reads {"hunks":[0,2],"lines":{"1":[3,4]}}.
func hunkSelection(b map[string]any) workspace.Selection {
	sel := workspace.Selection{Lines: map[int][]int{}}
	ints := func(v any) (out []int) {
		list, _ := v.([]any)
		for _, x := range list {
			if f, ok := x.(float64); ok {
				out = append(out, int(f))
			}
		}
		return out
	}
	sel.Hunks = ints(b["hunks"])
	lines, _ := b["lines"].(map[string]any)
	for k, v := range lines {
		if i, err := strconv.Atoi(k); err == nil {
			sel.Lines[i] = ints(v)
		} else {
			sel.Lines[-1] = nil // names no hunk: refused with the others
		}
	}
	return sel
}
