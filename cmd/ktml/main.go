package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/karl-tye/ktml/internal/buildstate"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/config"
	"github.com/karl-tye/ktml/internal/configdiag"
	"github.com/karl-tye/ktml/internal/dev"
	"github.com/karl-tye/ktml/internal/dev/control"
	"github.com/karl-tye/ktml/internal/dev/livereload"
	"github.com/karl-tye/ktml/internal/dev/picker"
	"github.com/karl-tye/ktml/internal/dev/runner"
	"github.com/karl-tye/ktml/internal/dev/watcher"
	"github.com/karl-tye/ktml/internal/formatter"
	"github.com/karl-tye/ktml/internal/gopkg"
	"github.com/karl-tye/ktml/internal/lsp"
	"github.com/karl-tye/ktml/internal/wasm"
)

// logLevel is the level of the CLI's stderr log: warnings and errors unless
// KTML_LOG_LEVEL says otherwise or a command's -v flag raises it to debug.
var logLevel = new(slog.LevelVar)

// defaultLogLevel reads KTML_LOG_LEVEL (debug, info, warn, or error),
// defaulting to warn.
func defaultLogLevel() slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(os.Getenv("KTML_LOG_LEVEL"))); err != nil {
		return slog.LevelWarn
	}
	return level
}

func main() {
	logLevel.Set(defaultLogLevel())
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	slog.Debug("KTML CLI starting", "args", os.Args)

	if len(os.Args) < 2 {
		fmt.Println("Usage: ktml <command> [options]")
		fmt.Println("Commands: dev, explore, generate, fmt, lsp, vet, migrate, classes, stories")
		os.Exit(1)
	}

	command := os.Args[1]
	slog.Debug("Parsed command", "command", command)

	var err error
	switch command {
	case "dev":
		devCmd()
	case "generate":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = generateCmd(ctx, os.Args[2:], os.Stdout)
		stop()
	case "fmt":
		err = fmtCmd()
	case "lsp":
		err = lspCmd()
	case "vet":
		os.Exit(vetCmd(os.Stdout, os.Stderr, os.Args[2:]))
	case "migrate":
		os.Exit(migrateCmd(os.Stdin, os.Stdout, os.Stderr, os.Args[2:]))
	case "classes":
		os.Exit(classesCmd(os.Stdout, os.Stderr, os.Args[2:]))
	case "explore":
		os.Exit(exploreCmd(os.Stdout, os.Stderr, os.Args[2:]))
	case "stories":
		os.Exit(storiesCmd(os.Stdout, os.Stderr, os.Args[2:]))
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
	if err != nil {
		slog.Debug("Command failed", "command", command, "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type managedLSPProxy interface {
	wasm.LSPProxy
	GenerationCacheIdentity() any
	Close() error
}

var acquireLSPManager = func(cfg *config.Config) (managedLSPProxy, error) {
	return lsp.AcquireManager(cfg)
}

const generateUsage = "usage: ktml generate [-check] [-v] <file|dir|dir/...> [more paths]"

// generateCmd runs ktml generate. Flags may come before or after the paths.
// -check generates in memory, writes nothing, and fails listing every
// output that is missing, changed, or stale; -v logs at debug level.
func generateCmd(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	check := flags.Bool("check", false, "write nothing; fail if any generated output is missing, changed, or stale")
	verbose := flags.Bool("v", false, "log at debug level")
	flags.BoolVar(verbose, "verbose", false, "log at debug level")
	var paths []string
	for {
		if err := flags.Parse(args); err != nil {
			return fmt.Errorf("%v\n%s", err, generateUsage)
		}
		args = flags.Args()
		if len(args) == 0 {
			break
		}
		paths, args = append(paths, args[0]), args[1:]
	}
	if len(paths) == 0 {
		return errors.New(generateUsage)
	}
	if *verbose {
		logLevel.Set(slog.LevelDebug)
	}
	files, err := expandGenerateArgs(paths)
	if err != nil {
		return err
	}
	slog.Debug("Executing generateCmd", "files", files, "check", *check)
	if *check {
		return checkGenerated(ctx, paths, files, stdout)
	}
	return generateFilesContext(ctx, files, false)
}

// generateFilesContext generates files in order, as if one at a time: when
// one fails, the files before it are still published. The native Go backend
// prepares the files concurrently and publishes them in one build-state
// transaction; a generator plugin generates and publishes them one by one.
func generateFilesContext(ctx context.Context, files []string, devMode bool) error {
	if !nativeGeneration() {
		for _, file := range files {
			if err := generateFileContext(ctx, file, devMode); err != nil {
				return err
			}
		}
		return nil
	}
	// Generation allocates much short-lived memory, so collecting less often
	// saves time; GOGC still overrides this.
	if os.Getenv("GOGC") == "" {
		defer debug.SetGCPercent(debug.SetGCPercent(400))
	}
	stateFile := filepath.FromSlash(buildStateFile)
	manifest, err := buildstate.Load(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load build state: %v", err)
	}
	prepared := prepareFiles(ctx, files, devMode, manifest, true, false)
	var publications []buildstate.Publication
	var failure error
	for _, file := range prepared {
		_, _ = os.Stderr.Write(file.warnings.Bytes())
		if file.err != nil {
			failure = file.err
			break
		}
		if file.publication != nil {
			publications = append(publications, *file.publication)
		}
	}
	if len(publications) == 0 {
		return failure
	}
	writers, err := configuredWriters()
	if err != nil {
		return err
	}
	if err := buildstate.PublishBatch(stateFile, manifest, publications, nil, writers...); err != nil {
		return err
	}
	for _, publication := range publications {
		fmt.Println("Generation complete")
		slog.Debug("Generation complete for file", "file", publication.Source)
	}
	return failure
}

// nativeGeneration reports whether the configured backend is the native Go
// generator, whose preparation is safe to run concurrently.
func nativeGeneration() bool {
	cfg, err := config.Load("ktml.config.json")
	return err == nil && cfg.Resolution.Generate == config.GoBackend
}

// preparedFile is one source's prepared publication, or the error that
// stopped it, with the warnings its analysis reported.
type preparedFile struct {
	publication *buildstate.Publication
	warnings    bytes.Buffer
	err         error
}

// prepareFiles prepares files against one build state with one Go package
// resolver, concurrently when parallel is set, and ignoring the build cache
// when noCache is set. Files after the first failure may be left unprepared.
func prepareFiles(ctx context.Context, files []string, devMode bool, manifest *buildstate.Manifest, parallel, noCache bool) []preparedFile {
	prepared := make([]preparedFile, len(files))
	packages := gopkg.NewResolver()
	cache := buildstate.NewCacheChecker(filepath.FromSlash(buildStateFile), manifest)
	workers := 1
	order := make([]int, len(files))
	for index := range order {
		order[index] = index
	}
	if parallel {
		workers = min(runtime.GOMAXPROCS(0), len(files))
		// The largest sources take longest, so they start first.
		sizes := make([]int64, len(files))
		for index, file := range files {
			if info, err := os.Stat(file); err == nil {
				sizes[index] = info.Size()
			}
		}
		sort.SliceStable(order, func(i, j int) bool { return sizes[order[i]] > sizes[order[j]] })
	}
	var mu sync.Mutex
	next, firstFailure := 0, len(files)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			for {
				mu.Lock()
				if next >= len(order) {
					mu.Unlock()
					return
				}
				index := order[next]
				next++
				skip := index > firstFailure
				mu.Unlock()
				if skip {
					continue
				}
				file := &prepared[index]
				file.publication, _, file.err = prepareGeneration(ctx, files[index], devMode, manifest, generationRun{packages: packages, cache: cache, warnings: &file.warnings, noCache: noCache})
				if file.err != nil {
					mu.Lock()
					firstFailure = min(firstFailure, index)
					mu.Unlock()
				}
			}
		})
	}
	wait.Wait()
	return prepared
}

