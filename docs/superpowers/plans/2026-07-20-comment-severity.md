# Comment Severity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a severity select to gutter comments and emit the chosen severity as a trailing `[SEVERITY]` token on the inline-comment heading (`### path:line [QUESTION]`) in both `-sync` stdout and `review.md`, so the centaur-review skill can parse and route comments.

**Architecture:** Additive. `Comment` gains a `Severity` field; `renderMarkdown` appends ` [SEVERITY]` after the existing optional ` (LEFT)`; `inlineHeaderRe`/`loadPrior` gain an optional trailing group that defaults to `QUESTION` when absent. The UI adds a `<select>` (default QUESTION) to both inline add-forms and the edit modal.

**Tech Stack:** Go (single `main.go`), embedded `index.html` (Go html/template), standard-library testing.

## Global Constraints

- All production Go code in `main.go`; tests in `main_test.go`; UI in `index.html`. Go floor 1.22; standard library only (no new deps).
- **Markdown round-trip is load-bearing:** old headings with no `[..]` token (and `(LEFT)` headings) MUST still parse; a consumer treats an absent token as `QUESTION`.
- **Purely additive to the heading.** Do not change: range-comment behavior, line anchoring, the snippet fence, the `## General feedback` / `## Inline comments` structure, or the `-sync` stdout/stderr split.
- Severity token order on the heading: `path:line` → optional `-end` → optional ` (LEFT)` → optional ` [SEVERITY]`.
- Five severities, uppercase: `BLOCKING`, `IMPORTANT`, `SUGGESTION`, `QUESTION`, `NITPICK`. Default `QUESTION`.
- **The whole feature is gated behind an opt-in `-severity` flag (default false).** With the flag off: no select box and NO `[SEVERITY]` token — output is byte-for-byte unchanged from today. With the flag on: select box + token (always emitted, including `[QUESTION]`). Add matching `GUTTER_SEVERITY` env and `severity` JSON field.
- General-feedback severity is OUT of scope (inline only).

**NOTE (revised mid-flight):** Task 1 landed an *always-emit* version (commit `c115c8a`); Task 2 below gates emission behind the new `-severity` flag and adjusts Task 1's tests accordingly. Tasks were renumbered when the flag was added — Task 1 (Go field/emit/round-trip) is done; the remaining tasks are the flag gate, the flag-gated UI, e2e, and docs + an `-md` screenshot.

---

## File Structure

- **Modify `main.go`:** `Comment.Severity`; `renderMarkdown` heading emit; `inlineHeaderRe` + `loadPrior` severity parse.
- **Modify `index.html`:** `SEVERITIES` const + `severitySelectHTML` helper; select in `openCommentForm`, `openDocCommentForm`, edit modal; severity in the `load()` prior-seed and the three saved-comment `.loc` displays.
- **Modify `main_test.go`:** severity emit + round-trip tests.
- **Modify `README.md`:** document the severity token.

---

## Task 1: Go — severity field, output token, round-trip

**Files:**
- Modify: `main.go` — `Comment` struct (~line 216), `renderMarkdown` LEFT block (~line 925), `inlineHeaderRe` (~line 947), `loadPrior` inline-header branch (~line 1122)
- Test: `main_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `Comment.Severity string` (`json:"severity,omitempty"`); `renderMarkdown` emits ` [SEVERITY]`; `loadPrior` populates `Severity` (default `"QUESTION"`).

- [ ] **Step 1: Write the failing tests**

Add to `main_test.go`:

```go
func TestRenderMarkdownSeverity(t *testing.T) {
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Body: "x"},
		{Path: "b.go", Side: "new", Line: 8, Body: "y"}, // empty -> QUESTION
		{Path: "c.go", Side: "new", Line: 10, EndLine: 12, Severity: "SUGGESTION", Body: "z"},
	}}
	md := renderMarkdown("", "git", "", nil, req)
	for _, want := range []string{
		"### a.go:5 [IMPORTANT]\n",
		"### b.go:8 [QUESTION]\n",
		"### c.go:10-12 [SUGGESTION]\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n---\n%s", want, md)
		}
	}
}

