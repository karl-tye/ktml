package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

func TestGetManager_GIVENConfigurationChanges_WHENRequestIsPending_THENReplacesManagerAndReapsOldServer(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	readyPath := t.TempDir() + "/old-ready"
	manager := GetManager(lspManagerTestConfig("silent", readyPath))
	t.Cleanup(func() { _ = manager.Close() })

	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requestErr := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(requestCtx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
		requestErr <- err
	}()
	pid := waitForHelperPID(t, readyPath)

	replacement := GetManager(lspManagerTestConfig("reply", "new-configuration"))
	t.Cleanup(func() { _ = replacement.Close() })
	if replacement == manager {
		t.Fatal("GetManager() retained the manager for obsolete LSP configuration")
	}

	select {
	case err := <-requestErr:
		if err == nil {
			t.Fatal("pending request succeeded after its configured server was replaced")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending request was not failed when its manager was replaced")
	}
	waitForProcessExit(t, pid)

	response, err := replacement.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
	if err != nil {
		t.Fatalf("replacement ProxyRequest() error = %v", err)
	}
	if !strings.Contains(string(response), `"result":"new-configuration"`) {
		t.Fatalf("replacement response = %s, want new configuration", response)
	}
}

func TestGetManager_GIVENSameIdentityHasTwoOwners_WHENOneClosesTwice_THENOtherRemainsUsable(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	cfg := lspManagerTestConfig("reply", "shared-manager")
	first := GetManager(cfg)
	second := GetManager(cfg)
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	if first == second {
		t.Fatal("GetManager() returned the same lease handle for identical LSP configuration")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second first.Close() error = %v", err)
	}

	response, err := second.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("second owner ProxyRequest() error = %v", err)
	}
	if !strings.Contains(string(response), `"result":"shared-manager"`) {
		t.Fatalf("second owner response = %s", response)
	}
}

func TestManager_GIVENLaunchEnvironmentChangesAfterIdentity_THENStartsServerWithIdentitySnapshot(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	t.Setenv("KTML_LSP_LAUNCH_VALUE", "identity-environment")
	manager := GetManager(lspManagerTestConfig("env-reply", "KTML_LSP_LAUNCH_VALUE"))
	t.Cleanup(func() { _ = manager.Close() })
	t.Setenv("KTML_LSP_LAUNCH_VALUE", "later-environment")

	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if !strings.Contains(string(response), `"result":"identity-environment"`) {
		t.Fatalf("response = %s, want environment represented by manager identity", response)
	}
}