// generationRun is what the files of one generation share: a Go package
// resolver, an optional cache checker for their build state, where one
// file's analysis warnings go, and whether to ignore the cache.
type generationRun struct {
	packages *gopkg.Resolver
	cache    *buildstate.CacheChecker
	warnings io.Writer
	noCache  bool
}

// cacheHit reports whether source's recorded outputs are a verified cache
// hit for hash. With noCache, nothing is a hit.
func (run generationRun) cacheHit(stateFile string, manifest *buildstate.Manifest, source, hash string) (bool, error) {
	if run.noCache {
		return false, nil
	}
	if run.cache != nil {
		return run.cache.Hit(source, hash)
	}
	return buildstate.CacheHit(stateFile, manifest, source, hash)
}

func generateFile(file string, devMode bool) error {
	return generateFileContext(context.Background(), file, devMode)
}

const buildStateFile = ".ktml/state.json"

func generateFileContext(ctx context.Context, file string, devMode bool) error {
	publication, manifest, err := prepareGeneration(ctx, file, devMode, nil, generationRun{packages: gopkg.NewResolver(), warnings: os.Stderr})
	if err != nil || publication == nil {
		return err
	}
	writers, err := configuredWriters()
	if err != nil {
		return err
	}
	if err := buildstate.PublishBatch(filepath.FromSlash(buildStateFile), manifest, []buildstate.Publication{*publication}, nil, writers...); err != nil {
		return err
	}
	fmt.Println("Generation complete")
	slog.Debug("Generation complete for file", "file", publication.Source)
	return nil
}