func TestRenderMarkdownSeverityAfterLeft(t *testing.T) {
	pr := &PRInfo{Repo: "o/r", Number: 1, Head: "h", Base: "b"}
	req := SaveRequest{Comments: []Comment{{Path: "c.go", Side: "old", Line: 7, Severity: "NITPICK", Body: "z"}}}
	md := renderMarkdown("", "git", "", pr, req)
	if !strings.Contains(md, "### c.go:7 (LEFT) [NITPICK]\n") {
		t.Errorf("severity should follow (LEFT):\n%s", md)
	}
}

func TestLoadPriorSeverityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Severity: "SUGGESTION", Body: "s"}}}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", "", nil, req)), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Severity != "SUGGESTION" {
		t.Fatalf("round-trip severity = %+v, want SUGGESTION", got)
	}
}

func TestLoadPriorSeverityLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	legacy := "# Review of `@` (jj)\n\n## Inline comments\n\n### a.go:5\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Severity != "QUESTION" {
		t.Fatalf("legacy severity = %+v, want QUESTION default", got)
	}
}

func TestLoadPriorSeverityWithLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	src := "# R\n\n## Inline comments\n\n### a.go:7 (LEFT) [NITPICK]\n\nbody\n\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Side != "old" || got[0].Severity != "NITPICK" {
		t.Fatalf("got %+v, want side=old severity=NITPICK", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestRenderMarkdownSeverity|TestLoadPriorSeverity' -v`
Expected: FAIL — `Comment.Severity` undefined (compile error).

- [ ] **Step 3: Add the `Severity` field**

In the `Comment` struct (~line 216), after `Body`:

```go
	Severity string `json:"severity,omitempty"`
```

- [ ] **Step 4: Emit the token in `renderMarkdown`**

Find the LEFT block (~line 925) and append the severity token right after it:

```go
			if pr != nil && c.Side == "old" {
				loc += " (LEFT)"
			}
			sev := c.Severity
			if sev == "" {
				sev = "QUESTION"
			}
			loc += " [" + sev + "]"
```

(The `fmt.Fprintf(&b, "### %s\n\n", loc)` line below is unchanged.)

- [ ] **Step 5: Extend the regex**

Replace `inlineHeaderRe` (~line 947):

```go
var inlineHeaderRe = regexp.MustCompile(`^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?(?:\s+\[([A-Z]+)\])?\s*$`)
```

- [ ] **Step 6: Parse severity in `loadPrior`**

In the inline-header branch (~line 1122), set severity from group 5 with a default:

```go
				side := "new"
				if m[4] == "LEFT" {
					side = "old"
				}
				sev := m[5]
				if sev == "" {
					sev = "QUESTION"
				}
				cur = &Comment{Path: m[1], Side: side, Line: start, EndLine: end, Severity: sev}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./... -v && go build ./... && go vet ./...`
Expected: all tests PASS (new + existing); clean build/vet.

- [ ] **Step 8: Commit**

```bash
git add main.go main_test.go
git commit -m "Add comment severity: [SEVERITY] heading token with round-trip"
```

---

## Task 2: Gate severity behind the `-severity` flag

Task 1 always-emits the token. This task adds the `-severity` flag (flag/env/JSON), threads it into `renderMarkdown` so the token is emitted ONLY when the flag is on, passes a `Severity` bool to the template, and adjusts the tests so the default (flag off) produces today's output.

**Files:**
- Modify: `main.go` — `Config` struct, `loadConfig` env, `mergeConfigFile`, flag block (~line 289), `renderMarkdown` signature + emit (~line 895), the three `renderMarkdown` callers (~line 501/523/542), template map (~line 437)
- Test: `main_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `cfg.Severity bool`; `-severity` flag bound to `severity *bool`; `GUTTER_SEVERITY` env; `severity` JSON field; `renderMarkdown(rev, vcs, docPath string, severity bool, pr *PRInfo, req SaveRequest) string` (new `severity` param, 4th position); template key `"Severity"`.

- [ ] **Step 1: Update the failing tests for the flag**

The Task-1 severity tests call `renderMarkdown` with the old arity and assume always-emit. Update them to the new signature (pass `true` for the token cases) and add an off-case. Replace the bodies of the Task-1 severity tests and add one:

```go
func TestRenderMarkdownSeverity(t *testing.T) {
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Body: "x"},
		{Path: "b.go", Side: "new", Line: 8, Body: "y"}, // empty -> QUESTION
		{Path: "c.go", Side: "new", Line: 10, EndLine: 12, Severity: "SUGGESTION", Body: "z"},
	}}
	md := renderMarkdown("", "git", "", true, nil, req)
	for _, want := range []string{
		"### a.go:5 [IMPORTANT]\n",
		"### b.go:8 [QUESTION]\n",
		"### c.go:10-12 [SUGGESTION]\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n---\n%s", want, md)
		}
	}
}

