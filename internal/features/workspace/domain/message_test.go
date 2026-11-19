package domain

import (
	"strings"
	"testing"
	"time"
)

func TestParsePerson(t *testing.T) {
	for in, want := range map[string]Person{
		"Ada Lovelace <ada@example.com>":   {"Ada Lovelace", "ada@example.com"},
		"  Ada   <ada@example.com>  ":      {"Ada", "ada@example.com"},
		"Ada<ada@example.com>":             {"Ada", "ada@example.com"},
		"José Ñ. O'Neil <j+x@a.b.example>": {"José Ñ. O'Neil", "j+x@a.b.example"},
	} {
		if got, err := ParsePerson(in); err != nil || got != want {
			t.Errorf("ParsePerson(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "Ada", "ada@example.com", "<ada@example.com>", "Ada <ada>", "Ada <a b@c.d>",
		"Ada <ada@example.com> x", "Ada\nB <a@b.c>", "Ada <a@b@c>", "A <b <a@b.c>"} {
		if p, err := ParsePerson(in); err == nil || !strings.Contains(err.Error(), "Name <email>") {
			t.Errorf("ParsePerson(%q) = %+v, %v; want refused", in, p, err)
		}
	}
	if s := (Person{"Ada", "a@b.c"}).String(); s != "Ada <a@b.c>" {
		t.Errorf("String = %q", s)
	}
}

func TestParseTrailers(t *testing.T) {
	for in, want := range map[string][]Trailer{
		"Signed-off-by: A <a@b.c>":              {{"Signed-off-by", "A <a@b.c>"}},
		"Fixes: #1\nCo-authored-by: B <b@c.d>":  {{"Fixes", "#1"}, {"Co-authored-by", "B <b@c.d>"}},
		"Note: long\n  goes on\nAcked-by:  X  ": {{"Note", "long\n  goes on"}, {"Acked-by", "X"}},
		"Key :value":                            {{"Key", "value"}},
	} {
		got, ok := ParseTrailers(in)
		if !ok || len(got) != len(want) {
			t.Errorf("ParseTrailers(%q) = %+v, %v", in, got, ok)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("ParseTrailers(%q)[%d] = %+v, want %+v", in, i, got[i], want[i])
			}
		}
	}
	for _, in := range []string{"", "Just words", "Fixes: #1\nand prose", "  indented: first", "Two words: no", "Key:"} {
		if got, ok := ParseTrailers(in); ok {
			t.Errorf("ParseTrailers(%q) = %+v, want not a block", in, got)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	so := Trailer{SignedOffBy, "Me <me@x.example>"}
	co := Trailer{CoAuthoredBy, "Ada <ada@x.example>"}
	for _, c := range []struct {
		name, summary, desc string
		trailers            []Trailer
		want                string
	}{
		{"summary only", " Fix login ", "", nil, "Fix login"},
		{"with body", "Fix login", "\nThe form lost the email.\n", nil, "Fix login\n\nThe form lost the email."},
		{"empty", "  ", "\n", []Trailer{so}, ""},
		{"trailers after summary", "Fix", "", []Trailer{co, so}, "Fix\n\nCo-authored-by: Ada <ada@x.example>\nSigned-off-by: Me <me@x.example>"},
		{"one blank line before the block", "Fix", "Body.\n\n\n", []Trailer{so}, "Fix\n\nBody.\n\nSigned-off-by: Me <me@x.example>"},
		{"keeps the description's trailers first", "Fix", "Body.\n\nFixes: #12", []Trailer{so},
			"Fix\n\nBody.\n\nFixes: #12\nSigned-off-by: Me <me@x.example>"},
		{"no duplicate", "Fix", "Body.\n\nsigned-off-by:  me <ME@x.example>", []Trailer{so, so, co, co},
			"Fix\n\nBody.\n\nsigned-off-by: me <ME@x.example>\nCo-authored-by: Ada <ada@x.example>"},
		{"a prose paragraph is not a block", "Fix", "Body: with a colon\nand more", []Trailer{so},
			"Fix\n\nBody: with a colon\nand more\n\nSigned-off-by: Me <me@x.example>"},
		{"multi-line summary", "Fix\n\nBody.", "More.", nil, "Fix\n\nBody.\n\nMore."},
		{"description only", "", "Fix\nBody", nil, "Fix\n\nBody"},
		{"windows newlines", "Fix", "Body.\r\n\r\nFixes: #1\r\n", []Trailer{so}, "Fix\n\nBody.\n\nFixes: #1\nSigned-off-by: Me <me@x.example>"},
		{"summary then trailer line", "Fix\nSigned-off-by: Me <me@x.example>", "", []Trailer{so}, "Fix\n\nSigned-off-by: Me <me@x.example>"},
	} {
		if got := BuildMessage(c.summary, c.desc, c.trailers); got != c.want {
			t.Errorf("%s: BuildMessage = %q, want %q", c.name, got, c.want)
		}
	}
	if got := WithTrailers("Fix\n\nBody", []Trailer{so}); got != "Fix\n\nBody\n\nSigned-off-by: Me <me@x.example>" {
		t.Errorf("WithTrailers = %q", got)
	}
}

func TestSplitMessage(t *testing.T) {
	s, b, ts := SplitMessage("Fix\n\nBody one.\n\nBody two.\n\nFixes: #1\nAcked-by: X")
	if s != "Fix" || b != "Body one.\n\nBody two." || len(ts) != 2 || ts[1].Key != "Acked-by" {
		t.Errorf("SplitMessage = %q %q %+v", s, b, ts)
	}
	if s, b, ts := SplitMessage("Fix"); s != "Fix" || b != "" || ts != nil {
		t.Errorf("SplitMessage(summary) = %q %q %+v", s, b, ts)
	}
}

func TestTrailers(t *testing.T) {
	me := Person{"Me", "me@x.example"}
	ts, err := Trailers(me, true, []string{"Ada <ada@x.example>", " ", "Bob <bob@x.example>"})
	if err != nil || len(ts) != 3 || ts[0].String() != "Co-authored-by: Ada <ada@x.example>" || ts[2].String() != "Signed-off-by: Me <me@x.example>" {
		t.Fatalf("Trailers = %+v, %v", ts, err)
	}
	if ts, err := Trailers(me, false, nil); err != nil || len(ts) != 0 {
		t.Errorf("none = %+v, %v", ts, err)
	}
	for name, c := range map[string]struct {
		me   Person
		so   bool
		co   []string
		want string
	}{
		"bad co-author": {me, false, []string{"Ada"}, "Name <email>"},
		"myself":        {me, false, []string{"Also Me <ME@x.example>"}, "is you"},
		"no identity":   {Person{}, true, nil, "name and email"},
		"no name":       {Person{Email: "a@b.c"}, true, nil, "name and email"},
	} {
		if _, err := Trailers(c.me, c.so, c.co); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

func TestRecentMessages(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC) }
	got := RecentMessages([]Recent{
		{Message: "Older", Time: at(1)},
		{Message: "Fix\n\nBody\n\nSigned-off-by: Me <me@x>\nCo-authored-by: A <a@x>", Time: at(5)},
		{Message: "Fix\n\nBody", Time: at(4)},
		{Message: "  ", Time: at(6)},
		{Message: "Keep\n\nFixes: #1\nSigned-off-by: Me <me@x>", Time: at(3)},
		{Message: "Oldest", Time: at(0)},
	}, 3)
	want := []Recent{{"Fix", "Fix\n\nBody", at(5)}, {"Keep", "Keep\n\nFixes: #1", at(3)}, {"Older", "Older", at(1)}}
	if len(got) != len(want) {
		t.Fatalf("RecentMessages = %+v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := RecentMessages(nil, 10); got == nil || len(got) != 0 {
		t.Errorf("none = %#v", got)
	}
}

func TestCoAuthors(t *testing.T) {
	got := CoAuthors([]Person{
		{"Ada L", "ada@x"}, {"Me", "me@x"}, {"Bob", "bob@x"}, {"Ada", "ADA@x"}, {"Work me", "me@work"},
		{"Cy", "cy@x"}, {"Bob", "bob@x"}, {"", "noname@x"}, {"No email", ""},
	}, []string{"Me@x", "me@work"})
	want := []CoAuthor{{"Ada L", "ada@x", 2}, {"Bob", "bob@x", 2}, {"Cy", "cy@x", 1}}
	if len(got) != len(want) {
		t.Fatalf("CoAuthors = %+v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSignoffSetting(t *testing.T) {
	for _, c := range []struct {
		global, repo string
		want         SignoffSetting
		describe     string
	}{
		{"", "", SignoffSetting{}, "off (every repo)"},
		{"true", "", SignoffSetting{Mode: true, Global: true}, "on (every repo)"},
		{"true", "false", SignoffSetting{Mode: false, Repo: "off", Global: true}, "off (this repo)"},
		{"maybe", "on", SignoffSetting{Mode: true, Repo: "on"}, "on (this repo)"},
	} {
		got := NewSignoffSetting(c.global, c.repo)
		if got != c.want || got.Describe() != c.describe {
			t.Errorf("NewSignoffSetting(%q, %q) = %+v %q", c.global, c.repo, got, got.Describe())
		}
	}
	for in, want := range map[string]string{"on": "true", "Yes": "true", "1": "true", "off": "false", "FALSE": "false"} {
		if v, ok := ParseOnOff(in); !ok || v != want {
			t.Errorf("ParseOnOff(%q) = %q %v", in, v, ok)
		}
	}
	if _, ok := ParseOnOff("sometimes"); ok {
		t.Error("ParseOnOff accepted sometimes")
	}
}
