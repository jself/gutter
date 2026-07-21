# Markdown Document Review (`-md`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gutter -md <file>`: render a whole markdown file as formatted HTML and let the user comment on it block-by-block, emitting the existing `review.md` format. Combined with `-sync`, this is an agent plan-review loop.

**Architecture:** A new "doc mode" parallel to diff mode. goldmark parses the file server-side; gutter walks top-level block nodes, rendering each to an HTML fragment with its source line range. `DiffData` carries a `Doc` instead of `Files`; the UI renders a document view where clicking a block opens the same comment editor, anchored to the block's source lines. Comments reuse the `### file:line-end` format and round-trip.

**Tech Stack:** Go (single `main.go`), goldmark (new dependency), embedded `index.html` (Go html/template), standard-library testing.

## Global Constraints

- **All production Go code stays in `main.go`; tests in `main_test.go`; UI in `index.html`.**
- **New dependency: `github.com/yuin/goldmark` v1.8.4.** This is gutter's first Go dependency and bumps the `go.mod` `go` directive from `1.16` to `1.19` (goldmark v1.8 requires Go ≥ 1.19). No other runtime dependencies; the UI still uses only CDN highlight.js.
- **Config precedence (highest wins):** CLI flag → env (`GUTTER_*`) → `./.gutter.json` → user config → `defaultConfig()`. A new flag adds the matching env var and JSON field.
- **Doc mode composes with `-sync`** (review → stdout, no file) **and non-sync** (`review.md`) identically to diff mode.
- **Comment format is unchanged:** a doc comment is a `Comment{Path:<md file>, Side:"new", Line, EndLine, Snippet, Body}`; `review.md` keeps `### path:line-end`; `loadPrior` is untouched.
- **No diff caching:** `/diff` recomputes per request (re-reads + re-renders the file in doc mode).
- **Startup banner always goes to stderr** (existing rule); doc mode adds a `doc:  <path>` banner line there.

---

## File Structure

- **Modify `go.mod` / `go.sum`:** add goldmark, bump `go` directive to 1.19.
- **Modify `main.go`:**
  - New `Doc`/`DocBlock` types; `renderDoc` + helpers (`computeLineStarts`, `offsetToLine`, `nodeLineRange`).
  - `Config.MD` + `loadConfig` env + `mergeConfigFile` + `-md` flag.
  - `renderMarkdown` gains a `docPath` param (doc-aware title); 3 callers updated.
  - `DiffData.Doc`; `computeData` doc branch; doc-mode banner + `-md`-beats-`-pr` warning; template `Doc` var + doc-mode header value.
- **Modify `index.html`:** doc-view CSS; dispatch branches in `render()`/`renderComments()`/`renderSidebar()`; `renderDocView`, `openDocCommentForm`, `renderDocComments`, `renderOutline`; header label gate.
- **Modify `main_test.go`:** unit tests for `renderDoc`, `md` config precedence, and `renderMarkdown` doc title.
- **Modify `README.md`:** document `-md`.

---

## Task 1: goldmark dependency + `renderDoc` (block extraction with source lines)

The foundational, pure, testable core: parse markdown and produce blocks with correct source line ranges. This is the load-bearing logic.

**Files:**
- Modify: `go.mod`, `go.sum` (add dep, bump go directive)
- Modify: `main.go` (types + renderDoc + helpers; add imports)
- Test: `main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type DocBlock struct { HTML string; LineStart int; LineEnd int; Source string }` with JSON tags `html`, `line_start`, `line_end`, `source`.
  - `type Doc struct { Path string; Blocks []DocBlock }` with JSON tags `path`, `blocks`.
  - `func renderDoc(path string) (Doc, error)`.

- [ ] **Step 1: Add the goldmark dependency and bump the go directive**

Run:
```bash
go get github.com/yuin/goldmark@v1.8.4
```
Then ensure `go.mod`'s `go` directive is at least `1.19` — open `go.mod`; if it still says `go 1.16`, change that line to `go 1.19`. Then:
```bash
go mod tidy
```
Expected: `go.mod` now requires `github.com/yuin/goldmark v1.8.4` and says `go 1.19`; `go.sum` has goldmark entries.

- [ ] **Step 2: Write the failing test**

Add to `main_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./... -run TestRenderDoc -v`
Expected: FAIL — `undefined: renderDoc` / `undefined: Doc`.

- [ ] **Step 4: Add the types and imports**

