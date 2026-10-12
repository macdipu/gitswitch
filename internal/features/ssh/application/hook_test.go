package application

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
)

type fakeStore struct {
	account.Store
	c       account.Config
	users   map[string]string
	loadErr error
}

func (f *fakeStore) Load() *account.Config { c := f.c; return &c }
func (f *fakeStore) LoadChecked() (*account.Config, error) {
	c := f.c
	return &c, f.loadErr
}
func (f *fakeStore) SetUser(id, user string) error {
	f.users[id] = user
	return nil
}

type fakeIdentity struct {
	account.RepoAccounts
	applied []string // "gitDir=account"
}

func (f *fakeIdentity) ApplyIdentity(gitDir string, a *account.Account) {
	f.applied = append(f.applied, gitDir+"="+a.ID)
}

type fakeRepos struct {
	gitDir  string
	saved   map[string]string
	probed  bool
	saveErr error
}

func (f *fakeRepos) CurrentGitDir(ssh.Invocation) string { return f.gitDir }
func (f *fakeRepos) RememberProbe()                      { f.probed = true }
func (f *fakeRepos) SavedAccount(gitDir string) string   { return f.saved[gitDir] }
func (f *fakeRepos) SaveAccount(gitDir, id string) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved[gitDir] = id
	return nil
}
func (f *fakeRepos) Worktree(gitDir string) string { return strings.TrimSuffix(gitDir, "/.git") }

type fakeTransport struct {
	policy string
	ran    []string
}

func (f *fakeTransport) Find(override string) string           { return "ssh" }
func (f *fakeTransport) HostKeyPolicy(string, []string) string { return f.policy }
func (f *fakeTransport) Exec(bin string, args []string) int {
	f.ran = append(f.ran, strings.Join(args, " "))
	return 7
}

// fakePrompter answers with answer; term=false means git runs without a
// terminal.
type fakePrompter struct {
	term     bool
	answer   *resolve.Decision
	asked    int
	dialog   int
	locked   int    // Lock calls not yet released
	onLock   func() // runs while waiting for the lock (another process saving)
	maxLocks int
}

func (f *fakePrompter) Lock(int) func() {
	if f.onLock != nil {
		f.onLock()
	}
	f.locked++
	if f.locked > f.maxLocks {
		f.maxLocks = f.locked
	}
	done := false
	return func() {
		if !done {
			done = true
			f.locked--
		}
	}
}

func (f *fakePrompter) OpenTerminal() ssh.Conversation {
	if !f.term {
		return nil
	}
	return conv{f}
}
func (f *fakePrompter) Dialog(string, []account.Account, *account.Account, int) *resolve.Decision {
	f.dialog++
	return f.answer
}

type conv struct{ p *fakePrompter }

func (c conv) Ask(string, []account.Account, *account.Account, int) *resolve.Decision {
	c.p.asked++
	return c.p.answer
}
func (c conv) Close() {}

var (
	work   = account.Account{ID: "work", Host: "github.com", Key: "/keys/work"}
	me     = account.Account{ID: "me", Host: "github.com", Key: "/keys/me"}
	gitArg = []string{"git@github.com", "git-upload-pack 'acme/api.git'"}
)

func newHook() (*Hook, *fakeStore, *fakeIdentity, *fakeRepos, *fakeTransport, *fakePrompter, *bytes.Buffer) {
	st := &fakeStore{c: account.Config{Default: "me", Accounts: []account.Account{work, me}}, users: map[string]string{}}
	id := &fakeIdentity{}
	r := &fakeRepos{gitDir: "/src/api/.git", saved: map[string]string{}}
	tr := &fakeTransport{}
	p := &fakePrompter{}
	notice := &bytes.Buffer{}
	return &Hook{Accounts: st, Identity: id, Repos: r, Transport: tr, Prompter: p, Notice: notice}, st, id, r, tr, p, notice
}