func TestManager_GIVENLiveServerNeverReplies_WHENRequestHasNoCallerDeadline_THENFailsAtProxyDeadline(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	readyPath := t.TempDir() + "/silent-ready"
	manager := GetManager(lspManagerTestConfig("silent", readyPath))
	manager.requestTimeout = 50 * time.Millisecond
	t.Cleanup(func() { _ = manager.Close() })

	started := time.Now()
	_, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if err == nil || err != context.DeadlineExceeded {
		t.Fatalf("ProxyRequest() error = %v, want proxy deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("silent proxy request took %v, want bounded return", elapsed)
	}
	pid := waitForHelperPID(t, readyPath)
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	waitForProcessExit(t, pid)
}

func TestManager_GIVENServerTransportTerminates_WHENRequestingAgain_THENReapsAndRestarts(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	for _, mode := range []string{"eof-then-reply", "protocol-then-reply"} {
		t.Run(mode, func(t *testing.T) {
			sequenceDir := t.TempDir()
			manager := GetManager(lspManagerTestConfig(mode, sequenceDir))
			t.Cleanup(func() { _ = manager.Close() })

			requestCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			_, err := manager.ProxyRequest(requestCtx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
			if err == nil {
				t.Fatal("ProxyRequest() succeeded after terminal server transport failure")
			}
			if elapsed := time.Since(started); elapsed > 4*time.Second {
				t.Fatalf("terminal server failure took %v to reach pending request", elapsed)
			}
			firstPID := waitForHelperPID(t, sequenceDir+"/first-pid")
			waitForProcessExit(t, firstPID)

			response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":2,"method":"test"}`))
			if err != nil {
				t.Fatalf("restarted ProxyRequest() error = %v", err)
			}
			if !strings.Contains(string(response), `"result":"restarted"`) {
				t.Fatalf("restarted response = %s", response)
			}
			secondPID := waitForHelperPID(t, sequenceDir+"/second-pid")
			waitForProcessExit(t, secondPID)
		})
	}
}

func TestManager_GIVENValidJSONRPCErrorResponse_THENReturnsItAsData(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	manager := GetManager(lspManagerTestConfig("error", "language failure"))
	t.Cleanup(func() { _ = manager.Close() })

	response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":"request-1","method":"test"}`))
	if err != nil {
		t.Fatalf("ProxyRequest() Go error = %v, want protocol error response data", err)
	}
	if !strings.Contains(string(response), `"error":{"code":-32000,"message":"language failure"}`) {
		t.Fatalf("ProxyRequest() response = %s, want JSON-RPC error", response)
	}
}

func TestManager_GIVENServerEOFWithMultiplePendingRequests_THENFailsAllAndReaps(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	readyPath := t.TempDir() + "/multi-ready"
	manager := GetManager(lspManagerTestConfig("two-eof", readyPath))
	t.Cleanup(func() { _ = manager.Close() })

	errors := make(chan error, 2)
	for id := 1; id <= 2; id++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := manager.ProxyRequest(ctx, "go", fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"method":"test"}`, id))
			errors <- err
		}()
	}
	pid := waitForHelperPID(t, readyPath)
	for range 2 {
		select {
		case err := <-errors:
			if err == nil || err == context.DeadlineExceeded {
				t.Fatalf("pending request error = %v, want immediate transport failure", err)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("pending request remained blocked after server EOF")
		}
	}
	waitForProcessExit(t, pid)
}

func TestManager_GIVENCallerCancelsAndImmediatelyReusesID_WHENLateResponseArrives_THENNewRequestGetsOnlyItsResponse(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	readyPath := t.TempDir() + "/cancel-ready"
	manager := GetManager(lspManagerTestConfig("cancel-then-reply", readyPath))
	t.Cleanup(func() { _ = manager.Close() })

	requestCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := manager.ProxyRequest(requestCtx, "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"first"}`))
	if err != context.DeadlineExceeded {
		t.Fatalf("cancelled ProxyRequest() error = %v, want caller deadline", err)
	}
	pid := waitForHelperPID(t, readyPath)

	type requestResult struct {
		payload []byte
		err     error
	}
	secondResult := make(chan requestResult, 1)
	go func() {
		response, err := manager.ProxyRequest(context.Background(), "go", []byte(`{"jsonrpc":"2.0","id":1,"method":"second"}`))
		secondResult <- requestResult{payload: response, err: err}
	}()
	if err := os.WriteFile(readyPath+"-release", nil, 0o600); err != nil {
		t.Fatalf("release late response: %v", err)
	}
	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("ProxyRequest() after cancellation error = %v", result.err)
		}
		if !strings.Contains(string(result.payload), `"result":"after-cancel"`) {
			t.Fatalf("response after cancellation = %s", result.payload)
		}
	case <-time.After(time.Second):
		t.Fatal("reused-ID request did not receive its correlated response")
	}
	waitForProcessExit(t, pid)
}

