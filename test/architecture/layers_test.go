// Package architecture enforces the dependency rules described in
// ARCHITECTURE.md. Every package is given a role from where it lives
// (domain, application, infrastructure, ...), and each role may import only
// the roles listed in allowed. The imports are read from the source, so
// files for every OS and build tag are checked, not just this machine's.
package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/macdipu/gitswitch/"

type role string

const (
	platform       role = "platform"       // internal/platform/*: OS details, no business rules
	domain         role = "domain"         // internal/features/<f>/domain: pure rules and ports
	application    role = "application"    // internal/features/<f>/application: use cases
	infrastructure role = "infrastructure" // internal/features/<f>/infrastructure: implements ports
	module_        role = "feature module" // internal/features/<f>: a single-package feature (setup, doctor)
	interfaces     role = "interface"      // internal/interfaces/*: front ends (CLI, TUI, desktop, prompts)
	bootstrap      role = "bootstrap"      // internal/bootstrap: builds and wires everything
	command        role = "command"        // cmd/*: main
)

// allowed lists the roles each role may import. Same-role imports not
// listed here are refused too (e.g. one application package importing
// another): features talk through ports.
var allowed = map[role][]role{
	platform:       {platform},
	domain:         {domain},
	application:    {domain, platform},
	infrastructure: {domain, infrastructure, platform},
	module_:        {domain, infrastructure, module_, platform},
	interfaces:     {domain, application, module_, interfaces, bootstrap, platform},
	bootstrap:      {domain, application, infrastructure, module_, interfaces, platform},
	command:        {interfaces},
}

// mains lists what each program's main package may import: gitswitch (the
// git hook, CLI and terminal app) starts the command line, gitswitch-app
// starts the desktop window.
var mains = map[string][]string{
	"cmd/gitswitch":     {"internal/interfaces/cli"},
	"cmd/gitswitch-app": {"internal/interfaces/desktop/window"},
}

// Standard-library packages a role must not use: domain code stays pure,
// and use cases reach processes, the network and the OS through ports or
// the platform packages.
var forbiddenStd = map[role][]string{
	domain:      {"os", "os/exec", "os/signal", "syscall", "unsafe", "runtime", "io/fs", "net/http", "database/sql"},
	application: {"os/exec", "os/signal", "syscall", "unsafe", "net/http", "database/sql"},
}

// roleOf classifies a package directory (relative to the module root).
func roleOf(pkg string) (role, bool) {
	parts := strings.Split(pkg, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "cmd":
		return command, true
	case pkg == "internal/bootstrap":
		return bootstrap, true
	case len(parts) >= 3 && parts[0] == "internal" && parts[1] == "platform":
		return platform, true
	case len(parts) >= 3 && parts[0] == "internal" && parts[1] == "interfaces":
		return interfaces, true
	case len(parts) == 3 && parts[0] == "internal" && parts[1] == "features":
		return module_, true
	case len(parts) == 4 && parts[0] == "internal" && parts[1] == "features":
		switch parts[3] {
		case "domain":
			return domain, true
		case "application":
			return application, true
		case "infrastructure":
			return infrastructure, true
		}
	}
	return "", false
}

// feature is the feature a package belongs to ("" when none).
func feature(pkg string) string {
	if p := strings.Split(pkg, "/"); len(p) >= 3 && p[0] == "internal" && p[1] == "features" {
		return p[2]
	}
	return ""
}

// imports maps each package (relative to the module root) to its imports,
// from non-test files of every OS and build tag.
func imports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	out := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		// Skip what `go build ./...` skips: hidden, _ and testdata folders,
		// and nested modules (e.g. a clone inside sandbox/config).
		if err == nil && d.IsDir() && p != root {
			if n := d.Name(); strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "testdata" {
				return filepath.SkipDir
			}
			if _, e := os.Stat(filepath.Join(p, "go.mod")); e == nil {
				return filepath.SkipDir
			}
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		pkg := filepath.ToSlash(rel)
		if strings.HasPrefix(pkg, "test/") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		if out[pkg] == nil {
			out[pkg] = map[string]bool{}
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			out[pkg][path] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("found no packages")
	}
	return out
}

func TestDependencyRules(t *testing.T) {
	for pkg, imps := range imports(t) {
		from, ok := roleOf(pkg)
		if !ok {
			t.Errorf("%s has no role: put it under internal/features/<feature>/{domain,application,infrastructure}, internal/features/<feature>, internal/interfaces, internal/platform or internal/bootstrap (see ARCHITECTURE.md)", pkg)
			continue
		}
		for path := range imps {
			if !strings.HasPrefix(path, module) {
				for _, bad := range forbiddenStd[from] {
					if path == bad {
						t.Errorf("%s (%s) imports %s: %s code must not use it; go through a port or internal/platform", pkg, from, path, from)
					}
				}
				continue
			}
			dep := strings.TrimPrefix(path, module)
			to, ok := roleOf(dep)
			if !ok {
				t.Errorf("%s imports %s, which has no role", pkg, dep)
				continue
			}
			if !contains(allowed[from], to) {
				t.Errorf("%s (%s) imports %s (%s): %s code may import only %v", pkg, from, dep, to, from, allowed[from])
			}
			// Front ends reach features only through their use cases and
			// domain types; bootstrap.App hands them the use cases.
			if from == command && !contains(mains[pkg], dep) {
				t.Errorf("%s imports %s: this main may import only %v", pkg, dep, mains[pkg])
			}
		}
	}
}

