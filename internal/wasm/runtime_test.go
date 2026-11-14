package wasm

import (
	"context"
	"encoding/json"

	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func buildDummyWasm(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	out := filepath.Join(dir, "plugin.wasm")
	content := []byte(`package main

import "unsafe"

var state uint32

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	b := make([]byte, size)
	if len(b) == 0 {
		var dummy [1]byte
		return uint32(uintptr(unsafe.Pointer(&dummy[0])))
	}
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

//go:wasmexport ktml_configure
func configure(ptr uint32, size uint32) {
	state = size
}

//go:wasmexport ktml_generate
func generate(astPtr, astLen, cfgPtr, cfgLen uint32) uint64 {
	b := make([]byte, 4)
	b[0] = byte(state)
	return (uint64(uintptr(unsafe.Pointer(&b[0]))) << 32) | 4
}

//go:wasmexport ktml_lsp_handle
func lspHandle(mPtr, mLen, pPtr, pLen uint32) uint64 {
	b := make([]byte, 4)
	b[0] = byte(state)
	return (uint64(uintptr(unsafe.Pointer(&b[0]))) << 32) | 4
}

//go:wasmexport ktml_manifest
func ktmlManifest() uint64 {
	m := []byte("{\"name\":\"test\",\"version\":\"0.0.0\",\"abi\":1,\"capabilities\":[\"generate\",\"format\",\"completion\"]}")
	return uint64(uintptr(unsafe.Pointer(&m[0])))<<32 | uint64(len(m))
}

func main() { <-make(chan struct{}) }
`)
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("failed to write dummy source: %v", err)
	}
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to compile dummy wasm: %v\n%s", err, string(outBytes))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read dummy wasm: %v", err)
	}
	return b
}

func TestPlugin_SharedState(t *testing.T) {
	wasmBytes := buildDummyWasm(t)

	ctx := context.Background()
	settings := map[string]any{"key": "value"}
	configuredSettings, err := json.Marshal(map[string]any{
		"key":   "value",
		"_ktml": map[string]any{"path_map": map[string]string{}},
	})
	if err != nil {
		t.Fatalf("marshal expected settings: %v", err)
	}
	expectedSize := byte(len(configuredSettings))
	p, err := NewPlugin(ctx, wasmBytes, nil, settings)
	if err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}
	defer p.Close(ctx)

	var wg sync.WaitGroup
	errCh := make(chan error, 100)
	for range 50 {
		wg.Go(func() {
			res, err := p.ExecuteGenerate(ctx, []byte{}, []byte{})
			if err != nil {
				errCh <- err
				return
			}
			if len(res.PrimaryOutput) < 1 || res.PrimaryOutput[0] != expectedSize {
				t.Errorf("expected state %d, got %v", expectedSize, res.PrimaryOutput)
			}

			lspRes, err := p.ExecuteLSP(ctx, "textDocument/didOpen", []byte{})
			if err != nil {
				errCh <- err
				return
			}
			if len(lspRes) < 1 || lspRes[0] != expectedSize {
				t.Errorf("expected LSP state %d, got %v", expectedSize, lspRes)
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent execution failed: %v", err)
	}
}

func TestNewPlugin_InvalidWasm(t *testing.T) {
	ctx := context.Background()
	_, err := NewPlugin(ctx, []byte("invalid"), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func buildGohtmlWasm(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "gohtml.wasm")
	// The path to cmd/plugins/gohtml is relative to the internal/wasm directory
	srcDir := "../../cmd/plugins/gohtml"
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to compile gohtml wasm: %v\n%s", err, string(outBytes))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read gohtml wasm: %v", err)
	}
	return b
}

func buildHtmxWasm(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "htmx.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Dir = "../../cmd/plugins/htmx"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to compile htmx wasm: %v\n%s", err, string(outBytes))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("failed to read htmx wasm: %v", err)
	}
	return b
}

func TestPlugin_ExecuteGenerate(t *testing.T) {
	wasmBytes := buildGohtmlWasm(t)

	ctx := context.Background()
	p, err := NewPlugin(ctx, wasmBytes, nil, nil)
	if err != nil {
		t.Fatalf("failed to initialize plugin: %v", err)
	}
	defer p.Close(ctx)

	payload := []byte(`{"Nodes":[]}`)
	out, err := p.ExecuteGenerate(ctx, payload, []byte(`{}`))
	if err != nil {
		t.Fatalf("ExecuteGenerate failed: %v", err)
	}

	if len(out.AuxFiles) == 0 {
		t.Log("Warning: output is empty, but generation succeeded")
	}
}

