package wasm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/symbolindex"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type generationStateKey struct{}

type generationState struct {
	auxFiles []AuxFile
	auxBytes int
	limit    int
	err      error
}

type AuxFile struct {
	Path    string
	Content []byte
}

type GenerateResult struct {
	PrimaryOutput string
	AuxFiles      []AuxFile
}

type Plugin struct {
	runtime      wazero.Runtime
	instance     api.Module
	module       wazero.CompiledModule
	moduleConfig wazero.ModuleConfig
	fsCfg        wazero.FSConfig
	settingsJSON []byte
	manifest     Manifest
	limits       Limits
	mu           sync.Mutex
	cacheID      string
	cacheEnabled bool
	// stopped records why the guest instance was closed, such as an exceeded
	// deadline; the next call starts a fresh instance.
	stopped error
	// source and options recreate the plugin as a background worker.
	source  []byte
	options Options
	// output logs the guest's stdout and stderr.
	output guestOutput
	// stderr is the guest's stderr, which ktml_configure failures capture.
	stderr *guestStderr
	// roots confine the allow_read mounts: a symlink cannot lead the guest
	// out of its directory.
	roots []*os.Root
}

type PluginConfig struct {
	Path      string         `json:"path"`
	AllowRead []string       `json:"allow_read,omitempty"`
	Settings  map[string]any `json:"settings,omitempty"`
}

// Options configure a plugin instance.
type Options struct {
	AllowRead []string
	Settings  map[string]any
	// Host are project settings the host passes every plugin under
	// "_ktml" beside the path map, such as {"html": {...}} from
	// ktml.config.json.
	Host   map[string]any
	Proxy  LSPProxy
	Limits Limits
	// Log receives the guest's stdout and stderr at debug level, escaped
	// and rate-limited (guestLog); slog.Default() when nil. Guest output
	// never reaches the process's stdout or stderr.
	Log *slog.Logger
	// Name is the plugin's name in ktml.config.json; the manifest name when
	// empty. Diagnostics and build state name the plugin by it.
	Name string
	// Outputs are the project directories the plugin's generated files may
	// be written to and cleaned up in (plugin.<name>.outputs). A plugin
	// without outputs may not write.
	Outputs []buildstate.OutputRoot
	// WorkDir is the guest's working directory (PWD), the project root; the
	// host's working directory when empty.
	WorkDir string
}

type LSPProxy interface {
	ProxyRequest(ctx context.Context, target string, payload []byte) ([]byte, error)
}

// LSPProxyNamespacer is implemented by proxies shared between plugins. Each
// plugin proxies through its own namespace so that equal JSON-RPC ids and
// cancellations from different plugins cannot collide.
type LSPProxyNamespacer interface {
	NewLSPNamespace() LSPProxy
}

func NewPlugin(ctx context.Context, wasmBytes []byte, allowRead []string, settings map[string]any) (*Plugin, error) {
	return NewPluginWithProxy(ctx, wasmBytes, allowRead, settings, nil)
}

var (
	compilationCacheOnce sync.Once
	compilationCache     wazero.CompilationCache
)

// newCompilationCache keeps compiled plugins in the user cache directory
// (KTML_CACHE_DIR overrides it), so each ktml process does not recompile
// them. wazero keys entries by module content and its own version. It falls
// back to memory when no cache directory is usable.
// It is created on first use, never at package initialisation.
func sharedCompilationCache() wazero.CompilationCache {
	compilationCacheOnce.Do(func() { compilationCache = newCompilationCache() })
	return compilationCache
}

func newCompilationCache() wazero.CompilationCache {
	dir := os.Getenv("KTML_CACHE_DIR")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return wazero.NewCompilationCache()
		}
		dir = filepath.Join(base, "ktml")
	}
	cache, err := wazero.NewCompilationCacheWithDir(filepath.Join(dir, "wazero"))
	if err != nil {
		return wazero.NewCompilationCache()
	}
	return cache
}

