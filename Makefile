PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/gutter

# Version is stamped into the binary (main.version). Defaults to `git describe`
# (falls back to a short commit when there are no tags); override with
# `make release VERSION=v1.2.3`.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Portable release targets (pure Go, no cgo/WebKit). Native window is Linux
# build-from-source only; these ship browser-mode binaries.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build install build-portable install-portable release clean clean-dist run

# Default build includes the native window (needs cgo + a system webview:
# WebKit2GTK on Linux, WebKit on macOS, WebView2 on Windows). scripts/build-window.sh
# adds the `webview` tag and, on Linux systems that ship only webkit2gtk-4.1,
# shims the webview_go pkg-config dependency from 4.0 to 4.1.
build:
	./scripts/build-window.sh -ldflags "$(LDFLAGS)" -o gutter .

install:
	./scripts/build-window.sh -ldflags "$(LDFLAGS)" -o $(BIN) .
	@echo "installed $(BIN)"

# Portable build: pure Go, no cgo/WebKit, cross-compilable. -window falls back
# to the browser on this binary.
build-portable:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o gutter .

install-portable:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) .
	@echo "installed $(BIN) (portable, no window support)"

# Cross-build portable binaries for all platforms into dist/, with checksums.
# These are browser-mode (no native window); Linux users wanting the window
# build from source with `make build`.
release: clean-dist
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=dist/gutter-$(VERSION)-$$os-$$arch; \
		[ "$$os" = windows ] && out=$$out.exe; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o $$out . || exit 1; \
	done
	@cd dist && sha256sum gutter-* > SHA256SUMS
	@echo "release $(VERSION):"; ls -1 dist/

clean: clean-dist
	rm -f gutter

clean-dist:
	rm -rf dist

run: build
	./gutter
