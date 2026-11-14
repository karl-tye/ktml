package lsp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/wasm"
)

type Manager struct {
	core           *managerCore
	namespace      string
	requestTimeout time.Duration
	acquisitionErr error
	closeOnce      sync.Once
	closeErr       error
	closed         atomic.Bool
}

type managerCore struct {
	mu           sync.Mutex
	servers      map[string]*ExternalServer
	stopping     map[*ExternalServer]error
	lsp          map[string]config.LSP
	identity     string
	launchEnv    []string
	launchErrors map[string]error
	workingDir   string
	closed       bool
	cleanupDone  chan struct{}
	cleanupBy    time.Time
	leases       int
}

type ExternalServer struct {
	manager         *managerCore
	target          string
	tree            *processTree
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	requests        map[string]*pendingRequest
	clientRequests  map[string]string
	transportPrefix string
	nextTransportID uint64
	accepting       bool
	mu              sync.Mutex
	writeMu         sync.Mutex
	stopOnce        sync.Once
	stopDone        chan struct{}
	stopErr         error
	stopBy          time.Time
	stopWait        time.Duration
	retry           *serverCleanupTransition
}

// ProxyNamespace proxies through a Manager lease with its own JSON-RPC id
// namespace, so callers sharing a backend cannot collide or cancel each other.
type ProxyNamespace struct {
	manager   *Manager
	namespace string
}

type proxyResult struct {
	payload []byte
	err     error
}

type pendingRequest struct {
	result       chan proxyResult
	clientID     json.RawMessage
	clientKey    string
	transportKey string
	abandonErr   error
}

type requestSubmission struct {
	transportKey string
	err          error
}

type rpcMessageKind uint8

const (
	rpcInvalid rpcMessageKind = iota
	rpcRequest
	rpcNotification
	rpcResponse
)

type rpcObject struct {
	members    map[string]json.RawMessage
	duplicates map[string]bool
	order      []string
}

type rpcMessage struct {
	kind      rpcMessageKind
	object    *rpcObject
	method    string
	id        json.RawMessage
	idKey     string
	parseErr  bool
	ambiguous bool
	err       error
}

type serverCleanupTransition struct {
	done     chan struct{}
	deadline time.Time
	err      error
}

const defaultProxyRequestTimeout = 30 * time.Second

const defaultProcessTreeCleanupTimeout = 2 * time.Second

var processTreeCleanupTimeout = defaultProcessTreeCleanupTimeout

const maxLSPMessageSize = 64 << 20

var errCancellationNotActive = errors.New("cancellation target is not active")

var nextProxyNamespace atomic.Uint64

func newProxyNamespace() string {
	return strconv.FormatUint(nextProxyNamespace.Add(1), 10) + "|"
}

var globalManager struct {
	sync.Mutex
	current    *managerCore
	transition *managerTransition
}

type managerTransition struct {
	desiredIdentity string
	done            chan struct{}
	replacement     *managerCore
	err             error
	waiters         int
	deadline        time.Time
}

func GetManager(cfg *config.Config) *Manager {
	manager, err := AcquireManager(cfg)
	if err != nil {
		return newManagerFailure(newManagerCore(cfg), err)
	}
	return manager
}

func AcquireManager(cfg *config.Config) (*Manager, error) {
	replacement := newManagerCore(cfg)
	for {
		globalManager.Lock()
		if transition := globalManager.transition; transition != nil {
			joined := transition.desiredIdentity == replacement.identity
			if joined {
				transition.waiters++
			}
			globalManager.Unlock()
			if err := waitForLifecycle(transition.done, transition.deadline, "LSP manager transition"); err != nil {
				if joined {
					globalManager.Lock()
					if globalManager.transition == transition {
						transition.waiters--
						globalManager.Unlock()
						return nil, err
					}
					globalManager.Unlock()
				} else {
					return nil, err
				}
			}
			if joined {
				if transition.err != nil {
					return nil, transition.err
				}
				return newManagerLease(transition.replacement), nil
			}
			continue
		}
		if current := globalManager.current; current != nil && current.identity == replacement.identity && !current.isClosed() {
			current.leases++
			globalManager.Unlock()
			return newManagerLease(current), nil
		}
		obsolete := globalManager.current
		if obsolete == nil {
			globalManager.current = replacement
			globalManager.Unlock()
			return newManagerLease(replacement), nil
		}
		deadline := time.Now().Add(processTreeCleanupTimeout)
		transition := &managerTransition{desiredIdentity: replacement.identity, done: make(chan struct{}), deadline: deadline}
		globalManager.transition = transition
		globalManager.Unlock()

		wasClosed := obsolete.isClosed()
		cleanupDeadline := nestedLifecycleDeadline(deadline)
		cleanupErr := obsolete.closeNowBefore(cleanupDeadline)
		if cleanupErr != nil && wasClosed {
			cleanupErr = obsolete.retryCleanupBefore(cleanupDeadline)
		}

		globalManager.Lock()
		if cleanupErr == nil {
			replacement.leases += transition.waiters
			globalManager.current = replacement
			transition.replacement = replacement
		} else {
			transition.err = fmt.Errorf("replace LSP manager: obsolete process-tree cleanup failed: %w", cleanupErr)
		}
		globalManager.transition = nil
		close(transition.done)
		globalManager.Unlock()
		if cleanupErr != nil {
			return nil, transition.err
		}
		return newManagerLease(replacement), nil
	}
}

