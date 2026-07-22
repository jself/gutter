# Design: readable typography + font zoom

Date: 2026-07-22

## Goal

Make gutter's UI more readable — a proportional system font for prose/chrome,
the bundled JetBrains Mono for code/diff, larger relative sizing — and add a
font zoom (in-UI + keyboard) that works in both the browser and the native
window.

## Non-goals

- No change to the diff overlay mechanism, colors/theme, or layout beyond
  font-family, font-size, line-height, and reading-measure caps.
- No multiple font weights bundled — one JetBrains Mono weight (Regular). UI
  bold comes from the proportional sans stack.
- No settings UI beyond the zoom controls; font choice is not user-configurable.

## Constraints

- The diff/code view relies on **monospace alignment** (the two-layer intra-line
  overlay must line up char-for-char). Code/diff MUST stay monospace; only
  prose/chrome may become proportional. (CLAUDE.md invariant.)
- gutter is a single self-contained binary; the UI is one embedded `index.html`
  and may run **offline in a webview**. No runtime CDN dependency for fonts — the
  font is served by gutter itself.
- gutter is MIT-licensed. JetBrains Mono is **OFL-1.1**, which permits embedding
  and redistribution in software of any license. The two coexist: the font file
  stays OFL, gutter's code stays MIT.

## 1. Font families

Two CSS variables on `:root`:

```css
--font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, "SF Mono",
             "Cascadia Mono", "Segoe UI Mono", Menlo, Consolas,
             "Liberation Mono", monospace;
--font-sans: system-ui, -apple-system, "Segoe UI", Roboto,
             "Helvetica Neue", Arial, sans-serif;
```

- **Code/diff → `--font-mono`.** The diff table (`table.diff`, `td.text`,
  `.bg-layer`, `code.fg-layer`) AND all code in rendered markdown — fenced blocks
  and inline code (`.doc-block pre`, `.doc-block pre code`, `.doc-block code`) —
  plus any snippet/`<code>` element. **Only the prose *around* code goes
  proportional; code itself always stays monospace** (per review feedback), so
  code alignment/readability in the `-md` doc view is preserved.
- **Prose/chrome → `--font-sans`.** `html, body` default; sidebar, header,
  buttons, comment bodies/forms, general-feedback textarea, the edit modal, and
  rendered-markdown prose (`.doc-block` text — but NOT its `pre`/`code`).
- JetBrains Mono is the first choice for code with the hardened system-mono
  stack as offline fallback; if the font fails to load, code still renders in a
  good system monospace with correct alignment.

### Bundling JetBrains Mono (offline, served by gutter)

- Vendor a **Latin subset woff2** at `fonts/jetbrains-mono-latin-400.woff2`
  (~21 KB, obtained from fontsource's `jetbrains-mono@latest/latin-400-normal.woff2`,
  which is the OFL font subset — no glyph modification, so the Reserved Font Name
  "JetBrains Mono" is retained).
- `go:embed` the woff2 and serve it from a new handler at
  `/assets/jetbrains-mono.woff2` with `Content-Type: font/woff2` and a long
  `Cache-Control`. (Served by gutter → works fully offline, unlike a CDN.)
- `@font-face` in `index.html`:
  ```css
  @font-face {
    font-family: "JetBrains Mono";
    font-style: normal;
    font-weight: 400;
    font-display: swap;
    src: url("/assets/jetbrains-mono.woff2") format("woff2");
  }
  ```
- Serving via a URL (not a base64 data-URI) keeps `index.html` clean; still
  offline because gutter is the origin.

### Licensing artifacts

