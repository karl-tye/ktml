// Package control is the `ktml dev` side of the application's development
// control channel (ADR-0004 decision 6): it asks the app to swap its
// interpreter program, reports page sessions' SSE connections, and asks
// which components the page's root instances are.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/karl-tye/ktml/dev/hmr"
	"github.com/karl-tye/ktml/internal/config"
)

// ErrUnavailable means the application serves no control channel: it was not
// built with -tags ktmldev, does not call hmr.Install, or is not running.
var ErrUnavailable = errors.New("the application serves no ktml dev control channel; build it with -tags ktmldev and wrap its handler with hmr.Install")

// DiagnosticError is a diagnostic from the application's interpreter. Its
// last valid program stays active.
type DiagnosticError struct {
	Message string
}

func (e *DiagnosticError) Error() string { return e.Message }

// Client calls the control channel of the application at BaseURL.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// AllowRemote permits a BaseURL outside loopback; the config allows that
	// only with dev.allow_remote_proxy_target and https.
	AllowRemote bool
}

// ErrRemote means the application's BaseURL is outside loopback and the
// client was not allowed to send the control token there.
var ErrRemote = errors.New("the application is not on a loopback host; ktml dev sends its control token only to loopback unless dev.allow_remote_proxy_target is set")

// New returns a client with a bounded request timeout. It never follows a
// redirect, which would carry the token to another host.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: baseURL, Token: token, HTTP: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Swap sends changed sources and returns the rerendered patches.
func (c *Client) Swap(ctx context.Context, request hmr.SwapRequest) (hmr.SwapResponse, error) {
	var response hmr.SwapResponse
	status, body, err := c.post(ctx, "swap", request)
	if err != nil {
		return response, err
	}
	switch status {
	case http.StatusOK:
		if err := json.Unmarshal(body, &response); err != nil {
			return response, fmt.Errorf("decode swap response: %w", err)
		}
		return response, nil
	case http.StatusUnprocessableEntity:
		if err := json.Unmarshal(body, &response); err != nil || response.Diagnostic == "" {
			return response, fmt.Errorf("swap failed: %s", bytes.TrimSpace(body))
		}
		return response, &DiagnosticError{Message: response.Diagnostic}
	case http.StatusNotFound:
		return response, ErrUnavailable
	default:
		return response, fmt.Errorf("swap: status %d: %s", status, bytes.TrimSpace(body))
	}
}

// Session reports that a page session's SSE stream connected or disconnected.
func (c *Client) Session(ctx context.Context, session string, connected bool) error {
	status, body, err := c.post(ctx, "session", hmr.SessionRequest{Session: session, Connected: connected})
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return ErrUnavailable
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("session: status %d: %s", status, bytes.TrimSpace(body))
	}
	return nil
}

// Capture reports the application's story capture.
func (c *Client) Capture(ctx context.Context) (hmr.CaptureStatus, error) {
	var status hmr.CaptureStatus
	code, body, err := c.post(ctx, "capture", struct{}{})
	if err != nil {
		return status, err
	}
	if code == http.StatusNotFound {
		return status, ErrUnavailable
	}
	if code != http.StatusOK {
		return status, fmt.Errorf("capture: status %d: %s", code, bytes.TrimSpace(body))
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return status, fmt.Errorf("decode capture status: %w", err)
	}
	return status, nil
}

// Components names the components of the application's live root
// instances, for the component picker.
func (c *Client) Components(ctx context.Context, instances []string) (map[string]string, error) {
	code, body, err := c.post(ctx, "components", hmr.ComponentsRequest{Instances: instances})
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, ErrUnavailable
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("components: status %d: %s", code, bytes.TrimSpace(body))
	}
	var response hmr.ComponentsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode components: %w", err)
	}
	return response.Components, nil
}

func (c *Client) post(ctx context.Context, route string, value any) (int, []byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+hmr.Prefix+route, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	if !c.AllowRemote && !config.LoopbackHost(request.URL.Hostname()) {
		return 0, nil, ErrRemote
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(hmr.TokenHeader, c.Token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	return response.StatusCode, body, err
}