func newManagerLease(core *managerCore) *Manager {
	return &Manager{core: core, namespace: newProxyNamespace(), requestTimeout: defaultProxyRequestTimeout}
}

func newManagerFailure(core *managerCore, err error) *Manager {
	return &Manager{core: core, namespace: newProxyNamespace(), requestTimeout: defaultProxyRequestTimeout, acquisitionErr: err}
}

// NewLSPNamespace returns a proxy that shares this lease's backends but keeps
// its own JSON-RPC ids: each plugin sees only its original ids, and its
// $/cancelRequest can only reach its own requests.
func (m *Manager) NewLSPNamespace() wasm.LSPProxy {
	return &ProxyNamespace{manager: m, namespace: newProxyNamespace()}
}

func (n *ProxyNamespace) ProxyRequest(ctx context.Context, target string, payload []byte) ([]byte, error) {
	return n.manager.proxyRequest(ctx, n.namespace, target, payload)
}

func (m *Manager) GenerationCacheIdentity() any {
	if m.acquisitionErr != nil {
		return nil
	}
	if len(m.core.lsp) > 0 {
		return nil
	}
	return m.core.identity
}

func (m *Manager) ProxyRequest(ctx context.Context, target string, payload []byte) ([]byte, error) {
	return m.proxyRequest(ctx, m.namespace, target, payload)
}

