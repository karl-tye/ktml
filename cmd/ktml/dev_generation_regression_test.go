package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKTMLDev_GIVENInitialGenerationFailure_WHENStarted_THENExitsUnsuccessfullyBeforeRunner(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	executable := filepath.Join(t.TempDir(), "ktml")
	build := exec.Command("go", "build", "-o", executable, ".")
	build.Dir = workingDirectory
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.ktml"), []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	runnerMarker := filepath.Join(dir, "runner-started")
	config := `{"dev":{"proxy_target":"http://127.0.0.1:1","command":["sh","-c","touch ` + runnerMarker + `"],"port":17331}}`
	if err := os.WriteFile(filepath.Join(dir, "ktml.config.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	command := exec.Command(executable, "dev")
	command.Dir = dir

	output, err := command.CombinedOutput()

	if err == nil {
		t.Fatalf("ktml dev error = nil, want unsuccessful exit after generation failure\n%s", output)
	}
	if _, err := os.Stat(runnerMarker); !os.IsNotExist(err) {
		t.Fatalf("runner marker exists after generation failure: %v", err)
	}
}

func TestGenerateKTMLFiles_GIVENGenerationFailure_WHENWalking_THENReturnsFailure(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.ktml")
	second := filepath.Join(dir, "second.ktml")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(`Card { div }`), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	wantErr := errors.New("generation failed")
	generated := 0

	err := generateKTMLFiles(dir, func(string) error {
		generated++
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("generateKTMLFiles() error = %v, want %v", err, wantErr)
	}
	if generated != 1 {
		t.Errorf("generation calls = %d, want 1 after first failure", generated)
	}
}

func TestGenerateKTMLFiles_GIVENWalkFailure_WHENWalking_THENReturnsFailure(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing")
	generated := false

	err := generateKTMLFiles(missingRoot, func(string) error {
		generated = true
		return nil
	})

	if err == nil {
		t.Fatal("generateKTMLFiles() error = nil, want walk failure")
	}
	if generated {
		t.Error("generation callback ran after walk failure")
	}
}

func TestGenerateKTMLFiles_GIVENTestdataVendorAndHiddenDirectories_WHENWalking_THENSkipsThem(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"page.ktml", "web/card.ktml", "testdata/app/web/page.ktml", "vendor/lib/x.ktml", ".ktml/dev/y.ktml", "node_modules/z/z.ktml"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`Card { div }`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var generated []string
	if err := generateKTMLFiles(dir, func(path string) error {
		relative, _ := filepath.Rel(dir, path)
		generated = append(generated, filepath.ToSlash(relative))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(generated, ","); got != "page.ktml,web/card.ktml" {
		t.Errorf("generated = %s, want only the project's sources", got)
	}
}
