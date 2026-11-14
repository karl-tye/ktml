package wasm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/tetratelabs/wazero"
)

// hardeningGuest is a plugin whose manifest is injected and whose exports
// misbehave on request: "loop" spins forever, "big" allocates 512 MiB,
// "wide" answers with 1 KiB, and configure tries to write into the mounted
// directory named by its settings.
const hardeningGuest = `package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"unsafe"
)

var keep [][]byte

func pack(b []byte) uint64 {
	keep = append(keep, b)
	return uint64(uintptr(unsafe.Pointer(&b[0])))<<32 | uint64(len(b))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	b := make([]byte, size+1)
	keep = append(keep, b)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

//go:wasmexport ktml_manifest
func manifest() uint64 { return pack([]byte(MANIFEST)) }

//go:wasmexport ktml_configure
func configure(ptr, size uint32) {
	var settings struct{ Dir string ` + "`json:\"dir\"`" + ` }
	_ = json.Unmarshal(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size), &settings)
	if settings.Dir != "" {
		if err := os.WriteFile(filepath.Join(settings.Dir, "written-by-plugin"), []byte("x"), 0o644); err == nil {
			panic("plugin wrote into a read-only mount")
		}
	}
}

//go:wasmexport ktml_generate
func generate(a, b, c, d uint32) uint64 { panic("ktml_generate must not be called") }

//go:wasmexport ktml_lsp_handle
func handle(mPtr, mLen, pPtr, pLen uint32) uint64 {
	method := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(mPtr))), mLen))
	switch method {
	case "loop":
		for i := 0; ; i++ {
			if i < 0 {
				return 0
			}
		}
	case "big":
		b := make([]byte, 512<<20)
		b[len(b)-1] = 1
		return pack(b[:1])
	case "wide":
		return pack(make([]byte, 1024))
	case "getwd":
		wd, err := os.Getwd()
		if err != nil {
			return pack([]byte("getwd:" + err.Error()))
		}
		return pack([]byte("wd:" + wd))
	case "textDocument/semanticTokens/full":
		panic("semantic tokens must not be requested")
	}
	return pack([]byte("ok:" + method))
}

func main() {}
`

func buildHardeningGuest(t *testing.T, manifest string) []byte {
	t.Helper()
	dir := t.TempDir()
	source := strings.Replace(hardeningGuest, "MANIFEST", "`"+manifest+"`", 1)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "plugin.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, output)
	}
	wasmBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return wasmBytes
}

const lspManifest = `{"name":"guest","version":"1.0.0","abi":1,"capabilities":["completion"]}`