func TestHookPassesThroughWhatItDoesNotManage(t *testing.T) {
	h, _, id, r, tr, _, _ := newHook()
	probe := []string{"-G", "git@github.com"}
	if code := h.Run(probe); code != 7 || !r.probed || tr.ran[0] != "-G git@github.com" {
		t.Errorf("probe: code=%d probed=%v ran=%q", code, r.probed, tr.ran)
	}
	other := []string{"git@example.org", "git-upload-pack 'x.git'"}
	h.Run(other)
	if tr.ran[1] != strings.Join(other, " ") || len(id.applied) != 0 {
		t.Errorf("unmanaged host must run plain ssh: ran=%q applied=%v", tr.ran, id.applied)
	}
}

func TestHookUsesTheSavedAccount(t *testing.T) {
	h, _, id, r, tr, p, notice := newHook()
	r.saved["/src/api/.git"] = "work"
	p.term = true
	h.Run(gitArg)
	if p.asked != 0 {
		t.Error("a saved account must not prompt")
	}
	if len(id.applied) != 1 || id.applied[0] != "/src/api/.git=work" {
		t.Errorf("applied = %v", id.applied)
	}
	if !strings.HasPrefix(tr.ran[0], "-i /keys/work -o IdentitiesOnly=yes") || !strings.HasSuffix(tr.ran[0], strings.Join(gitArg, " ")) {
		t.Errorf("ssh args = %q", tr.ran[0])
	}
	if notice.Len() != 0 {
		t.Errorf("unexpected notice: %q", notice.String())
	}
}

func TestHookAsksOnTheTerminalAndSaves(t *testing.T) {
	h, _, id, r, tr, p, notice := newHook()
	p.term = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Prompt, Save: true}
	h.Run(gitArg)
	if p.asked != 1 || r.saved["/src/api/.git"] != "work" || id.applied[0] != "/src/api/.git=work" {
		t.Errorf("asked=%d saved=%v applied=%v", p.asked, r.saved, id.applied)
	}
	if !strings.Contains(notice.String(), "work saved for this repo") {
		t.Errorf("notice = %q", notice.String())
	}
	if strings.Contains(tr.ran[0], "accept-new") {
		t.Error("with a terminal, ssh can ask about new hosts itself")
	}
}

func TestHookWithoutATerminalFallsBackToTheDefault(t *testing.T) {
	h, _, id, r, tr, p, notice := newHook()
	tr.policy = "ask"
	h.Run(gitArg)
	if p.dialog != 0 {
		t.Error("the dialog is off unless gitswitch.guiPrompt is set")
	}
	if len(id.applied) != 0 || len(r.saved) != 0 {
		t.Errorf("a default nobody chose must not become the repo's identity: applied=%v saved=%v", id.applied, r.saved)
	}
	if !strings.HasPrefix(tr.ran[0], "-i /keys/me ") {
		t.Errorf("the default account's key must still be used: %q", tr.ran[0])
	}
	if !strings.Contains(notice.String(), `using default "me"`) {
		t.Errorf("notice = %q", notice.String())
	}
	if !strings.Contains(tr.ran[0], "StrictHostKeyChecking=accept-new") {
		t.Errorf("without a terminal a new host must be accepted: %q", tr.ran[0])
	}
}

func TestHookUsesTheDialogWhenEnabled(t *testing.T) {
	h, st, id, _, _, p, _ := newHook()
	st.c.GUIPrompt = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Dialog}
	h.Run(gitArg)
	if p.dialog != 1 || id.applied[0] != "/src/api/.git=work" {
		t.Errorf("dialog=%d applied=%v", p.dialog, id.applied)
	}
}

func TestHookStopsWhenSettingsCannotBeRead(t *testing.T) {
	h, st, id, _, tr, _, notice := newHook()
	st.loadErr = errors.New("cannot read your git settings: fatal: bad config line 3")
	if code := h.Run(gitArg); code != 255 {
		t.Errorf("code = %d, want 255", code)
	}
	if len(tr.ran) != 0 || len(id.applied) != 0 {
		t.Errorf("must not connect: ran=%q applied=%v", tr.ran, id.applied)
	}
	if !strings.Contains(notice.String(), "bad config line 3") {
		t.Errorf("notice = %q", notice.String())
	}
	if h.Run([]string{"-G", "git@github.com"}); len(tr.ran) != 1 {
		t.Error("git's -G probe never connects, so it still runs")
	}
}

