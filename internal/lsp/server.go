package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/configdiag"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/karl-tye/ktml/internal/wasm"
)

func StartServer(ctx context.Context, plugins []*wasm.Plugin, config ConfigReport, adapters ...Adapter) {
	ServeWithAdapters(ctx, os.Stdin, os.Stdout, plugins, config, adapters)
}

// Serve processes LSP messages until input closes or an exit notification is
// received. Keeping transport streams explicit makes protocol behavior testable.
func Serve(ctx context.Context, input io.Reader, output io.Writer, plugins []*wasm.Plugin) {
	ServeWithConfig(ctx, input, output, plugins, ConfigReport{})
}

// ServeWithConfig is Serve that also publishes the errors of
// ktml.config.json once the client is initialized.
func ServeWithConfig(ctx context.Context, input io.Reader, output io.Writer, plugins []*wasm.Plugin, config ConfigReport) {
	ServeWithAdapters(ctx, input, output, plugins, config, nil)
}

// lspResponder answers client messages: a wasm plugin or a native adapter.
type lspResponder interface {
	ExecuteLSP(ctx context.Context, method string, payload []byte) ([]byte, error)
}

// adapterNotification is a notification an adapter published on its own;
// slot is the adapter's index among the responders.
type adapterNotification struct {
	slot    int
	payload []byte
}

