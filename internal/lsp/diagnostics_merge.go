package lsp

import (
	"encoding/json"
)

const publishDiagnosticsMethod = "textDocument/publishDiagnostics"

// diagnosticsMerger keeps each plugin's last published diagnostics per URI.
// publishDiagnostics replaces everything a client shows for a URI, so the
// host must publish one union per URI: forwarding each plugin's notification
// separately lets the last plugin erase the others' diagnostics.
type diagnosticsMerger struct {
	byURI map[string]map[int][]json.RawMessage
}

func newDiagnosticsMerger() *diagnosticsMerger {
	return &diagnosticsMerger{byURI: make(map[string]map[int][]json.RawMessage)}
}

// notifications takes each plugin's answer to one client notification,
// indexed by plugin, and returns what the host forwards: the other
// notifications unchanged, then one merged publishDiagnostics per URI that
// any plugin published, in first-published order.
func (merger *diagnosticsMerger) notifications(responses [][]byte) [][]byte {
	var forwarded [][]byte
	var touched []string
	seen := make(map[string]bool)
	replaced := make(map[string]map[int]bool)
	for plugin, response := range responses {
		if len(response) == 0 {
			continue
		}
		for _, notification := range pluginNotifications(response) {
			uri, diagnostics, ok := decodePublishDiagnostics(notification)
			if !ok {
				forwarded = append(forwarded, notification)
				continue
			}
			plugins := merger.byURI[uri]
			if plugins == nil {
				plugins = make(map[int][]json.RawMessage)
				merger.byURI[uri] = plugins
			}
			if !seen[uri] {
				seen[uri] = true
				touched = append(touched, uri)
				replaced[uri] = make(map[int]bool)
			}
			if !replaced[uri][plugin] {
				// A plugin's first publish in an answer replaces its stored set;
				// further publishes in the same answer (a batch) add to it.
				replaced[uri][plugin] = true
				plugins[plugin] = nil
			}
			plugins[plugin] = append(plugins[plugin], diagnostics...)
			if len(plugins[plugin]) == 0 {
				delete(plugins, plugin)
			}
		}
	}
	for _, uri := range touched {
		forwarded = append(forwarded, merger.publish(uri))
	}
	return forwarded
}

func (merger *diagnosticsMerger) publish(uri string) []byte {
	plugins := merger.byURI[uri]
	diagnostics := make([]json.RawMessage, 0)
	maxPlugin := -1
	for plugin := range plugins {
		maxPlugin = max(maxPlugin, plugin)
	}
	for plugin := 0; plugin <= maxPlugin; plugin++ {
		diagnostics = append(diagnostics, plugins[plugin]...)
	}
	if len(plugins) == 0 {
		delete(merger.byURI, uri)
	}
	notification, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  publishDiagnosticsMethod,
		"params":  map[string]any{"uri": uri, "diagnostics": diagnostics},
	})
	return notification
}

func decodePublishDiagnostics(notification []byte) (string, []json.RawMessage, bool) {
	var message struct {
		Method string `json:"method"`
		Params struct {
			URI         string            `json:"uri"`
			Diagnostics []json.RawMessage `json:"diagnostics"`
		} `json:"params"`
	}
	if json.Unmarshal(notification, &message) != nil || message.Method != publishDiagnosticsMethod || message.Params.URI == "" {
		return "", nil, false
	}
	return message.Params.URI, message.Params.Diagnostics, true
}
