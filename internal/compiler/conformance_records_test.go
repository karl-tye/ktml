package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

type normalizedRecord struct {
	Name   string            `json:"name"`
	Start  int               `json:"start"`
	End    int               `json:"end"`
	Fields []normalizedField `json:"fields"`
}

type normalizedField struct {
	Name          string `json:"name"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	NameStart     int    `json:"nameStart"`
	NameEnd       int    `json:"nameEnd"`
	Type          string `json:"type"`
	TypeStart     int    `json:"typeStart"`
	TypeEnd       int    `json:"typeEnd"`
	Optional      bool   `json:"optional"`
	OptionalStart int    `json:"optionalStart,omitempty"`
	OptionalEnd   int    `json:"optionalEnd,omitempty"`
}

type normalizedImport struct {
	Path       string                `json:"path"`
	PathStart  int                   `json:"pathStart"`
	PathEnd    int                   `json:"pathEnd"`
	Specifiers []normalizedSpecifier `json:"specifiers"`
}

type normalizedSpecifier struct {
	Name       string `json:"name"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Alias      string `json:"alias,omitempty"`
	AliasStart int    `json:"aliasStart,omitempty"`
	AliasEnd   int    `json:"aliasEnd,omitempty"`
}

type hostSchemaManifest struct {
	Cases []struct {
		Name    string             `json:"name"`
		Source  string             `json:"source"`
		Records []normalizedRecord `json:"records"`
		Imports []normalizedImport `json:"imports"`
	} `json:"cases"`
}

func TestConformanceCorpus_GIVENRecordsAndImports_WHENParsed_THENMatchesNormalizedShapeAndRanges(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	data, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest hostSchemaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, fixture := range manifest.Cases {
		if fixture.Records == nil && fixture.Imports == nil {
			continue
		}
		t.Run(fixture.Name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(fixture.Source)))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			result := compiler.CompileSyntax(fixture.Source, source)
			if result.HasErrors() {
				t.Fatalf("CompileSyntax() diagnostics = %#v", result.Diagnostics)
			}
			if fixture.Records != nil {
				got := mustJSON(t, normalizeGoRecords(result.Document))
				if want := mustJSON(t, fixture.Records); got != want {
					t.Errorf("records = %s, want %s", got, want)
				}
			}
			if fixture.Imports != nil {
				got := mustJSON(t, normalizeGoImports(result.Document))
				if want := mustJSON(t, fixture.Imports); got != want {
					t.Errorf("imports = %s, want %s", got, want)
				}
			}
		})
	}
}

func normalizeGoRecords(document *ast.Document) []normalizedRecord {
	records := []normalizedRecord{}
	for _, node := range document.Nodes {
		record, ok := node.(*ast.RecordDecl)
		if !ok {
			continue
		}
		records = append(records, normalizedRecord{
			Name: record.Name, Start: record.NameRange.Start, End: record.NameRange.End,
			Fields: normalizeGoFields(record.Fields),
		})
	}
	return records
}

// normalizeGoFields normalizes record or Variant payload fields.
func normalizeGoFields(fields []*ast.FieldDecl) []normalizedField {
	normalized := []normalizedField{}
	for _, field := range fields {
		item := normalizedField{
			Name: field.Name, Start: field.Pos(), End: field.End(),
			NameStart: field.NameRange.Start, NameEnd: field.NameRange.End,
			Type: field.TypeDecl, TypeStart: field.TypeRange.Start, TypeEnd: field.TypeRange.End,
			Optional: field.Optional,
		}
		if field.OptionalMarker != nil {
			item.OptionalStart, item.OptionalEnd = field.OptionalMarker.Start, field.OptionalMarker.End
		}
		normalized = append(normalized, item)
	}
	return normalized
}

func normalizeGoImports(document *ast.Document) []normalizedImport {
	imports := []normalizedImport{}
	for _, node := range document.Nodes {
		statement, ok := node.(*ast.ImportStmt)
		if !ok {
			continue
		}
		normalized := normalizedImport{
			Path: statement.Path, PathStart: statement.PathRange.Start, PathEnd: statement.PathRange.End,
			Specifiers: []normalizedSpecifier{},
		}
		for _, specifier := range statement.Specifiers {
			item := normalizedSpecifier{Name: specifier.Name, Start: specifier.NameRange.Start, End: specifier.NameRange.End}
			if specifier.AliasRange != nil {
				item.Alias, item.AliasStart, item.AliasEnd = specifier.Alias, specifier.AliasRange.Start, specifier.AliasRange.End
			}
			normalized.Specifiers = append(normalized.Specifiers, item)
		}
		imports = append(imports, normalized)
	}
	return imports
}