// ServeWithAdapters is ServeWithConfig that also serves native adapters
// beside the plugins. Adapters answer every message after the plugins, and
// their diagnostics merge with the plugins' per URI.
func ServeWithAdapters(ctx context.Context, input io.Reader, output io.Writer, plugins []*wasm.Plugin, config ConfigReport, adapters []Adapter) {
	responders := make([]lspResponder, 0, len(plugins)+len(adapters))
	for _, plugin := range plugins {
		responders = append(responders, plugin)
	}
	adapterNotifications := make(chan adapterNotification)
	adapterStop := make(chan struct{})
	defer close(adapterStop)
	for _, adapter := range adapters {
		slot := len(responders)
		responders = append(responders, adapter)
		go func(adapter Adapter) {
			for {
				select {
				case payload := <-adapter.Published():
					select {
					case adapterNotifications <- adapterNotification{slot: slot, payload: payload}:
					case <-adapterStop:
						return
					}
				case <-adapterStop:
					return
				}
			}
		}(adapter)
	}
	reader := bufio.NewReader(input)
	shutdown := false
	diagnostics := newDiagnosticsMerger()
	// The config file's diagnostics merge with the plugins' as one more
	// slot, so a plugin's publish for the file cannot erase them.
	configSlot := len(responders)
	publishConfig := func(report ConfigReport) {
		answers := make([][]byte, configSlot+1)
		answers[configSlot] = report.notification()
		for _, notification := range diagnostics.notifications(answers) {
			writeResponse(output, notification)
		}
	}
	watchFiles := false
	jobs := newBackgroundJobs(ctx, plugins)
	defer jobs.close()
	index := newSymbolIndexService(ctx, indexProviders(plugins, adapters))
	defer index.close()
	tokens := newSemanticTokensCache()

	// Frames are read on their own goroutine so that background job results
	// are delivered while the client is quiet.
	frames := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(frames)
		for {
			payload, ok := readFrame(reader)
			if !ok {
				return
			}
			select {
			case frames <- payload:
			case <-stop:
				return
			}
		}
	}()
	jobs.pollAll()

	for {
		var payload []byte
		select {
		case done := <-jobs.done:
			for _, notification := range diagnostics.notifications(jobs.deliver(done)) {
				writeResponse(output, notification)
			}
			// A job, such as a route scan, can change what the plugin
			// contributes to the symbol index.
			if plugin := plugins[done.plugin]; plugin.Manifest().Has(wasm.CapabilitySymbolIndex) {
				index.enqueue(symbolindex.Request{Workspace: true}, plugin.Name())
			}
			continue
		case published := <-adapterNotifications:
			answers := make([][]byte, published.slot+1)
			answers[published.slot] = published.payload
			for _, notification := range diagnostics.notifications(answers) {
				writeResponse(output, notification)
			}
			continue
		case frame, ok := <-frames:
			if !ok {
				return
			}
			payload = frame
		}

		message := classifyRPCMessage(payload)
		if message.err != nil {
			if message.parseErr {
				writeResponse(output, rpcError(json.RawMessage("null"), -32700, "parse error"))
			} else {
				writeResponse(output, rpcError(message.errorID(), -32600, "invalid request"))
			}
			continue
		}
		if message.kind == rpcResponse {
			continue
		}
		if message.kind == rpcNotification {
			// Built-in request methods have no notification semantics; exit is transport-local.
			if message.method == "exit" {
				return
			}
			if message.method == "initialize" || message.method == "shutdown" {
				continue
			}
		}
		if message.kind != rpcRequest && message.kind != rpcNotification {
			writeResponse(output, rpcError(message.errorID(), -32600, "invalid request"))
			continue
		}

		if message.method == "initialize" {
			watchFiles = clientWatchesFiles(message.object.members["params"])
			writeResponse(output, initializeResponse(message.id))
			continue
		}
		if message.method == "initialized" && config.URI != "" && len(config.Diagnostics) > 0 {
			publishConfig(config)
		}
		if config.URI != "" && message.kind == rpcNotification {
			if source, changed := configChange(message.method, message.object.members["params"], config.URI); changed {
				publishConfig(config.recheck(source))
			}
		}
		if message.method == "initialized" {
			index.enqueue(symbolindex.Request{Workspace: true})
		}
		if message.method == "initialized" && watchFiles {
			// Plugins such as the HTMX route index refresh from the Go
			// files the editor reports changed, and the config file is
			// checked again when it changes on disk.
			writeResponse(output, watchGoFilesRequest())
		}

		if message.method == "shutdown" {
			shutdown = true
			writeResponse(output, rpcResult(message.id, nil))
			continue
		}

		if message.method == "exit" {
			return
		}

		if shutdown {
			if message.kind == rpcRequest {
				writeResponse(output, rpcError(message.id, -32600, "server has shut down"))
			}
			continue
		}

		pluginPayload := extractParams(message)
		if message.kind == rpcRequest && navigationMethods[message.method] {
			// Indexing a change also brings the plugins' projects up to
			// date with the disk, so navigation waits for it.
			index.wait()
		}

		// Responders compute full semantic tokens; the host answers a delta
		// request from the full result.
		method, tokensURI, previousTokens := semanticTokensRequest(message.method, message.object.members["params"])

		var wg sync.WaitGroup
		responses := make([][]byte, len(responders))

		for i, p := range responders {
			wg.Add(1)
			go func(index int, responder lspResponder) {
				defer wg.Done()
				resp, err := responder.ExecuteLSP(ctx, method, pluginPayload)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error executing LSP on plugin: %v\n", err)
					return
				}
				if len(resp) > 0 {
					responses[index] = resp
				}
			}(i, p)
		}

		wg.Wait()
		if message.kind == rpcNotification {
			for _, notification := range diagnostics.notifications(responses) {
				writeResponse(output, notification)
			}
			if uris := changedURIs(message.method, message.object.members["params"]); len(uris) > 0 {
				index.enqueue(symbolindex.Request{URIs: uris})
				if message.method == "textDocument/didClose" {
					tokens.forget(uris[0])
				}
			}
			jobs.pollAll()
			continue
		}
		if answer := index.answer(message.method, message.id, message.object.members["params"]); answer != nil {
			responses = append(responses, answer)
		}
		responses = nonEmptyResponses(responses)
		responses = normalizePluginResponses(message.id, responses)

		if len(responses) == 1 {
			writeResponse(output, tokens.answer(message.method, tokensURI, previousTokens, responses[0]))
		} else if len(responses) > 1 {
			merged := mergeResponsesFor(method, responses)
			if len(merged) > 0 {
				writeResponse(output, tokens.answer(message.method, tokensURI, previousTokens, merged))
			}
		} else if message.kind == rpcRequest {
			writeResponse(output, rpcError(message.id, -32601, "method not found"))
		}
	}
}

// clientWatchesFiles reports whether the client accepts dynamic
// registration of watched files.
func clientWatchesFiles(params json.RawMessage) bool {
	var initialize struct {
		Capabilities struct {
			Workspace struct {
				DidChangeWatchedFiles struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
				} `json:"didChangeWatchedFiles"`
			} `json:"workspace"`
		} `json:"capabilities"`
	}
	return json.Unmarshal(params, &initialize) == nil && initialize.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration
}