func NewPluginWithProxy(ctx context.Context, wasmBytes []byte, allowRead []string, settings map[string]any, proxy LSPProxy) (*Plugin, error) {
	return NewPluginWithOptions(ctx, wasmBytes, Options{AllowRead: allowRead, Settings: settings, Proxy: proxy})
}

// NewLSPPlugin loads a plugin for `ktml lsp`, where stdout carries LSP frames.
// Like every plugin's, the guest's output goes only to the host log.
func NewLSPPlugin(ctx context.Context, wasmBytes []byte, allowRead []string, settings map[string]any, proxy LSPProxy) (*Plugin, error) {
	return NewPluginWithOptions(ctx, wasmBytes, Options{AllowRead: allowRead, Settings: settings, Proxy: proxy})
}

// NewPluginWithOptions loads, validates, and configures a plugin. The guest
// sees its allow_read directories read-only and runs within opts.Limits.
func NewPluginWithOptions(ctx context.Context, wasmBytes []byte, opts Options) (*Plugin, error) {
	output := guestOutput{stdout: newGuestLog("stdout", opts.Log), stderr: newGuestLog("stderr", opts.Log)}
	output.setName(opts.Name)
	workDir := opts.WorkDir
	if workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve the plugin working directory: %w", err)
		}
		workDir = wd
	}
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolve the plugin working directory: %w", err)
	}
	plugin, err := newPlugin(ctx, wasmBytes, opts.AllowRead, opts.Settings, opts.Host, opts.Proxy, output, opts.Limits.withDefaults(), workDir)
	if err != nil {
		return nil, err
	}
	plugin.source, plugin.options = wasmBytes, opts
	return plugin, nil
}

// NewWorker loads a second, independent instance of the plugin with the same
// settings, mounts, and limits, for background jobs: a long job on the worker
// never holds up requests to p.
func (p *Plugin) NewWorker(ctx context.Context) (*Plugin, error) {
	if p.source == nil {
		return nil, fmt.Errorf("plugin %q was not loaded with options and cannot start a worker", p.manifest.Name)
	}
	return NewPluginWithOptions(ctx, p.source, p.options)
}

// configuredSettings is the JSON ktml_configure receives: the plugin's own
// settings and, under "_ktml", the host's: the path map of its read-only
// mounts and the project settings in host.
func configuredSettings(settings map[string]any, pathMap map[string]string, host map[string]any) ([]byte, error) {
	pluginSettings := make(map[string]any, len(settings)+1)
	for key, value := range settings {
		pluginSettings[key] = value
	}
	hostSettings := make(map[string]any, len(host)+1)
	for key, value := range host {
		hostSettings[key] = value
	}
	hostSettings["path_map"] = pathMap
	pluginSettings["_ktml"] = hostSettings
	data, err := json.Marshal(pluginSettings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}
	return data, nil
}

