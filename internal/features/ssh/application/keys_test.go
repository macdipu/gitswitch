package application

import (
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
)

type fakeKeyTool struct {
	ssh.KeyTool
	agent  map[string]bool
	result ssh.TestResult
}

func (f *fakeKeyTool) AgentFingerprints() (map[string]bool, bool) { return f.agent, f.agent != nil }
func (f *fakeKeyTool) Fingerprint(a *account.Account) string {
	if a.Key == "" {
		return ""
	}
	return "SHA256:" + a.ID
}
func (f *fakeKeyTool) Test(*account.Account, string, bool) ssh.TestResult { return f.result }

func TestAgentStatus(t *testing.T) {
	st := &fakeStore{c: account.Config{Accounts: []account.Account{work, me, {ID: "nokey"}}}}
	k := &Keys{Accounts: st, Tool: &fakeKeyTool{}}
	if loaded, ok := k.AgentStatus(); ok || len(loaded) != 0 {
		t.Errorf("no agent: loaded=%v ok=%v", loaded, ok)
	}
	k.Tool = &fakeKeyTool{agent: map[string]bool{"SHA256:work": true, "SHA256:nokey": true}}
	loaded, ok := k.AgentStatus()
	if !ok || len(loaded) != 1 || !loaded["work"] {
		t.Errorf("loaded=%v ok=%v", loaded, ok)
	}
}

func TestTestRemembersTheUsername(t *testing.T) {
	st := &fakeStore{c: account.Config{Accounts: []account.Account{work}}, users: map[string]string{}}
	tool := &fakeKeyTool{result: ssh.TestResult{OK: false, User: "octo"}}
	k := &Keys{Accounts: st, Tool: tool}
	if _, err := k.Test("work", true); err != nil || len(st.users) != 0 {
		t.Errorf("a failed login must not save a username: err=%v users=%v", err, st.users)
	}
	tool.result.OK = true
	if r, err := k.Test("WORK", true); err != nil || !r.OK || st.users["work"] != "octo" {
		t.Errorf("r=%+v err=%v users=%v", r, err, st.users)
	}
	if _, err := k.Test("ghost", true); err == nil {
		t.Error("an unknown account must fail")
	}
}
