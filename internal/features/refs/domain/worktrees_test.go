package domain

import "testing"

func TestParseWorktrees(t *testing.T) {
	out := "worktree /r/main\nHEAD aaaaaaaabbbbbbbb\nbranch refs/heads/main\n\n" +
		"worktree /r/feat\nHEAD ccccccccdddddddd\nbranch refs/heads/feature/x\nlocked on a usb drive\n\n" +
		"worktree /r/det\nHEAD 1234567890abcdef\ndetached\nprunable gitdir file points to non-existent location\n\n" +
		"worktree /r/lockedplain\nHEAD 1234567\nlocked\n"
	w := ParseWorktrees(out)
	if len(w) != 4 {
		t.Fatalf("got %d worktrees: %+v", len(w), w)
	}
	if !w[0].Main || w[0].Branch != "main" || w[0].Short != "aaaaaaa" || w[1].Main {
		t.Errorf("main: %+v", w[:2])
	}
	if !w[1].Locked || w[1].LockReason != "on a usb drive" || w[1].Branch != "feature/x" {
		t.Errorf("locked: %+v", w[1])
	}
	if !w[2].Detached || w[2].Branch != "" || !w[2].Prunable || w[2].PruneReason == "" {
		t.Errorf("detached prunable: %+v", w[2])
	}
	if !w[3].Locked || w[3].LockReason != "" || w[3].Short != "1234567" {
		t.Errorf("locked without a reason: %+v", w[3])
	}
	b := ParseWorktrees("worktree /srv/r.git\nbare\n\nworktree /srv/wt\nHEAD abc\nbranch refs/heads/m\n")
	if len(b) != 2 || !b[0].Bare || !b[0].Main || b[1].Bare {
		t.Errorf("bare: %+v", b)
	}
	if len(ParseWorktrees("")) != 0 {
		t.Error("empty output has worktrees")
	}
	if got := ParseWorktrees("worktree C:/r\r\nHEAD abc\r\nbranch refs/heads/m\r\n\r\n"); len(got) != 1 || got[0].Path != "C:/r" || got[0].Branch != "m" {
		t.Errorf("CRLF: %+v", got)
	}
}
