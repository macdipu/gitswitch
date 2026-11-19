package domain

import "testing"

func TestIgnorePattern(t *testing.T) {
	for _, c := range []struct {
		path  string
		kind  IgnoreKind
		isDir bool
		want  string
	}{
		{"build.log", IgnoreFile, false, "/build.log"},
		{"dir/a.txt", IgnoreFile, false, "/dir/a.txt"},
		{"dir/sub", IgnoreFile, true, "/dir/sub/"},
		{"dir/a.txt", IgnoreExt, false, "*.txt"},
		{"a.tar.gz", IgnoreExt, false, "*.gz"},
		{"dir/sub/a.txt", IgnoreFolder, false, "/dir/sub/"},
		{"dir/sub", IgnoreFolder, true, "/dir/sub/"},
		{"*.txt", IgnoreFile, false, `/\*.txt`},
		{"a?[b].go", IgnoreFile, false, `/a\?\[b].go`},
		{`back\slash`, IgnoreFile, false, `/back\\slash`},
		{"#notes", IgnoreFile, false, "/#notes"},
		{"!important", IgnoreFile, false, "/!important"},
		{"x.*", IgnoreExt, false, `*.\*`},
		{"dir/with space.txt", IgnoreFile, false, "/dir/with space.txt"},
	} {
		got, err := IgnorePattern(c.path, c.kind, c.isDir)
		if err != nil || got != c.want {
			t.Errorf("IgnorePattern(%q, %s, %v) = %q, %v; want %q", c.path, c.kind, c.isDir, got, err, c.want)
		}
	}
	for _, c := range []struct {
		path string
		kind IgnoreKind
	}{
		{"Makefile", IgnoreExt}, {".env", IgnoreExt}, {"dir/x.", IgnoreExt},
		{"top.txt", IgnoreFolder}, {"a\nb", IgnoreFile}, {"a.txt", "glob"},
	} {
		if got, err := IgnorePattern(c.path, c.kind, false); err == nil {
			t.Errorf("IgnorePattern(%q, %s) = %q, want an error", c.path, c.kind, got)
		}
	}
}

func TestEscapeIgnore(t *testing.T) {
	for in, want := range map[string]string{
		"#a":     `\#a`,
		"!a":     `\!a`,
		"a ":     `a\ `,
		"a  ":    `a\ \ `,
		" a":     " a",
		"a b":    "a b",
		"/dir/":  "/dir/",
		"*.log":  "*.log",
		"#x  ":   `\#x\ \ `,
		"a\\b":   "a\\b",
		"plain":  "plain",
		"/x y /": "/x y /",
	} {
		if got := EscapeIgnore(in); got != want {
			t.Errorf("EscapeIgnore(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddIgnoreLine(t *testing.T) {
	for _, c := range []struct {
		in, line, want string
		added          bool
	}{
		{"", "*.log", "*.log\n", true},
		{"a\n", "*.log", "a\n*.log\n", true},
		{"a", "*.log", "a\n*.log\n", true},
		{"*.log\n", "*.log", "*.log\n", false},
		{"a\r\n*.log\r\nb\r\n", "*.log", "a\r\n*.log\r\nb\r\n", false},
		{"/*.log\n", "*.log", "/*.log\n*.log\n", true},
		{"*.log \n", "*.log", "*.log \n*.log\n", true},
	} {
		got, added := AddIgnoreLine(c.in, c.line)
		if got != c.want || added != c.added {
			t.Errorf("AddIgnoreLine(%q, %q) = %q, %v; want %q, %v", c.in, c.line, got, added, c.want, c.added)
		}
	}
}

func TestParseIgnoreKind(t *testing.T) {
	for in, want := range map[string]IgnoreKind{"": IgnoreFile, "file": IgnoreFile, "ext": IgnoreExt, "Folder": IgnoreFolder} {
		if got, err := ParseIgnoreKind(in); err != nil || got != want {
			t.Errorf("ParseIgnoreKind(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseIgnoreKind("glob"); err == nil {
		t.Error("ParseIgnoreKind(glob): want an error")
	}
}