// generateBatchContext prepares every file before publishing any output, then
// commits all publications and source removals as one build-state transaction.
func generateBatchContext(ctx context.Context, batch dev.Batch) error {
	stateFile := filepath.FromSlash(buildStateFile)
	manifest, err := buildstate.Load(stateFile)
	if err != nil {
		return fmt.Errorf("failed to load build state: %v", err)
	}
	publications := make([]buildstate.Publication, 0, len(batch.Generate))
	for index, prepared := range prepareFiles(ctx, batch.Generate, true, manifest, nativeGeneration(), false) {
		_, _ = os.Stderr.Write(prepared.warnings.Bytes())
		if prepared.err != nil {
			return fmt.Errorf("generate %s: %w", batch.Generate[index], prepared.err)
		}
		if prepared.publication != nil {
			publications = append(publications, *prepared.publication)
		}
	}
	removals := make([]string, 0, len(batch.Remove))
	for _, file := range batch.Remove {
		absPath, err := filepath.Abs(file)
		if err != nil {
			return fmt.Errorf("failed to resolve removed source %s: %v", file, err)
		}
		removals = append(removals, absPath)
	}
	writers, err := configuredWriters()
	if err != nil {
		return err
	}
	if err := buildstate.PublishBatch(stateFile, manifest, publications, removals, writers...); err != nil {
		return err
	}
	slog.Debug("Batch generation complete", "generated", len(publications), "removed", len(removals))
	return nil
}

