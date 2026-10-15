package domain

import (
	"reflect"
	"testing"
)

func TestParseLabels(t *testing.T) {
	got := ParseLabels("HEAD -> refs/heads/main, refs/heads/feature/x, refs/remotes/origin/main, refs/remotes/origin/HEAD, tag: refs/tags/v2.3.0")
	want := []Label{{"main", "head"}, {"feature/x", "local"}, {"origin/main", "remote"}, {"v2.3.0", "tag"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLabels = %+v", got)
	}
	if s := RefsText(got); s != "HEAD -> main, feature/x, origin/main, tag: v2.3.0" {
		t.Errorf("RefsText = %q", s)
	}
	if got := ParseLabels("HEAD, refs/heads/x"); !reflect.DeepEqual(got, []Label{{"HEAD", "detached"}, {"x", "local"}}) {
		t.Errorf("detached: %+v", got)
	}
	if got := ParseLabels(""); got != nil {
		t.Errorf("no labels: %+v", got)
	}
}

func TestRefFilter(t *testing.T) {
	if (Filter{Ref: "main"}).Narrows() || !(Filter{Ref: "main", Path: "a.go"}).Narrows() || !(Filter{Ref: "main"}).Active() {
		t.Error("a branch alone keeps the graph; a path does not")
	}
	for _, bad := range []string{"--all", "-n1", "a b", "x\ny"} {
		if CheckRef(bad) == nil {
			t.Errorf("CheckRef(%q) must fail", bad)
		}
	}
	if CheckRef("feature/x") != nil || CheckRef("") != nil {
		t.Error("ordinary names pass")
	}
}

func TestModules(t *testing.T) {
	files := []FileChange{{Path: "README.md"}, {Path: "app/web/src/main.ts"}, {Path: "app/web/package.json"}, {Path: "svc/api/x.go"}}
	if got := Ancestors(files); !reflect.DeepEqual(got, []string{"", "app", "app/web", "app/web/src", "svc", "svc/api"}) {
		t.Errorf("Ancestors = %q", got)
	}
	AssignModules(files, []string{"", "app/web", "svc"})
	var got []string
	for _, f := range files {
		got = append(got, f.Module)
	}
	if !reflect.DeepEqual(got, []string{"", "app/web", "app/web", "svc"}) {
		t.Errorf("modules = %q", got)
	}
	for name, want := range map[string]bool{"go.mod": true, "build.gradle.kts": true, "Api.csproj": true, "main.go": false, "": false} {
		if IsModuleFile(name) != want {
			t.Errorf("IsModuleFile(%q) = %v", name, !want)
		}
	}
}