func TestRenderMarkdownSeverityAfterLeft(t *testing.T) {
	pr := &PRInfo{Repo: "o/r", Number: 1, Head: "h", Base: "b"}
	req := SaveRequest{Comments: []Comment{{Path: "c.go", Side: "old", Line: 7, Severity: "NITPICK", Body: "z"}}}
	md := renderMarkdown("", "git", "", true, pr, req)
	if !strings.Contains(md, "### c.go:7 (LEFT) [NITPICK]\n") {
		t.Errorf("severity should follow (LEFT):\n%s", md)
	}
}

func TestRenderMarkdownSeverityOff(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 5, Severity: "IMPORTANT", Body: "x"}}}
	md := renderMarkdown("", "git", "", false, nil, req)
	if !strings.Contains(md, "### a.go:5\n") {
		t.Errorf("flag off must emit a bare heading:\n%s", md)
	}
	if strings.Contains(md, "[") {
		t.Errorf("flag off must not emit any [SEVERITY] token:\n%s", md)
	}
}
```

Update the two round-trip tests to write via the flag-on renderer (`renderMarkdown("", "git", "", true, nil, req)`) — `TestLoadPriorSeverityRoundTrip` is the one that calls `renderMarkdown`; leave the raw-string tests (`TestLoadPriorSeverityLegacyDefault`, `TestLoadPriorSeverityWithLeft`) as-is (they write literal markdown, not via renderMarkdown).

Also revert the two pre-existing tests that Task 1 edited (`TestRenderMarkdownPRBlock`, `TestRenderMarkdownLocalUnchanged`): they must now call `renderMarkdown` with `false` in the new 4th position and their expected heading substrings must NOT contain `[QUESTION]` (back to today's bare `### path:line`). Grep for every `renderMarkdown(` call in `main_test.go` and give each the new arity — token-feature tests pass `true`, all others pass `false`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestRenderMarkdownSeverity|TestLoadPriorSeverity' -v`
Expected: FAIL — not enough arguments to `renderMarkdown` (compile error).

- [ ] **Step 3: Add the config field**

In the `Config` struct, after `MD`:

```go
	Severity bool   `json:"severity,omitempty"`
```

- [ ] **Step 4: Add the env override**

In `loadConfig`, after the `GUTTER_MD` block:

```go
	if v := os.Getenv("GUTTER_SEVERITY"); v != "" {
		c.Severity = v != "0" && v != "false" && v != "no"
	}
```

- [ ] **Step 5: Add the JSON merge (OR, like Sync)**

In `mergeConfigFile`, after the `MD` merge:

```go
	if f.Severity {
		c.Severity = true
	}
```

