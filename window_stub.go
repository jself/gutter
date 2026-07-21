//go:build !webview

package main

import "errors"

// openWindow is the fallback when gutter is built without window support.
func openWindow(url, title string) error {
	return errors.New("this gutter was built without window support; rebuild the default target (make build) with cgo + a system webview")
}
