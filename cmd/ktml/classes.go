package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/karl-tye/ktml/internal/tailwind"
	"github.com/karl-tye/ktml/internal/textposition"
)

// classesCmd runs ktml classes [-o file] [-json] [-check] [paths...]. It
// lists the Tailwind class tokens of the KTML sources under paths (default
// ".") with the same extractor the language server uses, so a Tailwind
// build can read them with @source. It returns the process exit status.
func classesCmd(stdout, stderr io.Writer, args []string) int {
	flags := flag.NewFlagSet("classes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("o", "", "write the class list to `file`, only when it changed")
	asJSON := flags.Bool("json", false, "print every token and dynamic class construction per file as JSON")
	check := flags.Bool("check", false, "report class construction Tailwind cannot discover; exit 1 for classes built from parts")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	roots := flags.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}
	files, err := tailwind.ExtractFiles(roots)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	slices.SortFunc(files, func(a, b tailwind.File) int { return strings.Compare(a.Path, b.Path) })

	switch {
	case *check:
		return classesCheck(stdout, files)
	case *asJSON:
		data, err := json.MarshalIndent(classesJSON(files), "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = stdout.Write(append(data, '\n'))
		return 0
	}
	var all tailwind.Extraction
	for _, file := range files {
		all.Tokens = append(all.Tokens, file.Extraction.Tokens...)
	}
	var list bytes.Buffer
	for _, class := range all.Classes() {
		list.WriteString(class)
		list.WriteByte('\n')
	}
	if *out == "" {
		_, _ = stdout.Write(list.Bytes())
		return 0
	}
	if err := writeIfChanged(*out, list.Bytes()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// classesCheck prints each dynamic class construction as file:line:column.
// Classes built from parts fail the check; a whole-token interpolation is
// reported but can be satisfied where the presenter writes the class.
func classesCheck(stdout io.Writer, files []tailwind.File) int {
	status := 0
	for _, file := range files {
		for _, dynamic := range file.Extraction.Dynamic {
			if !dynamic.Partial {
				continue
			}
			line, column := byteLineColumn(file.Source, dynamic.Start)
			text := string(file.Source[dynamic.Start:dynamic.End])
			fmt.Fprintf(stdout, "%s:%d:%d: [%s] %s\n", file.Path, line, column, tailwind.DynamicCode, dynamic.Message(text))
			status = 1
		}
	}
	return status
}

type classesFile struct {
	Path    string           `json:"path"`
	Tokens  []classesToken   `json:"tokens"`
	Dynamic []classesDynamic `json:"dynamic"`
}

type classesToken struct {
	Class       string `json:"class"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Conditional bool   `json:"conditional,omitempty"`
	Bag         bool   `json:"bag,omitempty"`
	Script      bool   `json:"script,omitempty"`
}

type classesDynamic struct {
	Text    string `json:"text"`
	Path    string `json:"path"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Partial bool   `json:"partial,omitempty"`
}

// classesJSON is the per-file report, with byte offsets and 1-based lines
// and byte columns.
func classesJSON(files []tailwind.File) []classesFile {
	report := []classesFile{}
	for _, file := range files {
		extraction := file.Extraction
		if len(extraction.Tokens) == 0 && len(extraction.Dynamic) == 0 {
			continue
		}
		entry := classesFile{Path: file.Path, Tokens: []classesToken{}, Dynamic: []classesDynamic{}}
		for _, token := range extraction.Tokens {
			line, column := byteLineColumn(file.Source, token.Start)
			entry.Tokens = append(entry.Tokens, classesToken{Class: token.Class, Start: token.Start, End: token.End, Line: line, Column: column, Conditional: token.Conditional, Bag: token.Bag, Script: token.Script})
		}
		for _, dynamic := range extraction.Dynamic {
			line, column := byteLineColumn(file.Source, dynamic.Start)
			entry.Dynamic = append(entry.Dynamic, classesDynamic{Text: string(file.Source[dynamic.Start:dynamic.End]), Path: dynamic.Path, Start: dynamic.Start, End: dynamic.End, Line: line, Column: column, Partial: dynamic.Partial})
		}
		report = append(report, entry)
	}
	return report
}

func byteLineColumn(source []byte, offset int) (int, int) {
	line, _ := textposition.Position(source, offset)
	lineStart := bytes.LastIndexByte(source[:offset], '\n') + 1
	return line + 1, offset - lineStart + 1
}

// writeIfChanged replaces path with data through a temporary file, unless
// it already holds data: Tailwind's watcher rebuilds on every write.
func writeIfChanged(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
