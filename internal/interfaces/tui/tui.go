// Package tui is the terminal front end (`gitswitch` or `gitswitch tui`).
// It runs only while open, in the terminal it was started from.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/macdipu/gitswitch/internal/bootstrap"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	"github.com/macdipu/gitswitch/internal/features/doctor"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	hostingapp "github.com/macdipu/gitswitch/internal/features/hosting/application"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

// The TUI runs only while it is open, in the terminal it was started from
// (Windows Terminal, VS Code, Android Studio, macOS Terminal, ...).

var tabNames = []string{"Accounts", "Repos", "History", "Rules", "Settings", "Reviews"}

const (
	tabAccounts = iota
	tabRepos
	tabHistory
	tabRules
	tabSettings
	tabReviews
)

type field struct {
	label   string
	value   string
	secret  bool
	options []string // when set, ←/→ cycles through these
	hint    string
}

type form struct {
	title  string
	fields []field
	focus  int
	submit func(vals []string) error
	// changed, when set, runs after ←/→ changes a choice, so the form can
	// follow it (e.g. prefill a message).
	changed func(f *form)
}

type picker struct {
	title   string
	options []string
	sel     int    // position in shown()
	off     int    // first shown option on screen
	filter  string // typed text narrows the list
	pick    func(i int) error
}

// shown lists the options that match the filter, as indexes into options.
func (p *picker) shown() []int {
	f := strings.ToLower(p.filter)
	var out []int
	for i, o := range p.options {
		if strings.Contains(strings.ToLower(o), f) {
			out = append(out, i)
		}
	}
	return out
}

type confirmBox struct {
	text    string
	actions map[string]func() error // key -> action; n or Esc cancels, other keys do nothing
}

type viewer struct {
	title string
	lines []string
	url   string
	copy  string
	off   int // first line on screen
}

type tui struct {
	fetched map[string]bool // repos fetched on open since the app started
	tab     int
	sel     [6]int
	off     [6]int
	cfg     *account.Config
	app     *bootstrap.App
	repos   []repo.Info
	reviews []hosting.Inbox           // signed-in accounts' pull requests
	rvRows  []hosting.PullRequest     // the Reviews page's rows, in order
	apis    map[string]hosting.Status // host sign-in (GitHub, GitLab) by account id (lower case)
	groups  []repoHeading             // headings on the Repos page, by the repo they come before
	scanned bool
	checks  []doctor.Check // the health checks; run only on the Settings page
	hooked  bool           // git runs through gitswitch

	histDir   string
	histLines []string
	histN     int           // commits to read; m (load more) raises it
	histRows  []history.Row // the commit on each line
	hist      *history.History
	head      *refs.Head
	marked    map[string]bool // commits marked for cherry-pick
	flagged   bool
	filter    history.Filter
	changes   *changesView // the History page's list of uncommitted changes, while open
	stashes   *stashView   // the History page's stash manager, while open
	rewriteUI              // the rebase editor, worktrees and submodules, while open (tui_rewrite.go, tui_trees.go)
	mergeUI                // the operation in progress and the conflict editor (tui_merge.go)
	hunksUI                // a file's hunks in the changes (tui_hunks.go)
	insp      inspectState // the History page's Inspect views (blame, compare, ...), while open

	ext extState // remotes, redo, detached HEAD, signing badges, git check (tui_misc.go)

	msg    string
	msgErr bool

	form    *form
	picker  *picker
	confirm *confirmBox
	view    *viewer

	in        *bufio.Reader // the only reader of stdin, also while suspended
	tty       atomic.Pointer[terminal.TTYState]
	w, h      int
	quit      bool
	err       error // why the app had to stop
	mu        sync.Mutex
	suspended atomic.Bool
}

func Run(a *bootstrap.App) error {
	st, err := terminal.MakeRaw()
	if err != nil {
		return fmt.Errorf("%v (try `gitswitch ui` for the desktop app)", err)
	}
	t := &tui{app: a, in: bufio.NewReader(os.Stdin)}
	t.tty.Store(st)
	enterScreen()
	defer t.restore()
	// Asked to stop (kill, the window closing): give the terminal back first.
	// Ctrl+C while suspended goes to the command that is running instead.
	stopQuit := terminal.WatchQuit(func(s os.Signal) {
		if s == os.Interrupt && t.suspended.Load() {
			return
		}
		t.restore()
		os.Exit(1)
	})
	defer stopQuit()
	t.reload()
	t.histDir = a.Repos.Current()
	t.welcome()
	t.gitBanner()
	stop := terminal.WatchResize(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		defer func() { // a panic here would skip Run's restore
			if p := recover(); p != nil {
				t.restore()
				panic(p)
			}
		}()
		if !t.suspended.Load() && !t.quit {
			t.draw()
		}
	})
	defer stop()
	buf := make([]byte, 64)
	var pending []byte // the start of a key split across reads
	for !t.quit {
		t.mu.Lock()
		t.draw()
		t.mu.Unlock()
		n, err := t.in.Read(buf)
		if err != nil {
			return t.err
		}
		keys, rest := parseKeys(append(pending, buf[:n]...), n == len(buf))
		pending = append([]byte(nil), rest...)
		t.mu.Lock()
		for _, k := range keys {
			t.handle(k)
			if t.quit {
				break
			}
		}
		t.mu.Unlock()
	}
	return t.err
}

func enterScreen() { fmt.Print("\x1b[?1049h\x1b[?25l") } // alternate screen, hide cursor
func leaveScreen() { fmt.Print("\x1b[?25h\x1b[?1049l") }

// restore leaves the app's screen and puts the terminal back as it was.
func (t *tui) restore() {
	leaveScreen()
	terminal.RestoreTTY(t.tty.Load())
}

// reload reads the settings again. The health checks start several
// programs, so they run only while the Settings page is shown.
func (t *tui) reload() {
	t.cfg = t.app.Accounts.Config()
	t.hooked = t.app.Setup.Installed()
	t.apis = map[string]hosting.Status{}
	for _, st := range t.app.Hosting.Status() {
		t.apis[strings.ToLower(st.Account)] = st
	}
	if t.tab == tabSettings {
		t.checks = t.app.Doctor.Run(false)
	}
	t.loadStashes() // after an undo or any action: the open stash list may have changed
	t.clampSel()
}

// clampSel keeps every page's selection on a row that exists, after a
// list got shorter.
func (t *tui) clampSel() {
	for tab := range t.sel {
		t.sel[tab] = max(0, min(t.sel[tab], t.rowsOf(tab)-1))
	}
}

func (t *tui) say(err error, ok string) {
	if err != nil {
		t.msg, t.msgErr = err.Error(), true
		t.explain(err.Error(), "")
	} else {
		t.msg, t.msgErr = ok, false
	}
}

// welcome shows first-time users the few keys that set GitSwitch up, when
// there is no account yet.
func (t *tui) welcome() {
	if len(t.cfg.Accounts) > 0 {
		return
	}
	t.view = &viewer{title: "Welcome to GitSwitch", lines: []string{
		"GitSwitch picks the right git account (work, personal, ...) for every clone, pull and push.",
		"",
		"To set it up:",
		"  1. On Accounts, press a to add an account. Choose \"new key\" unless you already have one.",
		"  2. Press k to see its public key and the page to add it on (GitHub, GitLab, ...).",
		"  3. After adding it there, press t to test the login.",
		"  4. On Repos (2), press f to add the folder where you keep your projects.",
		"",
		"Prefer questions one at a time? Quit with q and run: gitswitch setup",
		"Or use the desktop app, which has a guided setup: gitswitch ui",
	}}
}

// explain opens a panel saying, in plain words, what a git or ssh error
// means and what to do, when it is one we know. id is the account
// involved, for the keys to suggest. It reports whether it opened.
func (t *tui) explain(msg, id string) bool {
	h := remote.Explain(msg)
	if h == nil {
		return false
	}
	lines := []string{"What happened:", "  " + h.Problem, "", "What to do:", "  " + h.Fix}
	switch {
	case h.Action == "pubkey" && id != "":
		lines = append(lines, "", "Close this, then press k on the Accounts page to see "+id+"'s public key and where to add it.")
	case h.Action == "test" && id != "":
		lines = append(lines, "", "Close this, then press t on the Accounts page to test "+id+" again.")
	}
	lines = append(lines, "", "Details (git's own message):")
	for _, l := range strings.Split(strings.TrimSpace(msg), "\n") {
		if l = history.CleanText(l); strings.TrimSpace(l) != "" {
			lines = append(lines, "  "+l)
		}
	}
	t.view = &viewer{title: "What went wrong", lines: lines}
	return true
}

// suspend leaves the TUI so a command can use the terminal normally
// (ssh-keygen passphrase, ssh login), then comes back.
func (t *tui) suspend(f func()) {
	t.suspended.Store(true)
	defer t.suspended.Store(false)
	t.restore()
	f()
	fmt.Print("\nPress Enter to go back to GitSwitch...")
	if t.in != nil {
		_, _ = t.in.ReadString('\n') // the same reader as the app, so no keys are lost
	}
	st, err := terminal.MakeRaw()
	if err != nil {
		t.tty.Store(nil)
		t.quit, t.err = true, fmt.Errorf("could not switch the terminal back to the app: %v", err)
		return
	}
	t.tty.Store(st)
	enterScreen()
}

// ---------------- keys ----------------

// parseKeys turns the bytes read from the terminal into key names. A key
// cut off at the end (an escape sequence or a character split across two
// reads) comes back as rest, to be put in front of the next read. full
// means the read filled its buffer, so a lone Esc at the end may be the
// start of a sequence rather than the Esc key.
func parseKeys(b []byte, full bool) (keys []string, rest []byte) {
	const maxSeq = 32 // longer is not a key: drop it
	for len(b) > 0 {
		if b[0] == 0x1b {
			if len(b) == 1 {
				if full {
					return keys, b
				}
				keys = append(keys, "esc")
				break
			}
			if b[1] == '[' || b[1] == 'O' {
				j := 2
				for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
					j++
				}
				if j >= len(b) {
					if len(b) > maxSeq {
						return keys, nil
					}
					return keys, b
				}
				seq := string(b[2 : j+1])
				b = b[j+1:]
				switch seq {
				case "A":
					keys = append(keys, "up")
				case "B":
					keys = append(keys, "down")
				case "C":
					keys = append(keys, "right")
				case "D":
					keys = append(keys, "left")
				case "Z":
					keys = append(keys, "backtab")
				case "1;2A":
					keys = append(keys, "shift-up")
				case "1;2B":
					keys = append(keys, "shift-down")
				case "H", "1~":
					keys = append(keys, "home")
				case "F", "4~":
					keys = append(keys, "end")
				case "5~":
					keys = append(keys, "pgup")
				case "6~":
					keys = append(keys, "pgdn")
				case "3~":
					keys = append(keys, "delete")
				}
				continue
			}
			keys = append(keys, "esc")
			b = b[1:]
			continue
		}
		switch b[0] {
		case '\r', '\n':
			keys = append(keys, "enter")
			if b[0] == '\r' && len(b) > 1 && b[1] == '\n' {
				b = b[1:]
			}
		case '\t':
			keys = append(keys, "tab")
		case 0x7f, 0x08:
			keys = append(keys, "backspace")
		case 0x03:
			keys = append(keys, "ctrl-c")
		case 0x15:
			keys = append(keys, "ctrl-u")
		default:
			if b[0] < 0x20 {
				b = b[1:]
				continue
			}
			if !utf8.FullRune(b) {
				return keys, b
			}
			r, size := utf8.DecodeRune(b)
			keys = append(keys, string(r))
			b = b[size:]
			continue
		}
		b = b[1:]
	}
	return keys, nil
}

func (t *tui) handle(k string) {
	if k == "ctrl-c" {
		t.quit = true
		return
	}
	switch {
	case t.view != nil:
		t.handleView(k)
	case t.confirm != nil:
		// Only the keys the question names do something, so a stray key
		// neither confirms nor cancels. Case matters (D is not d), except y.
		c := t.confirm
		f, ok := c.actions[k]
		if !ok && k == "Y" {
			f, ok = c.actions["y"]
		}
		switch {
		case ok:
			t.confirm = nil
			t.msg = ""
			// An action may ask a follow-up question, show a page or open
			// a form or list instead of finishing.
			if err := f(); err != nil || (t.msg == "" && t.confirm == nil && t.view == nil && t.form == nil && t.picker == nil) {
				t.say(err, "Done.")
			}
			t.reload()
		case k == "n" || k == "N" || k == "esc":
			t.confirm = nil
			t.say(nil, "Cancelled.")
		}
	case t.picker != nil:
		t.handlePicker(k)
	case t.form != nil:
		t.handleForm(k)
	case t.hv != nil && t.tab == tabHistory && t.keyHunks(k):
	case t.conf != nil && t.tab == tabHistory && t.keyConflict(k):
	case t.opv != nil && t.tab == tabHistory && t.keyOperation(k):
	case t.stashes != nil && t.tab == tabHistory && t.keyStashes(k):
	case t.inspecting() && t.tab == tabHistory && t.keyInspect(k):
	case t.changes != nil && t.tab == tabHistory && t.keyChanges(k):
	case t.rwEd != nil && t.tab == tabHistory && t.keyRewrite(k):
	case t.wtView != nil && t.tab == tabHistory && t.keyWorktrees(k):
	case t.subView != nil && t.tab == tabHistory && t.keySubmodules(k):
	default:
		t.handleMain(k)
	}
}

func (t *tui) handleView(k string) {
	v := t.view
	page := t.overlayRows()
	switch k {
	case "esc", "enter", "q":
		t.view = nil
	case "up", "k":
		v.off = max(0, v.off-1)
	case "down", "j":
		v.off++ // drawing stops it at the last page
	case "pgup":
		v.off = max(0, v.off-page)
	case "pgdn", " ":
		v.off += page
	case "home", "g":
		v.off = 0
	case "end", "G":
		v.off = 1 << 30 // drawing stops it at the last page

	case "c":
		if v.copy != "" && process.CopyToClipboard(v.copy) {
			t.say(nil, "Copied to clipboard.")
		}
	case "o":
		if v.url != "" {
			t.say(process.OpenBrowser(v.url), "Opened "+v.url)
		}
	}
}

func (t *tui) handlePicker(k string) {
	p := t.picker
	n := len(p.shown())
	page := max(1, t.pickerRows())
	switch k {
	case "up":
		p.sel = max(0, p.sel-1)
	case "down":
		p.sel = max(0, min(n-1, p.sel+1))
	case "pgup":
		p.sel = max(0, p.sel-page)
	case "pgdn":
		p.sel = max(0, min(n-1, p.sel+page))
	case "home":
		p.sel = 0
	case "end":
		p.sel = max(0, n-1)
	case "esc": // clears the filter first, then closes
		if p.filter != "" {
			p.filter, p.sel = "", 0
		} else {
			t.picker = nil
		}
	case "backspace":
		if p.filter != "" {
			_, size := utf8.DecodeLastRuneInString(p.filter)
			p.filter, p.sel = p.filter[:len(p.filter)-size], 0
		}
	case "ctrl-u":
		p.filter, p.sel = "", 0
	case "enter":
		shown := p.shown()
		if p.sel >= len(shown) {
			return
		}
		t.picker = nil
		if err := p.pick(shown[p.sel]); err != nil {
			t.say(err, "")
		}
		t.reload()
	default:
		if utf8.RuneCountInString(k) == 1 {
			p.filter, p.sel = p.filter+k, 0
		}
	}
}

func (t *tui) handleForm(k string) {
	f := t.form
	fl := &f.fields[f.focus]
	switch k {
	case "esc":
		t.form = nil
		t.say(nil, "Cancelled.")
	case "tab", "down":
		f.focus = (f.focus + 1) % len(f.fields)
	case "backtab", "up":
		f.focus = (f.focus + len(f.fields) - 1) % len(f.fields)
	case "left", "right":
		if len(fl.options) > 0 {
			i := indexOf(fl.options, fl.value)
			if k == "right" {
				i = (i + 1) % len(fl.options)
			} else {
				i = (i + len(fl.options) - 1) % len(fl.options)
			}
			fl.value = fl.options[i]
			if f.changed != nil {
				f.changed(f)
			}
		}
	case "backspace":
		if len(fl.options) == 0 && fl.value != "" {
			_, size := utf8.DecodeLastRuneInString(fl.value)
			fl.value = fl.value[:len(fl.value)-size]
		}
	case "ctrl-u":
		if len(fl.options) == 0 {
			fl.value = ""
		}
	case "enter":
		if f.focus < len(f.fields)-1 {
			f.focus++
			return
		}
		vals := make([]string, len(f.fields))
		for i, x := range f.fields {
			vals[i] = strings.TrimSpace(x.value)
		}
		t.form = nil
		if err := f.submit(vals); err != nil {
			t.say(err, "")
		}
		t.reload()
	default:
		if len(fl.options) == 0 && utf8.RuneCountInString(k) == 1 {
			fl.value += k
		}
	}
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return 0
}

func (t *tui) rows() int { return t.rowsOf(t.tab) }

func (t *tui) rowsOf(tab int) int {
	if t.cfg == nil {
		return 0
	}
	switch tab {
	case tabAccounts:
		return len(t.cfg.Accounts)
	case tabRepos:
		return len(t.repos)
	case tabHistory:
		return len(t.histLines)
	case tabRules:
		return len(t.cfg.Rules)
	case tabSettings:
		return settingRows + len(t.checks)
	case tabReviews:
		return len(t.rvRows)
	}
	return 0
}

