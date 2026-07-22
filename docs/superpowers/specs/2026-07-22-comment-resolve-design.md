# Design: resolvable comments + PRIOR badge

Date: 2026-07-22

## Goal

Let a reviewer mark a comment **resolved** (done), persisted in `review.md` so
the state survives across iterations — the agent reading `review.md` can then
tell resolved comments from open ones and focus on what's left. Also make the
"previous comment" marker more prominent (a `PRIOR` badge).

Builds on the teal comment-card styling (branch `comment-styling`, committed
`7fcb631`).

## Non-goals

- No change to the teal/amber card styling itself (done separately).
- No change to comment anchoring, severity, or the diff overlay.
- Resolved is not the same as deleted — `delete` still removes a comment
  entirely; `resolve` keeps it (marked done).

## Behavior

- Every saved comment gets a **Resolve** / **Unresolve** toggle alongside
  `edit`/`delete`. Resolving marks it done; unresolving reverts.
- A **resolved** comment stays visible but de-emphasized: reduced opacity and a
  strikethrough on the location header, so it reads as "handled" without
  disappearing.
- Resolved state **persists in `review.md`** and reloads (a resolved comment
  comes back resolved, and — since it's from a prior file — also `prior`).
- **PRIOR badge:** prior comments show a small right-aligned amber `PRIOR` pill
  in the header (like the severity badge), replacing reliance on the subtle
  "· prior" text. (Keep or drop the "· prior" text — see UI below.)

### Agent-facing value

Because resolved state round-trips, the `review.md` (and `-sync` stdout) the
agent consumes marks resolved comments explicitly. The agent can skip resolved
items and act only on open ones across review iterations.

## Data model

`Comment` gains `Resolved bool` (`json:"resolved,omitempty"`). Orthogonal to
`Severity`/`Side`/prior.

## `review.md` format (round-trip)

Resolved is encoded as a trailing ` (resolved)` marker on the inline heading,
**after** any ` (LEFT)` and ` [SEVERITY]` tokens. Human-readable and additive:

```
### path/to/file.ts:53 [SUGGESTION] (resolved)
### path/to/file.ts:120-124 (LEFT) [BLOCKING] (resolved)
### path/to/file.ts:7                              (open — no marker)
```

- **`renderMarkdown`**: when `c.Resolved`, append ` (resolved)` after the
  existing severity/LEFT tokens (a new innermost step in the same heading
  builder). Non-resolved comments are byte-for-byte unchanged.
- **`inlineHeaderRe`**: extend with an optional trailing group
  `(?:\s+\((resolved)\))?` after the `[SEVERITY]` group. Absent → not resolved.
- **`loadPrior`**: set `Comment.Resolved` from that group.
- Backward compatible: existing headings (no `(resolved)`) parse as
  `Resolved: false`.

Token order on a heading becomes: `path:line[-end]` → `[ (LEFT)]` →
`[ [SEVERITY]]` → `[ (resolved)]`.

## UI (`index.html`)

The saved-comment markup/rendering (`renderDocComments`, `renderComments`,
`renderUnattached`) and the `.saved-comment` styling:

1. **Resolve toggle** — add a button to `.saved-comment .controls`:
   - Open comment: **Resolve** → sets `c.resolved = true`, re-renders.
   - Resolved comment: **Unresolve** → sets `c.resolved = false`.
   - Keep `edit` and `delete` (delete = remove entirely; resolve = mark done).
2. **Resolved appearance** — a `.saved-comment.resolved` class:
   `opacity: 0.6;` and `text-decoration: line-through` on the `.loc` header
   (not the body — keep the note legible). A small `RESOLVED` badge could also
   sit in the header; to avoid badge clutter, the struck+greyed treatment plus a
   `✓` prefix on the loc is enough.
3. **PRIOR badge** — for `.saved-comment.prior`, render a right-aligned amber
   pill `PRIOR` in the `.loc` (reuse the `.sev` badge styling with an amber
   variant). Drop the `.loc::after " · prior"` text (the badge replaces it).
   If both prior and severity badges are present, show both (severity, then
   PRIOR) right-aligned.
4. Wire the new button in the three comment renderers' `data-action` handlers
   (`edit`/`delete` today) to also handle `resolve`/`unresolve` → flip
   `COMMENTS[idx].resolved` and re-render.
5. Seed `resolved` from `DATA.prior` in the `load()` prior-seed loop
   (`resolved: c.resolved || false`), and include `resolved` in the payload sent
   to `/save`/`/submit`/`/markdown` (the existing spread of `COMMENTS` minus
   `id`/`prior` already carries arbitrary fields, so `resolved` flows through).

## Testing

- **Unit** (`main_test.go`):
  - `renderMarkdown` emits ` (resolved)` after severity/LEFT when
    `Resolved: true`, and nothing when false; verify order with a
    `(LEFT) [X] (resolved)` case.
  - `loadPrior` round-trips `Resolved` (a `(resolved)` heading → `Resolved:true`;
    a legacy heading → `false`; a `(LEFT) [NITPICK] (resolved)` heading →
    side=old, severity=NITPICK, resolved=true).
- **Visual (browser + native window):** resolve a comment → it greys/strikes;
  unresolve → reverts; save + reload → it returns resolved (and prior, with the
  PRIOR badge); confirm the PRIOR badge shows on prior comments and severity +
  PRIOR coexist.

## Documentation

README: note the Resolve/Unresolve action and that resolved state is saved in
`review.md` (so agents can skip resolved comments across iterations); mention
the PRIOR badge. Update the UI cheatsheet.
