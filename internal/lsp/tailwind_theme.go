package lsp

import (
	"encoding/json"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/karl-tye/ktml/internal/tailwind"
)

var (
	cssVariableUse = regexp.MustCompile(`var\(\s*(--[A-Za-z0-9_-]+)`)
	cssComment     = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// themeVariables returns the custom properties a hover's CSS reads, in
// order: bg-peach shows background-color: var(--color-peach). Comments,
// where the server shows resolved values, are not read.
func themeVariables(hover json.RawMessage) []string {
	var result struct {
		Contents json.RawMessage `json:"contents"`
	}
	if json.Unmarshal(hover, &result) != nil {
		return nil
	}
	var variables []string
	for _, match := range cssVariableUse.FindAllStringSubmatch(cssComment.ReplaceAllString(hoverText(result.Contents), ""), -1) {
		if !slices.Contains(variables, match[1]) {
			variables = append(variables, match[1])
		}
	}
	return variables
}

// hoverText flattens MarkupContent, MarkedString, or an array of them.
func hoverText(contents json.RawMessage) string {
	var text string
	if json.Unmarshal(contents, &text) == nil {
		return text
	}
	var marked struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(contents, &marked) == nil && marked.Value != "" {
		return marked.Value
	}
	var list []json.RawMessage
	if json.Unmarshal(contents, &list) == nil {
		var parts []string
		for _, entry := range list {
			parts = append(parts, hoverText(entry))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// utilityName returns the utility of a class: without variants (hover:),
// the important marker, or an opacity modifier (/30).
func utilityName(class string) string {
	depth, last := 0, 0
	for index, r := range class {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ':':
			if depth == 0 {
				last = index + 1
			}
		}
	}
	name := strings.Trim(class[last:], "!")
	depth = 0
	for index, r := range name {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case '/':
			if depth == 0 {
				return name[:index]
			}
		}
	}
	return name
}

// findThemeDefinitions returns the locations that declare variables or the
// custom utility, in the project's Tailwind stylesheets: CSS files under
// root that use Tailwind at-rules, the files they import, and Tailwind's
// default theme when node_modules has it.
func findThemeDefinitions(root, utility string, variables []string) []any {
	var patterns []*regexp.Regexp
	if utility != "" {
		patterns = append(patterns, regexp.MustCompile(`@utility\s+(`+regexp.QuoteMeta(utility)+`)[\s{]`))
	}
	for _, variable := range variables {
		patterns = append(patterns, regexp.MustCompile(`(?:^|[\s;{])(`+regexp.QuoteMeta(variable)+`)\s*:`))
	}
	if len(patterns) == 0 {
		return nil
	}
	var locations []any
	for _, path := range tailwindStylesheets(root) {
		source, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
		for _, pattern := range patterns {
			for _, match := range pattern.FindAllSubmatchIndex(source, -1) {
				locations = append(locations, map[string]any{"uri": uri, "range": byteRange(source, match[2], match[3])})
			}
		}
	}
	return locations
}

var (
	tailwindAtRule = regexp.MustCompile(`@import\s+["']tailwindcss["']|@tailwind\s|@theme[\s{]|@utility\s`)
	cssImport      = regexp.MustCompile(`@import\s+["'](\.[^"']+)["']`)
)

func tailwindStylesheets(root string) []string {
	var stylesheets []string
	seen := make(map[string]bool)
	var add func(path string, source []byte)
	add = func(path string, source []byte) {
		if seen[path] {
			return
		}
		seen[path] = true
		stylesheets = append(stylesheets, path)
		for _, match := range cssImport.FindAllSubmatch(source, -1) {
			imported := filepath.Join(filepath.Dir(path), string(match[1]))
			if data, err := os.ReadFile(imported); err == nil {
				add(imported, data)
			}
		}
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && tailwind.SkipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".css") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err == nil && tailwindAtRule.Match(source) {
			add(path, source)
		}
		return nil
	})
	defaultTheme := filepath.Join(root, "node_modules", "tailwindcss", "theme.css")
	if data, err := os.ReadFile(defaultTheme); err == nil {
		add(defaultTheme, data)
	}
	return stylesheets
}

// tailwindIndex is the workspace's extracted class tokens. Each query
// re-extracts only the files whose size or modification time changed.
type tailwindIndex struct {
	root    string
	mu      sync.Mutex
	entries map[string]tailwindIndexEntry
}

type tailwindIndexEntry struct {
	size    int64
	modTime time.Time
	file    tailwind.File
}

func newTailwindIndex(root string) *tailwindIndex {
	return &tailwindIndex{root: root, entries: make(map[string]tailwindIndexEntry)}
}

func (index *tailwindIndex) files() []tailwind.File {
	index.mu.Lock()
	defer index.mu.Unlock()
	present := make(map[string]bool)
	_ = filepath.WalkDir(index.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != index.root && tailwind.SkipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".ktml") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		present[path] = true
		if cached, ok := index.entries[path]; ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		index.entries[path] = tailwindIndexEntry{size: info.Size(), modTime: info.ModTime(), file: tailwind.File{Path: path, Source: source, Extraction: tailwind.Extract(source)}}
		return nil
	})
	var files []tailwind.File
	for _, path := range slices.Sorted(maps.Keys(index.entries)) {
		if !present[path] {
			delete(index.entries, path)
			continue
		}
		files = append(files, index.entries[path].file)
	}
	return files
}