func (m *Manager) proxyRequest(ctx context.Context, namespace, target string, payload []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m.closed.Load() {
		return nil, fmt.Errorf("LSP manager is closed")
	}
	if m.acquisitionErr != nil {
		return nil, m.acquisitionErr
	}
	message := classifyRPCMessage(payload)
	isCancellation := message.kind == rpcNotification && message.method == "$/cancelRequest"
	if message.err != nil || (message.kind != rpcRequest && !isCancellation) {
		if message.err == nil {
			if message.kind == rpcNotification {
				message.err = fmt.Errorf("missing JSON-RPC request ID")
			} else {
				message.err = fmt.Errorf("message is not a request")
			}
		}
		return nil, fmt.Errorf("invalid JSON-RPC request envelope: %w", message.err)
	}
	core := m.core
	for {
		core.mu.Lock()
		if core.closed {
			core.mu.Unlock()
			return nil, fmt.Errorf("LSP manager is closed")
		}
		if isCancellation {
			if _, exists := core.servers[target]; !exists {
				core.mu.Unlock()
				return nil, nil
			}
		}
		var stoppingServer *ExternalServer
		for server := range core.stopping {
			if server.target == target {
				stoppingServer = server
				break
			}
		}
		if stoppingServer != nil {
			select {
			case <-stoppingServer.stopDone:
				core.mu.Unlock()
				if err := core.retryServerCleanupContext(ctx, stoppingServer, time.Now().Add(processTreeCleanupTimeout)); err != nil {
					return nil, fmt.Errorf("cleanup LSP server %s: %w", target, err)
				}
				continue
			default:
			}
			core.mu.Unlock()
			if err := waitForLifecycleContext(ctx, stoppingServer.stopDone, stoppingServer.stopBy, "LSP server cleanup"); err != nil {
				return nil, err
			}
			if stoppingServer.stopErr != nil {
				return nil, fmt.Errorf("cleanup LSP server %s: %w", target, stoppingServer.stopErr)
			}
			continue
		}
		srv, exists := core.servers[target]
		if exists {
			srv.mu.Lock()
			stopping := !srv.accepting
			srv.mu.Unlock()
			if stopping {
				core.mu.Unlock()
				// A retired server may not have entered fail yet, so stopBy can be
				// unset and is not synchronized here; stopWait bounds that cleanup.
				if err := waitForLifecycleContext(ctx, srv.stopDone, time.Now().Add(srv.stopWait), "LSP server cleanup"); err != nil {
					return nil, err
				}
				if srv.stopErr != nil {
					return nil, fmt.Errorf("cleanup LSP server %s: %w", target, srv.stopErr)
				}
				continue
			}
		}
		if !exists {
			lspCfg, ok := core.lsp[target]
			if !ok || len(lspCfg.Command) == 0 {
				core.mu.Unlock()
				return nil, fmt.Errorf("LSP target %s not configured", target)
			}
			if err := core.launchErrors[target]; err != nil {
				core.mu.Unlock()
				return nil, fmt.Errorf("failed to resolve LSP %s executable: %w", target, err)
			}

			transportPrefix, err := newTransportPrefix()
			if err != nil {
				core.mu.Unlock()
				return nil, fmt.Errorf("create LSP transport request ID prefix: %w", err)
			}
			srv = &ExternalServer{
				manager:         core,
				target:          target,
				requests:        make(map[string]*pendingRequest),
				clientRequests:  make(map[string]string),
				transportPrefix: transportPrefix,
				accepting:       true,
				stopDone:        make(chan struct{}),
				stopWait:        processTreeCleanupTimeout,
			}

			cmd := exec.Command(lspCfg.Command[0], lspCfg.Command[1:]...)
			cmd.Env = append([]string(nil), core.launchEnv...)
			cmd.Dir = core.workingDir
			cmd.WaitDelay = processTreeCleanupTimeout
			if err := prepareProcessTree(cmd); err != nil {
				core.mu.Unlock()
				return nil, err
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				core.mu.Unlock()
				return nil, fmt.Errorf("failed to get stdin: %v", err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				_ = stdin.Close()
				core.mu.Unlock()
				return nil, fmt.Errorf("failed to get stdout: %v", err)
			}

			srv.stdin = stdin
			srv.stdout = stdout

			if err := cmd.Start(); err != nil {
				_ = stdin.Close()
				_ = stdout.Close()
				core.mu.Unlock()
				return nil, fmt.Errorf("failed to start LSP %s: %v", target, err)
			}
			tree, err := trackProcessTree(cmd)
			if err != nil {
				_ = stdin.Close()
				_ = stdout.Close()
				core.mu.Unlock()
				return nil, fmt.Errorf("failed to track LSP %s process tree: %w", target, err)
			}
			srv.tree = tree

			core.servers[target] = srv
			go srv.readLoop()
		}
		core.mu.Unlock()

		if isCancellation {
			return nil, srv.sendCancellation(ctx, namespace, message, m.requestTimeout)
		}
		return srv.sendRequest(ctx, namespace, message, m.requestTimeout)
	}
}

func (s *ExternalServer) sendCancellation(ctx context.Context, namespace string, message rpcMessage, requestTimeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	s.mu.Lock()
	if !s.accepting || s.requests == nil {
		s.mu.Unlock()
		return fmt.Errorf("LSP server %s is not running", s.target)
	}
	params, err := rewriteCancellationParams(message.object.members["params"], namespace, s.clientRequests)
	if errors.Is(err, errCancellationNotActive) {
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("invalid cancellation request: %w", err)
	}
	payload, rewriteErr := rewriteRPCMembers(message.object, map[string]json.RawMessage{"params": params})
	s.mu.Unlock()
	if rewriteErr != nil {
		return rewriteErr
	}
	writeResult := make(chan error, 1)
	go func() { writeResult <- s.writePayload(payload) }()
	select {
	case err := <-writeResult:
		if err != nil {
			s.fail(fmt.Errorf("write LSP cancellation: %w", err))
		}
		return err
	case <-ctx.Done():
		s.fail(ctx.Err())
		<-writeResult
		return ctx.Err()
	}
}

func (s *ExternalServer) sendRequest(ctx context.Context, namespace string, message rpcMessage, requestTimeout time.Duration) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callerCtx := ctx
	ctx, cancel := context.WithTimeout(callerCtx, requestTimeout)
	defer cancel()

	request := &pendingRequest{
		result:    make(chan proxyResult, 1),
		clientID:  append(json.RawMessage(nil), message.id...),
		clientKey: namespace + message.idKey,
	}
	submissionResult := make(chan requestSubmission, 1)
	writeResult := make(chan error, 1)
	go func() {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if err := ctx.Err(); err != nil {
			submissionResult <- requestSubmission{err: err}
			return
		}
		transportKey, payload, err := s.registerRequest(message, request)
		submissionResult <- requestSubmission{transportKey: transportKey, err: err}
		if err == nil {
			writeResult <- s.writePayloadLocked(payload)
		}
	}()

	var submission requestSubmission
	select {
	case submission = <-submissionResult:
		if submission.err != nil {
			return nil, submission.err
		}
	case <-ctx.Done():
		// A caller that gives up before registration never reached the backend,
		// so only its own request fails; the shared server keeps serving others.
		if callerCtx.Err() != nil && s.abandonUnregisteredRequest(request, ctx.Err()) {
			return nil, ctx.Err()
		}
		if !s.retireOrAbandonRequest(request, ctx.Err()) {
			result := <-request.result
			return result.payload, result.err
		}
		s.fail(ctx.Err())
		<-submissionResult
		return nil, ctx.Err()
	}
	transportKey := submission.transportKey

	select {
	case writeErr := <-writeResult:
		if writeErr == nil {
			break
		}
		if !s.retireRequest(transportKey, request, true) {
			result := <-request.result
			return result.payload, result.err
		}
		s.fail(fmt.Errorf("write LSP request: %w", writeErr))
		return nil, writeErr
	case <-ctx.Done():
		if !s.retireRequest(transportKey, request, true) {
			result := <-request.result
			return result.payload, result.err
		}
		s.fail(ctx.Err())
		<-writeResult
		return nil, ctx.Err()
	}

	select {
	case result := <-request.result:
		return result.payload, result.err
	case <-ctx.Done():
		terminal := callerCtx.Err() == nil
		if !s.retireRequest(transportKey, request, terminal) {
			result := <-request.result
			return result.payload, result.err
		}
		if callerCtx.Err() == nil {
			s.fail(ctx.Err())
		}
		return nil, ctx.Err()
	}
}

