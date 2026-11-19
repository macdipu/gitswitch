package process

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// SplitCommand splits a command line such as `code --new-window` or
// `"C:\Program Files\Meld\meld.exe" {left} {right}` into words. Single and
// double quotes group words; a backslash is kept as it is (Windows paths),
// except \" inside double quotes. Nothing is expanded and no shell runs.
func SplitCommand(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	has := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(rs) && rs[i+1] == '"' {
				cur.WriteRune('"')
				i++
			} else if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, has = c, true
		case c == ' ' || c == '\t' || c == '\n':
			if has || cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(c)
		}
	}
	if quote != 0 {
		return nil, errors.New("the command has an unclosed quote: " + s)
	}
	if has || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words, nil
}

// Expand replaces {name} in each word with vars[name]. A value is never
// split or interpreted, so a path with spaces or quotes stays one argument.
func Expand(words []string, vars map[string]string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		for k, v := range vars {
			w = strings.ReplaceAll(w, "{"+k+"}", v)
		}
		out[i] = w
	}
	return out
}

// Uses reports whether any word mentions {name}.
func Uses(words []string, name string) bool {
	for _, w := range words {
		if strings.Contains(w, "{"+name+"}") {
			return true
		}
	}
	return false
}

// Spawn starts name with args in dir and does not wait for it. when is
// called (if not nil) after the program ended, e.g. to remove temp files.
// Tests change it through spawn.
func Spawn(dir, name string, args []string, done func()) error {
	return spawn(dir, name, args, done)
}

var spawn = func(dir, name string, args []string, done func()) error {
	if NoSystemChanges() {
		if done != nil {
			done()
		}
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		if done != nil {
			done()
		}
		return err
	}
	go func() {
		_ = cmd.Wait()
		if done != nil {
			done()
		}
	}()
	return nil
}

// terminalLaunch is the program and arguments that open a terminal in dir.
type terminalLaunch struct {
	name string
	args []string
}

// terminalFor decides how to open a terminal in dir. command is the
// user's setting ("" = the OS default); have says whether a program is on
// PATH. A command that mentions {dir} gets it replaced; otherwise the
// terminal starts in dir because the process is started there. On macOS a
// single word that is no program (Terminal, iTerm, Warp) is an app name.
func terminalFor(goos, dir, command string, have func(string) bool) (terminalLaunch, error) {
	if strings.TrimSpace(command) != "" {
		words, err := SplitCommand(command)
		if err != nil {
			return terminalLaunch{}, err
		}
		if goos == "darwin" && len(words) == 1 && !strings.ContainsAny(words[0], "/{}") && !have(words[0]) {
			name := words[0]
			if strings.EqualFold(name, "iterm2") {
				name = "iTerm"
			}
			return terminalLaunch{"open", []string{"-a", name, dir}}, nil
		}
		words = Expand(words, map[string]string{"dir": dir, "path": dir})
		return terminalLaunch{words[0], words[1:]}, nil
	}
	switch goos {
	case "darwin":
		return terminalLaunch{"open", []string{"-a", "Terminal", dir}}, nil
	case "windows":
		if have("wt") {
			return terminalLaunch{"wt", []string{"-d", dir}}, nil
		}
		return terminalLaunch{"cmd", []string{"/c", "start", "", "/D", dir, "cmd"}}, nil
	}
	for _, t := range []terminalLaunch{
		{"x-terminal-emulator", nil},
		{"gnome-terminal", []string{"--working-directory=" + dir}},
		{"konsole", []string{"--workdir", dir}},
		{"xfce4-terminal", []string{"--working-directory=" + dir}},
		{"xterm", nil},
	} {
		if have(t.name) {
			return t, nil
		}
	}
	return terminalLaunch{}, errors.New("no terminal program found; choose one with: gitswitch set terminal <command>")
}

// OpenTerminal opens a terminal window in dir with the user's command
// (gitswitch set terminal) or the OS default. No shell is involved.
func OpenTerminal(dir, command string) error {
	t, err := terminalFor(runtime.GOOS, dir, command, func(n string) bool {
		_, err := exec.LookPath(n)
		return err == nil
	})
	if err != nil {
		return err
	}
	return Spawn(dir, t.name, t.args, nil)
}