func (t *tui) switchTab(i int) {
	t.tab = (i + len(tabNames)) % len(tabNames)
	t.msg = ""
	if t.tab == tabRepos && !t.scanned {
		t.scanRepos()
	}
	if t.tab == tabHistory && t.histLines == nil {
		t.loadHistory()
	}
	if t.tab == tabSettings {
		t.checks = t.app.Doctor.Run(false)
		t.clampSel()
	}
	if t.tab == tabReviews {
		t.loadReviews(hostingapp.FetchStale)
	}
}

func (t *tui) handleMain(k string) {
	n := t.rows()
	page := max(1, t.h-6)
	s := &t.sel[t.tab]
	switch k {
	case "q": // not Esc: a second Esc after closing a form must not quit
		t.quit = true
		return
	case "tab", "right":
		t.switchTab(t.tab + 1)
		return
	case "backtab", "left":
		t.switchTab(t.tab - 1)
		return
	case "1", "2", "3", "4", "5", "6":
		t.switchTab(int(k[0] - '1'))
		return
	case "up", "k":
		if *s > 0 {
			*s--
		}
		return
	case "down", "j":
		if *s < n-1 {
			*s++
		}
		return
	case "pgup":
		*s = max(0, *s-page)
		return
	case "pgdn":
		*s = max(0, min(n-1, *s+page))
		return
	case "home", "g":
		*s = 0
		return
	case "end", "G":
		*s = max(0, n-1)
		return
	case "?":
		t.view = &viewer{title: "Keys", lines: strings.Split(strings.TrimSpace(helpText), "\n")}
		return
	}
	switch t.tab {
	case tabAccounts:
		t.keyAccounts(k)
	case tabRepos:
		t.keyRepos(k)
	case tabHistory:
		t.keyHistory(k)
	case tabRules:
		t.keyRules(k)
	case tabSettings:
		t.keySettings(k)
	case tabReviews:
		t.keyReviews(k)
	}
}

const helpText = `
Everywhere
  1-5 or Tab / ←→   switch page          ↑↓ PgUp PgDn   move
  q or Ctrl+C       quit                  ?              this help
Accounts
  a add   e edit   R rename   t test login   k public key   K use another key   d default (again: no default, always ask)
  p passphrase to agent   x remove (then y keeps the key files, Shift+D deletes them too)
  g sign in to GitHub or GitLab with a token (kept in your keychain)   G sign out
  s sign commits as this account: off, ssh key or gpg key (then the public key to add on the host)
Repos
  Enter choose account   h history   c clone by URL   f add folder   a add one repo   r rescan
  n start a new repo (git init: folder, first branch, account)
  W worktrees of the repo
  p pin / unpin   o open folder   O open in editor   x remove from list (files untouched)
  b browse the repos your signed-in accounts can see on GitHub or GitLab (type to filter; Enter clones or opens one)
  g group the list by nothing, account, owner, folder or your groups   G put the repo in a group you name
  s sort by folder, name, recently opened or account (pinned stay first)
History
  Enter commit details   o other repo   f only wrong-account commits   x fix not-pushed commits
  r reload   m load more
  b switch branch   B branches: switch, rename, set the remote branch it pulls from, delete (asks; again for commits on no other branch)
  t tag the selected commit (with a message: annotated)   T tags: push or delete (asks)
  F fetch (read new commits from the remote)   u update (pull)   p push (never forced; -u the first time; commits made as another account: fix first)
  i make new commits with the repo account's name and email (when the History header says they aren't)   w view in browser   K the branch's pull request   / filter by author, message, date or account
  U undo the last commit, switch, move, prune, revert, drop, roll back, cherry-pick or fix   Y redo what U took back (asks first)
  O remotes and more: list, add, rename, change URL, remove remotes; delete a branch on the server; fetch and forget deleted branches;
    force push with lease (shows the commits it overwrites, then you type yes; never a plain force); check out a commit or tag (detached); Back
  E back to the branch from a detached HEAD (warns about commits made there that are on no branch)
  a check mark before a commit means it is signed, when the repo's account signs: green good, red bad or revoked, amber expired, dim cannot be checked here
  R after a smart switch with clashing files: I've fixed them, go back, or park them instead
  P parked changes (git stash): restore, look first or delete
  h stashes: every git stash (see Stashes below)
  L rewrite history: rebase onto a branch, interactive rebase, reword, fold staged changes into an older commit (not-pushed commits only)
  l reword the selected commit   W worktrees (see below)   V submodules (see below)
  n new branch here   N new branch at the selected commit (uncommitted changes come along)
  M committed on the wrong branch: move the commits not pushed to a new branch
  D delete the branches merged into the remote's default branch
  S compare with the remote branch: the commits you would push and pull
  I inspect: blame a file, a file's history, compare two refs, reflog, bisect, search code changes (see Inspect below)
  C commit the staged files as the repo's account (nothing staged: every change, new files too)
    in its form, ↑ then ←→ amends the last commit instead (its message filled in; says why when it can't)
  s uncommitted changes: the files changed, staged or not, and in conflict (see Changes below)
  on a commit: v revert   d drop   z roll back (reset) to here   c cherry-pick   Space mark several to cherry-pick
Changes (s on History)
  ↑↓ or j/k move   Enter or d the file's diff (staged or not, as the section says)   r read again   Esc or q back
  s stage   u unstage   x discard: back to the last commit (asks first; the changes are kept in git stash)
  a stage all   A unstage all   X discard all (asks first)   C commit (the staged files, or every change when none is staged) or amend
  i ignore the file, its extension or its folder, in .gitignore or only for me (offers to untrack a tracked file)
Merge, operation in progress, conflicts, hunks
  e on History: merge a branch into the current one (pick it, read the plan: commits coming in and likely clashes; f fast-forward, m merge commit, s squash)
    also m in the branch list (B). When git is stopped in a merge, rebase, cherry-pick or revert, History says so and e opens the Operation page
  Operation: ↑↓ move   Enter fix the selected file   c continue   s skip (not for a merge)   a abort (asks first)   r read again   Esc back
  Conflict: ↑↓ PgUp PgDn move   v the file with markers, ours, theirs, the base   n / p next / previous clash
    o ours   t theirs   b both, for the clash under the cursor   u forget the choice   w save the file with your choices (marks it resolved)
    O / T keep ours / theirs for the whole file (asks)   d mark resolved as it is (refused while markers remain)
    e open in $VISUAL or $EDITOR, then read again   r read again   Esc back
  Hunks (H in Changes): ↑↓ move   n / p next / previous hunk   Space pick a changed line (on a hunk header: the whole hunk)   c clear picks
    s stage   u unstage   x discard (asks; kept in git stash, with the command to bring it back): the picked lines, else the hunk under the cursor
    "the file changed since you looked": r reads it again
Stashes (h on History)
  ↑↓ or j/k move   Enter the stash's files, then a file's diff (new files too)   Esc or q back (one level)
  a apply (keeps the stash)   p pop (apply, then remove it)   both need no uncommitted changes; a clash changes nothing
  d delete (asks; U brings it back)   n new branch from the stash   s stash the uncommitted changes (message form)   r read again
Inspect (I on History)
  Blame: ↑↓ move   Enter the line's commit   b blame before that commit   Backspace back   w ignore whitespace   m moved lines   Esc close
  File history (follows renames): Enter the file's diff in that commit   b blame at it   n older page   p newer page
  Compare: e change A and B (B may be :working)   m since they went apart / direct   Enter a file's diff   r read again
  Reflog: l lost commits only   b make a branch at the entry (asks a name; nothing else moves)   m older entries   Enter the commit
  Bisect: g good   b bad   s skip   x stop and go back (asks)   Esc leaves it running; History shows "Bisecting…"
  Search code changes: filters History to the commits that added or removed a text (or match a regex); empty clears (also / shows it)
Interactive rebase (L on History, then Interactive rebase)
  ↑↓ move   p pick   r reword   s squash   f fixup   d drop   J / K or Shift+↓/↑ move the commit down / up
  Enter on a reword row edits its message, on another row shows the commit   v show the commit
  The After list shows what the commits become; x checks, asks for the messages still missing, shows before and after, then runs   Esc cancels
  Stopped on clashes? Press e for the operation view: fix the files, then continue, skip or abort
Worktrees (W on History, or on Repos)
  ↑↓ move   Enter its history   a add (new branch, existing branch or detached; can join the repo list)   x remove (asks; work inside asks again: F)
  p prune the ones whose folder is gone (asks)   l lock   u unlock   r read again   Esc back
Submodules (V on History)
  ↑↓ move   Enter details   i init   u update to the recorded commit (asks)   m update to the tracked branch's tip (asks; you then commit the change)
  I U M the same for every submodule   s sync URLs   a add (url, folder, branch)   x remove (planned first; work inside asks again: F)   r read again   Esc back
Rules
  a add rule   x remove rule
Settings
  Enter toggle or fix the selected item   r run the health checks again
Reviews (6)
  Enter or o open the pull or merge request in the browser   r read them again from the hosts
In forms
  Tab / ↑↓ next field   ←→ change choice   Enter next / save   Esc cancel   Ctrl+U clear field
In lists that pop up (branch, repo, key)
  type to filter   ↑↓ PgUp PgDn choose   Enter select   Esc clear the filter, then close
Questions
  y yes (or the key the question names)   n or Esc no; other keys do nothing
`

// ---------------- accounts ----------------

func (t *tui) selAccount() *account.Account {
	i := t.sel[tabAccounts]
	if i < 0 || i >= len(t.cfg.Accounts) {
		return nil
	}
	return &t.cfg.Accounts[i]
}

func (t *tui) accountForm(a *account.Account) {
	edit := a != nil
	if a == nil {
		a = &account.Account{Host: "github.com"}
		a.Name = t.app.Accounts.SuggestedName()
		if a.Name == "" && len(t.cfg.Accounts) > 0 {
			a.Name = t.cfg.Accounts[0].Name
		}
	}
	keyOpts := []string{"Create a new key"}
	for _, k := range t.app.Keys.Existing() {
		if k.UsedBy == "" {
			keyOpts = append(keyOpts, k.Path)
		}
	}
	keyOpts = append(keyOpts, "Type a path below")
	fields := []field{
		{label: "Short name", value: a.ID, hint: "e.g. work, personal, client-acme"},
		{label: "Git host", value: a.Host},
		{label: "Commit name", value: a.Name},
		{label: "Commit email", value: a.Email},
	}
	if edit {
		fields[0].hint = "cannot be changed"
		fields = append(fields, field{label: "Key file", value: a.Key})
	} else {
		fields = append(fields,
			field{label: "Key", value: keyOpts[0], options: keyOpts, hint: "←→ new key or one of your keys in ~/.ssh"},
			field{label: "Key path (if typing one)", value: "", hint: "e.g. ~/.ssh/id_ed25519"})
	}
	title := "Add account"
	if edit {
		title = "Edit " + a.ID
	}
	orig := a.ID
	t.form = &form{title: title, fields: fields, submit: func(v []string) error {
		acc := account.Account{ID: v[0], Host: v[1], Name: v[2], Email: v[3]}
		if edit {
			acc.ID, acc.Key, acc.User = orig, v[4], a.User
		}
		if acc.Email == "" {
			return fmt.Errorf("commit email is required")
		}
		if !edit && t.cfg.Account(acc.ID) != nil {
			return fmt.Errorf("an account called %s already exists", acc.ID)
		}
		newKey := !edit && v[4] == keyOpts[0]
		if !edit && !newKey {
			if v[4] == "Type a path below" {
				acc.Key = v[5]
			} else {
				acc.Key = v[4]
			}
			if acc.Key == "" {
				return fmt.Errorf("type the key path, or choose \"Create a new key\"")
			}
		}
		if edit {
			if err := t.app.Accounts.Update(acc); err != nil {
				return err
			}
		} else if _, err := t.app.Accounts.Add(acc, account.KeyChoice{}); err != nil {
			return err
		}
		if newKey {
			var kerr error
			t.suspend(func() {
				fmt.Printf("Creating a key for %s. ssh-keygen asks for a passphrase (recommended; press Enter for none).\n\n", acc.ID)
				_, kerr = t.app.Accounts.GenerateKey(acc.ID, true, "")
			})
			if kerr != nil {
				return kerr
			}
			t.reload()
			t.showKey(t.cfg.Account(acc.ID))
		}
		t.say(nil, "Saved "+acc.ID+".")
		return nil
	}}
	if edit {
		t.form.focus = 1
	}
}

func (t *tui) showKey(a *account.Account) {
	if a == nil {
		return
	}
	pub, help, err := t.app.Keys.PublicKey(a.ID)
	if err != nil {
		t.say(err, "")
		return
	}
	copied := "c  copy to clipboard"
	if process.CopyToClipboard(pub) {
		copied = "Copied to clipboard (c to copy again)."
	}
	lines := []string{"Public key for " + a.ID + ":", "", pub, "", copied, "", "Add it on " + a.Host + ":"}
	for i, s := range help.Steps {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, s))
	}
	if help.Page != "" {
		lines = append(lines, "  "+help.Page, "", "o  open that page")
	}
	lines = append(lines, "Enter  close, then press t to test the login")
	t.view = &viewer{title: "Public key: " + a.ID, lines: lines, url: help.Page, copy: pub}
}

func (t *tui) keyAccounts(k string) {
	a := t.selAccount()
	switch k {
	case "a":
		t.accountForm(nil)
	case "e", "enter":
		if a != nil {
			t.accountForm(a)
		}
	case "k":
		t.showKey(a)
	case "s":
		t.signingForm(a)
	case "R":
		if a == nil {
			return
		}
		old := a.ID
		t.form = &form{title: "Rename " + old, fields: []field{{label: "New short name", value: old}}, submit: func(v []string) error {
			n, err := t.app.Accounts.Rename(old, v[0])
			if err == nil {
				t.say(nil, fmt.Sprintf("Renamed %s to %s (%d repo(s) updated).", old, v[0], n))
			}
			return err
		}}
	case "K":
		if a == nil {
			return
		}
		found := t.app.Keys.Existing()
		if len(found) == 0 {
			t.say(fmt.Errorf("no key pairs in ~/.ssh; press e to edit or a to add with a new key"), "")
			return
		}
		var opts []string
		for _, k := range found {
			o := k.Path
			if k.UsedBy != "" {
				o += "  (used by " + k.UsedBy + ")"
			}
			opts = append(opts, o)
		}
		id := a.ID
		t.picker = &picker{title: "Key for " + id, options: opts, pick: func(i int) error {
			if err := t.app.Accounts.UseKey(id, found[i].Path); err != nil {
				return err
			}
			t.say(nil, id+" now uses "+found[i].Path)
			return nil
		}}
	case "g":
		if a == nil {
			return
		}
		id, host := a.ID, a.Host
		if _, err := hosting.APIBase(host); err != nil {
			t.say(err, "")
			return
		}
		page, needs := hosting.TokenHelp(host)
		prov := hosting.ProviderName(host)
		t.form = &form{title: "Sign " + id + " in to " + prov + ": create a token at " + page + " (" + strings.TrimSuffix(needs, " is enough.") + " is enough)",
			fields: []field{{label: "Token", secret: true, hint: "kept in your keychain, never in git config"}},
			submit: func(v []string) error {
				r, err := t.app.Hosting.SignIn(id, v[0])
				if err != nil {
					return err
				}
				t.reload()
				msg := id + " is signed in to " + prov + " as " + r.Login + "."
				if r.HTTPS == "" {
					msg += " Git uses the token for HTTPS remotes of its repos."
				}
				t.say(nil, msg)
				return nil
			}}
	case "G":
		if a == nil || !t.apis[strings.ToLower(a.ID)].SignedIn {
			return
		}
		id, prov := a.ID, hosting.ProviderName(a.Host)
		t.confirm = &confirmBox{"Sign " + id + " out of " + prov + "? Its token is removed from your keychain (delete it on " + prov + " to revoke it). (y/n)",
			map[string]func() error{"y": func() error {
				if err := t.app.Hosting.SignOut(id); err != nil {
					return err
				}
				t.reload()
				t.say(nil, id+" is signed out of "+prov+".")
				return nil
			}}}
	case "t":
		if a == nil {
			return
		}
		acc := *a
		var r ssh.TestResult
		t.suspend(func() {
			fmt.Printf("Testing %s on %s...\n", acc.ID, acc.Host)
			r, _ = t.app.Keys.Test(acc.ID, false)
			if r.OK {
				fmt.Printf("\nConnected as %s.\n", r.User)
			} else {
				fmt.Printf("\nCould not log in:\n%s\n", r.Output)
			}
		})
		if r.OK {
			t.say(nil, acc.ID+" logs in as "+r.User+".")
		} else {
			t.msg, t.msgErr = acc.ID+" could not log in. Press k to see its public key", true
			t.explain(r.Output, acc.ID)
		}
		t.reload()
	case "p":
		if a == nil {
			return
		}
		acc := *a
		var err error
		t.suspend(func() { err = t.app.Keys.AddToAgent(acc.ID) })
		t.say(err, "Key loaded; IDEs won't need the passphrase.")
	case "d":
		// d on the default turns the default off: every new repo asks,
		// and an answer is saved only when ticked (2!).
		if a != nil && strings.EqualFold(a.ID, t.cfg.Default) {
			t.say(t.app.Accounts.SetDefault(""), "Default removed: repos with nothing saved ask; answer 2! to save it for the repo.")
			t.reload()
		} else if a != nil {
			t.say(t.app.Accounts.SetDefault(a.ID), a.ID+" is now the default.")
			t.reload()
		}
	case "x":
		if a != nil {
			id := a.ID
			acc := *a
			t.confirm = &confirmBox{"Remove " + id + " and its rules?  y = remove, keep the key files · D (Shift+d) = remove AND delete the key files · n or Esc = cancel",
				map[string]func() error{
					"y": func() error { t.sel[tabAccounts] = 0; _, err := t.app.Accounts.Remove(id, false); return err },
					"D": func() error {
						t.sel[tabAccounts] = 0
						if _, err := t.app.Accounts.Remove(id, true); err != nil {
							return err
						}
						t.say(nil, "Removed "+id+" and deleted its key files. Delete its public key on "+acc.Host+" too.")
						return nil
					},
				}}
		}
	}
}