func newPlugin(ctx context.Context, wasmBytes []byte, allowRead []string, settings, host map[string]any, proxy LSPProxy, output guestOutput, limits Limits, workDir string) (*Plugin, error) {
	if namespacer, ok := proxy.(LSPProxyNamespacer); ok {
		proxy = namespacer.NewLSPNamespace()
	}
	// Closing on context done is what makes call deadlines and cancellation
	// stop a running guest.
	config := wazero.NewRuntimeConfig().
		WithCompilationCache(sharedCompilationCache()).
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(limits.memoryPages())
	r := wazero.NewRuntimeWithConfig(ctx, config)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	_, err := r.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, pathPtr, pathLen, contentPtr, contentLen uint32) {
			pathBytes, ok := m.Memory().Read(pathPtr, pathLen)
			if !ok {
				recordHostWriteError(ctx, fmt.Errorf("invalid path memory range"))
				return
			}
			contentBytes, ok := m.Memory().Read(contentPtr, contentLen)
			if !ok {
				recordHostWriteError(ctx, fmt.Errorf("invalid content memory range"))
				return
			}
			path := string(pathBytes)
			clean := filepath.Clean(path)
			if path == "" || filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				recordHostWriteError(ctx, fmt.Errorf("rejected auxiliary path %q", path))
				return
			}

			if state, ok := ctx.Value(generationStateKey{}).(*generationState); ok {
				state.auxBytes += len(contentBytes)
				if state.limit > 0 && state.auxBytes > state.limit {
					recordHostWriteError(ctx, fmt.Errorf("auxiliary files exceed the plugin output limit of %d bytes", state.limit))
					return
				}
				state.auxFiles = append(state.auxFiles, AuxFile{
					Path:    path,
					Content: append([]byte(nil), contentBytes...),
				})
			}
		}).
		Export("ktml_host_write_file").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, targetPtr, targetLen, payloadPtr, payloadLen uint32) uint64 {
			targetBytes, ok := m.Memory().Read(targetPtr, targetLen)
			if !ok {
				recordGenerationHostError(ctx, "host LSP proxy", fmt.Errorf("invalid target memory range"))
				return 0
			}
			payloadBytes, ok := m.Memory().Read(payloadPtr, payloadLen)
			if !ok {
				recordGenerationHostError(ctx, "host LSP proxy", fmt.Errorf("invalid payload memory range"))
				return 0
			}

			target := string(targetBytes)
			var resp []byte
			if proxy != nil {
				r, err := proxy.ProxyRequest(ctx, target, payloadBytes)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[Host] LSP proxy error for %s: %v\n", target, err)
					recordGenerationHostError(ctx, "host LSP proxy", err)
					resp = proxyErrorResponse(payloadBytes, -32001, err.Error())
				} else {
					resp = r
				}
			} else {
				recordGenerationHostError(ctx, "host LSP proxy", fmt.Errorf("LSP proxy not configured"))
				resp = proxyErrorResponse(payloadBytes, -32601, "LSP proxy not configured")
			}
			if len(resp) == 0 {
				return 0
			}

			allocFunc := m.ExportedFunction("allocate")
			if allocFunc == nil {
				recordGenerationHostError(ctx, "host LSP proxy", fmt.Errorf("allocate function not found"))
				return 0
			}

			res, err := allocFunc.Call(ctx, uint64(len(resp)))
			if err != nil || len(res) == 0 {
				if err == nil {
					err = fmt.Errorf("allocate did not return a pointer")
				}
				recordGenerationHostError(ctx, "host LSP proxy", err)
				return 0
			}

			resultPtr := uint32(res[0])
			if !m.Memory().Write(resultPtr, resp) {
				recordGenerationHostError(ctx, "host LSP proxy", fmt.Errorf("invalid response memory range"))
				return 0
			}

			return (uint64(resultPtr) << 32) | uint64(len(resp))
		}).
		Export("ktml_host_proxy_lsp").
		Instantiate(ctx)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}

	mod, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}

	// Each allow_read directory is mounted through an os.Root, so a symlink
	// inside it cannot reach a file outside it (a plain directory mount
	// follows symlinks anywhere).
	fsCfg := wazero.NewFSConfig()
	var roots []*os.Root
	closeRoots := func() {
		for _, root := range roots {
			_ = root.Close()
		}
	}
	for _, dir := range allowRead {
		absoluteDir, err := filepath.Abs(dir)
		if err != nil {
			closeRoots()
			r.Close(ctx)
			return nil, fmt.Errorf("resolve allowed plugin directory %q: %w", dir, err)
		}
		root, err := os.OpenRoot(absoluteDir)
		if err != nil {
			closeRoots()
			r.Close(ctx)
			return nil, fmt.Errorf("open allowed plugin directory %q: %w", dir, err)
		}
		roots = append(roots, root)
		configuredPath := filepath.Clean(dir)
		fsCfg = fsCfg.WithFSMount(root.FS(), configuredPath)
		if configuredPath != absoluteDir {
			fsCfg = fsCfg.WithFSMount(root.FS(), absoluteDir)
		}
	}

	plugin := &Plugin{
		runtime:      r,
		module:       mod,
		fsCfg:        fsCfg,
		limits:       limits,
		cacheEnabled: true,
		roots:        roots,
	}
	var proxyIdentity any
	if proxy != nil {
		plugin.cacheEnabled = false
	}
	if !plugin.cacheEnabled {
		var opaqueIdentity [32]byte
		if _, err := rand.Read(opaqueIdentity[:]); err != nil {
			_ = plugin.Close(ctx)
			return nil, fmt.Errorf("create opaque LSP proxy cache identity: %w", err)
		}
		proxyIdentity = opaqueIdentity
	}
	cacheID, err := buildstate.GenerationHash("", wasmBytes, struct {
		AllowRead []string       `json:"allow_read"`
		Settings  map[string]any `json:"settings"`
		Host      map[string]any `json:"host,omitempty"`
		LSPProxy  any            `json:"lsp_proxy"`
		WorkDir   string         `json:"work_dir"`
	}{allowRead, settings, host, proxyIdentity, workDir})
	if err != nil {
		r.Close(ctx)
		return nil, err
	}
	plugin.cacheID = cacheID

	pathMap := make(map[string]string, len(allowRead))
	for _, dir := range allowRead {
		absoluteDir, err := filepath.Abs(dir)
		if err != nil {
			r.Close(ctx)
			return nil, fmt.Errorf("resolve allowed plugin directory %q: %w", dir, err)
		}
		pathMap[filepath.Clean(dir)] = absoluteDir
	}
	settingsJSON, err := configuredSettings(settings, pathMap, host)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}

	plugin.settingsJSON = settingsJSON
	plugin.output = output
	plugin.stderr = &guestStderr{target: output.stderr}
	// Go's wasip1 runtime takes its working directory from PWD; without it,
	// relative paths resolve against the first mount's configured name.
	plugin.moduleConfig = wazero.NewModuleConfig().WithFSConfig(fsCfg).WithStdout(output.stdout).WithStderr(plugin.stderr).WithEnv("PWD", workDir)
	if err := plugin.start(ctx, true); err != nil {
		_ = plugin.Close(ctx)
		return nil, err
	}
	return plugin, nil
}