// Domain packages may use other features' domain types (an account, a
// repo), but those uses must not go round in a circle.
func TestDomainFeaturesHaveNoCycles(t *testing.T) {
	deps := map[string]map[string]bool{}
	for pkg, imps := range imports(t) {
		if r, _ := roleOf(pkg); r != domain {
			continue
		}
		from := feature(pkg)
		if deps[from] == nil {
			deps[from] = map[string]bool{}
		}
		for path := range imps {
			dep := strings.TrimPrefix(path, module)
			if r, _ := roleOf(dep); r == domain && feature(dep) != from {
				deps[from][feature(dep)] = true
			}
		}
	}
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(f string)
	visit = func(f string) {
		switch state[f] {
		case active:
			i := indexOf(stack, f)
			t.Errorf("domain packages depend on each other in a circle: %s", strings.Join(append(stack[i:], f), " -> "))
			return
		case done:
			return
		}
		state[f] = active
		stack = append(stack, f)
		next := make([]string, 0, len(deps[f]))
		for d := range deps[f] {
			next = append(next, d)
		}
		sort.Strings(next)
		for _, d := range next {
			visit(d)
		}
		stack = stack[:len(stack)-1]
		state[f] = done
	}
	features := make([]string, 0, len(deps))
	for f := range deps {
		features = append(features, f)
	}
	sort.Strings(features)
	for _, f := range features {
		visit(f)
	}
}

// The rules above must keep meaning something: guard against a refactor
// that quietly empties a role.
func TestEveryRoleIsUsed(t *testing.T) {
	seen := map[role]bool{}
	for pkg := range imports(t) {
		if r, ok := roleOf(pkg); ok {
			seen[r] = true
		}
	}
	for r := range allowed {
		if !seen[r] {
			t.Errorf("no package has the role %q; update the rules in this file and ARCHITECTURE.md", r)
		}
	}
}

// Core packages must not print to standard output: they return values and
// errors, and front ends decide how to show them. (The ssh hook in
// particular: git reads its protocol from stdout.)
func TestCoreDoesNotPrint(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if top == "interfaces" || top == "platform" {
			return nil
		}
		b, _ := os.ReadFile(p)
		for i, line := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "//") {
				continue
			}
			if strings.Contains(l, "fmt.Print") || strings.Contains(l, "os.Stdout.Write") {
				t.Errorf("%s:%d prints to stdout; return the value or use a report callback instead", p, i+1)
			}
		}
		return nil
	})
}

func contains[T comparable](list []T, x T) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// reach lists every package of this module that pkg imports, directly or
// not, each with the import chain that reaches it.
func reach(graph map[string]map[string]bool, pkg string) map[string][]string {
	chains := map[string][]string{pkg: {pkg}}
	queue := []string{pkg}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for path := range graph[cur] {
			dep, ok := strings.CutPrefix(path, module)
			if !ok {
				continue
			}
			if _, seen := chains[dep]; !seen {
				chains[dep] = append(append([]string{}, chains[cur]...), dep)
				queue = append(queue, dep)
			}
		}
	}
	return chains
}

// Git runs the gitswitch program on every clone, pull and push, so it
// must never contain the desktop app (Wails, the system web view, cgo).
// The app is its own program, cmd/gitswitch-app.
func TestHookProgramHasNoDesktop(t *testing.T) {
	graph := imports(t)
	for pkg, chain := range reach(graph, "cmd/gitswitch") {
		if strings.HasPrefix(pkg, "internal/interfaces/desktop") {
			t.Errorf("gitswitch includes the desktop app: %s", strings.Join(chain, " -> "))
		}
		for path := range graph[pkg] {
			if strings.HasPrefix(path, "github.com/wailsapp/") {
				t.Errorf("gitswitch includes Wails: %s -> %s", strings.Join(chain, " -> "), path)
			}
		}
	}
}

// The ssh hook use case stays small: it may not depend on the git client
// features or any front end, so it can keep starting in milliseconds as
// the app grows.
func TestSSHHookStaysSmall(t *testing.T) {
	banned := []string{
		"internal/features/history", "internal/features/refs", "internal/features/repository",
		"internal/features/doctor", "internal/features/setup",
		"internal/interfaces/cli", "internal/interfaces/tui", "internal/interfaces/desktop",
	}
	for pkg, chain := range reach(imports(t), "internal/features/ssh/application") {
		for _, b := range banned {
			if pkg == b || strings.HasPrefix(pkg, b+"/") {
				t.Errorf("the ssh hook depends on %s: %s", b, strings.Join(chain, " -> "))
			}
		}
	}
}
