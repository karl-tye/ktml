package sse

import (
	"fmt"
	"net/http"
	"sync"
)

type Hub struct {
	mu      sync.Mutex
	clients map[chan string]bool
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan string]bool),
	}
}

func (h *Hub) Register(c chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

func (h *Hub) unregister(c chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *Hub) ActiveClients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	c := make(chan string, 16)
	h.Register(c)
	defer h.unregister(c)

	for {
		select {
		case msg := <-c:
			_, _ = fmt.Fprint(w, msg)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) Broadcast(event string, data string) {
	payload := Encode(event, data)

	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- payload:
		default:
		}
	}
}
