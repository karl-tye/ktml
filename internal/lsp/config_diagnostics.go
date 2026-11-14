package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/configdiag"
	"github.com/karl-tye/ktml/internal/textposition"
)

// ConfigReport holds the errors of ktml.config.json found when the server
// started: core configuration errors, and settings errors of plugins that
// did not load. The server publishes them on the file once the client is
// initialized, and checks the file again whenever it changes. Plugins load
// only at start, so a changed file also says that a restart applies it.
type ConfigReport struct {
	URI         string
	Source      []byte
	Diagnostics []configdiag.Diagnostic
}

// notification returns the publishDiagnostics notification of the report.
func (report ConfigReport) notification() []byte {
	type position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	}
	type diagnostic struct {
		Range struct {
			Start position `json:"start"`
			End   position `json:"end"`
		} `json:"range"`
		Severity int    `json:"severity"`
		Code     string `json:"code"`
		Source   string `json:"source"`
		Message  string `json:"message"`
	}
	diagnostics := make([]diagnostic, 0, len(report.Diagnostics))
	for _, entry := range report.Diagnostics {
		var converted diagnostic
		converted.Range.Start.Line, converted.Range.Start.Character = textposition.Position(report.Source, entry.Start)
		converted.Range.End.Line, converted.Range.End.Character = textposition.Position(report.Source, entry.End)
		severity := entry.Severity
		if severity == 0 {
			severity = 1
		}
		converted.Severity, converted.Code, converted.Source, converted.Message = severity, entry.Code, "ktml", entry.Message
		diagnostics = append(diagnostics, converted)
	}
	notification, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  publishDiagnosticsMethod,
		"params":  map[string]any{"uri": report.URI, "diagnostics": diagnostics},
	})
	return notification
}

// recheck returns the report for the file's new contents: its core
// configuration errors, and a note that plugins and the Tailwind adapter
// keep the settings they started with until the server restarts. The
// contents the server started with keep their startup report.
func (report ConfigReport) recheck(source []byte) ConfigReport {
	next := ConfigReport{URI: report.URI, Source: source}
	if bytes.Equal(source, report.Source) {
		next.Diagnostics = report.Diagnostics
		return next
	}
	if _, err := config.Parse(source); err != nil {
		next.Diagnostics = configdiag.Config(source, err)
	}
	next.Diagnostics = append(next.Diagnostics, configdiag.Diagnostic{
		Start: 0, End: min(1, len(source)), Code: "config-restart", Severity: 3,
		Message: "ktml.config.json changed since the language server started; restart it to load plugins and the Tailwind adapter with these settings",
	})
	return next
}

// configChange returns the new contents of the config file when a client
// message changes it: an open, change, or save of the document, or a
// watched-file event, which is read from disk.
func configChange(method string, params json.RawMessage, uri string) ([]byte, bool) {
	var message struct {
		TextDocument struct {
			URI  string  `json:"uri"`
			Text *string `json:"text"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
		Text    *string `json:"text"`
		Changes []struct {
			URI string `json:"uri"`
		} `json:"changes"`
	}
	if json.Unmarshal(params, &message) != nil {
		return nil, false
	}
	switch method {
	case "textDocument/didOpen":
		if message.TextDocument.URI == uri && message.TextDocument.Text != nil {
			return []byte(*message.TextDocument.Text), true
		}
	case "textDocument/didChange":
		if message.TextDocument.URI == uri && len(message.ContentChanges) > 0 {
			return []byte(message.ContentChanges[len(message.ContentChanges)-1].Text), true
		}
	case "textDocument/didSave":
		if message.TextDocument.URI == uri {
			if message.Text != nil {
				return []byte(*message.Text), true
			}
			return readConfigURI(uri)
		}
	case "workspace/didChangeWatchedFiles":
		for _, change := range message.Changes {
			if change.URI == uri {
				return readConfigURI(uri)
			}
		}
	}
	return nil, false
}

func readConfigURI(uri string) ([]byte, bool) {
	source, err := os.ReadFile(strings.TrimPrefix(uri, "file://"))
	if err != nil {
		// A deleted file has no errors to show.
		return []byte{}, true
	}
	return source, true
}