// ---------------- reviews ----------------

// loadReviews reads the signed-in accounts' pull requests: from the cache,
// from GitHub when it is older than 5 minutes (fetch FetchStale) or now
// (FetchAll).
func (t *tui) loadReviews(fetch hostingapp.Fetch) {
	t.reviews = t.app.Hosting.Reviews(fetch)
	t.rvRows = nil
	for _, in := range t.reviews {
		for _, l := range [][]hosting.PullRequest{in.ToReview, in.Mine, in.Reviewed} {
			t.rvRows = append(t.rvRows, l...)
		}
	}
	t.clampSel()
}

func (t *tui) keyReviews(k string) {
	i := t.sel[tabReviews]
	switch k {
	case "r":
		t.loadReviews(hostingapp.FetchAll)
		t.say(nil, "Read again from the hosts.")
	case "enter", "o":
		if i >= 0 && i < len(t.rvRows) {
			p := t.rvRows[i]
			t.say(process.OpenBrowser(p.URL), "Opened "+p.Repo+fmt.Sprintf("%s%d", orMark(p.Mark), p.Number)+".")
		}
	}
}

// orMark is the mark that goes before a request's number: # when unknown.
func orMark(m string) string {
	if m == "" {
		return "#"
	}
	return m
}

func (t *tui) drawReviews() ([]string, int) {
	if len(t.reviews) == 0 {
		return []string{" No account is signed in to GitHub or GitLab. Sign one in with g on the Accounts page (1) to see your pull and merge requests here."}, -1
	}
	var l []string
	selLine, row := -1, 0
	section := func(title string, prs []hosting.PullRequest, author bool) {
		l = append(l, " "+cDim+fmt.Sprintf("%s (%d)", title, len(prs))+cReset)
		if len(prs) == 0 {
			l = append(l, "   "+cDim+"none"+cReset)
		}
		for _, p := range prs {
			on := row == t.sel[tabReviews]
			if on {
				selLine = len(l)
			}
			meta := p.Repo + fmt.Sprintf("%s%d", orMark(p.Mark), p.Number)
			if author {
				meta += " · " + p.Author
			}
			var chips []string
			for _, c := range p.Chips {
				switch {
				case c.Bad:
					chips = append(chips, cRed+c.Text+cReset)
				case c.Kind == "ready":
					chips = append(chips, cGreen+c.Text+cReset)
				default:
					chips = append(chips, cDim+c.Text+cReset)
				}
			}
			l = append(l, fmt.Sprintf("%s%s%s  %s%s  %s", mark(on), boldIf(on), history.CleanText(p.Title), cReset+cDim, meta+cReset, strings.Join(chips, " · ")))
			row++
		}
	}
	for i, in := range t.reviews {
		if i > 0 {
			l = append(l, "")
		}
		head := " " + t.color(in.Account) + in.Account + cReset + " " + cBold + in.Login + cReset + cDim + " on " + in.Host
		if !in.Fetched.IsZero() {
			head += " · read " + refs.Ago(time.Now().Unix(), in.Fetched.Unix())
		}
		l = append(l, head+cReset)
		if in.Error != "" {
			l = append(l, " "+cRed+"could not read them: "+in.Error+cReset)
		}
		section("Waiting for your review", in.ToReview, true)
		section("Your pull requests", in.Mine, false)
		section("Reviewed by you lately", in.Reviewed, true)
	}
	return l, selLine
}

// ---------------- repos ----------------

// cloneForm asks where and as whom to clone, prefilled with url, dir and
// acct ("automatic" lets rules decide).
func (t *tui) cloneForm(url, dir, acct string) {
	t.form = &form{title: "Clone a repo by URL", fields: []field{
		{label: "URL", value: url, hint: "git@github.com:owner/repo.git, git@gitlab.com:group/repo.git or https://..."},
		{label: "Folder", value: dir, hint: "empty = ./<repo name> here"},
		{label: "Account", value: acct, options: append([]string{"automatic"}, t.cfg.SortedIDs()...), hint: "←/→ · automatic = rules, then ask"},
	}, submit: func(v []string) error {
		if v[2] == "automatic" {
			v[2] = ""
		}
		dir := v[1]
		if dir == "" {
			dir = remote.RepoName(v[0])
		}
		var ri repo.Info
		var err error
		t.suspend(func() { // git prints its progress straight to the terminal
			fmt.Println("Cloning " + v[0] + " ...")
			ri, err = t.app.Repos.Clone(v[0], dir, v[2], func(l string) { fmt.Print("\r\x1b[K  " + l) })
			fmt.Println()
			if err != nil {
				fmt.Println("gitswitch:", err)
			}
		})
		if err != nil {
			return err
		}
		t.reload()
		t.scanRepos()
		t.say(nil, "Cloned into "+ri.Path+".")
		return nil
	}}
}

// browseGitHub picks one of the repos the signed-in accounts can see:
// a cloned one opens its history, another is offered for cloning.
func (t *tui) browseGitHub() {
	b := t.app.Hosting.Browse(hostingapp.FetchStale)
	if len(b.Accounts) == 0 {
		t.say(errors.New("no account is signed in to GitHub or GitLab; sign one in with g on the Accounts page"), "")
		return
	}
	for _, a := range b.Accounts {
		if a.Error != "" {
			t.say(errors.New(a.Account+": "+a.Error+" (showing its last list)"), "")
		}
	}
	var opts []string
	var rs []hosting.RemoteRepo
	for _, r := range b.Repos {
		if r.Archived {
			continue
		}
		label := r.FullName
		if r.Private {
			label += "  · private"
		}
		if r.Language != "" {
			label += "  · " + r.Language
		}
		label += "  · " + r.Account
		if len(r.Cloned) > 0 {
			label += "  · cloned at " + r.Cloned[0]
		}
		opts, rs = append(opts, label), append(rs, r)
	}
	if len(opts) == 0 {
		t.say(nil, "Your signed-in accounts see no repos on their hosts.")
		return
	}
	t.picker = &picker{title: fmt.Sprintf("Hosted repos: %d · type to filter · Enter opens a cloned one or clones it", len(opts)), options: opts,
		pick: func(i int) error {
			r := rs[i]
			if len(r.Cloned) > 0 {
				if gd := t.app.Repos.At(r.Cloned[0]); gd != "" {
					t.openHistory(gd)
					t.tab = tabHistory
					return nil
				}
			}
			t.cloneForm(r.SSHURL, r.Name, r.Account)
			return nil
		}}
}

// openHistory shows a repo's history and records it as recently opened.
func (t *tui) openHistory(gitDir string) {
	t.histDir = gitDir
	t.changes, t.stashes = nil, nil
	// The fetch-on-open setting: once per repo while the app runs. A failed
	// fetch (offline) is not an error here; F says why when asked.
	if t.app.Refs.FetchOnOpen() && !t.fetched[gitDir] {
		if t.fetched == nil {
			t.fetched = map[string]bool{}
		}
		t.fetched[gitDir] = true
		_, _ = t.app.Refs.Fetch(gitDir, func(string) {})
	}
	_ = t.app.Repos.Opened(gitDir) // only the order of the recent list depends on it
	// Move it to the front of the recent ones here too, without reading
	// every repo again.
	was := 0
	for _, r := range t.repos {
		if r.GitDir == gitDir {
			was = r.Recent
		}
	}
	for i := range t.repos {
		switch r := &t.repos[i]; {
		case r.GitDir == gitDir:
			r.Recent = 1
		case r.Recent > 0 && (was == 0 || r.Recent < was):
			r.Recent++
		}
	}
	t.loadHistory()
}

// recentRepos are the listed repos opened lately, newest first, at most 5.
func (t *tui) recentRepos() []repo.Info {
	var out []repo.Info
	for _, r := range t.repos {
		if r.Recent > 0 && !r.Missing {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Recent < out[j].Recent })
	return out[:min(len(out), 5)]
}

// repoHeading is a group heading above repo at on the Repos page.
type repoHeading struct {
	at   int
	name string
	n    int
}

func (t *tui) scanRepos() {
	t.repos, t.groups = nil, nil
	groups, err := t.app.Repos.Groups("", "")
	if err != nil {
		groups = []repo.Group{{Repos: t.app.Repos.List()}}
	}
	for _, g := range groups {
		if g.Name != "" {
			t.groups = append(t.groups, repoHeading{len(t.repos), g.Name, len(g.Repos)})
		}
		t.repos = append(t.repos, g.Repos...)
	}
	if gd := t.app.Repos.Current(); gd != "" {
		found := false
		for _, r := range t.repos {
			if r.GitDir == gd {
				found = true
			}
		}
		if !found {
			t.repos = append([]repo.Info{t.app.Repos.Info(gd)}, t.repos...)
			for i := range t.groups {
				t.groups[i].at++
			}
			if len(t.groups) > 0 {
				t.groups = append([]repoHeading{{0, "This folder (not in the list)", 1}}, t.groups...)
			}
		}
	}
	t.scanned = true
}

// groupByNames are the Repos page's ways to group, as g cycles them.
var groupByNames = map[string]string{repo.ByNone: "nothing", repo.ByAccount: "account", repo.ByOwner: "owner or organization", repo.ByFolder: "folder", repo.ByGroup: "your groups"}

// keepSelected selects the repo at path again after the list changed.
func (t *tui) keepSelected(path string) {
	for j, x := range t.repos {
		if x.Path == path {
			t.sel[tabRepos] = j
		}
	}
}

func (t *tui) keyRepos(k string) {
	i := t.sel[tabRepos]
	var r *repo.Info
	if i >= 0 && i < len(t.repos) {
		r = &t.repos[i]
	}
	switch k {
	case "f":
		t.form = &form{title: "Add a folder with your projects", fields: []field{{label: "Folder", hint: "e.g. ~/work or D:\\Projects"}},
			submit: func(v []string) error {
				if err := t.app.Accounts.AddScanFolder(v[0]); err != nil {
					return err
				}
				t.reload()
				t.scanRepos()
				t.say(nil, "Folder added.")
				return nil
			}}
	case "r":
		t.reload()
		t.scanRepos()
		t.say(nil, fmt.Sprintf("Found %d repos.", len(t.repos)))
	case "enter":
		if r == nil {
			return
		}
		opts := []string{"Follow rules and default", "Ask every time"}
		for _, a := range t.cfg.Accounts {
			opts = append(opts, a.ID+"  "+a.Email)
		}
		gd := r.GitDir
		idx := i
		sel := 0
		if strings.EqualFold(r.Saved, account.AskEveryTime) {
			sel = 1
		}
		for j, a := range t.cfg.Accounts {
			if strings.EqualFold(a.ID, r.Saved) {
				sel = j + 2
			}
		}
		t.picker = &picker{title: "Account for " + filepath.Base(r.Path), options: opts, sel: sel,
			pick: func(p int) error {
				if p == 0 {
					_, _ = t.app.Accounts.SetRepoAccount(gd, "")
					t.say(nil, "This repo follows your rules and default, or asks.")
				} else if p == 1 {
					_, _ = t.app.Accounts.SetRepoAccount(gd, account.AskEveryTime)
					t.say(nil, "This repo asks on every pull and push.")
				} else {
					a := t.cfg.Accounts[p-2]
					removed, err := t.app.Accounts.SetRepoAccount(gd, a.ID)
					if err != nil {
						return err
					}
					msg := "This repo now uses " + a.ID + "."
					if removed != "" {
						msg += " Removed its own core.sshCommand that bypassed gitswitch."
					}
					t.say(nil, msg)
				}
				t.repos[idx] = t.app.Repos.Info(gd)
				return nil
			}}
	case "h":
		if r != nil && !r.Missing {
			t.openHistory(r.GitDir)
			t.tab = tabHistory
		}
	case "W": // the repo's worktrees
		if r != nil && !r.Missing {
			t.openHistory(r.GitDir)
			t.tab = tabHistory
			t.openWorktrees()
		}
	case "c":
		t.cloneForm("", "", "automatic")
	case "n":
		t.initForm()
	case "b":
		t.browseGitHub()
	case "a":
		t.form = &form{title: "Add an existing repo (nothing in it changes)", fields: []field{{label: "Repo folder", hint: "e.g. ~/code/my-app"}},
			submit: func(v []string) error {
				ri, err := t.app.Repos.Add(v[0])
				if err != nil {
					return err
				}
				t.reload()
				t.scanRepos()
				t.say(nil, "Added "+ri.Path+".")
				return nil
			}}
	case "p":
		if r == nil {
			return
		}
		path, on := r.Path, !r.Pinned
		t.say(t.app.Repos.Pin(path, on), map[bool]string{true: "Pinned.", false: "Unpinned."}[on])
		t.reload()
		t.scanRepos()
		t.keepSelected(path) // it moved
	case "g":
		by := t.app.Repos.GroupBy()
		next := repo.GroupBys[0]
		for j, b := range repo.GroupBys {
			if b == by {
				next = repo.GroupBys[(j+1)%len(repo.GroupBys)]
			}
		}
		path := ""
		if r != nil {
			path = r.Path
		}
		if err := t.app.Repos.SetGroupBy(next); err != nil {
			t.say(err, "")
			return
		}
		t.reload()
		t.scanRepos()
		t.keepSelected(path)
		t.say(nil, "Grouped by "+groupByNames[next]+". g changes it.")
	case "s":
		by := t.app.Repos.SortBy()
		next := repo.SortBys[0]
		for j, b := range repo.SortBys {
			if b == by {
				next = repo.SortBys[(j+1)%len(repo.SortBys)]
			}
		}
		path := ""
		if r != nil {
			path = r.Path
		}
		if err := t.app.Repos.SetSortBy(next); err != nil {
			t.say(err, "")
			return
		}
		t.reload()
		t.scanRepos()
		t.keepSelected(path)
		t.say(nil, "Sorted by "+map[string]string{repo.SortFolder: "folder", repo.SortName: "name", repo.SortRecent: "recently opened", repo.SortAccount: "account"}[next]+" (pinned first). s changes it.")
	case "G":
		if r == nil || r.Missing {
			return
		}
		path, hint := r.Path, "empty = take it out of its group"
		if names := t.app.Repos.GroupNames(); len(names) > 0 {
			hint += " · yours: " + strings.Join(names, ", ")
		}
		t.form = &form{title: "Group for " + filepath.Base(path), fields: []field{{label: "Group", value: r.Group, hint: hint}},
			submit: func(v []string) error {
				if err := t.app.Repos.SetGroup(path, v[0]); err != nil {
					return err
				}
				t.reload()
				t.scanRepos()
				t.keepSelected(path)
				switch {
				case strings.TrimSpace(v[0]) == "":
					t.say(nil, "Taken out of its group.")
				case t.app.Repos.GroupBy() != repo.ByGroup:
					t.say(nil, "In group "+strings.TrimSpace(v[0])+". Press g until the list is grouped by your groups to see it together.")
				default:
					t.say(nil, "In group "+strings.TrimSpace(v[0])+".")
				}
				return nil
			}}
	case "o", "O":
		if r != nil {
			t.say(t.app.Repos.Open(r.Path, k == "O"), "Opened "+filepath.Base(r.Path)+".")
		}
	case "x":
		if r == nil {
			return
		}
		path := r.Path
		t.confirm = &confirmBox{"Remove " + path + " from the list? Its files and git settings are not changed. (y/n)",
			map[string]func() error{"y": func() error {
				if err := t.app.Repos.Remove(path); err != nil {
					return err
				}
				t.reload()
				t.scanRepos()
				t.sel[tabRepos] = min(t.sel[tabRepos], max(0, len(t.repos)-1))
				t.say(nil, "Removed from the list.")
				return nil
			}}}
	}
}

// ---------------- history ----------------

// histPage is how many commits the History page reads at a time.
const histPage = 300

// loadHistory reads the history from the top, one page of commits.
func (t *tui) loadHistory() {
	t.sel[tabHistory] = 0
	t.histN = histPage
	t.readHistory()
}

// loadMoreHistory reads another page of older commits and keeps the
// selection where it was.
func (t *tui) loadMoreHistory() {
	if t.hist == nil || !t.hist.More {
		t.say(nil, "No more commits to load.")
		return
	}
	sel := t.sel[tabHistory]
	t.histN += histPage
	t.readHistory()
	t.sel[tabHistory] = min(sel, max(0, len(t.histLines)-1))
}

func (t *tui) readHistory() {
	if t.histDir == "" {
		t.histLines = []string{}
		return
	}
	t.head = nil
	if h, err := t.app.Refs.Head(t.histDir); err == nil {
		t.head = &h
	}
	rows, h, err := t.app.History.Rows(t.histDir, t.histN, t.flagged, true, t.filter)
	if err != nil { // show nothing rather than the previous repo's header
		t.histLines, t.histRows, t.hist, t.head = []string{}, nil, nil, nil
		t.marked = map[string]bool{}
		t.say(err, "")
		return
	}
	t.histRows, t.histLines, t.hist = rows, history.Texts(rows), h
	t.marked = map[string]bool{}
	t.refreshExt()
}

