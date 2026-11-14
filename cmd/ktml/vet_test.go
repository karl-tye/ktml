package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVetCmd_GIVENModules_WHENVetted_THENPrintsFindingsAndSetsExitStatus(t *testing.T) {
	exePath := filepath.Join(t.TempDir(), "ktml")
	if output, err := exec.Command("go", "build", "-o", exePath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	component := "Card [title: string, children?: content] {\n\th2 \"{title}\"\n}\n"
	tests := []struct {
		name       string
		files      map[string]string
		args       []string
		wantStdout string
		wantExit   int
	}{
		{
			name: "GIVEN_ALiteralOmittingARequiredInput_WHENVetted_THENPrintsTheFindingAndExitsOne",
			files: map[string]string{
				"ui/ui.ktml":     component,
				"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar card = ui.CardProps{}\n",
			},
			wantStdout: "web/handler.go:5:12: [missing-required-input] ui.CardProps literal omits required input title (field Title)\n",
			wantExit:   1,
		},
		{
			name: "GIVEN_CompleteLiterals_WHENVetted_THENPrintsNothingAndExitsZero",
			files: map[string]string{
				"ui/ui.ktml":     component,
				"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar card = ui.Card{Title: \"t\"}\n",
			},
			wantExit: 0,
		},
		{
			name: "GIVEN_APatternOutsideTheFinding_WHENVetted_THENExitsZero",
			files: map[string]string{
				"ui/ui.ktml":     component,
				"web/handler.go": "package web\n\nimport \"example.com/app/ui\"\n\nvar card = ui.CardProps{}\n",
			},
			args:     []string{"./ui/..."},
			wantExit: 0,
		},
		{
			name:     "GIVEN_AMissingPattern_WHENVetted_THENExitsOne",
			files:    map[string]string{"ui/ui.ktml": component},
			args:     []string{"./missing"},
			wantExit: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.files["go.mod"] = "module example.com/app\n\ngo 1.26\n"
			for name, content := range tt.files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exePath, append([]string{"vet"}, tt.args...)...)
			cmd.Dir = root
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			exit := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("run ktml vet: %v", err)
			}
			if exit != tt.wantExit {
				t.Errorf("exit status = %d, want %d\nstdout:\n%s\nstderr:\n%s", exit, tt.wantExit, stdout.String(), stderr.String())
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout =\n%q\nwant\n%q", stdout.String(), tt.wantStdout)
			}
		})
	}
}
