package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/buildstate"
)

// nativeOutputSuffixes are the file names the native Go generator writes.
var nativeOutputSuffixes = []string{"_ktml.go", "_ktml_dev.go", "_ktmlschema.go"}

// generatedHeader is Go's marker for a generated file.
var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// checkGenerated is ktml generate -check: it generates files in memory,
// ignoring the build cache, and compares every output with the file on
// disk. It writes nothing. It prints one line per output that is missing,
// changed, or stale (a generated file under a directory argument, or a
// recorded output of a selected source, that generation no longer
// produces), and fails when there is any.
func checkGenerated(ctx context.Context, args, files []string, stdout io.Writer) error {
	manifest, err := buildstate.Load(filepath.FromSlash(buildStateFile))
	if err != nil {
		return fmt.Errorf("failed to load build state: %v", err)
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	relative := func(path string) string {
		if filepath.IsAbs(path) {
			if rel, err := filepath.Rel(root, path); err == nil {
				path = rel
			}
		}
		return filepath.ToSlash(filepath.Clean(path))
	}
	expected := map[string][]byte{}
	sources := map[string]bool{}
	for index, prepared := range prepareFiles(ctx, files, false, manifest, nativeGeneration(), true) {
		_, _ = os.Stderr.Write(prepared.warnings.Bytes())
		if prepared.err != nil {
			return fmt.Errorf("generate %s: %w", files[index], prepared.err)
		}
		if prepared.publication == nil {
			continue
		}
		sources[prepared.publication.Source] = true
		for _, output := range prepared.publication.Outputs {
			expected[relative(output.Path)] = output.Content
		}
	}

	var problems []string
	for path, content := range expected {
		current, err := os.ReadFile(filepath.FromSlash(path))
		switch {
		case os.IsNotExist(err):
			problems = append(problems, "missing: "+path)
		case err != nil:
			return err
		case !bytes.Equal(current, content):
			problems = append(problems, "changed: "+path)
		}
	}
	stale := map[string]bool{}
	for source, entry := range manifest.Files {
		if !sources[source] {
			continue
		}
		for _, output := range append([]string{entry.PrimaryOutput}, entry.AuxiliaryOutputs...) {
			if path := relative(output); output != "" && expected[path] == nil {
				if _, err := os.Stat(filepath.FromSlash(path)); err == nil {
					stale[path] = true
				}
			}
		}
	}
	generated, err := generatedFilesUnder(args)
	if err != nil {
		return err
	}
	for _, path := range generated {
		if path = relative(path); expected[path] == nil {
			stale[path] = true
		}
	}
	for path := range stale {
		problems = append(problems, "stale: "+path)
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Slice(problems, func(i, j int) bool {
		return problems[i][strings.Index(problems[i], " ")+1:] < problems[j][strings.Index(problems[j], " ")+1:]
	})
	for _, problem := range problems {
		fmt.Fprintln(stdout, problem)
	}
	count := "files are"
	if len(problems) == 1 {
		count = "file is"
	}
	return fmt.Errorf("ktml generate -check: %d generated %s out of date; run ktml generate", len(problems), count)
}

// generatedFilesUnder lists the native generator's outputs (by file name
// and generated-code header) in the directories that the ktml generate
// arguments select, walking them as expandGenerateArgs does. File
// arguments select no directory.
func generatedFilesUnder(args []string) ([]string, error) {
	var found []string
	for _, arg := range args {
		recursive := arg == "..." || strings.HasSuffix(arg, "/...")
		root := strings.TrimSuffix(strings.TrimSuffix(arg, "..."), "/")
		if root == "" {
			root = "."
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				name := entry.Name()
				if path != root && (!recursive || strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			for _, suffix := range nativeOutputSuffixes {
				if strings.HasSuffix(path, suffix) {
					content, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if generatedHeader.Match(content) {
						found = append(found, path)
					}
					break
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}
