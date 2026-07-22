# Typography + Font Zoom Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make gutter more readable — bundled JetBrains Mono for code/diff, a proportional system sans for prose/chrome, larger base size — and add a segmented `[ − 100% + ]` font-zoom control (+ `Ctrl/Cmd +/−/0`) that works in the browser and the native window.

**Architecture:** Bundle a Latin-subset JetBrains Mono woff2 via `go:embed`, served from `/assets/jetbrains-mono.woff2`, referenced by `@font-face`. Two CSS font-family variables (`--font-mono` for code/diff, `--font-sans` for prose/chrome). Zoom is the CSS `zoom` property on `document.body`, driven by a header segmented control + keyboard shortcuts, persisted in `localStorage`.

**Tech Stack:** Go (`main.go`, `go:embed`), embedded `index.html` (HTML/CSS/JS), a vendored woff2 font asset, standard-library testing.

## Global Constraints

- The diff/code view MUST stay monospace (the two-layer intra-line overlay must align char-for-char) — CLAUDE.md invariant. Only prose/chrome may become proportional.
- gutter is a single self-contained binary; the UI is one embedded `index.html` and may run OFFLINE in the webview. The font is served by gutter itself (no CDN).
- gutter is MIT; JetBrains Mono is OFL-1.1 (permits embedding + redistribution). Ship `fonts/OFL.txt`; the font stays OFL, code stays MIT.
- Bundled font is a Latin subset woff2 (~21 KB) from fontsource's `jetbrains-mono@latest/latin-400-normal.woff2` — a subset only (no glyph edits), so the "JetBrains Mono" reserved name is retained. One weight (Regular/400).
- Zoom via CSS `zoom` on `document.body` (uniform scale, keeps overlay aligned); level clamped 0.7–2.0 in 0.1 steps, persisted under `localStorage` key `gutter_zoom`.
- Zoom control is **Variant B — a segmented pill** `[ − 100% + ]` placed just before the theme toggle; the `%` resets to 100% on click; also bind `Ctrl/Cmd +`, `Ctrl/Cmd =`, `Ctrl/Cmd -`, `Ctrl/Cmd 0`.

---

## File Structure

- **Create `fonts/jetbrains-mono-latin-400.woff2`** — vendored Latin subset (font asset).
- **Create `fonts/OFL.txt`** — JetBrains Mono SIL OFL-1.1 license text.
- **Modify `main.go`** — `go:embed` the font; serve it at `/assets/jetbrains-mono.woff2`.
- **Modify `index.html`** — `@font-face`; `--font-mono`/`--font-sans` vars; apply sans to prose/chrome + mono to code/diff; base 14px + line-heights + ~68ch caps; the segmented zoom control (markup, CSS, JS, shortcuts, persistence).
- **Modify `main_test.go`** — the font route serves 200 + `font/woff2`.
- **Modify `README.md`** — readability/zoom note + OFL attribution in the License section.

---

## Task 1: Vendor the font asset + license, embed & serve it

**Files:**
- Create: `fonts/jetbrains-mono-latin-400.woff2`, `fonts/OFL.txt`
- Modify: `main.go` (embed + route)
- Test: `main_test.go`

**Interfaces:**
- Produces: an HTTP route `GET /assets/jetbrains-mono.woff2` → the embedded woff2 with `Content-Type: font/woff2`.

- [ ] **Step 1: Download the font subset + license**

Run:
```bash
mkdir -p fonts
curl -sSL -o fonts/jetbrains-mono-latin-400.woff2 \
  "https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono@latest/latin-400-normal.woff2"
curl -sSL -o fonts/OFL.txt \
  "https://raw.githubusercontent.com/JetBrains/JetBrainsMono/master/OFL.txt"
# sanity: woff2 magic + license header
printf 'magic: '; head -c4 fonts/jetbrains-mono-latin-400.woff2; echo
head -1 fonts/OFL.txt
ls -l fonts/
```
Expected: `magic: wOF2`; the OFL.txt first line is the copyright (contains "Copyright"); the woff2 is ~15–25 KB.

- [ ] **Step 2: Write the failing test**

Add to `main_test.go`:

```go
func TestFontAssetServed(t *testing.T) {
	req := httptest.NewRequest("GET", "/assets/jetbrains-mono.woff2", nil)
	w := httptest.NewRecorder()
	fontHandler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Errorf("Content-Type = %q, want font/woff2", ct)
	}
	if w.Body.Len() < 1000 {
		t.Errorf("body too small: %d bytes", w.Body.Len())
	}
}
```