// prepareGeneration validates and generates one file without publishing it,
// loading build state after source validation when manifest is nil. It returns
// a nil publication when the recorded outputs are a verified cache hit.
func prepareGeneration(ctx context.Context, file string, devMode bool, manifest *buildstate.Manifest, run generationRun) (_ *buildstate.Publication, _ *buildstate.Manifest, resultErr error) {
	slog.Debug("Starting generateFile", "file", file, "devMode", devMode)
	if compiler.IsStoryPath(file) {
		// Stories are development fixtures for the Component Explorer: they
		// are never generated, so production builds cannot contain them.
		return nil, nil, nil
	}
	if compiler.IsSchemaPath(file) {
		return prepareSchemaGeneration(file, manifest, run)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read input file: %v", err)
	}
	compileResult := compiler.CompileSyntax(file, content)
	if err := compileResult.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to parse %s: %w", file, err)
	}
	doc := compileResult.Document
	absPath, err := filepath.Abs(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get absolute path: %v", err)
	}
	snapshot, err := compiler.NewWorkspace(filepath.Dir(absPath)).Update(absPath, 0, content)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	if err := snapshot.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to analyze %s: %w", file, err)
	}
	reportWarnings(run.warnings, snapshot, absPath)

	cfg, err := config.Load("ktml.config.json")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %v", err)
	}
	slog.Debug("Loaded config", "generate", cfg.Resolution.Generate)

	if cfg.Resolution.Generate == config.GoBackend {
		return prepareComponentGeneration(snapshot, absPath, manifest, run)
	}
	if cfg.Resolution.Generate == "" {
		return nil, nil, fmt.Errorf("no generate plugin configured")
	}
	pluginCfg, ok := cfg.Plugin[cfg.Resolution.Generate]
	if !ok {
		return nil, nil, fmt.Errorf("plugin '%s' not found in config", cfg.Resolution.Generate)
	}
	slog.Debug("Loaded plugin config", "pluginPath", pluginCfg.Path)

	stateFile := filepath.FromSlash(buildStateFile)
	if manifest == nil {
		manifest, err = buildstate.Load(stateFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load build state: %v", err)
		}
	}

	wasmBytes, err := os.ReadFile(pluginCfg.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read plugin wasm: %v", err)
	}
	environment := "production"
	if devMode {
		environment = "development"
	}
	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve project root: %v", err)
	}
	genCfg := config.GenerationConfig{
		Environment: environment,
		FilePath:    absPath,
		ProjectRoot: projectRoot,
		Settings:    pluginCfg.Settings,
		DevMode:     devMode,
	}
	proxy, err := acquireLSPManager(cfg)
	if err != nil {
		return nil, nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, proxy.Close()) }()
	proxyIdentity := proxy.GenerationCacheIdentity()
	cacheEnabled := proxyIdentity != nil
	if !cacheEnabled {
		var opaqueIdentity [32]byte
		if _, err := rand.Read(opaqueIdentity[:]); err != nil {
			return nil, nil, fmt.Errorf("create opaque LSP proxy cache identity: %w", err)
		}
		proxyIdentity = opaqueIdentity
	}
	contentHash, err := buildstate.GenerationHash(buildstate.HashWorkspace(snapshot, absPath), wasmBytes, struct {
		PluginName string                  `json:"plugin_name"`
		Plugin     config.Plugin           `json:"plugin"`
		PackageDir string                  `json:"package_dir"`
		LSP        any                     `json:"lsp"`
		Generation config.GenerationConfig `json:"generation"`
	}{cfg.Resolution.Generate, pluginCfg, cfg.PackageDir, proxyIdentity, genCfg})
	if err != nil {
		return nil, nil, err
	}
	if cacheEnabled {
		cacheHit, err := run.cacheHit(stateFile, manifest, absPath, contentHash)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to validate build cache: %v", err)
		}
		if cacheHit {
			slog.Debug("Cache hit, skipping generation", "file", absPath)
			return nil, nil, nil
		}
	}

	options := pluginOptions(cfg, pluginCfg)
	options.Proxy = proxy
	plugin, err := wasm.NewPluginWithOptions(ctx, wasmBytes, options)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load plugin: %v", err)
	}
	defer plugin.Close(ctx)
	slog.Debug("WASM plugin loaded successfully")

	slog.Debug("Parsing document", "file", absPath)

	astJSON, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal AST: %v", err)
	}

	configJSON, err := json.Marshal(genCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal config: %v", err)
	}

	slog.Debug("Executing plugin ExecuteGenerate")
	out, err := plugin.ExecuteGenerate(ctx, astJSON, configJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("plugin execution failed: %v", err)
	}
	slog.Debug("Plugin ExecuteGenerate finished", "auxFilesCount", len(out.AuxFiles))
	var resp wasm.PluginResponse
	responseErr := json.Unmarshal([]byte(out.PrimaryOutput), &resp)
	if responseErr == nil && len(resp.Errors) > 0 {
		return nil, nil, fmt.Errorf("plugin errors: %v", resp.Errors)
	}

	var primaryPath string
	var primaryContent []byte
	if responseErr == nil && resp.OutputPath != "" {
		primaryPath = resp.OutputPath
		primaryContent = []byte(resp.Content)
	} else {
		primaryPath, err = buildstate.FallbackOutputPath(projectRoot, file, ".go")
		if err != nil {
			return nil, nil, err
		}
		primaryContent = []byte(out.PrimaryOutput)
	}
	outputs := make([]buildstate.Output, 0, len(out.AuxFiles)+1)
	newAux := make([]string, 0, len(out.AuxFiles))
	for _, aux := range out.AuxFiles {
		outputs = append(outputs, buildstate.Output{Path: aux.Path, Content: aux.Content, Description: "aux file"})
		newAux = append(newAux, aux.Path)
	}
	outputs = append(outputs, buildstate.Output{Path: primaryPath, Content: primaryContent, Description: "primary output"})
	entry := buildstate.FileEntry{Hash: contentHash, PrimaryOutput: primaryPath, AuxiliaryOutputs: newAux}
	return &buildstate.Publication{Source: absPath, Entry: entry, Outputs: outputs, Writer: pluginWriter(cfg.Resolution.Generate, pluginCfg)}, manifest, nil
}

// pluginWriter is a configured plugin as a confined generator: it writes and
// cleans up only in its declared outputs.
func pluginWriter(name string, plugin config.Plugin) *buildstate.Writer {
	writer := &buildstate.Writer{Name: name}
	for _, output := range plugin.Outputs {
		writer.Roots = append(writer.Roots, buildstate.OutputRoot{Dir: output.Dir, Suffixes: output.Suffixes})
	}
	return writer
}

// configuredWriters are the native Go generator and every configured plugin,
// so stale cleanup can confine any recorded build-state entry to its writer.
func configuredWriters() ([]buildstate.Writer, error) {
	cfg, err := config.Load("ktml.config.json")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}
	writers := []buildstate.Writer{buildstate.NativeWriter}
	for name, plugin := range cfg.Plugin {
		writers = append(writers, *pluginWriter(name, plugin))
	}
	return writers, nil
}

