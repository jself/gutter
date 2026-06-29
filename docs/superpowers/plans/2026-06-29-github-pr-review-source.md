# GitHub PR Review Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `gutter -pr <number|url>` load a GitHub PR's diff into the existing review UI and emit a `review.md` that gives an AI agent everything it needs to understand the PR and post the human's comments back via `gh`.

**Architecture:** Add a config field + flag `-pr`. When set, the diff comes from `gh pr diff <arg>` instead of the local VCS, and PR metadata comes from `gh pr view <arg> --json ...` (fetched once at startup). `renderMarkdown` gains an optional `*PRInfo` so it can emit a `## PR` metadata block, a `PR #N` title, and a `(LEFT)` side suffix on old-side comment anchors. Parser, UI, and `loadPrior` are otherwise untouched.

**Tech Stack:** Go (single `main.go`, package `main`), `gh` CLI, `html/template`, standard library testing.

## Global Constraints

- **Go version floor: 1.16** (`go.mod`) — no language features newer than 1.16; embedding via `go:embed` is already in use.
- **Single file:** all Go code stays in `main.go` (project convention); tests go in a new `main_test.go`.
- **No new runtime dependencies** — only the standard library and the `gh` CLI (invoked via `os/exec`).
- **Markdown round-trip is load-bearing:** `renderMarkdown` (write) and `loadPrior` (read) must stay compatible. Existing `review.md` files (no `(LEFT)` suffix, no `## PR` block) must still parse unchanged.
- **Config precedence (highest wins):** CLI flag → env (`GUTTER_*`) → `./.gutter.json` → user config → `defaultConfig()`. A new flag MUST add the matching env var and JSON field.
- **No diff caching:** `/diff` recomputes every request. For PRs that means re-running `gh pr diff` per reload — keep it that way. PR *metadata* (`PRInfo`) is fetched once at startup and reused.
- **`gh` accepts a PR number or a full PR URL directly** as its positional argument — no URL parsing needed for invocation. The repo `owner/name` is derived from the `url` field returned by `gh pr view`.

---

## File Structure

- **Modify `main.go`** — the only production file:
  - `Config` struct, `defaultConfig`, `loadConfig` (env), `mergeConfigFile` (JSON): add `PR`.
  - `main`: add `-pr` flag; fetch `PRInfo` once when set; choose diff source in `computeData`; PR-aware header display; startup caveat.
  - New `PRInfo` type and helpers: `parsePRView` (pure), `githubPRDiff`, `githubPRInfo`.
  - `renderMarkdown`: new `*PRInfo` param → title variant, `## PR` block, `(LEFT)` suffix.
  - `inlineHeaderRe` + `loadPrior`: parse optional `(LEFT)` suffix into `Side`.
- **Create `main_test.go`** — unit tests for the pure functions (`parsePRView`, `renderMarkdown`, `loadPrior` round-trip). The thin `gh`-calling wrappers (`githubPRDiff`, `githubPRInfo`) are verified manually (Task 6), consistent with CLAUDE.md's "prefer end-to-end against the binary" stance.
- **Modify `README.md`** — document the `-pr` flag, `GUTTER_PR`, and the PR workflow.

---

## Task 1: PRInfo type and `parsePRView`

The pure core of the GitHub source: turn `gh pr view --json` output into a `PRInfo`. Pure and unit-testable with no network.

**Files:**
- Modify: `main.go` (add `PRInfo` type and `parsePRView` near the other types/helpers, e.g. just after the `Comment`/`SaveRequest` type block around line 189)
- Test: `main_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type PRInfo struct { Repo string; Number int; Head string; Base string }` — `Repo` is `"owner/name"`, `Head`/`Base` are commit SHAs.
  - `func parsePRView(b []byte) (PRInfo, error)` — unmarshals gh JSON (`number`, `headRefOid`, `baseRefOid`, `url`) and derives `Repo` from the `url`.

- [ ] **Step 1: Write the failing test**

