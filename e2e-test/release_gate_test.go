package e2e_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// developmentPackages must never link into a production binary (ADR-0004
// decision 7): only ktmldev builds may contain them.
var developmentPackages = []string{
	"github.com/karl-tye/ktml/dev",
	"github.com/karl-tye/ktml/dev/interp",
	"github.com/karl-tye/ktml/dev/hmr",
	"github.com/karl-tye/ktml/internal/compiler",
	// Story capture (ktml dev --capture-stories) writes rendered props into
	// story files; production binaries must hold no capture code.
	"github.com/karl-tye/ktml/internal/storygen",
}

// developmentStrings appear only in a binary that links story capture.
var developmentStrings = []string{"KTML_DEV_CAPTURE", "ktml:captured"}

// developmentSymbols are linked only by a ktmldev build: the control route
// that names root instances' components for the component picker.
var developmentSymbols = []string{"github.com/karl-tye/ktml/dev/hmr.(*HMR).Components"}

// toolPackages and toolStrings belong to the ktml command alone: the
// livereload client, the component picker, and the Explorer. No application
// build links them, with or without ktmldev.
var toolPackages = []string{
	"github.com/karl-tye/ktml/internal/dev/livereload",
	"github.com/karl-tye/ktml/internal/dev/picker",
	"github.com/karl-tye/ktml/internal/explorer",
}

var toolStrings = []string{"__ktml/picker/", "__ktmlPicker", "ktml-explorer"}

// TestReleaseGate_GIVENTheHMRApp_WHENBuiltForProduction_THENLinksNoInterpreterRegistryOrCompiler
// generates the dashboard fixture, builds it with and without -tags ktmldev,
// and inspects the package graph and the binary symbols.
func TestReleaseGate_GIVENTheHMRApp_WHENBuiltForProduction_THENLinksNoInterpreterRegistryOrCompiler(t *testing.T) {
	prepareBinaries(t)
	ktml, err := filepath.Abs("ktml")
	if err != nil {
		t.Fatal(err)
	}
	dir := copyHMRApp(t)
	run := func(name string, args ...string) string {
		t.Helper()
		command := exec.Command(name, args...)
		command.Dir = dir
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
		}
		return string(output)
	}
	run(ktml, "generate", "web")
	production := filepath.Join(t.TempDir(), "server")
	development := filepath.Join(t.TempDir(), "server-dev")
	run("go", "build", "-o", production, "./cmd/server")
	run("go", "build", "-tags", "ktmldev", "-o", development, "./cmd/server")

	deps := map[string]string{
		"production": run("go", "list", "-deps", "./cmd/server"),
		"ktmldev":    run("go", "list", "-tags", "ktmldev", "-deps", "./cmd/server"),
	}
	symbols := map[string]string{
		"production": run("go", "tool", "nm", production),
		"ktmldev":    run("go", "tool", "nm", development),
	}
	for _, pkg := range developmentPackages {
		listed := strings.Contains("\n"+deps["production"], "\n"+pkg+"\n")
		linked := strings.Contains(symbols["production"], " "+pkg+".")
		if listed || linked {
			t.Errorf("production build links %s (go list -deps: %v, go tool nm: %v)", pkg, listed, linked)
		}
		// The ktmldev build links each one, so the checks can see them.
		if !strings.Contains("\n"+deps["ktmldev"], "\n"+pkg+"\n") || !strings.Contains(symbols["ktmldev"], " "+pkg+".") {
			t.Errorf("the ktmldev build does not show %s; the gate could not detect it", pkg)
		}
	}
	for _, symbol := range developmentSymbols {
		if strings.Contains(symbols["production"], " "+symbol) {
			t.Errorf("the production build links %s", symbol)
		}
		if !strings.Contains(symbols["ktmldev"], " "+symbol) {
			t.Errorf("the ktmldev build does not link %s; the gate could not detect it", symbol)
		}
	}
	binaries := map[string]string{"production": string(readFile(t, production)), "ktmldev": string(readFile(t, development))}
	for build, listed := range deps {
		for _, pkg := range toolPackages {
			if strings.Contains("\n"+listed, "\n"+pkg+"\n") || strings.Contains(symbols[build], " "+pkg+".") {
				t.Errorf("the %s build links the ktml command's %s", build, pkg)
			}
		}
		for _, text := range toolStrings {
			if strings.Contains(binaries[build], text) {
				t.Errorf("the %s build contains the picker string %q", build, text)
			}
		}
	}
	// The strings are in the ktml command, so the gate can see them.
	tool := string(readFile(t, ktml))
	for _, text := range toolStrings {
		if !strings.Contains(tool, text) {
			t.Errorf("the ktml command does not contain %q; the gate could not detect it", text)
		}
	}
	for _, text := range developmentStrings {
		if strings.Contains(binaries["production"], text) {
			t.Errorf("the production build contains the capture string %q", text)
		}
		if !strings.Contains(binaries["ktmldev"], text) {
			t.Errorf("the ktmldev build does not contain %q; the gate could not detect it", text)
		}
	}
}