func fmtCmd() (resultErr error) {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: ktml fmt <file> [file]")
	}

	cfg, _ := config.Load("ktml.config.json")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var formatPlugin *wasm.Plugin
	var hasErrors bool
	if cfg != nil && len(cfg.Resolution.Format) > 0 {
		pluginName := cfg.Resolution.Format[0]
		if pluginCfg, ok := cfg.Plugin[pluginName]; ok {
			wasmBytes, err := os.ReadFile(pluginCfg.Path)
			if err == nil {
				proxy, acquireErr := acquireLSPManager(cfg)
				if acquireErr != nil {
					fmt.Fprintf(os.Stderr, "failed to acquire LSP manager: %v\n", acquireErr)
					hasErrors = true
				} else {
					defer func() { resultErr = errors.Join(resultErr, proxy.Close()) }()
					options := pluginOptions(cfg, pluginCfg)
					options.Proxy = proxy
					p, err := wasm.NewPluginWithOptions(ctx, wasmBytes, options)
					if err == nil {
						formatPlugin = p
						defer p.Close(ctx)
					}
				}
			}
		}
	}

	var files []string
	for _, arg := range os.Args[2:] {
		if arg == "./..." {
			filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() && (strings.HasSuffix(path, ".ktml") || compiler.IsSchemaPath(path)) {
					files = append(files, path)
				}
				return nil
			})
		} else {
			matches, err := filepath.Glob(arg)
			if err == nil && len(matches) > 0 {
				files = append(files, matches...)
			} else {
				files = append(files, arg)
			}
		}
	}

	for _, file := range files {
		if formatPlugin != nil {
			content, err := os.ReadFile(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to format %s: %v\n", file, err)
				hasErrors = true
				continue
			}
			var doc any
			func() {
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("parser panic: %v", r)
					}
				}()
				compileResult := compiler.CompileSyntax(file, content)
				doc = compileResult.Document
				if compileErr := compileResult.Err(); compileErr != nil {
					err = compileErr
				}
			}()
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to format %s: %v\n", file, err)
				hasErrors = true
				continue
			}
			astJSON, err := json.Marshal(doc)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to marshal AST for %s: %v\n", file, err)
				hasErrors = true
				continue
			}
			formattedBytes, err := formatPlugin.ExecuteFormat(ctx, astJSON)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to execute format plugin on %s: %v\n", file, err)
				hasErrors = true
				continue
			}
			if len(formattedBytes) > 0 {
				if err := os.WriteFile(file, formattedBytes, 0644); err != nil {
					fmt.Fprintf(os.Stderr, "failed to write formatted file %s: %v\n", file, err)
					hasErrors = true
				}
				continue
			}
		}

		if err := formatter.FormatFiles(file); err != nil {
			fmt.Fprintf(os.Stderr, "failed to format %s: %v\n", file, err)
			hasErrors = true
		}
	}

	if hasErrors {
		return fmt.Errorf("one or more files could not be formatted")
	}
	return nil
}

