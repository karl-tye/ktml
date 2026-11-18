package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/configdiag"
)

type Dev struct {
	ProxyTarget string   `json:"proxy_target"`
	Command     []string `json:"command"`
	// Build, when set, builds the backend into the {binary} path before the
	// running backend is replaced; Command then runs {binary}.
	Build        []string `json:"build,omitempty"`
	Port         int      `json:"port"`
	Watch        []string `json:"watch"`
	RestartWatch []string `json:"restart_watch"`
	// ReloadWatch are static assets, such as stylesheets and images, that
	// pages reload without generation or a rebuild: a stylesheet is swapped
	// in place, anything else reloads the page. A file matching one is never
	// a restart_watch change.
	ReloadWatch []string `json:"reload_watch,omitempty"`
	// Explorer, when set, makes ktml dev also serve the Component Explorer
	// over the project root.
	Explorer *DevExplorer `json:"explorer,omitempty"`
	// ReadyTimeoutMS bounds how long a rebuilt backend may take to answer
	// before ktml dev restores the previous one; 0 means 5 seconds.
	ReadyTimeoutMS int `json:"ready_timeout_ms,omitempty"`
	// AllowRemoteProxyTarget permits a ProxyTarget outside loopback, which
	// must then be https: ktml dev sends the app's control token to it.
	AllowRemoteProxyTarget bool `json:"allow_remote_proxy_target,omitempty"`
	// Capture configures story capture (ktml dev --capture-stories).
	Capture *DevCapture `json:"capture,omitempty"`
	// EnvFile is an env file, relative to the project root, whose variables
	// ktml dev reads itself and sets for the application (see ParseEnvFile).
	EnvFile string `json:"env_file,omitempty"`
	// Env sets variables for the application, over EnvFile's.
	Env map[string]string `json:"env,omitempty"`
	// Picker configures the component picker in pages served by ktml dev.
	Picker *DevPicker `json:"picker,omitempty"`
	// Profiles are named variants of dev, chosen with ktml dev --profile:
	// each overrides some of ProfileKeys (see DevProfile).
	Profiles map[string]json.RawMessage `json:"profiles,omitempty"`

	// profiles are the resolved and validated Profiles.
	profiles map[string]*Dev
}

// ProfileKeys are the dev keys a profile may override. env is merged key by
// key; every other key replaces dev's value.
var ProfileKeys = []string{"command", "build", "env_file", "env", "port", "proxy_target", "explorer", "ready_timeout_ms"}

// DevPicker is the component picker: a hotkey in pages served by ktml dev
// that selects a component on the page and opens it in the Explorer. It is
// on whenever dev.explorer is set.
type DevPicker struct {
	// Hotkey toggles pick mode, such as "Alt+Shift+K" (the default): Ctrl,
	// Alt, Shift, or Meta, at least one of them not Shift, and a letter,
	// digit, or F1-F12.
	Hotkey string `json:"hotkey,omitempty"`
	// Disabled turns the picker off.
	Disabled bool `json:"disabled,omitempty"`
	// Editor opens a picked element's source on Ctrl+click: a command and
	// its arguments, run without a shell, where {file}, {line}, and {column}
	// are the source's absolute path, line, and column, such as
	// ["code", "-g", "{file}:{line}:{column}"]. Without it, Ctrl+click
	// uses ktml dev's $VISUAL or $EDITOR, or copies file:line:column.
	Editor []string `json:"editor,omitempty"`
}

// DefaultPickerHotkey toggles pick mode unless dev.picker.hotkey is set.
const DefaultPickerHotkey = "Alt+Shift+K"

// PickerEnabled reports whether pages served by ktml dev get the component
// picker: the Explorer is served and the picker is not disabled.
func (d *Dev) PickerEnabled() bool {
	return d != nil && d.Explorer != nil && (d.Picker == nil || !d.Picker.Disabled)
}

// PickerHotkey is the canonical hotkey that toggles pick mode.
func (d *Dev) PickerHotkey() string {
	if d == nil || d.Picker == nil || d.Picker.Hotkey == "" {
		return DefaultPickerHotkey
	}
	return d.Picker.Hotkey
}

