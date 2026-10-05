# Guided review (design)

**Date:** 2026-10-04
**Status:** approved in conversation; sections 1-2 reviewed, 3-4 decided by Claude under "build it and try it"

## Problem

Large diffs are hard to review as one flat list of files. The agent that made
the change knows how it decomposes into logical steps and why each exists, but
that knowledge is lost by the time the user opens gutter. We want the agent to
write a short *guide* that gutter overlays on the live diff, so the user can
walk the change one step at a time with narration, while keeping the ordinary
full view one click away and guaranteeing nothing is skipped.

## 1. The guide file

Plain markdown, default `.claude/review-guide.md` (same directory convention as
`review.md`; set by `-guide`, `GUTTER_GUIDE`, or `"guide"` in config).

```markdown
# Review guide

One paragraph: what the change does as a whole and why.

## 1. Renderer

Part narration (optional). A part groups steps.

### 1.1 Enable GFM extensions

Step narration: what this changes, why, what to look at closely.

- main.go:23
- main.go:710-716

### 1.2 Style tables

- index.html:175-182

## 2. Docs

- README.md
```

Rules:

- `# ` title plus the text under it is the **overview**.
- `## ` is a **part**; `### ` is a **step** inside it. A part with no steps is
  itself a stop. Deeper headings are narration formatting.
- A **reference** is a list bullet whose text is `path`, `path:line` or
  `path:start-end`. Lines are new-side numbers. Bare `path` claims every
  changed line in that file (the only way to claim a wholly deleted file).
  References attach to the heading they sit under. Bullets that do not parse
  as references are narration, never errors.
- Everything else under a heading is narration, rendered with goldmark (same
  extensions as `-md`).
- Steps with no references are allowed (pure context).
- Parsing is loose: regex on headings and bullets. No front matter, no JSON.

## 2. Coverage and matching

Done in Go on every `/diff` request (the diff is live, the guide is re-read).

- `path:start-end` claims every `add` line whose new-side number is in range,
  plus every `del` line that sits between claimed positions in the same hunk
  (so a rewrite's deletions ride along with its insertions). A `del` run that
  immediately precedes a claimed `add` at the range start is included too.
- Bare `path` claims all `add`/`del` lines in the file.
- Context lines never count toward coverage.
- A reference that claims nothing is **dead**: kept, shown with a warning.
- Changed lines claimed by no stop go into a synthetic trailing part
  **Unassigned** with an amber badge and count. Coverage is 100% by
  construction.
- A line claimed by several stops appears in each with an "also in N" tag.
- Startup prints `guide: N stops, D dead references, U unassigned lines in F
  files` to stderr so the agent can fix the guide before handing over.

## 3. UI

- Header gains a `Full | Guided` segmented toggle when a guide is loaded.
  Guided is the default; the choice persists in `localStorage` key
  `gutter_view_mode`.
- **Guided mode.** Sidebar shows the overview entry, then parts with nested
  steps, then Unassigned if non-empty; current stop highlighted. Main pane
  shows: breadcrumb (part › step), part narration on the part's first stop,
  step narration, the reference list (dead ones flagged), then the claimed
  hunks. Within a hunk, lines outside the claimed set ± 3 context lines are
  folded behind a "… N lines" row that expands on click. Lines also claimed
  by another stop carry a small tag. Prev / Next buttons at the bottom;
  `[` and `]` keys when no text field is focused.
- **Narration is commentable.** Narration blocks reuse the doc-mode block
  machinery; a comment on one is anchored to the guide file
  (`review-guide.md:12`) so the agent can distinguish "your explanation is
  wrong" from code comments. These flow through `review.md` unchanged.
- **Full mode** is today's view, untouched. Comments made in either mode are
  the same `COMMENTS` array and render in both.
- Comments whose anchor row is not on the current stop are not shown in the
  unattached panel in guided mode (they are merely off-screen); the sidebar
  shows a comment count per stop instead.
- `-md` and `-guide` together: `-guide` is ignored with a note.

## 4. CLI, docs, skill

- `-guide <file>`: path to the guide. If unset and `<dir>/review-guide.md`
  exists, it is used and a note is printed.
- `-guide-format`: print the format reference (the template above plus the
  rules) and exit. The `-guide` help text points at it.
- `renderMarkdown` output is unchanged. The round-trip format is untouched.
- README gets a "Guided review" section. CLAUDE.md gets the invariants.
- `~/.claude/skills/gutter/SKILL.md` gets a "Guided review" section telling
  the agent when to write a guide (roughly: more than ~150 changed lines or
  more than ~5 files), how to write it (`gutter -guide-format`), to run
  gutter once and fix dead/unassigned until clean, and how to read
  guide-anchored comments.

## Non-goals

- No step markers in full mode (v1).
- No generation of the guide by gutter itself; the agent writes it.
- No changes to the review.md grammar.
