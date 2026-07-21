# Design: `--sync` mode for gutter

Date: 2026-07-20

## Goal

Let an AI agent (Claude) run `gutter --sync`, have it block while the user
reviews the diff in the browser, and — when the user clicks a single **Submit**
button — print the review markdown to stdout and exit. The agent that launched
the command receives the review synchronously as command output.

## Non-goals

- No `review.md` file is written in sync mode. stdout is the sole output
  channel.
- No explicit Cancel button and no browser tab-close detection. The reliable
  decline signal is Ctrl-C (SIGINT). Adding heartbeat/beacon machinery to
  detect a closed tab is out of scope (YAGNI).
- Non-sync behavior is unchanged: Save writes `review.md`, Quit exits, and all
  logging stays on stdout.

## Behavior

`--sync` (bool) turns gutter into a blocking, one-shot review:

- The UI is served and the browser opens as normal.
- The process blocks until the user clicks **Submit**.
- On Submit: gutter renders the review markdown (via the existing
  `renderMarkdown`), prints it to **stdout**, and exits `0`.
- **No `review.md` is written.**
- All startup/informational logging (`gutter: <url>`, `output:`/`rev:`/`pr:`
  lines, notes) is redirected to **stderr** so that **stdout contains only the
  review markdown**.
- An empty review is allowed: Submit with no comments prints the existing
  `_(no feedback)_` placeholder and exits `0`.
- Ctrl-C (SIGINT) before Submit exits non-zero with nothing on stdout — this is
  how the agent distinguishes "user declined" from "user submitted an empty
  review".

### Configuration

`--sync` follows the existing precedence chain (CLI flag → env → project JSON →
user JSON → defaults), so it adds all three surfaces:

- Flag: `-sync` (bool), default `false`.
- Env: `GUTTER_SYNC` (truthy parse identical to `GUTTER_OPEN`: false when `"0"`,
  `"false"`, or `"no"`; otherwise true when the var is set non-empty).
- JSON: `sync bool` field (`json:"sync,omitempty"`).

Note the asymmetry with `Open`: `mergeConfigFile` special-cases `Open` because
its zero value (`false`) is meaningful. `Sync` defaults to `false`, so a missing
JSON key must NOT flip an already-true value. The merge therefore ORs the file
value in (`if f.Sync { c.Sync = true }`) rather than overwriting, matching how
non-boolean fields only apply when non-empty. (See "Config merge" below for the
exact rule.)

## Architecture

All changes are in `main.go` and `index.html`, consistent with the single-file
convention.

### Diff/serve flow (unchanged parts)

`computeData`, `renderMarkdown`, `parseDiff`, `/diff`, `/save`, `/quit`,
`/markdown`, and the PR source are untouched. `--sync` adds a parallel submit
path; it does not modify the existing save/quit logic.

### New submit path

- New channel in `main`: `submitCh := make(chan string, 1)`.
- New endpoint **`/submit`** (POST only), registered unconditionally (harmless
  when not in sync mode, but only the sync UI calls it):
  - Decodes the `SaveRequest` body (same shape `/save` uses).
  - Renders `md := renderMarkdown(*rev, vcs, prInfo, req)`.
  - Sends `md` on `submitCh` (non-blocking send into the buffered channel).
  - Responds `200` with a short body: `Review submitted — you can close this tab`.
- In `main`, after the listener is started, replace the current no-op
  `go func() { <-doneCh }()` drain with sync-aware handling:

  ```go
  if *sync {
      go func() {
          md := <-submitCh
          time.Sleep(150 * time.Millisecond) // let the HTTP response flush
          fmt.Print(md)
          os.Exit(0)
      }()
  } else {
      go func() { <-doneCh }() // unchanged: drain save signals
  }
  ```

  The `time.Sleep` mirrors the existing `/quit` handler's flush delay so the
  browser receives the confirmation response before the process exits.

### stdout/stderr routing in sync mode

The startup prints currently go to stdout via `fmt.Println`. In sync mode they
must go to stderr so stdout carries only the review markdown. Introduce a small
helper local to `main`:

