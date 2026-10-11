// Package architecture enforces the layering described in ARCHITECTURE.md.
// It fails when a package imports a package from a higher layer, so the
// structure stays clean as the project grows.
package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/macdipu/gitswitch/"

// layer of each internal package: a package may import only packages in
// strictly lower layers (or the allowed same-layer pairs below).
var layer = map[string]int{
	"internal/sys":        0,
	"internal/gitcli":     1,
	"internal/account":    2,
	"internal/resolve":    3,
	"internal/keys":       3,
	"internal/setup":      3,
	"internal/repo":       4,
	"internal/prompt":     4,
	"internal/history":    5,
	"internal/sshhook":    5,
	"internal/doctor":     6,
	"internal/app":        7,
	"internal/ui/tui":     8,
	"internal/ui/desktop": 8,
	"internal/ui/cli":     9, // composes the other front ends
	"cmd/gitswitch":       10,
}

// Front ends talk to git only through the app layer.
var forbidden = map[string][]string{
	"internal/ui/cli":     {"internal/gitcli"},
	"internal/ui/tui":     {"internal/gitcli"},
	"internal/ui/desktop": {"internal/gitcli"},
	"cmd/gitswitch":       {"internal/gitcli", "internal/app"},
}

func TestLayers(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	seen := map[string]bool{}
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
		from, known := layer[pkg]
		if !known {
			t.Errorf("%s is not assigned a layer in layers_test.go (and ARCHITECTURE.md)", pkg)
			return nil
		}
		seen[pkg] = true
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, module) {
				continue
			}
			dep := strings.TrimPrefix(path, module)
			for _, bad := range forbidden[pkg] {
				if dep == bad {
					t.Errorf("%s imports %s: front ends must go through internal/app", p, dep)
				}
			}
			if to, ok := layer[dep]; ok && to >= from {
				t.Errorf("%s (layer %d) imports %s (layer %d): imports must point to lower layers", p, from, dep, to)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for pkg := range layer {
		if !seen[pkg] {
			t.Errorf("layer table lists %s, which no longer exists", pkg)
		}
	}
}

// Core packages must not print to standard output: they return values and
// errors, and front ends decide how to show them. (sshhook in particular:
// git reads its protocol from stdout.)
func TestCoreDoesNotPrint(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	allowed := map[string]bool{"ui": true, "prompt": true, "sys": true}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		top := strings.Split(filepath.ToSlash(rel), "/")[0]
		if allowed[top] {
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
