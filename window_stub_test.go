//go:build !webview

package main

import (
	"strings"
	"testing"
)

// TestOpenWindowStub lives behind the !webview tag so a `go test -tags webview`
// run doesn't call the real openWindow (which would block in the GUI event loop).
func TestOpenWindowStub(t *testing.T) {
	err := openWindow("http://127.0.0.1:0", "gutter")
	if err == nil {
		t.Fatal("stub openWindow should return an error")
	}
	if !strings.Contains(err.Error(), "without window support") {
		t.Errorf("unexpected stub error: %v", err)
	}
}
