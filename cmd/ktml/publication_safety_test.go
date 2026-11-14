package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFile_GIVENPluginOutputEscapesProject_WHENGenerating_THENRejectsWithoutMutation(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	externalPath := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(externalPath, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write external file: %v", err)
	}
	linkPath := filepath.Join(project, "linked")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatalf("create escaping symlink: %v", err)
	}
	if err := os.Symlink(externalPath, filepath.Join(project, "output-link")); err != nil {
		t.Fatalf("create output symlink: %v", err)
	}

	tests := []struct {
		name       string
		outputPath string
	}{
		{name: "absolute", outputPath: externalPath},
		{name: "lexical escape", outputPath: filepath.Join("..", filepath.Base(outside), "outside.txt")},
		{name: "symlink escape", outputPath: filepath.Join("linked", "outside.txt")},
		{name: "output symlink", outputPath: "output-link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(project)
			if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			pluginPath := buildOutputPathPlugin(t, test.outputPath)
			config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
			if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			err := generateFile("page.ktml", false)

			if err == nil || !strings.Contains(err.Error(), "outside project root") {
				t.Fatalf("generateFile() error = %v, want project-root rejection", err)
			}
			content, readErr := os.ReadFile(externalPath)
			if readErr != nil || string(content) != "preserve" {
				t.Fatalf("external file = %q, %v; want preserved", content, readErr)
			}
			if _, statErr := os.Stat(filepath.Join(project, ".ktml", "state.json")); !os.IsNotExist(statErr) {
				t.Fatalf("build state published after rejected output: %v", statErr)
			}
		})
	}
}

func TestGenerateFile_GIVENStateDirectoryEscapesProject_WHENGenerating_THENRejectsBeforePublication(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pluginPath := buildOutputPathPlugin(t, "page.out")
	config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Symlink(outside, ".ktml"); err != nil {
		t.Fatalf("create escaping state symlink: %v", err)
	}

	err := generateFile("page.ktml", false)

	if err == nil || !strings.Contains(err.Error(), "build state path is outside project root") {
		t.Fatalf("generateFile() error = %v, want state-root rejection", err)
	}
	if _, statErr := os.Stat("page.out"); !os.IsNotExist(statErr) {
		t.Fatalf("primary output published after state-root rejection: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("state published outside project: %v", statErr)
	}
}

func TestGenerateFile_GIVENOutputOwnedByAnotherSource_WHENGenerating_THENRejectsWithoutOverwrite(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	for _, source := range []string{"first.ktml", "second.ktml"} {
		if err := os.WriteFile(source, []byte(`Page { div }`), 0o600); err != nil {
			t.Fatalf("write %s: %v", source, err)
		}
	}
	pluginPath := buildOutputPathPlugin(t, "shared.out")
	config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := generateFile("first.ktml", false); err != nil {
		t.Fatalf("generate first source: %v", err)
	}
	statePath := filepath.Join(".ktml", "state.json")
	initialState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	err = generateFile("second.ktml", false)

	if err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("generateFile() error = %v, want ownership rejection", err)
	}
	if got := string(mustReadFile(t, "shared.out")); got != "generated" {
		t.Fatalf("shared output = %q, want first source content", got)
	}
	if got := string(mustReadFile(t, statePath)); got != string(initialState) {
		t.Fatalf("build state changed after ownership rejection:\n%s", got)
	}
}

func TestGenerateFile_GIVENRecordedOutputMissingOrCorrupt_WHENGenerating_THENRepairsOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "missing", mutate: os.Remove},
		{name: "corrupt", mutate: func(path string) error {
			return os.WriteFile(path, []byte("corrupt"), 0o600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			t.Chdir(project)
			if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}
			pluginPath := buildOutputPathPlugin(t, "page.out")
			config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
			if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if err := generateFile("page.ktml", false); err != nil {
				t.Fatalf("initial generation: %v", err)
			}
			if err := test.mutate("page.out"); err != nil {
				t.Fatalf("mutate output: %v", err)
			}

			if err := generateFile("page.ktml", false); err != nil {
				t.Fatalf("regenerate after output mutation: %v", err)
			}

			if got := string(mustReadFile(t, "page.out")); got != "generated" {
				t.Fatalf("repaired output = %q, want generated", got)
			}
		})
	}
}

