package gohtml

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/htmlcheck"
)

// htmlOptions are the HTML analyzer's project options, from the host
// settings ktml_configure receives.
var (
	htmlOptions   htmlcheck.Options
	htmlOptionsMu sync.RWMutex
)

// Configure reads the project settings the host passes under "_ktml":
// html.extension_attributes from ktml.config.json, which the host has
// validated. The plugin's own settings are not used by the LSP.
func Configure(data []byte) error {
	var settings struct {
		KTML struct {
			HTML struct {
				ExtensionAttributes []string `json:"extension_attributes"`
			} `json:"html"`
		} `json:"_ktml"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("gohtml settings: %w", err)
	}
	htmlOptionsMu.Lock()
	htmlOptions = htmlcheck.Options{ExtensionAttributes: settings.KTML.HTML.ExtensionAttributes}
	htmlOptionsMu.Unlock()
	return nil
}

// analyzeHTML runs the HTML analyzer with the project's options.
func analyzeHTML(snapshot *compiler.Snapshot, path string) []htmlcheck.Diagnostic {
	htmlOptionsMu.RLock()
	options := htmlOptions
	htmlOptionsMu.RUnlock()
	return htmlcheck.AnalyzeWith(snapshot, path, options)
}

// documentDiagnostics returns the compiler diagnostics of a KTML document
// and the HTML and accessibility analyzer's.
func documentDiagnostics(snapshot *compiler.Snapshot, path string) []compiler.Diagnostic {
	diagnostics := snapshot.Diagnostics(path)
	if !strings.HasSuffix(path, ".ktml") {
		return diagnostics
	}
	for _, finding := range analyzeHTML(snapshot, path) {
		severity := compiler.SeverityWarning
		if finding.Severity == htmlcheck.Error {
			severity = compiler.SeverityError
		}
		diagnostics = append(diagnostics, compiler.Diagnostic{File: path, Code: finding.Code, Message: finding.Message, Severity: severity, Start: finding.Start, End: finding.End})
	}
	return diagnostics
}

// htmlCompletionItems returns HTML completions as LSP items.
func htmlCompletionItems(snapshot *compiler.Snapshot, path string, content []byte, cursor int) ([]map[string]any, htmlcheck.CursorKind) {
	component := func(name string) bool {
		if snapshot == nil {
			return name != "" && name[0] >= 'A' && name[0] <= 'Z'
		}
		_, ok := snapshot.Resolve(path, name)
		return ok
	}
	items := make([]map[string]any, 0)
	for _, item := range htmlcheck.Complete(content, cursor, component) {
		entry := map[string]any{
			"label":    item.Label,
			"kind":     item.Kind,
			"detail":   item.Detail,
			"textEdit": map[string]any{"range": rangeAt(content, item.Start, item.End), "newText": item.Text},
		}
		if item.Documentation != "" {
			entry["documentation"] = map[string]any{"kind": "markdown", "value": item.Documentation}
		}
		items = append(items, entry)
	}
	return items, htmlcheck.CursorAt(content, cursor).Kind
}

// htmlCodeActions returns the analyzer's fixes whose diagnostics overlap
// [start, end].
func htmlCodeActions(snapshot *compiler.Snapshot, path, uri string, start, end int) []map[string]any {
	source := snapshot.Source(path)
	if source == nil || !strings.HasSuffix(path, ".ktml") {
		return nil
	}
	var actions []map[string]any
	for _, finding := range analyzeHTML(snapshot, path) {
		if finding.End < start || finding.Start > end {
			continue
		}
		for _, fix := range finding.Fixes {
			edits := make([]map[string]any, 0, len(fix.Edits))
			for _, edit := range fix.Edits {
				edits = append(edits, map[string]any{"range": rangeAt(source, edit.Start, edit.End), "newText": edit.Text})
			}
			actions = append(actions, map[string]any{
				"title":       fix.Title,
				"kind":        "quickfix",
				"isPreferred": fix.Preferred,
				"diagnostics": []map[string]any{{"range": rangeAt(source, finding.Start, finding.End), "code": finding.Code, "message": finding.Message, "source": "ktml"}},
				"edit":        map[string]any{"changes": map[string]any{uri: edits}},
			})
		}
	}
	return actions
}