// PickerEditor is the command that opens a picked element's source, or nil.
func (d *Dev) PickerEditor() []string {
	if d == nil || d.Picker == nil {
		return nil
	}
	return d.Picker.Editor
}

// canonicalHotkey validates a hotkey and spells it with its modifiers in
// Ctrl, Alt, Shift, Meta order.
func canonicalHotkey(hotkey string) (string, bool) {
	parts := strings.Split(hotkey, "+")
	if len(parts) < 2 {
		return "", false
	}
	order := []string{"Ctrl", "Alt", "Shift", "Meta"}
	held := make(map[string]bool)
	for _, part := range parts[:len(parts)-1] {
		found := false
		for _, modifier := range order {
			if strings.EqualFold(part, modifier) {
				held[modifier], found = true, true
			}
		}
		if !found {
			return "", false
		}
	}
	if !held["Ctrl"] && !held["Alt"] && !held["Meta"] {
		return "", false
	}
	key := strings.ToUpper(parts[len(parts)-1])
	valid := len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9')
	if number, err := strconv.Atoi(strings.TrimPrefix(key, "F")); err == nil && strings.HasPrefix(key, "F") && len(key) > 1 && number >= 1 && number <= 12 {
		valid = true
	}
	if !valid {
		return "", false
	}
	var spelled []string
	for _, modifier := range order {
		if held[modifier] {
			spelled = append(spelled, modifier)
		}
	}
	return strings.Join(append(spelled, key), "+"), true
}

// DevProfile returns dev with a named profile applied, or dev itself for
// "". The profile was validated by Parse.
func (c *Config) DevProfile(name string) (*Dev, error) {
	if c.Dev == nil {
		return nil, errors.New("dev configuration missing")
	}
	if name == "" {
		return c.Dev, nil
	}
	if profile, ok := c.Dev.profiles[name]; ok {
		return profile, nil
	}
	names := make([]string, 0, len(c.Dev.profiles))
	for known := range c.Dev.profiles {
		names = append(names, known)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("unknown dev profile %q: ktml.config.json defines no dev.profiles", name)
	}
	return nil, fmt.Errorf("unknown dev profile %q; dev.profiles defines %s", name, strings.Join(names, ", "))
}

// resolveProfile applies one profile to dev and validates the result. Errors
// in a key the profile sets are reported at that key, others at the profile.
func resolveProfile(dev *Dev, name string, raw json.RawMessage) (*Dev, error) {
	at := []string{"dev", "profiles", name}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, &configdiag.PathError{Path: at, Message: fmt.Sprintf("dev profile %q must be an object of dev keys", name)}
	}
	for key := range keys {
		if !slices.Contains(ProfileKeys, key) {
			return nil, &configdiag.PathError{Path: append(at, key), Message: fmt.Sprintf("dev profile %q cannot set %s; a profile may set %s", name, key, strings.Join(ProfileKeys, ", "))}
		}
	}
	base := *dev
	base.Profiles, base.profiles = nil, nil
	encoded, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	resolved := &Dev{}
	if err := json.Unmarshal(encoded, resolved); err != nil {
		return nil, err
	}
	// The profile's explorer replaces dev's, rather than merging into it.
	if _, ok := keys["explorer"]; ok {
		resolved.Explorer = nil
	}
	env := resolved.Env
	resolved.Env = nil
	if err := json.Unmarshal(raw, resolved); err != nil {
		return nil, &configdiag.PathError{Path: at, Message: fmt.Sprintf("dev profile %q: %v", name, err)}
	}
	if _, set := keys["env_file"]; set && resolved.EnvFile == "" {
		return nil, &configdiag.PathError{Path: append(at, "env_file"), Message: fmt.Sprintf("dev profile %q: env_file must name a file", name)}
	}
	if len(env) > 0 {
		merged := maps.Clone(env)
		maps.Copy(merged, resolved.Env)
		resolved.Env = merged
	}
	if err := validateDev(resolved); err != nil {
		var pathError *configdiag.PathError
		// dev itself is valid, so the profile made the error.
		if errors.As(err, &pathError) {
			path := slices.Clone(at)
			if len(pathError.Path) > 1 {
				if _, set := keys[pathError.Path[1]]; set {
					path = append(path, pathError.Path[1:]...)
				}
			}
			pathError.Path = path
			pathError.Message = fmt.Sprintf("dev profile %q: %s", name, pathError.Message)
		}
		return nil, err
	}
	return resolved, nil
}