- [ ] **Step 6: Add the flag**

In `main`'s flag block, after `md` (~line 290):

```go
		severity  = flag.Bool("severity", cfg.Severity, "show a severity dropdown on comments and emit a [SEVERITY] token on inline headings")
```

- [ ] **Step 7: Gate emission in `renderMarkdown`**

Change the signature (add `severity bool` in the 4th position):

```go
func renderMarkdown(rev, vcs, docPath string, severity bool, pr *PRInfo, req SaveRequest) string {
```

Wrap the token emission (added in Task 1) in the flag check:

```go
			if pr != nil && c.Side == "old" {
				loc += " (LEFT)"
			}
			if severity {
				sev := c.Severity
				if sev == "" {
					sev = "QUESTION"
				}
				loc += " [" + sev + "]"
			}
```

- [ ] **Step 8: Update the three callers + template**

Change each of the three handler calls (~line 501, 523, 542) from `renderMarkdown(*rev, vcs, docPath, prInfo, req)` to `renderMarkdown(*rev, vcs, docPath, *severity, prInfo, req)`.

In the `/` template map (~line 437, alongside `"Sync"`/`"Doc"`), add:

```go
			"Severity":  *severity,
```

- [ ] **Step 9: Run tests + build**

Run: `go test ./... -v && go build ./... && go vet ./...`
Expected: all tests PASS (flag-on token cases, flag-off no-token case, round-trip, and the reverted pre-existing tests); clean build/vet.

- [ ] **Step 10: Commit**

```bash
git add main.go main_test.go
git commit -m "Gate severity behind -severity flag (flag/env/JSON); emit only when on"
```

---

## Task 3: UI — severity select, gated on `-severity`

Render the severity select and the saved-comment tag ONLY when the server passes `Severity: true`.

**Files:**
- Modify: `index.html` — edit-modal markup (~line 184-194), a CSS rule, the script const block (~line 201-203), `load()` prior-seed (~line 213), `openCommentForm`, `openDocCommentForm`, `openEditModal`/`editSave`, and the three saved-comment `.loc` lines

**Interfaces:**
- Consumes: template `{{.Severity}}` bool (Task 2); `Comment.severity` from `/diff`'s `prior`.
- Produces: severity captured into `COMMENTS[*].severity` only in severity mode.

- [ ] **Step 1: Add the CSS**

Add near the `.comment-form` rules:

```css
  select.severity { display: block; background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 4px; padding: 4px 6px; font: inherit; margin-bottom: 6px; }
  .saved-comment .loc .sev { color: var(--accent); font-weight: 600; }
```

- [ ] **Step 2: Add the select to the edit modal markup**

Between the loc and textarea (~line 187-188):

```html
    <div class="loc" id="editLoc"></div>
    <select class="severity" id="editSeverity"></select>
    <textarea id="editBody"></textarea>
```

- [ ] **Step 3: Add the `SEVERITY_MODE` const + helpers**

After the `const SYNC = …;` line (~line 203):

```javascript
const SEVERITY_MODE = {{if .Severity}}true{{else}}false{{end}};
const SEVERITIES = ['BLOCKING', 'IMPORTANT', 'SUGGESTION', 'QUESTION', 'NITPICK'];
function severityOptionsHTML(selected) {
  const sel = SEVERITIES.includes(selected) ? selected : 'QUESTION';
  return SEVERITIES.map(s => `<option value="${s}"${s === sel ? ' selected' : ''}>${s}</option>`).join('');
}
function severitySelectHTML() {
  return SEVERITY_MODE ? `<select class="severity">${severityOptionsHTML('QUESTION')}</select>` : '';
}
```

- [ ] **Step 4: Seed severity from prior comments in `load()`**

In the prior-seed loop (~line 213), add `severity: c.severity || 'QUESTION',` to the pushed object (harmless when the feature is off).

