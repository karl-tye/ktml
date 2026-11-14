package lsp

import (
	"encoding/json"
	"strconv"
	"sync"
)

const (
	semanticTokensFull  = "textDocument/semanticTokens/full"
	semanticTokensDelta = "textDocument/semanticTokens/full/delta"
)

// semanticTokensCache gives each full semantic tokens result an ID and keeps
// the last data sent for each document, so a delta request is answered with
// the edit from that data to the current one. Responders only compute full
// results.
type semanticTokensCache struct {
	mu    sync.Mutex
	next  int
	byURI map[string]sentTokens
}

type sentTokens struct {
	id   string
	data []json.Number
}

func newSemanticTokensCache() *semanticTokensCache {
	return &semanticTokensCache{byURI: make(map[string]sentTokens)}
}

// semanticTokensRequest returns the method the responders answer for
// method, and, for a delta request, the document and the result it is
// relative to.
func semanticTokensRequest(method string, params json.RawMessage) (responderMethod, uri, previous string) {
	if method != semanticTokensFull && method != semanticTokensDelta {
		return method, "", ""
	}
	var request struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		PreviousResultID string `json:"previousResultId"`
	}
	_ = json.Unmarshal(params, &request)
	return semanticTokensFull, request.TextDocument.URI, request.PreviousResultID
}

// answer rewrites the responders' full result for method: it gains a result
// ID, and a delta request whose previous result is known gets the edit from
// it instead.
func (c *semanticTokensCache) answer(method, uri, previous string, response []byte) []byte {
	if uri == "" || method != semanticTokensFull && method != semanticTokensDelta {
		return response
	}
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Result *struct {
			Data []json.Number `json:"data"`
		} `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Result == nil {
		return response
	}
	data := envelope.Result.Data
	if data == nil {
		data = []json.Number{}
	}
	c.mu.Lock()
	c.next++
	id := strconv.Itoa(c.next)
	sent, known := c.byURI[uri]
	c.byURI[uri] = sentTokens{id: id, data: data}
	c.mu.Unlock()
	if method == semanticTokensDelta && known && sent.id == previous {
		return rpcResult(envelope.ID, map[string]any{"resultId": id, "edits": tokenEdits(sent.data, data)})
	}
	return rpcResult(envelope.ID, map[string]any{"resultId": id, "data": data})
}

func (c *semanticTokensCache) forget(uri string) {
	c.mu.Lock()
	delete(c.byURI, uri)
	c.mu.Unlock()
}

// tokenEdits is the single edit that turns before into after: what lies
// between their common prefix and suffix. Equal data needs none.
func tokenEdits(before, after []json.Number) []map[string]any {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	if prefix == len(before) && prefix == len(after) {
		return []map[string]any{}
	}
	return []map[string]any{{"start": prefix, "deleteCount": len(before) - prefix - suffix, "data": after[prefix : len(after)-suffix]}}
}
