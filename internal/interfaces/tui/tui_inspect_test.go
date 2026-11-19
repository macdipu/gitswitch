package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// inspectRepo is an empty repo on main, shown on the History page, with
// helpers to run git, write a file and commit it.
func inspectRepo(t *testing.T) (x *tui, dir string, git func(...string) string, commit func(file, text, msg string) string) {
	t.Helper()
	x = testTUI(t)
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "T", "GIT_AUTHOR_EMAIL": "t@x.example", "GIT_COMMITTER_NAME": "T", "GIT_COMMITTER_EMAIL": "t@x.example"} {
		t.Setenv(k, v)
	}
	dir = filepath.Join(t.TempDir(), "r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git = func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit = func(file, text, msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	return
}

// show opens the repo on History (after its commits were made).
func showRepo(t *testing.T, x *tui, dir string) {
	t.Helper()
	gd, err := x.app.Repos.For(dir)
	if err != nil {
		t.Fatal(err)
	}
	x.histDir = gd
	x.histLines = nil
	x.switchTab(tabHistory)
	x.loadHistory()
}

// page is the History page as drawn, without colours.
func page(x *tui) string {
	var lines []string
	switch {
	case x.inspecting():
		lines, _ = x.drawInspect()
	default:
		lines, _ = x.drawHistory()
	}
	return history.StripANSI(strings.Join(lines, "\n"))
}

// commitRows counts the History lines that are commits.
func commitRows(x *tui) int {
	n := 0
	for _, r := range x.histRows {
		if r.Hash != "" {
			n++
		}
	}
	return n
}

func TestInspectMenuAndKey(t *testing.T) {
	x, dir, _, commit := inspectRepo(t)
	commit("a.txt", "x\n", "one")
	showRepo(t, x, dir)
	x.handle("I")
	if x.picker == nil || x.picker.title != "Inspect" || len(x.picker.options) != 6 {
		t.Fatalf("I did not open the Inspect menu: %+v", x.picker)
	}
	box := history.StripANSI(strings.Join(x.overlay(), "\n"))
	for _, w := range []string{"Blame a file", "File history", "Compare two refs", "Reflog", "Bisect", "Search code changes"} {
		if !strings.Contains(box, w) {
			t.Errorf("menu lacks %q:\n%s", w, box)
		}
	}
	// A selected commit offers its files first: pick a.txt to blame it there.
	x.handle("enter")
	if x.picker == nil || !strings.Contains(strings.Join(x.picker.options, "|"), "a.txt") {
		t.Fatalf("the blame file list is missing a.txt: %+v", x.picker)
	}
	x.handle("down")
	x.handle("enter")
	if x.insp.kind != "blame" || x.insp.bl.path != "a.txt" {
		t.Fatalf("not blaming a.txt (msg %q)", x.msg)
	}
	x.handle("esc")
	if x.inspecting() {
		t.Fatal("Esc did not close blame")
	}
}