func TestManager_GIVENServerNeverReads_WHENPipeWriteBlocks_THENDeadlineTerminatesAndReaps(t *testing.T) {
	t.Setenv("KTML_LSP_HELPER", "1")
	readyPath := t.TempDir() + "/no-read-ready"
	manager := GetManager(lspManagerTestConfig("no-read", readyPath))
	manager.requestTimeout = 50 * time.Millisecond
	t.Cleanup(func() { _ = manager.Close() })
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"test","padding":"` + strings.Repeat("x", 8<<20) + `"}`)

	result := make(chan error, 1)
	go func() {
		_, err := manager.ProxyRequest(context.Background(), "go", payload)
		result <- err
	}()
	pid := waitForHelperPID(t, readyPath)
	select {
	case err := <-result:
		if err != context.DeadlineExceeded {
			t.Fatalf("ProxyRequest() error = %v, want write deadline", err)
		}
	case <-time.After(time.Second):
		_ = manager.Close()
		waitForProcessExit(t, pid)
		t.Fatal("blocked LSP pipe write ignored the proxy deadline")
	}
	waitForProcessExit(t, pid)
}

func TestLSPManagerHelperProcess(t *testing.T) {
	if os.Getenv("KTML_LSP_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := 0
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == 0 || len(args) < separator+3 {
		os.Exit(2)
	}
	mode, value := args[separator+1], args[separator+2]
	reader := bufio.NewReader(os.Stdin)
	if mode == "no-read" {
		_ = os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	}
	payload, err := readLSPManagerTestFrame(reader)
	if err != nil {
		os.Exit(3)
	}

	switch mode {
	case "silent":
		if err := os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(4)
		}
		_, _ = io.Copy(io.Discard, reader)
	case "reply":
		writeLSPManagerTestResponse(payload, fmt.Sprintf(`"result":%q`, value))
	case "error":
		writeLSPManagerTestResponse(payload, fmt.Sprintf(`"error":{"code":-32000,"message":%q}`, value))
	case "env-reply":
		writeLSPManagerTestResponse(payload, fmt.Sprintf(`"result":%q`, os.Getenv(value)))
	case "eof-then-reply", "protocol-then-reply":
		state, err := os.OpenFile(value+"/state", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		first := err == nil
		if first {
			_ = state.Close()
			_ = os.WriteFile(value+"/first-pid", []byte(strconv.Itoa(os.Getpid())), 0o600)
			if mode == "protocol-then-reply" {
				_, _ = fmt.Fprint(os.Stdout, "Content-Length: invalid\r\n\r\n")
				_, _ = io.Copy(io.Discard, reader)
			}
		} else {
			_ = os.WriteFile(value+"/second-pid", []byte(strconv.Itoa(os.Getpid())), 0o600)
			writeLSPManagerTestResponse(payload, `"result":"restarted"`)
		}
	case "two-eof":
		if _, err := readLSPManagerTestFrame(reader); err != nil {
			os.Exit(7)
		}
		_ = os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600)
	case "cancel-then-reply":
		_ = os.WriteFile(value, []byte(strconv.Itoa(os.Getpid())), 0o600)
		for {
			if _, err := os.Stat(value + "-release"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		writeLSPManagerTestResponse(payload, `"result":"cancelled"`)
		second, err := readLSPManagerTestFrame(reader)
		if err != nil {
			os.Exit(8)
		}
		writeLSPManagerTestResponse(second, `"result":"after-cancel"`)
	default:
		os.Exit(6)
	}
	os.Exit(0)
}

func writeLSPManagerTestResponse(requestPayload []byte, body string) {
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(requestPayload, &request) != nil {
		os.Exit(5)
	}
	response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,%s}`, request.ID, body)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response)
}

func lspManagerTestConfig(mode, value string) *config.Config {
	cfg := config.Default()
	cfg.LSP["go"] = config.LSP{Command: []string{
		os.Args[0], "-test.run=^TestLSPManagerHelperProcess$", "--", mode, value,
	}}
	return cfg
}

func readLSPManagerTestFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			if err != nil {
				return nil, err
			}
		}
	}
	payload := make([]byte, contentLength)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func waitForHelperPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(contents))
			if parseErr == nil && pid > 0 {
				return pid
			}
			if parseErr != nil {
				err = parseErr
			} else {
				err = fmt.Errorf("non-positive helper PID %d", pid)
			}
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper did not report a valid PID at %s: %v", path, lastErr)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper process %d remains after manager replacement", pid)
}
