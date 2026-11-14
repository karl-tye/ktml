package wasm

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// guestLogLinesPerSecond bounds the lines one guest stream logs per
	// second; the rest are counted and reported.
	guestLogLinesPerSecond = 100
	// guestLogLineBytes bounds one logged line.
	guestLogLineBytes = 4 << 10
)

// guestLog sends one guest output stream to the host log at debug level, a
// line at a time, never to the process's own stdout or stderr: under
// `ktml lsp` stdout carries JSON-RPC frames, and both reach the terminal.
// Every control character is escaped, so a guest can forge neither frames
// nor terminal escape sequences, and lines are bounded and rate-limited.
type guestLog struct {
	stream string
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	name    string
	line    []byte
	long    bool // the pending line was already logged truncated
	window  time.Time
	logged  int
	dropped int
}

func newGuestLog(stream string, logger *slog.Logger) *guestLog {
	if logger == nil {
		logger = slog.Default()
	}
	return &guestLog{stream: stream, logger: logger, now: time.Now}
}

// setName names the plugin in the log once its name is known.
func (l *guestLog) setName(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.name = name
}

func (l *guestLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range data {
		if b == '\n' {
			if !l.long {
				l.emit(l.line, false)
			}
			l.line, l.long = l.line[:0], false
			continue
		}
		if l.long {
			continue
		}
		l.line = append(l.line, b)
		if len(l.line) >= guestLogLineBytes {
			l.emit(l.line, true)
			l.line, l.long = l.line[:0], true
		}
	}
	return len(data), nil
}

// flush logs a pending line that has no newline yet.
func (l *guestLog) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.line) > 0 && !l.long {
		l.emit(l.line, false)
	}
	l.line, l.long = l.line[:0], false
}

func (l *guestLog) emit(line []byte, truncated bool) {
	now := l.now()
	if now.Sub(l.window) >= time.Second {
		if l.dropped > 0 {
			l.logger.Debug("plugin output dropped", "plugin", l.name, "stream", l.stream, "dropped", l.dropped)
		}
		l.window, l.logged, l.dropped = now, 0, 0
	}
	if l.logged >= guestLogLinesPerSecond {
		l.dropped++
		return
	}
	l.logged++
	attrs := []any{"plugin", l.name, "stream", l.stream, "line", escapeGuestText(line)}
	if truncated {
		attrs = append(attrs, "truncated", true)
	}
	l.logger.Debug("plugin output", attrs...)
}

// escapeGuestText returns text with every control character (C0, DEL, C1)
// and every invalid UTF-8 byte escaped, Go style.
func escapeGuestText(text []byte) string {
	var sb strings.Builder
	for len(text) > 0 {
		r, size := utf8.DecodeRune(text)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&sb, `\x%02x`, text[0])
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&sb, `\x%02x`, r)
		case unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r):
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			sb.WriteRune(r)
		}
		text = text[size:]
	}
	return sb.String()
}

// guestOutput is a guest's two output streams.
type guestOutput struct{ stdout, stderr *guestLog }

func (o guestOutput) setName(name string) {
	o.stdout.setName(name)
	o.stderr.setName(name)
}

// nameIfUnnamed names a plugin without a config name by its manifest.
func (o guestOutput) nameIfUnnamed(name string) {
	for _, l := range []*guestLog{o.stdout, o.stderr} {
		l.mu.Lock()
		if l.name == "" {
			l.name = name
		}
		l.mu.Unlock()
	}
}

func (o guestOutput) flush() {
	if o.stdout != nil {
		o.stdout.flush()
		o.stderr.flush()
	}
}