// showCommit opens the selected commit's full message and changed files.
func (t *tui) showCommit() { t.showCommitOf(t.selectedCommit()) }

// showCommitOf opens one commit's full message and changed files.
func (t *tui) showCommitOf(hash string) {
	if hash == "" {
		return
	}
	d, err := t.app.History.Show(t.histDir, hash)
	if err != nil {
		t.say(err, "")
		return
	}
	lines := []string{cDim + d.Hash + cReset, ""}
	for _, l := range strings.Split(d.Message, "\n") {
		lines = append(lines, history.CleanText(l))
	}
	lines = append(lines, "", fmt.Sprintf("%d file(s) changed, %s+%d%s %s-%d%s", len(d.Files)+d.More, cGreen, d.Added, cReset, cRed, d.Deleted, cReset))
	for _, f := range d.Files {
		if f.Binary {
			lines = append(lines, fmt.Sprintf("  %s%-11s%s %s", cDim, "binary", cReset, history.CleanText(f.Path)))
		} else {
			lines = append(lines, fmt.Sprintf("  %s%+5d%s %s%5s%s %s", cGreen, f.Added, cReset, cRed, fmt.Sprintf("-%d", f.Deleted), cReset, history.CleanText(f.Path)))
		}
	}
	if d.More > 0 {
		lines = append(lines, fmt.Sprintf("  … and %d more", d.More))
	}
	short := d.Hash
	if len(short) > 7 {
		short = short[:7]
	}
	t.view = &viewer{title: "Commit " + short, lines: lines, copy: d.Hash}
}

// selectedCommit is the commit on the selected history line, or "".
func (t *tui) selectedCommit() string {
	if i := t.sel[tabHistory]; i < len(t.histRows) {
		return t.histRows[i].Hash
	}
	return ""
}

func (t *tui) keyHistory(k string) {
	switch k {
	case "f":
		t.flagged = !t.flagged
		t.loadHistory()
	case "r":
		t.loadHistory()
	case "m":
		t.loadMoreHistory()
	case "enter":
		t.showCommit()
	case "o":
		if !t.scanned {
			t.scanRepos()
		}
		var opts, dirs []string
		for _, r := range t.recentRepos() {
			opts, dirs = append(opts, r.Path+"  · recent"), append(dirs, r.GitDir)
		}
		for _, r := range t.repos {
			if !r.Missing { // its folder is gone: there is no history to show
				opts, dirs = append(opts, r.Path), append(dirs, r.GitDir)
			}
		}
		if len(opts) == 0 {
			t.say(fmt.Errorf("no repos found; add a projects folder on the Repos page (key f)"), "")
			return
		}
		t.picker = &picker{title: "Show history for", options: opts, pick: func(i int) error {
			t.openHistory(dirs[i])
			return nil
		}}
	case "x":
		if t.histDir == "" || t.hist == nil || t.hist.Flagged == 0 {
			t.say(nil, "Nothing to fix.")
			return
		}
		gd := t.histDir
		t.confirm = &confirmBox{"Rewrite the author of not-pushed wrong-account commits on the current branch? Pushed commits are never changed. (y/n)",
			map[string]func() error{"y": func() error {
				n, err := t.app.History.FixAuthor(gd)
				if err == nil {
					t.loadHistory()
					t.say(nil, fmt.Sprintf("Fixed %d commit(s).", n)+undoHint)
				}
				return err
			}}}
	case "b":
		t.pickBranch()
	case "B":
		t.manageBranches()
	case "t":
		t.newTag()
	case "T":
		t.tags()
	case "F":
		t.fetch()
	case "u":
		t.update()
	case "p":
		t.push()
	case "i":
		if t.hist == nil || !t.hist.Repo.EmailBad {
			t.say(nil, "New commits here already use the repo account's name and email.")
			return
		}
		r := t.hist.Repo
		t.say(t.app.Accounts.UseOnce(r.GitDir, r.Account), "New commits here are made as "+r.Account+".")
		t.loadHistory()
	case "U":
		t.undoLast()
	case "Y":
		t.redoLast()
	case "O":
		t.remotesMenu()
	case "E":
		if t.histDir != "" {
			t.backToBranch(t.histDir)
		}
	case "R":
		t.resolve()
	case "P":
		t.pickParked()
	case "h":
		t.openStashes()
	case "L":
		t.rewriteMenu()
	case "l":
		t.keyRewordSelected()
	case "W":
		t.openWorktrees()
	case "V":
		t.openSubmodules()
	case "e":
		t.mergeKey()
	case "n":
		t.newBranch("")
	case "N":
		if h := t.selectedCommit(); h != "" {
			t.newBranch(h)
		} else {
			t.say(fmt.Errorf("select a commit line first (↑↓)"), "")
		}
	case "M":
		t.moveToNewBranch()
	case "D":
		t.pruneBranches()
	case "C":
		t.commit()
	case "s":
		t.openChanges()
	case "S":
		t.compare()
	case "I":
		t.inspectMenu()
	case "K":
		t.branchPR()
	case "v", "d", "z":
		t.commitAction(k)
	case " ":
		if h := t.selectedCommit(); h != "" {
			t.marked[h] = !t.marked[h]
			if !t.marked[h] {
				delete(t.marked, h)
			}
			if t.sel[tabHistory] < len(t.histLines)-1 {
				t.sel[tabHistory]++
			}
		}
	case "/":
		t.editFilter()
	case "c":
		t.cherryPick()
	case "w":
		hash := t.selectedCommit()
		if t.histDir == "" {
			return
		}
		u, err := t.app.History.WebURL(t.histDir, hash)
		if err == nil {
			err = process.OpenBrowser(u)
		}
		t.say(err, "Opened "+u)
	}
}

// editFilter asks for a commit filter; leaving every field empty clears it.
func (t *tui) editFilter() {
	accts := append([]string{"any"}, t.cfg.SortedIDs()...)
	acct := t.filter.Account
	if acct == "" {
		acct = "any"
	}
	f := t.filter
	t.form = &form{title: "Filter commits (empty = show all)", fields: []field{
		{label: "Author name or email contains", value: f.Author},
		{label: "Message contains", value: f.Text},
		{label: "Since", value: f.Since, hint: "e.g. 2024-05-01 or 2 weeks ago"},
		{label: "Until", value: f.Until, hint: "e.g. 2024-06-30"},
		{label: "Account", value: acct, options: accts, hint: "←→ to change"},
	}, submit: func(v []string) error {
		if v[4] == "any" {
			v[4] = ""
		}
		t.filter = history.Filter{Author: strings.TrimSpace(v[0]), Text: strings.TrimSpace(v[1]),
			Since: strings.TrimSpace(v[2]), Until: strings.TrimSpace(v[3]), Account: v[4]}
		t.loadHistory()
		return nil
	}}
}

// cherryPick copies the marked commits (or the selected one) onto the
// current branch, after asking.
func (t *tui) cherryPick() {
	var hashes []string
	for h := range t.marked { // the use case puts them in order
		hashes = append(hashes, h)
	}
	if len(hashes) == 0 {
		if h := t.selectedCommit(); h != "" {
			hashes = []string{h}
		}
	}
	if len(hashes) == 0 {
		t.say(fmt.Errorf("select a commit line, or mark several with Space"), "")
		return
	}
	branch := "the current branch"
	if t.head != nil && t.head.Branch != "" {
		branch = t.head.Branch
	}
	gd := t.histDir
	t.confirm = &confirmBox{fmt.Sprintf("Cherry-pick %d commit(s) onto %s, oldest first? If one does not apply cleanly, nothing changes. (y/n)", len(hashes), branch),
		map[string]func() error{"y": func() error {
			ts, err := t.app.History.CherryPick(gd, hashes)
			if err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, fmt.Sprintf("Cherry-picked %d commit(s) onto %s.", len(ts), branch)+undoHint)
			return nil
		}}}
}

// pickBranch lists the branches and switches to the chosen one.
func (t *tui) pickBranch() {
	if t.histDir == "" {
		return
	}
	bs, err := t.app.Refs.Branches(t.histDir)
	if err != nil || len(bs) == 0 {
		t.say(err, "This repo has no branches yet.")
		return
	}
	var opts []string
	for _, b := range bs {
		o := b.Name
		if b.Current {
			o += "  (current)"
		}
		opts = append(opts, o)
	}
	gd := t.histDir
	t.picker = &picker{title: "Switch to branch", options: opts, pick: func(i int) error { return t.switchTo(gd, bs[i]) }}
}

// switchTo switches to a branch. With uncommitted changes it smart
// switches or parks them as set for the repo, or asks what to do.
func (t *tui) switchTo(gd string, b refs.Branch) error {
	name := b.Name
	if h, err := t.app.Refs.Head(gd); err == nil && h.Changes.Any() && h.Busy == "" && !b.Current {
		switch st := t.app.Refs.SwitchSetting(gd); st.Mode {
		case refs.Smart:
			return t.smartSwitch(gd, name)
		case refs.Park:
			return t.park(gd, name)
		}
		// Uncommitted changes: say what happens to them first. Shift
		// remembers the choice for this repo.
		remember := func(mode string, then func() error) func() error {
			return func() error {
				if err := t.app.Refs.SetSwitchSetting(gd, mode); err != nil {
					return err
				}
				return then()
			}
		}
		smart, park := func() error { return t.smartSwitch(gd, name) }, func() error { return t.park(gd, name) }
		t.confirm = &confirmBox{fmt.Sprintf("You have uncommitted changes on %s (%s).  s = smart switch: take them with you to %s (if some clash, you fix them there or go back) · p = park them on %s (git stash), restored when you come back · S or P = the same, and remember it for this repo · n or Esc = cancel",
			h.Branch, strings.TrimPrefix(h.Changes.Describe(), "uncommitted: "), name, h.Branch), map[string]func() error{
			"s": smart, "p": park, "S": remember("smart", smart), "P": remember("park", park),
		}}
		return nil
	}
	name, err := t.app.Refs.Switch(gd, name)
	if err != nil {
		return err
	}
	t.loadHistory()
	t.say(nil, "Switched to "+name+"."+undoHint)
	w, _ := t.app.Refs.Waiting(gd)
	t.offerParked(gd, w)
	return nil
}

// manageBranches lists the local branches; the chosen one can be switched
// to, renamed, set to pull from a remote branch, or deleted.
func (t *tui) manageBranches() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	all, err := t.app.Refs.Branches(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	var bs []refs.Branch
	var opts []string
	for _, b := range all {
		if b.Remote {
			continue
		}
		o := b.Name
		switch {
		case b.Gone:
			o += "  → " + b.Upstream + " (gone)"
		case b.Upstream != "":
			o += "  → " + b.Upstream
		}
		if b.Current {
			o += "  (current)"
		}
		bs, opts = append(bs, b), append(opts, o)
	}
	if len(bs) == 0 {
		t.say(nil, "This repo has no branches yet.")
		return
	}
	t.picker = &picker{title: "Branches: choose one to switch, rename, set its upstream or delete", options: opts, pick: func(i int) error {
		b := bs[i]
		pulls := "pulls from no remote branch"
		if b.Upstream != "" {
			pulls = "pulls from " + b.Upstream
		}
		acts := map[string]func() error{
			"r": func() error { t.renameBranch(gd, b.Name); return nil },
			"u": func() error { t.setUpstream(gd, b.Name, all); return nil },
			"d": func() error { t.deleteBranch(gd, b.Name); return nil },
		}
		q := "r = rename · u = set the remote branch it pulls from · d = delete · n or Esc = cancel"
		if !b.Current {
			acts["s"] = func() error { return t.switchTo(gd, b) }
			acts["m"] = func() error { return t.mergePlan(gd, b.Name) }
			q = "s = switch to it · m = merge it into the current branch · " + q
		}
		t.confirm = &confirmBox{fmt.Sprintf("Branch %s (%s).  %s", b.Name, pulls, q), acts}
		return nil
	}}
}

// renameBranch asks for a branch's new name, its old one filled in.
func (t *tui) renameBranch(gd, from string) {
	t.form = &form{title: "Rename the branch " + from, fields: []field{{label: "New name", value: from, hint: "it keeps pulling from the same remote branch"}},
		submit: func(v []string) error {
			r, err := t.app.Refs.RenameBranch(gd, from, v[0])
			if err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, "Renamed the branch "+r.From+" to "+r.To+"."+undoHint)
			return nil
		}}
}

// setUpstream lets you choose the remote branch a branch pulls from, or
// none.
func (t *tui) setUpstream(gd, branch string, all []refs.Branch) {
	none := "(none: pull from no remote branch)"
	opts := []string{none}
	for _, b := range all {
		if b.Remote {
			opts = append(opts, b.Name)
		}
	}
	if len(opts) == 1 {
		t.say(fmt.Errorf("this clone has no remote branches; fetch first (F)"), "")
		return
	}
	t.picker = &picker{title: branch + " pulls from", options: opts, pick: func(i int) error {
		up := opts[i]
		if i == 0 {
			up = ""
		}
		u, err := t.app.Refs.SetUpstream(gd, branch, up)
		if err != nil {
			return err
		}
		t.loadHistory()
		if u.Upstream == "" {
			t.say(nil, u.Branch+" now pulls from no remote branch.")
		} else {
			t.say(nil, u.Branch+" now pulls from "+u.Upstream+".")
		}
		return nil
	}}
}

// deleteBranch asks, then deletes a branch. When some of its commits are
// on no other branch or remote it asks again before deleting them too.
func (t *tui) deleteBranch(gd, name string) {
	del := func(force bool) error {
		d, err := t.app.Refs.DeleteBranch(gd, name, force)
		var u *refs.Unmerged
		if errors.As(err, &u) {
			t.confirm = &confirmBox{fmt.Sprintf("%s has %d commit(s) on no other branch or remote: once it is deleted, only Undo (U) brings them back. Delete it anyway? (y/n)", u.Branch, u.Commits), map[string]func() error{"y": func() error {
				d, err := t.app.Refs.DeleteBranch(gd, name, true)
				if err != nil {
					return err
				}
				t.deletedBranch(d)
				return nil
			}}}
			return nil
		}
		if err != nil {
			return err
		}
		t.deletedBranch(d)
		return nil
	}
	t.confirm = &confirmBox{"Delete the branch " + name + "? (y/n)", map[string]func() error{"y": func() error { return del(false) }}}
}

func (t *tui) deletedBranch(d refs.DeletedBranch) {
	t.loadHistory()
	t.say(nil, "Deleted the branch "+d.Branch+"."+undoHint)
}

// newTag asks for a tag's name and, for an annotated tag, a message, then
// tags the selected commit.
func (t *tui) newTag() {
	if t.histDir == "" {
		return
	}
	hash := t.selectedCommit()
	if hash == "" {
		t.say(fmt.Errorf("select a commit line first (↑↓)"), "")
		return
	}
	gd, short := t.histDir, hash[:min(7, len(hash))]
	t.form = &form{title: "Tag the commit " + short, fields: []field{
		{label: "Name", hint: "e.g. v1.2.0"},
		{label: "Message", hint: "empty: a lightweight tag; with a message: an annotated one"},
	}, submit: func(v []string) error {
		r, err := t.app.Refs.NewTag(gd, v[0], hash, v[1])
		if err != nil {
			return err
		}
		t.loadHistory()
		kind := "lightweight"
		if r.Annotated {
			kind = "annotated"
		}
		t.say(nil, fmt.Sprintf("Tagged %s as %s (%s). T lists the tags, to push or delete it.", short, r.Tag, kind))
		return nil
	}}
}

// tags lists the repo's tags; the chosen one can be pushed or deleted.
func (t *tui) tags() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	ts, err := t.app.Refs.Tags(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	if len(ts) == 0 {
		t.say(nil, "This repo has no tags yet. t tags the selected commit.")
		return
	}
	t.picker = &picker{title: "Tags: choose one to push or delete", options: ts, pick: func(i int) error {
		name := ts[i]
		t.confirm = &confirmBox{"Tag " + name + ".  p = push it to the remote · d = delete it · n or Esc = cancel", map[string]func() error{
			"p": func() error { return t.pushTag(gd, name) },
			"d": func() error {
				t.confirm = &confirmBox{"Delete the tag " + name + " in this clone? A copy already pushed stays on the remote. (y/n)", map[string]func() error{"y": func() error {
					if err := t.app.Refs.DeleteTag(gd, name); err != nil {
						return err
					}
					t.loadHistory()
					t.say(nil, "Deleted the tag "+name+".")
					return nil
				}}}
				return nil
			},
		}}
		return nil
	}}
}