func (s *ExternalServer) registerRequest(message rpcMessage, request *pendingRequest) (string, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.accepting || s.requests == nil {
		return "", nil, fmt.Errorf("LSP server %s is not running", s.target)
	}
	if request.abandonErr != nil {
		return "", nil, request.abandonErr
	}
	if _, exists := s.clientRequests[request.clientKey]; exists {
		return "", nil, fmt.Errorf("duplicate JSON-RPC request ID")
	}
	for {
		if s.nextTransportID == ^uint64(0) {
			return "", nil, fmt.Errorf("LSP transport request ID space exhausted")
		}
		s.nextTransportID++
		transportID := s.transportPrefix + "-" + strconv.FormatUint(s.nextTransportID, 10)
		transportKey := "s:" + transportID
		if _, collision := s.requests[transportKey]; collision {
			continue
		}
		rawTransportID, err := json.Marshal(transportID)
		if err != nil {
			return "", nil, err
		}
		payload, err := rewriteRPCMembers(message.object, map[string]json.RawMessage{"id": rawTransportID})
		if err != nil {
			return "", nil, err
		}
		s.requests[transportKey] = request
		s.clientRequests[request.clientKey] = transportID
		request.transportKey = transportKey
		return transportKey, payload, nil
	}
}

func (s *ExternalServer) abandonUnregisteredRequest(request *pendingRequest, cause error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.transportKey != "" {
		return false
	}
	request.abandonErr = cause
	return true
}

func (s *ExternalServer) retireOrAbandonRequest(request *pendingRequest, cause error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.transportKey == "" {
		request.abandonErr = cause
		s.accepting = false
		return true
	}
	if s.requests[request.transportKey] != request {
		return false
	}
	delete(s.requests, request.transportKey)
	delete(s.clientRequests, request.clientKey)
	s.accepting = false
	return true
}

func newTransportPrefix() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("ktml-%x", random), nil
}

func (s *ExternalServer) writePayload(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writePayloadLocked(payload)
}

func (s *ExternalServer) writePayloadLocked(payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(s.stdin, header); err != nil {
		return err
	}
	_, err := s.stdin.Write(payload)
	return err
}

func (s *ExternalServer) readLoop() {
	reader := bufio.NewReader(s.stdout)
	for {
		var contentLength int
		seenContentLength := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				s.fail(fmt.Errorf("read LSP response header: %w", err))
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					length, err := strconv.Atoi(strings.TrimSpace(parts[1]))
					if err != nil {
						s.fail(fmt.Errorf("invalid LSP Content-Length: %w", err))
						return
					}
					if seenContentLength && length != contentLength {
						s.fail(fmt.Errorf("conflicting LSP Content-Length headers %d and %d", contentLength, length))
						return
					}
					contentLength, seenContentLength = length, true
				}
			}
		}

		if contentLength <= 0 || contentLength > maxLSPMessageSize {
			s.fail(fmt.Errorf("missing or invalid LSP Content-Length"))
			return
		}

		payload := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, payload); err != nil {
			s.fail(fmt.Errorf("read LSP response payload: %w", err))
			return
		}

		message := classifyRPCMessage(payload)
		if message.err != nil {
			if message.parseErr {
				if err := s.writePayload(rpcError(json.RawMessage("null"), -32700, "parse error")); err != nil {
					s.fail(fmt.Errorf("write LSP backend parse error: %w", err))
					return
				}
				// The complete length-delimited frame was consumed, so the next frame remains synchronized.
				continue
			}
			if message.object != nil {
				if _, requestShaped := message.object.members["method"]; requestShaped {
					if err := s.writePayload(rpcError(message.errorID(), -32600, "invalid request")); err != nil {
						s.fail(fmt.Errorf("write invalid backend request error: %w", err))
						return
					}
					continue
				}
			}
			s.fail(fmt.Errorf("invalid LSP response envelope: %w", message.err))
			return
		}
		switch message.kind {
		case rpcResponse:
			s.deliverResponse(message)
		case rpcRequest:
			// Bidirectional requests are unsupported, but must be answered so the backend cannot block.
			if err := s.writePayload(rpcError(message.id, -32601, "method not found")); err != nil {
				s.fail(fmt.Errorf("write LSP backend request error: %w", err))
				return
			}
		case rpcNotification:
			// Backend notifications are intentionally not forwarded by this request/response proxy.
		}
	}
}

