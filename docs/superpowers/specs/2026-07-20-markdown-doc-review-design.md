# Design: markdown document review (`-md`)

Date: 2026-07-20

## Goal

Let gutter render a whole markdown file (e.g. a plan or spec) as formatted HTML
and let the user comment on it block-by-block, emitting the same `review.md`
format gutter already uses. Combined with `-sync`, this gives an agent a
plan-review loop: render the plan, the user comments, clicks Submit, and the
review returns on stdout.

## Non-goals

- Not a markdown *diff* view. Doc mode renders a whole file, not changes to one.
  (Diff review of `.md` files continues to work in the normal diff mode,
  unrendered, as today.)
- No arbitrary text-span/character-offset anchoring. Comments anchor to whole
  rendered blocks mapped to source line ranges.
- No editing of the markdown in gutter. It is read-only review.

## Decisions (from brainstorming)

1. **Input:** a whole `.md` file, rendered.
2. **Anchor:** click a rendered block → the block's source line range. Comments
   reuse the existing `### file:line-end` `review.md` format.
3. **Renderer:** goldmark, server-side, using AST source positions for accurate
   per-block line ranges.
4. **Invocation:** `-md <file>` flag (composes with `-sync`).

## Invocation & configuration

- Flag `-md <file>` (string), env `GUTTER_MD`, JSON `md`, standard precedence
  (CLI → env → project JSON → user JSON → defaults). The `md` JSON merge follows
  the non-empty-string pattern (`if f.MD != "" { c.MD = f.MD }`).
- When `md != ""`, gutter is in **doc mode**: it renders that file and ignores
  `-r`. If both `-md` and `-pr` are set, `-md` wins and gutter prints a warning
  to stderr (`note: -md set; ignoring -pr`).
- Composes with `-sync` and non-sync exactly like diff mode (sync → review to
  stdout, no file; non-sync → `review.md`).

## Rendering (goldmark)

- Add dependency **github.com/yuin/goldmark** (v1.8.x). This is gutter's first Go
  dependency and requires bumping the `go` directive in `go.mod` from `1.16` to
  `1.19` (goldmark v1.8 needs Go ≥ 1.19; the installed toolchain is 1.26, so
  this only changes the module's declared floor).
- New function `renderDoc(path string) (Doc, error)`:
  1. Read the file bytes.
  2. Precompute a line-offset table: the byte offset at which each source line
     starts, so any byte offset maps to a 1-based line number.
  3. Parse to an AST: `md.Parser().Parse(text.NewReader(src))`.
  4. Walk the document node's **top-level block children**. For each block node:
     - Render just that node to an HTML fragment via the goldmark renderer
       (`renderer.Render(&buf, src, node)`).
     - Compute its source line span: recursively collect the node's text
       `Segments` (`node.Lines()` on block nodes, descending into children) and
       take min start line and max end line. Blocks with no text segments (rare;
       e.g. thematic breaks) fall back to a best-effort single line derived from
       sibling boundaries.
     - Extract the raw source markdown for that span (used as the comment
       snippet).
  5. Return `Doc{Path: path, Blocks: []DocBlock}` where each block is
     `{HTML string, LineStart int, LineEnd int, Source string}`.
- goldmark is configured with its defaults (CommonMark). Code fences render as
  `<pre><code class="language-…">`, which the existing highlight.js pass in the
  UI styles automatically.

### Data types

```go
type DocBlock struct {
	HTML      string `json:"html"`       // rendered HTML fragment for this block
	LineStart int    `json:"line_start"` // 1-based source line range (inclusive)
	LineEnd   int    `json:"line_end"`
	Source    string `json:"source"`     // raw markdown of the block (comment snippet)
}

type Doc struct {
	Path   string     `json:"path"`
	Blocks []DocBlock `json:"blocks"`
}
```

`DiffData` gains `Doc *Doc` (`json:"doc,omitempty"`). In doc mode `Files` is
empty and `Doc` is populated; in diff mode `Doc` is nil.

## Server

- In `main`, when `*md != ""`, `computeData` builds `DiffData` via `renderDoc`
  instead of the diff path: `Doc: &doc`, `Files: nil`, and still attaches prior
  comments via `loadPrior`. `/diff` recomputes per request as today (re-reads +
  re-renders the file — no caching, consistent with the existing rule).
- `/save`, `/submit`, `/markdown` are unchanged except that `renderMarkdown`
  gains a doc-aware title (see below). The comment payload shape (`SaveRequest`
  with `Comment{path,line,endLine,side,snippet,body}`) is identical.
- Header display: in doc mode the banner/`rev:` line is replaced by a
  `doc:  <path>` line (to stderr, per the banner-always-stderr rule), and the UI
  header shows the doc path instead of `rev … (vcs)`.

## Output & round-trip

- `renderMarkdown` gains a `docPath string` parameter (empty unless doc mode).
  When set, the title is `# Review of <docPath>` instead of
  `# Review of `rev` (vcs)`. Everything else (General feedback, Inline comments,
  `### path:line-end`, snippet fences) is unchanged. All existing callers pass
  `""`.
- `loadPrior` is unchanged: comments parse back as `### <md file>:line-end`.
- Re-running `gutter -md <file>` reattaches prior comments by matching a
  comment's `[line,endLine]` to a block's `[LineStart,LineEnd]`; comments that
  no longer match any block fall to the existing **unattached prior comments**
  panel. (Reattachment matching happens client-side, same as diff mode maps
  comments to diff lines.)

## UI (index.html)

The template already branches on data. Add a document view:

- The `/` template gains a `Doc bool` (or the client checks `data.doc`). When a
  `Doc` is present, render the **document view** instead of the diff table:
  - For each block, output its `HTML` inside a commentable container
    (`<div class="doc-block" data-line-start=… data-line-end=…>`).
  - Clicking a block selects it and opens the existing comment editor, anchored
    to the block's line range (`path` = doc path, `line`/`endLine` = the block
    range, `snippet` = block `Source`, `side` = "new").
  - Saved comments reuse the existing `.saved-comment` markup, shown beneath or
    beside their block; prior/unattached handling reuses the existing panels.
  - The file sidebar becomes a heading **outline** (links to heading blocks) or
    is hidden when there are no headings.
