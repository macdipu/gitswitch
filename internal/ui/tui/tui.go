// Package tui is the terminal front end (`gitswitch` or `gitswitch tui`).
// It runs only while open, in the terminal it was started from.
package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/macdipu/gitswitch/internal/account"
	"github.com/macdipu/gitswitch/internal/app"
	"github.com/macdipu/gitswitch/internal/doctor"
	"github.com/macdipu/gitswitch/internal/history"
	"github.com/macdipu/gitswitch/internal/keys"
	"github.com/macdipu/gitswitch/internal/repo"
	"github.com/macdipu/gitswitch/internal/sys"
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
	sel     int
	pick    func(i int) error
}

type confirmBox struct {
	text    string
	actions map[string]func() error // key -> action; anything else cancels
}

type viewer struct {
	title string
	lines []string
	url   string
	copy  string
}

type tui struct {
	tab     int
	sel     [5]int
	off     [5]int
	cfg     *account.Config
	app     *app.App
	repos   []repo.Info
	scanned bool
	checks  []doctor.Check

	histDir   string
	histLines []string
	hist      *history.History
	flagged   bool

	msg    string
	msgErr bool

	form    *form
	picker  *picker
	confirm *confirmBox
	view    *viewer

	tty       *sys.TTYState
	w, h      int
	quit      bool
	mu        sync.Mutex
	suspended bool
}

func Run(a *app.App) error {
	st, err := sys.MakeRaw()
	if err != nil {
		return fmt.Errorf("%v (try `gitswitch ui` for the desktop app)", err)
	}
	t := &tui{tty: st, app: a}
	fmt.Print("\x1b[?1049h\x1b[?25l") // alternate screen, hide cursor
	defer func() {
		fmt.Print("\x1b[?25h\x1b[?1049l")
		sys.RestoreTTY(t.tty)
	}()
	t.reload()
	t.histDir = a.CurrentRepo()
	in := bufio.NewReader(os.Stdin)
	buf := make([]byte, 64)
	stop := sys.WatchResize(func() {
		t.mu.Lock()
		if !t.suspended && !t.quit {
			t.draw()
		}
		t.mu.Unlock()
	})
	defer stop()
	for !t.quit {
		t.mu.Lock()
		t.draw()
		t.mu.Unlock()
		n, err := in.Read(buf)
		if err != nil {
			return nil
		}
		t.mu.Lock()
		for _, k := range parseKeys(buf[:n]) {
			t.handle(k)
			if t.quit {
				break
			}
		}
		t.mu.Unlock()
	}
	return nil
}

func (t *tui) reload() {
	t.cfg = t.app.Config()
	t.checks = t.app.Checks(false)
}

func (t *tui) say(err error, ok string) {
	if err != nil {
		t.msg, t.msgErr = err.Error(), true
	} else {
		t.msg, t.msgErr = ok, false
	}
}