func (s *ExternalServer) retireRequest(transportKey string, request *pendingRequest, terminal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests[transportKey] != request {
		return false
	}
	delete(s.requests, transportKey)
	delete(s.clientRequests, request.clientKey)
	if terminal {
		s.accepting = false
	}
	return true
}

func (s *ExternalServer) deliverResponse(message rpcMessage) {
	s.mu.Lock()
	request, ok := s.requests[message.idKey]
	if ok {
		delete(s.requests, message.idKey)
		delete(s.clientRequests, request.clientKey)
		rewritten, err := rewriteRPCMembers(message.object, map[string]json.RawMessage{"id": request.clientID})
		if err != nil {
			request.result <- proxyResult{err: err}
		} else {
			request.result <- proxyResult{payload: rewritten}
		}
	}
	s.mu.Unlock()
}

func classifyRPCMessage(payload []byte) rpcMessage {
	message := rpcMessage{kind: rpcInvalid}
	object, parseErr, err := decodeRPCObject(payload)
	message.object, message.parseErr, message.err = object, parseErr, err
	if err != nil {
		return message
	}
	for name := range object.duplicates {
		message.err = fmt.Errorf("duplicate JSON-RPC member %q", name)
		return message
	}
	var version string
	if raw, ok := object.members["jsonrpc"]; !ok || json.Unmarshal(raw, &version) != nil || version != "2.0" {
		message.err = fmt.Errorf("jsonrpc must be %q", "2.0")
		return message
	}
	rawMethod, hasMethod := object.members["method"]
	_, hasResult := object.members["result"]
	_, hasError := object.members["error"]
	if hasMethod {
		if bytes.Equal(bytes.TrimSpace(rawMethod), []byte("null")) || json.Unmarshal(rawMethod, &message.method) != nil {
			message.err = fmt.Errorf("method must be a non-null string")
			return message
		}
		if hasResult || hasError {
			message.ambiguous = true
			message.err = fmt.Errorf("request or notification must not contain result or error")
			return message
		}
		if raw, ok := object.members["params"]; ok && !isJSONObjectOrArray(raw) {
			message.err = fmt.Errorf("params must be an object or array")
			return message
		}
		message.id, hasMethod = object.members["id"]
		if !hasMethod {
			message.kind = rpcNotification
			return message
		}
		message.idKey, message.err = normalizeRPCID(message.id)
		if message.err == nil {
			message.kind = rpcRequest
		}
		return message
	}
	if hasResult == hasError {
		message.ambiguous = true
		message.err = fmt.Errorf("response must contain exactly one of result or error")
		return message
	}
	message.id = object.members["id"]
	if bytes.Equal(bytes.TrimSpace(message.id), []byte("null")) {
		message.idKey = ""
	} else {
		message.idKey, message.err = normalizeRPCID(message.id)
		if message.err != nil {
			message.err = fmt.Errorf("invalid LSP response ID: %w", message.err)
			return message
		}
	}
	if hasError {
		errorObject, _, err := decodeRPCObject(object.members["error"])
		if err != nil {
			message.err = fmt.Errorf("response error must be an object")
			return message
		}
		for name := range errorObject.duplicates {
			message.err = fmt.Errorf("duplicate JSON-RPC error member %q", name)
			return message
		}
		var code int
		if raw, ok := errorObject.members["code"]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &code) != nil {
			message.err = fmt.Errorf("response error code must be an integer")
			return message
		}
		var errorMessage string
		if raw, ok := errorObject.members["message"]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &errorMessage) != nil {
			message.err = fmt.Errorf("response error message must be a non-null string")
			return message
		}
	}
	message.kind = rpcResponse
	return message
}

