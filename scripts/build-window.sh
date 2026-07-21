#!/bin/sh
# Build gutter with native window support (the `webview` build tag, cgo).
#
# webview_go pins its pkg-config dependency to `webkit2gtk-4.0`, but modern
# Linux distros (Arch, recent Fedora/Ubuntu) ship only `webkit2gtk-4.1` (same
# WebKit API, linked against libsoup3). When that's the case we generate a
# throwaway pkg-config shim that forwards `webkit2gtk-4.0` to `webkit2gtk-4.1`,
# so the pinned webview_go builds unmodified. Systems with a real 4.0, and
# macOS/Windows (which don't use this pkg-config at all), are unaffected.
#
# Usage: scripts/build-window.sh <go-build-args...>   e.g. -o gutter .
set -e

if [ "$(go env GOOS)" = "linux" ]; then
	if ! pkg-config --exists webkit2gtk-4.0 2>/dev/null && pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
		SHIM_DIR=$(mktemp -d)
		trap 'rm -rf "$SHIM_DIR"' EXIT
		cat > "$SHIM_DIR/webkit2gtk-4.0.pc" <<'EOF'
Name: webkit2gtk-4.0
Description: shim forwarding webkit2gtk-4.0 to webkit2gtk-4.1
Version: 4.1
Requires: webkit2gtk-4.1
EOF
		PKG_CONFIG_PATH="$SHIM_DIR${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
		export PKG_CONFIG_PATH
		echo "build-window: webkit2gtk-4.0 absent; shimming to webkit2gtk-4.1" >&2
	fi
fi

# Not `exec`: keep this shell alive so the EXIT trap removes the shim dir.
# `set -e` forwards a non-zero build exit code.
env CGO_ENABLED=1 go build -tags webview "$@"