// start instantiates the guest, validates its manifest, and configures it.
// It runs on load and again after a call stopped the previous instance.
func (p *Plugin) start(ctx context.Context, first bool) error {
	callCtx, cancel := context.WithTimeout(ctx, p.limits.CallTimeout)
	defer cancel()
	instance, err := p.runtime.InstantiateModule(callCtx, p.module, p.moduleConfig)
	if err != nil {
		return fmt.Errorf("failed to instantiate module for configure: %w", p.callError(callCtx, "instantiate", err))
	}
	p.instance = instance
	p.stopped = nil

	if initFunc := instance.ExportedFunction("_initialize"); initFunc != nil {
		if _, err := initFunc.Call(callCtx); err != nil {
			return fmt.Errorf("failed to initialize module: %w", p.callError(callCtx, "_initialize", err))
		}
	}

	manifestFunc := instance.ExportedFunction("ktml_manifest")
	if manifestFunc == nil {
		return fmt.Errorf("plugin has no ktml_manifest export; plugins must declare abi %d and their capabilities", ABIVersion)
	}
	res, err := manifestFunc.Call(callCtx)
	if err != nil {
		return fmt.Errorf("ktml_manifest failed: %w", p.callError(callCtx, "ktml_manifest", err))
	}
	if len(res) == 0 {
		return fmt.Errorf("ktml_manifest did not return a value")
	}
	manifestBytes, err := p.readResult(res[0], "ktml_manifest")
	if err != nil {
		return err
	}
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		return err
	}
	if !first && (manifest.Name != p.manifest.Name || manifest.Version != p.manifest.Version) {
		return fmt.Errorf("plugin manifest changed on restart")
	}
	p.manifest = manifest
	p.output.nameIfUnnamed(manifest.Name)

	configFunc := instance.ExportedFunction("ktml_configure")
	if configFunc == nil {
		return nil
	}
	allocFunc := instance.ExportedFunction("allocate")
	if allocFunc == nil {
		return fmt.Errorf("allocate function not found")
	}
	res, err = allocFunc.Call(callCtx, uint64(len(p.settingsJSON)))
	if err != nil {
		return fmt.Errorf("allocate failed: %w", p.callError(callCtx, "allocate", err))
	}
	if len(res) == 0 {
		return fmt.Errorf("allocate did not return a pointer")
	}
	ptr := uint32(res[0])
	if !instance.Memory().Write(ptr, p.settingsJSON) {
		return fmt.Errorf("failed to write settings to memory")
	}

	configureState := &generationState{}
	configureCtx := context.WithValue(callCtx, generationStateKey{}, configureState)
	p.stderr.capture()
	_, err = configFunc.Call(configureCtx, uint64(ptr), uint64(len(p.settingsJSON)))
	panicked := guestPanic(p.stderr.release())
	if configureState.err != nil {
		return fmt.Errorf("ktml_configure failed: %w", configureState.err)
	}
	if err != nil && panicked != "" {
		// The guest's panic message names the rejected settings; the host
		// turns its JSON paths into ktml.config.json diagnostics.
		return fmt.Errorf("ktml_configure failed: %s: %w", panicked, p.callError(callCtx, "ktml_configure", err))
	}
	if err != nil {
		return fmt.Errorf("ktml_configure failed: %w", p.callError(callCtx, "ktml_configure", err))
	}
	return nil
}

