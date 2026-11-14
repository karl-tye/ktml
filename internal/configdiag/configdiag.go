// Package configdiag turns ktml.config.json errors into source-ranged
// diagnostics for the LSP: core configuration errors, and plugin settings
// errors that name a JSON path inside plugin.<name>.settings.
package configdiag

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FileName is the configuration file the diagnostics belong to.
const FileName = "ktml.config.json"

// Diagnostic is one configuration error at a byte range of the file.
type Diagnostic struct {
	Start, End int
	Code       string
	Message    string
	// Severity is an LSP severity; zero is an error.
	Severity int
}

// PathError is a configuration error at a JSON path such as
// ["plugin", "htmx", "limits", "memory_mb"].
type PathError struct {
	Path    []string
	Message string
}

func (e *PathError) Error() string {
	return e.Message
}

// FormatPath writes a path as a.b[0].c.
func FormatPath(path []string) string {
	var b strings.Builder
	for _, part := range path {
		if _, err := strconv.Atoi(part); err == nil && b.Len() > 0 {
			b.WriteString("[" + part + "]")
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(part)
	}
	return b.String()
}

// ParsePath splits a.b[0].c into its keys and indices. A key may be "*", as
// in chi.patterns.routes.*.
func ParsePath(path string) []string {
	var parts []string
	for _, field := range strings.Split(path, ".") {
		for field != "" {
			open := strings.IndexByte(field, '[')
			if open < 0 {
				parts = append(parts, field)
				break
			}
			if open > 0 {
				parts = append(parts, field[:open])
			}
			end := strings.IndexByte(field[open:], ']')
			if end < 0 {
				parts = append(parts, field[open:])
				break
			}
			parts = append(parts, field[open+1:open+end])
			field = field[open+end+1:]
		}
	}
	return parts
}

// settingsError matches one plugin settings violation, as plugins report
// them: "<plugin> settings: <JSON path>: <message>", one per line. A message
// that starts with "column N: " points into a string value.
var settingsError = regexp.MustCompile(`(?m)\bsettings: ([A-Za-z0-9_*.\-\[\]]+): (.+)$`)

var columnPrefix = regexp.MustCompile(`^column (\d+): `)

// Plugin returns the diagnostics for a plugin that failed to load: one per
// settings violation in err, ranged at the value it names inside
// plugin.<name>.settings, or one at the plugin's entry otherwise.
func Plugin(source []byte, name string, err error) []Diagnostic {
	base := []string{"plugin", name, "settings"}
	var diagnostics []Diagnostic
	for _, match := range settingsError.FindAllStringSubmatch(err.Error(), -1) {
		path := append(append([]string(nil), base...), ParsePath(match[1])...)
		message := match[2]
		diagnostic := Diagnostic{Code: "plugin-settings", Message: fmt.Sprintf("plugin %s settings: %s: %s", name, match[1], message)}
		diagnostic.Start, diagnostic.End = Locate(source, path)
		if column := columnPrefix.FindStringSubmatch(message); column != nil && diagnostic.End-diagnostic.Start >= 2 && source[diagnostic.Start] == '"' {
			// The column counts characters of the decoded pattern; patterns
			// are ASCII Go expressions, so it is the byte after the quote.
			n, _ := strconv.Atoi(column[1])
			if at := diagnostic.Start + n; n > 0 && at < diagnostic.End-1 {
				diagnostic.Start, diagnostic.End = at, at+1
			}
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	if len(diagnostics) == 0 {
		start, end := Locate(source, []string{"plugin", name})
		diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Code: "plugin-load", Message: fmt.Sprintf("plugin %s did not load: %v", name, err)})
	}
	return diagnostics
}

// Config returns the diagnostics for an error from loading the file.
func Config(source []byte, err error) []Diagnostic {
	var pathError *PathError
	if errors.As(err, &pathError) {
		start, end := Locate(source, pathError.Path)
		return []Diagnostic{{Start: start, End: end, Code: "config", Message: err.Error()}}
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		offset := int(syntaxError.Offset) - 1
		offset = max(0, min(offset, len(source)-1))
		return []Diagnostic{{Start: offset, End: min(offset+1, len(source)), Code: "config-syntax", Message: syntaxError.Error()}}
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) && typeError.Field != "" {
		start, end := Locate(source, ParsePath(typeError.Field))
		return []Diagnostic{{Start: start, End: end, Code: "config", Message: fmt.Sprintf("%s: must be %s, not a JSON %s", typeError.Field, typeError.Type, typeError.Value)}}
	}
	return []Diagnostic{{Start: 0, End: min(1, len(source)), Code: "config", Message: err.Error()}}
}
