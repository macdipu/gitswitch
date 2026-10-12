package prompt

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
)

var cands = []account.Account{{ID: "me", Email: "me@x.com"}, {ID: "work", Email: "w@x.com"}}

func TestAskPicksByNumberNameOrEnter(t *testing.T) {
	for in, want := range map[string]string{"\n": "me", "2\n": "work", "WORK\n": "work", "1!\n": "me"} {
		var out bytes.Buffer
		d := ask(strings.NewReader(in), &out, "acme/api", cands, &cands[0], time.Second)
		if d.Account.ID != want || d.Reason != resolve.Prompt || d.Save != strings.Contains(in, "!") {
			t.Errorf("%q: got %+v", in, d)
		}
	}
}

func TestAskAsksAgainAfterATypo(t *testing.T) {
	var out bytes.Buffer
	d := ask(strings.NewReader("3\nwrok!\nwork!\n"), &out, "acme/api", cands, &cands[0], time.Second)
	if d.Account.ID != "work" || !d.Save || d.Reason != resolve.Prompt {
		t.Fatalf("got %+v", d)
	}
	if n := strings.Count(out.String(), "is not a choice"); n != 2 {
		t.Errorf("want 2 retries, got %d:\n%s", n, out.String())
	}
}

func TestAskWithoutAnAnswerIsNotAChoice(t *testing.T) {
	var out bytes.Buffer
	if d := ask(strings.NewReader("oops\n"), &out, "acme/api", cands, &cands[0], time.Second); d.Account.ID != "me" || d.Reason != resolve.Default {
		t.Errorf("end of input: got %+v", d)
	}
	r, w := io.Pipe()
	defer w.Close()
	if d := ask(r, &out, "acme/api", cands, &cands[0], 50*time.Millisecond); d.Account.ID != "me" || d.Reason != resolve.Default {
		t.Errorf("timeout: got %+v", d)
	}
}
