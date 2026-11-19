package process

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	for in, want := range map[string][]string{
		`code --new-window`: {"code", "--new-window"},
		`  code   -w `:      {"code", "-w"},
		`"C:\Program Files\Meld\meld.exe" {left}`: {`C:\Program Files\Meld\meld.exe`, "{left}"},
		`tool 'a b' "c \"d\""`:                    {"tool", "a b", `c "d"`},
		`tool ""`:                                 {"tool", ""},
		``:                                        nil,
	} {
		got, err := SplitCommand(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := SplitCommand(`tool "open`); err == nil {
		t.Error("an unclosed quote must be refused")
	}
}

func TestExpandKeepsValuesWhole(t *testing.T) {
	got := Expand([]string{"d", "{left}", "--x={path}"}, map[string]string{"left": "/a b/c; rm -rf", "path": "p q"})
	want := []string{"d", "/a b/c; rm -rf", "--x=p q"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if !Uses([]string{"x{base}"}, "base") || Uses([]string{"x"}, "base") {
		t.Error("Uses")
	}
}

func TestTerminalFor(t *testing.T) {
	none := func(string) bool { return false }
	only := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		goos, cmd string
		have      func(string) bool
		want      terminalLaunch
	}{
		{"darwin", "", none, terminalLaunch{"open", []string{"-a", "Terminal", "/r p"}}},
		{"darwin", "iTerm2", none, terminalLaunch{"open", []string{"-a", "iTerm", "/r p"}}},
		{"darwin", "Warp", none, terminalLaunch{"open", []string{"-a", "Warp", "/r p"}}},
		{"darwin", "kitty", only("kitty"), terminalLaunch{"kitty", []string{}}},
		{"darwin", `alacritty --working-directory {dir}`, none, terminalLaunch{"alacritty", []string{"--working-directory", "/r p"}}},
		{"linux", "", only("konsole", "xterm"), terminalLaunch{"konsole", []string{"--workdir", "/r p"}}},
		{"linux", "", only("gnome-terminal"), terminalLaunch{"gnome-terminal", []string{"--working-directory=/r p"}}},
		{"linux", "", only("xterm"), terminalLaunch{"xterm", nil}},
		{"windows", "", only("wt"), terminalLaunch{"wt", []string{"-d", "/r p"}}},
		{"windows", "", none, terminalLaunch{"cmd", []string{"/c", "start", "", "/D", "/r p", "cmd"}}},
	}
	for _, c := range cases {
		got, err := terminalFor(c.goos, "/r p", c.cmd, c.have)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q: got %+v, %v; want %+v", c.goos, c.cmd, got, err, c.want)
		}
	}
	if _, err := terminalFor("linux", "/r", "", none); err == nil || !strings.Contains(err.Error(), "set terminal") {
		t.Errorf("no terminal: %v", err)
	}
	if _, err := terminalFor("linux", "/r", `x "`, none); err == nil {
		t.Error("bad quoting must fail")
	}
}

func TestSpawnNoSystemChangesCallsDone(t *testing.T) {
	t.Setenv("GITSWITCH_TEST_NO_SYSTEM", "1")
	called := false
	if err := Spawn("/", "definitely-not-a-program", nil, func() { called = true }); err != nil || !called {
		t.Fatalf("%v %v", err, called)
	}
	if err := OpenTerminal(t.TempDir(), "x-term {dir}"); err != nil {
		t.Fatal(err)
	}
}
