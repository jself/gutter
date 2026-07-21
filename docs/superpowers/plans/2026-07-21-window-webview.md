# Native Window (`-window`, Go webview) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gutter -window` to open the UI in a native OS window via `github.com/webview/webview_go`, with the default `make build` window-enabled (cgo + WebKit) and a `make build-portable` pure-Go target for cross-compilation.

**Architecture:** A single `openWindow(url, title string) error` function has two build-tagged implementations — `//go:build webview` (real webview) and `//go:build !webview` (stub returning an error). In `main`, `-window` runs the HTTP server in a goroutine and calls `openWindow` on the main thread (webview requires the main OS thread and blocks until the window closes). A bare `go build`/`go test` compiles the stub, so tests need no cgo/WebKit; `make build` adds `-tags webview`.

**Tech Stack:** Go (single `main.go` + two small build-tagged files), `webview_go` (cgo, system WebKit), embedded `index.html`, standard-library testing.

## Global Constraints

- Production Go in `main.go` plus the two new `window_*.go` files; tests in `main_test.go`; UI in `index.html`. Go floor 1.22.
- **Default build flips to window-enabled**, but the webview code stays behind `//go:build webview` so a bare `go build`/`go test ./...` (no tags) compiles the stub and needs no cgo/WebKit.
- `webview_go` module: `github.com/webview/webview_go` pinned at `v0.0.0-20240831120633-6173450d4dd6`. Only linked under the `webview` tag.
- Config precedence: CLI flag → env (`GUTTER_*`) → `./.gutter.json` → user config → defaults; a new flag adds the matching env var and JSON field. `window` JSON is OR-merged like `sync`.
- `-window` composes with `-md`/`-severity`/`-sync` (it only changes how the UI is surfaced). `-window` suppresses the auto-browser-open (`-open`); on the stub fallback it warns and opens the browser instead.
- On Linux the Makefile uses `-tags 'webview webkit2_41'` (this box has WebKit2GTK 4.1); a comment notes 4.0 systems drop `webkit2_41` and macOS/Windows need only `-tags webview`.

---

## File Structure

- **Create `window_stub.go`** (`//go:build !webview`) — `openWindow` returns a "built without window support" error.
- **Create `window_webview.go`** (`//go:build webview`) — `openWindow` via `webview_go`, on a locked OS thread.
- **Modify `main.go`** — `Config.Window`; `loadConfig` env; `mergeConfigFile`; `-window` flag; banner line; server-in-goroutine + `openWindow` main-thread wiring + fallback; suppress auto-open under `-window`.
- **Modify `go.mod`/`go.sum`** — add `webview_go`.
- **Modify `Makefile`** — default `build`/`install` gain `-tags 'webview webkit2_41'` + cgo; add `build-portable`/`install-portable`.
- **Modify `main_test.go`** — `window` config precedence + stub `openWindow` error.
- **Modify `README.md`** — Building / Native window docs.

---

## Task 1: `window` config plumbing

**Files:**
- Modify: `main.go` — `Config` (~line 37), `loadConfig` env (~line 89), `mergeConfigFile` (~line 135), flag block (~line 296)
- Test: `main_test.go`

**Interfaces:**
- Produces: `cfg.Window bool`; `-window` flag bound to `window *bool`; `GUTTER_WINDOW` env; `window` JSON field.

- [ ] **Step 1: Write the failing tests**

Add to `main_test.go`:

```go
func TestLoadConfigWindowEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GUTTER_WINDOW", "1")
	if got := loadConfig(); !got.Window {
		t.Errorf("GUTTER_WINDOW=1 should set Window")
	}
	t.Setenv("GUTTER_WINDOW", "false")
	if got := loadConfig(); got.Window {
		t.Errorf("GUTTER_WINDOW=false should be false")
	}
}

func TestMergeConfigFileWindowOR(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(`{"window":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	c := Config{Window: false}
	mergeConfigFile(&c, p)
	if !c.Window {
		t.Errorf("window:true in file should set Window")
	}
	p2 := filepath.Join(dir, "c2.json")
	if err := os.WriteFile(p2, []byte(`{"port":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	c2 := Config{Window: true}
	mergeConfigFile(&c2, p2)
	if !c2.Window {
		t.Errorf("missing window key must not clear Window")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestLoadConfigWindowEnv|TestMergeConfigFileWindowOR' -v`
Expected: FAIL — `Config.Window` undefined.

- [ ] **Step 3: Add the config field**

In the `Config` struct (after `Sync`, ~line 37):

```go
	Window   bool   `json:"window,omitempty"`
```

- [ ] **Step 4: Add the env override**

In `loadConfig`, after the `GUTTER_SYNC` block (~line 89):

```go
	if v := os.Getenv("GUTTER_WINDOW"); v != "" {
		c.Window = v != "0" && v != "false" && v != "no"
	}
```

- [ ] **Step 5: Add the JSON merge**

In `mergeConfigFile`, after the `Sync` merge (~line 135):

```go
	if f.Window {
		c.Window = true
	}
```

- [ ] **Step 6: Add the flag**

In `main`'s flag block, after `sync`/`md` (~line 297):

```go
		window    = flag.Bool("window", cfg.Window, "open the UI in a native desktop window (requires a window-enabled build; see README)")
```

Add `_ = window` right after `flag.Parse()` to keep the build green; Task 3 removes it.

- [ ] **Step 7: Run tests + build**

Run: `go test ./... -run 'TestLoadConfigWindowEnv|TestMergeConfigFileWindowOR' -v && go build ./... && go vet ./...`
Expected: PASS; clean build/vet.

- [ ] **Step 8: Commit**

```bash
git add main.go main_test.go
git commit -m "Add -window flag, GUTTER_WINDOW env, and window config field"
```

---

## Task 2: `openWindow` — stub + webview impl, build tags, dependency, Makefile

**Files:**
- Create: `window_stub.go`, `window_webview.go`
- Modify: `go.mod`, `go.sum`, `Makefile`
- Test: `main_test.go`

**Interfaces:**
- Produces: `func openWindow(url, title string) error` (two build-tagged impls). Stub returns a non-nil error containing "without window support"; webview impl opens a blocking native window.

- [ ] **Step 1: Write the failing test (stub behavior — default no-tag build)**

Add to `main_test.go`:

```go
func TestOpenWindowStub(t *testing.T) {
	// The default (no-tag) test build compiles window_stub.go.
	err := openWindow("http://127.0.0.1:0", "gutter")
	if err == nil {
		t.Fatal("stub openWindow should return an error")
	}
	if !strings.Contains(err.Error(), "without window support") {
		t.Errorf("unexpected stub error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestOpenWindowStub -v`
Expected: FAIL — `undefined: openWindow`.

- [ ] **Step 3: Create the stub**

`window_stub.go`:

```go
//go:build !webview

package main

import "errors"

// openWindow is the fallback when gutter is built without window support.
func openWindow(url, title string) error {
	return errors.New("this gutter was built without window support; rebuild the default target (make build) with cgo + a system webview")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestOpenWindowStub -v && go build ./... && go vet ./...`
Expected: PASS; clean build/vet (pure Go, stub compiled).

- [ ] **Step 5: Add the webview dependency**

Run:
```bash
GOFLAGS=-tags=webview go get github.com/webview/webview_go@v0.0.0-20240831120633-6173450d4dd6
```
Expected: `go.mod` now requires `github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6`; `go.sum` updated. Do NOT run bare `go mod tidy` (it would drop the tag-only dependency); if you must tidy, use `GOFLAGS=-tags=webview go mod tidy`.

- [ ] **Step 6: Create the webview implementation**

`window_webview.go`:

```go
//go:build webview

package main

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

// openWindow opens url in a native window titled `title`, blocking until the
// window is closed. Must run on the main OS thread.
func openWindow(url, title string) error {
	runtime.LockOSThread()
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(title)
	w.SetSize(1200, 800, webview.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
```

- [ ] **Step 7: Flip the Makefile default + add portable targets**

Replace the `build` and `install` targets and add portable ones (keep `PREFIX`/`BIN`/`clean`/`run`):

```makefile
# Default build includes the native window (needs cgo + a system webview).
# Linux: WebKit2GTK 4.1 via -tags 'webview webkit2_41' (drop webkit2_41 on 4.0
# systems; macOS/Windows need only -tags webview).
build:
	CGO_ENABLED=1 go build -tags 'webview webkit2_41' -o gutter .

install:
	CGO_ENABLED=1 go build -tags 'webview webkit2_41' -o $(BIN) .
	@echo "installed $(BIN)"

# Portable build: pure Go, no cgo/WebKit, cross-compilable. -window falls back
# to the browser on this binary.
build-portable:
	CGO_ENABLED=0 go build -o gutter .

install-portable:
	CGO_ENABLED=0 go build -o $(BIN) .
	@echo "installed $(BIN) (portable, no window support)"
```

Also update `.PHONY`:

```makefile
.PHONY: build install build-portable install-portable clean run
```

- [ ] **Step 8: Verify both builds compile**

Run:
```bash
go test ./... -run TestOpenWindowStub -v && \
make build && ./gutter -h 2>&1 | grep -q -- '-window' && echo "window build OK" && \
make build-portable && echo "portable build OK" && \
make build   # leave the default (window) binary in place
```
Expected: stub test passes; `make build` compiles with cgo+WebKit and the binary shows `-window`; `make build-portable` compiles pure-Go. (`make build` requires WebKit2GTK 4.1 + gcc — present on this box.)

- [ ] **Step 9: Commit**

```bash
git add window_stub.go window_webview.go go.mod go.sum Makefile main_test.go
git commit -m "Add openWindow (webview + stub), webview dep, window-enabled default build"
```

---

## Task 3: Wire `-window` into `main`

**Files:**
- Modify: `main.go` — remove `_ = window`; the auto-open guard (~line 596); banner (~line 592); the serve tail (~line 600-616)

**Interfaces:**
- Consumes: `*window` (Task 1); `openWindow(url, title string) error` (Task 2).

- [ ] **Step 1: Remove the placeholder**

Delete the `_ = window` line from Task 1.

- [ ] **Step 2: Add the banner line**

After the `if *sync { … }` banner block (~line 594), add:

```go
	if *window {
		fmt.Fprintln(infoW, "window:    on")
	}
```

- [ ] **Step 3: Suppress the auto-browser-open under `-window`**

Change the auto-open guard (~line 596) so a window run doesn't also pop a browser:

```go
	if *open && !*window {
		go openBrowser(url)
	}
```

- [ ] **Step 4: Restructure the serve tail for the window main-thread requirement**

Replace the serve tail (the `srv := &http.Server{…}` block through the final `}`, ~line 613-616):

```go
	srv := &http.Server{Handler: mux}
	serve := func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			die("serve: %v", err)
		}
	}

	if *window {
		go serve() // HTTP server runs in the background; the window owns the main thread
		if err := openWindow(url, "gutter"); err != nil {
			// No window support (portable build): warn and behave like a normal
			// server run — open the browser and keep serving.
			fmt.Fprintln(os.Stderr, "gutter: window unavailable:", err)
			go openBrowser(url)
			select {} // block forever; the server goroutine keeps handling requests
		}
		os.Exit(0) // window closed
	}

	serve() // non-window: blocks on the main goroutine as before
```

- [ ] **Step 5: Build both configurations + full suite**

Run:
```bash
go build ./... && go vet ./... && go test ./... && \
make build && echo "window build OK" && make build-portable && echo "portable OK" && make build
```
Expected: clean build/vet; all unit tests pass (stub path); both `make` targets compile.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "Wire -window: server in goroutine, openWindow on main thread, fallback"
```

---

## Task 4: Manual verification

**Files:** none (verification only).

- [ ] **Step 1: Portable fallback path (no display needed)**

```bash
make build-portable
printf '# Plan\n\ntext\n' > /tmp/w.md
GUTTER_OUTPUT=/tmp/none.md ./gutter -window -md /tmp/w.md -open=false -port=9990 >/tmp/w.out 2>/tmp/w.err &
GPID=$!; sleep 2
echo "warned + still serving (want 200):"; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9990/
grep -q 'window unavailable' /tmp/w.err && echo "fallback warning OK" || echo "fallback warning MISSING"
kill $GPID 2>/dev/null; rm -f /tmp/none.md
```
Expected: HTTP 200 (server still serving) and `fallback warning OK` — the portable binary with `-window` warns and falls back.

- [ ] **Step 2: Window build compiles + launches (GUI; controller/user with a display)**

```bash
make build
./gutter -window -md /tmp/w.md -port 9991
```
Expected: a native desktop window opens showing the rendered `/tmp/w.md` document view. Closing the window exits the process (0). If run over SSH/headless with no `$DISPLAY`/Wayland, the webview will fail to create a window — that's environmental, not a code defect; note it and rely on Step 1 + the build check.

- [ ] **Step 3: Restore the default (window) install**

```bash
make install && gutter -h 2>&1 | grep -- '-window'
```
Expected: `-window` present on the installed binary.

---

## Task 5: Documentation

**Files:** Modify `README.md`.

- [ ] **Step 1: Add Building / Native window docs**

Add a "Native window" subsection and a "Building" note covering:
- `gutter -window` opens the UI in a desktop window instead of a browser.
- **Default `make build` / `make install`** include window support and require **cgo + a system webview**: Linux WebKit2GTK 4.1 (`-tags 'webview webkit2_41'`, the Makefile default; 4.0 systems use `-tags webview`), macOS WebKit (`-tags webview`), Windows WebView2 (`-tags webview`). Show the raw command: `CGO_ENABLED=1 go build -tags 'webview webkit2_41' -o gutter .`.
- **`make build-portable` / `install-portable`**: pure Go, `CGO_ENABLED=0`, no WebKit, cross-compilable (`GOOS=… GOARCH=… make build-portable`); `-window` on this binary warns and falls back to the browser. Use this for releases and WebKit-less machines.
- `go test ./...` needs neither cgo nor WebKit (compiles the stub).
- Add `-window` / `GUTTER_WINDOW` / `window` to the flags/env/config reference tables.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "README: document -window native window and window/portable builds"
```

---

## Self-Review Notes

- **Spec coverage:** config plumbing → Task 1; build-tagged `openWindow` + dep + Makefile flip + portable target → Task 2; main-thread serve/window wiring + fallback + `-open` suppression + banner → Task 3; manual verify (fallback + GUI + install) → Task 4; docs → Task 5.
- **Type/name consistency:** `Config.Window`/`GUTTER_WINDOW`/`window`; flag var `window`; `openWindow(url, title string) error` identical in both build-tagged files; `//go:build webview` / `//go:build !webview`; Makefile `build`/`install` (window) vs `build-portable`/`install-portable`.
- **No placeholders:** every code step is complete; every run step states expected output.
- **Test portability:** `go test ./...` compiles the stub (no tag), so `TestOpenWindowStub` and the config tests run without cgo/WebKit — the suite stays pure-Go.
- **Main-thread correctness:** `openWindow` calls `runtime.LockOSThread()` and is invoked directly from `main` (not a goroutine); the HTTP server is moved to a goroutine only in the `-window` branch, leaving the non-window path unchanged.
- **Non-window regression:** with `-window` unset, the auto-open guard and the `serve()` call behave exactly as before (server on the main goroutine).
