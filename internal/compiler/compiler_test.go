package compiler_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/parser"
)

type conformanceManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Cases         []conformanceCase `json:"cases"`
}

type conformanceCase struct {
	Name       string                 `json:"name"`
	Source     string                 `json:"source"`
	Valid      bool                   `json:"valid"`
	Go         conformanceExpectation `json:"go"`
	TreeSitter conformanceExpectation `json:"treeSitter"`
}

type conformanceExpectation struct {
	Recovery               []conformanceRecovery `json:"recovery"`
	RecoveredTopLevelNodes int                   `json:"recoveredTopLevelNodes"`
}

type conformanceRecovery struct {
	Kind  string `json:"kind"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func TestCompileSyntax_GIVENInvalidSource_WHENCompiled_THENReturnsRangedDiagnosticsWithoutValidDocument(t *testing.T) {
	result := compiler.CompileSyntax("components.ktml", []byte(`Card [title string] { div }`))

	if !result.HasErrors() {
		t.Fatal("CompileSyntax() HasErrors() = false, want true")
	}
	if result.Document != nil {
		t.Error("CompileSyntax() returned a valid document for invalid source")
	}
	if len(result.Diagnostics) == 0 {
		t.Fatal("CompileSyntax() returned no diagnostics")
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.File != "components.ktml" {
		t.Errorf("diagnostic file = %q, want components.ktml", diagnostic.File)
	}
	if diagnostic.Code != "syntax" || diagnostic.Start < 0 || diagnostic.End <= diagnostic.Start {
		t.Errorf("diagnostic = %#v, want a non-empty syntax range", diagnostic)
	}
}

func TestConformanceCorpus_GIVENSharedFixture_WHENParsed_THENMatchesValidityAndRecoveryContract(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "testdata", "conformance")
	manifestData, err := os.ReadFile(filepath.Join(corpusDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read conformance manifest: %v", err)
	}

	var manifest conformanceManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode conformance manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("conformance schema version = %d, want 1", manifest.SchemaVersion)
	}

	for _, tc := range manifest.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(tc.Source)))
			if err != nil {
				t.Fatalf("read fixture %q: %v", tc.Source, err)
			}

			p := parser.New(src)
			doc := p.ParseDocument()
			gotRecovery := make([]conformanceRecovery, 0, len(p.Errors()))
			for _, parseError := range p.Errors() {
				gotRecovery = append(gotRecovery, conformanceRecovery{
					Kind:  "error",
					Start: parseError.Pos,
					End:   parseError.End,
				})
			}
			assertRecovery(t, gotRecovery, tc.Go.Recovery)
			if !tc.Valid && len(doc.Nodes) != tc.Go.RecoveredTopLevelNodes {
				t.Errorf("recovered top-level nodes = %d, want %d", len(doc.Nodes), tc.Go.RecoveredTopLevelNodes)
			}

			result := compiler.CompileSyntax(tc.Source, src)
			if result.HasErrors() == tc.Valid {
				t.Errorf("CompileSyntax() HasErrors() = %t, want %t", result.HasErrors(), !tc.Valid)
			}
			if tc.Valid && result.Document == nil {
				t.Error("CompileSyntax() document = nil for valid fixture")
			}
			if !tc.Valid && result.Document != nil {
				t.Error("CompileSyntax() exposed recovered document for invalid fixture")
			}
		})
	}
}

func assertRecovery(t *testing.T, got, want []conformanceRecovery) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recovery count = %d, want %d: got %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("recovery[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
