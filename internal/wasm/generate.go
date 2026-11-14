package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
)

func (p *Plugin) Generate(file string) error {
	return p.GenerateContext(context.Background(), file)
}

func (p *Plugin) GenerateContext(ctx context.Context, file string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("failed to read input file: %v", err)
	}
	absPath, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}
	compileResult := compiler.CompileSyntax(file, content)
	if err := compileResult.Err(); err != nil {
		return fmt.Errorf("failed to parse %s: %w", file, err)
	}
	snapshot, err := compiler.NewWorkspace(filepath.Dir(absPath)).Update(absPath, 0, content)
	if err != nil {
		return fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	if err := snapshot.Err(); err != nil {
		return fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	doc := compileResult.Document

	stateFile := filepath.Join(".ktml", "state.json")
	manifest, err := buildstate.Load(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load build state: %v", err)
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("failed to resolve project root: %v", err)
	}
	generationConfig := config.GenerationConfig{
		Environment: "production",
		FilePath:    absPath,
		ProjectRoot: projectRoot,
	}
	contentHash, err := buildstate.GenerationHash(buildstate.HashWorkspace(snapshot, absPath), []byte(p.cacheID), generationConfig)
	if err != nil {
		return err
	}
	if p.cacheEnabled {
		cacheHit, err := buildstate.CacheHit(stateFile, manifest, absPath, contentHash)
		if err != nil {
			return fmt.Errorf("failed to validate build cache: %v", err)
		}
		if cacheHit {
			return nil
		}
	}

	astJSON, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal AST: %v", err)
	}
	configJSON, err := json.Marshal(generationConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}
	out, err := p.ExecuteGenerate(ctx, astJSON, configJSON)
	if err != nil {
		return fmt.Errorf("plugin execution failed: %v", err)
	}
	var resp PluginResponse
	responseErr := json.Unmarshal([]byte(out.PrimaryOutput), &resp)
	if responseErr == nil && len(resp.Errors) > 0 {
		return fmt.Errorf("plugin errors: %v", resp.Errors)
	}
	var primaryPath string
	var primaryContent []byte
	if responseErr == nil && resp.OutputPath != "" {
		primaryPath = resp.OutputPath
		primaryContent = []byte(resp.Content)
	} else {
		primaryPath, err = buildstate.FallbackOutputPath(projectRoot, file, ".go")
		if err != nil {
			return err
		}
		primaryContent = []byte(out.PrimaryOutput)
	}
	outputs := make([]buildstate.Output, 0, len(out.AuxFiles)+1)
	newAux := make([]string, 0, len(out.AuxFiles))
	for _, aux := range out.AuxFiles {
		outputs = append(outputs, buildstate.Output{Path: aux.Path, Content: aux.Content, Description: "aux file"})
		newAux = append(newAux, aux.Path)
	}
	outputs = append(outputs, buildstate.Output{Path: primaryPath, Content: primaryContent, Description: "primary output"})
	entry := buildstate.FileEntry{Hash: contentHash, PrimaryOutput: primaryPath, AuxiliaryOutputs: newAux}
	return buildstate.PublishBatch(stateFile, manifest, []buildstate.Publication{{Source: absPath, Entry: entry, Outputs: outputs, Writer: p.Writer()}}, nil)
}