```go
infoW := os.Stdout
if *sync {
    infoW = os.Stderr
}
```

and change the startup banner block (`gutter: <url>`, `output:`, `rev:`/`pr:`
lines) from `fmt.Println(...)` to `fmt.Fprintln(infoW, ...)`. The existing
stderr-only notes (e.g. the PR local-tree caveat, "loaded N prior comments")
already go to stderr and stay there.

### Config merge

- `Config` gains `Sync bool` (`json:"sync,omitempty"`).
- `defaultConfig()` leaves it `false` (zero value) — no change needed.
- `loadConfig` env block:
  ```go
  if v := os.Getenv("GUTTER_SYNC"); v != "" {
      c.Sync = v != "0" && v != "false" && v != "no"
  }
  ```
- `mergeConfigFile`: because a missing key must not clear a truthy value, OR it
  in rather than assign:
  ```go
  if f.Sync {
      c.Sync = true
  }
  ```
  Rationale: unlike `Open`, `Sync` has no "explicitly set to false in JSON"
  requirement in scope; the CLI flag and env var remain the ways to force it,
  and they run after `mergeConfigFile`. This keeps the merge simple and avoids
  the `Open` special-case dance.
- Flag: `sync = flag.Bool("sync", cfg.Sync, "one-shot review: block until Submit, print the review to stdout, then exit (no review.md written)")`.

## UI (index.html)

The template already receives a flags map (`Rev`, `VCS`, `Out`, `HasEditor`,
`Collapse`). Add `Sync bool`.

- Pass `"Sync": *sync` in the `/` handler's `tmpl.Execute` map.
- In the header, when `Sync` is true:
  - Hide/replace **Save review**, **Quit**, and **Copy** with a single
    **Submit** button.
  - Keep the sidebar toggle, theme toggle, and collapse controls — they don't
    affect output.
- The Submit button POSTs the same JSON body `save()` builds (general + comments)
  to `/submit` instead of `/save`.
- On a successful `/submit` response, replace the button area with the text
  "Review submitted — you can close this tab." (The server exits ~150ms later,
  so further requests will fail; that is expected.)

Implementation approach in the single-file template: gate the button markup with
`{{if .Sync}} … {{else}} … {{end}}` in the header, and branch the submit
function in JS on a `SYNC` boolean injected the same way `COLLAPSE`/`HAS_EDITOR`
are (via a template expression in the inline script). Reuse the existing
comment-collection logic from `save()`; only the endpoint and the
post-success UI differ.

## Error handling

- `/submit` with a malformed body → `400`, same as `/save`; the process keeps
  blocking (the user can retry). The channel is only sent to on a successful
  render.
- If `submitCh` already holds a value (double submit race), the non-blocking
  send is dropped; the first submit wins and the process is already exiting.
- SIGINT/SIGTERM: default Go behavior terminates the process with a non-zero
  status; nothing is printed to stdout. No special handler needed.

## Testing

Consistent with CLAUDE.md (no unit suite for the diff parser; prefer end-to-end
against the binary). Add:

- **Unit** (`main_test.go`): `sync` config precedence — env `GUTTER_SYNC`
  parsing (truthy/falsey values) and the `mergeConfigFile` OR behavior (a file
  with `sync:true` sets it; a file without the key does not clear an
  already-true value).
- **End-to-end** (manual, documented in the plan): run `gutter -sync
  -open=false` on a repo with a diff; POST a `SaveRequest` to `/submit`; assert
  (1) the process prints exactly the rendered markdown to stdout, (2) exits 0,
  (3) writes no `review.md`, and (4) the startup banner appeared on stderr, not
  stdout.

## Documentation

Add a short "Agent sync mode" note to `README.md`: `gutter -sync` blocks until
the user clicks Submit, prints the review to stdout, and exits without writing a
file — intended for an agent that runs gutter and waits for the result. Add the
`-sync` flag, `GUTTER_SYNC` env, and `sync` JSON field to the existing
reference tables.