// Manifest returns the plugin's validated manifest.
func (p *Plugin) Manifest() Manifest {
	return p.manifest
}

// callError explains a failed guest call. An exceeded deadline or a
// cancellation has closed the instance, so it is marked stopped.
func (p *Plugin) callError(ctx context.Context, export string, err error) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		err = fmt.Errorf("plugin %q stopped: %s exceeded the %s call deadline: %w", p.manifest.Name, export, p.limits.CallTimeout, err)
	case context.Canceled:
		err = fmt.Errorf("plugin %q stopped: %s was cancelled: %w", p.manifest.Name, export, err)
	default:
		return err
	}
	p.stopped = err
	return err
}

// begin starts a guest call: it checks the capability, restarts a stopped
// instance, and bounds the call by the plugin's deadline.
func (p *Plugin) begin(ctx context.Context, allowed bool, export string) (context.Context, context.CancelFunc, error) {
	if !allowed {
		return nil, nil, fmt.Errorf("%w: plugin %q does not declare a capability for %s", ErrCapability, p.manifest.Name, export)
	}
	if p.stopped != nil || p.instance == nil || p.instance.IsClosed() {
		if p.instance != nil {
			_ = p.instance.Close(ctx)
		}
		if err := p.start(ctx, false); err != nil {
			return nil, nil, fmt.Errorf("restart plugin %q: %w", p.manifest.Name, err)
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, p.limits.CallTimeout)
	return callCtx, cancel, nil
}

// readResult reads a (pointer<<32 | length) result within the output limit.
func (p *Plugin) readResult(packed uint64, export string) ([]byte, error) {
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)
	if resLen == 0 {
		return nil, nil
	}
	if int64(resLen) > int64(p.limits.OutputBytes) {
		return nil, fmt.Errorf("%s returned %d bytes, over the plugin output limit of %d bytes", export, resLen, p.limits.OutputBytes)
	}
	outBytes, ok := p.instance.Memory().Read(resPtr, resLen)
	if !ok {
		return nil, fmt.Errorf("failed to read result from memory")
	}
	return append([]byte(nil), outBytes...), nil
}

func proxyErrorResponse(payload []byte, code int, message string) []byte {
	response, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      proxyRequestID(payload),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
	return response
}

