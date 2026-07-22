# Design: visually distinct comment cards

Date: 2026-07-22

## Goal

Make saved review comments visually distinct from the content they annotate —
especially in the markdown/doc view, where a comment currently renders as plain
prose and is easy to mistake for the document. Give comments a "sticky-note"
card treatment in a dedicated teal accent, consistent across the doc and diff
views.

## Non-goals

- No change to comment data, the `review.md` format, anchoring, or the
  comment-editing flow (the edit modal, add-form).
- No change to the diff intra-line overlay, fonts, or zoom.
- Not restyling the comment *form* (`.comment-form`) — only saved/displayed
  comments (`.saved-comment`).

## Chosen design (from mockups)

The combined "A+C sticky-note card": a bordered panel with a subtle tint, a
colored left border, a raised drop shadow, a bold accent-colored location
header, and the severity shown as a right-aligned badge. Flush-aligned with the
content column (not indented). Accent color is **teal** — currently unused in
gutter (blue = accent/links, amber = prior comments, green = diff add, red =
diff del), so it reads as its own thing and doesn't collide.

- The **commented block/line** gets a matching teal left rail (replacing the
  current blue `has-comment` rail so the block and its comment share a color).
- **"Prior" comments keep amber** — same card shape, amber left border + the
  existing "· prior" tag — so "new vs prior" stays legible against the teal of
  regular comments.
- Applies in **both** the doc view (`.doc-saved-comment`) and the diff view
  (`.saved-comment-row`) and the unattached-prior panel, since all render a
  `.saved-comment`.

## Color tokens (both themes)

Add per-theme CSS variables (alongside the existing `--accent` in the
`:root/[data-theme=dark]` and `[data-theme=light]` blocks):

- **Dark:** `--comment: #39c5c8;` (border/rail), `--comment-fg: #4fd6d9;` (loc
  text), `--comment-bg: #12201f;` (card fill — panel with a faint teal cast).
- **Light:** `--comment: #0e9aa0;`, `--comment-fg: #0e7490;`,
  `--comment-bg: #ecfbfb;` (teal-tinted light panel).

(Explicit per-theme fills rather than `color-mix`, to stay safe across the
WebKit native-window renderer.)

## CSS changes (`index.html`)

All in the `<style>` block; JS/markup unchanged (the existing `.saved-comment`
structure — `.loc`, `.body`, `.controls`, optional `.sev` — already supports
this).

1. **Card base** — style `.saved-comment` as the card:
   ```css
   .saved-comment {
     background: var(--comment-bg);
     border: 1px solid var(--border);
     border-left: 3px solid var(--comment);
     border-radius: 6px;
     padding: 10px 14px;
     box-shadow: 0 2px 10px rgba(0,0,0,.4);
     color: var(--text);
   }
   ```
   (Light theme uses a lighter shadow, e.g. `rgba(0,0,0,.12)` — set via the
   `[data-theme=light]` override on `.saved-comment` box-shadow.)
2. **Header** — `.saved-comment .loc` becomes a flex row: teal, bold, with the
   severity badge pushed right:
   ```css
   .saved-comment .loc { display:flex; align-items:center; gap:6px; color: var(--comment-fg); font-weight:600; margin-bottom:4px; }
   .saved-comment .loc .sev { margin-left:auto; font-weight:700; font-size:10px; letter-spacing:.4px; text-transform:uppercase; padding:1px 7px; border-radius:3px; border:1px solid var(--border); color: var(--muted); }
   ```
3. **Prior override** — keep amber for prior, matching the new card shape:
   ```css
   .saved-comment.prior { border-left-color: #d29922; }
   .saved-comment.prior .loc { color: #e3b341; }
   ```
   (Replaces the old `border-left: 3px solid #d29922; padding-left:8px; margin-left:-8px` rule, which assumed the old no-card layout.)
4. **Commented block/line rail → teal** — change the doc block marker and the
   diff line-number chip from the blue accent to teal:
   - `.doc-block.has-comment-block { box-shadow: inset 3px 0 0 var(--comment); }`
   - `td.ln.has-comment { background: var(--comment); … }` (or its existing
     `--selected` treatment tinted teal) — keep it legible in both themes.
5. **Container spacing** — the doc/diff wrappers (`.doc-saved-comment`,
   `.saved-comment-row td`) drop redundant backgrounds/borders so the card isn't
   doubly boxed:
   - `.doc-saved-comment { max-width: 820px; margin: 8px auto 16px; }`
   - `.comment-row td` (diff): remove the `background: var(--panel-deep)` and
     top/bottom borders (the card now provides the visual box); keep padding so
     the card has room. Verify the card sits cleanly spanning the diff row.

## Error handling / edge cases

- Long comment bodies already wrap (`white-space: pre-wrap; max-width: 68ch`) —
  unchanged inside the card.
- A comment with no severity renders no badge (the `.sev` span is only emitted
  in severity mode) — the flex header still lays out correctly.
- Light theme: verify teal border/text contrast on the light panel and that the
  shadow is subtle (not a heavy dark blob).

## Testing

Consistent with the repo (visual verification for CSS; no unit tests for
styling).

- **Visual (browser + native window):**
  - Doc view (`-md`): a comment renders as a distinct teal card below its block;
    the block shows a teal left rail; a `-severity` comment shows the badge; a
    reloaded prior comment shows the amber card variant.
  - Diff view: a saved comment renders as the same teal card within its row,
    not double-boxed; the commented line-number chip is teal.
  - Toggle light/dark: the card, border, header, and shadow all read well in
    both themes.
- Confirm the diff **intra-line overlay** and everything else are visually
  unchanged (this is a comment-styling-only change).

## Documentation

Minor: the README "UI cheatsheet"/features mention that comments render as
distinct cards is nice-to-have; update only if it reads stale. No flag/config
changes.