func decodeRPCObject(payload []byte) (*rpcObject, bool, error) {
	if !json.Valid(payload) {
		return nil, true, fmt.Errorf("malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return nil, true, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, false, fmt.Errorf("JSON-RPC message must be an object")
	}
	object := &rpcObject{members: make(map[string]json.RawMessage), duplicates: make(map[string]bool)}
	for decoder.More() {
		nameToken, _ := decoder.Token()
		name := nameToken.(string)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, true, err
		}
		if _, exists := object.members[name]; exists {
			object.duplicates[name] = true
			continue
		}
		object.members[name] = append(json.RawMessage(nil), raw...)
		object.order = append(object.order, name)
	}
	_, _ = decoder.Token()
	return object, false, nil
}

func isJSONObjectOrArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func rewriteRPCMembers(object *rpcObject, overrides map[string]json.RawMessage) ([]byte, error) {
	if object == nil {
		return nil, fmt.Errorf("missing JSON-RPC object")
	}
	var result bytes.Buffer
	result.WriteByte('{')
	for index, name := range object.order {
		if index > 0 {
			result.WriteByte(',')
		}
		encodedName, _ := json.Marshal(name)
		result.Write(encodedName)
		result.WriteByte(':')
		value := object.members[name]
		if override, ok := overrides[name]; ok {
			value = override
		}
		if !json.Valid(value) {
			return nil, fmt.Errorf("invalid JSON-RPC member %q", name)
		}
		result.Write(value)
	}
	result.WriteByte('}')
	return result.Bytes(), nil
}

func rewriteCancellationParams(raw json.RawMessage, namespace string, clientRequests map[string]string) (json.RawMessage, error) {
	params, _, err := decodeRPCObject(raw)
	if err != nil {
		return nil, fmt.Errorf("params must be an object")
	}
	for name := range params.duplicates {
		return nil, fmt.Errorf("duplicate cancellation member %q", name)
	}
	clientID, ok := params.members["id"]
	if !ok {
		return nil, fmt.Errorf("missing cancellation ID")
	}
	clientKey, err := normalizeRPCID(clientID)
	if err != nil {
		return nil, err
	}
	transportID, active := clientRequests[namespace+clientKey]
	if !active {
		return nil, errCancellationNotActive
	}
	rawTransportID, err := json.Marshal(transportID)
	if err != nil {
		return nil, err
	}
	rewritten, err := rewriteRPCMembers(params, map[string]json.RawMessage{"id": rawTransportID})
	return json.RawMessage(rewritten), err
}

func normalizeRPCID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("missing JSON-RPC request ID")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("invalid JSON-RPC request ID: %w", err)
	}
	switch id := value.(type) {
	case string:
		return "s:" + id, nil
	case json.Number:
		normalized, err := normalizeJSONNumber(string(id))
		if err != nil {
			return "", err
		}
		return "n:" + normalized, nil
	default:
		return "", fmt.Errorf("unsupported or missing request ID type")
	}
}

func normalizeJSONNumber(number string) (string, error) {
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign = "-"
		number = number[1:]
	}
	mantissa := number
	exponent := new(big.Int)
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		mantissa = number[:index]
		parsed, ok := new(big.Int).SetString(number[index+1:], 10)
		if !ok {
			return "", fmt.Errorf("invalid JSON-RPC numeric ID")
		}
		exponent.Set(parsed)
	}
	fractionDigits := 0
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		fractionDigits = len(mantissa) - index - 1
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(mantissa, "0")
	trailingZeros := len(mantissa) - len(trimmed)
	exponent.Sub(exponent, big.NewInt(int64(fractionDigits)))
	exponent.Add(exponent, big.NewInt(int64(trailingZeros)))
	if exponent.Sign() < 0 {
		return "", fmt.Errorf("JSON-RPC numeric request ID must be an integer")
	}
	return sign + trimmed + "e" + exponent.String(), nil
}

func newManagerCore(cfg *config.Config) *managerCore {
	lspConfig := make(map[string]config.LSP)
	if cfg != nil {
		for target, lspCfg := range cfg.LSP {
			lspConfig[target] = config.LSP{Command: append([]string(nil), lspCfg.Command...)}
		}
	}
	launchEnv := append([]string(nil), os.Environ()...)
	workingDir, _ := os.Getwd()
	launchErrors := make(map[string]error)
	for target, lspCfg := range lspConfig {
		if len(lspCfg.Command) == 0 {
			continue
		}
		resolved, err := exec.LookPath(lspCfg.Command[0])
		if err != nil {
			launchErrors[target] = err
			continue
		}
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workingDir, resolved)
		}
		lspCfg.Command[0] = resolved
		lspConfig[target] = lspCfg
	}
	identity := lspConfigurationIdentity(lspConfig, launchEnv, workingDir, launchErrors)
	return &managerCore{
		servers:      make(map[string]*ExternalServer),
		stopping:     make(map[*ExternalServer]error),
		lsp:          lspConfig,
		identity:     identity,
		launchEnv:    launchEnv,
		launchErrors: launchErrors,
		workingDir:   workingDir,
		cleanupDone:  make(chan struct{}),
		leases:       1,
	}
}