func watchGoFilesRequest() []byte {
	request, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "ktml-watch-go-files",
		"method":  "client/registerCapability",
		"params": map[string]any{
			"registrations": []map[string]any{{
				"id":              "ktml-go-files",
				"method":          "workspace/didChangeWatchedFiles",
				"registerOptions": map[string]any{"watchers": []map[string]any{{"globPattern": "**/*.go"}, {"globPattern": "**/*.ktml"}, {"globPattern": "**/*.ktmlschema"}, {"globPattern": "**/" + configdiag.FileName}}},
			}},
		},
	})
	return request
}

// readFrame reads one Content-Length framed message; ok is false when the
// stream ends or cannot be resynchronised.
func readFrame(reader *bufio.Reader) (payload []byte, ok bool) {
	for {
		var contentLength int
		seenContentLength := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "Error reading header: %v\n", err)
				}
				return nil, false
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.EqualFold(strings.TrimSpace(strings.SplitN(line, ":", 2)[0]), "Content-Length") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cl, err := strconv.Atoi(strings.TrimSpace(parts[1]))
					if err == nil {
						if seenContentLength && cl != contentLength {
							// Either length could be the real frame boundary, so the stream cannot be resynchronized.
							fmt.Fprintf(os.Stderr, "Conflicting Content-Length headers: %d and %d\n", contentLength, cl)
							return nil, false
						}
						contentLength = cl
						seenContentLength = true
					}
				}
			}
		}

		if contentLength == 0 {
			continue
		}
		if contentLength < 0 || contentLength > maxLSPMessageSize {
			fmt.Fprintf(os.Stderr, "Invalid Content-Length: %d\n", contentLength)
			return nil, false
		}

		payload := make([]byte, contentLength)
		_, err := io.ReadFull(reader, payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading payload: %v\n", err)
			return nil, false
		}
		return payload, true

	}
}

func (message rpcMessage) errorID() json.RawMessage {
	nullID := json.RawMessage("null")
	if message.object == nil || message.object.duplicates["id"] || message.ambiguous {
		return nullID
	}
	rawID, ok := message.object.members["id"]
	if !ok {
		return nullID
	}
	if _, err := normalizeRPCID(rawID); err != nil {
		return nullID
	}
	return rawID
}

// pluginNotifications returns the valid notifications in a plugin's answer to
// a notification. A plugin may answer with one notification or a JSON array of
// them, for example diagnostics for a document and its open dependents.
func pluginNotifications(response []byte) [][]byte {
	candidates := [][]byte{response}
	if trimmed := bytes.TrimSpace(response); len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(trimmed, &batch) != nil {
			return nil
		}
		candidates = candidates[:0]
		for _, member := range batch {
			candidates = append(candidates, member)
		}
	}
	var notifications [][]byte
	for _, candidate := range candidates {
		message := classifyRPCMessage(candidate)
		if message.err == nil && message.kind == rpcNotification {
			notifications = append(notifications, candidate)
		}
	}
	return notifications
}

func nonEmptyResponses(responses [][]byte) [][]byte {
	result := responses[:0]
	for _, response := range responses {
		if len(response) > 0 {
			result = append(result, response)
		}
	}
	return result
}

