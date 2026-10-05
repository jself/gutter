# Notes for Claude

`gutter` is a single-binary Go tool that serves a local web UI for reviewing a
jj/git diff and emitting a markdown review file for an AI agent to act on.
This file captures the non-obvious decisions so future sessions don't undo them.

## Layout

- `main.go` — CLI, HTTP server, diff parser, word-level intra-line diff,
  markdown renderer and parser for `review.md`. Single file on purpose.
- `index.html` — entire UI (HTML/CSS/JS in one file), embedded via `go:embed`.
  Uses highlight.js from a CDN at runtime; no bundler.
- `guide.go` — guided review: parser for `review-guide.md` (parts `##`,
  steps `###`, `path:start-end` reference bullets), the matcher that claims
  diff lines per stop, and the `-guide-format` text. Split out of `main.go`
  because it is self-contained and has its own tests in `guide_test.go`.
- `Makefile` — `build`, `install` (honors `PREFIX`, default `~/.local`),
  `clean`, `run`. Install is the standard path.
- `docs/screenshot-{dark,light}.png` — referenced by README, regenerated from
  Playwright in the past. Keep both in sync if the UI changes materially.

## Conventions worth preserving

- **Default revset for jj is `@`, not `@-`.** `@-` shows the parent commit's
  diff (i.e. master), which is almost never what the user wants. This was a
  bug; don't reintroduce it.
- **Default rev for git is `@{u}`** (working tree vs upstream).
- **VCS detection** is `jj root` then `git rev-parse --git-dir`. Order matters
  in jj-colocated git repos: prefer jj.
- **Diff source is `--git` unified format** for both VCSes so a single parser
  handles both.
- **Markdown round-trip is load-bearing.** `renderMarkdown` (write) and
  `loadPrior` (read) must stay compatible: re-running gutter on the same
  revset must parse the previously-written file and reattach comments. The
  format is `## General feedback` / `## Inline comments` / `### path:line` or
  `### path:line-end`, with an optional fenced snippet, then body until the
  next `###`. Be conservative when editing either function.
- **Comments without a matching diff line** (because the code was rewritten)
  must appear in the "unattached prior comments" panel, not be silently dropped.
- **Live diff:** `/diff` recomputes on every request — do not re-add caching.
  Reload-to-refresh is a feature.
- **Configuration precedence** (highest wins): CLI flags → env (`GUTTER_*`) →
  `./.gutter.json` → `$XDG_CONFIG_HOME/gutter/config.json` (or
  `~/.config/gutter/config.json`) → defaults in `defaultConfig()`. If you add
  a flag, add the matching env var and JSON field.
- **Output path is `dir + output` joined**, with `output` winning if absolute.
  `MkdirAll` the parent on startup so `.claude/review.md` Just Works.
- **localStorage keys are prefixed `gutter_`** (`gutter_theme`,
  `gutter_sidebar_hidden`). Don't change them without a migration — users will
  lose their theme.
- **Guided review coverage is computed in Go, not trusted from the guide.**
  `resolveGuide` puts every unclaimed add/del line into a synthetic
  `Unassigned` stop and keeps dead references with `Hits == 0`. Deleted lines
  are addressed by the new-side line that follows them. Don't move this logic
  to the browser: the startup coverage summary is how the agent learns to fix
  its guide.
- **`-guide` defaults to `<dir>/review-guide.md`, then `.claude/review-guide.md`**,
  whichever exists first, so the agent's usual `.claude/` layout works with a
  bare `gutter` even when `-dir` is unset.

## UI invariants

- Syntax highlighting (`hljs.highlightElement`) runs once per render. Lines
  with intra-line segments use a two-layer overlay: a transparent-text
  `.bg-layer` carrying the colored backgrounds and a `code.fg-layer` on top
  for hljs. The trick relies on monospace alignment — don't switch to a
  proportional font.
- `+`/`-`/` ` prefix is rendered as an inline `<span class="prefix">`, NOT a
  CSS `::before` pseudo-element. Earlier `::before` broke the overlay
  alignment.
- `white-space: pre-wrap` is scoped to `.saved-comment .body` only. Applying
  it higher (e.g. `.saved-comment`) makes the edit/delete buttons stack
  vertically because the whitespace between them becomes literal newlines.
  This was a bug; don't widen the selector.
- Theme is driven by `html[data-theme="light|dark"]` CSS variables. The
  highlight.js stylesheet href is swapped at the same time
  (`#hljs-theme.href`).
- Guided mode reuses the doc-mode block machinery: `makeBlock(b, path)`
  renders any markdown block as a commentable `.doc-block` carrying
  `data-path/ls/le`, and `renderBlockComments` attaches comments to whatever
  blocks are in the DOM. Doc mode and guide narration share this; don't fork it.
- In guided mode, a comment whose anchor row is on another step is simply
  off-screen, not "unattached". Only comments with no anchor anywhere go to
  the unattached panel.
- The current step lives in `location.hash` (`#s3`) and the Full/Guided
  choice in `localStorage` `gutter_view_mode`.
- `td.ln.has-comment` uses `var(--selected)` for its background so the chip
  adapts to light mode. Don't hardcode it.

## Things that are deliberately NOT here

- Tests exist only for the pure Go pieces (`main_test.go`, `guide_test.go`:
  config, markdown round-trip, guide parsing and matching). The UI is verified
  visually against a local server (headless Chromium screenshots work). If you
  add more, prefer end-to-end against the binary over unit tests of the diff
  parser.
- No bundler, no npm. The UI is hand-written and depends only on CDN-loaded
  highlight.js at runtime.
- No file watcher / push updates. Reload is the refresh mechanism.
- No persistent backend state beyond two files: `review.md` (the source of
  truth, written on Save) and `<output>.draft.json` (the in-progress review,
  rewritten on every change and deleted on Save/Submit). The draft exists
  because a killed window once lost a whole review; it is keyed by rev/PR/doc
  and ignored for any other key. Don't add more state than that.

## Workflow expectations

- The user reviews an agent's work with `gutter`, saves `review.md` (or
  uses the Copy button), and feeds the file back to the agent. Re-running
  gutter on the same revset surfaces prior comments with an amber "prior"
  tag so the user can tell what's been addressed.
- The user is on jj primarily but the tool must keep working with plain git.
