//go:build webview

package main

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

// Lock the main goroutine to the main OS thread before the Go scheduler can
// migrate it. GTK/WebKit must run on the process's main thread; init() runs on
// the main goroutine on that thread, so this pins it for openWindow.
func init() {
	runtime.LockOSThread()
}

// openWindow opens url in a native window titled `title`, blocking until the
// window is closed. GTK/WebKit require the main OS thread, so the caller MUST
// invoke this synchronously from the main goroutine (never `go openWindow(...)`).
func openWindow(url, title string) error {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle(title)
	w.SetSize(1200, 800, webview.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}
