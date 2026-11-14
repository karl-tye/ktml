package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
	"github.com/karl-tye/ktml/internal/gopkg"
)

// componentGeneratorIdentity versions native component generation in build
// cache keys, in place of a plugin's content hash.
const componentGeneratorIdentity = "ktml-native-go-v3"

// prepareComponentGeneration generates one component document with the
// native Go backend: foo.ktml becomes foo_ktml.go (//go:build !ktmldev) and
// foo_ktml_dev.go (//go:build ktmldev) in the directory's own Go package
// (ADR-0003). It returns a nil publication on a verified cache hit.
func prepareComponentGeneration(snapshot *compiler.Snapshot, absPath string, manifest *buildstate.Manifest, run generationRun) (*buildstate.Publication, *buildstate.Manifest, error) {
	dir := filepath.Dir(absPath)
	pkg, err := run.packages.Resolve(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Go package for %s: %w", absPath, err)
	}
	if err := run.packages.CheckCollisions(dir, componentGoNames(snapshot, absPath)); err != nil {
		return nil, nil, fmt.Errorf("generate %s: %w", absPath, err)
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve project root: %v", err)
	}
	relativeSource, err := buildstate.FallbackOutputPath(projectRoot, absPath, "")
	if err != nil {
		return nil, nil, err
	}
	base := strings.TrimSuffix(relativeSource, ".ktml")
	productionPath, developmentPath := base+"_ktml.go", base+"_ktml_dev.go"
	contentHash, err := buildstate.GenerationHash(buildstate.HashWorkspace(snapshot, absPath), []byte(componentGeneratorIdentity), struct {
		Package gopkg.Package `json:"package"`
		Outputs []string      `json:"outputs"`
	}{pkg, []string{productionPath, developmentPath}})
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
	options := gogen.Options{Package: pkg.Name, Packages: func(dir string) (gogen.GoPackage, error) {
		resolved, err := run.packages.Resolve(dir)
		return gogen.GoPackage{ImportPath: resolved.ImportPath, Name: resolved.Name}, err
	}}
	// The two variants are independent, so they generate concurrently.
	var development []byte
	var developmentErr error
	developmentDone := make(chan struct{})
	go func() {
		defer close(developmentDone)
		developmentOptions := options
		developmentOptions.Development = true
		development, developmentErr = gogen.GenerateFile(snapshot, absPath, developmentOptions)
	}()
	production, err := gogen.GenerateFile(snapshot, absPath, options)
	<-developmentDone
	if err != nil {
		return nil, nil, fmt.Errorf("generate %s: %w", absPath, err)
	}
	if developmentErr != nil {
		return nil, nil, fmt.Errorf("generate %s: %w", absPath, developmentErr)
	}
	entry := buildstate.FileEntry{Hash: contentHash, PrimaryOutput: productionPath, AuxiliaryOutputs: []string{developmentPath}}
	outputs := []buildstate.Output{
		{Path: developmentPath, Content: development, Description: "development variant"},
		{Path: productionPath, Content: production, Description: "primary output"},
	}
	return &buildstate.Publication{Source: absPath, Entry: entry, Outputs: outputs, Writer: &buildstate.NativeWriter}, manifest, nil
}

// componentGoNames returns the package-level Go names the components of one
// document generate, for collision checks against handwritten Go.
func componentGoNames(snapshot *compiler.Snapshot, file string) []string {
	var names []string
	for _, component := range snapshot.Components() {
		if component.File != file {
			continue
		}
		name := gogen.GoName(component.Name)
		names = append(names, name, "ktmlRender"+name)
	}
	sort.Strings(names)
	return names
}

// expandGenerateArgs turns ktml generate arguments into source files. A
// file is used as given; a directory contributes its own .ktml and
// .ktmlschema files; a path ending in /... contributes every such file below
// it, skipping hidden, vendor, and node_modules directories.
func expandGenerateArgs(args []string) ([]string, error) {
	var files []string
	for _, arg := range args {
		recursive := arg == "..." || strings.HasSuffix(arg, "/...")
		root := strings.TrimSuffix(strings.TrimSuffix(arg, "..."), "/")
		if root == "" {
			root = "."
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if recursive {
				return nil, fmt.Errorf("%s is not a directory", root)
			}
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path == root {
					return nil
				}
				name := entry.Name()
				if !recursive || strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if (strings.HasSuffix(path, ".ktml") || compiler.IsSchemaPath(path)) && !compiler.IsStoryPath(path) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}
