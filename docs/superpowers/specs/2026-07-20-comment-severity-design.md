# Design: comment severity

Date: 2026-07-20

## Goal

Add a severity select to gutter comments and emit the chosen severity as a
trailing `[SEVERITY]` token on the inline-comment heading in the review
markdown, so the centaur-review skill can parse gutter's `-sync` output (and
`review.md`) and route comments by severity.

## Non-goals

- General-feedback severity (the `## General feedback` block has no heading
  token). Inline is the must-have; general feedback is a possible follow-up.
- No change to range-comment behavior, line anchoring, the snippet fence
  format, the `## General feedback` / `## Inline comments` structure, or the
  `-sync` stdout/stderr split.

## Opt-in flag `-severity`

The whole feature is gated behind a boolean flag `-severity` (default `false`),
with the matching `GUTTER_SEVERITY` env and `severity` JSON field per the config
convention. The centaur skill launches with `-severity`; a plain `gutter` run is
unchanged.

- **`-severity` off (default):** the severity select is not rendered, and NO
  `[SEVERITY]` token is emitted on any heading — output is byte-for-byte what it
  is today.
- **`-severity` on:** the select appears (5 options, default QUESTION) on the
  inline add-forms + edit modal (diff + doc), and ` [SEVERITY]` is emitted after
  the optional ` (LEFT)` on each inline heading.

`loadPrior` parses a `[SEVERITY]` token whenever present regardless of the flag
(harmless — the regex is tolerant); it just won't be re-emitted when the flag is
off.

## Severity values

Five, emitted verbatim in uppercase: `BLOCKING`, `IMPORTANT`, `SUGGESTION`,
`QUESTION`, `NITPICK`. **Default = `QUESTION`.** (Only emitted when `-severity`.)

## Model

`Comment` gains `Severity string` (`json:"severity,omitempty"`). It applies to
inline comments in every mode (diff, PR, and markdown-doc — doc comments are
ordinary `### path:line` inline comments, so severity flows through with no
extra work).

## Output — `renderMarkdown`

`renderMarkdown` gains a `severity bool` parameter (threaded from the `-severity`
flag). When `false`, no token is emitted (today's output). When `true`, the
inline heading — currently built as `loc` (`path:line` or `path:start-end`) with
an optional ` (LEFT)` appended for PR old-side comments — gets the severity token
**after** any ` (LEFT)`, always emitting it (defaulting to `QUESTION` when
`Severity` is empty):

```
### path/to/file.ts:53 [QUESTION]
### path/to/file.ts:183-188 [SUGGESTION]
### path/to/file.ts:7 (LEFT) [NITPICK]      (PR old-side + severity)
```

Concretely, after the existing `if pr != nil && c.Side == "old" { loc += " (LEFT)" }`:

```go
if severity {
    sev := c.Severity
    if sev == "" {
        sev = "QUESTION"
    }
    loc += " [" + sev + "]"
}
```

Everything below the heading (snippet fence, body) is unchanged.

## Round-trip — `inlineHeaderRe` + `loadPrior`

The regex is currently:

```
^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?\s*$
```

Add a trailing optional severity group (order: line, optional `-end`, optional
`(LEFT)`, optional `[SEV]`):

```
^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?(?:\s+\[([A-Z]+)\])?\s*$
```

- The severity capture is group 5 (path=1, start=2, end=3, LEFT=4, SEV=5).
- `loadPrior` sets `Comment.Severity` from group 5; when the token is absent
  (legacy reviews, or headings written before this feature), it defaults to
  `"QUESTION"`.
- Backward compatibility: old `### path:line` and `### path:line (LEFT)` headings
  still match (the new group is optional). On re-save they normalize to include
  an explicit `[QUESTION]` (or their parsed severity). This is the intended,
  additive behavior; the heading structure is otherwise unchanged.

## UI (`index.html`)

The `/` template passes a `Severity` bool (from the flag); a JS `SEVERITY_MODE`
const gates all severity UI. When off, no select is rendered anywhere and the
saved-comment severity tag is hidden. When on, add a `<select>` with the five
options (default `QUESTION` selected) to the inline-comment **add-form** in both
modes and to the **edit modal**:

1. **Diff add-form** (`openCommentForm`): the comment form inserted as a table
   row. Add the select above/below the textarea; on "Add comment", read its
   value into the pushed comment's `severity`.
2. **Doc add-form** (`openDocCommentForm`): the comment form inserted after a
   doc block. Same select + same wiring.
3. **Edit modal** (`openEditModal`): add the select, pre-selected to the
   comment's current `severity` (default `QUESTION` if empty), and write it back
   on save.

The comment payload sent to `/save`, `/submit`, `/markdown` already serializes
the whole `COMMENTS` array (minus `id`/`prior`), so adding a `severity` property
flows to the server with no transport change.

Saved-comment display: show the severity as a small tag in the `.saved-comment`
`.loc` line (e.g. `💬 path:53 · QUESTION`), so the reviewer can see it at a
glance. Purely presentational.

A single shared constant `const SEVERITIES = ['BLOCKING','IMPORTANT','SUGGESTION','QUESTION','NITPICK'];`
and a small `severitySelectHTML(selected)` helper avoids duplicating the option
markup across the three sites.

## Error handling / edge cases

- A comment with no severity (programmatic, or legacy) renders as `[QUESTION]`.
- The regex only accepts `[A-Z]+` inside the brackets, so a stray `[note]` in a
  heading path won't be mis-parsed as severity (paths are matched non-greedily
  before the final optional token). Unknown uppercase tokens (e.g. a future
  severity) are captured verbatim into `Severity` and round-trip unchanged; the
  consumer decides how to treat unknowns.

## Testing

- **Unit** (`main_test.go`):
  - `renderMarkdown` emits ` [SEVERITY]` after the heading, including after
    ` (LEFT)`, and defaults empty severity to `[QUESTION]`.
  - `loadPrior` round-trips severity: a heading with `[SUGGESTION]` parses to
    `Severity: "SUGGESTION"`; a legacy heading with no token parses to
    `Severity: "QUESTION"`; a `(LEFT) [NITPICK]` heading parses both side=old and
    severity=NITPICK.
  Tests exercise `renderMarkdown` with `severity=true` for the token cases and
  assert `severity=false` produces NO token (today's output).
- **End-to-end**: `gutter -sync -severity` (diff/doc mode), POST a comment with
  `severity:"IMPORTANT"` to `/submit`, assert the stdout heading is
  `### <path>:<line> [IMPORTANT]`; confirm a range comment still emits
  `:start-end [..]`. Also confirm a plain `gutter -sync` (no `-severity`) emits
  `### <path>:<line>` with NO token.

## Documentation

Brief note in `README.md` that `-severity` (opt-in flag) adds a severity dropdown
to inline comments (`BLOCKING`/`IMPORTANT`/`SUGGESTION`/`QUESTION`/`NITPICK`,
default `QUESTION`), emitted as a trailing `[SEVERITY]` token on the
`### path:line` heading; without the flag, output is unchanged and absence parses
as `QUESTION`. Add `-severity`, `GUTTER_SEVERITY`, and the `severity` JSON field
to the reference tables.

## Also (separate, bundled): `-md` view screenshot

Add a screenshot of the `-md` document view to `README.md` (the markdown-doc
feature currently has no visual in the docs). Capture `gutter -md <file>` on a
representative markdown file, save under `docs/` (e.g. `docs/screenshot-md.png`),
and reference it from the `-md` / "Reviewing a markdown document" section. This
is independent of the severity feature but requested in the same batch.
