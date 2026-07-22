package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePRView(t *testing.T) {
	in := []byte(`{
		"number": 123,
		"headRefOid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"baseRefOid": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"url": "https://github.com/octo/widgets/pull/123"
	}`)
	got, err := parsePRView(in)
	if err != nil {
		t.Fatalf("parsePRView: %v", err)
	}
	want := PRInfo{
		Repo:   "octo/widgets",
		Number: 123,
		Head:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Base:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParsePRViewBadURL(t *testing.T) {
	in := []byte(`{"number":1,"headRefOid":"x","baseRefOid":"y","url":"not-a-url"}`)
	if _, err := parsePRView(in); err == nil {
		t.Fatal("expected error for unparseable url, got nil")
	}
}

func TestRenderMarkdownPRBlock(t *testing.T) {
	pr := &PRInfo{Repo: "octo/widgets", Number: 123, Head: "abc123", Base: "def456"}
	req := SaveRequest{
		General: "Looks good overall.",
		Comments: []Comment{
			{Path: "a.go", Side: "new", Line: 10, Body: "new-side note"},
			{Path: "b.go", Side: "old", Line: 5, Body: "old-side note"},
		},
	}
	md := renderMarkdown("", "git", "", false, pr, req)

	for _, want := range []string{
		"# Review of PR #123 (github)",
		"## PR",
		"- repo: octo/widgets",
		"- number: 123",
		"- head: abc123",
		"- base: def456",
		"local working tree is NOT the PR's code",
		"gh pr diff 123",
		"### a.go:10\n",       // new side: no LEFT suffix
		"### b.go:5 (LEFT)\n", // old side: LEFT suffix
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestLoadPriorRoundTripSide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")

	pr := &PRInfo{Repo: "octo/widgets", Number: 7, Head: "h", Base: "b"}
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 10, Body: "new note"},
		{Path: "b.go", Side: "old", Line: 5, Body: "old note"},
	}}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", "", false, pr, req)), 0644); err != nil {
		t.Fatal(err)
	}

	got, _ := loadPrior(path)
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2: %+v", len(got), got)
	}
	if got[0].Path != "a.go" || got[0].Line != 10 || got[0].Side != "new" {
		t.Errorf("comment 0 = %+v, want a.go:10 side new", got[0])
	}
	if got[1].Path != "b.go" || got[1].Line != 5 || got[1].Side != "old" {
		t.Errorf("comment 1 = %+v, want b.go:5 side old", got[1])
	}
}

func TestLoadPriorLegacyNoSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	legacy := "# Review of `@` (jj)\n\n## Inline comments\n\n### a.go:10-12\n\nbody here\n\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Side != "new" || got[0].Line != 10 || got[0].EndLine != 12 {
		t.Fatalf("legacy parse = %+v, want a.go:10-12 side new", got)
	}
}

func TestRenderMarkdownLocalUnchanged(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 10, Body: "x"}}}
	md := renderMarkdown("@", "jj", "", false, nil, req)
	if strings.Contains(md, "## PR") {
		t.Errorf("local review should not contain a PR block:\n%s", md)
	}
	if !strings.Contains(md, "# Review of `@` (jj)") {
		t.Errorf("local title changed:\n%s", md)
	}
	if !strings.Contains(md, "### a.go:10\n") {
		t.Errorf("new-side anchor should have no LEFT suffix:\n%s", md)
	}
}

func TestRenderMarkdownLocalOldSideNoSuffix(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "old", Line: 3, Body: "del note"}}}
	md := renderMarkdown("@", "jj", "", false, nil, req)
	if strings.Contains(md, "(LEFT)") {
		t.Errorf("local review should not emit LEFT suffix:\n%s", md)
	}
}

func TestRenderMarkdownDocTitle(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "plan.md", Side: "new", Line: 5, Body: "note"}}}
	md := renderMarkdown("", "git", "docs/plan.md", false, nil, req)
	if !strings.Contains(md, "# Review of docs/plan.md\n") {
		t.Errorf("doc title missing:\n%s", md)
	}
	if strings.Contains(md, "(git)") {
		t.Errorf("doc title should not include vcs:\n%s", md)
	}
	// Non-doc path unchanged
	md2 := renderMarkdown("@", "jj", "", false, nil, req)
	if !strings.Contains(md2, "# Review of `@` (jj)") {
		t.Errorf("non-doc title changed:\n%s", md2)
	}
}

