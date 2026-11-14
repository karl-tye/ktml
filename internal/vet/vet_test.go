package vet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const uiComponents = `Card [title: string, subtitle?: string, compact: boolean = false, children: content, actions?: content, data-id: string] {
	article data-id="{data-id}" {
		h2 "{title}"
		children
	}
}

Layout [] {
	main {
		children
	}
}

Note [text: string] {
	p "{text}"
}
`

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.26\n",
		"ui/ui.ktml": uiComponents,
		// Note is handwritten, so Note literals are never props literals.
		"ui/ui.go": "package ui\n\ntype Note struct{ Body string }\n",
	}
	for name, content := range files {
		base[name] = content
	}
	for name, content := range base {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRun_GIVENPropsLiterals_WHENVetted_THENReportsOmittedRequiredInputs(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		patterns []string
		want     []string
	}{
		{
			name:  "GIVEN_APropsLiteralInTheComponentPackage_WHENRequiredInputsOmitted_THENReportsEachInSignatureOrder",
			files: map[string]string{"ui/page.go": "package ui\n\nvar page = CardProps{Title: \"t\"}\n"},
			want: []string{
				"ui/page.go:3:12: [missing-required-input] CardProps literal omits required input children (field Children)",
				"ui/page.go:3:12: [missing-required-input] CardProps literal omits required input data-id (field DataId)",
			},
		},
		{
			name:  "GIVEN_AnHStructLiteral_WHENTitleOmitted_THENReportsTitle",
			files: map[string]string{"ui/page.go": "package ui\n\nvar page = Card{Children: nil, DataId: \"d\"}\n"},
			want:  []string{"ui/page.go:3:12: [missing-required-input] Card literal omits required input title (field Title)"},
		},
		{
			name:  "GIVEN_AnEmptyLiteral_WHENVetted_THENReportsEveryRequiredInput",
			files: map[string]string{"ui/page.go": "package ui\n\nvar page = CardProps{}\n"},
			want: []string{
				"ui/page.go:3:12: [missing-required-input] CardProps literal omits required input title (field Title)",
				"ui/page.go:3:12: [missing-required-input] CardProps literal omits required input children (field Children)",
				"ui/page.go:3:12: [missing-required-input] CardProps literal omits required input data-id (field DataId)",
			},
		},
		{
			name:  "GIVEN_OnlyOptionalAndDefaultedInputsOmitted_WHENVetted_THENReportsNothing",
			files: map[string]string{"ui/page.go": "package ui\n\nvar page = CardProps{Title: \"t\", Children: nil, DataId: \"d\"}\n"},
		},
		{
			name:  "GIVEN_APositionalLiteral_WHENVetted_THENSkipsIt",
			files: map[string]string{"ui/page.go": "package ui\n\nvar page = CardProps{\"t\", nil, nil, nil, nil, \"d\"}\n"},
		},
		{
			name:  "GIVEN_AComponentWithAnImplicitChildrenOutlet_WHENChildrenOmitted_THENReportsNothing",
			files: map[string]string{"ui/page.go": "package ui\n\nvar a, b = LayoutProps{}, Layout{}\n"},
		},
		{
			name:  "GIVEN_AHandwrittenTypeNamedLikeAComponent_WHENVetted_THENSkipsIt",
			files: map[string]string{"ui/page.go": "package ui\n\nvar note = Note{}\nvar props = NoteProps{}\n"},
			want:  []string{"ui/page.go:4:13: [missing-required-input] NoteProps literal omits required input text (field Text)"},
		},
		{
			name:  "GIVEN_ASelectorWithoutAlias_WHENVetted_THENResolvesThePackageThroughTheModule",
			files: map[string]string{"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar card = ui.CardProps{Title: \"t\", Children: nil}\n"},
			want:  []string{"web/handler.go:5:12: [missing-required-input] ui.CardProps literal omits required input data-id (field DataId)"},
		},
		{
			name:  "GIVEN_AnAliasedSelectorUnderAddressOf_WHENVetted_THENReportsAtTheLiteral",
			files: map[string]string{"web/handler.go": "package web\n\nimport widgets \"example.com/app/ui\"\n\nvar card = &widgets.Card{Title: \"t\", DataId: \"d\"}\n"},
			want:  []string{"web/handler.go:5:13: [missing-required-input] widgets.Card literal omits required input children (field Children)"},
		},
		{
			name: "GIVEN_APackageNameThatDiffersFromItsDirectory_WHENImportedWithoutAlias_THENUsesThePackageName",
			files: map[string]string{
				"components/parts.ktml": "Badge [label: string] {\n\tspan \"{label}\"\n}\n",
				"components/parts.go":   "package parts\n",
				"web/handler.go":        "package web\n\nimport \"example.com/app/components\"\n\nvar badge = parts.BadgeProps{}\nvar other = components.BadgeProps{}\n",
			},
			want: []string{"web/handler.go:5:13: [missing-required-input] parts.BadgeProps literal omits required input label (field Label)"},
		},
		{
			name:  "GIVEN_AnExternalImport_WHENVetted_THENSkipsIt",
			files: map[string]string{"web/handler.go": "package web\n\nimport ui \"github.com/other/ui\"\n\nvar card = ui.CardProps{}\n"},
		},
		{
			name: "GIVEN_GeneratedFiles_WHENVetted_THENSkipsThem",
			files: map[string]string{
				"ui/ui_ktml.go":       "package ui\n\nvar card = CardProps{}\n",
				"ui/ui_ktml_dev.go":   "//go:build ktmldev\n\npackage ui\n\nvar card = CardProps{}\n",
				"ui/ui_ktmlschema.go": "package ui\n\nvar card = CardProps{}\n",
			},
		},
		{
			name:  "GIVEN_AnExternalTestFile_WHENVetted_THENChecksItsLiterals",
			files: map[string]string{"ui/card_test.go": "package ui_test\n\nimport \"example.com/app/ui\"\n\nvar card = ui.CardProps{Title: \"t\", DataId: \"d\"}\n"},
			want:  []string{"ui/card_test.go:5:12: [missing-required-input] ui.CardProps literal omits required input children (field Children)"},
		},
		{
			name: "GIVEN_ANonRecursivePattern_WHENVetted_THENChecksOnlyThatDirectory",
			files: map[string]string{
				"ui/page.go":     "package ui\n\nvar page = NoteProps{}\n",
				"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar note = ui.NoteProps{}\n",
			},
			patterns: []string{"web"},
			want:     []string{"web/handler.go:5:12: [missing-required-input] ui.NoteProps literal omits required input text (field Text)"},
		},
		{
			name: "GIVEN_AKTMLPackageWithErrors_WHENVetted_THENReportsItOnceAndContinues",
			files: map[string]string{
				"broken/broken.ktml": "Box [title: string] {\n\tMissing\n}\n",
				"broken/a.go":        "package broken\n\nvar a, b = BoxProps{}, BoxProps{}\n",
				"web/handler.go":     "package web\n\nimport \"example.com/app/ui\"\n\nvar note = ui.NoteProps{}\n",
			},
			want: []string{
				"broken/broken.ktml:2:2: [ktml-errors] KTML package broken has errors, so literals of its props types were not checked: [unknown-component] component Missing is not in scope",
				"web/handler.go:5:12: [missing-required-input] ui.NoteProps literal omits required input text (field Text)",
			},
		},
		{
			name:  "GIVEN_AnUnparsableGoFile_WHENVetted_THENReportsItAndContinues",
			files: map[string]string{"ui/bad.go": "package ui\n\nvar = \n", "ui/page.go": "package ui\n\nvar page = NoteProps{}\n"},
			want: []string{
				"ui/bad.go:3:5: [go-parse-error] expected 'IDENT', found '='",
				"ui/page.go:3:12: [missing-required-input] NoteProps literal omits required input text (field Text)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeModule(t, tt.files)
			diagnostics, err := Run(root, tt.patterns)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			got := make([]string, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				got = append(got, diagnostic.String())
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("Run() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestRun_GIVENAMissingPatternDirectory_WHENVetted_THENFails(t *testing.T) {
	root := writeModule(t, nil)
	if _, err := Run(root, []string{"./missing/..."}); err == nil || !strings.Contains(err.Error(), "vet pattern ./missing/...") {
		t.Fatalf("Run() error = %v, want unresolvable pattern", err)
	}
}

func TestRun_GIVENAWorkingDirectoryBelowTheModuleRoot_WHENVetted_THENReportsPathsRelativeToIt(t *testing.T) {
	root := writeModule(t, map[string]string{"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar note = ui.NoteProps{}\n"})
	diagnostics, err := Run(filepath.Join(root, "web"), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "handler.go:5:12: [missing-required-input] ui.NoteProps literal omits required input text (field Text)"
	if len(diagnostics) != 1 || diagnostics[0].String() != want {
		t.Fatalf("Run() = %v, want [%s]", diagnostics, want)
	}
}

func TestRunWithOverlays_GIVENAnUnsavedKTMLBuffer_WHENVetted_THENItsRequirementsApply(t *testing.T) {
	root2 := writeModule(t, map[string]string{
		"ui/page.go": "package ui\n\nvar card = Card{Children: nil, DataId: \"x\"}\n",
	})
	saved, err := Run(root2, nil)
	if err != nil || len(saved) != 1 || !strings.Contains(saved[0].Message, "omits required input title") {
		t.Fatalf("Run() = %v, %v; want the omitted title", saved, err)
	}
	optional := strings.Replace(uiComponents, "Card [title: string,", "Card [title: string = \"Untitled\",", 1)
	overlaid, err := RunWithOverlays(root2, nil, map[string][]byte{filepath.Join(root2, "ui", "ui.ktml"): []byte(optional)})
	if err != nil || len(overlaid) != 0 {
		t.Errorf("RunWithOverlays() = %v, %v; want none once title has a default in the buffer", overlaid, err)
	}
}