// pushTag sends a tag to the remote, leaving the TUI while git runs.
func (t *tui) pushTag(gd, name string) error {
	var p refs.TagPushed
	var err error
	t.suspend(func() {
		fmt.Println("Pushing the tag " + name + " ...")
		p, err = t.app.Refs.PushTag(gd, name, func(l string) { fmt.Print("\r\x1b[K  " + l) })
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	if err != nil {
		return err
	}
	as := ""
	if p.Account != "" {
		as = " as " + p.Account
	}
	t.say(nil, "Pushed the tag "+p.Tag+" to "+p.Remote+as+".")
	return nil
}

// newBranch asks for a name, the prefix setting already typed, then starts
// the branch at from (a commit; "" for the current one) and switches to it.
func (t *tui) newBranch(from string) {
	if t.histDir == "" {
		return
	}
	gd, where := t.histDir, "here"
	if from != "" {
		where = "at " + from[:min(7, len(from))]
	}
	prefix := t.app.Refs.BranchPrefix()
	t.form = &form{title: "New branch " + where, fields: []field{{label: "Name", value: prefix, hint: "uncommitted changes come along"}},
		submit: func(v []string) error {
			name := strings.TrimSpace(v[0])
			if name == "" || name == prefix {
				return fmt.Errorf("type a name for the branch")
			}
			r, err := t.app.Refs.NewBranch(gd, name, from)
			if err != nil {
				return err
			}
			t.loadHistory()
			with := ""
			if r.Carried {
				with = ", with your changes"
			}
			t.say(nil, "Started branch "+r.Branch+with+"."+undoHint)
			return nil
		}}
}

// compare shows the commits the branch would push and pull.
// branchPR shows the open pull request of the current branch, read with
// the repo's account.
func (t *tui) branchPR() {
	if t.histDir == "" {
		return
	}
	b := t.app.Hosting.BranchPR(t.histDir)
	var lines []string
	switch {
	case b.Found:
		lines = append(lines, b.Summary, "", history.CleanText(b.PR.Title), b.PR.URL, "")
		for _, c := range b.PR.Chips {
			if c.Hint != "" {
				lines = append(lines, c.Text+": "+c.Hint)
			}
		}
	case b.Reason != "":
		lines = append(lines, b.Reason)
	case b.Account == "":
		lines = append(lines, "Nothing to read: this branch has no GitHub or GitLab remote with a signed-in account.")
	default:
		lines = append(lines, b.Branch+" has no open "+b.Kind.RequestName()+" in "+b.Repo+".")
	}
	t.view = &viewer{title: "Pull or merge request of " + b.Branch, lines: lines}
}

func (t *tui) compare() {
	if t.histDir == "" {
		return
	}
	c, err := t.app.Refs.Compare(t.histDir)
	if err != nil {
		t.say(err, "")
		return
	}
	var lines []string
	list := func(what string, ls []refs.Line, more int) {
		if len(ls) == 0 {
			return
		}
		lines = append(lines, fmt.Sprintf("%s (%d):", what, len(ls)+more))
		for _, l := range ls {
			lines = append(lines, fmt.Sprintf("  %s  %s  (%s, %s)", l.Short, history.CleanText(l.Subject), history.CleanText(l.Author), refs.Ago(time.Now().Unix(), l.Time)))
		}
		if more > 0 {
			lines = append(lines, fmt.Sprintf("  … and %d older", more))
		}
		lines = append(lines, "")
	}
	list("To push to "+c.Upstream, c.ToPush, c.MorePush)
	list("To pull from "+c.Upstream, c.ToPull, c.MorePull)
	switch {
	case len(c.ToPush) == 0 && len(c.ToPull) == 0:
		lines = append(lines, c.Branch+" is up to date with "+c.Upstream+".")
	case len(c.ToPush) > 0 && len(c.ToPull) > 0:
		lines = append(lines, "Both have new commits: u (update) only fast-forwards, so combine them first (git pull --rebase), then push.")
	case len(c.ToPull) > 0:
		lines = append(lines, "Press u to get them.")
	default:
		lines = append(lines, "Press p to push them.")
	}
	t.view = &viewer{title: c.Branch + " compared with " + c.Upstream, lines: lines}
}

// commit asks for a message, then commits the staged files, or every
// change when none is staged. ↑ to its first field amends the last commit
// instead, its message filled in; when that is not allowed, the field says
// why.
func (t *tui) commit() {
	if t.histDir == "" || t.head == nil {
		return
	}
	if h, err := t.app.Refs.Head(t.histDir); err == nil { // staged since History was read
		t.head = &h
	}
	gd, c := t.histDir, t.head.Changes
	all := c.Staged == 0
	title := fmt.Sprintf("Commit the %d staged file(s)", c.Staged)
	if all {
		title = fmt.Sprintf("Commit every change (%d changed, %d new)", c.Changed, c.New)
	}
	// The first field chooses a new commit or amending the last one; the
	// cursor starts on the message, so typing and Enter commit as before.
	am := t.app.Workspace.Amendable(gd)
	last := am.Short + " “" + history.CleanText(am.Subject) + "”"
	kinds := []string{"a new commit"}
	titles := []string{title}
	kindHint := "←→ a new commit, or amend the last one"
	if am.Can {
		if c.Staged > 0 {
			kinds = append(kinds, "amend "+am.Short+" with the staged files")
			titles = append(titles, fmt.Sprintf("Amend %s with the %d staged file(s)", last, c.Staged))
		} else {
			kinds = append(kinds, "amend "+am.Short+": only its message")
			titles = append(titles, "Reword "+last)
			if c.Any() {
				kinds = append(kinds, "amend "+am.Short+" with every change")
				titles = append(titles, fmt.Sprintf("Amend %s with every change (%d changed, %d new)", last, c.Changed, c.New))
			}
		}
	} else {
		kindHint = "can't amend the last commit: " + am.Why
	}
	t.form = &form{title: title, focus: 1, fields: []field{
		{label: "Commit", value: kinds[0], options: kinds, hint: kindHint},
		{label: "Message", hint: "what the change does · ↑ to amend the last commit instead"},
	}, changed: func(f *form) {
		i := indexOf(kinds, f.fields[0].value)
		f.title = titles[i]
		msg := &f.fields[1].value
		switch {
		case i > 0 && strings.TrimSpace(*msg) == "":
			*msg = am.Message // start from the old message
		case i == 0 && *msg == am.Message:
			*msg = ""
		}
	}, submit: func(v []string) error {
		i := indexOf(kinds, v[0])
		if i == 0 {
			r, err := t.app.Workspace.Commit(gd, v[1], all)
			if err != nil {
				return err
			}
			t.committed(r, "Committed "+r.Short)
			return nil
		}
		msg := v[1]
		if workspace.Message(msg) == workspace.Message(am.Message) {
			msg = "" // unchanged: keep the message as it is
		}
		r, err := t.app.Workspace.Amend(gd, msg, i == 2)
		if err != nil {
			return err
		}
		t.committed(r, "Amended "+r.Amended+": now "+r.Short)
		return nil
	}}
}

// committed reads History (and the changes, while open) again after a
// commit or an amend, and says what was made.
func (t *tui) committed(r workspace.Committed, what string) {
	t.loadHistory()
	if t.changes != nil { // back to History when nothing is left
		if t.loadChanges(); t.changes != nil && len(t.changes.st.Files) == 0 {
			t.changes = nil
		}
	}
	as := ""
	if r.Account != "" {
		as = " as " + r.Account
	}
	t.say(nil, what+" "+history.CleanText(r.Subject)+as+"."+undoHint)
}

// ---------------- uncommitted changes ----------------

// changesView is the History page's list of uncommitted changes: the
// files in conflict, staged and not staged, one row per file and section
// (a file changed again after staging is in both).
type changesView struct {
	gd   string
	st   workspace.Status
	rows []changeRow
	sel  int
}

// changeRow is a file in one section: "conflict", "staged" or "changed".
type changeRow struct {
	f   workspace.File
	sec string
}

// openChanges shows the selected repo's uncommitted changes.
func (t *tui) openChanges() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	t.changes = &changesView{gd: t.histDir}
	t.msg = ""
	t.loadChanges()
}

// loadChanges reads the changes again, keeping the selection on the same
// file and section when it is still there, and the branch line up to date.
func (t *tui) loadChanges() {
	c := t.changes
	if c == nil {
		return
	}
	var was changeRow
	if c.sel < len(c.rows) {
		was = c.rows[c.sel]
	}
	st, err := t.app.Workspace.Status(c.gd)
	if err != nil {
		t.changes = nil
		t.say(err, "")
		return
	}
	if h, err := t.app.Refs.Head(c.gd); err == nil {
		t.head = &h
	}
	c.st, c.rows = st, nil
	for _, s := range []struct {
		sec   string
		files []workspace.File
	}{{"conflict", st.Conflicts()}, {"staged", st.Staged()}, {"changed", st.Changed()}} {
		for _, f := range s.files {
			c.rows = append(c.rows, changeRow{f, s.sec})
		}
	}
	for i, r := range c.rows {
		if r.f.Path == was.f.Path && r.sec == was.sec {
			c.sel = i
			return
		}
	}
	c.sel = max(0, min(c.sel, len(c.rows)-1))
}

// closeChanges goes back to the commits, read again (a commit or a
// discard may have changed them).
func (t *tui) closeChanges() {
	t.changes = nil
	sel := t.sel[tabHistory]
	t.readHistory()
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

// changeLetter is the one-letter mark of a row's change, as on the
// desktop: M A D R C T, N new, ! conflict; and its colour.
func changeLetter(r changeRow) (string, string) {
	l := r.f.Worktree
	switch {
	case r.sec == "conflict":
		return "!", cRed
	case r.f.New():
		return "N", cGreen
	case r.sec == "staged":
		l = r.f.Staged
	}
	switch l {
	case "A":
		return "A", cGreen
	case "D":
		return "D", cRed
	case "R", "C", "T":
		return l, cYellow
	}
	return "M", cYellow
}

// keyChanges handles a key while the changes are shown. Keys it does not
// use (switching page, help, quit with Ctrl+C) go to the main handler.
func (t *tui) keyChanges(k string) bool {
	c := t.changes
	n := len(c.rows)
	var sel *changeRow
	if c.sel < n {
		sel = &c.rows[c.sel]
	}
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		t.closeChanges()
	case "up", "k":
		c.sel = max(0, c.sel-1)
	case "down", "j":
		c.sel = max(0, min(n-1, c.sel+1))
	case "pgup":
		c.sel = max(0, c.sel-max(1, t.h-6))
	case "pgdn":
		c.sel = max(0, min(n-1, c.sel+max(1, t.h-6)))
	case "home", "g":
		c.sel = 0
	case "end", "G":
		c.sel = max(0, n-1)
	case "r":
		t.loadChanges()
	case "enter", "d":
		if sel != nil {
			t.showChangeDiff(*sel)
		}
	case "s":
		if sel != nil {
			t.changeAction("Staged", func() ([]string, error) { return t.app.Workspace.Stage(c.gd, []string{sel.f.Path}, false) })
		}
	case "u":
		if sel != nil {
			t.changeAction("Unstaged", func() ([]string, error) { return t.app.Workspace.Unstage(c.gd, []string{sel.f.Path}, false) })
		}
	case "a":
		t.changeAction("Staged", func() ([]string, error) { return t.app.Workspace.Stage(c.gd, nil, true) })
	case "A":
		t.changeAction("Unstaged", func() ([]string, error) { return t.app.Workspace.Unstage(c.gd, nil, true) })
	case "x":
		if sel != nil {
			t.discard(sel.f, false)
		}
	case "X":
		t.discard(workspace.File{}, true)
	case "i":
		if sel != nil {
			t.ignore(sel.f.Path)
		}
	case "C":
		t.commit()
	case "H":
		t.openHunks()
	}
	return true
}

// changeAction reports a stage or unstage and reads the changes again.
func (t *tui) changeAction(done string, run func() ([]string, error)) {
	if paths, err := run(); err == nil {
		t.say(nil, done+" "+changedFiles(paths)+".")
	} else {
		t.say(err, "")
	}
	t.loadChanges()
}

// changedFiles names one file, or counts several.
func changedFiles(paths []string) string {
	if len(paths) == 1 {
		return history.CleanText(paths[0])
	}
	return fmt.Sprintf("%d files", len(paths))
}

// discard asks, then puts a file (or with all every file) back as in the
// last commit. The changes are kept in git stash.
func (t *tui) discard(f workspace.File, all bool) {
	c := t.changes
	if !all && f.Conflict {
		t.say(fmt.Errorf("%s has a conflict: fix it in your editor first", f.Path), "")
		return
	}
	what := "the changes in " + history.CleanText(f.Path)
	switch {
	case all:
		what = fmt.Sprintf("every uncommitted change (%d file(s), staged or not; new files are removed)", len(c.st.Files))
	case f.New():
		what = "the new file " + history.CleanText(f.Path) + " (it is removed)"
	case f.IsStaged():
		what += " (staged and not staged)"
	}
	stash := workspace.DiscardMessage(c.st.Branch)
	t.confirm = &confirmBox{fmt.Sprintf("Discard %s? The file goes back to the last commit. Nothing is lost: the changes are kept in git stash as \"%s\". (y/n)", what, stash),
		map[string]func() error{"y": func() error {
			var paths []string
			if !all {
				paths = []string{f.Path}
			}
			r, err := t.app.Workspace.Discard(c.gd, paths, all)
			t.loadChanges()
			if err != nil {
				return err
			}
			t.say(nil, fmt.Sprintf("Discarded %s; kept in git stash as \"%s\".", changedFiles(r.Files), r.Stash))
			return nil
		}}}
}

// ignore asks what to ignore for a file (the file, its extension or its
// folder) and where (.gitignore, or only for me), then adds the line. When
// git still tracks the file, it asks whether to untrack it.
func (t *tui) ignore(path string) {
	c := t.changes
	isDir := strings.HasSuffix(path, "/")
	var kinds []workspace.IgnoreKind
	var opts []string
	for _, k := range []workspace.IgnoreKind{workspace.IgnoreFile, workspace.IgnoreExt, workspace.IgnoreFolder} {
		pat, err := workspace.IgnorePattern(path, k, isDir)
		if err != nil {
			continue // no extension, or not in a folder
		}
		label := map[workspace.IgnoreKind]string{workspace.IgnoreFile: "this file", workspace.IgnoreExt: "every file like it", workspace.IgnoreFolder: "its folder"}[k]
		if isDir && k == workspace.IgnoreFile {
			label = "this folder"
		}
		kinds, opts = append(kinds, k), append(opts, label+": "+history.CleanText(pat))
	}
	if len(kinds) == 0 {
		t.say(fmt.Errorf("%s can't be ignored here; use gitswitch ignore", history.CleanText(path)), "")
		return
	}
	shared, mine := ".gitignore (shared with everyone through commits)", "only for me (.git/info/exclude)"
	t.form = &form{title: "Ignore " + history.CleanText(path), fields: []field{
		{label: "Ignore", value: opts[0], options: opts, hint: "←→ the file, its extension or its folder"},
		{label: "Where", value: shared, options: []string{shared, mine}, hint: "←→ shared, or only for this clone"},
	}, submit: func(v []string) error {
		kind, local := kinds[indexOf(opts, v[0])], v[1] == mine
		r, err := t.app.Workspace.Ignore(c.gd, path, kind, local, false)
		t.loadChanges()
		if err != nil {
			return err
		}
		did := "Added " + history.CleanText(r.Pattern) + " to " + r.Where + "."
		if !r.Added {
			did = r.Where + " already had " + history.CleanText(r.Pattern) + "."
		}
		if len(r.Tracked) == 0 {
			t.say(nil, did)
			return nil
		}
		what := fmt.Sprintf("%d file(s) it covers are", len(r.Tracked))
		if len(r.Tracked) == 1 {
			what = history.CleanText(r.Tracked[0]) + " is"
		}
		t.confirm = &confirmBox{did + " But " + what + " tracked by git, so git keeps following them. Untrack (git rm --cached; the files stay in your folder, and the next commit removes them from the repo)? (y/n)",
			map[string]func() error{"y": func() error {
				r, err := t.app.Workspace.Ignore(c.gd, path, kind, local, true)
				t.loadChanges()
				if err != nil {
					return err
				}
				t.say(nil, fmt.Sprintf("Ignored %s in %s; untracked %s (kept in your folder).", history.CleanText(r.Pattern), r.Where, changedFiles(r.Untracked)))
				return nil
			}}}
		return nil
	}}
}

// showChangeDiff opens a row's diff: the staged change under Staged, the
// one in the working folder otherwise (a new file in full).
func (t *tui) showChangeDiff(r changeRow) {
	staged := r.sec == "staged"
	d, err := t.app.Workspace.Diff(t.changes.gd, r.f.Path, staged)
	if err != nil {
		t.say(err, "")
		return
	}
	which := "not staged"
	if staged {
		which = "staged"
	}
	if r.sec == "conflict" {
		which = "conflict"
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(d.Text, "\n"), "\n") {
		l = history.CleanText(l)
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			l = cBold + l + cReset
		case strings.HasPrefix(l, "+"):
			l = cGreen + l + cReset
		case strings.HasPrefix(l, "-"):
			l = cRed + l + cReset
		case strings.HasPrefix(l, "@@"):
			l = cYellow + l + cReset
		case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			l = cDim + l + cReset
		}
		lines = append(lines, l)
	}
	if d.Text == "" {
		lines = []string{"No " + which + " change in this file."}
	}
	if d.Truncated {
		lines = append(lines, "", cDim+fmt.Sprintf("… cut at %d KB; git diff shows all of it", workspace.MaxDiffBytes>>10)+cReset)
	}
	t.view = &viewer{title: history.CleanText(r.f.Path) + " · " + which, lines: lines}
}

