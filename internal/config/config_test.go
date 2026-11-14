package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/configdiag"
)

func createTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_DevValid(t *testing.T) {
	content := `{
		"dev": {
			"proxy_target": "http://localhost:8080",
			"command": ["npm", "run", "dev"],
			"port": 3000,
			"watch": ["src/**/*.js"],
			"restart_watch": ["config.js"]
		}
	}`
	path := createTempConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Dev == nil {
		t.Fatal("expected Dev config to be populated")
	}
	if cfg.Dev.ProxyTarget != "http://localhost:8080" {
		t.Errorf("expected ProxyTarget 'http://localhost:8080', got %q", cfg.Dev.ProxyTarget)
	}
	if len(cfg.Dev.Command) != 3 || cfg.Dev.Command[0] != "npm" {
		t.Errorf("expected Command ['npm', 'run', 'dev'], got %v", cfg.Dev.Command)
	}
	if cfg.Dev.Port != 3000 {
		t.Errorf("expected Port 3000, got %d", cfg.Dev.Port)
	}
	if len(cfg.Dev.Watch) != 1 || cfg.Dev.Watch[0] != "src/**/*.js" {
		t.Errorf("expected Watch ['src/**/*.js'], got %v", cfg.Dev.Watch)
	}
	if len(cfg.Dev.RestartWatch) != 1 || cfg.Dev.RestartWatch[0] != "config.js" {
		t.Errorf("expected RestartWatch ['config.js'], got %v", cfg.Dev.RestartWatch)
	}
}

func TestLoad_DevMissingPort(t *testing.T) {
	content := `{
		"dev": {
			"proxy_target": "http://localhost:8080",
			"command": ["npm", "run", "dev"]
		}
	}`
	path := createTempConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Dev == nil {
		t.Fatal("expected Dev config to be populated")
	}
	if cfg.Dev.Port != 7331 {
		t.Errorf("expected default Port 7331, got %d", cfg.Dev.Port)
	}
}

func TestLoad_DevMissingProxyTarget(t *testing.T) {
	content := `{
		"dev": {
			"command": ["npm", "run", "dev"],
			"port": 3000
		}
	}`
	path := createTempConfig(t, content)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "proxy target") {
		t.Errorf("expected error about proxy target, got %v", err)
	}
}

func TestLoad_DevMissingCommand(t *testing.T) {
	content := `{
		"dev": {
			"proxy_target": "http://localhost:8080",
			"port": 3000
		}
	}`
	path := createTempConfig(t, content)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("expected error about command, got %v", err)
	}
}

