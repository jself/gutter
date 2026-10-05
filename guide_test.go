package main

import (
	"os"
	"strings"
	"testing"
)

const sampleGuide = `# Review guide

Overview paragraph.

## 1. Renderer

Part narration.

### 1.1 Enable extensions

Step narration with ` + "`code`" + `.

- main.go:23
- main.go:710-716
- not a reference, just a note

### 1.2 Style tables

- index.html:175-182

## 2. Docs

- README.md
- gone.md:5
`

func TestParseGuideStructure(t *testing.T) {
	g, err := parseGuideBytes("g.md", []byte(sampleGuide))
	if err != nil {
		t.Fatal(err)
	}
	if g.Title != "Review guide" {
		t.Errorf("title = %q", g.Title)
	}
	if len(g.Overview) != 1 || !strings.Contains(g.Overview[0].HTML, "Overview paragraph") {
		t.Errorf("overview = %+v", g.Overview)
	}
	if len(g.Parts) != 2 {
		t.Fatalf("parts = %d", len(g.Parts))
	}
	p1 := g.Parts[0]
	if p1.ID != "1" || p1.Title != "1. Renderer" || len(p1.Steps) != 2 {
		t.Errorf("part1 = %+v", p1)
	}
	if len(p1.Blocks) != 1 || !strings.Contains(p1.Blocks[0].HTML, "Part narration") {
		t.Errorf("part1 blocks = %+v", p1.Blocks)
	}
	s := p1.Steps[0]
	if s.ID != "1.1" || s.Level != 3 {
		t.Errorf("step = %+v", s)
	}
	if len(s.Refs) != 2 {
		t.Fatalf("refs = %+v", s.Refs)
	}
	if s.Refs[0].Path != "main.go" || s.Refs[0].Start != 23 || s.Refs[0].End != 23 {
		t.Errorf("ref0 = %+v", s.Refs[0])
	}
	if s.Refs[1].Start != 710 || s.Refs[1].End != 716 || s.Refs[1].Line != 14 {
		t.Errorf("ref1 = %+v", s.Refs[1])
	}
	// The non-reference bullet survives as narration, the refs do not.
	joined := ""
	for _, b := range s.Blocks {
		joined += b.HTML
	}
	if !strings.Contains(joined, "just a note") || strings.Contains(joined, "main.go:23") {
		t.Errorf("step narration = %q", joined)
	}
	p2 := g.Parts[1]
	if len(p2.Steps) != 0 || len(p2.Refs) != 2 || p2.Refs[0].Start != 0 || p2.Refs[0].Path != "README.md" {
		t.Errorf("part2 = %+v", p2)
	}
	if got := len(g.leaves()); got != 3 {
		t.Errorf("leaves = %d", got)
	}
}

func TestParseGuideStepWithoutPart(t *testing.T) {
	g, err := parseGuideBytes("g.md", []byte("### Only a step\n\n- a.go:1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Parts) != 1 || g.Parts[0].Title != "Only a step" || len(g.Parts[0].Refs) != 1 {
		t.Errorf("parts = %+v", g.Parts)
	}
}

func mkFiles() []File {
	return []File{
		{Path: "main.go", Hunks: []Hunk{{
			NewStart: 20,
			Lines: []Line{
				{Kind: "ctx", OldLine: 20, NewLine: 20},
				{Kind: "add", NewLine: 21},
				{Kind: "del", OldLine: 21},
				{Kind: "del", OldLine: 22},
				{Kind: "add", NewLine: 22},
				{Kind: "add", NewLine: 23},
				{Kind: "ctx", OldLine: 23, NewLine: 24},
				{Kind: "del", OldLine: 24},
				{Kind: "ctx", OldLine: 25, NewLine: 25},
			},
		}}},
		{Path: "README.md", Hunks: []Hunk{{
			NewStart: 1,
			Lines: []Line{
				{Kind: "del", OldLine: 1},
				{Kind: "del", OldLine: 2},
			},
		}}},
	}
}

func TestResolveGuideClaims(t *testing.T) {
	guide := "## A\n\n- main.go:22-23\n\n## B\n\n- main.go:25\n\n## C\n\n- README.md\n- missing.go\n- main.go:900\n"
	g, err := parseGuideBytes("g.md", []byte(guide))
	if err != nil {
		t.Fatal(err)
	}
	files := mkFiles()
	resolveGuide(g, files)

	a := g.Parts[0]
	// 22-23 claims adds at 22,23 and the del run (old 21,22) that precedes add 22.
	if len(a.Claims) != 4 {
		t.Errorf("A claims = %v", a.Claims)
	}
	b := g.Parts[1]
	// del at old 24 is followed by ctx new 25 → addressed by 25.
	if len(b.Claims) != 1 || b.Claims[0] != [3]int{0, 0, 7} {
		t.Errorf("B claims = %v", b.Claims)
	}
	c := g.Parts[2]
	if len(c.Claims) != 2 {
		t.Errorf("C claims = %v", c.Claims)
	}
	if g.Dead != 2 {
		t.Errorf("dead = %d", g.Dead)
	}
	if c.Refs[1].Hits != 0 || c.Refs[2].Hits != 0 || c.Refs[0].Hits != 2 {
		t.Errorf("hits = %+v", c.Refs)
	}
	// add at 21 is nobody's → Unassigned.
	if g.Unassigned == nil || len(g.Unassigned.Claims) != 1 || g.Unassigned.Claims[0] != [3]int{0, 0, 1} {
		t.Errorf("unassigned = %+v", g.Unassigned)
	}
	if g.Stops != 4 {
		t.Errorf("stops = %d", g.Stops)
	}
	want := "guide: 3 stop(s), 2 dead reference(s), 1 unassigned line(s) in 1 file(s)"
	if got := g.coverageSummary(); got != want {
		t.Errorf("summary = %q", got)
	}
}

func TestResolveGuideFullCoverage(t *testing.T) {
	g, err := parseGuideBytes("g.md", []byte("## All\n\n- main.go\n- README.md\n"))
	if err != nil {
		t.Fatal(err)
	}
	resolveGuide(g, mkFiles())
	if g.Unassigned != nil || g.Dead != 0 || g.Stops != 1 || len(g.Parts[0].Claims) != 8 {
		t.Errorf("g = %+v claims=%v", g, g.Parts[0].Claims)
	}
}

func TestResolveGuideDeletionAtHunkEnd(t *testing.T) {
	// Pure-deletion file: dels addressed by NewStart (1) since nothing follows.
	g, _ := parseGuideBytes("g.md", []byte("## D\n\n- README.md:1\n"))
	resolveGuide(g, mkFiles())
	if len(g.Parts[0].Claims) != 2 {
		t.Errorf("claims = %v", g.Parts[0].Claims)
	}
}

func TestGuideConfigEnvAndFile(t *testing.T) {
	t.Setenv("GUTTER_GUIDE", "x.md")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if c := loadConfig(); c.Guide != "x.md" {
		t.Errorf("env guide = %q", c.Guide)
	}
	c := defaultConfig()
	dir := t.TempDir()
	p := dir + "/c.json"
	if err := os.WriteFile(p, []byte(`{"guide":"y.md"}`), 0644); err != nil {
		t.Fatal(err)
	}
	mergeConfigFile(&c, p)
	if c.Guide != "y.md" {
		t.Errorf("file guide = %q", c.Guide)
	}
}

func TestGuideFormatTextMentionsRules(t *testing.T) {
	for _, want := range []string{"path:start-end", "NEW-side", "Unassigned", "dead"} {
		if !strings.Contains(guideFormatText, want) {
			t.Errorf("format text missing %q", want)
		}
	}
}