// drawChanges lists the uncommitted changes in sections, under the repo,
// its account and branch.
func (t *tui) drawChanges() ([]string, int) {
	c := t.changes
	acct := cDim + "not set" + cReset
	path := c.gd
	if t.hist != nil {
		path = t.hist.Repo.Path
		if a := t.hist.Repo.Account; a != "" {
			acct = t.color(a) + a + cReset
		}
	}
	branch := c.st.Branch
	if branch == "" {
		branch = "(detached)"
	}
	l := []string{
		"\x00 " + cBold + "Uncommitted changes" + cReset + "  " + path + "  account: " + acct + "  branch: " + cBold + history.CleanText(branch) + cReset,
	}
	if c.st.Busy != "" {
		l = append(l, "\x00 "+cRed+"git is in the middle of a "+c.st.Busy+": Esc, then e to continue or abort it."+cReset)
	}
	l = append(l, "\x00")
	if len(c.rows) == 0 {
		return append(l, " No uncommitted changes. Esc goes back to the commits."), -1
	}
	nStaged := len(c.st.Staged())
	hdr, selLine, last := len(l), -1, ""
	for i, r := range c.rows {
		if r.sec != last {
			last = r.sec
			if i > 0 {
				l = append(l, "")
			}
			title, hint := "", ""
			switch r.sec {
			case "conflict":
				title, hint = "Conflicts", "fix them in your editor, then stage them (s)"
			case "staged":
				title, hint = "Staged", "in the next commit: C commits them · u takes one out"
			default:
				title, hint = "Unstaged", "s stages one · a stages all · x discards"
				if nStaged == 0 {
					title, hint = "Changes", "stage the files to commit (s, a), or press C to commit them all"
				}
			}
			n := 0
			for _, x := range c.rows {
				if x.sec == r.sec {
					n++
				}
			}
			l = append(l, " "+cBold+title+cReset+cDim+fmt.Sprintf(" (%d)  %s", n, hint)+cReset)
		}
		on := i == c.sel
		if on {
			selLine = len(l) - hdr
		}
		letter, col := changeLetter(r)
		desc := r.f.Describe()
		if r.f.Orig != "" && !strings.Contains(desc, r.f.Orig) {
			desc += " (from " + r.f.Orig + ")"
		}
		l = append(l, fmt.Sprintf("%s%s%s%s %s%s%s  %s", mark(on), col, letter, cReset, boldIf(on), history.CleanText(r.f.Path), cReset, cDim+history.CleanText(desc)+cReset))
	}
	return l, selLine
}

// pruneBranches lists the branches merged into the default branch and
// deletes them when confirmed.
func (t *tui) pruneBranches() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	m, err := t.app.Refs.Merged(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	kept := ""
	if len(m.Kept) > 0 {
		var names []string
		for _, b := range m.Kept {
			names = append(names, b.Name)
		}
		kept = " Kept, not on " + m.Into + " (squash-merged?): " + strings.Join(names, ", ") + "."
	}
	if len(m.Branches) == 0 {
		t.say(nil, "No branch is merged into "+m.Into+"."+kept)
		return
	}
	var names []string
	for _, b := range m.Branches {
		names = append(names, b.Name)
	}
	q := fmt.Sprintf("Delete %d branch(es) merged into %s? %s.%s (y/n)", len(names), m.Into, strings.Join(names, ", "), kept)
	t.confirm = &confirmBox{q, map[string]func() error{"y": func() error {
		r, err := t.app.Refs.Prune(gd)
		if err != nil {
			return err
		}
		t.loadHistory()
		t.say(nil, fmt.Sprintf("Deleted %d merged branch(es).%s", len(r.Branches), undoHint))
		return nil
	}}}
}

// moveToNewBranch asks for a name, the prefix setting already typed, then
// moves the commits not pushed from the current branch to a new branch.
func (t *tui) moveToNewBranch() {
	if t.histDir == "" || t.head == nil {
		return
	}
	h := *t.head
	if h.Branch == "" || h.Upstream == "" || h.Ahead == 0 {
		t.say(fmt.Errorf("M moves the commits not pushed yet to a new branch, and %s has none", map[bool]string{true: "this detached HEAD", false: h.Branch}[h.Branch == ""]), "")
		return
	}
	gd, prefix := t.histDir, t.app.Refs.BranchPrefix()
	t.form = &form{title: fmt.Sprintf("Move %d commit(s) from %s to a new branch", h.Ahead, h.Branch),
		fields: []field{{label: "Name", value: prefix, hint: h.Branch + " goes back to " + h.Upstream}},
		submit: func(v []string) error {
			name := strings.TrimSpace(v[0])
			if name == "" || name == prefix {
				return fmt.Errorf("type a name for the branch")
			}
			r, err := t.app.Refs.MoveToNewBranch(gd, name)
			if err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, fmt.Sprintf("Moved %d commit(s) from %s to %s.%s", r.Commits, r.From, r.Branch, undoHint))
			return nil
		}}
}

// park switches leaving the uncommitted changes parked on their branch.
func (t *tui) park(gd, name string) error {
	r, err := t.app.Refs.Park(gd, name)
	if err != nil {
		return err
	}
	t.loadHistory()
	t.say(nil, "Parked your changes and switched to "+r.Branch+". They come back when you switch back (P lists them)."+undoHint)
	t.offerParked(gd, r.Waiting)
	return nil
}

// offerParked follows a switch to a branch with parked changes: it offers
// to restore the newest, or look at them first.
func (t *tui) offerParked(gd string, w []refs.Parked) {
	if len(w) == 0 {
		return
	}
	p := w[0]
	more := ""
	if len(w) > 1 {
		more = fmt.Sprintf(" (%d sets are parked here; P lists them)", len(w))
	}
	t.confirm = &confirmBox{fmt.Sprintf("You parked changes on %s %s: %s%s.  r = restore them · l = look first · n or Esc = not now",
		p.Branch, refs.Ago(time.Now().Unix(), p.Time), refs.FileList(p.Files), more), t.parkedActions(gd, p)}
}

// parkedActions are the keys for one set of parked changes: r restore,
// l look first, x delete (asks again).
func (t *tui) parkedActions(gd string, p refs.Parked) map[string]func() error {
	return map[string]func() error{
		"r": func() error {
			if err := t.app.Refs.RestoreParked(gd, p.Stash); err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, "Restored the changes parked on "+p.Branch+".")
			return nil
		},
		"l": func() error {
			out, err := t.app.Refs.ParkedPatch(gd, p.Stash)
			if err != nil {
				return err
			}
			t.view = &viewer{title: "Parked on " + p.Describe(time.Now().Unix()) + " · P to restore", lines: strings.Split(out, "\n")}
			return nil
		},
		"x": func() error {
			t.confirm = &confirmBox{fmt.Sprintf("Delete the changes parked on %s? This can't be undone. (y/n)", p.Describe(time.Now().Unix())),
				map[string]func() error{"y": func() error {
					if err := t.app.Refs.DropParked(gd, p.Stash); err != nil {
						return err
					}
					t.loadHistory()
					t.say(nil, "Deleted the changes parked on "+p.Branch+".")
					return nil
				}}}
			return nil
		},
	}
}

// pickParked lists the parked changes, newest first, then asks what to do
// with the chosen ones.
func (t *tui) pickParked() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	all, err := t.app.Refs.Parked(gd)
	if err != nil || len(all) == 0 {
		t.say(err, "Nothing is parked in this repo.")
		return
	}
	h, _ := t.app.Refs.Head(gd)
	now := time.Now().Unix()
	var opts []string
	for _, p := range all {
		o := p.Describe(now)
		if p.Branch == h.Branch {
			o += "  (this branch)"
		}
		opts = append(opts, o)
	}
	t.picker = &picker{title: "Parked changes (git stash)", options: opts, pick: func(i int) error {
		p := all[i]
		where := "here"
		if p.Branch != h.Branch {
			where = "here, on " + h.Branch + " (not " + p.Branch + ")"
		}
		t.confirm = &confirmBox{fmt.Sprintf("Parked on %s.  r = restore them %s · l = look first · x = delete · n or Esc = cancel", p.Describe(now), where), t.parkedActions(gd, p)}
		return nil
	}}
}

// smartSwitch switches taking the uncommitted changes along.
func (t *tui) smartSwitch(gd, name string) error {
	r, err := t.app.Refs.SmartSwitch(gd, name)
	if err != nil {
		return err
	}
	t.loadHistory()
	if r.Clash != nil {
		t.say(nil, fmt.Sprintf("Switched to %s, but %d file(s) clash: %s. Fix them in your editor, then press R.", r.Branch, len(r.Clash.Files), strings.Join(r.Clash.Files, ", ")))
		return nil
	}
	if r.Carried {
		t.say(nil, "Switched to "+r.Branch+", with your changes."+undoHint)
	} else {
		t.say(nil, "Switched to "+r.Branch+"."+undoHint)
	}
	t.offerParked(gd, r.Waiting)
	return nil
}

// resolve offers the ways out of a smart switch that clashed.
func (t *tui) resolve() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	c, err := t.app.Refs.Clash(gd)
	if err != nil || c == nil {
		t.say(err, "Nothing to resolve: no smart switch is waiting in this repo.")
		return
	}
	t.confirm = &confirmBox{fmt.Sprintf("Smart switch to %s: %d file(s) clash: %s. Keep the lines you want between the <<<<<<< ======= >>>>>>> markers and delete the markers, in your editor or IDE.  d = I've fixed them · a = go back to %s exactly as before · p = park them on %s and stay on %s without them (a and p throw away fixes made since) · Esc = later",
		c.To, len(c.Files), strings.Join(c.Files, ", "), c.From, c.From, c.To), map[string]func() error{
		"d": func() error {
			if err := t.app.Refs.ResolveDone(gd); err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, "Done: your changes are on "+c.To+", uncommitted.")
			return nil
		},
		"a": func() error {
			if err := t.app.Refs.ResolveAbort(gd); err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, "Back on "+c.From+" with your changes as they were.")
			return nil
		},
		"p": func() error {
			r, err := t.app.Refs.ResolvePark(gd)
			if err != nil {
				return err
			}
			t.loadHistory()
			t.say(nil, "Parked your changes on "+c.From+"; you are on "+r.Branch+" without them.")
			t.offerParked(gd, r.Waiting)
			return nil
		},
	}}
}

// undoHint follows a change that can be undone.
const undoHint = "  (U to undo)"

// undoLast undoes the repo's last change, after asking.
func (t *tui) undoLast() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	st, err := t.app.Undo.Last(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	label := history.CleanText(st.Label)
	if st.Blocked != "" {
		t.say(fmt.Errorf("can't undo “%s”: %s", label, st.Blocked), "")
		return
	}
	t.confirm = &confirmBox{"Undo “" + label + "”? " + st.Effect + " (y/n)", map[string]func() error{"y": func() error {
		if _, err := t.app.Undo.Undo(gd); err != nil {
			return err
		}
		t.marked = map[string]bool{}
		t.loadHistory()
		t.say(nil, "Undone: "+label)
		return nil
	}}}
}

// update pulls the current branch. It leaves the TUI while git runs, so
// git's progress and any account question show in the terminal.
// fetch reads the remote's new commits, so "to pull" is up to date.
func (t *tui) fetch() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	var f refs.Fetched
	var err error
	t.suspend(func() {
		fmt.Println("Fetching from the remote ...")
		f, err = t.app.Refs.Fetch(gd, func(l string) { fmt.Print("\r\x1b[K  " + l) })
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	t.loadHistory()
	switch {
	case err != nil:
		t.say(err, "")
	case f.Gained > 0:
		t.say(nil, fmt.Sprintf("Fetched: %d new commit(s) on %s. Press u to get them.", f.Gained, f.Head.Upstream))
	default:
		t.say(nil, "Fetched: nothing new.")
	}
}

func (t *tui) update() { t.updateWith("") }

// updateWith updates with a mode for this once ("" for the setting). When
// both sides have new commits, it offers to rebase this once.
func (t *tui) updateWith(mode string) {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	var h refs.Head
	var err error
	t.suspend(func() {
		fmt.Println("Updating from the remote ...")
		h, err = t.app.Refs.UpdateWith(gd, mode, func(l string) { fmt.Print("\r\x1b[K  " + l) })
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	t.loadHistory()
	if err != nil && mode == "" && strings.Contains(err.Error(), "update --rebase") {
		t.confirm = &confirmBox{"This branch and its remote both have new commits. r = put your not-pushed commits on top of the new ones (rebase; nothing changes if they clash, U undoes it until you push) · Esc = leave it",
			map[string]func() error{"r": func() error { t.updateWith("rebase"); return nil }}}
		return
	}
	msg := "Updated: " + h.Describe()
	if mode == "rebase" {
		msg += " · your commits are on top (U undoes it)"
	}
	t.say(err, msg)
}

// push pushes the current branch after asking, says which account pushed,
// and offers the pull request page.
func (t *tui) push() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	h, err := t.app.Refs.Head(gd)
	if err != nil {
		t.say(err, "")
		return
	}
	to := h.Upstream
	if to == "" {
		to = "the remote, for the first time (it will track it)"
	}
	as := ""
	if t.hist != nil && t.hist.Repo.Account != "" {
		as = " as " + t.hist.Repo.Account
	}
	pushIt := func() error {
		var p refs.Pushed
		var err error
		t.suspend(func() {
			fmt.Println("Pushing ...")
			p, err = t.app.Refs.Push(gd, func(l string) { fmt.Print("\r\x1b[K  " + l) })
			fmt.Println()
			if err != nil {
				fmt.Println("gitswitch:", err)
			}
		})
		t.loadHistory()
		if err != nil {
			return err
		}
		if p.Commits == 0 && !p.First {
			t.say(nil, "Nothing to push: up to date with "+p.Upstream+".")
			return nil
		}
		msg := fmt.Sprintf("Pushed %d commit(s) to %s", p.Commits, p.Upstream)
		if p.Account != "" {
			msg += " as " + p.Account
		}
		if p.PRURL == "" {
			t.say(nil, msg+".")
			return nil
		}
		t.confirm = &confirmBox{msg + ".  o = open a pull request (" + p.PRURL + ") · n or Esc = not now", map[string]func() error{"o": func() error {
			err := process.OpenBrowser(p.PRURL)
			t.say(err, "Opened "+p.PRURL)
			return err
		}}}
		return nil
	}
	// Commits made as another account: fix them first, or push them as
	// they are.
	if ch, err := t.app.History.CheckAccount(gd); err == nil && ch.Wrong > 0 {
		t.confirm = &confirmBox{fmt.Sprintf("Push %s to %s%s? %s  f = fix them as %s first, then push · y = push them as they are · n or Esc = cancel",
			h.Branch, to, as, ch.Describe(), ch.Account), map[string]func() error{
			"f": func() error {
				if _, err := t.app.History.FixAuthor(gd); err != nil {
					return err
				}
				return pushIt()
			},
			"y": pushIt,
		}}
		return
	}
	t.confirm = &confirmBox{fmt.Sprintf("Push %s to %s%s? GitSwitch never forces a push. (y/n)", h.Branch, to, as), map[string]func() error{"y": pushIt}}
}

// commitAction reverts (v), drops (d) or rolls back to (z) the selected
// commit, after asking.
func (t *tui) commitAction(k string) {
	hash := t.selectedCommit()
	if hash == "" {
		t.say(fmt.Errorf("select a commit line first (↑↓)"), "")
		return
	}
	short := hash[:min(7, len(hash))]
	gd := t.histDir
	var q string
	var do func() (string, error)
	switch k {
	case "v":
		q = "Revert " + short + "? A new commit undoes its changes; history is not rewritten, so this is safe for pushed commits. (y/n)"
		do = func() (string, error) {
			c, err := t.app.History.Revert(gd, hash)
			return "Reverted " + c.Label() + ". Push when ready.", err
		}
	case "d":
		q = "Drop " + short + " from this branch? Its changes are removed; later commits are kept. Only not-pushed commits can be dropped. (y/n)"
		do = func() (string, error) {
			c, err := t.app.History.Drop(gd, hash)
			return "Dropped " + c.Label() + ".", err
		}
	case "z":
		q = "Roll this branch back to " + short + "? The later commits are undone and their changes stay in your files, uncommitted. Only not-pushed commits can be undone. (y/n)"
		do = func() (string, error) {
			n, c, err := t.app.History.Rollback(gd, hash)
			return fmt.Sprintf("Rolled back to %s; %d commit(s) are now uncommitted changes.", c.Label(), n), err
		}
	}
	t.confirm = &confirmBox{q, map[string]func() error{"y": func() error {
		msg, err := do()
		if err != nil {
			return err
		}
		t.loadHistory()
		t.say(nil, msg+undoHint)
		return nil
	}}}
}

// ---------------- rules ----------------

func (t *tui) keyRules(k string) {
	switch k {
	case "a":
		if len(t.cfg.Accounts) == 0 {
			t.say(fmt.Errorf("add an account first"), "")
			return
		}
		ids := t.cfg.SortedIDs()
		kinds := []string{"owner or org is", "repo is inside folder"}
		t.form = &form{title: "Add rule", fields: []field{
			{label: "When", value: kinds[0], options: kinds, hint: "←→ to change"},
			{label: "Owner, org or folder", hint: "e.g. acme-corp  or  ~/work"},
			{label: "Use account", value: ids[0], options: ids, hint: "←→ to change"},
		}, submit: func(v []string) error {
			kind := "owner"
			if v[0] == kinds[1] {
				kind = "folder"
			}
			if err := t.app.Accounts.AddRule(account.Rule{Kind: kind, Pattern: v[1], Account: v[2]}); err != nil {
				return err
			}
			t.say(nil, "Rule added.")
			return nil
		}}
	case "x":
		i := t.sel[tabRules]
		if i < len(t.cfg.Rules) {
			r := t.cfg.Rules[i]
			t.confirm = &confirmBox{"Remove rule " + r.Kind + ":" + r.Pattern + " → " + r.Account + "? (y/n)",
				map[string]func() error{"y": func() error { t.sel[tabRules] = 0; return t.app.Accounts.RemoveRule(r) }}}
		}
	}
}

// ---------------- settings ----------------

func (t *tui) keySettings(k string) {
	if k == "r" {
		t.reload()
		t.say(nil, "Checked again.")
		return
	}
	if k != "enter" && k != " " {
		return
	}
	i := t.sel[tabSettings]
	switch {
	case i-settingRows >= len(t.checks): // the list changed under the selection
		t.clampSel()
		return
	case i == 0:
		t.say(t.app.Accounts.SetSetting("guiPrompt", fmt.Sprint(!t.cfg.GUIPrompt)), "Saved.")
	case i == 1:
		t.form = &form{title: "Prompt timeout", fields: []field{{label: "Seconds to wait for an answer", value: strconv.Itoa(t.cfg.PromptTimeout)}},
			submit: func(v []string) error {
				n, err := strconv.Atoi(v[0])
				if err != nil || n < 5 {
					return fmt.Errorf("enter a number of seconds, 5 or more")
				}
				return t.app.Accounts.SetSetting("promptTimeout", v[0])
			}}
	case i == 3:
		t.form = &form{title: "New branch names", fields: []field{{label: "Start with", value: t.app.Refs.BranchPrefix(), hint: "e.g. feature/; empty for none"}},
			submit: func(v []string) error {
				err := t.app.Refs.SetBranchPrefix(v[0])
				t.say(err, "Saved.")
				return nil
			}}
	case i == 5:
		on := !t.app.Refs.FetchOnOpen()
		t.say(t.app.Refs.SetFetchOnOpen(on), map[bool]string{true: "History now fetches when a repo opens.", false: "History opens without fetching."}[on])
	case i == 4:
		next := map[refs.PullMode]string{refs.FFOnly: "rebase", refs.Rebase: "ff-only"}[t.app.Refs.PullSetting("").Global]
		t.say(t.app.Refs.SetPullSetting("", next), "Saved.")
	case i == 2:
		next := map[refs.SwitchMode]string{refs.Ask: "smart", refs.Smart: "park", refs.Park: "ask"}[t.app.Refs.SwitchSetting("").Global]
		t.say(t.app.Refs.SetSwitchSetting("", next), "Saved.")
	default:
		c := t.checks[i-settingRows]
		if c.CanFix {
			t.say(t.app.Doctor.Fix(c.ID), "Fixed: "+c.Title)
		} else if !c.OK {
			t.say(fmt.Errorf("%s", c.Detail), "")
		}
	}
	t.reload()
}

// ---------------- drawing ----------------

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cRev    = "\x1b[7m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
)

func (t *tui) color(id string) string { return "\x1b[" + history.AccountColor(t.cfg, id) + "m" }

// The smallest screen drawn. A smaller window shows the top-left part
// instead of breaking the layout.
const minW, minH = 40, 12

func (t *tui) draw() {
	w, h := terminal.TermSize()
	t.w, t.h = max(w, minW), max(h, minH)
	var out []string
	// header
	head := " " + cBold + "GitSwitch" + cReset + "  "
	for i, n := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, n)
		if i == t.tab {
			head += cRev + label + cReset
		} else {
			head += cDim + label + cReset
		}
	}
	out = append(out, head, t.banner())
	body := t.h - 4
	var lines []string
	sel := -1
	switch t.tab {
	case tabAccounts:
		lines, sel = t.drawAccounts()
	case tabRepos:
		lines, sel = t.drawRepos()
	case tabHistory:
		if t.inspecting() {
			lines, sel = t.drawInspect()
		} else if t.hv != nil {
			lines, sel = t.drawHunks()
		} else if t.conf != nil {
			lines, sel = t.drawConflict()
		} else if t.opv != nil {
			lines, sel = t.drawOperation()
		} else if t.rwEd != nil {
			lines, sel = t.drawRewrite()
		} else if t.wtView != nil {
			lines, sel = t.drawWorktrees()
		} else if t.subView != nil {
			lines, sel = t.drawSubmodules()
		} else if t.stashes != nil {
			lines, sel = t.drawStashes()
		} else if t.changes != nil {
			lines, sel = t.drawChanges()
		} else {
			lines, sel = t.drawHistory()
		}
	case tabRules:
		lines, sel = t.drawRules()
	case tabSettings:
		lines, sel = t.drawSettings()
	case tabReviews:
		lines, sel = t.drawReviews()
	}
	// keep the selection in view
	o := &t.off[t.tab]
	hdr := 0
	for hdr < len(lines) && strings.HasPrefix(lines[hdr], "\x00") {
		hdr++
	}
	view := max(1, body-hdr)
	if sel >= 0 {
		*o = scrollTo(*o, sel, view, len(lines)-hdr)
	} else {
		*o = 0
	}
	for i := 0; i < hdr; i++ {
		out = append(out, strings.TrimPrefix(lines[i], "\x00"))
	}
	rest := lines[hdr:]
	for i := *o; i < len(rest) && i < *o+view; i++ {
		out = append(out, rest[i])
	}
	for len(out) < t.h-2 {
		out = append(out, "")
	}
	// overlays replace the body
	if ov := t.overlay(); ov != nil {
		out = append(out[:2], ov...)
		for len(out) < t.h-2 {
			out = append(out, "")
		}
		out = out[:t.h-2]
	}
	// status + help
	status := ""
	if msg := history.CleanText(t.msg); msg != "" { // messages can quote git's output
		if t.msgErr {
			status = cRed + " " + msg + cReset
		} else {
			status = cGreen + " " + msg + cReset
		}
	}
	out = append(out[:min(len(out), t.h-2)], status, cDim+" "+t.footer()+cReset)
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range out {
		b.WriteString(fit(l, t.w))
		b.WriteString("\x1b[K")
		if i < len(out)-1 {
			b.WriteString("\r\n")
		}
	}
	fmt.Print(b.String())
}