func TestRepoFromPRURLDegenerateNoOwner(t *testing.T) {
	// URL with no owner segment — "github.com" ends up as the name, empty string as owner.
	if _, err := repoFromPRURL("https://github.com/pull/123"); err == nil {
		t.Fatal("expected error for degenerate url with no owner, got nil")
	}
}

func TestLoadPriorIgnoresPRBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	pr := &PRInfo{Repo: "octo/widgets", Number: 5, Head: "h", Base: "b"}
	req := SaveRequest{
		General:  "overall note",
		Comments: []Comment{{Path: "c.go", Side: "new", Line: 7, Body: "inline note"}},
	}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", "", false, pr, req)), 0644); err != nil {
		t.Fatal(err)
	}
	got, gen := loadPrior(path)
	if gen != "overall note" {
		t.Errorf("general feedback = %q, want %q", gen, "overall note")
	}
	if len(got) != 1 || got[0].Path != "c.go" || got[0].Line != 7 {
		t.Errorf("comments = %+v, want one c.go:7 comment", got)
	}
}

func TestMergeConfigFileSyncOR(t *testing.T) {
	dir := t.TempDir()

	// A file with sync:true sets it.
	p1 := filepath.Join(dir, "on.json")
	if err := os.WriteFile(p1, []byte(`{"sync":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	c := Config{Sync: false}
	mergeConfigFile(&c, p1)
	if !c.Sync {
		t.Errorf("sync:true in file should set Sync, got false")
	}

	// A file WITHOUT the sync key must not clear an already-true value.
	p2 := filepath.Join(dir, "other.json")
	if err := os.WriteFile(p2, []byte(`{"port":123}`), 0644); err != nil {
		t.Fatal(err)
	}
	c2 := Config{Sync: true}
	mergeConfigFile(&c2, p2)
	if !c2.Sync {
		t.Errorf("missing sync key must not clear Sync; got false")
	}
}

func TestLoadConfigSyncEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from real user config
	t.Setenv("GUTTER_SYNC", "1")
	if got := loadConfig(); !got.Sync {
		t.Errorf("GUTTER_SYNC=1 should yield Sync=true")
	}
	t.Setenv("GUTTER_SYNC", "false")
	if got := loadConfig(); got.Sync {
		t.Errorf("GUTTER_SYNC=false should yield Sync=false")
	}
}

func TestLoadConfigMDEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GUTTER_MD", "plan.md")
	if got := loadConfig(); got.MD != "plan.md" {
		t.Errorf("GUTTER_MD should set MD, got %q", got.MD)
	}
}

func TestMergeConfigFileMD(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(`{"md":"spec.md"}`), 0644); err != nil {
		t.Fatal(err)
	}
	c := Config{}
	mergeConfigFile(&c, p)
	if c.MD != "spec.md" {
		t.Errorf("md from file should set MD, got %q", c.MD)
	}
}

func TestRenderDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	src := "# Title\n\nA paragraph.\n\n```go\nx := 1\n```\n\n- item one\n- item two\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := renderDoc(path)
	if err != nil {
		t.Fatalf("renderDoc: %v", err)
	}
	if doc.Path != path {
		t.Errorf("Path = %q, want %q", doc.Path, path)
	}
	if len(doc.Blocks) != 4 {
		t.Fatalf("got %d blocks, want 4: %+v", len(doc.Blocks), doc.Blocks)
	}
	// Heading on line 1
	if doc.Blocks[0].LineStart != 1 || doc.Blocks[0].LineEnd != 1 {
		t.Errorf("heading range = %d-%d, want 1-1", doc.Blocks[0].LineStart, doc.Blocks[0].LineEnd)
	}
	// Paragraph on line 3
	if doc.Blocks[1].LineStart != 3 || doc.Blocks[1].LineEnd != 3 {
		t.Errorf("paragraph range = %d-%d, want 3-3", doc.Blocks[1].LineStart, doc.Blocks[1].LineEnd)
	}
	// Fenced code lives within lines 5-7 (goldmark anchors to content; don't over-assert)
	if doc.Blocks[2].LineStart < 5 || doc.Blocks[2].LineEnd > 7 {
		t.Errorf("code range = %d-%d, want within 5-7", doc.Blocks[2].LineStart, doc.Blocks[2].LineEnd)
	}
	// List spans lines 9-10
	if doc.Blocks[3].LineStart != 9 || doc.Blocks[3].LineEnd != 10 {
		t.Errorf("list range = %d-%d, want 9-10", doc.Blocks[3].LineStart, doc.Blocks[3].LineEnd)
	}
	// Every block has rendered HTML and source text
	for i, b := range doc.Blocks {
		if strings.TrimSpace(b.HTML) == "" {
			t.Errorf("block %d has empty HTML", i)
		}
		if strings.TrimSpace(b.Source) == "" {
			t.Errorf("block %d has empty Source", i)
		}
	}
	// Rendered HTML is real markdown output
	if !strings.Contains(doc.Blocks[0].HTML, "<h1") {
		t.Errorf("heading HTML = %q, want an <h1>", doc.Blocks[0].HTML)
	}
}

func TestRenderDocMissingFile(t *testing.T) {
	if _, err := renderDoc(filepath.Join(t.TempDir(), "nope.md")); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestRenderMarkdownSeverity(t *testing.T) {
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Body: "x"},
		{Path: "b.go", Side: "new", Line: 8, Body: "y"}, // empty -> QUESTION
		{Path: "c.go", Side: "new", Line: 10, EndLine: 12, Severity: "SUGGESTION", Body: "z"},
	}}
	md := renderMarkdown("", "git", "", true, nil, req)
	for _, want := range []string{
		"### a.go:5 [IMPORTANT]\n",
		"### b.go:8 [QUESTION]\n",
		"### c.go:10-12 [SUGGESTION]\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n---\n%s", want, md)
		}
	}
}

func TestRenderMarkdownSeverityAfterLeft(t *testing.T) {
	pr := &PRInfo{Repo: "o/r", Number: 1, Head: "h", Base: "b"}
	req := SaveRequest{Comments: []Comment{{Path: "c.go", Side: "old", Line: 7, Severity: "NITPICK", Body: "z"}}}
	md := renderMarkdown("", "git", "", true, pr, req)
	if !strings.Contains(md, "### c.go:7 (LEFT) [NITPICK]\n") {
		t.Errorf("severity should follow (LEFT):\n%s", md)
	}
}

func TestRenderMarkdownSeverityOff(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Body: "x"}}}
	md := renderMarkdown("", "git", "", false, nil, req)
	if !strings.Contains(md, "### a.go:5\n") {
		t.Errorf("flag off must emit a bare heading:\n%s", md)
	}
	if strings.Contains(md, "[") {
		t.Errorf("flag off must not emit any [SEVERITY] token:\n%s", md)
	}
}

func TestLoadPriorSeverityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Severity: "SUGGESTION", Body: "s"}}}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", "", true, nil, req)), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Severity != "SUGGESTION" {
		t.Fatalf("round-trip severity = %+v, want SUGGESTION", got)
	}
}

func TestLoadPriorSeverityLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	legacy := "# Review of `@` (jj)\n\n## Inline comments\n\n### a.go:5\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Severity != "QUESTION" {
		t.Fatalf("legacy severity = %+v, want QUESTION default", got)
	}
}

func TestLoadPriorSeverityWithLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	src := "# R\n\n## Inline comments\n\n### a.go:7 (LEFT) [NITPICK]\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Side != "old" || got[0].Severity != "NITPICK" {
		t.Fatalf("got %+v, want side=old severity=NITPICK", got)
	}
}

func TestLoadConfigWindowEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GUTTER_WINDOW", "1")
	if got := loadConfig(); !got.Window {
		t.Errorf("GUTTER_WINDOW=1 should set Window")
	}
	t.Setenv("GUTTER_WINDOW", "false")
	if got := loadConfig(); got.Window {
		t.Errorf("GUTTER_WINDOW=false should be false")
	}
}

func TestMergeConfigFileWindowOR(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(`{"window":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	c := Config{Window: false}
	mergeConfigFile(&c, p)
	if !c.Window {
		t.Errorf("window:true in file should set Window")
	}
	p2 := filepath.Join(dir, "c2.json")
	if err := os.WriteFile(p2, []byte(`{"port":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	c2 := Config{Window: true}
	mergeConfigFile(&c2, p2)
	if !c2.Window {
		t.Errorf("missing window key must not clear Window")
	}
}

func TestFontAssetServed(t *testing.T) {
	req := httptest.NewRequest("GET", "/assets/jetbrains-mono.woff2", nil)
	w := httptest.NewRecorder()
	fontHandler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Errorf("Content-Type = %q, want font/woff2", ct)
	}
	if w.Body.Len() < 1000 {
		t.Errorf("body too small: %d bytes", w.Body.Len())
	}
}