Add to `main_test.go`:

```go
package main

import "testing"

func TestParsePRView(t *testing.T) {
	in := []byte(`{
		"number": 123,
		"headRefOid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"baseRefOid": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"url": "https://github.com/octo/widgets/pull/123"
	}`)
	got, err := parsePRView(in)
	if err != nil {
		t.Fatalf("parsePRView: %v", err)
	}
	want := PRInfo{
		Repo:   "octo/widgets",
		Number: 123,
		Head:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Base:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParsePRViewBadURL(t *testing.T) {
	in := []byte(`{"number":1,"headRefOid":"x","baseRefOid":"y","url":"not-a-url"}`)
	if _, err := parsePRView(in); err == nil {
		t.Fatal("expected error for unparseable url, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestParsePRView -v`
Expected: FAIL — `undefined: parsePRView` / `undefined: PRInfo`.

- [ ] **Step 3: Write minimal implementation**

Add to `main.go` (after the `SaveRequest` type, ~line 189):

```go
type PRInfo struct {
	Repo   string // "owner/name"
	Number int
	Head   string // head commit SHA
	Base   string // base commit SHA
}

// parsePRView turns `gh pr view <pr> --json number,headRefOid,baseRefOid,url`
// output into a PRInfo. Repo ("owner/name") is derived from the canonical url.
func parsePRView(b []byte) (PRInfo, error) {
	var v struct {
		Number     int    `json:"number"`
		HeadRefOid string `json:"headRefOid"`
		BaseRefOid string `json:"baseRefOid"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return PRInfo{}, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	repo, err := repoFromPRURL(v.URL)
	if err != nil {
		return PRInfo{}, err
	}
	return PRInfo{Repo: repo, Number: v.Number, Head: v.HeadRefOid, Base: v.BaseRefOid}, nil
}