func TestNewPlugin_GIVENInvalidManifests_WHENLoading_THENRefusesThePlugin(t *testing.T) {
	tests := []struct {
		name, manifest, want string
	}{
		{"unsupported abi", `{"name":"guest","version":"1.0.0","abi":3,"capabilities":["completion"]}`, "abi 3 is not supported; this host implements abi 1 and 2"},
		{"symbol index on abi 1", `{"name":"guest","version":"1.0.0","abi":1,"capabilities":["completion","symbol-index"]}`, `capability "symbol-index" needs abi 2`},
		{"missing abi", `{"name":"guest","version":"1.0.0","capabilities":["completion"]}`, "abi 0 is not supported"},
		{"unknown capability", `{"name":"guest","version":"1.0.0","abi":1,"capabilities":["lsp"]}`, `unknown capability "lsp"`},
		{"no capabilities", `{"name":"guest","version":"1.0.0","abi":1,"capabilities":[]}`, "at least one capability is required"},
		{"unknown field", `{"name":"guest","version":"1.0.0","abi":1,"capabilities":["format"],"exports":[]}`, `unknown field "exports"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPlugin(context.Background(), buildHardeningGuest(t, test.manifest), nil, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewPlugin() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNewPlugin_GIVENNoManifestExport_WHENLoading_THENRefusesThePlugin(t *testing.T) {
	_, err := NewPlugin(context.Background(), buildGohtmlWasmWithoutManifest(t), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no ktml_manifest export") {
		t.Fatalf("NewPlugin() error = %v, want a missing-manifest error", err)
	}
}

func buildGohtmlWasmWithoutManifest(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	source := "package main\n\nimport \"unsafe\"\n\n//go:wasmexport allocate\nfunc allocate(size uint32) uint32 {\n\tb := make([]byte, size+1)\n\treturn uint32(uintptr(unsafe.Pointer(&b[0])))\n}\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", "plugin.wasm", "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build guest: %v\n%s", err, output)
	}
	wasmBytes, err := os.ReadFile(filepath.Join(dir, "plugin.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	return wasmBytes
}

func TestPlugin_GIVENUndeclaredCapabilities_WHENCalled_THENHostNeverEntersTheGuest(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPlugin(ctx, buildHardeningGuest(t, lspManifest), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	defer plugin.Close(ctx)

	if _, err := plugin.ExecuteGenerate(ctx, []byte(`{}`), []byte(`{}`)); !errors.Is(err, ErrCapability) {
		t.Errorf("ExecuteGenerate() error = %v, want ErrCapability", err)
	}
	if _, err := plugin.ExecuteFormat(ctx, []byte(`{}`)); !errors.Is(err, ErrCapability) {
		t.Errorf("ExecuteFormat() error = %v, want ErrCapability", err)
	}
	response, err := plugin.ExecuteLSP(ctx, "textDocument/semanticTokens/full", []byte(`{}`))
	if err != nil || response != nil {
		t.Errorf("semantic tokens = %q, %v; want no call into a plugin without semantic-tokens", response, err)
	}
	response, err = plugin.ExecuteLSP(ctx, "textDocument/hover", []byte(`{}`))
	if err != nil || string(response) != "ok:textDocument/hover" {
		t.Errorf("hover = %q, %v", response, err)
	}

	generator, err := NewPlugin(ctx, buildHardeningGuest(t, `{"name":"gen","version":"1.0.0","abi":1,"capabilities":["format"]}`), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	defer generator.Close(ctx)
	if _, err := generator.ExecuteLSP(ctx, "textDocument/hover", []byte(`{}`)); !errors.Is(err, ErrCapability) {
		t.Errorf("ExecuteLSP() on a format-only plugin error = %v, want ErrCapability", err)
	}
}

func TestNewPlugin_GIVENAllowReadDirectory_WHENGuestWritesIntoIt_THENTheMountIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	plugin, err := NewPlugin(ctx, buildHardeningGuest(t, lspManifest), []string{dir}, map[string]any{"dir": dir})
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	defer plugin.Close(ctx)
	if _, err := os.Stat(filepath.Join(dir, "written-by-plugin")); !os.IsNotExist(err) {
		t.Fatalf("plugin wrote into its read mount (stat error = %v)", err)
	}
}

func TestPlugin_GIVENCallExceedingItsDeadline_WHENCalled_THENStopsTheGuestAndRestartsOnTheNextCall(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, buildHardeningGuest(t, lspManifest), Options{Limits: Limits{CallTimeout: 300 * time.Millisecond}})
	if err != nil {
		t.Fatalf("NewPluginWithOptions() error = %v", err)
	}
	defer plugin.Close(ctx)

	started := time.Now()
	_, err = plugin.ExecuteLSP(ctx, "loop", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "exceeded the 300ms call deadline") {
		t.Fatalf("ExecuteLSP(loop) error = %v, want a deadline error", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("deadline took %s", elapsed)
	}
	response, err := plugin.ExecuteLSP(ctx, "after", []byte(`{}`))
	if err != nil || string(response) != "ok:after" {
		t.Fatalf("call after the deadline = %q, %v; want a restarted guest", response, err)
	}
}

func TestPlugin_GIVENCancelledCaller_WHENCalling_THENStopsTheGuest(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPlugin(ctx, buildHardeningGuest(t, lspManifest), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin() error = %v", err)
	}
	defer plugin.Close(ctx)
	callCtx, cancel := context.WithCancel(ctx)
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := plugin.ExecuteLSP(callCtx, "loop", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "was cancelled") {
		t.Fatalf("ExecuteLSP(loop) error = %v, want a cancellation error", err)
	}
}

func TestPlugin_GIVENMemoryAndOutputLimits_WHENExceeded_THENFailsTheCall(t *testing.T) {
	ctx := context.Background()
	plugin, err := NewPluginWithOptions(ctx, buildHardeningGuest(t, lspManifest), Options{Limits: Limits{MemoryBytes: 128 << 20, OutputBytes: 512}})
	if err != nil {
		t.Fatalf("NewPluginWithOptions() error = %v", err)
	}
	defer plugin.Close(ctx)

	if _, err := plugin.ExecuteLSP(ctx, "big", []byte(`{}`)); err == nil {
		t.Errorf("ExecuteLSP(big) succeeded under a 128 MiB memory limit")
	}
	if _, err := plugin.ExecuteLSP(ctx, "wide", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "over the plugin output limit of 512 bytes") {
		t.Errorf("ExecuteLSP(wide) error = %v, want the output limit", err)
	}
	if response, err := plugin.ExecuteLSP(ctx, "small", []byte(`{}`)); err != nil || string(response) != "ok:small" {
		t.Errorf("ExecuteLSP(small) = %q, %v", response, err)
	}
}

func TestCompilationCache_GIVENCacheDirectory_WHENCompiling_THENPersistsCompiledPlugins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KTML_CACHE_DIR", dir)
	ctx := context.Background()
	runtime := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(newCompilationCache()))
	defer runtime.Close(ctx)
	if _, err := runtime.CompileModule(ctx, buildHardeningGuest(t, lspManifest)); err != nil {
		t.Fatalf("CompileModule() error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "wazero"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("cache directory entries = %v, %v; want the compiled module", entries, err)
	}
}

func TestPlugin_GIVENTheSymbolIndexRequest_WHENThePluginDeclaresNoSymbolIndex_THENTheHostNeverEntersTheGuest(t *testing.T) {
	ctx := context.Background()
	legacy, err := NewPlugin(ctx, buildHardeningGuest(t, lspManifest), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin(abi 1) error = %v", err)
	}
	defer legacy.Close(ctx)
	if response, err := legacy.ExecuteLSP(ctx, symbolindex.Method, []byte(`{"id":1,"workspace":true}`)); err != nil || response != nil {
		t.Errorf("abi 1 index = %q, %v; want no call into the guest", response, err)
	}

	provider, err := NewPlugin(ctx, buildHardeningGuest(t, `{"name":"guest","version":"1.0.0","abi":2,"capabilities":["completion","symbol-index"]}`), nil, nil)
	if err != nil {
		t.Fatalf("NewPlugin(abi 2) error = %v", err)
	}
	defer provider.Close(ctx)
	if !provider.Manifest().Has(CapabilitySymbolIndex) {
		t.Fatalf("manifest = %+v, want symbol-index", provider.Manifest())
	}
	if response, err := provider.ExecuteLSP(ctx, symbolindex.Method, []byte(`{"id":1,"workspace":true}`)); err != nil || string(response) != "ok:"+symbolindex.Method {
		t.Errorf("abi 2 index = %q, %v", response, err)
	}
}

func TestPlugin_GIVENAWorkDir_WHENTheGuestAsksForItsWorkingDirectory_THENItIsTheHostsProjectRoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	plugin, err := NewPluginWithOptions(ctx, buildHardeningGuest(t, lspManifest), Options{AllowRead: []string{dir}, WorkDir: dir})
	if err != nil {
		t.Fatalf("NewPluginWithOptions() error = %v", err)
	}
	defer plugin.Close(ctx)
	response, err := plugin.ExecuteLSP(ctx, "getwd", []byte(`{}`))
	if err != nil || string(response) != "wd:"+dir {
		t.Fatalf("guest working directory = %q, %v; want %q", response, err, dir)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defaulted, err := NewPluginWithOptions(ctx, buildHardeningGuest(t, lspManifest), Options{AllowRead: []string{"."}})
	if err != nil {
		t.Fatalf("NewPluginWithOptions() error = %v", err)
	}
	defer defaulted.Close(ctx)
	response, err = defaulted.ExecuteLSP(ctx, "getwd", []byte(`{}`))
	if err != nil || string(response) != "wd:"+wd {
		t.Fatalf("default guest working directory = %q, %v; want the host's %q", response, err, wd)
	}
}
