# Resolvable Comments + PRIOR Badge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-comment **Resolve/Unresolve** toggle whose resolved state persists in `review.md` (so the agent can skip done comments across iterations), and show a prominent amber **PRIOR** badge on prior comments (replacing the "· prior" text).

**Architecture:** `Comment` gains a `Resolved bool`. `renderMarkdown` appends a ` (resolved)` marker to the inline heading (after the LEFT/severity tokens, ungated by `-severity`); `inlineHeaderRe`/`loadPrior` parse it back. The UI adds a resolve toggle + resolved styling (grey/strike) and a PRIOR badge, across all three comment renderers.

**Tech Stack:** Go (`main.go`), embedded `index.html` (HTML/CSS/JS), standard-library testing. Branch `comment-resolve` (stacked on the teal-card styling, commit `7fcb631`).

## Global Constraints

- Production Go in `main.go`; tests in `main_test.go`; UI in `index.html`. Go floor 1.22.
- **`review.md` round-trip is load-bearing.** The `(resolved)` marker is additive and optional; existing headings (no marker) MUST still parse (as `Resolved:false`). Non-resolved comments render byte-for-byte as today.
- Heading token order: `path:line[-end]` → optional ` (LEFT)` → optional ` [SEVERITY]` → optional ` (resolved)`.
- The ` (resolved)` marker is emitted whenever `c.Resolved` is true, **independent of the `-severity` flag** (unlike the `[SEVERITY]` token).
- `resolve` ≠ `delete`: `delete` removes a comment entirely; `resolve` keeps it, marked done.
- PRIOR badge is amber, right-aligned in the comment header; coexists with the severity badge (severity then PRIOR).

---

## File Structure

- **Modify `main.go`:** `Comment.Resolved`; `renderMarkdown` marker; `inlineHeaderRe` + `loadPrior`.
- **Modify `index.html`:** resolved CSS + PRIOR-badge CSS (remove "· prior" text); resolve/unresolve button + handler and PRIOR badge + resolved class in all three renderers (`renderDocComments`, `renderComments`, `renderUnattached`); `load()` prior-seed gains `resolved`.
- **Modify `main_test.go`:** resolved emit + round-trip tests.
- **Modify `README.md`:** document resolve + PRIOR badge.

---

## Task 1: Go — `Resolved` field, `(resolved)` round-trip

**Files:**
- Modify: `main.go` — `Comment` (~line 216), `renderMarkdown` heading (~line 999-1006), `inlineHeaderRe` (~line 1025), `loadPrior` (~line 1207-1215)
- Test: `main_test.go`

**Interfaces:**
- Produces: `Comment.Resolved bool` (`json:"resolved,omitempty"`); heading emits ` (resolved)` when resolved; `loadPrior` populates `Resolved`.

- [ ] **Step 1: Write the failing tests**

Add to `main_test.go`:

```go
func TestRenderMarkdownResolved(t *testing.T) {
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Resolved: true, Body: "x"},
		{Path: "b.go", Side: "new", Line: 8, Body: "y"}, // not resolved
	}}
	md := renderMarkdown("", "git", "", true, nil, req)
	if !strings.Contains(md, "### a.go:5 [IMPORTANT] (resolved)\n") {
		t.Errorf("resolved marker missing/misordered:\n%s", md)
	}
	if strings.Contains(md, "### b.go:8 [QUESTION] (resolved)") {
		t.Errorf("non-resolved comment must not get the marker:\n%s", md)
	}
}

func TestRenderMarkdownResolvedUngatedBySeverity(t *testing.T) {
	// resolved marker emits even when -severity is off (no [SEVERITY] token)
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Resolved: true, Body: "x"}}}
	md := renderMarkdown("", "git", "", false, nil, req)
	if !strings.Contains(md, "### a.go:5 (resolved)\n") {
		t.Errorf("resolved marker should emit without -severity:\n%s", md)
	}
}

func TestLoadPriorResolvedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Severity: "SUGGESTION", Resolved: true, Body: "s"}}}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", "", true, nil, req)), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || !got[0].Resolved {
		t.Fatalf("resolved round-trip = %+v, want Resolved true", got)
	}
}

func TestLoadPriorResolvedWithLeftAndSeverity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	src := "# R\n\n## Inline comments\n\n### a.go:7 (LEFT) [NITPICK] (resolved)\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Side != "old" || got[0].Severity != "NITPICK" || !got[0].Resolved {
		t.Fatalf("got %+v, want side=old severity=NITPICK resolved=true", got)
	}
}

func TestLoadPriorLegacyNotResolved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	src := "# R\n\n## Inline comments\n\n### a.go:5\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Resolved {
		t.Fatalf("legacy heading must parse Resolved=false, got %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'Resolved|LegacyNotResolved' -v`