func TestPlugin_GIVENCompilerErrors_WHENGenerating_THENFailsBeforeCachePluginAndWrites(t *testing.T) {
	wasmBytes := buildDummyWasm(t)
	ctx := context.Background()
	p, err := NewPlugin(ctx, wasmBytes, nil, nil)
	if err != nil {
		t.Fatalf("failed to initialize plugin: %v", err)
	}
	defer p.Close(ctx)

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "parse diagnostic", source: "div {", want: "invalid.ktml:5-6: [syntax]"},
		{name: "semantic diagnostic", source: "Page { Missing }", want: "invalid.ktml:7-14: [unknown-component]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("invalid.ktml", []byte(test.source), 0o644); err != nil {
				t.Fatalf("write invalid source: %v", err)
			}
			stateFile := filepath.Join(".ktml", "state.json")
			if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
				t.Fatalf("create build-state directory: %v", err)
			}
			if err := os.WriteFile(stateFile, []byte("not-json"), 0o644); err != nil {
				t.Fatalf("write poisoned build state: %v", err)
			}

			err := p.Generate("invalid.ktml")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want %q", err, test.want)
			}
			if _, err := os.Stat("invalid.ktml.go"); !os.IsNotExist(err) {
				t.Fatalf("generated output exists after compiler error: %v", err)
			}
			content, err := os.ReadFile(stateFile)
			if err != nil {
				t.Fatalf("read build state: %v", err)
			}
			if got := string(content); got != "not-json" {
				t.Fatalf("build state = %q, want poisoned state unchanged", got)
			}
		})
	}
}

func TestNewPlugin_AllowedDirectories(t *testing.T) {
	wasmBytes := buildGohtmlWasm(t)

	ctx := context.Background()
	allowRead := []string{"/tmp", "/var"}

	p, err := NewPlugin(ctx, wasmBytes, allowRead, nil)
	if err != nil {
		t.Fatalf("failed to initialize plugin: %v", err)
	}
	defer p.Close(ctx)

	if p.fsCfg == nil {
		t.Fatal("expected fsCfg to be configured")
	}
}

func TestPlugin_GIVENHTMXPlugin_WHENCompletingRoute_THENReadsConfiguredChiDirectory(t *testing.T) {
	dir := t.TempDir()
	routerPath := filepath.Join(dir, "router.go")
	routerSource := `package web
import "github.com/go-chi/chi/v5"
func Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/engineers/{id}/approve", approveEngineer)
	return r
}`
	if err := os.WriteFile(routerPath, []byte(routerSource), 0o600); err != nil {
		t.Fatalf("failed to write router fixture: %v", err)
	}

	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, buildHtmxWasm(t), Options{AllowRead: []string{dir}, Settings: map[string]any{
		"chi": map[string]any{"dir": dir},
	}})
	if err != nil {
		t.Fatalf("failed to initialize HTMX plugin: %v", err)
	}
	defer plugin.Close(ctx)

	manifest, err := plugin.ExecuteManifest(ctx)
	if err != nil {
		t.Fatalf("ExecuteManifest() error = %v", err)
	}
	if string(manifest) != `{"name":"htmx","version":"0.3.0","abi":2,"capabilities":["completion","route-provider","symbol-index"]}` {
		t.Errorf("manifest = %s", manifest)
	}

	// Play the host: run the plugin's scan job on a worker instance.
	job, err := plugin.ExecuteLSP(ctx, "$/ktml/pendingJob", []byte(`{}`))
	if err != nil || len(job) == 0 {
		t.Fatalf("pendingJob = %s, %v; want the initial scan", job, err)
	}
	worker, err := plugin.NewWorker(ctx)
	if err == nil {
		defer worker.Close(ctx)
	}
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	result, err := worker.ExecuteLSP(ctx, "$/ktml/runJob", job)
	if err != nil {
		t.Fatalf("runJob error = %v", err)
	}
	delivery, _ := json.Marshal(map[string]any{"job": json.RawMessage(job), "result": json.RawMessage(result)})
	if _, err := plugin.ExecuteLSP(ctx, "$/ktml/jobResult", delivery); err != nil {
		t.Fatalf("jobResult error = %v", err)
	}

	_, err = plugin.ExecuteLSP(ctx, "textDocument/didOpen", []byte(`{
		"textDocument": {
			"uri": "file:///component.ktml",
			"text": "Button hx-post=\"/engineers\""
		}
	}`))
	if err != nil {
		t.Fatalf("didOpen error = %v", err)
	}
	response, err := plugin.ExecuteLSP(ctx, "textDocument/completion", []byte(`{
		"id": 1,
		"textDocument": {"uri": "file:///component.ktml"},
		"position": {"line": 0, "character": 26}
	}`))
	if err != nil {
		t.Fatalf("completion error = %v", err)
	}
	var decoded struct {
		Result struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if len(decoded.Result.Items) != 1 || decoded.Result.Items[0].Label != "/engineers/{id}/approve" {
		t.Errorf("completion result = %s", response)
	}
}
