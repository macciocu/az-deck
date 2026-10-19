//go:build windows

package ui

// watchResize is a no-op on Windows (no SIGWINCH).
func watchResize(func()) (stop func()) { return func() {} }