// repoFromPRURL extracts "owner/name" from a PR url like
// https://github.com/owner/name/pull/123.
func repoFromPRURL(u string) (string, error) {
	i := strings.Index(u, "/pull/")
	if i < 0 {
		return "", fmt.Errorf("cannot parse repo from PR url %q", u)
	}
	rest := strings.TrimRight(u[:i], "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("cannot parse repo from PR url %q", u)
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1], nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestParsePRView -v`
Expected: PASS (both `TestParsePRView` and `TestParsePRViewBadURL`).

- [ ] **Step 5: Commit**

```bash
git add main.go main_test.go
git commit -m "Add PRInfo type and parsePRView for GitHub PR metadata"
```

---

## Task 2: `renderMarkdown` PR block, title, and `(LEFT)` suffix

Teach the writer to emit the `## PR` metadata block, the `PR #N` title, and a `(LEFT)` suffix on old-side comment anchors. This changes `renderMarkdown`'s signature, so its two callers are updated in the same task to keep the build green.

**Files:**
- Modify: `main.go` — `renderMarkdown` (~line 576), and its callers `/save` (~line 353) and `/markdown` (~line 376)
- Test: `main_test.go`

**Interfaces:**
- Consumes: `PRInfo` (Task 1).
- Produces: `func renderMarkdown(rev, vcs string, pr *PRInfo, req SaveRequest) string` — `pr` is `nil` for local reviews (unchanged output) and non-nil for PR reviews (adds title variant + `## PR` block; old-side comments get a ` (LEFT)` suffix).

- [ ] **Step 1: Write the failing test**

Add to `main_test.go`:

```go
import "strings" // add to the import block if not present

func TestRenderMarkdownPRBlock(t *testing.T) {
	pr := &PRInfo{Repo: "octo/widgets", Number: 123, Head: "abc123", Base: "def456"}
	req := SaveRequest{
		General: "Looks good overall.",
		Comments: []Comment{
			{Path: "a.go", Side: "new", Line: 10, Body: "new-side note"},
			{Path: "b.go", Side: "old", Line: 5, Body: "old-side note"},
		},
	}
	md := renderMarkdown("", "git", pr, req)

	for _, want := range []string{
		"# Review of PR #123 (github)",
		"## PR",
		"- repo: octo/widgets",
		"- number: 123",
		"- head: abc123",
		"- base: def456",
		"local working tree is NOT the PR's code",
		"gh pr diff 123",
		"### a.go:10\n",        // new side: no suffix
		"### b.go:5 (LEFT)\n",  // old side: suffix
	} {
		if !strings.Contains(md, want) {
			t.Errorf("rendered markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestRenderMarkdownLocalUnchanged(t *testing.T) {
	req := SaveRequest{Comments: []Comment{{Path: "a.go", Side: "new", Line: 10, Body: "x"}}}
	md := renderMarkdown("@", "jj", nil, req)
	if strings.Contains(md, "## PR") {
		t.Errorf("local review should not contain a PR block:\n%s", md)
	}
	if !strings.Contains(md, "# Review of `@` (jj)") {
		t.Errorf("local title changed:\n%s", md)
	}
	if !strings.Contains(md, "### a.go:10\n") {
		t.Errorf("new-side anchor should have no suffix:\n%s", md)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestRenderMarkdown -v`
Expected: FAIL — `too many arguments in call to renderMarkdown` (signature mismatch) / compile error.

- [ ] **Step 3: Write minimal implementation**

Replace the start of `renderMarkdown` (signature + title) and the comment-location line. Full updated function:

```go
func renderMarkdown(rev, vcs string, pr *PRInfo, req SaveRequest) string {
	var b strings.Builder
	if pr != nil {
		fmt.Fprintf(&b, "# Review of PR #%d (github)\n\n", pr.Number)
		fmt.Fprintf(&b, "## PR\n\n")
		fmt.Fprintf(&b, "- repo: %s\n", pr.Repo)
		fmt.Fprintf(&b, "- number: %d\n", pr.Number)
		fmt.Fprintf(&b, "- head: %s\n", pr.Head)
		fmt.Fprintf(&b, "- base: %s\n\n", pr.Base)
		fmt.Fprintf(&b, "NOTE: This is a GitHub PR review. The local working tree is NOT the PR's code —\n")
		fmt.Fprintf(&b, "do not read local files to understand the changes. Use `gh pr diff %d` (or\n", pr.Number)
		fmt.Fprintf(&b, "`gh pr view %d`) to see the actual changes these comments refer to.\n", pr.Number)
		fmt.Fprintf(&b, "To post a comment: `gh` review-comment API — use `head` as the commit id,\n")
		fmt.Fprintf(&b, "`path`/`line` from each comment, side RIGHT for added/context, LEFT for removed.\n\n")
	} else {
		fmt.Fprintf(&b, "# Review of `%s` (%s)\n\n", rev, vcs)
	}
	if strings.TrimSpace(req.General) != "" {
		b.WriteString("## General feedback\n\n")
		b.WriteString(strings.TrimSpace(req.General))
		b.WriteString("\n\n")
	}
	if len(req.Comments) > 0 {
		b.WriteString("## Inline comments\n\n")
		for _, c := range req.Comments {
			loc := fmt.Sprintf("%s:%d", c.Path, c.Line)
			if c.EndLine != 0 && c.EndLine != c.Line {
				loc = fmt.Sprintf("%s:%d-%d", c.Path, c.Line, c.EndLine)
			}
			if c.Side == "old" {
				loc += " (LEFT)"
			}
			fmt.Fprintf(&b, "### %s\n\n", loc)
			if strings.TrimSpace(c.Snippet) != "" {
				b.WriteString("```\n")
				b.WriteString(c.Snippet)
				if !strings.HasSuffix(c.Snippet, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("```\n\n")
			}
			b.WriteString(strings.TrimSpace(c.Body))
			b.WriteString("\n\n")
		}
	}
	if req.General == "" && len(req.Comments) == 0 {
		b.WriteString("_(no feedback)_\n")
	}
	return b.String()
}
```

Update the two callers to pass `nil` for now (PR threading comes in Task 4). In `/save` (~line 353):

```go
		md := renderMarkdown(*rev, vcs, nil, req)