func TestLoad_DevNone(t *testing.T) {
	content := `{
		"package_dir": "."
	}`
	path := createTempConfig(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Dev != nil {
		t.Errorf("expected Dev config to be nil, got %v", cfg.Dev)
	}
}

func TestGenerationConfig_DevMode(t *testing.T) {
	content := `{
		"environment": "test",
		"file_path": "/tmp/test.ktml",
		"settings": {"foo": "bar"},
		"dev_mode": true
	}`
	path := createTempConfig(t, content)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	var genCfg GenerationConfig
	if err := json.Unmarshal(data, &genCfg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !genCfg.DevMode {
		t.Errorf("expected DevMode to be true")
	}

	out, err := json.Marshal(genCfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var genCfg2 GenerationConfig
	if err := json.Unmarshal(out, &genCfg2); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !genCfg2.DevMode {
		t.Errorf("expected DevMode to be true after roundtrip")
	}
}

func TestLoad_GIVENMinimalHTMXPluginConfig_WHENLoaded_THENPreservesChiDirectory(t *testing.T) {
	path := createTempConfig(t, `{
		"plugin": {
			"htmx": {
				"path": "./plugins/htmx.wasm",
				"allow_read": ["./internal/web"],
				"settings": {
					"chi": {"dir": "./internal/web"}
				}
			}
		},
		"resolution": {"lsp": ["htmx"]}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	plugin, ok := cfg.Plugin["htmx"]
	if !ok {
		t.Fatal("HTMX plugin config not loaded")
	}
	chi, ok := plugin.Settings["chi"].(map[string]any)
	if !ok {
		t.Fatalf("chi settings = %#v", plugin.Settings["chi"])
	}
	if got, want := chi["dir"], "./internal/web"; got != want {
		t.Errorf("chi.dir = %#v, want %q", got, want)
	}
	if len(cfg.Resolution.LSP) != 1 || cfg.Resolution.LSP[0] != "htmx" {
		t.Errorf("resolution.lsp = %#v", cfg.Resolution.LSP)
	}
}

func TestLoad_GIVENDevBuild_WHENLoaded_THENRequiresBinaryPlaceholderInBuildAndCommand(t *testing.T) {
	tests := []struct {
		name    string
		build   string
		command string
		wantErr bool
	}{
		{name: "build then run", build: `["go", "build", "-o", "{binary}", "./cmd/server"]`, command: `["{binary}"]`},
		{name: "build without placeholder", build: `["go", "build", "./cmd/server"]`, command: `["{binary}"]`, wantErr: true},
		{name: "command without placeholder", build: `["go", "build", "-o", "{binary}", "."]`, command: `["go", "run", "."]`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := createTempConfig(t, `{"dev": {"proxy_target": "http://localhost:8080", "build": `+test.build+`, "command": `+test.command+`}}`)

			cfg, err := Load(path)

			if test.wantErr {
				if err == nil {
					t.Fatal("Load() accepted a build-then-run config without {binary}")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(cfg.Dev.Build) != 5 || cfg.Dev.Build[3] != "{binary}" {
				t.Errorf("Build = %v", cfg.Dev.Build)
			}
		})
	}
}

func TestLoad_GIVENGenerateBackend_WHENLoaded_THENDefaultsToTheNativeGoBackend(t *testing.T) {
	tests := []struct {
		name, config, want string
	}{
		{name: "no file", want: GoBackend},
		{name: "no resolution", config: `{}`, want: GoBackend},
		{name: "single plugin is not selected implicitly", config: `{"plugin":{"gohtml":{"path":"./gohtml.wasm"}}}`, want: GoBackend},
		{name: "explicit plugin", config: `{"plugin":{"gohtml":{"path":"./gohtml.wasm"}},"resolution":{"generate":"gohtml"}}`, want: "gohtml"},
		{name: "explicit go", config: `{"resolution":{"generate":"go"}}`, want: GoBackend},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ktml.config.json")
			if tt.config != "" {
				path = createTempConfig(t, tt.config)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Resolution.Generate != tt.want {
				t.Errorf("resolution.generate = %q, want %q", cfg.Resolution.Generate, tt.want)
			}
		})
	}
}

func TestLoad_GIVENPluginNamedGo_WHENLoaded_THENRejectsTheReservedName(t *testing.T) {
	path := createTempConfig(t, `{"plugin":{"go":{"path":"./go.wasm"}}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Load() error = %v, want the reserved-name error", err)
	}
}

func TestLoad_GIVENPluginLimits_WHENLoaded_THENValidatesThem(t *testing.T) {
	tests := []struct {
		name, limits, wantErr string
	}{
		{"valid", `{"memory_mb":256,"timeout_ms":5000,"output_mb":8}`, ""},
		{"negative", `{"timeout_ms":-1}`, `plugin "htmx": limits must not be negative`},
		{"too much memory", `{"memory_mb":8192}`, `plugin "htmx": limits.memory_mb must be at most 4096`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ktml.config.json")
			if err := os.WriteFile(path, []byte(`{"plugin":{"htmx":{"path":"htmx.wasm","limits":`+test.limits+`}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("Load() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := cfg.Plugin["htmx"].Limits; got != (PluginLimits{MemoryMB: 256, TimeoutMS: 5000, OutputMB: 8}) {
				t.Errorf("limits = %#v", got)
			}
		})
	}
}

func TestParse_GIVENExplorerAssets_WHENOutsideTheProjectOrRemote_THENRejectsAtTheirPath(t *testing.T) {
	tests := []struct {
		name, source, path string
	}{
		{"escaping dir", `{"explorer": {"static": {"dir": "../x", "url": "/static"}}}`, "explorer.static.dir"},
		{"root url", `{"explorer": {"static": {"dir": "static", "url": "/"}}}`, "explorer.static.url"},
		{"remote script", `{"explorer": {"scripts": ["https://cdn.example/htmx.js"]}}`, "explorer.scripts[0]"},
		{"protocol-relative stylesheet", `{"explorer": {"stylesheets": ["/a.css", "//cdn.example/a.css"]}}`, "explorer.stylesheets[1]"},
	}
	for _, tt := range tests {
		_, err := Parse([]byte(tt.source))
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != tt.path {
			t.Errorf("%s: error = %v, want a path error at %s", tt.name, err, tt.path)
		}
	}
	cfg, err := Parse([]byte(`{"explorer": {"static": {"dir": "static", "url": "/static"}, "stylesheets": ["/static/css/main.css"]}}`))
	if err != nil || cfg.Explorer.Static.Dir != "static" || cfg.Explorer.Stylesheets[0] != "/static/css/main.css" {
		t.Fatalf("Parse(valid) = %+v, %v", cfg, err)
	}
}

func TestParse_GIVENDevExplorer_WHENLoopbackAddress_THENKeepsIt(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:7332", "localhost:0", "[::1]:7400"} {
		cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"], "explorer": {"addr": "` + addr + `"}}}`))
		if err != nil {
			t.Fatalf("Parse(%s) error = %v", addr, err)
		}
		if cfg.Dev.Explorer == nil || cfg.Dev.Explorer.Addr != addr {
			t.Errorf("Parse(%s) dev.explorer = %+v", addr, cfg.Dev.Explorer)
		}
	}
}

func TestParse_GIVENDevExplorerWithoutAddr_WHENLoaded_THENDefaultsToTheExploreAddress(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"], "explorer": {}}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Dev.Explorer == nil || cfg.Dev.Explorer.Addr != DefaultExplorerAddr || DefaultExplorerAddr != "127.0.0.1:7332" {
		t.Errorf("dev.explorer = %+v, want addr %s", cfg.Dev.Explorer, DefaultExplorerAddr)
	}
}

func TestParse_GIVENNoDevExplorer_WHENLoaded_THENLeavesItUnset(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"]}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Dev.Explorer != nil {
		t.Errorf("dev.explorer = %+v, want nil", cfg.Dev.Explorer)
	}
}

func TestParse_GIVENDevExplorerAddress_WHENNotLoopbackOrMalformed_THENRejectsAtTheAddr(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7332", ":7332", "192.168.1.10:7332", "example.com:7332", "127.0.0.1"} {
		_, err := Parse([]byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"], "explorer": {"addr": "` + addr + `"}}}`))
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != "dev.explorer.addr" {
			t.Errorf("Parse(%q) error = %v, want a path error at dev.explorer.addr", addr, err)
			continue
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Errorf("Parse(%q) error = %q, want it to name the loopback rule", addr, err)
		}
	}
}

func TestParse_GIVENDevExplorerUnknownKey_WHENLoaded_THENIgnoresItLikeTheRestOfDev(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"], "unknown": 1, "explorer": {"addr": "127.0.0.1:0", "unknown": true}}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Dev.Explorer.Addr != "127.0.0.1:0" {
		t.Errorf("dev.explorer.addr = %q", cfg.Dev.Explorer.Addr)
	}
}

func TestLoopbackHost_GIVENHosts_WHENChecked_THENAcceptsOnlyLoopback(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true, "": false, "0.0.0.0": false, "10.0.0.1": false, "example.com": false} {
		if got := LoopbackHost(host); got != want {
			t.Errorf("LoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestParse_GIVENNonLoopbackDevExplorer_WHENReportedToTheLSP_THENRangesAtTheAddrValue(t *testing.T) {
	source := []byte(`{"dev": {"proxy_target": "http://localhost:8080", "command": ["app"], "explorer": {"addr": "0.0.0.0:7332"}}}`)
	_, err := Parse(source)

	diagnostics := configdiag.Config(source, err)

	if len(diagnostics) != 1 || string(source[diagnostics[0].Start:diagnostics[0].End]) != `"0.0.0.0:7332"` || diagnostics[0].Code != "config" {
		t.Fatalf("diagnostics = %#v, want one config diagnostic on the addr value", diagnostics)
	}
}

func TestParse_GIVENHTMLExtensionAttributes_WHENValid_THENKeepsThem(t *testing.T) {
	cfg, err := Parse([]byte(`{"html": {"extension_attributes": ["hx-require", "hx-my-ext"]}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.HTML == nil || strings.Join(cfg.HTML.ExtensionAttributes, ",") != "hx-require,hx-my-ext" {
		t.Errorf("html = %+v", cfg.HTML)
	}
}

func TestParse_GIVENHTMLExtensionAttributes_WHENNotAnHXName_THENRejectsAtTheName(t *testing.T) {
	tests := []struct{ name, value, path string }{
		{"custom hook attribute", `["hx-require", "x-require"]`, "html.extension_attributes[1]"},
		{"data- prefixed", `["data-hx-require"]`, "html.extension_attributes[0]"},
		{"bare prefix", `["hx-"]`, "html.extension_attributes[0]"},
		{"modifier", `["hx-require:inherited"]`, "html.extension_attributes[0]"},
		{"upper case", `["hx-Require"]`, "html.extension_attributes[0]"},
	}
	for _, tt := range tests {
		source := []byte(`{"html": {"extension_attributes": ` + tt.value + `}}`)
		_, err := Parse(source)
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != tt.path {
			t.Errorf("%s: error = %v, want a path error at %s", tt.name, err, tt.path)
			continue
		}
		if !strings.Contains(err.Error(), "hx-") {
			t.Errorf("%s: error = %q, want it to name the hx- rule", tt.name, err)
		}
		if diagnostics := configdiag.Config(source, err); len(diagnostics) != 1 || diagnostics[0].Code != "config" {
			t.Errorf("%s: diagnostics = %#v, want one config diagnostic", tt.name, diagnostics)
		}
	}
}

func TestParse_GIVENPluginOutputs_WHENValid_THENKeepsThem(t *testing.T) {
	cfg, err := Parse([]byte(`{"plugin": {"gohtml": {"path": "g.wasm", "outputs": [{"dir": "."	, "suffixes": [".html", ".ktml.go"]}, {"dir": "web/gen"}]}}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	got := cfg.Plugin["gohtml"].Outputs
	if len(got) != 2 || got[0].Dir != "." || len(got[0].Suffixes) != 2 || got[1].Dir != "web/gen" {
		t.Fatalf("outputs = %#v", got)
	}
}

func TestParse_GIVENPluginOutputs_WHENEscapingOrDenied_THENRejectsAtTheValue(t *testing.T) {
	tests := []struct{ name, value, path string }{
		{"empty dir", `[{"dir": ""}]`, "plugin.p.outputs[0].dir"},
		{"absolute dir", `[{"dir": "/tmp/gen"}]`, "plugin.p.outputs[0].dir"},
		{"escaping dir", `[{"dir": "gen"}, {"dir": "../gen"}]`, "plugin.p.outputs[1].dir"},
		{"git dir", `[{"dir": ".git/hooks"}]`, "plugin.p.outputs[0].dir"},
		{"state dir", `[{"dir": ".ktml"}]`, "plugin.p.outputs[0].dir"},
		{"suffix with a separator", `[{"dir": ".", "suffixes": ["x/y.go"]}]`, "plugin.p.outputs[0].suffixes[0]"},
		{"empty suffix", `[{"dir": ".", "suffixes": [""]}]`, "plugin.p.outputs[0].suffixes[0]"},
		{"source suffix", `[{"dir": ".", "suffixes": [".ktml"]}]`, "plugin.p.outputs[0].suffixes[0]"},
	}
	for _, tt := range tests {
		source := []byte(`{"plugin": {"p": {"path": "p.wasm", "outputs": ` + tt.value + `}}}`)
		_, err := Parse(source)
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != tt.path {
			t.Errorf("%s: error = %v, want a path error at %s", tt.name, err, tt.path)
			continue
		}
		if !strings.Contains(err.Error(), `plugin "p"`) {
			t.Errorf("%s: error = %q, want it to name the plugin", tt.name, err)
		}
	}
}

func TestParse_GIVENDevReadyTimeout_WHENSetOrNegative_THENKeepsOrRejectsIt(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:1", "command": ["x"], "ready_timeout_ms": 120000}}`))
	if err != nil || cfg.Dev.ReadyTimeoutMS != 120000 {
		t.Fatalf("Parse() = %v, %v; want ready_timeout_ms 120000", cfg, err)
	}
	_, err = Parse([]byte(`{"dev": {"proxy_target": "http://127.0.0.1:1", "command": ["x"], "ready_timeout_ms": -1}}`))
	var pathError *configdiag.PathError
	if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != "dev.ready_timeout_ms" {
		t.Fatalf("negative ready_timeout_ms error = %v, want a path error at dev.ready_timeout_ms", err)
	}
}

func TestParse_GIVENDevProxyTarget_WHENNotLoopback_THENRequiresTheOptInAndHTTPS(t *testing.T) {
	tests := []struct {
		name, dev string
		wantErr   bool
	}{
		{"loopback http", `"proxy_target": "http://127.0.0.1:8080"`, false},
		{"localhost http", `"proxy_target": "http://localhost:8080"`, false},
		{"ipv6 loopback", `"proxy_target": "http://[::1]:8080"`, false},
		{"remote without opt-in", `"proxy_target": "https://app.example.com"`, true},
		{"private address without opt-in", `"proxy_target": "http://10.0.0.5:8080"`, true},
		{"unspecified address", `"proxy_target": "http://0.0.0.0:8080"`, true},
		{"remote http with opt-in", `"proxy_target": "http://app.example.com", "allow_remote_proxy_target": true`, true},
		{"remote https with opt-in", `"proxy_target": "https://app.example.com", "allow_remote_proxy_target": true`, false},
		{"not http", `"proxy_target": "ftp://127.0.0.1:8080"`, true},
		{"no host", `"proxy_target": "http://"`, true},
		{"credentials", `"proxy_target": "http://user:pw@127.0.0.1:8080"`, true},
	}
	for _, tt := range tests {
		source := []byte(`{"dev": {"command": ["app"], ` + tt.dev + `}}`)
		_, err := Parse(source)
		if !tt.wantErr {
			if err != nil {
				t.Errorf("%s: Parse() error = %v", tt.name, err)
			}
			continue
		}
		var pathError *configdiag.PathError
		if !errors.As(err, &pathError) || configdiag.FormatPath(pathError.Path) != "dev.proxy_target" {
			t.Errorf("%s: error = %v, want a path error at dev.proxy_target", tt.name, err)
		}
	}
}

func TestParse_GIVENRemoteProxyTargetOptIn_WHENParsed_THENReportsItAsRemote(t *testing.T) {
	cfg, err := Parse([]byte(`{"dev": {"command": ["app"], "proxy_target": "https://app.example.com", "allow_remote_proxy_target": true}}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !cfg.Dev.AllowRemoteProxyTarget || !cfg.Dev.RemoteProxyTarget() {
		t.Errorf("dev = %+v, want an opted-in remote proxy target", cfg.Dev)
	}
	cfg, err = Parse([]byte(`{"dev": {"command": ["app"], "proxy_target": "http://localhost:8080", "allow_remote_proxy_target": true}}`))
	if err != nil || cfg.Dev.RemoteProxyTarget() {
		t.Errorf("loopback target with the opt-in: %v, remote = %v; want loopback", err, cfg != nil && cfg.Dev.RemoteProxyTarget())
	}
}