(Add `"net/http/httptest"` to `main_test.go`'s imports if not present.)

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./... -run TestFontAssetServed -v`
Expected: FAIL — `undefined: fontHandler`.

- [ ] **Step 4: Embed the font and add the handler**

In `main.go`, extend the embed directive (near `//go:embed index.html`, ~line 25):

```go
//go:embed index.html
//go:embed fonts/jetbrains-mono-latin-400.woff2
var assets embed.FS
```

Add a `fontHandler` helper (package-level, near the other handlers/helpers):

```go
// fontHandler serves the embedded JetBrains Mono woff2 so the UI font works
// offline (no CDN), including in the native window.
func fontHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := assets.ReadFile("fonts/jetbrains-mono-latin-400.woff2")
		if err != nil {
			http.Error(w, "font not found", 404)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(b)
	})
}
```

Register it in `main` alongside the other `mux.HandleFunc` calls (e.g. after the `/` handler, ~line 467):

```go
	mux.Handle("/assets/jetbrains-mono.woff2", fontHandler())
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run TestFontAssetServed -v && go build ./... && go vet ./...`
Expected: PASS; clean build/vet.

- [ ] **Step 6: Commit**

```bash
git add fonts/jetbrains-mono-latin-400.woff2 fonts/OFL.txt main.go main_test.go
git commit -m "Bundle JetBrains Mono (OFL) and serve it at /assets/jetbrains-mono.woff2"
```

---

## Task 2: Fonts — @font-face, family variables, prose/code split, sizing

**Files:**
- Modify: `index.html` — the `<style>` block: add `@font-face`, `:root` font vars, base font, and per-context `font-family`; base size + line-heights + measure caps

**Interfaces:**
- Consumes: `/assets/jetbrains-mono.woff2` (Task 1).
- Produces: `--font-mono` / `--font-sans` CSS variables; code/diff in mono, prose/chrome in sans.

- [ ] **Step 1: Add `@font-face` + font variables**

At the very top of the `<style>` block (before the existing `:root` rule), add:

```css
  @font-face {
    font-family: "JetBrains Mono";
    font-style: normal;
    font-weight: 400;
    font-display: swap;
    src: url("/assets/jetbrains-mono.woff2") format("woff2");
  }
```

Inside the existing `:root, html[data-theme="dark"] { … }` rule (add two vars; they're theme-independent but `:root` is fine), add:

```css
    --font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, "SF Mono", "Cascadia Mono", "Segoe UI Mono", Menlo, Consolas, "Liberation Mono", monospace;
    --font-sans: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
```

- [ ] **Step 2: Base font → sans + 14px**

Change the `html, body` rule (~line 44) from:

```css
  html, body { margin: 0; padding: 0; background: var(--bg); color: var(--text); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
```

to:

```css
  html, body { margin: 0; padding: 0; background: var(--bg); color: var(--text); font-family: var(--font-sans); font-size: 14px; }
```

- [ ] **Step 3: Code/diff → mono**

The diff table currently inherits (`table.diff { font-family: inherit; }`) — now that the body is sans, code must explicitly opt into mono. Change the diff/code rules:

- `table.diff { … font-family: inherit; }` → `font-family: var(--font-mono);`
- `table.diff td { … line-height: 1.5; }` → `line-height: 1.45;`
- `td.text .bg-layer { … font: inherit; }` → replace `font: inherit;` with `font-family: var(--font-mono); font-size: inherit;` (keep the rest of the rule).
- `td.text code.hljs { … font: inherit; }` → `font-family: var(--font-mono); font-size: inherit;` (keep `background: transparent !important; padding: 0;`).

And the markdown doc view code (add/adjust in the `.doc-block` rules):

```css
  .doc-block pre, .doc-block pre code, .doc-block code { font-family: var(--font-mono); }
```

(Replace the existing `.doc-block code { font-family: inherit; }` with the line above.)

- [ ] **Step 4: Prose readability — line-height + measure caps**

- `.doc-block p, .doc-block ul, .doc-block ol { … line-height: 1.55; }` → `1.5`.
- Comment body prose: add `line-height: 1.5;` to `.saved-comment .body` (currently only `white-space: pre-wrap;`).
- Cap comment reading measure — change `.saved-comment .body` to include `max-width: 68ch;`.
- The `.doc` column is already capped (`max-width: 860px`); leave it (≈ a comfortable measure at 14px).

- [ ] **Step 5: Verify build + serve, font loads, alignment holds**

Run:
```bash
go build -o /tmp/gutter . && \
printf '# Doc\n\nProse here.\n\n```go\nx := 1 // code stays mono\n```\n' > /tmp/f.md && \
/tmp/gutter -md /tmp/f.md -open=false -port=9995 >/dev/null 2>&1 & sleep 2; \
echo "font-face present:"; curl -s http://127.0.0.1:9995/ | grep -c '@font-face'; \
echo "mono var present:"; curl -s http://127.0.0.1:9995/ | grep -c -- '--font-mono'; \
echo "font asset 200:"; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9995/assets/jetbrains-mono.woff2; \
kill %1 2>/dev/null
```
Expected: `@font-face` present (1), `--font-mono` present (≥1), font asset returns 200. (Visual check of prose-vs-mono + diff alignment happens in Task 4.)

- [ ] **Step 6: Commit**

```bash
git add index.html
git commit -m "Typography: JetBrains Mono for code, proportional sans for prose, larger base"
```

---

## Task 3: Segmented zoom control + keyboard shortcuts + persistence

**Files:**
- Modify: `index.html` — header markup (~line 159, before `#themeToggle`), CSS (`.seg`), and the script (zoom state/apply/handlers, near the theme JS ~line 439-460)

**Interfaces:**
- Consumes: nothing (CSS `zoom` on `document.body`).
- Produces: a `[ − 100% + ]` control (`#zoomOut`/`#zoomPct`/`#zoomIn`), `applyZoom(level)`, `gutter_zoom` persistence, `Ctrl/Cmd +/−/0` handlers.

- [ ] **Step 1: Add the segmented control CSS**

Add near the header button CSS (after the `header button:hover` rule):

```css
  .seg { display: inline-flex; border: 1px solid var(--border); border-radius: 4px; overflow: hidden; }
  .seg button { background: transparent; border: 0; color: var(--text); padding: 4px 11px; cursor: pointer; font: inherit; }
  .seg button:hover { background: var(--selected); }
  .seg .pct { border-left: 1px solid var(--border); border-right: 1px solid var(--border); color: var(--muted); min-width: 52px; text-align: center; font-variant-numeric: tabular-nums; }
```

- [ ] **Step 2: Add the control markup**

In the header, immediately before the `#themeToggle` button (~line 160), add:

```html
  <div class="seg" title="Zoom (Ctrl +/-/0)">
    <button type="button" id="zoomOut" title="Zoom out (Ctrl -)">−</button>
    <button type="button" class="pct" id="zoomPct" title="Reset zoom (Ctrl 0)">100%</button>
    <button type="button" id="zoomIn" title="Zoom in (Ctrl +)">+</button>
  </div>
```

- [ ] **Step 3: Add the zoom JS**

After the theme block (~after line 460), add:

```javascript
const ZOOM_MIN = 0.7, ZOOM_MAX = 2.0, ZOOM_STEP = 0.1;
let zoom = (() => {
  const v = parseFloat((() => { try { return localStorage.getItem('gutter_zoom'); } catch (e) { return null; } })());
  return (v >= ZOOM_MIN && v <= ZOOM_MAX) ? v : 1.0;
})();
function applyZoom(level) {
  zoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(level * 10) / 10));
  document.body.style.zoom = zoom;
  document.getElementById('zoomPct').textContent = Math.round(zoom * 100) + '%';
  try { localStorage.setItem('gutter_zoom', String(zoom)); } catch (e) {}
}
applyZoom(zoom);
document.getElementById('zoomIn').addEventListener('click', () => applyZoom(zoom + ZOOM_STEP));
document.getElementById('zoomOut').addEventListener('click', () => applyZoom(zoom - ZOOM_STEP));
document.getElementById('zoomPct').addEventListener('click', () => applyZoom(1.0));
document.addEventListener('keydown', (e) => {
  if (!(e.ctrlKey || e.metaKey)) return;
  if (e.key === '=' || e.key === '+') { e.preventDefault(); applyZoom(zoom + ZOOM_STEP); }
  else if (e.key === '-') { e.preventDefault(); applyZoom(zoom - ZOOM_STEP); }
  else if (e.key === '0') { e.preventDefault(); applyZoom(1.0); }
});
```

- [ ] **Step 4: Verify build + control present in both modes**

Run:
```bash
go build -o /tmp/gutter . && \
/tmp/gutter -md /tmp/f.md -open=false -port=9996 >/dev/null 2>&1 & sleep 2; \
echo "zoom control present:"; curl -s http://127.0.0.1:9996/ | grep -c 'id="zoomPct"'; \
echo "seg css present:"; curl -s http://127.0.0.1:9996/ | grep -c '.seg button'; \
kill %1 2>/dev/null; \
/tmp/gutter -open=false -port=9997 >/dev/null 2>&1 & sleep 2; \
echo "non-doc / 200:"; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9997/; \
kill %1 2>/dev/null
```
Expected: `id="zoomPct"` present (1), `.seg button` css present (1), non-doc `/` returns 200. (Interactive zoom behavior verified in Task 4.)

- [ ] **Step 5: Commit**

```bash
git add index.html
git commit -m "Add segmented font-zoom control with Ctrl +/-/0 and persistence"
```

---

## Task 4: Manual/visual verification

**Files:** none (verification only).

- [ ] **Step 1: Build + fixtures**

Run: `go build -o /tmp/gutter . && printf '# Heading\n\nA readable paragraph of prose.\n\n```go\nfunc main() { x := 1 }\n```\n\n- list item\n' > /tmp/f.md`

- [ ] **Step 2: Browser — fonts + zoom + alignment**

Run `/tmp/gutter -md /tmp/f.md -port 9998` (opens browser) and confirm:
- Prose (heading, paragraph, list) renders in a **proportional** font; the fenced **code block stays monospace** (JetBrains Mono).
- The `[ − 100% + ]` control sits left of the theme toggle. Clicking `+`/`−` scales the whole UI; the `%` updates; clicking it resets to 100%. `Ctrl +`, `Ctrl -`, `Ctrl 0` do the same.
- Reload the page → zoom level persists.
- Open a **diff** (`/tmp/gutter -port 9998` in a repo with changes) and confirm the intra-line word-diff overlay still aligns char-for-char at 100% and while zoomed. Quit when done.

- [ ] **Step 3: Native window — offline font + zoom**

Run `/tmp/gutter -window -md /tmp/f.md` and confirm the window opens with JetBrains Mono code (served from `/assets/…`, i.e. offline), proportional prose, and that the zoom control + `Ctrl +/-/0` work inside the webview. Close the window.

- [ ] **Step 4: Font actually applied (not just fallback)**

In the browser devtools (or via a quick check), confirm a code cell's computed `font-family` begins with `"JetBrains Mono"` and the `/assets/jetbrains-mono.woff2` request is 200 — i.e. the bundled font loaded, not only the system fallback.

---

## Task 5: Documentation

**Files:** Modify `README.md`.

- [ ] **Step 1: Document readability + zoom + attribution**

- Note the typography pass: proportional system sans for prose/chrome, bundled JetBrains Mono for code/diff, larger base size.
- Document zoom: the `[ − 100% + ]` control and `Ctrl/Cmd +`, `Ctrl/Cmd -`, `Ctrl/Cmd 0`, persisted per browser/window.
- In the **License** section, add attribution: "Bundles JetBrains Mono, © the JetBrains Mono authors, under the SIL Open Font License 1.1 — see `fonts/OFL.txt`. gutter's own code is MIT." Add the `-version`/config tables? (no — none needed here.)

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "README: document typography, font zoom, and JetBrains Mono (OFL) attribution"
```

---

## Self-Review Notes

- **Spec coverage:** bundle+serve font → Task 1; @font-face + family vars + prose/code split + sizing/line-height/measure → Task 2; segmented zoom control + shortcuts + persistence → Task 3; manual visual (browser + window, alignment, offline font, zoom) → Task 4; docs + OFL attribution → Task 5.
- **Type/name consistency:** `fontHandler()`, route `/assets/jetbrains-mono.woff2`, embed path `fonts/jetbrains-mono-latin-400.woff2`, CSS vars `--font-mono`/`--font-sans`, `--font-mono` first entry `"JetBrains Mono"`, control ids `#zoomOut`/`#zoomPct`/`#zoomIn`, `applyZoom`, `gutter_zoom` — consistent across tasks.
- **Monospace invariant:** Task 2 explicitly moves every code/diff surface (diff table, overlay `.bg-layer`, `code.hljs`, and markdown `pre`/`code`) to `--font-mono` while only prose goes sans; Task 4 verifies the overlay alignment at 100% and zoomed.
- **Offline:** the font is embedded and served by gutter (`/assets/…`), not a CDN, so the native window works offline; Task 4 Step 3/4 verify.
- **No placeholders:** every code step is complete; every run step states expected output.
