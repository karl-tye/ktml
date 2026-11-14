package tree_sitter_ktml_test

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_ktml "github.com/karl-tye/ktml/tree-sitter-ktml/bindings/go"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_ktml.Language())
	if language == nil {
		t.Errorf("Error loading KTML grammar")
	}
}