func proxyRequestID(payload []byte) json.RawMessage {
	nullID := json.RawMessage("null")
	if !json.Valid(payload) {
		return nullID
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nullID
	}
	var id json.RawMessage
	for decoder.More() {
		name, _ := decoder.Token()
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nullID
		}
		if name == "id" {
			if id != nil {
				return nullID
			}
			id = append(json.RawMessage(nil), raw...)
		}
	}
	if len(id) == 0 {
		return nullID
	}
	var stringID string
	if json.Unmarshal(id, &stringID) == nil {
		return id
	}
	var number json.Number
	if json.Unmarshal(id, &number) != nil {
		return nullID
	}
	if !proxyIntegralNumber(string(number)) {
		return nullID
	}
	return id
}

func proxyIntegralNumber(number string) bool {
	number = strings.TrimPrefix(number, "-")
	mantissa := number
	exponent := new(big.Int)
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		mantissa = number[:index]
		if _, ok := exponent.SetString(number[index+1:], 10); !ok {
			return false
		}
	}
	fractionDigits := 0
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		fractionDigits = len(mantissa) - index - 1
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	if strings.Trim(mantissa, "0") == "" {
		return true
	}
	trailingZeros := len(mantissa) - len(strings.TrimRight(mantissa, "0"))
	exponent.Add(exponent, big.NewInt(int64(trailingZeros-fractionDigits)))
	return exponent.Sign() >= 0
}

func (p *Plugin) ExecuteGenerate(ctx context.Context, astJSON []byte, configJSON []byte) (GenerateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctx, cancel, err := p.begin(ctx, p.manifest.Has(CapabilityGenerate), "ktml_generate")
	if err != nil {
		return GenerateResult{}, err
	}
	defer cancel()
	astPtr, err := p.writeInput(ctx, astJSON, "AST")
	if err != nil {
		return GenerateResult{}, err
	}
	cfgPtr, err := p.writeInput(ctx, configJSON, "Config")
	if err != nil {
		return GenerateResult{}, err
	}

	genFunc := p.instance.ExportedFunction("ktml_generate")
	if genFunc == nil {
		return GenerateResult{}, fmt.Errorf("ktml_generate function not found")
	}

	state := &generationState{limit: p.limits.OutputBytes}
	callCtx := context.WithValue(ctx, generationStateKey{}, state)

	res, err := genFunc.Call(callCtx, uint64(astPtr), uint64(len(astJSON)), uint64(cfgPtr), uint64(len(configJSON)))
	if err != nil {
		return GenerateResult{}, p.callError(ctx, "ktml_generate", err)
	}
	if state.err != nil {
		return GenerateResult{}, state.err
	}
	if len(res) == 0 {
		return GenerateResult{}, fmt.Errorf("ktml_generate did not return a value")
	}
	outBytes, err := p.readResult(res[0], "ktml_generate")
	if err != nil {
		return GenerateResult{}, err
	}

	return GenerateResult{
		PrimaryOutput: string(outBytes),
		AuxFiles:      state.auxFiles,
	}, nil
}

// writeInput copies an input into guest memory allocated by the guest.
func (p *Plugin) writeInput(ctx context.Context, input []byte, what string) (uint32, error) {
	allocFunc := p.instance.ExportedFunction("allocate")
	if allocFunc == nil {
		return 0, fmt.Errorf("allocate function not found")
	}
	res, err := allocFunc.Call(ctx, uint64(len(input)))
	if err != nil {
		return 0, p.callError(ctx, "allocate", err)
	}
	if len(res) == 0 {
		return 0, fmt.Errorf("allocate did not return a pointer for %s", what)
	}
	ptr := uint32(res[0])
	if !p.instance.Memory().Write(ptr, input) {
		return 0, fmt.Errorf("failed to write %s to memory", what)
	}
	return ptr, nil
}

func recordHostWriteError(ctx context.Context, cause error) {
	recordGenerationHostError(ctx, "host write", cause)
}

func recordGenerationHostError(ctx context.Context, operation string, cause error) {
	state, ok := ctx.Value(generationStateKey{}).(*generationState)
	if ok && state.err == nil {
		state.err = fmt.Errorf("%s failed: %w", operation, cause)
	}
}

