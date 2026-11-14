// Package compiler owns fail-closed KTML source analysis shared by generation
// and development tooling.
package compiler

import (
	"fmt"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/parser"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic describes a source-ranged compiler problem.
type Diagnostic struct {
	File     string
	Code     string
	Message  string
	Severity Severity
	Start    int
	End      int
}

func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s:%d-%d: [%s] %s", d.File, d.Start, d.End, d.Code, d.Message)
}

// Result contains either a valid syntax document or error diagnostics. A
// recovered parser document is never exposed as valid compiler output.
type Result struct {
	Document    *ast.Document
	Diagnostics []Diagnostic
}

func (r Result) HasErrors() bool {
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r Result) Err() error {
	for i, diagnostic := range r.Diagnostics {
		if diagnostic.Severity != SeverityError {
			continue
		}
		remaining := 0
		for _, candidate := range r.Diagnostics[i+1:] {
			if candidate.Severity == SeverityError {
				remaining++
			}
		}
		if remaining > 0 {
			return fmt.Errorf("%s (and %d more errors)", diagnostic, remaining)
		}
		return diagnostic
	}
	return nil
}

// CompileSyntax parses one source file and rejects recovered partial syntax.
func CompileSyntax(filename string, src []byte) Result {
	p := parser.New(src)
	doc := p.ParseDocument()
	result := Result{Document: doc}
	for _, parseError := range p.Errors() {
		code := parseError.Code
		if code == "" {
			code = "syntax"
		}
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			File:     filename,
			Code:     code,
			Message:  parseError.Msg,
			Severity: SeverityError,
			Start:    parseError.Pos,
			End:      parseError.End,
		})
	}
	if result.HasErrors() {
		result.Document = nil
	}
	return result
}
