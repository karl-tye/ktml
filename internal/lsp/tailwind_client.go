package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// childServer is a language server the host runs as a client: the editor
// never talks to it directly. It answers the server's own requests through
// onRequest and hands its notifications to onNotification.
type childServer struct {
	name string
	cmd  *exec.Cmd
	tree *processTree

	stdin io.WriteCloser
	// writeMu orders frames on stdin.
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[string]chan childResponse
	failed  error
	// ready is closed once initialize answered or the server failed.
	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}

	onRequest      func(method string, params json.RawMessage) any
	onNotification func(method string, params json.RawMessage)
	stderr         *tailBuffer
}

type childResponse struct {
	result json.RawMessage
	err    error
}

// startChildServer starts command in dir and sends initialize with params.
// Requests sent before initialize answers wait for it.
func startChildServer(name string, command []string, dir string, initialize any, onRequest func(string, json.RawMessage) any, onNotification func(string, json.RawMessage)) (*childServer, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("%s: no command", name)
	}
	path, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, command[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = processTreeCleanupTimeout
	if err := prepareProcessTree(cmd); err != nil {
		return nil, err
	}
	server := &childServer{
		name:           name,
		cmd:            cmd,
		pending:        make(map[string]chan childResponse),
		ready:          make(chan struct{}),
		done:           make(chan struct{}),
		onRequest:      onRequest,
		onNotification: onNotification,
		stderr:         &tailBuffer{limit: 4096, relay: os.Stderr, prefix: name + ": "},
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = server.stderr
	server.stdin = stdin
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	tree, err := trackProcessTree(cmd)
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	server.tree = tree
	go server.readLoop(bufio.NewReader(stdout))
	go func() {
		_, err := server.call(context.Background(), "initialize", initialize, 60*time.Second, false)
		if err != nil {
			server.fail(fmt.Errorf("initialize: %w", err))
			return
		}
		_ = server.notify("initialized", map[string]any{})
		server.markReady()
	}()
	return server, nil
}

func (s *childServer) markReady() {
	s.readyOnce.Do(func() { close(s.ready) })
}

// Err returns why the server stopped, or nil while it runs.
func (s *childServer) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// Done is closed when the server has failed or exited.
func (s *childServer) Done() <-chan struct{} { return s.done }

func (s *childServer) fail(cause error) {
	s.mu.Lock()
	if s.failed != nil {
		s.mu.Unlock()
		return
	}
	if tail := strings.TrimSpace(s.stderr.String()); tail != "" {
		cause = fmt.Errorf("%w: %s", cause, lastLine(tail))
	}
	s.failed = cause
	pending := s.pending
	s.pending = map[string]chan childResponse{}
	s.mu.Unlock()
	for _, response := range pending {
		response <- childResponse{err: cause}
	}
	s.markReady()
	close(s.done)
}

// Request sends a request once the server is initialized and waits for its
// result at most timeout.
func (s *childServer) Request(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	return s.call(ctx, method, params, timeout, true)
}

func (s *childServer) call(ctx context.Context, method string, params any, timeout time.Duration, waitReady bool) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if waitReady {
		select {
		case <-s.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	if s.failed != nil {
		err := s.failed
		s.mu.Unlock()
		return nil, err
	}
	s.nextID++
	id := strconv.Itoa(s.nextID)
	response := make(chan childResponse, 1)
	s.pending[id] = response
	s.mu.Unlock()
	if err := s.write(map[string]any{"jsonrpc": "2.0", "id": s.nextIDValue(id), "method": method, "params": params}); err != nil {
		s.fail(err)
	}
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		_ = s.notify("$/cancelRequest", map[string]any{"id": s.nextIDValue(id)})
		return nil, ctx.Err()
	}
}

func (s *childServer) nextIDValue(id string) int {
	value, _ := strconv.Atoi(id)
	return value
}

// Notify sends a notification once the server is initialized, in order with
// the notifications sent before it.
func (s *childServer) Notify(method string, params any) error {
	<-s.ready
	if err := s.Err(); err != nil {
		return err
	}
	return s.notify(method, params)
}

func (s *childServer) notify(method string, params any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *childServer) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = s.stdin.Write(payload)
	return err
}

func (s *childServer) readLoop(reader *bufio.Reader) {
	for {
		payload, err := readChildFrame(reader)
		if err != nil {
			s.fail(fmt.Errorf("%s exited: %w", s.name, err))
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		switch {
		case message.Method != "" && len(message.ID) > 0:
			result := s.onRequest(message.Method, message.Params)
			_ = s.write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
		case message.Method != "":
			s.onNotification(message.Method, message.Params)
		case len(message.ID) > 0:
			s.mu.Lock()
			response, ok := s.pending[string(message.ID)]
			delete(s.pending, string(message.ID))
			s.mu.Unlock()
			if !ok {
				continue
			}
			if message.Error != nil {
				response <- childResponse{err: fmt.Errorf("%s: %s (%d)", s.name, message.Error.Message, message.Error.Code)}
			} else {
				response <- childResponse{result: message.Result}
			}
		}
	}
}

func readChildFrame(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 || length > maxLSPMessageSize {
		return nil, errors.New("missing or invalid Content-Length")
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

// Close asks the server to shut down and exit, then removes its process
// group.
func (s *childServer) Close() error {
	if s.Err() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		select {
		case <-s.ready:
			_, _ = s.call(ctx, "shutdown", nil, time.Second, false)
			_ = s.notify("exit", nil)
		case <-ctx.Done():
		}
		cancel()
	}
	_ = s.stdin.Close()
	err := cleanupProcessTree(s.tree)
	s.fail(errors.New("closed"))
	return err
}

// tailBuffer relays a child's stderr and keeps its last bytes for error
// messages.
type tailBuffer struct {
	mu     sync.Mutex
	limit  int
	data   []byte
	relay  io.Writer
	prefix string
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = b.data[len(b.data)-b.limit:]
	}
	if b.relay != nil {
		for _, line := range strings.SplitAfter(string(p), "\n") {
			if line != "" {
				_, _ = io.WriteString(b.relay, b.prefix+line)
			}
		}
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