func initializeResponse(id json.RawMessage) []byte {
	capabilities := map[string]any{
		"textDocumentSync": map[string]any{
			"openClose": true,
			"change":    1,
		},
		"definitionProvider":              true,
		"callHierarchyProvider":           true,
		"referencesProvider":              true,
		"documentHighlightProvider":       true,
		"documentSymbolProvider":          true,
		"foldingRangeProvider":            true,
		"codeLensProvider":                map[string]any{"resolveProvider": false},
		"hoverProvider":                   true,
		"documentFormattingProvider":      true,
		"documentRangeFormattingProvider": true,
		"documentOnTypeFormattingProvider": map[string]any{
			"firstTriggerCharacter": "}",
			"moreTriggerCharacter":  []string{"\n"},
		},
		"completionProvider": map[string]any{
			"resolveProvider": false,
		},
		"signatureHelpProvider": map[string]any{
			"triggerCharacters":   []string{" ", "=", "("},
			"retriggerCharacters": []string{"\""},
		},
		"workspaceSymbolProvider": true,
		"codeActionProvider":      true,
		"renameProvider": map[string]any{
			"prepareProvider": true,
		},
		"semanticTokensProvider": map[string]any{
			"legend": map[string]any{
				"tokenTypes":     compiler.SemanticTokenTypes,
				"tokenModifiers": compiler.SemanticTokenModifiers,
			},
			"full":  map[string]any{"delta": true},
			"range": true,
		},
	}

	serverInfo := map[string]any{
		"name":    "ktml",
		"version": "1.0.0",
	}

	result := map[string]any{
		"capabilities": capabilities,
		"serverInfo":   serverInfo,
	}

	resp, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return resp
}

func rpcResult(id json.RawMessage, result any) []byte {
	response, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return response
}

func rpcError(id json.RawMessage, code int, message string) []byte {
	response, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
	return response
}

func extractParams(message rpcMessage) []byte {
	params := message.object.members["params"]
	if len(params) == 0 {
		if len(message.id) == 0 {
			return nil
		}
		return []byte(fmt.Sprintf(`{"id":%s}`, message.id))
	}
	if len(message.id) == 0 || !isJSONObjectOrArray(params) || bytes.TrimSpace(params)[0] == '[' {
		return params
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(params, &members) != nil {
		return params
	}
	if _, exists := members["id"]; !exists {
		members["id"] = append(json.RawMessage(nil), message.id...)
	}
	result, err := json.Marshal(members)
	if err != nil {
		return params
	}
	return result
}

func normalizePluginResponses(id json.RawMessage, responses [][]byte) [][]byte {
	normalized := responses[:0]
	for _, response := range responses {
		message := classifyRPCMessage(response)
		if message.err != nil || message.kind != rpcResponse {
			continue
		}
		rewritten, err := rewriteRPCMembers(message.object, map[string]json.RawMessage{"id": id})
		if err == nil {
			normalized = append(normalized, rewritten)
		}
	}
	return normalized
}

func mergeResponses(responses [][]byte) []byte {
	return mergeResponsesFor("", responses)
}

func mergeResponsesFor(method string, responses [][]byte) []byte {
	switch method {
	case "textDocument/completion":
		return mergeCompletionResponses(responses)
	case "textDocument/semanticTokens/full", "textDocument/semanticTokens/range":
		return mergeExclusiveResponses(responses)
	case "textDocument/hover":
		return mergeHoverResponses(responses)
	case "textDocument/definition", "textDocument/references", "textDocument/documentHighlight":
		return mergeLocationResponses(responses, method == "textDocument/definition")
	}
	return mergeGenericResponses(responses)
}

// mergeLocationResponses concatenates location or highlight results,
// without repeats. A responder may answer with one Location or a list of
// them, so when several answer, each answer is a list before they are
// joined. A single answer is kept as it is.
//
// A definition may be a single Location: when every answer names the same
// one and some responder gave it alone, the merge keeps it alone too.
func mergeLocationResponses(responses [][]byte, single bool) []byte {
	results, rpcErrors := validPluginResponses(responses, nil)
	if len(results) == 0 {
		return deterministicPluginError(rpcErrors)
	}
	var answered []validPluginResponse
	for _, response := range results {
		if response.result != nil {
			answered = append(answered, response)
		}
	}
	if len(answered) == 1 {
		return rpcResult(results[0].message.id, answered[0].result)
	}
	// The symbol index and an analyzer may both find a location.
	var locations []any
	var alone any
	seen := make(map[string]bool)
	add := func(location any) {
		key, err := json.Marshal(location)
		if err == nil && seen[string(key)] {
			return
		}
		seen[string(key)] = true
		locations = append(locations, location)
	}
	for _, response := range answered {
		switch value := response.result.(type) {
		case []any:
			for _, location := range value {
				add(location)
			}
		case map[string]any:
			add(value)
			alone = value
		}
	}
	if single && alone != nil && len(locations) == 1 {
		return rpcResult(results[0].message.id, alone)
	}
	if locations == nil {
		return rpcResult(results[0].message.id, nil)
	}
	return rpcResult(results[0].message.id, locations)
}

// mergeExclusiveResponses answers with the first non-null plugin result.
// Semantic token data is relative-encoded per document, so concatenating two
// plugins' arrays would corrupt both.
func mergeExclusiveResponses(responses [][]byte) []byte {
	results, rpcErrors := validPluginResponses(responses, nil)
	if len(results) == 0 {
		return deterministicPluginError(rpcErrors)
	}
	for _, response := range results {
		if response.result != nil {
			return rpcResult(results[0].message.id, response.result)
		}
	}
	return rpcResult(results[0].message.id, nil)
}

func mergeGenericResponses(responses [][]byte) []byte {
	results, rpcErrors := validPluginResponses(responses, nil)
	if len(results) == 0 {
		return deterministicPluginError(rpcErrors)
	}
	merged := results[0].result
	for _, response := range results[1:] {
		merged = mergeValues(merged, response.result)
	}
	return rpcResult(results[0].message.id, merged)
}

func mergeCompletionResponses(responses [][]byte) []byte {
	results, rpcErrors := validPluginResponses(responses, validCompletionResult)
	if len(results) == 0 && len(rpcErrors) == 0 && len(responses) > 0 {
		// Every plugin answered with an invalid completion result; the request still needs a response.
		id := classifyRPCMessage(responses[0]).id
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		return rpcError(id, -32603, "invalid plugin completion result")
	}
	if len(results) == 0 {
		return deterministicPluginError(rpcErrors)
	}
	var items []any
	incomplete := false
	for _, response := range results {
		switch value := response.result.(type) {
		case []any:
			items = append(items, value...)
		case map[string]any:
			valueIncomplete, _ := value["isIncomplete"].(bool)
			incomplete = incomplete || valueIncomplete
			valueItems, _ := value["items"].([]any)
			items = append(items, valueItems...)
		}
	}
	return rpcResult(results[0].message.id, map[string]any{"isIncomplete": incomplete, "items": items})
}

type validPluginResponse struct {
	payload []byte
	message rpcMessage
	result  any
}

func validPluginResponses(responses [][]byte, validateResult func(any) bool) (results, rpcErrors []validPluginResponse) {
	for _, payload := range responses {
		message := classifyRPCMessage(payload)
		if message.err != nil || message.kind != rpcResponse {
			continue
		}
		response := validPluginResponse{payload: payload, message: message}
		if raw, ok := message.object.members["result"]; ok {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&response.result); err != nil || validateResult != nil && !validateResult(response.result) {
				continue
			}
			results = append(results, response)
		} else {
			rpcErrors = append(rpcErrors, response)
		}
	}
	return results, rpcErrors
}