func lspCmd() (resultErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report := configReport()
	cfg, err := config.Load(configdiag.FileName)
	if err != nil {
		// The editor shows the error on ktml.config.json; no plugin can
		// load without a valid configuration.
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		report.Diagnostics = configdiag.Config(report.Source, err)
		lsp.StartServer(ctx, nil, report)
		return nil
	}
	var plugins []*wasm.Plugin
	proxy, err := acquireLSPManager(cfg)
	if err != nil {
		return fmt.Errorf("failed to acquire LSP manager: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, proxy.Close()) }()

	var targetPlugins []string
	for _, pName := range cfg.Resolution.LSP {
		if pName == "*" {
			for name := range cfg.Plugin {
				targetPlugins = append(targetPlugins, name)
			}
		} else {
			targetPlugins = append(targetPlugins, pName)
		}
	}

	for _, pName := range targetPlugins {
		pluginCfg, ok := cfg.Plugin[pName]
		if !ok {
			fmt.Fprintf(os.Stderr, "plugin '%s' not found in config\n", pName)
			continue
		}

		wasmBytes, err := os.ReadFile(pluginCfg.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read plugin wasm %s: %v\n", pluginCfg.Path, err)
			report.Diagnostics = append(report.Diagnostics, configdiag.Plugin(report.Source, pName, fmt.Errorf("failed to read plugin wasm %s: %w", pluginCfg.Path, err))...)
			continue
		}

		options := pluginOptions(cfg, pluginCfg)
		options.Proxy = proxy
		p, err := wasm.NewPluginWithOptions(ctx, wasmBytes, options)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load plugin %s: %v\n", pName, err)
			report.Diagnostics = append(report.Diagnostics, configdiag.Plugin(report.Source, pName, err)...)
			continue
		}
		if !p.Manifest().ServesLSP() {
			fmt.Fprintf(os.Stderr, "plugin %s declares no LSP capability; it is not loaded for ktml lsp\n", pName)
			_ = p.Close(ctx)
			continue
		}
		defer p.Close(ctx)
		plugins = append(plugins, p)
	}

	var adapters []lsp.Adapter
	if cfg.Tailwind != nil {
		adapter, diagnostics := tailwindAdapter(cfg.Tailwind, report.Source)
		report.Diagnostics = append(report.Diagnostics, diagnostics...)
		defer func() { resultErr = errors.Join(resultErr, adapter.Close()) }()
		adapters = append(adapters, adapter)
	}

	if len(plugins) == 0 && len(adapters) == 0 && len(report.Diagnostics) == 0 {
		return fmt.Errorf("no LSP plugins loaded")
	}
	if cfg.Gopls == nil || !cfg.Gopls.Disabled {
		adapter := goplsAdapter(cfg.Gopls)
		defer func() { resultErr = errors.Join(resultErr, adapter.Close()) }()
		adapters = append(adapters, adapter)
	}
	// ktml vet's findings show on the handwritten Go that has them.
	if root, err := os.Getwd(); err == nil {
		adapter := lsp.NewVetAdapter(root)
		defer func() { resultErr = errors.Join(resultErr, adapter.Close()) }()
		adapters = append(adapters, adapter)
	}

	lsp.StartServer(ctx, plugins, report, adapters...)
	return nil
}

// tailwindAdapter returns the Tailwind CSS adapter for the working
// directory, and a diagnostic on tailwind.command when the language server
// is not installed: KTML's own class checks still run without it.
func tailwindAdapter(settings *config.Tailwind, source []byte) (*lsp.TailwindAdapter, []configdiag.Diagnostic) {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	adapter := lsp.NewTailwindAdapter(root, settings)
	command := settings.TailwindCommand()
	if _, err := exec.LookPath(command[0]); err != nil {
		path := []string{"tailwind", "command"}
		if len(settings.Command) == 0 {
			path = []string{"tailwind"}
		}
		start, end := configdiag.Locate(source, path)
		return adapter, []configdiag.Diagnostic{{Start: start, End: end, Code: "tailwind-server", Message: fmt.Sprintf("Tailwind CSS language server %q not found: install it with npm install -g @tailwindcss/language-server@%s, or set tailwind.command", command[0], lsp.TailwindPinnedVersion)}}
	}
	return adapter, nil
}

// goplsAdapter returns the gopls adapter for the working directory. It
// starts gopls with the first KTML document of a Go module, shared through
// the configured daemon, and tells the user once if gopls cannot start.
func goplsAdapter(settings *config.Gopls) *lsp.GoplsAdapter {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	return lsp.NewGoplsAdapter(root, settings)
}

// configReport reads ktml.config.json for the LSP's configuration
// diagnostics. A missing file has none.
// pluginOptions are the load options of a configured plugin: its own
// allow_read, settings, and limits, and the project settings the host
// passes every plugin under "_ktml", such as the HTML analyzer's
// extension attributes.
func pluginOptions(cfg *config.Config, pluginCfg config.Plugin) wasm.Options {
	options := wasm.Options{AllowRead: pluginCfg.AllowRead, Settings: pluginCfg.Settings, Limits: wasm.LimitsFromConfig(pluginCfg.Limits)}
	if names := cfg.HTML.ExtensionAttributeNames(); len(names) > 0 {
		options.Host = map[string]any{"html": map[string]any{"extension_attributes": names}}
	}
	return options
}

func configReport() lsp.ConfigReport {
	report := lsp.ConfigReport{}
	source, err := os.ReadFile(configdiag.FileName)
	if err != nil {
		return report
	}
	path, err := filepath.Abs(configdiag.FileName)
	if err != nil {
		return report
	}
	report.URI, report.Source = (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(), source
	return report
}

// newControlToken returns a random per-run token for the app's control
// channel.
func newControlToken() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// generateKTMLFiles walks the project's KTML sources. Like the go command and
// the compiler's workspace index, it skips hidden, vendor, testdata, and
// node_modules directories.
func generateKTMLFiles(root string, generate func(string) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		// Hidden files are editor and formatter temp copies, never sources.
		if !strings.HasSuffix(path, ".ktml") && !compiler.IsSchemaPath(path) || compiler.IsStoryPath(path) || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		return generate(path)
	})
}