func TestHookNeverAsksInBatchMode(t *testing.T) {
	h, st, id, _, tr, p, _ := newHook()
	p.term = true
	st.c.GUIPrompt = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Prompt}
	h.Run(append([]string{"-o", "BatchMode=yes"}, gitArg...))
	// The default's key is used for this connection, but nobody chose it,
	// so it does not become the repo's identity.
	if p.asked != 0 || p.dialog != 0 || len(id.applied) != 0 || !strings.HasPrefix(tr.ran[0], "-i /keys/me ") {
		t.Errorf("asked=%d dialog=%d applied=%v ran=%q", p.asked, p.dialog, id.applied, tr.ran)
	}
}

func TestHookUsesAnAnswerSavedWhileWaitingToAsk(t *testing.T) {
	h, _, id, r, _, p, _ := newHook()
	p.term = true
	p.answer = &resolve.Decision{Account: &me, Reason: resolve.Prompt}
	p.onLock = func() { r.saved["/src/api/.git"] = "work" } // the other gitswitch saved "always"
	h.Run(gitArg)
	if p.asked != 0 || id.applied[0] != "/src/api/.git=work" {
		t.Errorf("asked=%d applied=%v", p.asked, id.applied)
	}
	if p.maxLocks != 1 || p.locked != 0 {
		t.Errorf("lock taken %d, still held %d", p.maxLocks, p.locked)
	}
}

func TestHookSaysWhenItCannotSave(t *testing.T) {
	h, _, _, r, _, p, notice := newHook()
	p.term = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Prompt, Save: true}
	r.saveErr = errors.New("permission denied")
	h.Run(gitArg)
	if !strings.Contains(notice.String(), "could not save work") || !strings.Contains(notice.String(), "permission denied") {
		t.Errorf("notice = %q", notice.String())
	}

	h, _, _, r, _, p, notice = newHook()
	p.term = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Prompt, Save: true}
	r.gitDir = ""
	h.Run(gitArg)
	if !strings.Contains(notice.String(), "no repo found to save work") {
		t.Errorf("notice = %q", notice.String())
	}
}

func TestSSHArgsWithoutAKey(t *testing.T) {
	got := strings.Join(SSHArgs(&account.Account{ID: "x"}, []string{"host"}, false), " ")
	if strings.Contains(got, "-i ") || !strings.HasPrefix(got, "-o AddKeysToAgent=yes") || !strings.HasSuffix(got, " host") {
		t.Errorf("args = %q", got)
	}
}

// With no default account, an answer is used (and saved only when the
// user ticked it), and with nobody to answer git does not connect at all.
func TestHookWithNoDefault(t *testing.T) {
	h, st, id, r, tr, p, _ := newHook()
	st.c.Default = ""
	p.term = true
	p.answer = &resolve.Decision{Account: &work, Reason: resolve.Prompt}
	h.Run(gitArg)
	if len(r.saved) != 0 || len(id.applied) != 1 || len(tr.ran) != 1 || !strings.HasPrefix(tr.ran[0], "-i /keys/work ") {
		t.Fatalf("an unticked answer is used, not saved: saved=%v applied=%v ran=%v", r.saved, id.applied, tr.ran)
	}

	h, st, id, r, tr, p, notice := newHook()
	st.c.Default = ""
	if code := h.Run(gitArg); code != 255 || len(tr.ran) != 0 {
		t.Fatalf("no answer and no default: code=%d ran=%v, want 255 and no ssh", code, tr.ran)
	}
	if len(r.saved) != 0 || len(id.applied) != 0 || p.locked != 0 {
		t.Errorf("saved=%v applied=%v locked=%d", r.saved, id.applied, p.locked)
	}
	if !strings.Contains(notice.String(), "gitswitch use <account>") {
		t.Errorf("notice = %q", notice.String())
	}
}
