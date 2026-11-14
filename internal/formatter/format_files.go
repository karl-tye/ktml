package formatter

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/karl-tye/ktml/internal/parser"
)

func FormatFiles(pattern string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	var firstErr error
	for _, match := range matches {
		if err := formatFile(match); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to format %s: %w", match, err)
			}
		}
	}
	return firstErr
}

func formatFile(match string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parser panic: %v", r)
		}
	}()
	content, err := os.ReadFile(match)
	if err != nil {
		return err
	}
	p := parser.New(content)
	doc := p.ParseDocument()
	if err := p.Err(); err != nil {
		return err
	}
	formatted := Format(doc)
	if err := os.WriteFile(match, []byte(formatted), 0644); err != nil {
		return err
	}
	return nil
}
