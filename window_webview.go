//go:build webview

package main

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

// openWindow opens url in a native window titled `title`, blocking until the
// window is closed. GTK/WebKit require the main OS thread, so the caller MUST
// invoke this synchronously from the main goroutine (never `go openWindow(...)`).
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