func (p *Plugin) ExecuteFormat(ctx context.Context, astJSON []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ctx, cancel, err := p.begin(ctx, p.manifest.Has(CapabilityFormat), "ktml_format")
	if err != nil {
		return nil, err
	}
	defer cancel()
	astPtr, err := p.writeInput(ctx, astJSON, "AST")
	if err != nil {
		return nil, err
	}
	fmtFunc := p.instance.ExportedFunction("ktml_format")
	if fmtFunc == nil {
		return nil, fmt.Errorf("ktml_format function not found")
	}
	res, err := fmtFunc.Call(ctx, uint64(astPtr), uint64(len(astJSON)))
	if err != nil {
		return nil, p.callError(ctx, "ktml_format", err)
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("ktml_format did not return a value")
	}
	return p.readResult(res[0], "ktml_format")
}

// ExecuteManifest returns the manifest the plugin declared on load.
func (p *Plugin) ExecuteManifest(ctx context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Marshal(p.manifest)
}

// ExecuteLSP forwards one LSP message. Semantic token requests go only to
// plugins that declare semantic-tokens, and symbol index requests only to
// plugins that declare symbol-index; others answer nothing.
func (p *Plugin) ExecuteLSP(ctx context.Context, method string, payloadJSON []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if strings.HasPrefix(method, "textDocument/semanticTokens") && p.manifest.ServesLSP() && !p.manifest.Has(CapabilitySemanticTokens) {
		return nil, nil
	}
	if method == symbolindex.Method && !p.manifest.Has(CapabilitySymbolIndex) {
		return nil, nil
	}
	ctx, cancel, err := p.begin(ctx, p.manifest.ServesLSP(), "ktml_lsp_handle")
	if err != nil {
		return nil, err
	}
	defer cancel()
	methodPtr, err := p.writeInput(ctx, []byte(method), "method")
	if err != nil {
		return nil, err
	}
	payloadPtr, err := p.writeInput(ctx, payloadJSON, "payload")
	if err != nil {
		return nil, err
	}
	lspFunc := p.instance.ExportedFunction("ktml_lsp_handle")
	if lspFunc == nil {
		return nil, fmt.Errorf("ktml_lsp_handle function not found")
	}
	res, err := lspFunc.Call(ctx, uint64(methodPtr), uint64(len(method)), uint64(payloadPtr), uint64(len(payloadJSON)))
	if err != nil {
		return nil, p.callError(ctx, "ktml_lsp_handle", err)
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("ktml_lsp_handle did not return a value")
	}
	return p.readResult(res[0], "ktml_lsp_handle")
}

type PluginResponse struct {
	Content    string   `json:"content"`
	OutputPath string   `json:"output_path"`
	Errors     []string `json:"errors,omitempty"`
}

// Writer is the plugin as a confined generator of project files.
func (p *Plugin) Writer() *buildstate.Writer {
	return &buildstate.Writer{Name: p.Name(), Roots: p.options.Outputs}
}

// Name is the plugin's name in ktml.config.json, or its manifest name.
func (p *Plugin) Name() string {
	if p.options.Name != "" {
		return p.options.Name
	}
	return p.manifest.Name
}

// ProcessPluginResponse writes a plugin response's primary output for
// writer, confined to its declared outputs.
func ProcessPluginResponse(writer *buildstate.Writer, data []byte) error {
	var resp PluginResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("plugin errors: %v", resp.Errors)
	}
	if resp.OutputPath == "" {
		return fmt.Errorf("output_path is required")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	return buildstate.WriteOutput(root, writer, buildstate.Output{
		Path:        resp.OutputPath,
		Content:     []byte(resp.Content),
		Description: "primary output",
	})
}

func (p *Plugin) Close(ctx context.Context) error {
	if p.instance != nil {
		_ = p.instance.Close(ctx)
	}
	p.output.flush()
	for _, root := range p.roots {
		_ = root.Close()
	}
	return p.runtime.Close(ctx)
}