func (t *tui) footer() string {
	if t.form != nil {
		return "Tab next field · ←→ change choice · Enter save · Esc cancel"
	}
	if t.picker != nil {
		return "type to filter · ↑↓ choose · Enter select · Esc cancel"
	}
	if t.confirm != nil {
		if _, ok := t.confirm.actions["D"]; ok {
			return "y remove, keep key files · D remove and delete key files · n or Esc cancel"
		}
		if _, ok := t.confirm.actions["y"]; !ok {
			return "a key the question names · n or Esc cancel"
		}
		return "y yes · n or Esc no"
	}
	if t.view != nil {
		if t.view.copy != "" && t.view.url != "" {
			return "c copy · o open link · ↑↓ scroll · Enter close"
		}
		if t.view.copy != "" {
			return "c copy · ↑↓ scroll · Enter close"
		}
		return "↑↓ PgUp PgDn scroll · Enter close"
	}
	switch t.tab {
	case tabAccounts:
		return "a add · e edit · R rename · t test · k public key · s signing · K pick key · d default · g sign in to GitHub/GitLab · x remove · ? help · q quit"
	case tabRepos:
		return "Enter account · h history · c clone · n new repo · b hosted repos · a add repo · f add folder · p pin · s sort · g group by · G group · o open · x remove · r rescan · ? help · q quit"
	case tabHistory:
		if t.inspecting() {
			return t.inspectFooter()
		}
		if t.hv != nil {
			return "↑↓ move · Space pick a line · s stage · u unstage · x discard · n/p hunk · c clear picks · r reload · Esc back · ? help"
		}
		if t.conf != nil {
			return "o ours · t theirs · b both (this clash) · n/p clash · w save · O/T whole side · d resolved · e editor · v version · Esc back"
		}
		if t.opv != nil {
			return "↑↓ move · Enter fix the file · c continue · s skip · a abort · r reload · Esc back · ? help"
		}
		if t.rwEd != nil {
			return "↑↓ move · p pick · r reword · s squash · f fixup · d drop · J/K move the commit · Enter edit message / see commit · x run · Esc cancel · ? help"
		}
		if t.wtView != nil {
			return "↑↓ move · Enter open its history · a add · x remove · p prune · l lock · u unlock · r reload · Esc back · ? help"
		}
		if t.subView != nil {
			return "↑↓ move · Enter details · i init · u update · m update to branch tip · I U M all · s sync · a add · x remove · r reload · Esc back · ? help"
		}
		if t.stashes != nil {
			return "↑↓ move · Enter files, then a file's diff · a apply · p pop · d drop · n new branch · s stash changes · U undo · r reload · Esc back · ? help"
		}
		if t.changes != nil {
			return "↑↓ move · Enter or d diff · s stage · u unstage · x discard · H hunks · a stage all · A unstage all · X discard all · i ignore · C commit · r reload · Esc back · ? help"
		}
		return "Enter details · / filter · b branch · B branches · e merge · C commit · t tag · T tags · u update · v revert · d drop · z roll back · c cherry-pick · U undo" + t.redoFooter() + " · O remotes and more · m more · ? all keys · q quit"
	case tabRules:
		return "a add · x remove · ? help · q quit"
	case tabReviews:
		return "Enter or o open in the browser · r refresh · ? help · q quit"
	}
	return "Enter toggle / fix · r check again · ? help · q quit"
}

func mark(on bool) string {
	if on {
		return cBold + "› "
	}
	return "  "
}

func (t *tui) drawAccounts() ([]string, int) {
	c := t.cfg
	var l []string
	if !t.installed() {
		l = append(l, "\x00"+cRed+" Git is not using GitSwitch yet. Go to Settings (5) and fix \"Git runs through gitswitch\"."+cReset, "\x00")
	}
	if len(c.Accounts) == 0 {
		return append(l, " No accounts yet. Press a to add your first one."), -1
	}
	if c.Default == "" && len(c.Accounts) > 1 {
		l = append(l, "\x00"+cDim+" No default account: repos with nothing saved ask; 2! saves the answer for the repo. d makes an account the default."+cReset, "\x00")
	}
	// Columns are as wide as their longest value, and every row keeps room
	// for " (default)", so the default account does not push its row right.
	wID, wEmail, wHost := 14, 30, 14
	for _, a := range c.Accounts {
		wID = max(wID, utf8.RuneCountInString(a.ID))
		wEmail = max(wEmail, utf8.RuneCountInString(a.Email))
		wHost = max(wHost, utf8.RuneCountInString(a.Host))
	}
	for i, a := range c.Accounts {
		def := strings.Repeat(" ", 10)
		if strings.EqualFold(a.ID, c.Default) {
			def = cDim + " (default)" + cReset
		}
		key := cGreen + "key ok" + cReset
		if !t.app.Keys.HasKey(&a) {
			key = cRed + "no key file" + cReset
		}
		user := ""
		if a.User != "" {
			user = cDim + "  logs in as " + a.User + cReset
		}
		switch st := t.apis[strings.ToLower(a.ID)]; {
		case st.Error != "":
			user += cRed + "  " + st.Provider + " token: " + st.Error + cReset
		case st.SignedIn:
			user += cGreen + "  · signed in to " + st.Provider + cReset
		case st.Supported:
			user += cDim + "  · not signed in to " + st.Provider + " (g)" + cReset
		}
		user += signNote(a)
		on := i == t.sel[tabAccounts]
		l = append(l, fmt.Sprintf("%s%s█%s %s%-*s%s%s  %-*s %-*s %s%s", mark(on), t.color(a.ID), cReset, boldIf(on), wID, a.ID, cReset, def, wEmail, a.Email, wHost, a.Host, key, user))
	}
	return l, t.sel[tabAccounts]
}

func (t *tui) installed() bool { return t.hooked }

func boldIf(on bool) string {
	if on {
		return cBold
	}
	return ""
}

func (t *tui) drawRepos() ([]string, int) {
	l := []string{"\x00" + cDim + " Folders: " + orNone(strings.Join(t.cfg.ScanFolders, ", ")) + cReset, "\x00"}
	if len(t.repos) == 0 {
		return append(l, " No repos found. Press f to add the folder where you keep your projects, or a to add one repo."), -1
	}
	why := map[string]string{"saved": "saved", "owner-rule": "owner rule", "folder-rule": "folder rule", "only-account": "only account", "asks": "asks on next push", "asks-every-time": "asks on every push", "": "no account for this host"}
	selLine, hdr := -1, len(l) // the selection, counted below the fixed lines
	for i, r := range t.repos {
		for _, g := range t.groups {
			if g.at == i {
				if i > 0 {
					l = append(l, "")
				}
				l = append(l, " "+cBold+g.name+cReset+cDim+fmt.Sprintf(" (%d)", g.n)+cReset)
			}
		}
		on := i == t.sel[tabRepos]
		if on {
			selLine = len(l) - hdr
		}
		star := "  "
		if r.Pinned {
			star = cYellow + "★ " + cReset + boldIf(on)
		}
		if r.Missing {
			l = append(l, fmt.Sprintf("%s%s%-22s%s %s", mark(on), star, clip(filepath.Base(r.Path), 22), cReset, cRed+"folder not found: "+r.Path+" (x removes it from the list)"+cReset))
			continue
		}
		acct := cDim + why[r.Reason] + cReset
		if r.Account != "" {
			acct = t.color(r.Account) + r.Account + cReset + cDim + " (" + why[r.Reason] + ")" + cReset
		}
		email := cDim + orNone(r.Email) + cReset
		if r.SSHOverride != "" {
			acct = cRed + "bypassed (own core.sshCommand)" + cReset
		}
		if r.EmailBad {
			email = cRed + r.Email + " (wrong account)" + cReset
		}
		branch := r.Branch
		if branch == "" {
			branch = "(detached)"
		}
		clones := ""
		if len(r.Clones) > 0 {
			clones = cYellow + fmt.Sprintf("  · also cloned at %s", r.Clones[0]) + cReset
			if len(r.Clones) > 1 {
				clones += cYellow + fmt.Sprintf(" and %d more", len(r.Clones)-1) + cReset
			}
		}
		l = append(l, fmt.Sprintf("%s%s%-22s%s %s  %s  %s  %s%s", mark(on), star, clip(filepath.Base(r.Path), 22), cReset, cDim+padANSI(clip(branch, 16), 16)+cReset, padANSI(acct, 34), padANSI(email, 34), cDim+r.Remote+cReset, clones))
	}
	return l, selLine
}

func (t *tui) drawHistory() ([]string, int) {
	if t.histDir == "" {
		return []string{" Press o to choose a repo, or start gitswitch inside a repo."}, -1
	}
	var l []string
	if h := t.hist; h != nil {
		acct := cDim + "not set" + cReset
		if h.Repo.Account != "" {
			acct = t.color(h.Repo.Account) + h.Repo.Account + cReset
		}
		flag := cGreen + "no wrong-account commits" + cReset
		if h.Flagged > 0 {
			flag = cRed + fmt.Sprintf("%d wrong-account commit(s)", h.Flagged) + cReset
		}
		filter := ""
		if t.flagged {
			filter = cDim + "  [showing wrong-account only]" + cReset
		}
		l = append(l, "\x00 "+cBold+h.Repo.Path+cReset+"  account: "+acct+"  "+flag+cDim+fmt.Sprintf("  %d not pushed", h.Unpushed)+cReset+filter)
		if t.head != nil {
			more := ""
			if t.head.Changes.Any() {
				more = cDim + "  · s to see the changes" + cReset
			}
			l = append(l, "\x00 branch: "+cBold+history.CleanText(t.head.Describe())+cReset+more)
			l = append(l, t.opBanner()...)
		}
		if b := t.bisectBanner(); b != "" {
			l = append(l, b)
		}
		l = append(append(l, t.detachedLines()...), t.sigLegend()...)
		if h.Repo.EmailBad {
			l = append(l, "\x00 "+cRed+"new commits here are made as "+history.CleanText(h.Repo.Email)+", not as "+h.Repo.Account+cReset+cDim+"  · i to use "+h.Repo.Account+"'s name and email"+cReset)
		}
		if t.filter.Active() {
			l = append(l, "\x00 "+cYellow+"filter: "+t.filter.Describe()+cReset+cDim+fmt.Sprintf("  %d commit(s) · / to change", len(h.Commits))+cReset)
		}
		l = append(l, "\x00")
	}
	sel := t.sel[tabHistory]
	t.loadSigs()
	for i, x := range t.histLines {
		lead := " "
		if i < len(t.histRows) && t.marked[t.histRows[i].Hash] {
			lead = cGreen + "+" + cReset
		}
		lead += t.sigBadge(i)
		if i == sel {
			l = append(l, cRev+lead+cReset+x)
		} else {
			l = append(l, lead+x)
		}
	}
	if t.hist != nil && t.hist.More {
		l = append(l, "", " "+cDim+fmt.Sprintf("Showing the latest %d commits · m loads %d more", len(t.hist.Commits), histPage)+cReset)
	}
	return l, sel
}

func (t *tui) drawRules() ([]string, int) {
	l := []string{"\x00" + cDim + " Rules pick the account automatically. A repo's saved account always wins." + cReset, "\x00"}
	if len(t.cfg.Rules) == 0 {
		return append(l, " No rules yet. Press a to add one, e.g. owner acme-corp → client-acme."), -1
	}
	for i, r := range t.cfg.Rules {
		on := i == t.sel[tabRules]
		what := "owner or org is"
		if r.Kind == "folder" {
			what = "repo is inside"
		}
		l = append(l, fmt.Sprintf("%s%-16s %-40s → %s%s%s", mark(on), what, r.Pattern, t.color(r.Account), r.Account, cReset))
	}
	return l, t.sel[tabRules]
}

