package wasm

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestGuestLog(stream string) (*guestLog, *bytes.Buffer, *time.Time) {
	var buffer bytes.Buffer
	now := time.Unix(1000, 0)
	log := newGuestLog(stream, slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.setName("p")
	log.now = func() time.Time { return now }
	return log, &buffer, &now
}

func TestGuestLog_GIVENControlCharacters_WHENWritten_THENEscapesThemInPrefixedDebugLines(t *testing.T) {
	log, buffer, _ := newTestGuestLog("stdout")
	_, _ = log.Write([]byte("Content-Length: 2\r\n\r\n{}\x1b[2J\x1b]0;title\x07\u009b31m\xff ok\n"))
	_, _ = log.Write([]byte("second\n"))

	output := buffer.String()
	for _, raw := range []string{"\x1b", "\x07", "\u009b", "\r"} {
		if strings.Contains(output, raw) {
			t.Errorf("log contains raw %q:\n%s", raw, output)
		}
	}
	for _, want := range []string{`"level":"DEBUG"`, `"plugin":"p"`, `"stream":"stdout"`,
		`Content-Length: 2\\r`, `{}\\x1b[2J\\x1b]0;title\\x07\\u009b31m\\xff ok`, `"line":"second"`} {
		if !strings.Contains(output, want) {
			t.Errorf("log lacks %s:\n%s", want, output)
		}
	}
	if lines := strings.Count(output, "\n"); lines != 4 {
		t.Errorf("logged %d records, want 4 (the CRLF frame header, its blank line, the body, second):\n%s", lines, output)
	}
}

func TestGuestLog_GIVENAFlood_WHENWritten_THENRateLimitsAndReportsTheDropped(t *testing.T) {
	log, buffer, now := newTestGuestLog("stderr")
	for range guestLogLinesPerSecond + 50 {
		_, _ = log.Write([]byte("spam\n"))
	}
	if got := strings.Count(buffer.String(), `"line":"spam"`); got != guestLogLinesPerSecond {
		t.Fatalf("logged %d lines in one second, want %d", got, guestLogLinesPerSecond)
	}
	*now = now.Add(time.Second)
	_, _ = log.Write([]byte("later\n"))
	if !strings.Contains(buffer.String(), `"dropped":50`) || !strings.Contains(buffer.String(), `"line":"later"`) {
		t.Fatalf("log = %s, want the 50 dropped lines reported, then the next line", buffer.String())
	}
}

func TestGuestLog_GIVENALongLineWithoutNewline_WHENWritten_THENTruncatesAndFlushes(t *testing.T) {
	log, buffer, _ := newTestGuestLog("stdout")
	_, _ = log.Write(bytes.Repeat([]byte("x"), 3*guestLogLineBytes))
	_, _ = log.Write([]byte("\ntail"))
	log.flush()
	output := buffer.String()
	if strings.Count(output, `"truncated":true`) != 1 || !strings.Contains(output, `"line":"tail"`) {
		t.Fatalf("log = %s, want one truncated line, then the flushed tail", output)
	}
	if len(output) > 2*guestLogLineBytes {
		t.Fatalf("logged %d bytes for one long line", len(output))
	}
}

func TestPlugin_GIVENGuestWritesStdout_WHENCalled_THENItReachesOnlyTheHostLog(t *testing.T) {
	ctx := context.Background()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stdout := captureStdout(t, func() {
		plugin, err := NewPluginWithOptions(ctx, buildStdoutGuest(t), Options{Name: "stdout-guest", Log: logger})
		if err != nil {
			t.Fatalf("NewPluginWithOptions() error = %v", err)
		}
		defer plugin.Close(ctx)
		if _, err := plugin.ExecuteLSP(ctx, "textDocument/hover", []byte(`{}`)); err != nil {
			t.Fatalf("ExecuteLSP() error = %v", err)
		}
	})
	if len(stdout) != 0 {
		t.Fatalf("guest wrote %q to the process stdout", stdout)
	}
	if !strings.Contains(buffer.String(), `"plugin":"stdout-guest","stream":"stdout","line":"guest-stdout-marker"`) {
		t.Fatalf("host log = %s, want the guest's line", buffer.String())
	}
}

// captureStdout runs f with os.Stdout redirected and returns what was
// written to it.
func captureStdout(t *testing.T, f func()) []byte {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	original := os.Stdout
	os.Stdout = writer
	done := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(reader)
		done <- data
	}()
	defer func() {
		os.Stdout = original
	}()
	f()
	os.Stdout = original
	_ = writer.Close()
	return <-done
}