func TestGenerateFile_GIVENPluginContentChanges_WHENSourceIsUnchanged_THENRegenerates(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pluginPath := filepath.Join(project, "plugin.wasm")
	buildOutputPathPluginAt(t, pluginPath, "page.out", "first plugin")
	config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `}}}`
	if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("initial generation: %v", err)
	}
	buildOutputPathPluginAt(t, pluginPath, "page.out", "second plugin")

	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("generation after plugin change: %v", err)
	}

	if got := string(mustReadFile(t, "page.out")); got != "second plugin" {
		t.Fatalf("output = %q, want changed plugin output", got)
	}
}

func TestGenerateFile_GIVENSettingsOrEnvironmentChanges_WHENSourceIsUnchanged_THENRegenerates(t *testing.T) {
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile("page.ktml", []byte(`Page { div }`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	pluginPath := filepath.Join(project, "plugin.wasm")
	buildGenerationConfigPlugin(t, pluginPath)
	writeConfig := func(value string) {
		t.Helper()
		config := `{"package_dir":".","resolution":{"generate":"test"},"plugin":{"test":{"outputs":[{"dir":"."}],"path":` + quoteJSON(t, pluginPath) + `,"settings":{"value":` + quoteJSON(t, value) + `}}}}`
		if err := os.WriteFile("ktml.config.json", []byte(config), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	writeConfig("first")
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("initial generation: %v", err)
	}
	writeConfig("second")
	if err := generateFile("page.ktml", false); err != nil {
		t.Fatalf("generation after settings change: %v", err)
	}
	if got := string(mustReadFile(t, "page.out")); got != "second:production:false" {
		t.Fatalf("settings output = %q", got)
	}

	if err := generateFile("page.ktml", true); err != nil {
		t.Fatalf("generation after environment change: %v", err)
	}
	if got := string(mustReadFile(t, "page.out")); got != "second:development:true" {
		t.Fatalf("environment output = %q", got)
	}
}

func buildOutputPathPlugin(t *testing.T, outputPath string) string {
	t.Helper()
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "plugin.wasm")
	buildOutputPathPluginAt(t, wasmPath, outputPath, "generated")
	return wasmPath
}

func buildOutputPathPluginAt(t *testing.T, wasmPath, outputPath, content string) {
	t.Helper()
	response, err := json.Marshal(map[string]string{"content": content, "output_path": outputPath})
	if err != nil {
		t.Fatalf("marshal plugin response: %v", err)
	}
	source := `package main

import "unsafe"

var buffer []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_generate
func generate(_, _, _, _ uint32) uint64 {
	response := []byte(` + quoteGoString(response) + `)
	return uint64(uint32(uintptr(unsafe.Pointer(&response[0]))))<<32 | uint64(len(response))
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() {}
`
	sourcePath := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write plugin source: %v", err)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, sourcePath)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
}

func buildGenerationConfigPlugin(t *testing.T, wasmPath string) {
	t.Helper()
	source := `package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

var buffer []byte
var result []byte

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	buffer = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport ktml_generate
func generate(_, _, configPtr, configLen uint32) uint64 {
	var config struct {
		Environment string ` + "`json:\"environment\"`" + `
		DevMode bool ` + "`json:\"dev_mode\"`" + `
		Settings map[string]any ` + "`json:\"settings\"`" + `
	}
	_ = json.Unmarshal(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(configPtr))), configLen), &config)
	result, _ = json.Marshal(map[string]string{
		"content": fmt.Sprintf("%v:%s:%t", config.Settings["value"], config.Environment, config.DevMode),
		"output_path": "page.out",
	})
	return uint64(uint32(uintptr(unsafe.Pointer(&result[0]))))<<32 | uint64(len(result))
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() {}
`
	sourcePath := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write plugin source: %v", err)
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, sourcePath)
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON string: %v", err)
	}
	return string(encoded)
}

func quoteGoString(value []byte) string {
	encoded, _ := json.Marshal(string(value))
	return string(encoded)
}
