package sse

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHubClientRegistration(t *testing.T) {
	hub := NewHub()

	clientCh := make(chan string)
	hub.Register(clientCh)

	if count := hub.ActiveClients(); count != 1 {
		t.Errorf("expected 1 active client, got %d", count)
	}
}

func TestHubHTTPHeaders(t *testing.T) {
	hub := NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	hub.ServeHTTP(rr, req)

	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control no-cache, got %q", rr.Header().Get("Cache-Control"))
	}
	if rr.Header().Get("Connection") != "keep-alive" {
		t.Errorf("expected Connection keep-alive, got %q", rr.Header().Get("Connection"))
	}
	if !rr.Flushed {
		t.Error("expected immediate flush")
	}
}

func TestHubClientDisconnect(t *testing.T) {
	hub := NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(rr, req)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)

	if count := hub.ActiveClients(); count != 1 {
		t.Fatalf("expected 1 active client, got %d", count)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected ServeHTTP to return")
	}

	if count := hub.ActiveClients(); count != 0 {
		t.Fatalf("expected 0 active clients, got %d", count)
	}
}

func TestHubBroadcast(t *testing.T) {
	hub := NewHub()

	client1Ch := make(chan string, 10)
	client2Ch := make(chan string, 10)

	hub.Register(client1Ch)
	hub.Register(client2Ch)

	hub.Broadcast("reload", `{"file": "index.html"}`)

	expected := "event: reload\ndata: {\"file\": \"index.html\"}\n\n"

	select {
	case msg := <-client1Ch:
		if msg != expected {
			t.Errorf("client 1 expected %q, got %q", expected, msg)
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("client 1 did not receive message")
	}

	select {
	case msg := <-client2Ch:
		if msg != expected {
			t.Errorf("client 2 expected %q, got %q", expected, msg)
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("client 2 did not receive message")
	}
}

func TestHubBroadcastSlowClient(t *testing.T) {
	hub := NewHub()

	client1Ch := make(chan string)
	client2Ch := make(chan string, 10)

	hub.Register(client1Ch)
	hub.Register(client2Ch)

	hub.Broadcast("ping", "")

	select {
	case msg := <-client2Ch:
		if !strings.Contains(msg, "ping") {
			t.Errorf("expected ping event, got %q", msg)
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("client 2 did not receive message")
	}
}
