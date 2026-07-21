# Design: `-window` native app window (Go webview)

Date: 2026-07-21

## Goal

Add a `-window` flag that opens gutter's UI in a native OS window (via
`github.com/webview/webview_go`) instead of a browser tab. The **default**
`make build`/`install` includes window support (cgo + system WebKit), so
`-window` works out of the box; a separate `make build-portable` produces the
pure-Go, cgo-free, cross-compilable binary for distribution. A build tag keeps
the two cleanly separated and keeps `go test` pure-Go.

## Non-goals

- No in-UI "pop out" button; the window is requested only at launch via
  `-window`. "New window" = run the command again.
- No bundling/packaging (app icons, installers, code signing). This is a native
  window around the existing local server, not a distributable desktop app.
- No change to the default build's dependency story or the HTTP UI itself.

## Constraints / context

- gutter is today a single pure-Go binary (`go build -o gutter .`), no cgo, no
  system deps, trivially cross-compiled.
- `webview_go` requires cgo and a system webview: Linux → WebKit2GTK, macOS →
  Cocoa/WKWebView, Windows → WebView2. On this dev box: WebKit2GTK **4.1**
  (2.52.5) + GTK3 present, `CGO_ENABLED=1`, gcc available.
- `webview.Run()` must execute on the **main OS thread** and blocks until the
  window is closed.

## Cross-compilation impact

The default build now includes the window, so it does **not** cross-compile;
the portable target is the cross-compilable one. This tradeoff was chosen
deliberately (convenience-first default, explicit portable path).

- **Default `make build`/`install`: native, window-enabled.** Adds
  `-tags webview` + cgo, so it must be built on (or with a full cross C
  toolchain for) the target OS. Requires a system WebKit locally.
- **`make build-portable`: pure Go, cross-compilable.** `CGO_ENABLED=0`, no
  tags → the stub is compiled, no WebKit, and `GOOS=windows/darwin/linux
  go build` all work exactly as gutter does today. This is the target for
  releases/distribution and for any machine without WebKit.
- **`go build` / `go test` with no tags: pure Go.** Because window support is
  behind `//go:build webview`, a bare `go build`/`go test ./...` compiles the
  stub — so the test suite and CI need no cgo or WebKit. Only the `make build`
  target opts into the tag.

`webview_go` is cross-*platform* (Linux→WebKit2GTK, macOS→WKWebView,
Windows→WebView2); the window feature works on all three, each built natively.

## Build isolation

Two files with mutually exclusive build tags provide one function:

```go
// openWindow opens the given URL in a native window titled `title`, blocking
// until the window is closed. Returns an error if window support is unavailable.
func openWindow(url, title string) error
```

- `window_webview.go` — `//go:build webview`. Imports `webview_go`; creates a
  webview, sets the title and a reasonable default size, `Navigate(url)`,
  `Run()` (blocks), then `Destroy()`; returns nil after the window closes.
- `window_stub.go` — `//go:build !webview`. Returns
  `errors.New("this gutter was built without window support; rebuild the default target (make build) with cgo + a system webview")`.

`go.mod` gains `github.com/webview/webview_go`, linked only under the tag (a
bare `go build`/`go test` compiles the stub, so the module resolves but doesn't
require cgo/WebKit unless the tag is set).

### Makefile (default flips to window-enabled)

- `build` / `install` — now `CGO_ENABLED=1 go build -tags 'webview webkit2_41'`.
  The `webkit2_41` sub-tag selects WebKit2GTK 4.1 (what this box has); a comment
  notes that WebKit2GTK-4.0 systems drop it and macOS/Windows need only
  `-tags webview`.
- `build-portable` / `install-portable` — `CGO_ENABLED=0 go build` (no tags):
  the pure-Go, cross-compilable binary (today's default behavior), for releases
  and WebKit-less machines.
- `run` continues to use `build`.

## Runtime wiring

- New config: flag `-window` (bool, default false), env `GUTTER_WINDOW`
  (truthy like `GUTTER_SYNC`), JSON `window` (OR-merged like `sync`). Standard
  precedence.
- In `main`, today the server ends with `srv.Serve(ln)` on the main goroutine.
  When `*window` is true:
  1. Run `srv.Serve(ln)` in a goroutine.
  2. Call `openWindow(url, "gutter")` on the main thread (blocks until closed).
  3. On return (window closed), `os.Exit(0)`.
  If `openWindow` returns the stub's "not built" error, log it to stderr and
  fall back to the normal path (serve + honor `-open`) rather than exiting —
  so a pure-Go binary run with `-window` still works, just in the browser.
- `-window` implies `-open=false` (don't also spawn a browser). The startup
  banner (already stderr-routed) notes `window: on`.
- Composition: `-window` only changes how the UI is surfaced, not what the
  server serves, so it composes with `-md`, `-severity`, and `-sync`. With
  `-sync`, clicking Submit prints the review to stdout and exits, which also
  closes the window — expected.

## Error handling

- Window support present but `Run()`/webview creation fails (no display,
  headless): the webview call returns/panics at the C layer. Guard with a clear
  stderr message and exit non-zero; do not silently hang. (Manual-verify path;
  the common case is a desktop session where it just works.)
- Stub binary + `-window`: warn + fall back to browser/serve (above).

## Testing

- **Unit** (`main_test.go`): `window` config precedence (flag/env/JSON OR-merge),
  mirroring the `sync` tests. `go test ./...` runs with no build tag, so it
  compiles `window_stub.go`; a test asserts the stub `openWindow` returns a
  non-nil error containing "without window support". (This keeps the suite
  pure-Go — no cgo/WebKit needed to run tests.)
- **Manual** (cgo/GUI, not CI): `make build && ./gutter -window` on a repo with a
  diff opens a native window showing the UI; closing it exits 0.
  `./gutter -window -md <file>` opens the doc view in a window. A
  `make build-portable` binary + `./gutter -window` prints the fallback warning
  and opens the browser instead.

## Documentation

README gets a thorough **Building** / "Native window" section (the user
specifically asked for full build/tag docs), covering:

- `gutter -window` opens the UI in a desktop window instead of a browser.
- **Default build** (`make build` / `make install`) includes window support and
  therefore needs **cgo + a system webview**:
  - Linux: WebKit2GTK — 4.1 (`-tags 'webview webkit2_41'`, the Makefile default)
    or 4.0 (`-tags webview`); package e.g. `webkit2gtk-4.1` + `pkg-config`.
  - macOS: WebKit (system framework), just `-tags webview`.
  - Windows: WebView2 runtime, `-tags webview`.
  - Show the raw command too: `CGO_ENABLED=1 go build -tags 'webview webkit2_41' -o gutter .`
- **Portable build** (`make build-portable` / `install-portable`): pure Go,
  `CGO_ENABLED=0`, no WebKit, cross-compiles (`GOOS=… GOARCH=… make build-portable`);
  `-window` on this binary warns and falls back to the browser. This is the
  build for releases and WebKit-less environments.
- Note that `go test ./...` needs neither cgo nor WebKit (compiles the stub).

Add `-window` / `GUTTER_WINDOW` / `window` to the flags/env/config reference
tables.
