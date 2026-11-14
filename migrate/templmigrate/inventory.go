package templmigrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// TemplVersion is the templ release whose parser/v2 AST this package reads.
// It must match the generator pinned by the migrated module.
const TemplVersion = "v0.3.1020"

// ReportSchema identifies the JSON report format.
const ReportSchema = "ktml.migrate.templ.inventory/v1"

// Position is a 1-based line and column (bytes) in a templ file.
type Position struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

// Node is one classified templ construct.
type Node struct {
	File string `json:"file"`
	// Template is the enclosing templ declaration, if any.
	Template string `json:"template,omitempty"`
	// Kind is the templ parser/v2 node type.
	Kind string `json:"kind"`
	// Construct refines Kind into the construct being migrated.
	Construct string   `json:"construct"`
	Status    Status   `json:"status"`
	Reasons   []string `json:"reasons,omitempty"`
	// Gap names the missing KTML feature for a blocked node.
	Gap  string   `json:"gap,omitempty"`
	From Position `json:"from"`
	To   Position `json:"to"`
	Code string   `json:"code,omitempty"`
	// Callee is the file declaring the templ component this node calls.
	Callee string `json:"callee,omitempty"`
	// References are the files of templates the call passes as arguments.
	References []string `json:"references,omitempty"`
	Typed      bool     `json:"typed"`
	// Partial marks a node the converter could not emit faithfully; any
	// partial node makes a conversion fail.
	Partial bool `json:"partial,omitempty"`
	// Output locates what the converter emitted for the node.
	Output *OutputSpan `json:"output,omitempty"`
	// Notes are the converter's review and TODO messages for the node.
	Notes []string `json:"notes,omitempty"`
}

// OutputSpan is a 1-based line range in an emitted file.
type OutputSpan struct {
	File string `json:"file"`
	From int    `json:"from,omitempty"`
	To   int    `json:"to,omitempty"`
}

// Options configure an inventory run.
type Options struct {
	// Root is the directory report paths are relative to. It defaults to the
	// working directory.
	Root string
	// Categories assigns files to migration categories and exclusions.
	Categories *Categories
	// NoTypes skips go/types loading and classifies syntactically.
	NoTypes bool
}

type sourceFile struct {
	abs, rel   string
	src        []byte
	parsed     *parser.TemplateFile
	parseError string
	category   string
	excluded   string
}

// ExpandPaths turns files, directories, and dir/... patterns into the sorted
// list of .templ files they name.
func ExpandPaths(paths []string) ([]string, error) {
	seen := make(map[string]bool)
	var files []string
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err == nil && !seen[abs] {
			seen[abs] = true
			files = append(files, abs)
		}
	}
	for _, arg := range paths {
		recursive := false
		dir := arg
		if strings.HasSuffix(arg, "/...") || arg == "..." {
			recursive = true
			dir = strings.TrimSuffix(strings.TrimSuffix(arg, "..."), "/")
			if dir == "" {
				dir = "."
			}
		}
		info, err := os.Stat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !strings.HasSuffix(dir, ".templ") {
				return nil, fmt.Errorf("%s: not a .templ file", arg)
			}
			add(dir)
			continue
		}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				name := entry.Name()
				if path != dir && (!recursive || name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".templ") {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, errors.New("no .templ files found")
	}
	return files, nil
}

// Inventory parses and classifies every node of the given .templ files.
func Inventory(paths []string, options Options) (*Report, error) {
	report, _, err := inventoryRun(paths, options)
	return report, err
}

// run holds what one inventory read and derived: the parsed files, their
// types, and each file's walker with its classified nodes. The converter
// emits from it.
type run struct {
	root    string
	files   []*sourceFile
	typed   map[*sourceFile]*typedFile
	symbols *symbols
	walkers map[*sourceFile]*walker
}

func inventoryRun(paths []string, options Options) (*Report, *run, error) {
	root := options.Root
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return nil, nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	categories := options.Categories
	if categories == nil {
		categories = &Categories{}
	}
	var files []*sourceFile
	for _, abs := range paths {
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = abs
		}
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, nil, err
		}
		file := &sourceFile{abs: abs, rel: rel, src: src}
		file.parsed, err = parser.ParseString(string(src))
		if err != nil {
			file.parsed = nil
			file.parseError = err.Error()
		} else {
			file.parsed.Filepath = abs
		}
		file.category, file.excluded = categories.Assign(rel)
		if file.excluded == "" && file.parsed != nil && isHyperview(file.parsed) {
			file.excluded = "hyperview-xml"
		}
		files = append(files, file)
	}
	var typed map[*sourceFile]*typedFile
	var problems []string
	if !options.NoTypes {
		typed, problems = loadTypes(files)
	}
	symbols := collectSymbols(files, typed)
	r := &run{root: root, files: files, typed: typed, symbols: symbols, walkers: make(map[*sourceFile]*walker)}
	report := &Report{Schema: ReportSchema, TemplVersion: TemplVersion, TypeProblems: problems}
	for _, file := range files {
		fileReport := FileReport{
			Path:       file.rel,
			Category:   file.category,
			Excluded:   file.excluded,
			ParseError: file.parseError,
		}
		if file.parsed != nil {
			fileReport.Package = strings.TrimSpace(strings.TrimPrefix(file.parsed.Package.Expression.Value, "package"))
			diagnostics, err := parser.Diagnose(file.parsed)
			if err != nil {
				fileReport.Diagnostics = append(fileReport.Diagnostics, err.Error())
			}
			for _, diagnostic := range diagnostics {
				fileReport.Diagnostics = append(fileReport.Diagnostics, fmt.Sprintf("%d:%d: %s", diagnostic.Range.From.Line+1, diagnostic.Range.From.Col+1, diagnostic.Message))
			}
			w := &walker{file: file, typed: typed[file], symbols: symbols}
			w.walkFile()
			r.walkers[file] = w
			fileReport.Typed = w.typed != nil
			fileReport.Templates = w.templates
			for i := range w.nodes {
				if file.excluded != "" {
					w.nodes[i].Status = ReviewRequired
					w.nodes[i].Reasons = append([]string{file.excluded}, w.nodes[i].Reasons...)
					w.nodes[i].Gap = ""
				}
			}
			report.Nodes = append(report.Nodes, w.nodes...)
			report.Dropped += w.dropped
			report.Presenters = append(report.Presenters, w.presenters...)
		}
		report.Files = append(report.Files, fileReport)
	}
	report.summarize()
	return report, r, nil
}

// isHyperview reports whether a templ file renders Hyperview XML rather
// than HTML.
func isHyperview(file *parser.TemplateFile) bool {
	found := false
	var visit func(nodes []parser.Node)
	visit = func(nodes []parser.Node) {
		for _, node := range nodes {
			if found {
				return
			}
			switch n := node.(type) {
			case *parser.Element:
				if n.Name == "doc" || n.Name == "screen" {
					found = true
					return
				}
				for _, attr := range n.Attributes {
					if constant, ok := attr.(*parser.ConstantAttribute); ok && strings.HasPrefix(constant.Key.String(), "xmlns") && strings.Contains(constant.Value, "hyperview.org") {
						found = true
						return
					}
				}
				visit(n.Children)
			case parser.CompositeNode:
				visit(n.ChildNodes())
			}
		}
	}
	for _, node := range file.Nodes {
		if template, ok := node.(*parser.HTMLTemplate); ok {
			visit(template.Children)
		}
	}
	return found
}