```

In `/markdown` (~line 376):

```go
		w.Write([]byte(renderMarkdown(*rev, vcs, nil, req)))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestRenderMarkdown -v && go build ./...`
Expected: PASS and a clean build.

- [ ] **Step 5: Commit**

```bash
git add main.go main_test.go
git commit -m "renderMarkdown: emit PR metadata block and LEFT side suffix"
```

---

## Task 3: Parse the `(LEFT)` suffix in `loadPrior`

Close the round-trip: `loadPrior` must read the `(LEFT)` suffix back into `Comment.Side`, and existing files without it must still parse as the new side.

**Files:**
- Modify: `main.go` — `inlineHeaderRe` (~line 610) and the inline-header branch of `loadPrior` (~line 782-789)
- Test: `main_test.go`

**Interfaces:**
- Consumes: `renderMarkdown` output (Task 2).
- Produces: no signature changes. `inlineHeaderRe` gains a 4th capture group for `LEFT`; `loadPrior` sets `Side` to `"old"` when present, `"new"` otherwise.

- [ ] **Step 1: Write the failing test**

Add to `main_test.go` (uses `os` and `path/filepath` — add to imports):

```go
import (
	"os"
	"path/filepath"
)

func TestLoadPriorRoundTripSide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")

	pr := &PRInfo{Repo: "octo/widgets", Number: 7, Head: "h", Base: "b"}
	req := SaveRequest{Comments: []Comment{
		{Path: "a.go", Side: "new", Line: 10, Body: "new note"},
		{Path: "b.go", Side: "old", Line: 5, Body: "old note"},
	}}
	if err := os.WriteFile(path, []byte(renderMarkdown("", "git", pr, req)), 0644); err != nil {
		t.Fatal(err)
	}

	got, _ := loadPrior(path)
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2: %+v", len(got), got)
	}
	if got[0].Path != "a.go" || got[0].Line != 10 || got[0].Side != "new" {
		t.Errorf("comment 0 = %+v, want a.go:10 side new", got[0])
	}
	if got[1].Path != "b.go" || got[1].Line != 5 || got[1].Side != "old" {
		t.Errorf("comment 1 = %+v, want b.go:5 side old", got[1])
	}
}

func TestLoadPriorLegacyNoSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.md")
	legacy := "# Review of `@` (jj)\n\n## Inline comments\n\n### a.go:10-12\n\nbody here\n\n"
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := loadPrior(path)
	if len(got) != 1 || got[0].Side != "new" || got[0].Line != 10 || got[0].EndLine != 12 {
		t.Fatalf("legacy parse = %+v, want a.go:10-12 side new", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestLoadPrior -v`
Expected: FAIL — comment 1 parses with `Side: "new"` because the `(LEFT)` suffix isn't recognized (the regex doesn't match the line, so the comment is dropped or mis-parsed).

- [ ] **Step 3: Write minimal implementation**

Update the regex (~line 610):

```go
var inlineHeaderRe = regexp.MustCompile(`^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?\s*$`)
```

Update the inline-header branch in `loadPrior` (~line 782) — set `Side` from the new group 4:

```go
			if m := inlineHeaderRe.FindStringSubmatch(ln); m != nil {
				flushCur()
				start, _ := strconv.Atoi(m[2])
				end := start
				if m[3] != "" {
					end, _ = strconv.Atoi(m[3])
				}
				side := "new"
				if m[4] == "LEFT" {
					side = "old"
				}
				cur = &Comment{Path: m[1], Side: side, Line: start, EndLine: end}
				continue
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -v`
Expected: PASS (all tests, including Tasks 1–2).

- [ ] **Step 5: Commit**

```bash
git add main.go main_test.go
git commit -m "loadPrior: parse (LEFT) side suffix, keep legacy files compatible"
```

---

## Task 4: Config + flag plumbing for `-pr`

Wire `-pr` through the full config precedence chain (flag, env, JSON), matching the existing pattern for every other option.

**Files:**
- Modify: `main.go` — `Config` (~line 24), `loadConfig` env block (~line 53), `mergeConfigFile` (~line 93), flag declarations in `main` (~line 195)

**Interfaces:**
- Consumes: nothing.
- Produces: `cfg.PR string`; a `-pr` string flag; `GUTTER_PR` env; `pr` JSON field. (Consumed by Task 5.)

- [ ] **Step 1: Add the config field**

In the `Config` struct (~line 24), add:

```go
	PR       string `json:"pr,omitempty"`
```

- [ ] **Step 2: Add env override**

In `loadConfig`, alongside the other `GUTTER_*` blocks (~line 53):

```go
	if v := os.Getenv("GUTTER_PR"); v != "" {
		c.PR = v
	}
```

- [ ] **Step 3: Add JSON merge**

In `mergeConfigFile`, alongside the other field merges (~line 93):

```go
	if f.PR != "" {
		c.PR = f.PR
	}
```

- [ ] **Step 4: Add the flag**

In `main`'s flag block (~line 195), add:

```go
		prArg     = flag.String("pr", cfg.PR, "review a GitHub PR by number or URL (uses the gh CLI)")
```

- [ ] **Step 5: Verify it builds**

Run: `go build ./... && go vet ./...`
Expected: clean build, no vet errors. (`prArg` is unused until Task 5; if `go vet`/compile complains about an unused variable, proceed directly to Task 5 in the same working session — they form one buildable unit. To keep this task independently green, temporarily add `_ = prArg` at the end of the flag block and remove it in Task 5.)

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "Add -pr flag, GUTTER_PR env, and pr config field"
```

---

## Task 5: GitHub source + wire into main

Add the two thin `gh` wrappers, fetch `PRInfo` once at startup, select the diff source in `computeData`, make the header show `PR #N (github)`, thread `PRInfo` into `renderMarkdown`, and print the local-tree caveat.

**Files:**
- Modify: `main.go` — add `githubPRDiff`/`githubPRInfo` (near `getDiff`, ~line 452); `main` (`computeData` ~line 237, header template ~line 290, startup prints ~line 394, `/save` and `/markdown` handlers); `DiffData` (~line 169)

**Interfaces:**
- Consumes: `PRInfo`, `parsePRView` (Task 1); `renderMarkdown(rev, vcs, *PRInfo, req)` (Task 2); `cfg.PR`/`*prArg` (Task 4).
- Produces:
  - `func githubPRDiff(arg string) (string, error)` — runs `gh pr diff <arg>`, returns the unified diff.
  - `func githubPRInfo(arg string) (PRInfo, error)` — runs `gh pr view <arg> --json number,headRefOid,baseRefOid,url`, returns `parsePRView(out)`.
  - `DiffData` gains `PR *PRInfo` (`json:"pr,omitempty"`).

- [ ] **Step 1: Add the gh wrappers**

After `gitUntrackedDiff` (or near `getDiff`, ~line 452) in `main.go`:

```go
// githubPRDiff returns the unified git diff for a GitHub PR via the gh CLI.
// arg is a PR number or a full PR URL (gh accepts either).
func githubPRDiff(arg string) (string, error) {
	cmd := exec.Command("gh", "pr", "diff", arg)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr diff %s: %v: %s", arg, err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// githubPRInfo fetches PR metadata via the gh CLI.
func githubPRInfo(arg string) (PRInfo, error) {
	cmd := exec.Command("gh", "pr", "view", arg, "--json", "number,headRefOid,baseRefOid,url")
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %s: %v: %s", arg, err, strings.TrimSpace(errOut.String()))
	}
	return parsePRView(out.Bytes())
}
```

- [ ] **Step 2: Add the `PR` field to `DiffData`**

In the `DiffData` struct (~line 169):

```go
	PR       *PRInfo   `json:"pr,omitempty"`
```

- [ ] **Step 3: Fetch PRInfo once at startup**

In `main`, after `vcs, err := detectVCS()` and the `*rev` defaulting block (~line 223), add:

```go
	var prInfo *PRInfo
	if *prArg != "" {
		info, err := githubPRInfo(*prArg)
		if err != nil {
			die("%v", err)
		}
		prInfo = &info
	}
```

If you added the temporary `_ = prArg` in Task 4, remove it now.

- [ ] **Step 4: Select the diff source in `computeData`**

Replace the first two lines of the `computeData` closure (~line 238) — the `getDiff` call — with a source switch:

```go
	computeData := func() (DiffData, error) {
		var (
			diff           string
			untrackedPaths map[string]bool
		)
		if prInfo != nil {
			d, err := githubPRDiff(*prArg)
			if err != nil {
				return DiffData{}, fmt.Errorf("getting PR diff: %w", err)
			}
			diff = d
			untrackedPaths = map[string]bool{}
		} else {
			d, up, err := getDiff(vcs, *rev)
			if err != nil {
				return DiffData{}, fmt.Errorf("getting diff: %w", err)
			}
			diff, untrackedPaths = d, up
		}
		files, err := parseDiff(diff)
		if err != nil {
			return DiffData{}, fmt.Errorf("parsing diff: %w", err)
		}
```

Then, in the same closure, update the returned `DiffData` (~line 263) to include the PR pointer:

```go
		priorComments, priorGen := loadPrior(outAbs)
		return DiffData{Rev: *rev, VCS: vcs, Files: files, Prior: priorComments, PriorGen: priorGen, PR: prInfo}, nil
	}
```

- [ ] **Step 5: PR-aware header display**

In the `/` handler (~line 290), compute display values and use them in the template map:

```go
	displayHdrRev := *rev
	displayHdrVCS := vcs
	if prInfo != nil {
		displayHdrRev = fmt.Sprintf("PR #%d", prInfo.Number)
		displayHdrVCS = "github"
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, map[string]interface{}{
			"Rev":       displayHdrRev,
			"VCS":       displayHdrVCS,
			"Out":       outAbs,
			"HasEditor": *editorCmd != "",
			"Collapse":  *collapse,
		})
	})
```

- [ ] **Step 6: Thread PRInfo into the save/markdown handlers**

In `/save` (~line 353):

```go
		md := renderMarkdown(*rev, vcs, prInfo, req)
```

In `/markdown` (~line 376):

```go
		w.Write([]byte(renderMarkdown(*rev, vcs, prInfo, req)))
```

- [ ] **Step 7: Startup caveat + rev line**

Replace the startup `rev:` print block (~line 394) so PR mode shows the PR and the local-tree warning:

```go
	if prInfo != nil {
		fmt.Println("pr:       ", fmt.Sprintf("#%d", prInfo.Number), "("+prInfo.Repo+")")
		fmt.Fprintln(os.Stderr, "note: showing the PR diff; the local working tree is NOT the PR's code")
	} else {
		displayRev := *rev
		if displayRev == "" {
			displayRev = "(working tree)"
		}
		fmt.Println("rev:      ", displayRev, "("+vcs+")")
	}
```

- [ ] **Step 8: Build and run the full test suite**

Run: `go build ./... && go vet ./... && go test ./... -v`
Expected: clean build, no vet errors, all unit tests PASS.

- [ ] **Step 9: Commit**

```bash
git add main.go
git commit -m "Wire GitHub PR source: gh diff/metadata, PR header, render threading"
```

---

## Task 6: Manual end-to-end verification

No automated test covers the `gh` integration (network + auth). Verify against a real PR, per CLAUDE.md's end-to-end preference.

**Files:** none (verification only).

- [ ] **Step 1: Build the binary**

Run: `go build -o /tmp/gutter . && gh auth status`
Expected: binary builds; `gh` reports an authenticated account.

- [ ] **Step 2: Run against a real open PR (number form)**

From inside a clone with an open PR (substitute a real number):

Run: `GUTTER_OUTPUT=/tmp/pr-review.md /tmp/gutter -pr <N> -open=false`
Expected stdout: `gutter: http://127.0.0.1:...`, an `output:` line, and `pr: #<N> (owner/name)`; stderr shows the `note: showing the PR diff ...` caveat. Open the URL — the header reads `rev PR #<N> (github)` and the PR's changed files render.

- [ ] **Step 3: Add comments and save**

In the browser, add one comment on an added (green) line and one on a removed (red) line, then click **Save review**.

- [ ] **Step 4: Inspect the output**

Run: `cat /tmp/pr-review.md`
Expected: starts with `# Review of PR #<N> (github)`; a `## PR` block with correct `repo`/`number`/`head`/`base` and the `NOTE:`/`gh pr diff` lines; the added-line comment anchor has no suffix; the removed-line comment anchor ends with ` (LEFT)`.

- [ ] **Step 5: Verify the round-trip**

Re-run the same command (Step 2) so it loads the just-written file as prior comments.
Expected: stderr logs `loaded 2 prior comment(s) ...`; both comments reattach in the UI with the amber "prior" tag, including the removed-line (LEFT) one.

- [ ] **Step 6: Verify the URL form**

Run: `/tmp/gutter -pr https://github.com/owner/name/pull/<N> -open=false -port=0`
Expected: resolves the same PR (header shows `PR #<N>`), changed files render.

- [ ] **Step 7: Verify error handling**

Run: `/tmp/gutter -pr 999999999 -open=false`
Expected: exits non-zero with a `gh pr view ...` error surfacing gh's stderr (e.g. "no pull requests found"). Server does not start.

---

## Task 7: Documentation

Document the new flag and workflow so users discover it.

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the flag and workflow**

Add a "Reviewing a GitHub PR" subsection to `README.md` near the existing usage/flags docs. Cover:
- `gutter -pr <number|url>` — run from inside the repo clone; requires the `gh` CLI, authenticated.
- The diff is the PR's; the local working tree is not touched and may be on a different branch.
- `review.md` gains a `## PR` block with `repo`/`number`/`head`/`base` and instructions for the agent to load context with `gh pr diff <N>` and post comments back via `gh`.
- The `GUTTER_PR` env var and `pr` JSON config field (add to the existing config/env/flag reference table).

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "README: document -pr GitHub PR review flag and workflow"
```

---

## Self-Review Notes

- **Spec coverage:** diff-source seam → Tasks 4/5; GitHub `gh` commands → Task 5; `## PR` block + NOTE → Task 2; `(LEFT)` anchors → Tasks 2/3; round-trip preserved → Task 3; config precedence → Task 4; startup caveat → Task 5 step 7; error handling → Task 6 step 7; README/future-Bitbucket note → Task 7. The future Bitbucket source is intentionally out of scope (spec "non-goals"); the `prInfo != nil` switch in `computeData` is the seam where it would slot in.
- **Type consistency:** `PRInfo{Repo,Number,Head,Base}`, `renderMarkdown(rev, vcs string, pr *PRInfo, req SaveRequest)`, `githubPRDiff(arg string)`, `githubPRInfo(arg string)`, `parsePRView([]byte)`, and `DiffData.PR *PRInfo` are used identically across all tasks.
- **No placeholders:** every code step shows complete code; every test step shows the assertion; every run step states the expected result.
- **Round-trip safety:** the regex change is purely additive (an optional non-capturing group wrapping a `LEFT` capture); `TestLoadPriorLegacyNoSuffix` guards backward compatibility with existing `review.md` files.
