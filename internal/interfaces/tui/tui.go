// Package tui is the terminal front end (`gitswitch` or `gitswitch tui`).
// It runs only while open, in the terminal it was started from.
package tui

import (
	"bufio"
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
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

// The TUI runs only while it is open, in the terminal it was started from
// (Windows Terminal, VS Code, Android Studio, macOS Terminal, ...).

var tabNames = []string{"Accounts", "Repos", "History", "Rules", "Settings"}

const (
	tabAccounts = iota
	tabRepos
	tabHistory
	tabRules
	tabSettings
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
	tab     int
	sel     [5]int
	off     [5]int
	cfg     *account.Config
	app     *bootstrap.App
	repos   []repo.Info
	groups  []repoHeading // headings on the Repos page, by the repo they come before
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
	if t.tab == tabSettings {
		t.checks = t.app.Doctor.Run(false)
	}
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
			// An action may ask a follow-up question or show a page
			// instead of finishing.
			if err := f(); err != nil || (t.msg == "" && t.confirm == nil && t.view == nil) {
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
	case "1", "2", "3", "4", "5":
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
	}
}

const helpText = `
Everywhere
  1-5 or Tab / ←→   switch page          ↑↓ PgUp PgDn   move
  q or Ctrl+C       quit                  ?              this help
Accounts
  a add   e edit   R rename   t test login   k public key   K use another key   d default (again: no default, always ask)
  p passphrase to agent   x remove (then y keeps the key files, Shift+D deletes them too)
Repos
  Enter choose account   h history   c clone by URL   f add folder   a add one repo   r rescan
  p pin / unpin   o open folder   O open in editor   x remove from list (files untouched)
  g group the list by nothing, account, owner, folder or your groups   G put the repo in a group you name
History
  Enter commit details   o other repo   f only wrong-account commits   x fix not-pushed commits
  r reload   m load more
  b switch branch   u update (pull)   p push (never forced; -u the first time; commits made as another account: fix first)
  i make new commits with the repo account's name and email (when the History header says they aren't)   w view in browser   / filter by author, message, date or account
  U undo the last commit, switch, move, prune, revert, drop, roll back, cherry-pick or fix
  R after a smart switch with clashing files: I've fixed them, go back, or park them instead
  P parked changes (git stash): restore, look first or delete
  n new branch here   N new branch at the selected commit (uncommitted changes come along)
  M committed on the wrong branch: move the commits not pushed to a new branch
  D delete the branches merged into the remote's default branch
  S compare with the remote branch: the commits you would push and pull
  C commit the staged files as the repo's account (nothing staged: every change, new files too)
  on a commit: v revert   d drop   z roll back (reset) to here   c cherry-pick   Space mark several to cherry-pick
Rules
  a add rule   x remove rule
Settings
  Enter toggle or fix the selected item   r run the health checks again
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

// ---------------- repos ----------------

// openHistory shows a repo's history and records it as recently opened.
func (t *tui) openHistory(gitDir string) {
	t.histDir = gitDir
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
	groups, err := t.app.Repos.Groups("")
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
	case "c":
		t.form = &form{title: "Clone a repo by URL", fields: []field{
			{label: "URL", hint: "git@github.com:owner/repo.git or https://..."},
			{label: "Folder", hint: "empty = ./<repo name> here"},
			{label: "Account", value: "automatic", options: append([]string{"automatic"}, t.cfg.SortedIDs()...), hint: "←/→ · automatic = rules, then ask"},
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
}

// showCommit opens the selected commit's full message and changed files.
func (t *tui) showCommit() {
	hash := t.selectedCommit()
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
	case "R":
		t.resolve()
	case "P":
		t.pickParked()
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
	case "S":
		t.compare()
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
	t.picker = &picker{title: "Switch to branch", options: opts, pick: func(i int) error {
		name := bs[i].Name
		if h, err := t.app.Refs.Head(gd); err == nil && h.Changes.Any() && h.Busy == "" && !bs[i].Current {
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
	}}
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
// change when none is staged.
func (t *tui) commit() {
	if t.histDir == "" || t.head == nil {
		return
	}
	gd, c := t.histDir, t.head.Changes
	all := c.Staged == 0
	title := fmt.Sprintf("Commit the %d staged file(s)", c.Staged)
	if all {
		title = fmt.Sprintf("Commit every change (%d changed, %d new)", c.Changed, c.New)
	}
	t.form = &form{title: title, fields: []field{{label: "Message", hint: "what the change does"}},
		submit: func(v []string) error {
			r, err := t.app.Workspace.Commit(gd, v[0], all)
			if err != nil {
				return err
			}
			t.loadHistory()
			as := ""
			if r.Account != "" {
				as = " as " + r.Account
			}
			t.say(nil, "Committed "+r.Short+" "+history.CleanText(r.Subject)+as+"."+undoHint)
			return nil
		}}
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
func (t *tui) update() {
	if t.histDir == "" {
		return
	}
	gd := t.histDir
	var h refs.Head
	var err error
	t.suspend(func() {
		fmt.Println("Updating from the remote ...")
		h, err = t.app.Refs.Update(gd, func(l string) { fmt.Print("\r\x1b[K  " + l) })
		fmt.Println()
		if err != nil {
			fmt.Println("gitswitch:", err)
		}
	})
	t.loadHistory()
	t.say(err, "Updated: "+h.Describe())
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
	out = append(out, head, "")
	body := t.h - 4
	var lines []string
	sel := -1
	switch t.tab {
	case tabAccounts:
		lines, sel = t.drawAccounts()
	case tabRepos:
		lines, sel = t.drawRepos()
	case tabHistory:
		lines, sel = t.drawHistory()
	case tabRules:
		lines, sel = t.drawRules()
	case tabSettings:
		lines, sel = t.drawSettings()
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
		return "a add · e edit · R rename · t test · k public key · K pick key · d default · x remove · ? help · q quit"
	case tabRepos:
		return "Enter account · h history · c clone · a add repo · f add folder · p pin · g group by · G group · o open · x remove · r rescan · ? help · q quit"
	case tabHistory:
		return "Enter details · / filter · b branch · C commit · u update · v revert · d drop · z roll back · c cherry-pick · U undo · m more · ? all keys · q quit"
	case tabRules:
		return "a add · x remove · ? help · q quit"
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
			user = cDim + "  signed in as " + a.User + cReset
		}
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
			l = append(l, "\x00 branch: "+cBold+history.CleanText(t.head.Describe())+cReset)
		}
		if h.Repo.EmailBad {
			l = append(l, "\x00 "+cRed+"new commits here are made as "+history.CleanText(h.Repo.Email)+", not as "+h.Repo.Account+cReset+cDim+"  · i to use "+h.Repo.Account+"'s name and email"+cReset)
		}
		if t.filter.Active() {
			l = append(l, "\x00 "+cYellow+"filter: "+t.filter.Describe()+cReset+cDim+fmt.Sprintf("  %d commit(s) · / to change", len(h.Commits))+cReset)
		}
		l = append(l, "\x00")
	}
	sel := t.sel[tabHistory]
	for i, x := range t.histLines {
		lead := " "
		if i < len(t.histRows) && t.marked[t.histRows[i].Hash] {
			lead = cGreen + "+" + cReset
		}
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
const settingRows = 4

func (t *tui) drawSettings() ([]string, int) {
	s := t.sel[tabSettings]
	onoff := map[bool]string{true: cGreen + "on" + cReset, false: cDim + "off" + cReset}
	l := []string{
		fmt.Sprintf("%sPopup when git runs without a terminal (IDE source control): %s%s", mark(s == 0), onoff[t.cfg.GUIPrompt], cReset),
		fmt.Sprintf("%sSeconds to wait for an answer: %d %s%s", mark(s == 1), t.cfg.PromptTimeout, map[bool]string{true: "(then git does not connect: no default account)", false: "(then the default account, " + t.cfg.Default + ")"}[t.cfg.Default == ""], cReset),
		fmt.Sprintf("%sSwitching branch with uncommitted changes, in every repo: %s%s %s(Enter: ask → smart switch → park; a repo's own choice comes first)%s", mark(s == 2), cBold,
			map[refs.SwitchMode]string{refs.Ask: "ask each time", refs.Smart: "take them along (smart switch)", refs.Park: "park them on their branch"}[t.app.Refs.SwitchSetting("").Global], cReset+cDim, cReset),
		fmt.Sprintf("%sNew branch names start with: %s%s", mark(s == 3), map[bool]string{true: cDim + "nothing (Enter to set, e.g. feature/)", false: cBold + t.app.Refs.BranchPrefix()}[t.app.Refs.BranchPrefix() == ""], cReset),
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
		return box("Confirm", wrapPlain(t.confirm.text, min(t.w-8, 96)))
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
			v := f.value
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
