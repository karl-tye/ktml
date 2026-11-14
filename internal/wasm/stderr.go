package wasm

import (
	"io"
	"strings"
	"sync"
)

// guestStderrCapture bounds what a capture keeps.
const guestStderrCapture = 64 << 10

// guestStderr forwards the guest's stderr and, while capturing, keeps a
// bounded copy, so a failing call can report what the guest printed.
type guestStderr struct {
	target    io.Writer
	mu        sync.Mutex
	capturing bool
	kept      strings.Builder
}

func (w *guestStderr) Write(data []byte) (int, error) {
	w.mu.Lock()
	if w.capturing && w.kept.Len() < guestStderrCapture {
		w.kept.Write(data[:min(len(data), guestStderrCapture-w.kept.Len())])
	}
	w.mu.Unlock()
	return w.target.Write(data)
}

func (w *guestStderr) capture() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.capturing = true
	w.kept.Reset()
}

func (w *guestStderr) release() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.capturing = false
	kept := w.kept.String()
	w.kept.Reset()
	return kept
}

// guestPanic returns the message of a Go panic printed to stderr, without
// the goroutine trace, or "".
func guestPanic(stderr string) string {
	start := strings.LastIndex(stderr, "panic: ")
	if start < 0 {
		return ""
	}
	message := stderr[start+len("panic: "):]
	if end := strings.Index(message, "\n\ngoroutine "); end >= 0 {
		message = message[:end]
	}
	return strings.TrimSpace(message)
}
