package main

import (
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
	md := renderMarkdown("", "git", pr, req)

	for _, want := range []string{
		"# Review of PR #123 (github)",
		"## PR",
		"- repo: octo/widgets",
		"- number: 123",
		"- head: abc123",
		"- base: def456",
		"local working tree is NOT the PR's code",
		"gh pr diff 123",
		"### a.go:10\n",        // new side: no suffix
		"### b.go:5 (LEFT)\n",  // old side: suffix
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
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", pr, req)), 0644); err != nil {
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
	md := renderMarkdown("@", "jj", nil, req)
	if strings.Contains(md, "## PR") {
		t.Errorf("local review should not contain a PR block:\n%s", md)
	}
	if !strings.Contains(md, "# Review of `@` (jj)") {
		t.Errorf("local title changed:\n%s", md)
	}
	if !strings.Contains(md, "### a.go:10\n") {
		t.Errorf("new-side anchor should have no suffix:\n%s", md)
	}
}