func TestCompareBothModes(t *testing.T) {
	x, dir, git, commit := inspectRepo(t)
	commit("base.txt", "base\n", "base")
	git("switch", "-q", "-c", "feat")
	commit("feat.txt", "f\n", "feature work")
	git("switch", "-q", "main")
	commit("main.txt", "m\n", "main work")
	showRepo(t, x, dir)

	x.handle("I")
	x.handle("down")
	x.handle("down")
	x.handle("enter") // Compare two refs…
	if x.form == nil {
		t.Fatal("no compare form")
	}
	if got := x.form.fields[1].value; got != "main" {
		t.Fatalf("B defaults to %q, want the current branch", got)
	}
	x.form.fields[0].value, x.form.fields[1].value = "feat", "main"
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	cm := x.insp.cm
	if x.insp.kind != "compare" || cm == nil {
		t.Fatalf("compare not open (msg %q)", x.msg)
	}
	// since the fork: only what main changed
	if len(cm.c.Files) != 1 || cm.c.Files[0].Path != "main.txt" {
		t.Fatalf("since-fork files = %+v", cm.c.Files)
	}
	scr := page(x)
	for _, w := range []string{"Only on A (1)", "feature work", "Only on B (1)", "main work", "Changed files (1)", "A main.txt"} {
		if !strings.Contains(scr, w) {
			t.Errorf("screen lacks %q:\n%s", w, scr)
		}
	}
	x.handle("m") // direct: both differ
	if cm.mode != inspect.Direct || len(cm.c.Files) != 2 {
		t.Fatalf("direct: mode %v files %+v", cm.mode, cm.c.Files)
	}
	x.handle("enter")
	if x.view == nil || !strings.Contains(history.StripANSI(strings.Join(x.view.lines, "\n")), "+++") {
		t.Fatalf("Enter did not show a diff: %+v", x.view)
	}
	x.handle("esc")

	// B can be the files on disk
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("m\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.handle("e")
	x.form.fields[0].value, x.form.fields[1].value = "main", inspect.Working
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if !x.insp.cm.c.Working || len(x.insp.cm.c.Files) != 1 || !strings.Contains(page(x), "your working folder") {
		t.Fatalf("working compare: %+v\n%s", x.insp.cm.c, page(x))
	}
	// A cannot be the working folder
	x.handle("e")
	x.form.fields[0].value = inspect.Working
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if !x.msgErr {
		t.Fatal("A = :working should be refused")
	}
}

func TestReflogLostAndRecover(t *testing.T) {
	x, dir, git, commit := inspectRepo(t)
	first := commit("a.txt", "1\n", "first")
	commit("a.txt", "2\n", "second")
	lost := commit("a.txt", "3\n", "third")
	git("reset", "-q", "--hard", first)
	showRepo(t, x, dir)

	x.handle("I")
	for i := 0; i < 3; i++ {
		x.handle("down")
	}
	x.handle("enter")
	rf := x.insp.rf
	if x.insp.kind != "reflog" || rf == nil {
		t.Fatalf("reflog not open (msg %q)", x.msg)
	}
	if scr := page(x); !strings.Contains(scr, "lost") || !strings.Contains(scr, "reset") {
		t.Fatalf("reflog screen:\n%s", scr)
	}
	x.handle("l") // lost only
	if !rf.lost || len(rf.entries) == 0 {
		t.Fatalf("lost only: %+v", rf.entries)
	}
	for _, e := range rf.entries {
		if !e.Lost {
			t.Fatalf("a kept commit is listed under lost only: %+v", e)
		}
	}
	if rf.entries[0].Hash != lost {
		t.Fatalf("newest lost = %s, want the third commit %s", rf.entries[0].Hash, lost)
	}
	x.handle("b")
	if x.form == nil {
		t.Fatal("b did not ask for a branch name")
	}
	typeKeys(x, "rescued")
	x.handle("enter")
	if x.msgErr || !strings.Contains(x.msg, "git switch rescued") {
		t.Fatalf("msg after recovery: %q", x.msg)
	}
	if got := git("rev-parse", "rescued"); got != lost {
		t.Fatalf("rescued at %s, want %s", got, lost)
	}
	if git("branch", "--show-current") != "main" {
		t.Fatal("recovery moved the current branch")
	}
	// the same name is refused (all entries again: the recovered one is no longer lost)
	x.handle("l")
	x.handle("b")
	typeKeys(x, "rescued")
	x.handle("enter")
	if !x.msgErr {
		t.Fatal("a taken name should be refused")
	}
}

func TestBisectFindsCulpritAndResets(t *testing.T) {
	x, dir, git, commit := inspectRepo(t)
	var hashes []string
	for i, txt := range []string{"ok", "ok", "ok", "bad", "bad", "bad"} {
		hashes = append(hashes, commit("v.txt", txt+" "+string(rune('a'+i))+"\n", "change "+string(rune('a'+i))))
	}
	showRepo(t, x, dir)

	x.handle("I")
	for i := 0; i < 4; i++ {
		x.handle("down")
	}
	x.handle("enter")
	if x.form == nil || x.form.fields[0].value != "HEAD" {
		t.Fatalf("bisect form: %+v", x.form)
	}
	x.form.fields[1].value = hashes[0]
	x.handle("enter")
	x.handle("enter")
	if x.insp.kind != "bisect" || x.insp.bs.st.Next == nil {
		t.Fatalf("bisect not running (msg %q)", x.msg)
	}
	if scr := page(x); !strings.Contains(scr, "steps") && !strings.Contains(scr, "step") {
		t.Fatalf("no steps left shown:\n%s", scr)
	}
	for i := 0; i < 10 && x.insp.bs.st.Culprit == nil; i++ {
		b, _ := os.ReadFile(filepath.Join(dir, "v.txt"))
		if strings.HasPrefix(string(b), "bad") {
			x.handle("b")
		} else {
			x.handle("g")
		}
	}
	c := x.insp.bs.st.Culprit
	if c == nil || c.Hash != hashes[3] {
		t.Fatalf("culprit = %+v, want %s", c, hashes[3])
	}
	scr := page(x)
	for _, w := range []string{"The first bad commit", "change d", "v.txt", "Steps so far", "main"} {
		if !strings.Contains(scr, w) {
			t.Errorf("culprit screen lacks %q:\n%s", w, scr)
		}
	}
	// leave the view: History says a bisect is running
	x.handle("esc")
	if !strings.Contains(page(x), "Bisecting…") {
		t.Fatalf("no banner on History:\n%s", page(x))
	}
	x.handle("I")
	for i := 0; i < 4; i++ {
		x.handle("down")
	}
	x.handle("enter") // running: back into the view
	if x.insp.kind != "bisect" {
		t.Fatal("Bisect did not reopen the running bisect")
	}
	x.handle("x")
	if x.confirm == nil {
		t.Fatal("x did not ask")
	}
	x.handle("y")
	if git("branch", "--show-current") != "main" || x.inspecting() || strings.Contains(page(x), "Bisecting…") {
		t.Fatalf("not back on main (msg %q)", x.msg)
	}
}

func TestCodeSearchFiltersHistory(t *testing.T) {
	x, dir, _, commit := inspectRepo(t)
	commit("a.txt", "plain\n", "plain")
	commit("a.txt", "plain\nNeedle here\n", "adds the needle")
	commit("b.txt", "other\n", "other")
	showRepo(t, x, dir)
	if commitRows(x) != 3 {
		t.Fatalf("%d rows", commitRows(x))
	}
	x.handle("I")
	for i := 0; i < 5; i++ {
		x.handle("down")
	}
	x.handle("enter")
	if x.form == nil {
		t.Fatal("no search form")
	}
	typeKeys(x, "Needle")
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if x.filter.Pickaxe != "Needle" || commitRows(x) != 1 || !strings.Contains(x.histLines[0], "adds the needle") {
		t.Fatalf("filter %+v rows %v", x.filter, x.histLines)
	}
	if !strings.Contains(page(x), `code "Needle"`) {
		t.Fatalf("the filter line is not shown:\n%s", page(x))
	}
	// case matters unless asked
	x.codeSearch()
	x.form.fields[0].value = "needle"
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if commitRows(x) != 0 {
		t.Fatalf("a case-sensitive search found %d", commitRows(x))
	}
	x.codeSearch()
	x.form.fields[0].value, x.form.fields[1].value, x.form.fields[2].value = "need.e", "regex (-G)", "yes"
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if !x.filter.PickaxeRegex || !x.filter.PickaxeIgnoreCase || commitRows(x) != 1 {
		t.Fatalf("regex search: %+v rows %d", x.filter, commitRows(x))
	}
	// a bad regex keeps the list and says why
	x.codeSearch()
	x.form.fields[0].value = "(("
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if !x.msgErr || commitRows(x) != 1 || x.filter.Pickaxe != "need.e" {
		t.Fatalf("bad regex: msg %q err %v filter %+v rows %d", x.msg, x.msgErr, x.filter, commitRows(x))
	}
	// empty clears it
	x.codeSearch()
	x.form.fields[0].value = ""
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if x.filter.Active() || commitRows(x) != 3 {
		t.Fatalf("not cleared: %+v rows %d", x.filter, commitRows(x))
	}
}
