package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
)

// schemaGeneratorIdentity versions native Host Schema generation in build
// cache keys, in place of a plugin's content hash.
const schemaGeneratorIdentity = "ktml-native-schema-v2"

// prepareSchemaGeneration generates the Go records of one .ktmlschema file
// natively. Host Schemas define types, which generator plugins cannot add. It
// returns a nil publication on a verified cache hit.
func prepareSchemaGeneration(file string, manifest *buildstate.Manifest, run generationRun) (*buildstate.Publication, *buildstate.Manifest, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read input file: %v", err)
	}
	absPath, err := filepath.Abs(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get absolute path: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(filepath.Dir(absPath)).Update(absPath, 0, content)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	if err := snapshot.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	dir := filepath.Dir(absPath)
	pkg, err := run.packages.Resolve(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Go package for %s: %w", file, err)
	}
	reportWarnings(run.warnings, snapshot, absPath)
	names := snapshot.GeneratedSchemaNames(absPath)
	for _, state := range snapshot.StatesIn(absPath) {
		for _, variant := range state.Variants {
			names = append(names, "ktml"+state.Name+variant.Name)
		}
	}
	if err := run.packages.CheckCollisions(dir, names); err != nil {
		return nil, nil, fmt.Errorf("generate %s: %w", file, err)
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve project root: %v", err)
	}
	relativeSource, err := buildstate.FallbackOutputPath(projectRoot, absPath, "")
	if err != nil {
		return nil, nil, err
	}
	outputPath := strings.TrimSuffix(relativeSource, ".ktmlschema") + "_ktmlschema.go"
	contentHash, err := buildstate.GenerationHash(buildstate.HashWorkspace(snapshot, absPath), []byte(schemaGeneratorIdentity), struct {
		Package gopkg.Package `json:"package"`
		Output  string        `json:"output"`
	}{pkg, outputPath})
	if err != nil {
		return nil, nil, err
	}
	stateFile := filepath.FromSlash(buildStateFile)
	if manifest == nil {
		manifest, err = buildstate.Load(stateFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load build state: %v", err)
		}
	}
	cacheHit, err := run.cacheHit(stateFile, manifest, absPath, contentHash)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to validate build cache: %v", err)
	}
	if cacheHit {
		return nil, nil, nil
	}
	generated, _, err := gogen.GenerateSchema(snapshot, absPath, gogen.Options{Package: pkg.Name, Packages: func(dir string) (gogen.GoPackage, error) {
		resolved, err := run.packages.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}})
	if err != nil {
		return nil, nil, fmt.Errorf("generate %s: %w", file, err)
	}
	entry := buildstate.FileEntry{Hash: contentHash, PrimaryOutput: outputPath}
	outputs := []buildstate.Output{{Path: outputPath, Content: generated, Description: "primary output"}}
	return &buildstate.Publication{Source: absPath, Entry: entry, Outputs: outputs, Writer: &buildstate.NativeWriter}, manifest, nil
}

// reportWarnings prints a document's nonfatal semantic diagnostics, which the
// LSP publishes with the same codes, ranges, and messages.
func reportWarnings(output io.Writer, snapshot *compiler.Snapshot, file string) {
	source := snapshot.Source(file)
	for _, diagnostic := range snapshot.Diagnostics(file) {
		if diagnostic.Severity != compiler.SeverityWarning {
			continue
		}
		line, column := 1, 1
		for index := 0; index < diagnostic.Start && index < len(source); index++ {
			if source[index] == '\n' {
				line, column = line+1, 1
			} else {
				column++
			}
		}
		fmt.Fprintf(output, "%s:%d:%d: warning [%s] %s\n", file, line, column, diagnostic.Code, diagnostic.Message)
	}
}