- Add `fonts/OFL.txt` (JetBrains Mono's SIL OFL-1.1 text + copyright), obtained
  from the official JetBrains/JetBrainsMono repo.
- Note the bundled font + its OFL license in `README.md` (License section) so
  the attribution ships with the source. gutter's own `LICENSE` (MIT) is
  unchanged.

## 2. Sizing & readability

- **Base font-size → 14px** on `html, body` (from 13px) for a more readable
  baseline. The existing px font-sizes on small chrome (11/12/13px sidebar,
  badges, status, hints, modal) stay as-is — zoom scales the whole UI uniformly
  (see §3), so no mass `px`→`rem` conversion is needed.
- **Line-heights:** code/diff `line-height: 1.45` (from 1.5, slightly tighter for
  denser diffs while staying ≥1.4); prose (`.doc-block` text, comment bodies)
  `1.5`.
- **Reading measure:** cap prose columns at **~68ch** — the doc view already caps
  `.doc` at a px max-width; switch/confirm to a ch-based cap, and cap comment
  body width similarly so long comments/markdown don't run the full window width.

## 3. Font zoom (browser + native window)

The native window (webview) doesn't reliably expose browser zoom, so zoom is
implemented in-UI with the **CSS `zoom` property**, which uniformly scales the
whole UI — font sizes *and* spacing/layout, like real browser zoom — in one
assignment. A uniform scale keeps both diff-overlay layers (`.bg-layer` /
`code.fg-layer`) aligned. `zoom` is well-supported in WebKit (the native window)
and in modern browsers.

- **Mechanism:** a zoom level stored in `localStorage` under `gutter_zoom` (a
  multiplier, default `1.0`, clamped `0.7`–`2.0` in fixed `0.1` steps). Applied
  as `document.body.style.zoom = level`. An `applyZoom(level)` function sets the
  body zoom and updates the `%` readout in the control.
- **Controls (Variant B — segmented pill):** one joined segmented control
  `[ −  100%  + ]` placed just before the theme toggle in the header — a `−`
  button, a middle `%` readout that resets to 100% on click, and a `+` button,
  bordered as a single grouped widget (`.seg` with a shared border, hover
  `var(--selected)`, tabular-nums for the %). Reads as one "zoom" widget rather
  than three loose buttons.
- **Keyboard:** `Ctrl/Cmd +` and `Ctrl/Cmd =` (zoom in), `Ctrl/Cmd -` (out),
  `Ctrl/Cmd 0` (reset). `preventDefault` so the browser's own zoom doesn't also
  fire; in the webview these are the only handler.
- **Persistence:** read `gutter_zoom` on load and apply before first paint;
  write on every change. Same pattern as the existing `gutter_theme` /
  `gutter_sidebar_hidden` keys.
- **Alignment safety:** CSS `zoom` scales the whole subtree by the same factor,
  so the diff overlay's `.bg-layer` and `code.fg-layer` stay char-for-char
  aligned. Verify visually after implementing (the CLAUDE.md overlay invariant).

## Error handling / edge cases

- Font fails to load (missing asset, decode error): `@font-face` falls through to
  the system-mono stack; alignment preserved. Non-fatal.
- `localStorage` unavailable/blocked: zoom still works for the session (in-memory
  default), just not persisted — wrap reads/writes in try/catch like the existing
  theme code.
- Zoom clamped to the min/max so the UI can't be scaled into uselessness.

## Testing

- **Manual/visual** (the historical gutter verification path): after the change,
  open a diff and a `-md` doc in both the browser and the native window;
  confirm (a) prose/chrome is proportional and code/diff is JetBrains Mono,
  (b) the diff intra-line overlay still aligns char-for-char, (c) zoom in/out via
  buttons and `Ctrl +/-/0` scales everything together and persists across reload,
  (d) offline: the font loads from `/assets/jetbrains-mono.woff2` (served by
  gutter), verified by loading with no network.
- **Automated (light):** a Go test that `GET /assets/jetbrains-mono.woff2`
  returns 200 with `Content-Type: font/woff2` and a non-empty body (the embed +
  handler wiring), which needs no GUI.

## Documentation

- README: note the readability pass (proportional prose + JetBrains Mono code),
  the zoom controls/shortcuts, and — in the License section — the bundled
  JetBrains Mono under OFL-1.1 with a pointer to `fonts/OFL.txt`.
