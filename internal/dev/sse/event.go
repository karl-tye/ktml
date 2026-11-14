package sse

import (
	"bufio"
	"io"
	"strings"
)

// Encode formats one server-sent event. Every line ending in data (CRLF, CR,
// or LF, all line terminators to an EventSource) starts a new data line, so
// the client reads data back with LF line endings and nothing in it can end
// the event or set another field. The event always carries a data line: the
// spec does not dispatch an event without one. Line breaks in name are
// replaced with spaces.
func Encode(name, data string) string {
	var sb strings.Builder
	sb.WriteString("event: " + strings.NewReplacer("\r", " ", "\n", " ").Replace(name) + "\n")
	for line := range strings.SplitSeq(lineEndings.Replace(data), "\n") {
		sb.WriteString("data: " + line + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// lineEndings normalizes CRLF and CR to LF.
var lineEndings = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// Event is one dispatched server-sent event.
type Event struct {
	// Name is the event type; "message" when the event names none.
	Name string
	Data string
}

// Reader parses an event stream as the HTML spec's EventSource does: CRLF,
// CR, and LF line endings, comment lines, multi-line data, and the optional
// space after a field's colon. It ignores id and retry.
type Reader struct {
	reader  *bufio.Reader
	started bool
	skipLF  bool // the last line ended with CR, so a following LF is part of it
}

// NewReader returns a Reader of r.
func NewReader(r io.Reader) *Reader {
	return &Reader{reader: bufio.NewReader(r)}
}

// Next returns the next dispatched event, or io.EOF at the end of the stream.
// An event that the stream ends before its blank line is not dispatched.
func (r *Reader) Next() (Event, error) {
	var name string
	var data strings.Builder
	hasData := false
	for {
		line, err := r.line()
		if err != nil {
			return Event{}, err
		}
		if line == "" {
			if !hasData {
				name = ""
				continue
			}
			value := strings.TrimSuffix(data.String(), "\n")
			if name == "" {
				name = "message"
			}
			return Event{Name: name, Data: value}, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			data.WriteString(value + "\n")
			hasData = true
		}
	}
}

// line reads one line without its terminator.
func (r *Reader) line() (string, error) {
	var sb strings.Builder
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return "", err
		}
		if r.skipLF {
			r.skipLF = false
			if b == '\n' {
				continue
			}
		}
		switch b {
		case '\n':
			return r.strip(sb.String()), nil
		case '\r':
			r.skipLF = true
			return r.strip(sb.String()), nil
		}
		sb.WriteByte(b)
	}
}

// strip removes the byte order mark the stream may start with.
func (r *Reader) strip(line string) string {
	if !r.started {
		r.started = true
		return strings.TrimPrefix(line, "\ufeff")
	}
	return line
}