Expected: FAIL — `Comment.Resolved` undefined (compile error).

- [ ] **Step 3: Add the `Resolved` field**

In the `Comment` struct (after `Severity`, ~line 216):

```go
	Resolved bool   `json:"resolved,omitempty"`
```

- [ ] **Step 4: Emit the marker in `renderMarkdown`**

After the `if severity { … }` block (~line 1005), before the `fmt.Fprintf(&b, "### %s\n\n", loc)` line, add:

```go
			if c.Resolved {
				loc += " (resolved)"
			}
```

(Note: NOT inside `if severity` — resolved always round-trips.)

- [ ] **Step 5: Extend the regex**

Replace `inlineHeaderRe` (~line 1025):

```go
var inlineHeaderRe = regexp.MustCompile(`^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?(?:\s+\[([A-Z]+)\])?(?:\s+\((resolved)\))?\s*$`)
```

- [ ] **Step 6: Parse resolved in `loadPrior`**

Change the `cur = &Comment{…}` construction (~line 1215) to set `Resolved` from group 6:

```go
				cur = &Comment{Path: m[1], Side: side, Line: start, EndLine: end, Severity: sev, Resolved: m[6] == "resolved"}
```

- [ ] **Step 7: Run tests + build**

Run: `go test ./... -v && go build ./... && go vet ./...`
Expected: all tests PASS (new + existing); clean build/vet.

- [ ] **Step 8: Commit**

```bash
git add main.go main_test.go
git commit -m "Add Comment.Resolved with (resolved) review.md round-trip"
```

---

## Task 2: UI — resolve toggle, resolved styling, PRIOR badge

**Files:**
- Modify: `index.html` — CSS (~line 130-146), `load()` prior-seed (~line 263), and all three comment renderers: `renderDocComments` (~line 610-640), `renderComments` (~line 760-800), `renderUnattached` (~line 810-840)

**Interfaces:**
- Consumes: `Comment.resolved` from `/diff` `prior` (Task 1).
- Produces: resolve/unresolve UI + resolved styling + PRIOR badge.

- [ ] **Step 1: CSS — resolved state, PRIOR badge, drop "· prior" text**

Change the badge/prior CSS block. Replace:

```css
  .saved-comment .loc .sev { margin-left: auto; font-weight: 700; font-size: 10px; letter-spacing: 0.4px; text-transform: uppercase; padding: 1px 7px; border-radius: 3px; border: 1px solid var(--border); color: var(--muted); }
```

with (keep `.sev`, add `.prior-badge`, both right-align as a group via `margin-left:auto`):

```css
  .saved-comment .loc .sev { margin-left: auto; font-weight: 700; font-size: 10px; letter-spacing: 0.4px; text-transform: uppercase; padding: 1px 7px; border-radius: 3px; border: 1px solid var(--border); color: var(--muted); }
  .saved-comment .loc .prior-badge { margin-left: auto; font-weight: 700; font-size: 10px; letter-spacing: 0.4px; text-transform: uppercase; padding: 1px 7px; border-radius: 3px; border: 1px solid #d29922; color: #e3b341; }
```

Replace the prior block:

```css
  .saved-comment.prior { border-left-color: #d29922; }
  .saved-comment.prior .loc { color: #e3b341; }
  .saved-comment.prior .loc::after { content: " · prior"; color: #d29922; font-size: 11px; }
```

with (drop the `::after` text — the badge replaces it — and add resolved styling):

```css
  .saved-comment.prior { border-left-color: #d29922; }
  .saved-comment.prior .loc { color: #e3b341; }
  .saved-comment.resolved { opacity: 0.6; }
  .saved-comment.resolved .loc { text-decoration: line-through; }
```

- [ ] **Step 2: Add JS badge/state helpers**

Near the top of the `<script>` (after the `SEVERITY_MODE`/`SEVERITIES` block), add helpers used by all three renderers:

```javascript
function badgesHTML(c) {
  const sev = SEVERITY_MODE ? `<span class="sev">${escapeHtml(c.severity || 'QUESTION')}</span>` : '';
  const prior = c.prior ? `<span class="prior-badge">prior</span>` : '';
  return sev + prior;
}
function resolveBtnHTML(c) {
  const a = c.resolved ? 'unresolve' : 'resolve';
  return `<button type="button" data-id="${c.id}" data-action="${a}">${a}</button>`;
}
```

- [ ] **Step 3: `renderDocComments` — class, loc badges, resolve button, handler**

- The card div: add the `resolved` class. Change the `.saved-comment` class expression to include resolved:
  ```javascript
  div.innerHTML = `
    <div class="saved-comment ${c.prior ? 'prior' : ''} ${c.resolved ? 'resolved' : ''}">
  ```
- The loc line: replace the inline `${SEVERITY_MODE ? …}` badge with `${badgesHTML(c)}`:
  ```javascript
        <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}${badgesHTML(c)}</div>
  ```
- The controls: add the resolve button before edit/delete:
  ```javascript
        <div class="controls">
          ${resolveBtnHTML(c)}
          <button type="button" data-id="${c.id}" data-action="edit">edit</button>
          <button type="button" data-id="${c.id}" data-action="del">delete</button>
        </div>
  ```
- The button handler: add resolve/unresolve to the `if (action === 'del') … else if (action === 'edit') …` chain:
  ```javascript
        if (action === 'del') { COMMENTS.splice(idx, 1); renderComments(); }
        else if (action === 'edit') { openEditModal(COMMENTS[idx]); }
        else if (action === 'resolve' || action === 'unresolve') { COMMENTS[idx].resolved = (action === 'resolve'); renderComments(); }
  ```

- [ ] **Step 4: `renderComments` (diff view) — same four changes**

Apply the identical four edits in `renderComments` (~line 760-800): add `${c.resolved ? 'resolved' : ''}` to the `.saved-comment` class; replace its severity-badge expression in the loc with `${badgesHTML(c)}`; add `${resolveBtnHTML(c)}` to the controls (before edit/delete, alongside the existing `open` button); add the `resolve`/`unresolve` branch to its action handler (~line 836-837).

- [ ] **Step 5: `renderUnattached` — same (these are always prior)**

In `renderUnattached` (~line 810-840): the div already uses `class="saved-comment prior"` — add `${c.resolved ? ' resolved' : ''}`. Replace its loc badge expression with `${badgesHTML(c)}` (so the PRIOR badge shows). Add `${resolveBtnHTML(c)}` to its controls and the `resolve`/`unresolve` branch to its handler.

- [ ] **Step 6: Seed `resolved` from prior comments in `load()`**

In the prior-seed loop (~line 263), add `resolved`:

```javascript
        severity: c.severity || 'QUESTION',
        resolved: c.resolved || false,
        prior: true,
```

(The payload sent to `/save`/`/submit`/`/markdown` maps `COMMENTS.map(({id, prior, ...rest}) => rest)`, so `resolved` is already forwarded via `...rest` — no transport change.)

- [ ] **Step 7: Verify build + both modes**

Run:
```bash
go build -o /tmp/gutter . && printf '# D\n\npara\n' > /tmp/r.md && \
/tmp/gutter -md /tmp/r.md -open=false -port=9994 >/dev/null 2>&1 & sleep 2; \
echo "resolve helper present:"; curl -s http://127.0.0.1:9994/ | grep -c 'function resolveBtnHTML'; \
echo "prior-badge css present:"; curl -s http://127.0.0.1:9994/ | grep -c 'prior-badge'; \
echo "no · prior text:"; curl -s http://127.0.0.1:9994/ | grep -c '· prior'; \
kill %1 2>/dev/null
```
Expected: `resolveBtnHTML` present (1), `prior-badge` css present (≥1), `· prior` count 0 (removed). Interactive behavior verified in Task 3.

- [ ] **Step 8: Commit**

```bash
git add index.html
git commit -m "UI: resolve/unresolve toggle, resolved styling, PRIOR badge"
```

---

## Task 3: Manual/visual verification

**Files:** none (verification only).

- [ ] **Step 1: Build (window) + fixtures**

```bash
./scripts/build-window.sh -o /tmp/gutter .
printf '# Plan\n\nGoal one paragraph.\n\n## Section\n\nSecond paragraph.\n' > /tmp/r.md
```

- [ ] **Step 2: Sync round-trip — resolved persists + agent-visible marker**

```bash
GUTTER_OUTPUT=/tmp/none.md /tmp/gutter -sync -severity -md /tmp/r.md -open=false -port=9993 >/tmp/r.out 2>/dev/null &
GPID=$!; sleep 2
curl -s -X POST http://127.0.0.1:9993/submit -d '{"general":"","comments":[
  {"path":"/tmp/r.md","side":"new","line":3,"end_line":3,"severity":"SUGGESTION","resolved":true,"body":"done"},
  {"path":"/tmp/r.md","side":"new","line":6,"end_line":6,"severity":"BLOCKING","resolved":false,"body":"open"}]}'