// Environment returns the variables dev sets for the application: those of
// EnvFile, read relative to root, then Env's in name order, which override
// them. lookup expands references in the env file to variables it does not
// set; nil expands them to nothing.
func (d *Dev) Environment(root string, lookup func(string) (string, bool)) ([]EnvVar, error) {
	var vars []EnvVar
	if d.EnvFile != "" {
		path := d.EnvFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, filepath.FromSlash(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("dev env_file: %w", err)
		}
		parsed, err := ParseEnvFile(data, lookup)
		if err != nil {
			return nil, fmt.Errorf("dev env_file %s: %w", d.EnvFile, err)
		}
		for _, variable := range parsed {
			if strings.HasPrefix(variable.Key, ControlEnvPrefix) {
				return nil, fmt.Errorf("dev env_file %s: line %d sets %s; %s* variables belong to ktml dev", d.EnvFile, variable.Line, variable.Key, ControlEnvPrefix)
			}
			if _, overridden := d.Env[variable.Key]; overridden {
				continue
			}
			variable.Source = fmt.Sprintf("%s:%d", d.EnvFile, variable.Line)
			variable.Line = 0
			vars = append(vars, variable)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(d.Env)) {
		vars = append(vars, EnvVar{Key: key, Value: d.Env[key], Source: "dev.env"})
	}
	return vars, nil
}

// DevCapture is the project's consent to story capture. Capture writes the
// props of rendered pages into story files, so a project opts in only when
// its development application reads local data.
type DevCapture struct {
	// Allow must be true for ktml dev to capture stories.
	Allow bool `json:"allow"`
}

// CaptureAllowed reports whether the project allows story capture.
func (d *Dev) CaptureAllowed() bool {
	return d != nil && d.Capture != nil && d.Capture.Allow
}

// RemoteProxyTarget reports whether the validated ProxyTarget is outside
// loopback, which only AllowRemoteProxyTarget permits.
func (d *Dev) RemoteProxyTarget() bool {
	target, err := url.Parse(d.ProxyTarget)
	return err == nil && !LoopbackHost(target.Hostname())
}

// validateProxyTarget accepts an http(s) URL with a host and no credentials.
// ktml dev sends the app's control token to it, so a host outside loopback
// needs the explicit opt-in and https.
func validateProxyTarget(dev *Dev) error {
	fail := func(message string) error {
		return &configdiag.PathError{Path: []string{"dev", "proxy_target"}, Message: fmt.Sprintf("dev proxy_target %q %s", dev.ProxyTarget, message)}
	}
	target, err := url.Parse(dev.ProxyTarget)
	switch {
	case err != nil || target.Scheme != "http" && target.Scheme != "https" || target.Hostname() == "":
		return fail("must be an http or https URL with a host")
	case target.User != nil:
		return fail("must not contain credentials")
	case LoopbackHost(target.Hostname()):
		return nil
	case !dev.AllowRemoteProxyTarget:
		return fail("is not loopback (127.0.0.1, ::1, or localhost); ktml dev sends the app's control token to it. Set dev.allow_remote_proxy_target to true, with an https target, to use a remote app")
	case target.Scheme != "https":
		return fail("is not loopback, so it must use https: ktml dev sends the app's control token to it")
	}
	return nil
}

// DevExplorer is the Component Explorer that ktml dev serves beside the
// application.
type DevExplorer struct {
	// Addr is the loopback address the Explorer serves on, as ktml explore
	// -addr takes it. The default is DefaultExplorerAddr; port 0 picks a
	// free port.
	Addr string `json:"addr,omitempty"`
}

// DefaultExplorerAddr is the default address of the Component Explorer.
const DefaultExplorerAddr = "127.0.0.1:7332"

// LoopbackHost reports whether host, from a host:port address, names a
// loopback interface: localhost or a loopback IP address.
func LoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

type LSP struct {
	Command []string `json:"command"`
}

// Tailwind enables the Tailwind CSS adapter of ktml lsp, which runs the
// Tailwind CSS language server over virtual documents of the KTML sources.
type Tailwind struct {
	// Command starts the language server on stdio. The default is
	// tailwindcss-language-server --stdio.
	Command []string `json:"command,omitempty"`
	// Settings are the server's tailwindCSS settings, such as
	// classAttributes or lint severities.
	Settings map[string]any `json:"settings,omitempty"`
}

// DefaultTailwindCommand starts the Tailwind CSS language server
// (@tailwindcss/language-server) from PATH.
var DefaultTailwindCommand = []string{"tailwindcss-language-server", "--stdio"}

// TailwindCommand returns the configured command or the default.
func (t *Tailwind) TailwindCommand() []string {
	if t == nil || len(t.Command) == 0 {
		return DefaultTailwindCommand
	}
	return t.Command
}

// Gopls configures the gopls adapter of ktml lsp, which shows Go type errors
// in generated code on the KTML ranges they were generated from, and adds
// handwritten Go to Host Schema navigation and rename. It is on by default
// in a Go module.
type Gopls struct {
	// Command starts gopls on stdio. The default is gopls from PATH.
	Command []string `json:"command,omitempty"`
	// Remote is the shared gopls daemon the adapter connects to, as gopls's
	// -remote flag names it: "auto" (the default) shares the daemon of every
	// gopls started with -remote=auto, such as an editor's; "unix;/path"
	// and "host:port" name a daemon's socket; "off" runs a private gopls.
	Remote string `json:"remote,omitempty"`
	// Disabled turns the adapter off.
	Disabled bool `json:"disabled,omitempty"`
}

// GoplsRemoteOff runs a private gopls instead of sharing a daemon.
const GoplsRemoteOff = "off"

// DefaultGoplsRemote shares the daemon of every gopls started with
// -remote=auto.
const DefaultGoplsRemote = "auto"

// GoplsRemote returns the configured daemon address, the default, or "" for
// a private gopls.
func (g *Gopls) GoplsRemote() string {
	switch {
	case g == nil || g.Remote == "":
		return DefaultGoplsRemote
	case g.Remote == GoplsRemoteOff:
		return ""
	}
	return g.Remote
}

type Plugin struct {
	Path      string         `json:"path"`
	AllowRead []string       `json:"allow_read,omitempty"`
	Settings  map[string]any `json:"settings,omitempty"`
	Limits    PluginLimits   `json:"limits,omitempty"`
	// Outputs are the project directories the plugin's generated files may
	// be written to and cleaned up in. A plugin without outputs may not
	// write files.
	Outputs []PluginOutput `json:"outputs,omitempty"`
}

// PluginOutput is a directory a plugin may generate files in: Dir, relative
// to the project root ("." for all of it), matched by whole path segments,
// and optionally only files ending in one of Suffixes. Whatever a plugin
// declares, the host never writes .git, .ktml, configuration, KTML sources,
// or handwritten Go.
type PluginOutput struct {
	Dir      string   `json:"dir"`
	Suffixes []string `json:"suffixes,omitempty"`
}

// PluginLimits override a plugin's default resource limits: 1 GiB of guest
// memory, 30 s per call, and 64 MiB per result.
type PluginLimits struct {
	MemoryMB  int `json:"memory_mb,omitempty"`
	TimeoutMS int `json:"timeout_ms,omitempty"`
	OutputMB  int `json:"output_mb,omitempty"`
}

// GoBackend names the native production Go generator (gogen). It is the
// default generate backend; generator plugins such as the experimental gohtml
// backend must be named explicitly.
const GoBackend = "go"

type Resolution struct {
	Generate string   `json:"generate"`
	Format   []string `json:"format"`
	LSP      []string `json:"lsp"`
}

type GenerationConfig struct {
	Environment string         `json:"environment"`
	FilePath    string         `json:"file_path"`
	ProjectRoot string         `json:"project_root"`
	Settings    map[string]any `json:"settings"`
	DevMode     bool           `json:"dev_mode"`
}

type Config struct {
	PackageDir string            `json:"package_dir"`
	LSP        map[string]LSP    `json:"lsp,omitempty"`
	Plugin     map[string]Plugin `json:"plugin,omitempty"`
	Resolution Resolution        `json:"resolution"`
	Dev        *Dev              `json:"dev,omitempty"`
	Tailwind   *Tailwind         `json:"tailwind,omitempty"`
	Gopls      *Gopls            `json:"gopls,omitempty"`
	Explorer   *Explorer         `json:"explorer,omitempty"`
	HTML       *HTML             `json:"html,omitempty"`
}

// HTML configures the HTML analyzer of ktml lsp.
type HTML struct {
	// ExtensionAttributes are the hx-* attributes of the project's own HTMX
	// extensions, such as hx-require. The analyzer also accepts each with a
	// data- prefix and with htmx 4 :modifier suffixes. Only hx-* names may be
	// declared: other custom attributes belong in data-*.
	ExtensionAttributes []string `json:"extension_attributes,omitempty"`
}

// ExtensionAttributeNames returns the declared extension attributes; nil
// when the section is absent.
func (h *HTML) ExtensionAttributeNames() []string {
	if h == nil {
		return nil
	}
	return h.ExtensionAttributes
}

// extensionAttribute matches a declarable extension attribute: hx- and a
// lower-case name, without a data- prefix or a :modifier.
var extensionAttribute = regexp.MustCompile(`^hx-[a-z0-9]+(-[a-z0-9]+)*$`)

// Explorer configures the Component Explorer's previews. Paths are relative
// to the project root.
type Explorer struct {
	// Static serves a project directory at a URL path prefix, as the
	// application serves it, so component markup and stylesheets resolve
	// the same URLs: {"dir": "static", "url": "/static"}.
	Static *ExplorerStatic `json:"static,omitempty"`
	// Stylesheets and Scripts are the URLs each preview loads, in order.
	// Scripts load deferred. Without Scripts the Explorer's bundled
	// HTMX and Alpine.js load.
	Stylesheets []string `json:"stylesheets,omitempty"`
	Scripts     []string `json:"scripts,omitempty"`
	// BodyClass is the class attribute of each preview's body.
	BodyClass string `json:"body_class,omitempty"`
}

// ExplorerStatic is a project directory served at a URL path prefix.
type ExplorerStatic struct {
	Dir string `json:"dir"`
	URL string `json:"url"`
}

func Default() *Config {
	return &Config{
		PackageDir: ".",
		LSP:        make(map[string]LSP),
		Plugin:     make(map[string]Plugin),
		Resolution: Resolution{
			Generate: GoBackend,
			Format:   []string{},
			LSP:      []string{"*"},
		},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates the contents of ktml.config.json.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if _, exists := cfg.Plugin[GoBackend]; exists {
		return nil, &configdiag.PathError{Path: []string{"plugin", GoBackend}, Message: `plugin name "go" is reserved for the native Go backend`}
	}
	for _, name := range sortedPluginNames(cfg.Plugin) {
		limits := cfg.Plugin[name].Limits
		for _, limit := range []struct {
			key   string
			value int
		}{{"memory_mb", limits.MemoryMB}, {"timeout_ms", limits.TimeoutMS}, {"output_mb", limits.OutputMB}} {
			if limit.value < 0 {
				return nil, &configdiag.PathError{Path: []string{"plugin", name, "limits", limit.key}, Message: fmt.Sprintf("plugin %q: limits must not be negative", name)}
			}
		}
		if limits.MemoryMB > 4096 {
			return nil, &configdiag.PathError{Path: []string{"plugin", name, "limits", "memory_mb"}, Message: fmt.Sprintf("plugin %q: limits.memory_mb must be at most 4096", name)}
		}
		if err := validatePluginOutputs(name, cfg.Plugin[name].Outputs); err != nil {
			return nil, err
		}
	}
	if cfg.Resolution.Generate == "" {
		cfg.Resolution.Generate = GoBackend
	}

	if cfg.Dev != nil {
		if err := validateDev(cfg.Dev); err != nil {
			return nil, err
		}
		for _, name := range slices.Sorted(maps.Keys(cfg.Dev.Profiles)) {
			if name == "" {
				return nil, &configdiag.PathError{Path: []string{"dev", "profiles"}, Message: "a dev profile needs a name"}
			}
			profile, err := resolveProfile(cfg.Dev, name, cfg.Dev.Profiles[name])
			if err != nil {
				return nil, err
			}
			if cfg.Dev.profiles == nil {
				cfg.Dev.profiles = make(map[string]*Dev)
			}
			cfg.Dev.profiles[name] = profile
		}
	}

	if explorer := cfg.Explorer; explorer != nil {
		if static := explorer.Static; static != nil {
			if static.Dir == "" || filepath.IsAbs(static.Dir) || !filepath.IsLocal(filepath.FromSlash(static.Dir)) {
				return nil, &configdiag.PathError{Path: []string{"explorer", "static", "dir"}, Message: "explorer static dir must be a relative path inside the project"}
			}
			if !strings.HasPrefix(static.URL, "/") || strings.HasPrefix(static.URL, "/_explorer") || strings.HasPrefix(static.URL, "/__ktml") || static.URL == "/" {
				return nil, &configdiag.PathError{Path: []string{"explorer", "static", "url"}, Message: "explorer static url must be an absolute path prefix other than /, /_explorer, and /__ktml"}
			}
		}
		for index, list := range [][]string{explorer.Stylesheets, explorer.Scripts} {
			key := []string{"stylesheets", "scripts"}[index]
			for position, value := range list {
				if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
					return nil, &configdiag.PathError{Path: []string{"explorer", key, strconv.Itoa(position)}, Message: "explorer assets are local URL paths starting with /; the Explorer loads nothing from the network"}
				}
			}
		}
	}

	for index, name := range cfg.HTML.ExtensionAttributeNames() {
		if !extensionAttribute.MatchString(name) {
			return nil, &configdiag.PathError{Path: []string{"html", "extension_attributes", strconv.Itoa(index)}, Message: fmt.Sprintf("html extension attribute %q must be a lower-case hx-* name such as hx-require, without data- or a :modifier; other custom attributes belong in data-*", name)}
		}
	}

	return cfg, nil
}

// validateDev applies dev's defaults and validates it.
func validateDev(dev *Dev) error {
	if dev.Port == 0 {
		dev.Port = 7331
	}
	if dev.Port < 0 || dev.Port > 65535 {
		return &configdiag.PathError{Path: []string{"dev", "port"}, Message: fmt.Sprintf("dev port %d must be between 1 and 65535", dev.Port)}
	}
	if dev.ProxyTarget == "" {
		return &configdiag.PathError{Path: []string{"dev"}, Message: "proxy target is required"}
	}
	if err := validateProxyTarget(dev); err != nil {
		return err
	}
	if len(dev.Command) == 0 {
		return &configdiag.PathError{Path: []string{"dev"}, Message: "command is required"}
	}
	for index, pattern := range dev.ReloadWatch {
		if _, err := filepath.Match(pattern, ""); err != nil || pattern == "" {
			return &configdiag.PathError{Path: []string{"dev", "reload_watch", strconv.Itoa(index)}, Message: fmt.Sprintf("dev reload_watch %q is not a file name pattern such as *.css", pattern)}
		}
	}
	if dev.ReadyTimeoutMS < 0 {
		return &configdiag.PathError{Path: []string{"dev", "ready_timeout_ms"}, Message: "dev ready_timeout_ms must not be negative"}
	}
	if len(dev.Build) > 0 && (!containsBinaryPlaceholder(dev.Build) || !containsBinaryPlaceholder(dev.Command)) {
		return &configdiag.PathError{Path: []string{"dev", "build"}, Message: "dev build and command must both reference {binary}"}
	}
	if explorer := dev.Explorer; explorer != nil {
		if explorer.Addr == "" {
			explorer.Addr = DefaultExplorerAddr
		}
		if host, _, err := net.SplitHostPort(explorer.Addr); err != nil || !LoopbackHost(host) {
			return &configdiag.PathError{Path: []string{"dev", "explorer", "addr"}, Message: fmt.Sprintf("dev explorer addr %q must be a loopback host:port (127.0.0.1, ::1, or localhost); the Explorer is never reachable from the network", explorer.Addr)}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(dev.Env)) {
		switch {
		case !envName.MatchString(key):
			return &configdiag.PathError{Path: []string{"dev", "env", key}, Message: fmt.Sprintf("dev env %q is not a variable name", key)}
		case strings.HasPrefix(key, ControlEnvPrefix):
			return &configdiag.PathError{Path: []string{"dev", "env", key}, Message: fmt.Sprintf("dev env %s: %s* variables belong to ktml dev", key, ControlEnvPrefix)}
		}
	}
	if picker := dev.Picker; picker != nil && picker.Hotkey != "" {
		canonical, ok := canonicalHotkey(picker.Hotkey)
		if !ok {
			return &configdiag.PathError{Path: []string{"dev", "picker", "hotkey"}, Message: fmt.Sprintf("dev picker hotkey %q must be Ctrl, Alt, or Meta (optionally with Shift) and a letter, digit, or F1-F12, such as %s", picker.Hotkey, DefaultPickerHotkey)}
		}
		picker.Hotkey = canonical
	}
	if picker := dev.Picker; picker != nil && picker.Editor != nil {
		if len(picker.Editor) == 0 || picker.Editor[0] == "" || !slices.ContainsFunc(picker.Editor, func(arg string) bool { return strings.Contains(arg, "{file}") }) {
			return &configdiag.PathError{Path: []string{"dev", "picker", "editor"}, Message: `dev picker editor must be a command and its arguments, one of them naming {file}, such as ["code", "-g", "{file}:{line}:{column}"]`}
		}
	}
	return nil
}

// validatePluginOutputs rejects output directories outside the project or
// in .git or .ktml, and suffixes that are not plain file-name endings or that
// name KTML sources.
func validatePluginOutputs(name string, outputs []PluginOutput) error {
	for index, output := range outputs {
		at := []string{"plugin", name, "outputs", strconv.Itoa(index)}
		dir := filepath.FromSlash(output.Dir)
		if output.Dir == "" || filepath.IsAbs(dir) || !filepath.IsLocal(dir) {
			return &configdiag.PathError{Path: append(at, "dir"), Message: fmt.Sprintf("plugin %q: output dir %q must be a relative path inside the project (\".\" for the whole project)", name, output.Dir)}
		}
		for _, segment := range strings.Split(filepath.ToSlash(filepath.Clean(dir)), "/") {
			if lower := strings.ToLower(segment); lower == ".git" || lower == ".ktml" {
				return &configdiag.PathError{Path: append(at, "dir"), Message: fmt.Sprintf("plugin %q: output dir %q is never writable: %s belongs to version control or KTML's state", name, output.Dir, segment)}
			}
		}
		for position, suffix := range output.Suffixes {
			lower := strings.ToLower(suffix)
			if suffix == "" || strings.ContainsAny(suffix, `/\`) || strings.HasSuffix(lower, ".ktml") || strings.HasSuffix(lower, ".ktmlschema") {
				return &configdiag.PathError{Path: append(at, "suffixes", strconv.Itoa(position)), Message: fmt.Sprintf("plugin %q: output suffix %q must be a file-name ending such as .html, and not a KTML source", name, suffix)}
			}
		}
	}
	return nil
}

func sortedPluginNames(plugins map[string]Plugin) []string {
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsBinaryPlaceholder(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, "{binary}") {
			return true
		}
	}
	return false
}