func devCmd() {
	slog.Debug("Starting devCmd")
	flags, err := parseDevFlags(os.Args[2:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	cfg, err := config.Load("ktml.config.json")
	if err != nil {
		slog.Error("Failed to load dev config", "error", err)
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if cfg.Dev == nil {
		slog.Error("dev configuration missing")
		fmt.Fprintln(os.Stderr, "dev configuration missing")
		os.Exit(1)
	}
	plan, err := planDev(cfg, ".", flags, os.LookupEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ktml dev: %v\n", err)
		os.Exit(1)
	}
	if flags.dryRun {
		writeDevPlan(os.Stdout, plan)
		return
	}
	cfg = plan.cfg
	if cfg.Dev.EnvFile != "" {
		fmt.Fprintf(os.Stderr, liveWarning, cfg.Dev.EnvFile)
	}
	if flags.captureRaw {
		fmt.Fprintln(os.Stderr, "WARNING: --capture-raw writes raw page values (names, contact details, identifiers) into story files; keep them local and never commit them")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Debug("Received termination signal, shutting down")
		cancel()
	}()

	slog.Debug("Clearing .ktml cache directory")
	if err := os.RemoveAll(".ktml"); err != nil {
		slog.Error("Failed to clear .ktml cache", "error", err)
		fmt.Fprintf(os.Stderr, "failed to clear .ktml cache: %v\n", err)
		os.Exit(1)
	}

	// The proxy's port is taken before anything starts, so a busy port
	// fails ktml dev instead of leaving it running without its proxy.
	proxyListener, err := net.Listen("tcp", devProxyAddr(cfg.Dev.Port))
	if err != nil {
		slog.Error("Failed to listen for the dev proxy", "error", err)
		fmt.Fprintf(os.Stderr, "failed to start the dev proxy: %v\n", err)
		os.Exit(1)
	}

	// The Component Explorer runs in-process beside the dev loop, with its
	// own server and watcher, and stops when ctx ends. It starts while the
	// initial generation runs.
	var explorerRun *devExplorer
	explorerStarted := make(chan error, 1)
	if cfg.Dev.Explorer != nil {
		slog.Debug("Starting the Component Explorer", "addr", cfg.Dev.Explorer.Addr)
		go func() {
			var err error
			explorerRun, err = startDevExplorer(ctx, os.Stdout, ".", cfg)
			explorerStarted <- err
		}()
	} else {
		explorerStarted <- nil
	}

	slog.Debug("Initializing livereload server", "proxyTarget", cfg.Dev.ProxyTarget)
	lrServer := livereload.NewServer(cfg.Dev.ProxyTarget)
	var pickerHandler atomic.Pointer[picker.Handler]

	proxyServer := newDevProxyServer(proxyListener.Addr().String(), devProxyHandler(devRoutes(lrServer, &pickerHandler)))
	go func() {
		slog.Debug("Starting dev HTTP server", "addr", proxyServer.Addr)
		if err := proxyServer.Serve(proxyListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Dev HTTP server error", "error", err)
			fmt.Fprintf(os.Stderr, "http server error: %v\n", err)
		}
	}()
	defer proxyServer.Close()

	slog.Debug("Performing initial generation of all .ktml files")
	var sources []string
	if err := generateKTMLFiles(".", func(path string) error {
		sources = append(sources, path)
		return nil
	}); err != nil {
		slog.Error("Initial generation failed", "error", err)
		fmt.Fprintf(os.Stderr, "initial generation failed: %v\n", err)
		os.Exit(1)
	}
	generationErr := generateFilesContext(ctx, sources, true)
	if err := <-explorerStarted; err != nil {
		slog.Error("Failed to start the Component Explorer", "error", err)
		fmt.Fprintf(os.Stderr, "failed to start the Component Explorer: %v\n", err)
		os.Exit(1)
	}
	if generationErr != nil {
		slog.Error("Initial generation failed", "error", generationErr)
		fmt.Fprintf(os.Stderr, "initial generation failed: %v\n", generationErr)
		os.Exit(1)
	}

	run := runner.New(cfg.Dev.Command)
	if len(cfg.Dev.Build) > 0 {
		run = runner.NewBuild(cfg.Dev.Build, cfg.Dev.Command, filepath.Join(".ktml", "dev"))
		if !runner.BuildsDevelopmentVariant(cfg.Dev.Build) {
			slog.Warn("dev.build does not pass -tags ktmldev, so the app links no development interpreter: every component change rebuilds and reloads. Add \"-tags\", \"ktmldev\" to dev.build and wrap the app's handler with hmr.Install in a ktmldev-only file (ADR-0004)")
		}
	} else {
		slog.Warn("dev.build is not configured; backend restarts stop the running backend before its replacement is ready")
	}
	// The app's development control channel (ADR-0004): a per-run token and
	// the workspace root for its interpreter.
	controlToken, err := newControlToken()
	if err != nil {
		slog.Error("Failed to create the dev control token", "error", err)
		fmt.Fprintf(os.Stderr, "failed to create the dev control token: %v\n", err)
		os.Exit(1)
	}
	workspaceRoot, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve the workspace root: %v\n", err)
		os.Exit(1)
	}
	run.SetEnv(plan.processEnv(controlToken, workspaceRoot)...)
	controlClient := control.New(cfg.Dev.ProxyTarget, controlToken)
	if cfg.Dev.RemoteProxyTarget() {
		// The config allows this only with the opt-in and https.
		controlClient.AllowRemote = true
		slog.Warn("dev.proxy_target is not loopback: ktml dev sends the app's development control token to it over https, and the app's control channel must be reachable only by you", "proxy_target", cfg.Dev.ProxyTarget)
		fmt.Fprintf(os.Stderr, "WARNING: dev.proxy_target %s is not loopback (dev.allow_remote_proxy_target is set); ktml dev sends the app's development control token to it\n", cfg.Dev.ProxyTarget)
	}
	if cfg.Dev.PickerEnabled() && explorerRun != nil {
		handler := picker.New(explorerRun.server, controlClient.Components, explorerRun.url)
		editor := cfg.Dev.PickerEditor()
		if editor != nil {
			handler.SetEditor(picker.CommandEditor(workspaceRoot, editor))
		}
		pickerHandler.Store(handler)
		lrServer.SetPicker(livereload.Picker{Hotkey: cfg.Dev.PickerHotkey(), Explorer: explorerRun.url, Editor: editor != nil})
		fmt.Fprintf(os.Stdout, "KTML component picker: press %s on a page to pick an element and open its component in the Explorer, or Ctrl+click it to open its source\n", cfg.Dev.PickerHotkey())
	}
	lrServer.OnSession(func(session string, connected bool) {
		go func() {
			sessionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := controlClient.Session(sessionCtx, session, connected); err != nil {
				slog.Debug("The app did not record a page session", "session", session, "connected", connected, "error", err)
			}
		}()
	})
	hub := dev.NewHub(lrServer, run, cfg.Dev.Port, func(batch dev.Batch) error {
		return generateBatchContext(ctx, batch)
	})
	hub.SetControl(controlClient)
	hub.SetReadyTimeout(time.Duration(cfg.Dev.ReadyTimeoutMS) * time.Millisecond)
	if err := hub.TrackFiles(sources); err != nil {
		slog.Error("Failed to seed dev ASTs", "error", err)
		fmt.Fprintf(os.Stderr, "failed to seed dev ASTs: %v\n", err)
		os.Exit(1)
	}

	slog.Debug("Starting runner", "command", cfg.Dev.Command)
	if err := run.Start(ctx); err != nil {
		slog.Error("Failed to start backend", "error", err)
		fmt.Fprintf(os.Stderr, "failed to start backend: %v\n", err)
	}
	defer run.Stop()
	if flags.captureDir != "" {
		go reportCapture(ctx, os.Stderr, controlClient, time.Duration(cfg.Dev.ReadyTimeoutMS)*time.Millisecond)
	}

	slog.Debug("Starting watcher", "watch", cfg.Dev.Watch, "restartWatch", cfg.Dev.RestartWatch, "reloadWatch", cfg.Dev.ReloadWatch)
	w := watcher.New(".", cfg.Dev.Watch, cfg.Dev.RestartWatch, 50*time.Millisecond)
	w.SetAssetWatch(cfg.Dev.ReloadWatch)
	events := w.Start(ctx)

	slog.Debug("Starting hub listen loop")
	hub.Listen(ctx, events)
	cancel()
	if explorerRun == nil {
		return
	}
	if err := explorerRun.wait(); err != nil {
		slog.Error("Component Explorer error", "error", err)
		fmt.Fprintf(os.Stderr, "component explorer error: %v\n", err)
	}
}

// devProxyAddr is the dev proxy's listen address: loopback only, like the
// Explorer, since the proxy serves the app, its diagnostics stream, and the
// livereload routes without authentication.
func devProxyAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// newDevProxyServer is the dev proxy's server. Request headers must arrive
// within 10 seconds; there is no write timeout, since the livereload stream
// stays open.
func newDevProxyServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
}

// devRoutes serves the component picker's routes, once ktml dev enables it,
// and proxies everything else to the application.
func devRoutes(proxy http.Handler, pickerHandler *atomic.Pointer[picker.Handler]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, picker.Prefix) {
			if handler := pickerHandler.Load(); handler != nil {
				handler.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// devProxyHandler answers only requests for a loopback host, so a page on
// another origin cannot reach the proxy through DNS rebinding.
func devProxyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if split, _, err := net.SplitHostPort(r.Host); err == nil {
			host = split
		}
		if !config.LoopbackHost(host) {
			http.Error(w, "ktml dev only answers loopback hosts", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