echo; wait $GPID 2>/dev/null
grep -q '### /tmp/r.md:3 \[SUGGESTION\] (resolved)' /tmp/r.out && echo "resolved marker OK" || echo "resolved marker FAIL"
grep -q '### /tmp/r.md:6 \[BLOCKING\]$' /tmp/r.out && echo "open (no marker) OK" || echo "open FAIL"
```
Expected: `resolved marker OK`, `open (no marker) OK` — the agent-facing output distinguishes resolved from open.

- [ ] **Step 3: Reload shows resolved + prior**

```bash
# write a review.md, then reload in non-sync doc mode
cat > /tmp/r-review.md <<'EOF'
# Review of /tmp/r.md

## Inline comments

### /tmp/r.md:3 (resolved)

Already handled.
EOF
GUTTER_OUTPUT=/tmp/r-review.md /tmp/gutter -md /tmp/r.md -open=false -port=9992 >/dev/null 2>&1 &
GPID=$!; sleep 2
curl -s http://127.0.0.1:9992/diff | grep -o '"resolved":true' | head -1
kill $GPID 2>/dev/null; rm -f /tmp/none.md /tmp/r-review.md
```
Expected: `"resolved":true` present — the reloaded prior comment carries resolved state.

- [ ] **Step 4: Visual (browser + native window)**

`/tmp/gutter -window -md /tmp/r.md` (and a diff via `/tmp/gutter -window`): add a comment, click **resolve** → it greys + strikes the header, button flips to **unresolve**; click **unresolve** → reverts. Preload the `/tmp/r-review.md` above to see a **prior** comment with the amber **prior** badge, and confirm a `-severity` comment shows both the severity badge and (when prior) the PRIOR badge. Toggle light/dark.

---

## Task 4: Documentation

**Files:** Modify `README.md`.

- [ ] **Step 1: Document resolve + PRIOR badge**

- UI cheatsheet / comments section: a **Resolve/Unresolve** control marks a comment done (greyed + struck) without deleting it; resolved state is saved in `review.md` (as a `(resolved)` marker on the comment heading) so it persists across review iterations and an agent can skip resolved comments.
- Note prior comments now show a **PRIOR** badge.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "README: document resolvable comments and PRIOR badge"
```

---

## Self-Review Notes

- **Spec coverage:** `Resolved` field + `(resolved)` emit (ungated) + regex/loadPrior round-trip + backward-compat → Task 1; resolve toggle + resolved styling + PRIOR badge + drop "· prior" + load-seed → Task 2; e2e (agent-visible marker, persist/reload, visual) → Task 3; docs → Task 4.
- **Type/name consistency:** `Comment.Resolved`/JSON `resolved`; heading marker ` (resolved)`; regex group 6; JS `badgesHTML`/`resolveBtnHTML`, `data-action` `resolve`/`unresolve`, classes `.saved-comment.resolved`/`.prior-badge` — consistent across tasks and all three renderers.
- **Round-trip safety:** marker is additive/optional; `TestLoadPriorLegacyNotResolved` guards old headings; order tested via the `(LEFT) [NITPICK] (resolved)` case; non-resolved output unchanged.
- **Payload:** `resolved` rides the existing `...rest` spread to the server — no transport change; `load()` seeds it from prior.
- **Three renderers:** `renderDocComments`, `renderComments`, `renderUnattached` all get the identical four-part change (class, badges, resolve button, handler) so behavior is uniform across doc/diff/unattached.