- [ ] **Step 5: Diff add-form (`openCommentForm`)**

Insert `${severitySelectHTML()}` above the `<textarea>` in the form `innerHTML`. In the "Add comment" `COMMENTS.push`, add:

```javascript
      severity: SEVERITY_MODE ? tr.querySelector('select.severity').value : undefined,
```

- [ ] **Step 6: Doc add-form (`openDocCommentForm`)**

Insert `${severitySelectHTML()}` above the `<textarea>`. In its `COMMENTS.push`, add:

```javascript
      severity: SEVERITY_MODE ? form.querySelector('select.severity').value : undefined,
```

- [ ] **Step 7: Edit modal — populate + save (gated)**

In `openEditModal`, show/populate the select only in severity mode:

```javascript
  const editSev = document.getElementById('editSeverity');
  editSev.style.display = SEVERITY_MODE ? '' : 'none';
  if (SEVERITY_MODE) editSev.innerHTML = severityOptionsHTML(c.severity || 'QUESTION');
```

In `editSave`, on the non-empty branch, write severity back only in severity mode:

```javascript
  else {
    c.body = v;
    if (SEVERITY_MODE) c.severity = document.getElementById('editSeverity').value;
    c.prior = false;
  }
```

- [ ] **Step 8: Saved-comment display tag (gated), all three sites**

At each of the three identical `.loc` lines (`renderComments`, `renderDocComments`, `renderUnattached`), append a gated tag:

```javascript
          <div class="loc">💬 ${escapeHtml(c.path)}:${c.line}${range}${SEVERITY_MODE ? ` <span class="sev">${escapeHtml(c.severity || 'QUESTION')}</span>` : ''}</div>
```

- [ ] **Step 9: Verify both flag states**

Run:
```bash
go build -o /tmp/gutter . && printf '# Plan\n\nPara one.\n\n- a\n- b\n' > /tmp/sev.md && \
/tmp/gutter -severity -md /tmp/sev.md -open=false -port=9971 >/dev/null 2>&1 & sleep 2; \
echo "severity-on: SEVERITY_MODE true?"; curl -s http://127.0.0.1:9971/ | grep -c 'const SEVERITY_MODE = true'; \
echo "severity-on: modal select present?"; curl -s http://127.0.0.1:9971/ | grep -c 'id="editSeverity"'; \
kill %1 2>/dev/null; \
/tmp/gutter -md /tmp/sev.md -open=false -port=9972 >/dev/null 2>&1 & sleep 2; \
echo "severity-off: SEVERITY_MODE false?"; curl -s http://127.0.0.1:9972/ | grep -c 'const SEVERITY_MODE = false'; \
kill %1 2>/dev/null
```
Expected: severity-on shows `SEVERITY_MODE = true` (1) and the modal select (1); severity-off shows `SEVERITY_MODE = false` (1). Kill all servers.

- [ ] **Step 10: Commit**

```bash
git add index.html
git commit -m "UI: severity select + tag, gated on -severity"
```

---

## Task 4: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Build + fixture**

Run: `go build -o /tmp/gutter . && printf '# Plan\n\nParagraph one that spans\ninto a second source line.\n\n## Section\n\nAnother para.\n' > /tmp/sev.md`

- [ ] **Step 2: `-severity` on emits the token (single + range)**

```bash
GUTTER_OUTPUT=/tmp/none.md /tmp/gutter -sync -severity -md /tmp/sev.md -open=false -port=9973 >/tmp/sev.out 2>/dev/null &
GPID=$!; sleep 2
curl -s -X POST http://127.0.0.1:9973/submit -d '{"general":"","comments":[
  {"path":"/tmp/sev.md","side":"new","line":1,"end_line":1,"severity":"BLOCKING","body":"t"},
  {"path":"/tmp/sev.md","side":"new","line":3,"end_line":4,"severity":"SUGGESTION","body":"m"}]}'
echo; wait $GPID 2>/dev/null
grep -q '### /tmp/sev.md:1 \[BLOCKING\]' /tmp/sev.out && echo "single OK" || echo "single FAIL"
grep -q '### /tmp/sev.md:3-4 \[SUGGESTION\]' /tmp/sev.out && echo "range OK" || echo "range FAIL"
```
Expected: `single OK`, `range OK`. (If `wait` hangs from backgrounding, `kill -TERM $GPID`.)