// suspend leaves the TUI so a command can use the terminal normally
// (ssh-keygen passphrase, ssh login), then comes back.
func (t *tui) suspend(f func()) {
	t.suspended = true
	defer func() { t.suspended = false }()
	fmt.Print("\x1b[?25h\x1b[?1049l")
	sys.RestoreTTY(t.tty)
	f()
	fmt.Print("\nPress Enter to go back to GitSwitch...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	st, _ := sys.MakeRaw()
	t.tty = st
	fmt.Print("\x1b[?1049h\x1b[?25l")
}

// ---------------- keys ----------------

func parseKeys(b []byte) []string {
	var keys []string
	for len(b) > 0 {
		if b[0] == 0x1b {
			if len(b) == 1 {
				keys = append(keys, "esc")
				break
			}
			if b[1] == '[' || b[1] == 'O' {
				j := 2
				for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
					j++
				}
				if j >= len(b) {
					break
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
			r, size := utf8.DecodeRune(b)
			keys = append(keys, string(r))
			b = b[size:]
			continue
		}
		b = b[1:]
	}
	return keys
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
		c := t.confirm
		t.confirm = nil
		if f, ok := c.actions[strings.ToLower(k)]; ok {
			t.msg = ""
			if err := f(); err != nil || t.msg == "" {
				t.say(err, "Done.")
			}
			t.reload()
		} else {
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
	switch k {
	case "esc", "enter", "q":
		t.view = nil
	case "c":
		if v.copy != "" && sys.CopyToClipboard(v.copy) {
			t.say(nil, "Copied to clipboard.")
		}
	case "o":
		if v.url != "" {
			t.say(sys.OpenBrowser(v.url), "Opened "+v.url)
		}
	}
}

func (t *tui) handlePicker(k string) {
	p := t.picker
	switch k {
	case "up", "k":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "j":
		if p.sel < len(p.options)-1 {
			p.sel++
		}
	case "esc", "q":
		t.picker = nil
	case "enter":
		t.picker = nil
		if err := p.pick(p.sel); err != nil {
			t.say(err, "")
		}
		t.reload()
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

func (t *tui) rows() int {
	switch t.tab {
	case tabAccounts:
		return len(t.cfg.Accounts)
	case tabRepos:
		return len(t.repos)
	case tabHistory:
		return len(t.histLines)
	case tabRules:
		return len(t.cfg.Rules)
	case tabSettings:
		return 2 + len(t.checks)
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
}

func (t *tui) handleMain(k string) {
	n := t.rows()
	page := t.h - 6
	s := &t.sel[t.tab]
	switch k {
	case "q", "esc":
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
  q or Esc          quit                  ?              this help
Accounts
  a add   e edit   R rename   t test login   k public key   K use another key   d default
  p passphrase to agent   x remove (then d to also delete key files)
Repos
  Enter choose account   h history   c clone by URL   f add folder   a add one repo   r rescan
  p pin / unpin   o open folder   O open in editor   x remove from list (files untouched)
History
  o other repo   f only wrong-account commits   x fix not-pushed commits   r reload
Rules
  a add rule   x remove rule
Settings
  Enter toggle or fix the selected item
In forms
  Tab / ↑↓ next field   ←→ change choice   Enter next / save   Esc cancel   Ctrl+U clear field
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
		a.Name = t.app.SuggestedName()
		if a.Name == "" && len(t.cfg.Accounts) > 0 {
			a.Name = t.cfg.Accounts[0].Name
		}
	}
	keyOpts := []string{"Create a new key"}
	for _, k := range t.app.ExistingKeys() {
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
			if err := t.app.UpdateAccount(acc); err != nil {
				return err
			}
		} else if _, err := t.app.AddAccount(acc, app.KeyChoice{}); err != nil {
			return err
		}
		if newKey {
			var kerr error
			t.suspend(func() {
				fmt.Printf("Creating a key for %s. ssh-keygen asks for a passphrase (recommended; press Enter for none).\n\n", acc.ID)
				_, kerr = t.app.GenerateKey(acc.ID, true, "")
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
	pub, help, err := t.app.PublicKey(a.ID)
	if err != nil {
		t.say(err, "")
		return
	}
	copied := "c  copy to clipboard"
	if sys.CopyToClipboard(pub) {
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
			n, err := t.app.RenameAccount(old, v[0])
			if err == nil {
				t.say(nil, fmt.Sprintf("Renamed %s to %s (%d repo(s) updated).", old, v[0], n))
			}
			return err
		}}
	case "K":
		if a == nil {
			return
		}
		found := t.app.ExistingKeys()
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
			if err := t.app.UseKey(id, found[i].Path); err != nil {
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
		var r keys.TestResult
		t.suspend(func() {
			fmt.Printf("Testing %s on %s...\n", acc.ID, acc.Host)
			r, _ = t.app.TestAccount(acc.ID, false)
			if r.OK {
				fmt.Printf("\nConnected as %s.\n", r.User)
			} else {
				fmt.Printf("\nCould not log in:\n%s\n", r.Output)
			}
		})
		if r.OK {
			t.say(nil, acc.ID+" logs in as "+r.User+".")
		} else {
			t.say(fmt.Errorf("%s could not log in. Press k to see its public key", acc.ID), "")
		}
		t.reload()
	case "p":
		if a == nil {
			return
		}
		acc := *a
		var err error
		t.suspend(func() { err = t.app.AddToAgent(acc.ID) })
		t.say(err, "Key loaded; IDEs won't need the passphrase.")
	case "d":
		if a != nil {
			t.say(t.app.SetDefault(a.ID), a.ID+" is now the default.")
			t.reload()
		}
	case "x":
		if a != nil {
			id := a.ID
			acc := *a
			t.confirm = &confirmBox{"Remove " + id + " and its rules?  y = remove, keep key files · d = remove AND delete key files · any other key cancels",
				map[string]func() error{
					"y": func() error { t.sel[tabAccounts] = 0; _, err := t.app.RemoveAccount(id, false); return err },
					"d": func() error {
						t.sel[tabAccounts] = 0
						if _, err := t.app.RemoveAccount(id, true); err != nil {
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

func (t *tui) scanRepos() {
	t.repos = t.app.Repos()
	if gd := t.app.CurrentRepo(); gd != "" {
		found := false
		for _, r := range t.repos {
			if r.GitDir == gd {
				found = true
			}
		}
		if !found {
			t.repos = append([]repo.Info{t.app.RepoInfo(gd)}, t.repos...)
		}
	}
	t.scanned = true
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
				if err := t.app.AddScanFolder(v[0]); err != nil {
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
		opts := []string{"Ask (follow rules)"}
		for _, a := range t.cfg.Accounts {
			opts = append(opts, a.ID+"  "+a.Email)
		}
		gd := r.GitDir
		idx := i
		t.picker = &picker{title: "Account for " + filepath.Base(r.Path), options: opts, sel: indexOfPrefix(opts, r.Saved),
			pick: func(p int) error {
				if p == 0 {
					_, _ = t.app.SetRepoAccount(gd, "")
					t.say(nil, "This repo will follow rules or ask.")
				} else {
					a := t.cfg.Accounts[p-1]
					removed, err := t.app.SetRepoAccount(gd, a.ID)
					if err != nil {
						return err
					}
					msg := "This repo now uses " + a.ID + "."
					if removed != "" {
						msg += " Removed its own core.sshCommand that bypassed gitswitch."
					}
					t.say(nil, msg)
				}
				t.repos[idx] = t.app.RepoInfo(gd)
				return nil
			}}
	case "h":
		if r != nil && !r.Missing {
			t.histDir = r.GitDir
			t.loadHistory()
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
				dir = app.RepoName(v[0])
			}
			var ri repo.Info
			var err error
			t.suspend(func() { // git prints its progress straight to the terminal
				fmt.Println("Cloning " + v[0] + " ...")
				ri, err = t.app.CloneRepo(v[0], dir, v[2], func(l string) { fmt.Print("\r\x1b[K  " + l) })
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
				ri, err := t.app.AddRepo(v[0])
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
		t.say(t.app.PinRepo(path, on), map[bool]string{true: "Pinned.", false: "Unpinned."}[on])
		t.reload()
		t.scanRepos()
		for j, x := range t.repos { // keep the same repo selected after it moves
			if x.Path == path {
				t.sel[tabRepos] = j
			}
		}
	case "o", "O":
		if r != nil {
			t.say(t.app.OpenRepo(r.Path, k == "O"), "Opened "+filepath.Base(r.Path)+".")
		}
	case "x":
		if r == nil {
			return
		}
		path := r.Path
		t.confirm = &confirmBox{"Remove " + path + " from the list? Its files and git settings are not changed. (y/n)",
			map[string]func() error{"y": func() error {
				if err := t.app.RemoveRepo(path); err != nil {
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

func indexOfPrefix(opts []string, id string) int {
	if id == "" {
		return 0
	}
	for i, o := range opts {
		if strings.HasPrefix(o, id+"  ") {
			return i
		}
	}
	return 0
}

// ---------------- history ----------------

func (t *tui) loadHistory() {
	t.sel[tabHistory] = 0
	if t.histDir == "" {
		t.histLines = []string{}
		return
	}
	lines, h, err := t.app.HistoryLines(t.histDir, 300, t.flagged, true)
	if err != nil {
		t.histLines = []string{}
		t.say(err, "")
		return
	}
	t.histLines, t.hist = lines, h
}

func (t *tui) keyHistory(k string) {
	switch k {
	case "f":
		t.flagged = !t.flagged
		t.loadHistory()
	case "r":
		t.loadHistory()
	case "o":
		if !t.scanned {
			t.scanRepos()
		}
		if len(t.repos) == 0 {
			t.say(fmt.Errorf("no repos found; add a projects folder on the Repos page (key f)"), "")
			return
		}
		var opts []string
		for _, r := range t.repos {
			opts = append(opts, r.Path)
		}
		t.picker = &picker{title: "Show history for", options: opts, pick: func(i int) error {
			t.histDir = t.repos[i].GitDir
			t.loadHistory()
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
				n, err := t.app.FixAuthor(gd)
				if err == nil {
					t.loadHistory()
					t.say(nil, fmt.Sprintf("Fixed %d commit(s).", n))
				}
				return err
			}}}
	}
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
			if err := t.app.AddRule(account.Rule{Kind: kind, Pattern: v[1], Account: v[2]}); err != nil {
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
				map[string]func() error{"y": func() error { t.sel[tabRules] = 0; return t.app.RemoveRule(r) }}}
		}
	}
}

// ---------------- settings ----------------

func (t *tui) keySettings(k string) {
	if k != "enter" && k != " " {
		return
	}
	i := t.sel[tabSettings]
	switch {
	case i == 0:
		t.say(t.app.SetSetting("guiPrompt", fmt.Sprint(!t.cfg.GUIPrompt)), "Saved.")
	case i == 1:
		t.form = &form{title: "Prompt timeout", fields: []field{{label: "Seconds to wait before using the default", value: strconv.Itoa(t.cfg.PromptTimeout)}},
			submit: func(v []string) error {
				n, err := strconv.Atoi(v[0])
				if err != nil || n < 5 {
					return fmt.Errorf("enter a number of seconds, 5 or more")
				}
				return t.app.SetSetting("promptTimeout", v[0])
			}}
	default:
		c := t.checks[i-2]
		if c.CanFix {
			t.say(t.app.Fix(c.ID), "Fixed: "+c.Title)
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

func (t *tui) draw() {
	t.w, t.h = sys.TermSize()
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
	view := body - hdr
	if sel >= 0 {
		if sel < *o {
			*o = sel
		}
		if sel >= *o+view {
			*o = sel - view + 1
		}
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
	if t.msg != "" {
		if t.msgErr {
			status = cRed + " " + t.msg + cReset
		} else {
			status = cGreen + " " + t.msg + cReset
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
		return "↑↓ choose · Enter select · Esc cancel"
	}
	if t.confirm != nil {
		return "y yes · n no"
	}
	if t.view != nil {
		if t.view.copy != "" {
			return "c copy · o open link · Enter close"
		}
		return "Enter close"
	}
	switch t.tab {
	case tabAccounts:
		return "a add · e edit · R rename · t test · k public key · K pick key · d default · x remove · ? help · q quit"
	case tabRepos:
		return "Enter account · h history · c clone · a add repo · f add folder · p pin · o open · x remove · r rescan · ? help · q quit"
	case tabHistory:
		return "o other repo · f wrong-account only · x fix · r reload · ? help · q quit"
	case tabRules:
		return "a add · x remove · ? help · q quit"
	}
	return "Enter toggle / fix · ? help · q quit"
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
		if !keys.Exists(&a) {
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

func (t *tui) installed() bool { return t.app.Installed() }

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
	why := map[string]string{"saved": "saved", "owner-rule": "owner rule", "folder-rule": "folder rule", "only-account": "only account", "asks": "asks on next push", "": "no account for this host"}
	for i, r := range t.repos {
		on := i == t.sel[tabRepos]
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
		l = append(l, fmt.Sprintf("%s%s%-22s%s %s  %s  %s  %s", mark(on), star, clip(filepath.Base(r.Path), 22), cReset, cDim+padANSI(clip(branch, 16), 16)+cReset, padANSI(acct, 34), padANSI(email, 34), cDim+r.Remote+cReset))
	}
	return l, t.sel[tabRepos]
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
		l = append(l, "\x00 "+cBold+h.Repo.Path+cReset+"  account: "+acct+"  "+flag+cDim+fmt.Sprintf("  %d not pushed", h.Unpushed)+cReset+filter, "\x00")
	}
	sel := t.sel[tabHistory]
	for i, x := range t.histLines {
		if i == sel {
			l = append(l, cRev+" "+cReset+x)
		} else {
			l = append(l, " "+x)
		}
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

func (t *tui) drawSettings() ([]string, int) {
	s := t.sel[tabSettings]
	onoff := map[bool]string{true: cGreen + "on" + cReset, false: cDim + "off" + cReset}
	l := []string{
		fmt.Sprintf("%sPopup when git runs without a terminal (IDE source control): %s%s", mark(s == 0), onoff[t.cfg.GUIPrompt], cReset),
		fmt.Sprintf("%sSeconds to wait for an answer before using the default: %d%s", mark(s == 1), t.cfg.PromptTimeout, cReset),
	}
	for i, c := range t.checks {
		st := cGreen + "ok " + cReset
		if !c.OK {
			st = cRed + "FIX" + cReset
			if c.CanFix {
				st += cDim + " (Enter)" + cReset
			}
		}
		l = append(l, fmt.Sprintf("%s%s %s%s  %s%s", mark(s == i+2), st, c.Title, cReset, cDim, c.Detail+cReset))
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
		out = append(out, " └"+strings.Repeat("─", w)+"┘")
		return out
	}
	switch {
	case t.view != nil:
		var body []string
		for _, l := range t.view.lines {
			body = append(body, wrapPlain(l, min(t.w-8, 96))...)
		}
		return box(t.view.title, body)
	case t.confirm != nil:
		return box("Confirm", wrapPlain(t.confirm.text, min(t.w-8, 96)))
	case t.picker != nil:
		var body []string
		for i, o := range t.picker.options {
			body = append(body, mark(i == t.picker.sel)+o+cReset)
		}
		return box(t.picker.title, body)
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
