package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// internalCommands are run by git or by completions, not typed by people.
var internalCommands = map[string]bool{"credential": true, "__branches": true, "__accounts": true, "version": true, "help": true}

// Every command people can type is in the README's command table, and in
// the help text, so the docs cannot fall behind the code again.
func TestCommandsAreDocumented(t *testing.T) {
	src, err := os.ReadFile("cli.go")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	table := string(readme)
	if i := strings.Index(table, "## Command reference"); i >= 0 {
		table = table[i:]
		if j := strings.Index(table, "### Shell completions"); j >= 0 {
			table = table[:j]
		}
	} else {
		t.Fatal("README has no Command reference")
	}
	// the switch in Run: tab, case "name"[, "alias"]:
	cases := regexp.MustCompile(`(?m)^\tcase ("[^"]+"(?:, "[^"]+")*):`).FindAllStringSubmatch(string(src), -1)
	if len(cases) < 20 {
		t.Fatalf("found only %d commands in cli.go: has the switch moved?", len(cases))
	}
	for _, c := range cases {
		name := strings.Trim(strings.SplitN(c[1], ",", 2)[0], `"`)
		if internalCommands[name] || strings.HasPrefix(name, "-") {
			continue
		}
		if !strings.Contains(table, "`"+name) && !strings.Contains(table, "|"+name) {
			t.Errorf("the README command table does not mention %q", name)
		}
		if !strings.Contains(usage, "gitswitch "+name) {
			t.Errorf("the help text does not mention %q", name)
		}
	}
}
