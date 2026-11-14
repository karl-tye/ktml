package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, stream string) []Event {
	t.Helper()
	reader := NewReader(strings.NewReader(stream))
	var events []Event
	for {
		event, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		events = append(events, event)
	}
}

func TestReader_GIVENMixedLineEndings_WHENRead_THENFollowsTheSSESpec(t *testing.T) {
	tests := []struct {
		name, stream string
		want         []Event
	}{
		{"LF", "event: a\ndata: 1\n\n", []Event{{Name: "a", Data: "1"}}},
		{"CRLF", "event: a\r\ndata: 1\r\n\r\n", []Event{{Name: "a", Data: "1"}}},
		{"CR", "event: a\rdata: 1\r\r", []Event{{Name: "a", Data: "1"}}},
		{"mixed in one stream", "event: a\r\ndata: x\rdata: y\n\r\nevent: b\rdata: z\n\n", []Event{{Name: "a", Data: "x\ny"}, {Name: "b", Data: "z"}}},
		{"multi-line data", "data: one\ndata: two\ndata:\ndata: four\n\n", []Event{{Name: "message", Data: "one\ntwo\n\nfour"}}},
		{"comments", ": keepalive\r\n:\revent: a\n: between\ndata: 1\n\n", []Event{{Name: "a", Data: "1"}}},
		{"only a comment", ": ping\n\n", nil},
		{"no space after the colon, and an extra one kept", "data:1\ndata:  2\n\n", []Event{{Name: "message", Data: "1\n 2"}}},
		{"a field without a colon", "data\n\n", []Event{{Name: "message", Data: ""}}},
		{"no data is not dispatched", "event: a\n\n", nil},
		{"unknown fields ignored", "retry: 10\nfoo: bar\ndata: 1\n\n", []Event{{Name: "message", Data: "1"}}},
		{"event name resets", "event: a\ndata: 1\n\ndata: 2\n\n", []Event{{Name: "a", Data: "1"}, {Name: "message", Data: "2"}}},
		{"a leading BOM", "\ufeffdata: 1\n\n", []Event{{Name: "message", Data: "1"}}},
		{"an unterminated event is dropped", "data: 1\n\ndata: 2\n", []Event{{Name: "message", Data: "1"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAll(t, tt.stream)
			if len(got) != len(tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEncode_GIVENDataWithAnyLineEnding_WHENReadBack_THENRoundTripsAsLFLines(t *testing.T) {
	tests := []struct{ name, event, data, want string }{
		{"plain", "reload", "1", "1"},
		{"empty data is still dispatched", "clear", "", ""},
		{"LF", "diagnostic", "a\nb", "a\nb"},
		{"CRLF", "diagnostic", "a\r\nb", "a\nb"},
		{"CR", "diagnostic", "a\rb", "a\nb"},
		{"mixed and trailing", "diagnostic", "a\r\nb\rc\n\r\n", "a\nb\nc\n\n"},
		{"injected fields stay data", "diagnostic", "x\revent: reload\r\rdata: forged", "x\nevent: reload\n\ndata: forged"},
		{"an event name with line breaks", "bad\r\nevent: reload", "1", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := Encode(tt.event, tt.data)
			events := readAll(t, encoded)
			if len(events) != 1 || events[0].Data != tt.want {
				t.Fatalf("Encode(%q, %q) = %q reads back as %#v, want one event with data %q", tt.event, tt.data, encoded, events, tt.want)
			}
			if strings.ContainsAny(events[0].Name, "\r\n") || events[0].Name == "reload" && tt.event != "reload" {
				t.Errorf("event name = %q from %q", events[0].Name, tt.event)
			}
		})
	}
}

func TestHubBroadcast_GIVENEmptyData_WHENRead_THENTheEventIsDispatched(t *testing.T) {
	hub := NewHub()
	client := make(chan string, 1)
	hub.Register(client)
	hub.Broadcast("ping", "")
	if events := readAll(t, <-client); len(events) != 1 || events[0].Name != "ping" {
		t.Fatalf("events = %#v, want one ping", events)
	}
}
