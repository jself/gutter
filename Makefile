PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/gutter

.PHONY: build install build-portable install-portable clean run

# Default build includes the native window (needs cgo + a system webview:
# WebKit2GTK on Linux, WebKit on macOS, WebView2 on Windows). scripts/build-window.sh
# adds the `webview` tag and, on Linux systems that ship only webkit2gtk-4.1,
# shims the webview_go pkg-config dependency from 4.0 to 4.1.
build:
	./scripts/build-window.sh -o gutter .

install:
	./scripts/build-window.sh -o $(BIN) .
	@echo "installed $(BIN)"

# Portable build: pure Go, no cgo/WebKit, cross-compilable. -window falls back
# to the browser on this binary.
build-portable:
	CGO_ENABLED=0 go build -o gutter .

install-portable:
	CGO_ENABLED=0 go build -o $(BIN) .
	@echo "installed $(BIN) (portable, no window support)"

clean:
	rm -f gutter

run: build
	./gutter
