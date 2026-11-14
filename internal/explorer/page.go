package explorer

import (
	"fmt"
	"html/template"
	"path/filepath"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/textposition"
)

// trusted is markup the Explorer built with escaping already applied.
type trusted = template.HTML

type previewData struct {
	Title       string
	Stylesheets []string
	Scripts     []string
	BodyClass   string
	Body        template.HTML
	Error       string
}

// previewPage wraps a rendered story in its own document, with the project's
// stylesheets and scripts, for the isolated preview frame. preview.js comes
// first, so it names the element and trigger of every HTMX request.
var previewPage = template.Must(template.New("preview").Parse(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
{{range .Stylesheets}}<link rel="stylesheet" href="{{.}}">
{{end}}<script defer src="/_explorer/assets/preview.js"></script>
{{range .Scripts}}<script defer src="{{.}}"></script>
{{end}}</head>
<body{{if .BodyClass}} class="{{.BodyClass}}"{{end}}>
{{if .Error}}<pre data-explorer-render-error style="margin:1rem;padding:1rem;border:2px solid #d20f39;background:#fff0f3;color:#7a0019;white-space:pre-wrap;font:13px ui-monospace,monospace">Render error: {{.Error}}</pre>
{{else}}{{.Body}}
{{end}}</body>
</html>
`))

// indexPage is the Explorer interface: stories, the preview frame, typed
// controls, and diagnostics. It is server-rendered; explorer.js only sends
// control edits and refreshes the panels when the workspace changes.
var indexPage = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Title}}{{.Title}} · {{end}}KTML Explorer</title>
<link rel="stylesheet" href="/_explorer/assets/explorer.css">
<script defer src="/_explorer/assets/explorer.js"></script>
</head>
<body data-story="{{.Story}}">
<header class="top"><strong>KTML Explorer</strong> <span class="root">{{.Root}}</span></header>
<div class="layout">
<div class="sidebar">
<input type="search" class="search" data-search placeholder="Search stories (/)" aria-label="Search stories by component, story, package, or route" autocomplete="off" spellcheck="false">
<p class="no-results" data-no-results hidden>No stories match.</p>
<nav class="stories" data-navigation>{{.Navigation}}</nav>
</div>
<main class="stage">
{{if .Story}}<div class="title"><h1>{{.Title}}</h1> <span class="source">{{.Source}}</span> <a class="reset" href="/?story={{.Story}}">Reset controls</a></div>
<iframe class="preview" data-preview title="Preview of {{.Title}}" src="{{.Preview}}" sandbox="allow-scripts allow-same-origin allow-forms"></iframe>
<section class="requests" data-requests aria-label="HTMX requests from the preview">
<div class="requests-head"><h2>Requests <span class="request-total" data-request-total></span></h2> <button type="button" data-requests-clear>Clear</button></div>
<p class="requests-note" data-requests-empty>No HTMX requests yet. Previews have no application behind them: the Explorer answers each request with 204 No Content, so nothing is swapped.</p>
<ol class="request-list" data-request-list></ol>
</section>
{{else}}<p class="empty">No stories yet. Add a <code>.story.ktml</code> file next to a component.</p>
{{end}}<section class="diagnostics" data-diagnostics aria-live="polite">{{.Diagnostics}}</section>
</main>
<aside class="panel" data-panel>{{.Controls}}</aside>
</div>
<script type="application/json" data-values>{{.ValuesJSON}}</script>
</body>
</html>
`))

func urlQuery(value string) string {
	return strings.NewReplacer("%", "%25", "#", "%23", "&", "%26", "+", "%2B", " ", "%20", "?", "%3F").Replace(value)
}

// location is file:line:column of a byte offset, relative to the root.
func (s *Server) location(file string, source []byte, offset int) string {
	line, column := position(source, offset)
	relative, err := filepath.Rel(s.root, file)
	if err != nil {
		relative = file
	}
	return fmt.Sprintf("%s:%d:%d", filepath.ToSlash(relative), line, column)
}

// position is the one-based line and column of a byte offset.
func position(source []byte, offset int) (int, int) {
	if offset > len(source) {
		offset = len(source)
	}
	line, character := textposition.Position(source, offset)
	return line + 1, character + 1
}

// formatErrors is the overlay text of a snapshot's errors.
func (s *Server) formatErrors(snapshot *compiler.Snapshot) string {
	var lines []string
	for _, diagnostic := range snapshot.AllDiagnostics() {
		if diagnostic.Severity != compiler.SeverityError {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: [%s] %s", s.location(diagnostic.File, snapshot.Source(diagnostic.File), diagnostic.Start), diagnostic.Code, diagnostic.Message))
	}
	return strings.Join(lines, "\n")
}

// renderDiagnostics lists the workspace's syntax and type diagnostics and the
// selected story's render error, each with its source range and the source
// line it marks.
func (s *Server) renderDiagnostics(key string) string {
	s.mu.Lock()
	snapshot := s.workspace.Snapshot()
	failure, failed := s.renderError[key]
	s.mu.Unlock()
	var b strings.Builder
	count := 0
	if failed {
		program := s.registry().Program()
		var source []byte
		if program != nil {
			source = program.Snapshot().Source(failure.Range.File)
		}
		s.diagnostic(&b, "render", "render", failure.Message, failure.Range.File, source, failure.Range.Start, failure.Range.End)
		count++
	}
	for _, diagnostic := range snapshot.AllDiagnostics() {
		severity := "error"
		if diagnostic.Severity != compiler.SeverityError {
			severity = "warning"
		}
		s.diagnostic(&b, severity, diagnostic.Code, diagnostic.Message, diagnostic.File, snapshot.Source(diagnostic.File), diagnostic.Start, diagnostic.End)
		count++
	}
	if count == 0 {
		return `<p class="ok" data-diagnostic-count="0">No diagnostics.</p>`
	}
	return fmt.Sprintf(`<h2 data-diagnostic-count="%d">Diagnostics</h2>`, count) + b.String()
}

func (s *Server) diagnostic(b *strings.Builder, severity, code, message, file string, source []byte, start, end int) {
	b.WriteString(`<article class="diagnostic ` + attr(severity) + `" data-code="` + attr(code) + `">`)
	b.WriteString(`<div><span class="where">` + text(s.location(file, source, start)) + `</span> <span class="code">[` + text(code) + `]</span> ` + text(message) + `</div>`)
	if start >= 0 && start <= len(source) {
		lineStart := strings.LastIndexByte(string(source[:start]), '\n') + 1
		lineEnd := len(source)
		if next := strings.IndexByte(string(source[start:]), '\n'); next >= 0 {
			lineEnd = start + next
		}
		markEnd := end
		if markEnd > lineEnd || markEnd < start {
			markEnd = lineEnd
		}
		b.WriteString(`<pre class="excerpt">` + text(string(source[lineStart:start])) + `<mark>` + text(string(source[start:markEnd])) + `</mark>` + text(string(source[markEnd:lineEnd])) + `</pre>`)
	}
	b.WriteString(`</article>`)
}
