package buildstate

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// OutputRoot is a project directory a writer may write generated files
// into, optionally only with one of Suffixes. Dir is relative to the project
// root and matches whole path segments: "gen" admits gen/a.go, not
// generated/a.go. "." is the whole project.
type OutputRoot struct {
	Dir      string
	Suffixes []string
}

// Writer is the generator behind a publication: a plugin, named as
// ktml.config.json names it, or the native Go generator. The host writes and
// cleans up its outputs only inside Roots, and never on a denied path.
type Writer struct {
	Name  string
	Roots []OutputRoot
}

// NativeWriterName records outputs of the native Go generator; plugins
// cannot use the name (config reserves "go").
const NativeWriterName = "go"

// NativeWriter is the native Go generator: foo.ktml becomes foo_ktml.go and
// foo_ktml_dev.go, and foo.ktmlschema becomes foo_ktmlschema.go, next to
// the source.
var NativeWriter = Writer{Name: NativeWriterName, Roots: []OutputRoot{{Dir: ".", Suffixes: []string{"_ktml.go", "_ktml_dev.go", "_ktmlschema.go"}}}}

func (w *Writer) label() string {
	if w.Name == NativeWriterName {
		return "the native Go generator"
	}
	return fmt.Sprintf("plugin %q", w.Name)
}

// permits reports whether stored, a canonical project-relative path, lies in
// one of the writer's roots.
func (w *Writer) permits(stored string) bool {
	path := filepath.ToSlash(filepath.Clean(stored))
	for _, root := range w.Roots {
		dir := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(root.Dir)), "/")
		if dir != "." && !strings.HasPrefix(path, dir+"/") {
			continue
		}
		if len(root.Suffixes) == 0 {
			return true
		}
		for _, suffix := range root.Suffixes {
			if strings.HasSuffix(path, suffix) && len(path) > len(suffix) {
				return true
			}
		}
	}
	return false
}

func (w *Writer) describeRoots() string {
	if len(w.Roots) == 0 {
		return "it declares no outputs"
	}
	parts := make([]string, 0, len(w.Roots))
	for _, root := range w.Roots {
		part := filepath.ToSlash(filepath.Clean(root.Dir)) + "/"
		if len(root.Suffixes) > 0 {
			part += "*{" + strings.Join(root.Suffixes, ",") + "}"
		}
		parts = append(parts, part)
	}
	return "its declared outputs are " + strings.Join(parts, ", ")
}

// confinementError reports a write or deletion the host refused.
func confinementError(w *Writer, operation, stored, reason string) error {
	name := "KTML"
	if w != nil {
		name = w.label()
	}
	return fmt.Errorf("%s may not %s %s: %s", name, operation, filepath.ToSlash(stored), reason)
}

// checkOutput refuses a path outside the writer's roots or on the host's
// deny-list. A nil writer is the host itself, confined by the deny-list only.
func checkOutput(w *Writer, operation, stored string) error {
	if reason := deniedPath(stored); reason != "" {
		return confinementError(w, operation, stored, reason)
	}
	if w != nil && !w.permits(stored) {
		return confinementError(w, operation, stored, "outside its declared outputs ("+w.describeRoots()+")")
	}
	return nil
}

// checkExisting refuses to replace or delete a handwritten Go file: one
// without the "Code generated ... DO NOT EDIT." header.
func checkExisting(w *Writer, operation, stored string, content []byte) error {
	if strings.EqualFold(filepath.Ext(stored), ".go") && !IsGeneratedGo(content) {
		return confinementError(w, operation, stored, "a handwritten Go file (no \"// Code generated ... DO NOT EDIT.\" header)")
	}
	return nil
}

// deniedConfig are configuration files no generator may write or delete,
// wherever they are.
var deniedConfig = map[string]bool{
	"ktml.config.json": true,
	"go.mod":           true,
	"go.sum":           true,
	"go.work":          true,
	"go.work.sum":      true,
	".gitignore":       true,
	".gitattributes":   true,
	".gitmodules":      true,
}

// deniedPath returns why the host never writes or deletes stored, whatever a
// writer declares, or "": version control (.git), the host's state (.ktml),
// project configuration, and KTML sources. Names compare case-insensitively.
func deniedPath(stored string) string {
	path := strings.ToLower(filepath.ToSlash(filepath.Clean(stored)))
	segments := strings.Split(path, "/")
	for _, segment := range segments[:len(segments)-1] {
		switch segment {
		case ".git":
			return "version control (.git) is never generated"
		case ".ktml":
			return "the .ktml directory holds KTML's own state"
		}
	}
	base := segments[len(segments)-1]
	switch {
	case base == ".git" || base == ".ktml":
		return "version control and KTML state are never generated"
	case deniedConfig[base]:
		return "project configuration is never generated"
	case strings.HasSuffix(base, ".ktml") || strings.HasSuffix(base, ".ktmlschema"):
		return "KTML sources belong to the project"
	}
	return ""
}

var generatedGoHeader = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// IsGeneratedGo reports whether Go source carries the standard generated-code
// header before its package clause.
func IsGeneratedGo(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if generatedGoHeader.MatchString(line) {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(line), "package ") {
			return false
		}
	}
	return false
}
