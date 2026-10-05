package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Guided review: an agent-written markdown "guide" that groups the diff into
// parts (##) and steps (###), each with narration and a list of line-range
// references. gutter overlays the guide on the live diff; anything the guide
// does not claim lands in a synthetic "Unassigned" part so coverage is always
// complete. See docs/superpowers/specs/2026-10-04-guided-review-design.md.

// GuideRef is one `path`, `path:line` or `path:start-end` bullet.
type GuideRef struct {
	Raw   string `json:"raw"`
	Path  string `json:"path"`
	Start int    `json:"start"` // 0 with End 0 = whole file
	End   int    `json:"end"`
	Line  int    `json:"line"` // 1-based source line in the guide file
	Hits  int    `json:"hits"` // changed lines claimed; 0 = dead reference
}

// GuideNode is a part (level 2) or a step (level 3).
type GuideNode struct {
	ID        string       `json:"id"` // "1", "1.2", or "U" for Unassigned
	Title     string       `json:"title"`
	Level     int          `json:"level"`
	LineStart int          `json:"line_start"` // heading line in the guide
	Blocks    []DocBlock   `json:"blocks"`     // narration, excluding the heading
	Refs      []GuideRef   `json:"refs"`
	Steps     []*GuideNode `json:"steps,omitempty"`
	// Claims lists [fileIndex, hunkIndex, lineIndex] for every changed line this
	// node's own references claim (a part's steps claim separately).
	Claims [][3]int `json:"claims"`
}

// Guide is the parsed and (after resolveGuide) matched guide.
type Guide struct {
	Path       string       `json:"path"`
	Title      string       `json:"title"`
	Overview   []DocBlock   `json:"overview"`
	Parts      []*GuideNode `json:"parts"`
	Unassigned *GuideNode   `json:"unassigned,omitempty"`
	Stops      int          `json:"stops"`
	Dead       int          `json:"dead"`
}

// guideRefRe matches a reference bullet: a path with no whitespace or colon,
// optionally followed by :line or :start-end. Backticks around it are allowed.
var guideRefRe = regexp.MustCompile("^`?([^\\s:`]+?)(?::(\\d+)(?:-(\\d+))?)?`?$")

// parseGuide reads and parses a guide file. It does not match against a diff.
func parseGuide(path string) (*Guide, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseGuideBytes(path, src)
}

func parseGuideBytes(path string, src []byte) (*Guide, error) {
	lineStarts := computeLineStarts(src)
	root := mdRenderer.Parser().Parse(text.NewReader(src))
	g := &Guide{Path: path}
	var cur *GuideNode // node receiving narration/refs
	var curPart *GuideNode
	appendBlock := func(n ast.Node) error {
		b, err := docBlockFor(n, src, lineStarts)
		if err != nil {
			return err
		}
		if cur == nil {
			g.Overview = append(g.Overview, b)
		} else {
			cur.Blocks = append(cur.Blocks, b)
		}
		return nil
	}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		switch nd := n.(type) {
		case *ast.Heading:
			title := strings.TrimSpace(string(headingText(nd, src)))
			start, _ := nodeLineRange(nd, lineStarts)
			switch {
			case nd.Level == 1 && cur == nil && g.Title == "":
				g.Title = title
				continue
			case nd.Level == 2:
				curPart = &GuideNode{Title: title, Level: 2, LineStart: start, ID: strconv.Itoa(len(g.Parts) + 1)}
				g.Parts = append(g.Parts, curPart)
				cur = curPart
				continue
			case nd.Level == 3:
				if curPart == nil {
					// A step before any part: promote to an implicit part.
					curPart = &GuideNode{Title: title, Level: 2, LineStart: start, ID: strconv.Itoa(len(g.Parts) + 1)}
					g.Parts = append(g.Parts, curPart)
					cur = curPart
					continue
				}
				step := &GuideNode{Title: title, Level: 3, LineStart: start, ID: fmt.Sprintf("%s.%d", curPart.ID, len(curPart.Steps)+1)}
				curPart.Steps = append(curPart.Steps, step)
				cur = step
				continue
			}
			// Other headings (a second #, #### and deeper) are narration.
			if err := appendBlock(n); err != nil {
				return nil, err
			}
		case *ast.List:
			if cur == nil {
				if err := appendBlock(n); err != nil {
					return nil, err
				}
				continue
			}
			// Pull reference items out of the list; whatever remains is narration.
			var keep []ast.Node
			for item := nd.FirstChild(); item != nil; item = item.NextSibling() {
				if ref, ok := refFromListItem(item, src, lineStarts); ok {
					cur.Refs = append(cur.Refs, ref)
				} else {
					keep = append(keep, item)
				}
			}
			if len(keep) == 0 {
				continue
			}
			if len(keep) < childCount(nd) {
				// Rebuild the list with only the narration items. Line range is
				// derived from the remaining items' segments.
				for item := nd.FirstChild(); item != nil; {
					next := item.NextSibling()
					isKept := false
					for _, k := range keep {
						if k == item {
							isKept = true
							break
						}
					}
					if !isKept {
						nd.RemoveChild(nd, item)
					}
					item = next
				}
			}
			if err := appendBlock(n); err != nil {
				return nil, err
			}
		default:
			if err := appendBlock(n); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

func childCount(n ast.Node) int {
	c := 0
	for x := n.FirstChild(); x != nil; x = x.NextSibling() {
		c++
	}
	return c
}

// headingText collects the raw text of a heading's inline children.
func headingText(h *ast.Heading, src []byte) []byte {
	var b bytes.Buffer
	var visit func(ast.Node)
	visit = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch t := c.(type) {
			case *ast.Text:
				b.Write(t.Segment.Value(src))
			case *ast.CodeSpan:
				for cc := t.FirstChild(); cc != nil; cc = cc.NextSibling() {
					if tt, ok := cc.(*ast.Text); ok {
						b.Write(tt.Segment.Value(src))
					}
				}
			default:
				visit(c)
			}
		}
	}
	visit(h)
	return b.Bytes()
}