func (m *managerCore) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		if m.acquisitionErr != nil {
			m.closeErr = m.acquisitionErr
			return
		}
		m.closeErr = m.core.release()
	})
	return m.closeErr
}

func (m *managerCore) release() error {
	for {
		globalManager.Lock()
		if transition := globalManager.transition; transition != nil {
			globalManager.Unlock()
			if err := waitForLifecycle(transition.done, transition.deadline, "LSP manager transition"); err != nil {
				return err
			}
			continue
		}
		if globalManager.current != m {
			globalManager.Unlock()
			return m.closeNow()
		}
		if m.leases > 1 {
			m.leases--
			globalManager.Unlock()
			return nil
		}
		deadline := time.Now().Add(processTreeCleanupTimeout)
		transition := &managerTransition{done: make(chan struct{}), deadline: deadline}
		globalManager.transition = transition
		globalManager.Unlock()

		cleanupErr := m.closeNowBefore(nestedLifecycleDeadline(deadline))

		globalManager.Lock()
		if cleanupErr == nil && globalManager.current == m {
			globalManager.current = nil
		}
		transition.err = cleanupErr
		globalManager.transition = nil
		close(transition.done)
		globalManager.Unlock()
		return cleanupErr
	}
}

func (m *managerCore) closeNow() error {
	return m.closeNowBefore(time.Now().Add(processTreeCleanupTimeout))
}

func (m *managerCore) closeNowBefore(deadline time.Time) error {
	m.mu.Lock()
	if m.closed {
		cleanupDone := m.cleanupDone
		cleanupBy := m.cleanupBy
		m.mu.Unlock()
		if err := waitForLifecycle(cleanupDone, minTime(deadline, cleanupBy), "LSP manager cleanup"); err != nil {
			return err
		}
		return m.cleanupError()
	}
	m.closed = true
	m.cleanupBy = deadline
	servers := make([]*ExternalServer, 0, len(m.servers)+len(m.stopping))
	for _, server := range m.servers {
		servers = append(servers, server)
	}
	for server := range m.stopping {
		servers = append(servers, server)
	}
	clear(m.servers)
	m.mu.Unlock()

	results := make(chan struct{}, len(servers))
	for _, server := range servers {
		go func() {
			_ = server.failBefore(fmt.Errorf("LSP manager closed"), deadline)
			results <- struct{}{}
		}()
	}
	for range servers {
		select {
		case <-results:
		case <-time.After(max(time.Until(deadline), 0)):
			close(m.cleanupDone)
			return fmt.Errorf("LSP manager cleanup exceeded its deadline")
		}
	}
	close(m.cleanupDone)
	return m.cleanupError()
}

func (m *managerCore) retryCleanupBefore(deadline time.Time) error {
	if err := waitForLifecycle(m.cleanupDone, deadline, "LSP manager cleanup"); err != nil {
		return err
	}
	m.mu.Lock()
	servers := make([]*ExternalServer, 0, len(m.stopping))
	for server := range m.stopping {
		servers = append(servers, server)
	}
	m.mu.Unlock()
	results := make(chan error, len(servers))
	for _, server := range servers {
		go func() {
			if err := waitForLifecycle(server.stopDone, deadline, "LSP server cleanup"); err != nil {
				m.finishServerCleanup(server, err)
				results <- err
				return
			}
			results <- m.retryServerCleanup(server, deadline)
		}()
	}
	for range servers {
		select {
		case <-results:
		case <-time.After(max(time.Until(deadline), 0)):
			return fmt.Errorf("LSP manager cleanup retry exceeded its deadline")
		}
	}
	return m.cleanupError()
}

func (m *managerCore) retryServerCleanup(server *ExternalServer, deadline time.Time) error {
	return m.retryServerCleanupContext(context.Background(), server, deadline)
}

func (m *managerCore) retryServerCleanupContext(ctx context.Context, server *ExternalServer, deadline time.Time) error {
	m.mu.Lock()
	if retry := server.retry; retry != nil {
		m.mu.Unlock()
		if err := waitForLifecycleContext(ctx, retry.done, minTime(deadline, retry.deadline), "LSP server cleanup retry"); err != nil {
			return err
		}
		return retry.err
	}
	retry := &serverCleanupTransition{done: make(chan struct{}), deadline: deadline}
	server.retry = retry
	m.mu.Unlock()

	go func() {
		retry.err = retryProcessTreeCleanupBefore(server.tree, nestedLifecycleDeadline(deadline))
		m.finishServerCleanup(server, retry.err)
		m.mu.Lock()
		server.retry = nil
		close(retry.done)
		m.mu.Unlock()
	}()
	if err := waitForLifecycleContext(ctx, retry.done, deadline, "LSP server cleanup retry"); err != nil {
		return err
	}
	return retry.err
}