In `main.go`, add these imports to the existing import block: `"github.com/yuin/goldmark"`, `"github.com/yuin/goldmark/ast"`, `"github.com/yuin/goldmark/text"`. (`bytes`, `os`, `strings` are already imported.)

Add the types near `DiffData` (after the `DiffData` struct, ~line 190):

```go
type DocBlock struct {
	HTML      string `json:"html"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Source    string `json:"source"`
}

type Doc struct {
	Path   string     `json:"path"`
	Blocks []DocBlock `json:"blocks"`
}
```

- [ ] **Step 5: Implement `renderDoc` and helpers**

Add to `main.go` (e.g. after the diff helpers, near `getDiff`):

```go
var mdRenderer = goldmark.New()

// renderDoc parses a markdown file and returns its top-level blocks, each with
// its rendered HTML fragment and 1-based source line range.
func renderDoc(path string) (Doc, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	lineStarts := computeLineStarts(src)
	root := mdRenderer.Parser().Parse(text.NewReader(src))
	var blocks []DocBlock
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		start, end := nodeLineRange(n, lineStarts)
		var buf bytes.Buffer
		if err := mdRenderer.Renderer().Render(&buf, src, n); err != nil {
			return Doc{}, err
		}
		source := ""
		if start >= 1 && start <= len(lineStarts) {
			s := lineStarts[start-1]
			e := len(src)
			if end < len(lineStarts) {
				e = lineStarts[end] // start of the line after `end`
			}
			source = strings.TrimRight(string(src[s:e]), "\n")
		}
		blocks = append(blocks, DocBlock{
			HTML:      buf.String(),
			LineStart: start,
			LineEnd:   end,
			Source:    source,
		})
	}
	return Doc{Path: path, Blocks: blocks}, nil
}

