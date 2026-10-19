//go:build !windows

package ui

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize calls fn whenever the terminal is resized.
func watchResize(fn func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				fn()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