- The general-feedback box, Save (non-sync), and Submit (sync) header controls
  are unchanged and work identically.
- Doc CSS: constrain content width for readability, style headings/lists/code
  using the existing theme variables (`--panel`, `--text`, `--border`, etc.) so
  light/dark both work. Reuse the highlight.js theme swap already in place.

## Error handling

- `-md` path missing/unreadable → `die` at startup with a clear message
  (mirrors the diff-error path).
- Empty file or a file with no blocks → the doc view shows a "no content"
  message; Submit still works (empty review allowed).
- `-md` pointing at a non-markdown file → still rendered as markdown (goldmark
  treats plain text as paragraphs); no special-casing by extension.

## Testing

Consistent with the repo (pure-function unit tests where they fit; end-to-end
against the binary otherwise).

- **Unit** (`main_test.go`):
  - `renderDoc`: a small markdown source (heading + paragraph + fenced code +
    list) produces blocks with the expected `LineStart`/`LineEnd` ranges and
    non-empty `HTML`/`Source`. This is the load-bearing source-line-mapping
    logic.
  - `md` config precedence (flag/env/JSON), mirroring the existing pattern.
  - `renderMarkdown` doc title: `docPath` set → `# Review of <path>`; empty →
    unchanged `# Review of `rev` (vcs)`.
- **End-to-end** (documented in the plan): `gutter -sync -md <file> -open=false`;
  GET `/diff` returns a `doc` with blocks; POST a block-anchored comment to
  `/submit`; assert stdout is the review markdown with the correct
  `### <file>:line-end` anchor and exits 0; assert no `review.md` written.

## Documentation

Add a "Reviewing a markdown document" section to `README.md`: `gutter -md
<file>` renders a markdown file for block-level commenting; combine with `-sync`
for the agent plan-review loop. Add `-md`, `GUTTER_MD`, and the `md` JSON field
to the reference tables. Note the goldmark dependency and the `go 1.19` bump.