// computeLineStarts returns the byte offset at which each source line begins.
func computeLineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetToLine maps a byte offset to its 1-based line number.
func offsetToLine(lineStarts []int, off int) int {
	lo, hi := 0, len(lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// nodeLineRange returns the 1-based source line span covered by a block node,
// derived from the text segments of the node and its descendants. Nodes with no
// text segments (e.g. thematic breaks) fall back to line 1.
func nodeLineRange(n ast.Node, lineStarts []int) (int, int) {
	minOff, maxOff := -1, -1
	var visit func(ast.Node)
	visit = func(nd ast.Node) {
		if lines := nd.Lines(); lines != nil && lines.Len() > 0 {
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				if minOff == -1 || seg.Start < minOff {
					minOff = seg.Start
				}
				if seg.Stop > maxOff {
					maxOff = seg.Stop
				}
			}
		}
		for c := nd.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(n)
	if minOff == -1 {
		return 1, 1
	}
	endOff := maxOff - 1
	if endOff < minOff {
		endOff = minOff
	}
	return offsetToLine(lineStarts, minOff), offsetToLine(lineStarts, endOff)
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./... -run TestRenderDoc -v && go build ./... && go vet ./...`
Expected: both tests PASS; clean build and vet.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum main.go main_test.go
git commit -m "Add goldmark and renderDoc: markdown blocks with source line ranges"
```

---

## Task 2: `md` config plumbing

Wire `-md` through the config-precedence chain.

**Files:**
- Modify: `main.go` — `Config`, `loadConfig` env, `mergeConfigFile`, flag block in `main`
- Test: `main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `cfg.MD string`; `-md` flag bound to `md *string`; `GUTTER_MD` env; `md` JSON field.

- [ ] **Step 1: Write the failing test**

Add to `main_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestLoadConfigMDEnv|TestMergeConfigFileMD' -v`
Expected: FAIL — `Config.MD` undefined.

- [ ] **Step 3: Add the config field**

In the `Config` struct, after `Sync`:

```go
	MD       string `json:"md,omitempty"`
```

- [ ] **Step 4: Add the env override**

In `loadConfig`, after the `GUTTER_SYNC` block:

```go
	if v := os.Getenv("GUTTER_MD"); v != "" {
		c.MD = v
	}
```

- [ ] **Step 5: Add the JSON merge**

In `mergeConfigFile`, after the `Sync` merge:

```go
	if f.MD != "" {
		c.MD = f.MD
	}
```

- [ ] **Step 6: Add the flag**

In `main`'s flag block, after `sync`:

```go
		md        = flag.String("md", cfg.MD, "review a markdown file as a rendered document (compose with -sync)")
```

`md` is unused until Task 4. Add `_ = md` right after `flag.Parse()` to keep the build green; Task 4 removes it.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./... -run 'TestLoadConfigMDEnv|TestMergeConfigFileMD' -v && go build ./... && go vet ./...`
Expected: PASS; clean build/vet.

- [ ] **Step 8: Commit**

```bash
git add main.go main_test.go
git commit -m "Add -md flag, GUTTER_MD env, and md config field"
```

---

## Task 3: `renderMarkdown` doc-aware title

Give `renderMarkdown` a `docPath` param so doc reviews get a `# Review of <path>` title. Signature change, so all callers are updated in this task.

**Files:**
- Modify: `main.go` — `renderMarkdown` (~line 745) and its three callers (`/save`, `/markdown`, and the sync submit path — grep `renderMarkdown(`)
- Test: `main_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func renderMarkdown(rev, vcs, docPath string, pr *PRInfo, req SaveRequest) string` — when `docPath != ""`, the title is `# Review of <docPath>`; otherwise unchanged.

- [ ] **Step 1: Write the failing test**

Add to `main_test.go`:

```go
func TestRenderMarkdownDocTitle(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "plan.md", Side: "new", Line: 5, Body: "note"}}}
	md := renderMarkdown("", "git", "docs/plan.md", nil, req)
	if !strings.Contains(md, "# Review of docs/plan.md\n") {
		t.Errorf("doc title missing:\n%s", md)
	}
	if strings.Contains(md, "(git)") {
		t.Errorf("doc title should not include vcs:\n%s", md)
	}
	// Non-doc path unchanged
	md2 := renderMarkdown("@", "jj", "", nil, req)
	if !strings.Contains(md2, "# Review of `@` (jj)") {
		t.Errorf("non-doc title changed:\n%s", md2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestRenderMarkdownDocTitle -v`
Expected: FAIL — too many arguments to `renderMarkdown` (compile error).

- [ ] **Step 3: Update `renderMarkdown`**

Change the signature and the title block at the top of `renderMarkdown`. Current start:

```go
func renderMarkdown(rev, vcs string, pr *PRInfo, req SaveRequest) string {
	var b strings.Builder
	if pr != nil {
		fmt.Fprintf(&b, "# Review of PR #%d (github)\n\n", pr.Number)
```

Change to:

```go
func renderMarkdown(rev, vcs, docPath string, pr *PRInfo, req SaveRequest) string {
	var b strings.Builder
	if docPath != "" {
		fmt.Fprintf(&b, "# Review of %s\n\n", docPath)
	} else if pr != nil {
		fmt.Fprintf(&b, "# Review of PR #%d (github)\n\n", pr.Number)
```

(The existing `if pr != nil { … } else { … }` becomes `if docPath != "" { … } else if pr != nil { … } else { … }`. Leave the PR block and the final `else` git/jj title block otherwise unchanged, and everything below the title unchanged.)

- [ ] **Step 4: Update the three callers to pass `""`**

Each existing `renderMarkdown(*rev, vcs, prInfo, req)` becomes `renderMarkdown(*rev, vcs, "", prInfo, req)`. There are three (the `/save`, `/markdown`, and `/submit` handlers). Task 4 changes them to pass the real doc path.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run TestRenderMarkdownDocTitle -v && go build ./...`
Expected: PASS; clean build.

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go
git commit -m "renderMarkdown: doc-aware title via docPath param"
```

---

## Task 4: Server wiring — doc mode

Populate `DiffData.Doc` in doc mode, add the doc banner + `-md`-beats-`-pr` warning, pass doc state to the template, and thread the doc path into the review output.

**Files:**
- Modify: `main.go` — `DiffData` (~line 183); `main` (`prInfo`/`md` handling, `computeData` ~line 309, template map, banner block, the three `renderMarkdown` calls)

**Interfaces:**
- Consumes: `renderDoc`, `Doc` (Task 1); `*md` (Task 2); `renderMarkdown(rev, vcs, docPath, pr, req)` (Task 3).
- Produces: `DiffData.Doc *Doc`; doc-mode `computeData`; template keys `"Doc"`; a `docPath` string used by the review handlers.

- [ ] **Step 1: Remove the Task 2 placeholder**

Delete the `_ = md` line added in Task 2 Step 6.

- [ ] **Step 2: Add the `Doc` field to `DiffData`**

In the `DiffData` struct, after `PR`:

```go
	Doc      *Doc      `json:"doc,omitempty"`
```

- [ ] **Step 3: Resolve doc path once and warn on `-md`+`-pr`**

In `main`, right after `prInfo` is set up (after the PR-fetch block, before `outPath`), add:

```go
	docPath := *md
	if docPath != "" && prInfo != nil {
		fmt.Fprintln(os.Stderr, "note: -md set; ignoring -pr")
		prInfo = nil
	}
```

- [ ] **Step 4: Branch `computeData` for doc mode**

At the very top of the `computeData` closure body (before the existing `var ( diff … )` block), add the doc branch:

```go
	computeData := func() (DiffData, error) {
		if docPath != "" {
			doc, err := renderDoc(docPath)
			if err != nil {
				return DiffData{}, fmt.Errorf("rendering doc: %w", err)
			}
			priorComments, priorGen := loadPrior(outAbs)
			return DiffData{Rev: docPath, VCS: "doc", Doc: &doc, Prior: priorComments, PriorGen: priorGen}, nil
		}
		var (
			diff           string
			untrackedPaths map[string]bool
		)
		// ...existing diff/PR body unchanged...
```

(Leave the rest of `computeData` exactly as-is.)

- [ ] **Step 5: Doc-aware header template values**

Find where `displayHdrRev`/`displayHdrVCS` are computed (the block before the `/` handler). Add a doc branch so the header shows the path:

```go
	displayHdrRev := *rev
	displayHdrVCS := vcs
	if docPath != "" {
		displayHdrRev = docPath
		displayHdrVCS = "doc"
	} else if prInfo != nil {
		displayHdrRev = fmt.Sprintf("PR #%d", prInfo.Number)
		displayHdrVCS = "github"
	}
```

In the `/` handler's `tmpl.Execute` map, add:

```go
			"Doc":       docPath != "",
```

- [ ] **Step 6: Thread the doc path into the review handlers**

In the `/save`, `/markdown`, and `/submit` handlers, change `renderMarkdown(*rev, vcs, "", prInfo, req)` to `renderMarkdown(*rev, vcs, docPath, prInfo, req)`.

- [ ] **Step 7: Doc-mode startup banner**

In the startup banner block, add a doc line. After the `if prInfo != nil { … } else { … }` rev/pr block, add:

```go
	if docPath != "" {
		fmt.Fprintln(infoW, "doc:      ", docPath)
	}
```

(Place it so it prints in doc mode; the `rev:` else-branch will still print `(working tree)` because `*rev` is empty in doc mode — that's acceptable noise on stderr, but to keep it clean, guard the existing `rev:` print with `else if docPath == ""`. Concretely: change the final `else {` of the pr/rev block to `else if docPath == "" {` so the `rev:` line is skipped in doc mode.)

- [ ] **Step 8: Build and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean build/vet; all existing tests pass.

- [ ] **Step 9: Commit**

```bash
git add main.go
git commit -m "Wire doc mode: computeData renderDoc branch, header, banner, output"
```

---

## Task 5: UI — document view

Render the doc as commentable blocks, with the same comment editor and saved-comment display, dispatched from the existing render functions.

**Files:**
- Modify: `index.html` — CSS (near the existing styles), the header rev label (~line 141), and the script: dispatch lines in `render()`/`renderComments()`/`renderSidebar()`, plus new `renderDocView`, `openDocCommentForm`, `renderDocComments`, `renderOutline`.

**Interfaces:**
- Consumes: `DATA.doc` (`{path, blocks:[{html,line_start,line_end,source}]}`) and template `{{.Doc}}`; existing `COMMENTS`, `nextId`, `escapeHtml`, `openEditModal`, `renderUnattached`.
- Produces: the document view + doc comment attachment.

- [ ] **Step 1: Add doc-view CSS**

Add near the end of the `<style>` block:

```css
  .doc { max-width: 860px; margin: 0 auto; padding: 8px 20px 40px; }
  .doc-block { position: relative; padding: 2px 10px; border-radius: 4px; cursor: pointer; }
  .doc-block:hover { background: var(--panel); }
  .doc-block.selected { background: var(--selected); }
  .doc-block.has-comment-block { box-shadow: inset 3px 0 0 var(--accent); }
  .doc-block h1, .doc-block h2, .doc-block h3 { margin: 0.6em 0 0.3em; }
  .doc-block p, .doc-block ul, .doc-block ol { margin: 0.4em 0; line-height: 1.55; }
  .doc-block pre { background: var(--panel-deep); padding: 10px 12px; border-radius: 6px; overflow-x: auto; }
  .doc-block code { font-family: inherit; }
  .doc-block :not(pre) > code { background: var(--panel-deep); padding: 1px 4px; border-radius: 3px; }
  .doc-saved-comment { max-width: 860px; margin: 2px auto 8px; }
  .doc-comment-form { max-width: 860px; margin: 4px auto; }
```

- [ ] **Step 2: Gate the header rev label for doc mode**

Replace the header rev cell (~line 141):

```html
  <div>rev <span class="rev">{{.Rev}}</span> ({{.VCS}})</div>
```

with:

```html
  <div>{{if .Doc}}doc <span class="rev">{{.Rev}}</span>{{else}}rev <span class="rev">{{.Rev}}</span> ({{.VCS}}){{end}}</div>
```

- [ ] **Step 3: Dispatch `render()` to the doc view**

At the very top of `function render() {`, add:

```javascript
  if (DATA.doc) { renderDocView(); renderComments(); return; }
```

- [ ] **Step 4: Dispatch `renderComments()` and `renderSidebar()`**

At the top of `function renderComments() {`:

```javascript
  if (DATA.doc) { renderDocComments(); return; }
```

At the top of `function renderSidebar() {`:

```javascript
  if (DATA.doc) { renderOutline(); return; }
```

- [ ] **Step 5: Add the doc-view functions**

Add these functions in the `<script>` (e.g. just before `function renderSidebar()`):

```javascript
function renderDocView() {
  updateViewing();
  const container = document.getElementById('files');
  container.innerHTML = '';
  const wrap = document.createElement('div');
  wrap.className = 'doc';
  DATA.doc.blocks.forEach((b, bi) => {
    const el = document.createElement('div');
    el.className = 'doc-block';
    el.dataset.bi = bi;
    el.innerHTML = b.html;
    el.addEventListener('click', (e) => {
      if (e.target.closest('a')) return;
      if (String(window.getSelection())) return; // don't hijack text selection
      openDocCommentForm(bi);
    });
    wrap.appendChild(el);
  });
  container.appendChild(wrap);
  if (window.hljs) {
    container.querySelectorAll('pre code').forEach(el => { try { hljs.highlightElement(el); } catch (e) {} });
  }
}

function openDocCommentForm(bi) {
  const b = DATA.doc.blocks[bi];
  const blockEl = document.querySelector(`.doc-block[data-bi="${bi}"]`);
  if (!blockEl) return;
  document.querySelectorAll('.doc-comment-form').forEach(r => r.remove());
  document.querySelectorAll('.doc-block.selected').forEach(r => r.classList.remove('selected'));
  blockEl.classList.add('selected');
  const range = b.line_end !== b.line_start ? '-' + b.line_end : '';
  const form = document.createElement('div');
  form.className = 'comment-form doc-comment-form';
  form.innerHTML = `
    <div style="color: var(--muted); margin-bottom: 4px;">${escapeHtml(DATA.doc.path)}:${b.line_start}${range}</div>
    <textarea placeholder="Comment for the agent..."></textarea>
    <div class="actions">
      <button type="button">Add comment</button>
      <button type="button" class="cancel">Cancel</button>
    </div>`;
  blockEl.after(form);
  const ta = form.querySelector('textarea');
  ta.focus();
  form.querySelector('button').addEventListener('click', () => {
    const body = ta.value.trim();
    if (!body) { form.remove(); blockEl.classList.remove('selected'); return; }
    COMMENTS.push({
      id: nextId++,
      path: DATA.doc.path,
      side: 'new',
      line: b.line_start,
      end_line: b.line_end,
      snippet: b.source,
      body,
    });
    form.remove();
    blockEl.classList.remove('selected');
    renderComments();
  });
  form.querySelector('button.cancel').addEventListener('click', () => {
    form.remove();
    blockEl.classList.remove('selected');
  });
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) form.querySelector('button').click();
    else if (e.key === 'Escape') form.querySelector('button.cancel').click();
  });
}

function renderDocComments() {
  document.querySelectorAll('.doc-saved-comment').forEach(r => r.remove());
  document.querySelectorAll('.doc-block.has-comment-block').forEach(el => el.classList.remove('has-comment-block'));
  const unattached = [];
  COMMENTS.forEach(c => {
    let bi = -1;
    if (c.path === DATA.doc.path) {
      bi = DATA.doc.blocks.findIndex(b => b.line_start <= c.line && c.line <= b.line_end);
    }
    const blockEl = bi >= 0 ? document.querySelector(`.doc-block[data-bi="${bi}"]`) : null;
    if (!blockEl) { unattached.push(c); return; }
    blockEl.classList.add('has-comment-block');
    const range = c.end_line && c.end_line !== c.line ? '-' + c.end_line : '';
    const div = document.createElement('div');
    div.className = 'doc-saved-comment';
    div.innerHTML = `
      <div class="saved-comment ${c.prior ? 'prior' : ''}">
        <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}</div>
        <div class="body">${escapeHtml(c.body)}</div>
        <div class="controls">
          <button type="button" data-id="${c.id}" data-action="edit">edit</button>
          <button type="button" data-id="${c.id}" data-action="del">delete</button>
        </div>
      </div>`;
    blockEl.after(div);
    div.querySelectorAll('button').forEach(btn => {
      btn.addEventListener('click', () => {
        const id = +btn.dataset.id, action = btn.dataset.action;
        const idx = COMMENTS.findIndex(x => x.id === id);
        if (idx < 0) return;
        if (action === 'del') { COMMENTS.splice(idx, 1); renderComments(); }
        else if (action === 'edit') { openEditModal(COMMENTS[idx]); }
      });
    });
  });
  renderUnattached(unattached);
}

function renderOutline() {
  const ul = document.getElementById('fileList');
  ul.innerHTML = '';
  DATA.doc.blocks.forEach((b, bi) => {
    const m = b.html.match(/^<h([1-6])[^>]*>([\s\S]*?)<\/h\1>/i);
    if (!m) return;
    const li = document.createElement('li');
    li.dataset.bi = bi;
    li.style.paddingLeft = (4 + (parseInt(m[1], 10) - 1) * 10) + 'px';
    const text = m[2].replace(/<[^>]+>/g, '');
    li.innerHTML = `<span class="path" title="${escapeHtml(text)}">${escapeHtml(text)}</span>`;
    li.addEventListener('click', () => {
      const el = document.querySelector(`.doc-block[data-bi="${bi}"]`);
      if (el) el.scrollIntoView({ block: 'start', behavior: 'smooth' });
    });
    ul.appendChild(li);
  });
}
```

- [ ] **Step 6: Verify both modes render (template parses, no JS break)**

Run:
```bash
go build -o /tmp/gutter . && \
printf '# Plan\n\nFirst paragraph.\n\n- a\n- b\n' > /tmp/plan.md && \
/tmp/gutter -md /tmp/plan.md -open=false -port=9961 >/tmp/d.out 2>/tmp/d.err & sleep 2; \
echo "header shows doc label:"; curl -s http://127.0.0.1:9961/ | grep -o 'doc <span class="rev">[^<]*</span>' | head -1; \
echo "diff has doc blocks:"; curl -s http://127.0.0.1:9961/diff | grep -o '"blocks":\[' | head -1; \
kill %1 2>/dev/null; \
/tmp/gutter -open=false -port=9962 >/dev/null 2>&1 & sleep 2; \
echo "non-doc / still 200:"; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9962/; \
kill %1 2>/dev/null
```
Expected: header shows `doc <span class="rev">/tmp/plan.md</span>`; `/diff` contains `"blocks":[`; non-doc `/` returns 200. Kill any servers you start.

- [ ] **Step 7: Commit**

```bash
git add index.html
git commit -m "UI: markdown document view with block-level commenting"
```

---

## Task 6: End-to-end verification

Verify the full doc-review round-trip against the binary, including sync.

**Files:** none (verification only).

- [ ] **Step 1: Build and make a test doc**

Run: `go build -o /tmp/gutter . && printf '# Plan\n\nParagraph one.\n\n## Section\n\nParagraph two.\n' > /tmp/plan.md`
Expected: clean build.

- [ ] **Step 2: Sync doc review — stdout is the review, no file**

```bash
rm -f /tmp/none.md
GUTTER_OUTPUT=/tmp/none.md /tmp/gutter -sync -md /tmp/plan.md -open=false -port=9963 >/tmp/dsync.out 2>/tmp/dsync.err &
GPID=$!; sleep 2
echo "blocks in /diff:"; curl -s http://127.0.0.1:9963/diff | grep -o '"line_start":[0-9]*' | head
curl -s -X POST http://127.0.0.1:9963/submit -d '{"general":"Plan LGTM.","comments":[{"path":"/tmp/plan.md","side":"new","line":3,"end_line":3,"body":"tighten this"}]}'
echo; wait $GPID 2>/dev/null; echo "exit: $?"
echo "=== stdout (review only) ==="; cat /tmp/dsync.out
echo "=== banner on stderr ==="; grep '^doc:' /tmp/dsync.err
echo "=== no file ==="; test -e /tmp/none.md && echo "FILE (BUG)" || echo "no file (correct)"
```
Expected: `/diff` shows `line_start` values; submit → the review markdown on stdout titled `# Review of /tmp/plan.md`, with `### /tmp/plan.md:3` and the comment body; exit 0; `doc:` banner on stderr; no file written. (If the `wait` hangs because the server was backgrounded, kill with `kill -TERM $GPID` — that's the shell SIG_IGN artifact, not a bug.)

- [ ] **Step 3: Non-sync doc review writes review.md with the doc title**

```bash
GUTTER_OUTPUT=/tmp/doc-review.md /tmp/gutter -md /tmp/plan.md -open=false -port=9964 >/dev/null 2>&1 &
GPID=$!; sleep 2
curl -s -X POST http://127.0.0.1:9964/save -d '{"general":"ok","comments":[{"path":"/tmp/plan.md","side":"new","line":5,"end_line":5,"body":"heading note"}]}'
echo; head -3 /tmp/doc-review.md; grep -c '### /tmp/plan.md:5' /tmp/doc-review.md
curl -s http://127.0.0.1:9964/quit >/dev/null 2>&1; kill $GPID 2>/dev/null
```
Expected: `review.md` starts with `# Review of /tmp/plan.md`; contains `### /tmp/plan.md:5`.

- [ ] **Step 4: `-md` beats `-pr`, and missing file fails fast**

```bash
/tmp/gutter -md /tmp/nope-missing.md -open=false -port=9965 2>&1 | head -1; echo "(missing-file exit expected non-zero)"
```
Expected: exits with a `rendering doc:`/open error before serving.

---

## Task 7: Documentation

**Files:** Modify `README.md`.

- [ ] **Step 1: Document doc mode**

Add a "Reviewing a markdown document" subsection near the usage/flags docs covering:
- `gutter -md <file>` renders a markdown file as a document; click any block to comment; block comments anchor to source line ranges.
- Combine with `-sync` for the agent plan-review loop (`gutter -sync -md docs/plan.md`).
- `-md` takes precedence over `-pr` and ignores `-r`.
- Uses the goldmark library (note the `go 1.19` module floor).

Add `-md`, `GUTTER_MD`, and the `md` JSON field to the flags/env/config reference tables.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "README: document -md markdown document review"
```

---

## Self-Review Notes

- **Spec coverage:** renderer + block/source-line extraction → Task 1; `md` config → Task 2; doc title → Task 3; server doc branch + banner + `-md`>`-pr` + header → Task 4; UI doc view + block commenting + outline → Task 5; e2e incl. sync round-trip → Task 6; docs → Task 7.
- **Type/name consistency:** `Doc{Path,Blocks}`, `DocBlock{HTML,LineStart,LineEnd,Source}` (JSON `html`/`line_start`/`line_end`/`source`), `renderDoc`, `computeLineStarts`/`offsetToLine`/`nodeLineRange`, `Config.MD`/`GUTTER_MD`/`md`, `renderMarkdown(rev, vcs, docPath, pr, req)`, `DiffData.Doc`, template `Doc`, JS `DATA.doc` + `renderDocView`/`openDocCommentForm`/`renderDocComments`/`renderOutline` — used identically across tasks.
- **No placeholders:** every code step shows complete code; every run step states expected output.
- **Non-doc safety:** diff and PR modes are untouched; doc branches are additive and dispatched at the top of `render()`/`renderComments()`/`renderSidebar()` and `computeData`, so `DATA.doc == null` keeps every existing path identical.
- **Known imperfection (acceptable, per spec):** goldmark anchors a fenced code block to its content lines, so a code block's range may exclude the ``` fence lines; Task 1's test asserts a range rather than exact fence lines. Thematic breaks with no text segments fall back to line 1.