// refFromListItem returns the reference if the list item is exactly one
// reference and nothing else (a single paragraph whose text parses).
func refFromListItem(item ast.Node, src []byte, lineStarts []int) (GuideRef, bool) {
	if childCount(item) != 1 {
		return GuideRef{}, false
	}
	para := item.FirstChild()
	if _, ok := para.(*ast.TextBlock); !ok {
		if _, ok := para.(*ast.Paragraph); !ok {
			return GuideRef{}, false
		}
	}
	var b bytes.Buffer
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
		case *ast.CodeSpan:
			for cc := t.FirstChild(); cc != nil; cc = cc.NextSibling() {
				if tt, ok := cc.(*ast.Text); ok {
					b.Write(tt.Segment.Value(src))
				}
			}
		default:
			return GuideRef{}, false
		}
	}
	raw := strings.TrimSpace(b.String())
	m := guideRefRe.FindStringSubmatch(raw)
	if m == nil || strings.Contains(m[1], " ") {
		return GuideRef{}, false
	}
	ref := GuideRef{Raw: raw, Path: m[1]}
	if m[2] != "" {
		ref.Start, _ = strconv.Atoi(m[2])
		ref.End = ref.Start
		if m[3] != "" {
			ref.End, _ = strconv.Atoi(m[3])
			if ref.End < ref.Start {
				ref.Start, ref.End = ref.End, ref.Start
			}
		}
	}
	ref.Line, _ = nodeLineRange(item, lineStarts)
	return ref, true
}

// docBlockFor renders one top-level markdown node into a DocBlock.
func docBlockFor(n ast.Node, src []byte, lineStarts []int) (DocBlock, error) {
	start, end := nodeLineRange(n, lineStarts)
	var buf bytes.Buffer
	if err := mdRenderer.Renderer().Render(&buf, src, n); err != nil {
		return DocBlock{}, err
	}
	source := ""
	if start >= 1 && start <= len(lineStarts) {
		s := lineStarts[start-1]
		e := len(src)
		if end < len(lineStarts) {
			e = lineStarts[end]
		}
		source = strings.TrimRight(string(src[s:e]), "\r\n")
	}
	return DocBlock{HTML: buf.String(), LineStart: start, LineEnd: end, Source: source}, nil
}

// stops returns every part and step in walk order. A part is a stop of its
// own (its narration, direct references, and the list of its steps) so the
// walk reads part → its steps → next part.
func (g *Guide) stops() []*GuideNode {
	var out []*GuideNode
	for _, p := range g.Parts {
		out = append(out, p)
		out = append(out, p.Steps...)
	}
	return out
}

// resolveGuide matches every reference against the parsed diff, filling in
// Claims, Hits, Dead, Stops and the Unassigned node.
func resolveGuide(g *Guide, files []File) {
	fileIdx := map[string]int{}
	for i, f := range files {
		fileIdx[f.Path] = i
	}
	claimed := map[[3]int]bool{}
	g.Dead = 0
	stops := g.stops()
	g.Stops = len(stops)
	for _, node := range stops {
		node.Claims = nil
		seen := map[[3]int]bool{}
		for ri := range node.Refs {
			ref := &node.Refs[ri]
			ref.Hits = 0
			fi, ok := fileIdx[ref.Path]
			if !ok {
				g.Dead++
				continue
			}
			for hi, h := range files[fi].Hunks {
				for li, l := range h.Lines {
					if l.Kind == "ctx" || !refClaims(*ref, h, li) {
						continue
					}
					key := [3]int{fi, hi, li}
					ref.Hits++
					claimed[key] = true
					if !seen[key] {
						seen[key] = true
						node.Claims = append(node.Claims, key)
					}
				}
			}
			if ref.Hits == 0 {
				g.Dead++
			}
		}
		if node.Claims == nil {
			node.Claims = [][3]int{}
		}
	}
	un := &GuideNode{ID: "U", Title: "Unassigned", Level: 2, Claims: [][3]int{}}
	for fi, f := range files {
		for hi, h := range f.Hunks {
			for li, l := range h.Lines {
				if l.Kind == "ctx" {
					continue
				}
				key := [3]int{fi, hi, li}
				if !claimed[key] {
					un.Claims = append(un.Claims, key)
				}
			}
		}
	}
	if len(un.Claims) > 0 {
		g.Unassigned = un
		g.Stops++
	} else {
		g.Unassigned = nil
	}
}