// settingRows is how many settings come before the health checks on the
// Settings page.
const settingRows = 6

func (t *tui) drawSettings() ([]string, int) {
	s := t.sel[tabSettings]
	onoff := map[bool]string{true: cGreen + "on" + cReset, false: cDim + "off" + cReset}
	l := []string{
		fmt.Sprintf("%sPopup when git runs without a terminal (IDE source control): %s%s", mark(s == 0), onoff[t.cfg.GUIPrompt], cReset),
		fmt.Sprintf("%sSeconds to wait for an answer: %d %s%s", mark(s == 1), t.cfg.PromptTimeout, map[bool]string{true: "(then git does not connect: no default account)", false: "(then the default account, " + t.cfg.Default + ")"}[t.cfg.Default == ""], cReset),
		fmt.Sprintf("%sSwitching branch with uncommitted changes, in every repo: %s%s %s(Enter: ask → smart switch → park; a repo's own choice comes first)%s", mark(s == 2), cBold,
			map[refs.SwitchMode]string{refs.Ask: "ask each time", refs.Smart: "take them along (smart switch)", refs.Park: "park them on their branch"}[t.app.Refs.SwitchSetting("").Global], cReset+cDim, cReset),
		fmt.Sprintf("%sNew branch names start with: %s%s", mark(s == 3), map[bool]string{true: cDim + "nothing (Enter to set, e.g. feature/)", false: cBold + t.app.Refs.BranchPrefix()}[t.app.Refs.BranchPrefix() == ""], cReset),
		fmt.Sprintf("%sWhen update finds new commits on both sides, in every repo: %s%s %s(Enter switches; a repo's own choice comes first)%s", mark(s == 4), cBold,
			map[refs.PullMode]string{refs.FFOnly: "stop and explain (fast-forward only)", refs.Rebase: "put my commits on top (rebase)"}[t.app.Refs.PullSetting("").Global], cReset+cDim, cReset),
		fmt.Sprintf("%sRead the remote's new commits when a repo's History opens (fetch): %s%s", mark(s == 5), onoff[t.app.Refs.FetchOnOpen()], cReset),
	}
	for i, c := range t.checks {
		st := cGreen + "ok " + cReset
		if !c.OK {
			st = cRed + "FIX" + cReset
			if c.CanFix {
				st += cDim + " (Enter)" + cReset
			}
		}
		l = append(l, fmt.Sprintf("%s%s %s%s  %s%s", mark(s == i+settingRows), st, c.Title, cReset, cDim, c.Detail+cReset))
	}
	return l, s
}

func (t *tui) overlay() []string {
	box := func(title string, body []string) []string {
		w := min(t.w-4, 100)
		out := []string{" ┌─ " + cBold + title + cReset + " " + strings.Repeat("─", max(0, w-utf8.RuneCountInString(title)-4)) + "┐"}
		for _, b := range body {
			out = append(out, " │ "+padANSI(b, w-2)+"│")
		}
		out = append(out, " └"+strings.Repeat("─", w-1)+"┘") // as wide as the top and the sides
		return out
	}
	switch {
	case t.view != nil:
		v := t.view
		var body []string
		for _, l := range v.lines {
			body = append(body, wrapPlain(l, min(t.w-8, 96))...)
		}
		rows := t.overlayRows()
		if len(body) <= rows {
			v.off = 0
			return box(v.title, body)
		}
		rows-- // room for the position line
		v.off = max(0, min(v.off, len(body)-rows))
		page := append([]string(nil), body[v.off:v.off+rows]...)
		page = append(page, cDim+fmt.Sprintf("lines %d-%d of %d · ↑↓ PgUp PgDn to scroll", v.off+1, v.off+rows, len(body))+cReset)
		return box(v.title, page)
	case t.confirm != nil:
		var body []string
		for _, l := range strings.Split(t.confirm.text, "\n") { // a question may list lines
			body = append(body, wrapPlain(l, min(t.w-8, 96))...)
		}
		return box("Confirm", body)
	case t.picker != nil:
		p := t.picker
		shown := p.shown()
		rows := t.pickerRows()
		p.sel = max(0, min(p.sel, len(shown)-1))
		p.off = scrollTo(p.off, p.sel, rows, len(shown))
		body := []string{"Filter: " + p.filter + cRev + " " + cReset + cDim + "  type to narrow the list" + cReset, ""}
		for j := p.off; j < len(shown) && j < p.off+rows; j++ {
			body = append(body, mark(j == p.sel)+p.options[shown[j]]+cReset)
		}
		switch {
		case len(shown) == 0:
			body = append(body, "  No match. Backspace changes the filter, Esc clears it.")
		case len(shown) > rows:
			body = append(body, cDim+fmt.Sprintf("  %d-%d of %d", p.off+1, min(len(shown), p.off+rows), len(shown))+cReset)
		}
		return box(p.title, body)
	case t.form != nil:
		var body []string
		for i, f := range t.form.fields {
			v := strings.ReplaceAll(f.value, "\n", " ⏎ ") // a commit message's lines, on one line
			if f.secret {
				v = strings.Repeat("•", utf8.RuneCountInString(v))
			}
			if len(f.options) > 0 {
				v = "‹ " + v + " ›"
			}
			cursor := ""
			if i == t.form.focus && len(f.options) == 0 {
				cursor = cRev + " " + cReset
			}
			label := fmt.Sprintf("%-28s", f.label)
			line := mark(i == t.form.focus) + label + cReset + v + cursor
			if f.hint != "" && i == t.form.focus {
				line += "  " + cDim + f.hint + cReset
			}
			body = append(body, line)
		}
		body = append(body, "", cDim+"Enter on the last field saves."+cReset)
		return box(t.form.title, body)
	}
	return nil
}

// overlayRows is how many lines fit inside a box: the screen less the
// header, status and footer lines and the box's own borders.
func (t *tui) overlayRows() int { return max(1, t.h-6) }

// pickerRows is how many options fit, below the filter line and above
// the position line.
func (t *tui) pickerRows() int { return max(1, t.overlayRows()-3) }

// scrollTo returns the first row to show of n so that row sel is among
// the rows shown.
func scrollTo(off, sel, rows, n int) int {
	if sel < off {
		off = sel
	}
	if sel >= off+rows {
		off = sel - rows + 1
	}
	return max(0, min(off, n-rows))
}

// ---------------- text helpers (ANSI aware) ----------------

func visibleLen(s string) int { return utf8.RuneCountInString(history.StripANSI(s)) }

func padANSI(s string, w int) string {
	n := visibleLen(s)
	if n >= w {
		return fit(s, w)
	}
	return s + strings.Repeat(" ", w-n)
}

// fit cuts a line to w visible columns, keeping colour codes intact.
func fit(s string, w int) string {
	var b strings.Builder
	vis := 0
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
			b.WriteRune(r)
		case esc:
			b.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				esc = false
			}
		default:
			if vis >= w {
				continue
			}
			b.WriteRune(r)
			vis++
		}
	}
	b.WriteString(cReset)
	return b.String()
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func wrapPlain(s string, w int) []string {
	if w < 10 || visibleLen(s) <= w || strings.Contains(s, "\x1b") {
		return []string{s}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > w {
			out = append(out, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return append(out, line)
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// ---------------- stashes ----------------

// stashView is the History page's stash manager: every stash of the repo
// (parked and discarded changes too), and the files of the selected one.
type stashView struct {
	gd    string
	all   []refs.Stash
	sel   int
	files bool // the selected stash's files are open
	fsel  int
}

// openStashes shows the selected repo's stashes.
func (t *tui) openStashes() {
	if t.histDir == "" {
		t.say(fmt.Errorf("choose a repo first (o)"), "")
		return
	}
	t.stashes = &stashView{gd: t.histDir}
	t.msg = ""
	t.loadStashes()
}

// loadStashes reads the list again, keeping the selection on the same stash
// (by its commit) while it is still there.
func (t *tui) loadStashes() {
	v := t.stashes
	if v == nil {
		return
	}
	var was string
	if v.sel < len(v.all) {
		was = v.all[v.sel].Hash
	}
	all, err := t.app.Refs.Stashes(v.gd)
	if err != nil {
		t.stashes = nil
		t.say(err, "")
		return
	}
	if h, err := t.app.Refs.Head(v.gd); err == nil {
		t.head = &h
	}
	v.all = all
	moved := false
	for i, st := range all {
		if st.Hash == was {
			v.sel, moved = i, true
		}
	}
	if !moved {
		v.sel, v.files = max(0, min(v.sel, len(all)-1)), false
	}
	if v.sel < len(all) {
		v.fsel = max(0, min(v.fsel, len(all[v.sel].Files)-1))
	}
}

func (t *tui) closeStashes() {
	t.stashes = nil
	sel := t.sel[tabHistory]
	t.readHistory() // an apply or a pop changed the uncommitted changes
	t.sel[tabHistory] = max(0, min(sel, len(t.histLines)-1))
}

// stashDone says how a stash action went and reads the list again; a stale
// stash (the list changed) is reloaded too.
func (t *tui) stashDone(err error, ok string) error {
	t.loadStashes()
	t.say(err, ok)
	return nil
}

// keyStashes handles a key while the stashes are shown. Keys it does not use
// (switching page, help, quit with Ctrl+C) go to the main handler.
func (t *tui) keyStashes(k string) bool {
	v := t.stashes
	n := len(v.all)
	var st *refs.Stash
	if v.sel < n {
		st = &v.all[v.sel]
	}
	move := func(d int) {
		if v.files && st != nil {
			v.fsel = max(0, min(len(st.Files)-1, v.fsel+d))
		} else {
			v.sel, v.fsel = max(0, min(n-1, v.sel+d)), 0
		}
	}
	page := max(1, t.h-6)
	switch k {
	case "tab", "backtab", "left", "right", "1", "2", "3", "4", "5", "6", "?":
		return false
	case "esc", "q":
		if v.files {
			v.files = false
		} else {
			t.closeStashes()
		}
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup":
		move(-page)
	case "pgdn":
		move(page)
	case "home", "g":
		move(-1 << 20)
	case "end", "G":
		move(1 << 20)
	case "r":
		t.loadStashes()
	case "enter":
		switch {
		case st == nil:
		case !v.files:
			if len(st.Files) == 0 {
				t.say(nil, "That stash holds no files.")
			} else {
				v.files, v.fsel = true, 0
			}
		default:
			t.showStashDiff(*st, st.Files[v.fsel])
		}
	case "a", "p":
		if st != nil {
			t.stashApply(*st, k == "p")
		}
	case "d":
		if st != nil {
			t.stashDrop(*st)
		}
	case "n":
		if st != nil {
			t.stashBranch(*st)
		}
	case "s":
		t.stashSave()
	case "U":
		t.undoLast() // the list is read again once it is confirmed (reload)
	}
	return true
}

func (t *tui) stashApply(st refs.Stash, pop bool) {
	gd := t.stashes.gd
	if pop {
		err := t.app.Refs.StashPop(gd, st.Hash)
		t.stashDone(err, "Popped "+st.Label()+" and removed it from the list. It can't be undone: the changes are in your files.")
		return
	}
	err := t.app.Refs.StashApply(gd, st.Hash)
	t.stashDone(err, "Applied "+st.Label()+"; it is still in the list.")
}

func (t *tui) stashDrop(st refs.Stash) {
	gd := t.stashes.gd
	t.confirm = &confirmBox{fmt.Sprintf("Delete %s: %s? U brings it back. (y/n)", history.CleanText(st.Label()), refs.FileList(stashFilePaths(st))),
		map[string]func() error{"y": func() error {
			err := t.app.Refs.StashDrop(gd, st.Hash)
			if err == nil {
				t.loadStashes()
				t.say(nil, "Deleted "+st.Label()+"."+undoHint)
				return nil
			}
			return t.stashDone(err, "")
		}}}
}

func (t *tui) stashBranch(st refs.Stash) {
	gd := t.stashes.gd
	t.form = &form{title: "New branch from " + history.CleanText(st.Label()), fields: []field{{label: "Name", value: t.app.Refs.BranchPrefix(),
		hint: "the stash goes onto it and is removed; needs no uncommitted changes"}},
		submit: func(v []string) error {
			name := strings.TrimSpace(v[0])
			if name == "" || name == t.app.Refs.BranchPrefix() {
				return fmt.Errorf("type a name for the branch")
			}
			if err := t.app.Refs.StashBranch(gd, st.Hash, name); err != nil {
				t.loadStashes()
				return err
			}
			t.loadStashes()
			t.say(nil, "Made branch "+name+" from "+st.Label()+" and switched to it.")
			return nil
		}}
}

// stashSave asks for a message and stashes every uncommitted change.
func (t *tui) stashSave() {
	gd := t.stashes.gd
	t.form = &form{title: "Stash the uncommitted changes", fields: []field{
		{label: "Message", hint: "optional"},
		{label: "New files", value: "include", options: []string{"include", "leave"}, hint: "←→ untracked files too, or only tracked ones"},
		{label: "Staged", value: "stash too", options: []string{"stash too", "keep staged"}, hint: "←→ keep the staged changes in your files"},
	}, submit: func(v []string) error {
		st, err := t.app.Refs.StashSave(gd, refs.StashOptions{Message: v[0], Untracked: v[1] == "include", KeepIndex: v[2] == "keep staged"})
		if err != nil {
			return err
		}
		t.loadStashes()
		hint := ""
		if v[2] != "keep staged" {
			hint = undoHint
		}
		t.say(nil, "Stashed "+refs.FileList(stashFilePaths(st))+" as "+st.Label()+"."+hint)
		return nil
	}}
}

func stashFilePaths(st refs.Stash) []string {
	p := make([]string, len(st.Files))
	for i, f := range st.Files {
		p[i] = f.Path
	}
	return p
}

// showStashDiff shows one file of a stash; new files show their whole
// content.
func (t *tui) showStashDiff(st refs.Stash, f refs.StashFile) {
	d, err := t.app.Refs.StashDiff(t.stashes.gd, st.Hash, f.Path)
	if err != nil {
		t.loadStashes()
		t.say(err, "")
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(d.Text, "\n"), "\n") {
		l = history.CleanText(l)
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			l = cBold + l + cReset
		case strings.HasPrefix(l, "+"):
			l = cGreen + l + cReset
		case strings.HasPrefix(l, "-"):
			l = cRed + l + cReset
		case strings.HasPrefix(l, "@@"):
			l = cYellow + l + cReset
		case strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "):
			l = cDim + l + cReset
		}
		lines = append(lines, l)
	}
	if d.Truncated {
		lines = append(lines, "", cDim+fmt.Sprintf("… cut at %d KB; git stash show -p shows all of it", refs.MaxStashDiffBytes>>10)+cReset)
	}
	t.view = &viewer{title: history.CleanText(f.Path) + " · " + st.Ref, lines: lines}
}

// drawStashes lists the stashes, newest first, and under the selected one
// its files when they are open.
func (t *tui) drawStashes() ([]string, int) {
	v := t.stashes
	path := v.gd
	if t.hist != nil {
		path = t.hist.Repo.Path
	}
	branch := "(detached)"
	if t.head != nil && t.head.Branch != "" {
		branch = t.head.Branch
	}
	l := []string{"\x00 " + cBold + "Stashes" + cReset + "  " + path + "  branch: " + cBold + history.CleanText(branch) + cReset, "\x00"}
	if len(v.all) == 0 {
		return append(l, " No stashes. s stashes the uncommitted changes; Esc goes back to the commits."), -1
	}
	hdr, selLine := len(l), -1
	now := time.Now().Unix()
	for i, st := range v.all {
		on := i == v.sel && !v.files
		if i == v.sel && !v.files {
			selLine = len(l) - hdr
		}
		kind := ""
		switch st.Kind {
		case refs.StashPark:
			kind = cYellow + " parked on " + history.CleanText(st.ParkedOn) + cReset
		case refs.StashDiscard:
			kind = cYellow + " discarded on " + history.CleanText(st.ParkedOn) + cReset
		}
		files := fmt.Sprintf("%d files", len(st.Files))
		if len(st.Files) == 1 {
			files = "1 file"
		}
		if st.Untracked > 0 {
			files += fmt.Sprintf(", %d new", st.Untracked)
		}
		from := st.Branch
		if from == "" {
			from = "detached HEAD"
		}
		l = append(l, fmt.Sprintf("%s%s  %s%s%s  %s%s", mark(on), history.CleanText(st.Ref), boldIf(on), history.CleanText(st.Message), cReset+kind,
			cDim, history.CleanText(from)+" · "+refs.Ago(now, st.Time)+" · "+files+cReset))
		if i == v.sel && v.files {
			for j, f := range st.Files {
				fon := j == v.fsel
				if fon {
					selLine = len(l) - hdr
				}
				col := cYellow
				switch f.Status {
				case "A", "?":
					col = cGreen
				case "D":
					col = cRed
				}
				extra := fmt.Sprintf("+%d -%d", f.Added, f.Deleted)
				if f.Binary {
					extra = "binary"
				}
				if f.Staged {
					extra += " · staged"
				}
				l = append(l, fmt.Sprintf("  %s%s%s%s %s%s%s  %s", mark(fon), col, f.Status, cReset, boldIf(fon), history.CleanText(f.Path), cReset, cDim+extra+cReset))
			}
		}
	}
	return l, selLine
}
