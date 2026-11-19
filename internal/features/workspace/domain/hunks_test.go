package domain

import (
	"strings"
	"testing"
)

const hdr = "diff --git a/f.txt b/f.txt\nindex 111..222 100644\n--- a/f.txt\n+++ b/f.txt\n"

func TestParseDiff(t *testing.T) {
	text := hdr + "@@ -1,4 +1,5 @@ func x\n a\n-b\n+B\n+B2\n c\n d\n@@ -20 +21,2 @@\n z\n+y\n"
	fd, err := ParseDiff(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(fd.Hunks) != 2 || len(fd.Header) != 4 {
		t.Fatalf("hunks %d header %d", len(fd.Hunks), len(fd.Header))
	}
	h := fd.Hunks[0]
	if h.Header != "@@ -1,4 +1,5 @@ func x" || h.suffix != " func x" || len(h.Lines) != 6 {
		t.Fatalf("hunk 0: %+v", h)
	}
	want := []DiffLine{
		{Kind: "context", Text: "a", Old: 1, New: 1},
		{Kind: "del", Text: "b", Old: 2},
		{Kind: "add", Text: "B", New: 2},
		{Kind: "add", Text: "B2", New: 3},
		{Kind: "context", Text: "c", Old: 3, New: 4},
		{Kind: "context", Text: "d", Old: 4, New: 5},
	}
	for i, w := range want {
		if h.Lines[i] != w {
			t.Errorf("line %d = %+v, want %+v", i, h.Lines[i], w)
		}
	}
	if a, d := h.CountChanges(); a != 2 || d != 1 {
		t.Errorf("changes %d %d", a, d)
	}
	if h2 := fd.Hunks[1]; h2.oldCount != 1 || h2.newCount != 2 || h2.Lines[1].New != 22 {
		t.Errorf("hunk 1: %+v", h2)
	}
}

func TestParseDiffKinds(t *testing.T) {
	for name, c := range map[string]struct {
		text                              string
		hunks                             int
		creates, deletes, renames, binary bool
		modeOnly                          bool
	}{
		"new file":                         {"diff --git a/n b/n\nnew file mode 100644\nindex 0..1\n--- /dev/null\n+++ b/n\n@@ -0,0 +1,2 @@\n+a\n+b\n", 1, true, false, false, false, false},
		"deleted":                          {"diff --git a/n b/n\ndeleted file mode 100644\nindex 1..0\n--- a/n\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n", 1, false, true, false, false, false},
		"rename":                           {"diff --git a/o b/n\nsimilarity index 80%\nrename from o\nrename to n\nindex 1..2 100644\n--- a/o\n+++ b/n\n@@ -1 +1 @@\n-a\n+b\n", 1, false, false, true, false, false},
		"pure rename":                      {"diff --git a/o b/n\nsimilarity index 100%\nrename from o\nrename to n\n", 0, false, false, true, false, true},
		"binary":                           {"diff --git a/i.png b/i.png\nindex 1..2 100644\nBinary files a/i.png and b/i.png differ\n", 0, false, false, false, true, false},
		"mode only":                        {"diff --git a/s b/s\nold mode 100644\nnew mode 100755\n", 0, false, false, false, false, true},
		"no eol":                           {hdr + "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n", 1, false, false, false, false, false},
		"crlf":                             {hdr + "@@ -1 +1 @@\n-a\r\n+b\r\n", 1, false, false, false, false, false},
		"content that looks like a header": {hdr + "@@ -1 +1 @@\n--- not a file\n+++ not a file\n", 1, false, false, false, false, false},
	} {
		fd, err := ParseDiff(c.text)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(fd.Hunks) != c.hunks || fd.Creates != c.creates || fd.Deletes != c.deletes || fd.Renames != c.renames || fd.Binary != c.binary || fd.ModeOnly != c.modeOnly {
			t.Errorf("%s: %+v", name, fd)
		}
	}
	fd, _ := ParseDiff(hdr + "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n")
	if !fd.Hunks[0].Lines[0].NoEOL || !fd.Hunks[0].Lines[1].NoEOL {
		t.Errorf("no-eol flags: %+v", fd.Hunks[0].Lines)
	}
	fd, _ = ParseDiff(hdr + "@@ -1 +1 @@\n-a\r\n+b\r\n")
	if fd.Hunks[0].Lines[1].Text != "b\r" {
		t.Errorf("CRLF kept: %q", fd.Hunks[0].Lines[1].Text)
	}
}

func TestParseDiffRefuses(t *testing.T) {
	for name, text := range map[string]string{
		"empty":         "",
		"two files":     hdr + "@@ -1 +1 @@\n-a\n+b\n" + hdr + "@@ -1 +1 @@\n-a\n+b\n",
		"not a diff":    "hello\n",
		"cut short":     hdr + "@@ -1,3 +1,3 @@\n a\n",
		"garbage":       hdr + "@@ -1 +1 @@\n-a\n+b\nrubbish\n",
		"bad line mark": hdr + "@@ -1 +1 @@\n?a\n",
	} {
		if _, err := ParseDiff(text); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func parse(t *testing.T, text string) FileDiff {
	t.Helper()
	fd, err := ParseDiff(text)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestBuildPatch(t *testing.T) {
	// hunk 0: lines 0..: a(ctx) -b +B +B2 c(ctx) ; indexes 1,2,3 are changes
	two := hdr + "@@ -1,3 +1,4 @@\n a\n-b\n+B\n+B2\n c\n@@ -10,3 +11,3 @@\n x\n-y\n+Y\n z\n"
	for name, c := range map[string]struct {
		diff    string
		sel     Selection
		reverse bool
		want    string
		whole   bool
	}{
		"whole hunk": {two, Selection{Hunks: []int{0}}, false,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,4 @@\n a\n-b\n+B\n+B2\n c\n", false},
		"second hunk moves by the first one's size": {two, Selection{Hunks: []int{1}}, false,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -10,3 +10,3 @@\n x\n-y\n+Y\n z\n", false},
		"every hunk keeps the file header": {two, Selection{Hunks: []int{0, 1}}, false,
			hdr + "@@ -1,3 +1,4 @@\n a\n-b\n+B\n+B2\n c\n@@ -10,3 +11,3 @@\n x\n-y\n+Y\n z\n", true},
		"only the deletion: the additions are dropped": {two, Selection{Lines: map[int][]int{0: {1}}}, false,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,2 @@\n a\n-b\n c\n", false},
		"only an addition: the deletion stays as context": {two, Selection{Lines: map[int][]int{0: {2}}}, false,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,4 @@\n a\n b\n+B\n c\n", false},
		"the second addition only": {two, Selection{Lines: map[int][]int{0: {3}}}, false,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,4 @@\n a\n b\n+B2\n c\n", false},
		"reverse: only the addition, the deletion is left out": {two, Selection{Lines: map[int][]int{0: {2}}}, true,
			"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,4 @@\n a\n+B\n B2\n c\n", false},
	} {
		got, whole, err := BuildPatch(parse(t, c.diff), c.sel, c.reverse)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want || whole != c.whole {
			t.Errorf("%s:\n%s\nwant\n%s (whole %v, got %v)", name, got, c.want, c.whole, whole)
		}
	}
}

func TestBuildPatchNoEOLAndCRLF(t *testing.T) {
	// "a" lost its line break and a line was added after it.
	d := hdr + "@@ -1,2 +1,3 @@\n x\n-a\n\\ No newline at end of file\n+a\n+b\n\\ No newline at end of file\n"
	fd := parse(t, d)
	// Picking only the "+b" would keep "a" without a break as context, with a change after it.
	if _, _, err := BuildPatch(fd, Selection{Lines: map[int][]int{0: {3}}}, false); err == nil || !strings.Contains(err.Error(), "without a line break") {
		t.Errorf("want the no-newline refusal, got %v", err)
	}
	// Picking everything keeps the markers.
	p, _, err := BuildPatch(fd, Selection{Hunks: []int{0}}, false)
	if err != nil || strings.Count(p, "\\ No newline at end of file\n") != 2 {
		t.Errorf("whole hunk: %q %v", p, err)
	}
	// Picking only the final line's change in a file whose end stays without a break.
	e := hdr + "@@ -1,2 +1,2 @@\n x\n-a\n+b\n\\ No newline at end of file\n"
	p, _, err = BuildPatch(parse(t, e), Selection{Lines: map[int][]int{0: {2}}}, false)
	// the deletion is not picked and stays as context, and its replacement is the last line
	if err != nil || !strings.Contains(p, " a\n+b\n\\ No newline at end of file\n") {
		t.Errorf("add without eol: %q %v", p, err)
	}
	// CRLF text is written back byte for byte.
	c := hdr + "@@ -1,2 +1,2 @@\n x\r\n-a\r\n+b\r\n"
	p, _, err = BuildPatch(parse(t, c), Selection{Hunks: []int{0}}, false)
	if err != nil || !strings.Contains(p, " x\r\n-a\r\n+b\r\n") {
		t.Errorf("crlf: %q %v", p, err)
	}
}

func TestBuildPatchCounts(t *testing.T) {
	// Two hunks; picking one line of each: the second's new start shifts by what the first adds.
	d := hdr + "@@ -1,2 +1,4 @@\n a\n+p\n+q\n b\n@@ -10,2 +12,3 @@\n x\n+r\n y\n"
	p, _, err := BuildPatch(parse(t, d), Selection{Lines: map[int][]int{0: {1}, 1: {1}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "@@ -1,2 +1,3 @@\n a\n+p\n b\n") || !strings.Contains(p, "@@ -10,2 +11,3 @@\n x\n+r\n y\n") {
		t.Errorf("counts:\n%s", p)
	}
	// An addition at the start of a file: "-0,0".
	n := "diff --git a/n b/n\nnew file mode 100644\nindex 0..1\n--- /dev/null\n+++ b/n\n@@ -0,0 +1,3 @@\n+1\n+2\n+3\n"
	p, whole, err := BuildPatch(parse(t, n), Selection{Lines: map[int][]int{0: {1}}}, false)
	if err != nil || whole {
		t.Fatalf("%v %v", err, whole)
	}
	want := "diff --git a/n b/n\n--- a/n\n+++ b/n\n@@ -0,0 +1 @@\n+2\n"
	if p != want {
		t.Errorf("part of a new file:\n%s\nwant\n%s", p, want)
	}
	p, whole, _ = BuildPatch(parse(t, n), Selection{Hunks: []int{0}}, false)
	if !whole || !strings.Contains(p, "new file mode 100644") || !strings.Contains(p, "--- /dev/null") {
		t.Errorf("whole new file keeps its header:\n%s", p)
	}
}

func TestBuildPatchDeletedAndRenamed(t *testing.T) {
	del := "diff --git a/n b/n\ndeleted file mode 100644\nindex 1..0\n--- a/n\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-1\n-2\n-3\n"
	p, whole, err := BuildPatch(parse(t, del), Selection{Lines: map[int][]int{0: {0}}}, false)
	if err != nil || whole {
		t.Fatal(err, whole)
	}
	if want := "diff --git a/n b/n\n--- a/n\n+++ b/n\n@@ -1,3 +1,2 @@\n-1\n 2\n 3\n"; p != want {
		t.Errorf("part of a deleted file:\n%s\nwant\n%s", p, want)
	}
	ren := "diff --git a/o b/n\nsimilarity index 80%\nrename from o\nrename to n\nindex 1..2 100644\n--- a/o\n+++ b/n\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n"
	if _, _, err := BuildPatch(parse(t, ren), Selection{Hunks: []int{0}}, false); err != nil {
		t.Errorf("a renamed file, all of it picked: %v", err)
	}
	two := ren + "@@ -9 +9 @@\n-p\n+q\n"
	if _, _, err := BuildPatch(parse(t, two), Selection{Hunks: []int{0}}, false); err == nil {
		t.Error("staging part of a renamed file should be refused")
	}
	p, _, err = BuildPatch(parse(t, two), Selection{Hunks: []int{0}}, true)
	if err != nil || !strings.HasPrefix(p, "diff --git a/n b/n\n--- a/n\n+++ b/n\n@@ -1,2 +1,2 @@") {
		t.Errorf("unstaging part of a renamed file: %q %v", p, err)
	}
}

func TestSelectionRefused(t *testing.T) {
	fd := parse(t, hdr+"@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n")
	for name, sel := range map[string]Selection{
		"nothing":         {},
		"empty lines":     {Lines: map[int][]int{0: {}}},
		"no such hunk":    {Hunks: []int{3}},
		"negative hunk":   {Hunks: []int{-1}},
		"no such line":    {Lines: map[int][]int{0: {9}}},
		"a context line":  {Lines: map[int][]int{0: {0}}},
		"line of no hunk": {Lines: map[int][]int{2: {1}}},
	} {
		if _, _, err := BuildPatch(fd, sel, false); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	bin, _ := ParseDiff("diff --git a/i b/i\nBinary files a/i and b/i differ\n")
	if _, _, err := BuildPatch(bin, Selection{Hunks: []int{0}}, false); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary: %v", err)
	}
	mode, _ := ParseDiff("diff --git a/s b/s\nold mode 100644\nnew mode 100755\n")
	if _, _, err := BuildPatch(mode, Selection{Hunks: []int{0}}, false); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("mode only: %v", err)
	}
}

func TestFingerprint(t *testing.T) {
	a, b := Fingerprint("x"), Fingerprint("y")
	if a == b || len(a) != 16 || a != Fingerprint("x") {
		t.Errorf("%q %q", a, b)
	}
}
