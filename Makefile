PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/gutter

.PHONY: build install build-portable install-portable clean run

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

clean:
	rm -f gutter

run: build
	./gutter
