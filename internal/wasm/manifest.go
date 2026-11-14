package wasm

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/karl-tye/ktml/internal/config"
)

// ABIVersion is the newest plugin ABI this host implements, and
// MinABIVersion the oldest it still loads.
//
// ABI 1 is the exports allocate, ktml_manifest, and optionally
// ktml_configure, ktml_generate, ktml_format, and ktml_lsp_handle, with
// (pointer<<32 | length) results, and the env host functions
// ktml_host_write_file and ktml_host_proxy_lsp.
//
// ABI 2 adds, with no change to ABI 1's exports:
//   - the symbol-index capability: the host sends symbolindex.Method
//     requests through ktml_lsp_handle, and only to plugins that declare it;
//   - a guest working directory: PWD is the project root (Options.WorkDir),
//     so relative paths and os.Getwd name the host's absolute project paths.
//
// The working directory applies to ABI 1 plugins too; it only changes paths
// that named the first read mount before.
const (
	ABIVersion    = 2
	MinABIVersion = 1
)

// Capability names what a plugin may be asked to do. The host calls an
// export only when the manifest declares the capability that covers it.
type Capability string

const (
	CapabilityGenerate         Capability = "generate"
	CapabilityFormat           Capability = "format"
	CapabilityCompletion       Capability = "completion"
	CapabilitySemanticTokens   Capability = "semantic-tokens"
	CapabilityEmbeddedLanguage Capability = "embedded-language"
	CapabilityRouteProvider    Capability = "route-provider"
	CapabilityClassProvider    Capability = "class-provider"
	CapabilityStoryControl     Capability = "story-control"
	// CapabilitySymbolIndex contributes documents to the language server's
	// workspace symbol index (internal/symbolindex).
	CapabilitySymbolIndex Capability = "symbol-index"
)

// capabilityABI is the oldest ABI that defines a capability, when newer
// than ABI 1.
var capabilityABI = map[Capability]int{CapabilitySymbolIndex: 2}

var knownCapabilities = map[Capability]bool{
	CapabilityGenerate: true, CapabilityFormat: true, CapabilityCompletion: true,
	CapabilitySemanticTokens: true, CapabilityEmbeddedLanguage: true,
	CapabilityRouteProvider: true, CapabilityClassProvider: true, CapabilityStoryControl: true,
	CapabilitySymbolIndex: true,
}

// lspCapabilities are the capabilities that serve LSP requests through
// ktml_lsp_handle.
var lspCapabilities = []Capability{
	CapabilityCompletion, CapabilitySemanticTokens, CapabilityEmbeddedLanguage,
	CapabilityRouteProvider, CapabilityClassProvider, CapabilitySymbolIndex,
}

// Manifest is what a plugin's ktml_manifest export returns.
type Manifest struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	ABI          int          `json:"abi"`
	Capabilities []Capability `json:"capabilities"`
}

// ErrCapability reports a call the plugin's manifest does not allow.
var ErrCapability = errors.New("plugin capability not declared")

func parseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid plugin manifest: %w", err)
	}
	var problems []string
	if strings.TrimSpace(manifest.Name) == "" {
		problems = append(problems, "name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		problems = append(problems, "version is required")
	}
	if manifest.ABI < MinABIVersion || manifest.ABI > ABIVersion {
		problems = append(problems, fmt.Sprintf("abi %d is not supported; this host implements abi %d and %d", manifest.ABI, MinABIVersion, ABIVersion))
	}
	if len(manifest.Capabilities) == 0 {
		problems = append(problems, "at least one capability is required")
	}
	for _, capability := range manifest.Capabilities {
		if !knownCapabilities[capability] {
			problems = append(problems, fmt.Sprintf("unknown capability %q; known capabilities are %s", capability, capabilityList()))
		} else if needed := capabilityABI[capability]; manifest.ABI >= MinABIVersion && manifest.ABI < needed {
			problems = append(problems, fmt.Sprintf("capability %q needs abi %d", capability, needed))
		}
	}
	if len(problems) > 0 {
		return Manifest{}, fmt.Errorf("invalid plugin manifest %q: %s", manifest.Name, strings.Join(problems, "; "))
	}
	return manifest, nil
}

func capabilityList() string {
	names := make([]string, 0, len(knownCapabilities))
	for capability := range knownCapabilities {
		names = append(names, string(capability))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Has reports whether the manifest declares capability.
func (manifest Manifest) Has(capability Capability) bool {
	for _, declared := range manifest.Capabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

// ServesLSP reports whether the plugin declares any LSP capability.
func (manifest Manifest) ServesLSP() bool {
	for _, capability := range lspCapabilities {
		if manifest.Has(capability) {
			return true
		}
	}
	return false
}

// Limits bound a plugin's resources. Zero fields take the defaults.
type Limits struct {
	// MemoryBytes caps the guest's linear memory.
	MemoryBytes uint64
	// CallTimeout bounds each call into the guest, configuration included.
	// A call that exceeds it stops the guest instance; the next call starts
	// a fresh one.
	CallTimeout time.Duration
	// OutputBytes caps each result read from the guest and the total size of
	// a generation's auxiliary files.
	OutputBytes int
}

var DefaultLimits = Limits{
	MemoryBytes: 1 << 30,
	CallTimeout: 30 * time.Second,
	OutputBytes: 64 << 20,
}

// LimitsFromConfig converts ktml.config.json plugin limits.
func LimitsFromConfig(limits config.PluginLimits) Limits {
	return Limits{
		MemoryBytes: uint64(limits.MemoryMB) << 20,
		CallTimeout: time.Duration(limits.TimeoutMS) * time.Millisecond,
		OutputBytes: limits.OutputMB << 20,
	}
}

func (limits Limits) withDefaults() Limits {
	if limits.MemoryBytes == 0 {
		limits.MemoryBytes = DefaultLimits.MemoryBytes
	}
	if limits.CallTimeout == 0 {
		limits.CallTimeout = DefaultLimits.CallTimeout
	}
	if limits.OutputBytes == 0 {
		limits.OutputBytes = DefaultLimits.OutputBytes
	}
	return limits
}

func (limits Limits) memoryPages() uint32 {
	pages := limits.MemoryBytes / 65536
	if pages < 1 {
		return 1
	}
	if pages > 65536 {
		return 65536
	}
	return uint32(pages)
}