// refClaims reports whether ref claims the changed line at h.Lines[li].
// Added lines match on their new-side number. A deleted line is addressed by
// the new-side line that now follows it (the first non-deleted line after the
// deletion run, or one past the hunk's last new line if the run ends the hunk).
func refClaims(ref GuideRef, h Hunk, li int) bool {
	if ref.Start == 0 && ref.End == 0 {
		return true
	}
	l := h.Lines[li]
	var pos int
	switch l.Kind {
	case "add":
		pos = l.NewLine
	case "del":
		pos = -1
		for j := li + 1; j < len(h.Lines); j++ {
			if h.Lines[j].Kind != "del" {
				pos = h.Lines[j].NewLine
				break
			}
		}
		if pos == -1 {
			last := 0
			for j := range h.Lines {
				if h.Lines[j].NewLine > last {
					last = h.Lines[j].NewLine
				}
			}
			if last == 0 {
				last = h.NewStart - 1
			}
			pos = last + 1
		}
	default:
		return false
	}
	return pos >= ref.Start && pos <= ref.End
}

// coverageSummary is the one-line startup report for the agent.
func (g *Guide) coverageSummary() string {
	unLines, unFiles := 0, 0
	if g.Unassigned != nil {
		unLines = len(g.Unassigned.Claims)
		seen := map[int]bool{}
		for _, c := range g.Unassigned.Claims {
			seen[c[0]] = true
		}
		unFiles = len(seen)
	}
	stops := g.Stops
	if g.Unassigned != nil {
		stops--
	}
	return fmt.Sprintf("guide: %d stop(s), %d dead reference(s), %d unassigned line(s) in %d file(s)", stops, g.Dead, unLines, unFiles)
}

const guideFormatText = `gutter review guide format
==========================

A guide is a markdown file (default .claude/review-guide.md, or -guide <file>)
that splits a diff into parts and steps so a reviewer can walk the change in
order with narration. gutter overlays it on the live diff.

Template
--------

    # Review guide

    One paragraph: what the change does as a whole and why.

    ## 1. Renderer

    Optional part narration. A part groups related steps.

    ### 1.1 Enable GFM extensions

    What this step changes, why, and what to look at closely.
    Paragraphs, lists and code spans are fine here.

    - main.go:23
    - main.go:710-716

    ### 1.2 Style tables in the doc view

    - index.html:175-182

    ## 2. Docs

    - README.md

Rules
-----

- "# " title and the text under it are the overview.
- "## " is a part. "### " is a step inside it. Both are stops: the part's page
  shows its narration and lists its steps, then each step follows. Give every
  part a sentence or two of narration. Deeper headings are just formatting.
- A reference is a list bullet whose whole text is one of:
      path              every changed line in the file (also how you claim a
                        file that was deleted outright)
      path:line         one line
      path:start-end    a range
  Line numbers are NEW-side numbers (the file after the change), exactly as
  gutter's review.md addresses code. Paths are as the diff prints them,
  relative to the repo root.
- A deleted line is addressed by the new-side line that now follows it. So a
  block deleted between what are now lines 40 and 41 belongs to a range that
  includes 41.
- Cut ranges at function, class or block boundaries, never mid-function. A
  range that ends inside a function splits it across two steps and the
  reviewer sees a fold in the middle of the code. Read the diff and pick the
  blank line between declarations, not a round number.
- References attach to the heading they sit under. A bullet that does not
  parse as a reference is ordinary narration, not an error.
- Steps with no references are allowed (pure context).
- Context lines never count. Only added and removed lines matter.

Coverage
--------

Every changed line must belong to a stop. Lines no step claims are collected
into a trailing "Unassigned" part and flagged. A reference that matches no
changed line is kept and flagged as dead. On startup gutter prints

    guide: N stop(s), D dead reference(s), U unassigned line(s) in F file(s)

Run gutter, read that line, and fix the guide until D and U are 0.
`
