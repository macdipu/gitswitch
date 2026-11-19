package domain

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSignatures(t *testing.T) {
	out := "aaa111\x1fG\x1fme@x.example\x1fSHA256:abc\x1e\nbbb222\x1fN\x1f\x1f\x1e\nccc333\x1fE\x1f\x1fSHA256:def\x1e\nddd444\x1fB\x1f\x1f\x1e"
	m := ParseSignatures(out)
	if len(m) != 4 {
		t.Fatalf("%v", m)
	}
	if g := m["aaa111"]; g.Status != "G" || !g.Verified || g.Signer != "me@x.example" || g.Key != "SHA256:abc" || g.Label != "Verified signature" {
		t.Errorf("good = %+v", g)
	}
	if n := m["bbb222"]; n.Status != "N" || n.Verified || n.Label != "Not signed" {
		t.Errorf("none = %+v", n)
	}
	if e := m["ccc333"]; e.Status != "E" || e.Verified || !strings.Contains(e.Label, "cannot be checked") {
		t.Errorf("cannot check = %+v", e)
	}
	if m["ddd444"].Status != "B" || m["ddd444"].Verified {
		t.Error("bad")
	}
	for _, c := range "UXYR" {
		if SignatureLabel(string(c)) == "Not signed" || SignatureLabel(string(c)) == "" {
			t.Errorf("no words for %c", c)
		}
	}
}

func TestCleanSignatureHashes(t *testing.T) {
	got := CleanSignatureHashes([]string{"ABC123", "abc123", " def4 ", "zzz", "ab", "", strings.Repeat("a", 65), "1a2b3c4"})
	if strings.Join(got, ",") != "abc123,def4,1a2b3c4" {
		t.Errorf("got %v", got)
	}
	var many []string
	for i := 0; i < 500; i++ {
		many = append(many, fmt.Sprintf("%08x", i+1))
	}
	if got := CleanSignatureHashes(many); len(got) != MaxSignatureHashes || got[0] != "00000001" {
		t.Errorf("cap: %d", len(got))
	}
}
