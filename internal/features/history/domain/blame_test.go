package domain

import (
	"strings"
	"testing"
)

const h1 = "1111111111111111111111111111111111111111"
const h2 = "2222222222222222222222222222222222222222"

func TestParseBlame(t *testing.T) {
	// h1 is a boundary commit holding lines 1-2; h2 (CRLF text) line 3,
	// then h1 again (line 4, not contiguous with its first group).
	out := h1 + " 1 1 2\nauthor Ana\nauthor-mail <ana@x.example>\nauthor-time 1700000000\nauthor-tz +0000\n" +
		"summary first\nboundary\nfilename old/name.txt\n\tone\n" +
		h1 + " 2 2\n\ttwo\n" +
		h2 + " 1 3 1\nauthor Bob B\nauthor-mail <bob@x.example>\nauthor-time 1700000100\nsummary second\nprevious " + h1 + " x\nfilename new/name.txt\n\tthree\r\n" +
		h1 + " 9 4 1\nfilename elsewhere.txt\n\tfour\n"
	lines, trunc, err := ParseBlame(strings.NewReader(out), 100)
	if err != nil || trunc || len(lines) != 4 {
		t.Fatalf("lines=%v trunc=%v err=%v", lines, trunc, err)
	}
	if l := lines[0]; l.Line != 1 || l.Hash != h1 || l.Short != "1111111" || l.Author != "Ana" || l.Email != "ana@x.example" || !l.Boundary || l.OrigPath != "old/name.txt" || l.Summary != "first" || l.Text != "one" || l.Time != 1700000000 {
		t.Errorf("line 1: %+v", l)
	}
	if l := lines[1]; l.Author != "Ana" || l.OrigPath != "old/name.txt" || l.OrigLine != 2 || !l.Boundary {
		t.Errorf("line 2 (no repeated info): %+v", l)
	}
	if l := lines[2]; l.Text != "three" || l.Author != "Bob B" || l.Boundary || l.OrigPath != "new/name.txt" {
		t.Errorf("line 3 (CRLF): %+v", l)
	}
	if l := lines[3]; l.OrigPath != "elsewhere.txt" || l.OrigLine != 9 || l.Author != "Ana" {
		t.Errorf("line 4 (copied from another file): %+v", l)
	}
	ch := BlameChunks(lines)
	if len(ch) != 3 || ch[0].Count != 2 || ch[0].StartLine != 1 || ch[1].Hash != h2 || ch[2].StartLine != 4 {
		t.Errorf("chunks: %+v", ch)
	}
}

func TestParseBlameTruncates(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5; i++ {
		if i == 1 {
			b.WriteString(h1 + " 1 1 5\nauthor A\nauthor-mail <a@x>\nauthor-time 1\nsummary s\nfilename f\n")
		} else {
			b.WriteString(h1 + " " + string(rune('0'+i)) + " " + string(rune('0'+i)) + "\n")
		}
		b.WriteString("\tx\n")
	}
	lines, trunc, err := ParseBlame(strings.NewReader(b.String()), 3)
	if err != nil || !trunc || len(lines) != 3 {
		t.Fatalf("%d lines, trunc=%v, err=%v", len(lines), trunc, err)
	}
	if _, trunc, _ := ParseBlame(strings.NewReader(b.String()), 5); trunc {
		t.Error("exactly max lines is not truncated")
	}
}

func TestParseFileLog(t *testing.T) {
	out := "\x1e" + h2 + "\x1fb@x\x1fBob\x1f200\x1fedit\n\nM\tnew.txt\n" +
		"\x1e" + h1 + "\x1fa@x\x1fAna\x1f100\x1frename it\n\nR087\told.txt\tnew.txt\n" +
		"\x1e" + "3333333333333333333333333333333333333333\x1fa@x\x1fAna\x1f50\x1fcreate\n\nA\told.txt\n"
	cs := ParseFileLog(out, "new.txt")
	if len(cs) != 3 {
		t.Fatalf("%+v", cs)
	}
	if c := cs[0]; c.Status != "M" || c.Path != "new.txt" || c.Subject != "edit" {
		t.Errorf("%+v", c)
	}
	if c := cs[1]; c.Status != "R" || c.Similarity != 87 || c.OldPath != "old.txt" || c.Path != "new.txt" {
		t.Errorf("%+v", c)
	}
	if c := cs[2]; c.Status != "A" || c.Path != "old.txt" {
		t.Errorf("%+v", c)
	}
}

func TestCheckPickaxe(t *testing.T) {
	if (Filter{Pickaxe: "x"}).CheckPickaxe() != nil || (Filter{}).CheckPickaxe() != nil {
		t.Error("plain text is fine")
	}
	if (Filter{Pickaxe: strings.Repeat("a", MaxPickaxe+1)}).CheckPickaxe() == nil {
		t.Error("too long")
	}
	if !(Filter{Pickaxe: "x"}).Narrows() {
		t.Error("a code search hides commits")
	}
}