func (m *managerCore) cleanupError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var cleanupErr error
	for _, err := range m.stopping {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (m *managerCore) beginServerCleanup(target string, server *ExternalServer) {
	m.mu.Lock()
	if m.servers[target] == server {
		delete(m.servers, target)
	}
	m.stopping[server] = fmt.Errorf("LSP server %s cleanup is incomplete", target)
	m.mu.Unlock()
}

func (m *managerCore) finishServerCleanup(server *ExternalServer, cleanupErr error) {
	m.mu.Lock()
	if cleanupErr == nil {
		delete(m.stopping, server)
	} else {
		m.stopping[server] = cleanupErr
	}
	m.mu.Unlock()
}

func (s *ExternalServer) fail(cause error) error {
	return s.failBefore(cause, time.Now().Add(s.stopWait))
}

func (s *ExternalServer) failBefore(cause error, deadline time.Time) error {
	s.stopOnce.Do(func() {
		s.stopBy = deadline
		s.manager.beginServerCleanup(s.target, s)
		go s.stop(cause, nestedLifecycleDeadline(deadline))
	})
	if err := waitForLifecycle(s.stopDone, minTime(deadline, s.stopBy), "LSP server cleanup"); err != nil {
		return err
	}
	return s.stopErr
}

func (s *ExternalServer) stop(cause error, deadline time.Time) {
	s.mu.Lock()
	requests := s.requests
	s.requests = nil
	s.mu.Unlock()

	stdinErr := s.stdin.Close()
	stdoutErr := s.stdout.Close()
	for _, request := range requests {
		request.result <- proxyResult{err: cause}
	}
	cleanupErr := errors.Join(
		wrapCloseError("LSP server stdin", stdinErr),
		wrapCloseError("LSP server stdout", stdoutErr),
		cleanupProcessTreeBefore(s.tree, deadline),
	)
	s.stopErr = cleanupErr
	s.manager.finishServerCleanup(s, cleanupErr)
	close(s.stopDone)
}

func wrapCloseError(resource string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close %s: %w", resource, err)
}

func waitForLifecycle(done <-chan struct{}, deadline time.Time, operation string) error {
	return waitForLifecycleContext(context.Background(), done, deadline, operation)
}

func waitForLifecycleContext(ctx context.Context, done <-chan struct{}, deadline time.Time, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-done:
		return ctx.Err()
	default:
	}
	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("%s exceeded its deadline", operation)
	}
}

func minTime(first, second time.Time) time.Time {
	if second.IsZero() || first.Before(second) {
		return first
	}
	return second
}

func nestedLifecycleDeadline(deadline time.Time) time.Time {
	remaining := time.Until(deadline)
	margin := remaining / 5
	if margin < time.Millisecond {
		margin = time.Millisecond
	}
	nested := deadline.Add(-margin)
	if nested.Before(time.Now()) {
		return deadline
	}
	return nested
}

func lspConfigurationIdentity(lspConfig map[string]config.LSP, launchEnvironment []string, workingDirectory string, launchErrors map[string]error) string {
	hash := sha256.New()
	if len(lspConfig) == 0 {
		return fmt.Sprintf("%x", hash.Sum(nil))
	}
	launchEnvironment = append([]string(nil), launchEnvironment...)
	sort.Strings(launchEnvironment)
	environmentHash := sha256.Sum256([]byte(strings.Join(launchEnvironment, "\x00")))
	_, _ = fmt.Fprintf(hash, "cwd=%s\nenv=%x\n", workingDirectory, environmentHash)

	targets := make([]string, 0, len(lspConfig))
	for target := range lspConfig {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		command := lspConfig[target].Command
		commandJSON, _ := json.Marshal(command)
		commandHash := sha256.Sum256(commandJSON)
		_, _ = fmt.Fprintf(hash, "target=%s\ncommand=%x\n", target, commandHash)
		if len(command) == 0 {
			continue
		}
		if err := launchErrors[target]; err != nil {
			_, _ = fmt.Fprintf(hash, "executable-error=%x\n", sha256.Sum256([]byte(err.Error())))
			continue
		}
		executableHash, err := hashExecutable(command[0])
		if err != nil {
			_, _ = fmt.Fprintf(hash, "executable-error=%x\n", sha256.Sum256([]byte(err.Error())))
			continue
		}
		_, _ = fmt.Fprintf(hash, "executable=%x\n", executableHash)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func hashExecutable(path string) ([sha256.Size]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