func validCompletionResult(result any) bool {
	switch value := result.(type) {
	case nil, []any:
		return true
	case map[string]any:
		if items, ok := value["items"]; ok {
			if _, ok := items.([]any); !ok {
				return false
			}
		}
		if incomplete, ok := value["isIncomplete"]; ok {
			if _, ok := incomplete.(bool); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func deterministicPluginError(responses []validPluginResponse) []byte {
	if len(responses) == 0 {
		return nil
	}
	selected := responses[0].payload
	for _, response := range responses[1:] {
		if bytes.Compare(response.payload, selected) < 0 {
			selected = response.payload
		}
	}
	return selected
}

func mergeValues(a, b any) any {
	if b == nil {
		return a
	}
	if a == nil {
		return b
	}
	aSlice, aIsSlice := a.([]any)
	bSlice, bIsSlice := b.([]any)
	if aIsSlice && bIsSlice {
		return append(aSlice, bSlice...)
	}

	aMap, aIsMap := a.(map[string]any)
	bMap, bIsMap := b.(map[string]any)
	if aIsMap && bIsMap {
		res := make(map[string]any)
		maps.Copy(res, aMap)
		for k, v := range bMap {
			if existing, ok := res[k]; ok {
				res[k] = mergeValues(existing, v)
			} else {
				res[k] = v
			}
		}
		return res
	}

	return b
}

func writeResponse(output io.Writer, resp []byte) {
	fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(resp))
	_, _ = output.Write(resp)
}