- [ ] **Step 3: Plain run (no `-severity`) emits NO token**

```bash
GUTTER_OUTPUT=/tmp/none.md /tmp/gutter -sync -md /tmp/sev.md -open=false -port=9974 >/tmp/sev2.out 2>/dev/null &
GPID=$!; sleep 2
curl -s -X POST http://127.0.0.1:9974/submit -d '{"general":"","comments":[{"path":"/tmp/sev.md","side":"new","line":1,"end_line":1,"severity":"BLOCKING","body":"t"}]}' >/dev/null
wait $GPID 2>/dev/null
grep -q '### /tmp/sev.md:1$' /tmp/sev2.out && echo "no-token OK" || { echo "no-token FAIL"; grep '### /tmp/sev.md:1' /tmp/sev2.out; }
rm -f /tmp/none.md
```
Expected: `no-token OK` — the heading is bare `### /tmp/sev.md:1` even though the payload carried a severity, because the flag is off.

---

## Task 5: Documentation + `-md` screenshot

**Files:** Modify `README.md`; add `docs/screenshot-md.png`.

- [ ] **Step 1: Document `-severity`**

Add `-severity`, `GUTTER_SEVERITY`, and the `severity` JSON field to the reference tables, and a short note: with `-severity`, inline comments get a severity dropdown (`BLOCKING`/`IMPORTANT`/`SUGGESTION`/`QUESTION`/`NITPICK`, default `QUESTION`) emitted as a trailing `[SEVERITY]` token on the `### path:line` heading; without the flag, output is unchanged and absent tokens parse as `QUESTION`.

- [ ] **Step 2: Capture and embed the `-md` screenshot**

The controller (outer session) will capture a screenshot of the `-md` document view via a browser tool and save it to `docs/screenshot-md.png`; then reference it from the "Reviewing a markdown document" section of `README.md`, e.g. `![gutter -md document view](docs/screenshot-md.png)`. (If running this task standalone without a browser tool, add the `![…](docs/screenshot-md.png)` reference and leave a note that the PNG must be added; the controller supplies the image.)

- [ ] **Step 3: Commit**

```bash
git add README.md docs/screenshot-md.png
git commit -m "README: document -severity flag; add -md view screenshot"
```

---

## Self-Review Notes

- **Spec coverage:** severity field + token emit + round-trip → Task 1 (done); `-severity` flag (flag/env/JSON) + gated emission + template var + test adjustment → Task 2; flag-gated UI (select in both add-forms + edit modal, prior-seed, display tag) → Task 3; e2e both flag states → Task 4; docs + `-md` screenshot → Task 5. General-feedback severity omitted (spec non-goal).
- **Flag-off = today's output:** `TestRenderMarkdownSeverityOff` and Task 4 Step 3 both assert a bare `### path:line` with no token when `-severity` is absent; the two pre-existing render tests are reverted to no-token expectations.
- **Type/name consistency:** `Config.Severity`/`GUTTER_SEVERITY`/`severity` JSON; `renderMarkdown(rev, vcs, docPath string, severity bool, pr, req)`; template `Severity`; JS `SEVERITY_MODE`, `SEVERITIES`, `severityOptionsHTML`, `severitySelectHTML`, `select.severity`, `#editSeverity`.
- **Unchanged invariants:** range comments, snippet fence, `## General feedback`/`## Inline comments`, and the stdout/stderr split are untouched; the heading gains a trailing token only when `-severity` is on.
