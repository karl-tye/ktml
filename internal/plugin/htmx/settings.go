package htmx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Settings is the HTMX plugin's settings object, `plugin.htmx.settings` in
// ktml.config.json. The minimal configuration is {"chi":{"dir":"..."}};
// everything else is optional.
type Settings struct {
	Chi  ChiSettings
	Host HostSettings
}

// HostSettings carries what the KTML host adds under "_ktml".
type HostSettings struct {
	PathMap map[string]string `json:"path_map,omitempty"`
}

type ChiSettings struct {
	// Dir is the Go source directory whose Chi registrations are indexed. It
	// is resolved against the host's read-only mounts.
	Dir string
	// Patterns holds the optional AST matchers for custom router wrappers.
	Patterns Patterns
}

// Patterns are the optional `chi.patterns` matchers. Defaults reports whether
// the built-in Chi matchers stay active: user patterns extend them by default
// and replace them when `"defaults": false`.
type Patterns struct {
	Defaults bool
	Scope    []*Pattern
	Mount    []*Pattern
	Routes   map[string][]*Pattern
}

// SettingsError is one schema violation at a JSON path inside the plugin
// settings, such as chi.patterns.routes.GET[0].
type SettingsError struct {
	Path    string
	Message string
}

func (err SettingsError) Error() string {
	return fmt.Sprintf("htmx settings: %s: %s", err.Path, err.Message)
}

// routeMethods are the method keys accepted under chi.patterns.routes. "*"
// registers a route for every method, as Chi's Handle does.
var routeMethodKeys = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "CONNECT": true, "OPTIONS": true, "TRACE": true, "*": true,
}

// ParseSettings decodes and validates the plugin settings. Every violation is
// reported with its JSON path; the returned error joins them in path order.
func ParseSettings(data []byte) (Settings, error) {
	var errs []error
	report := func(path, format string, args ...any) {
		errs = append(errs, SettingsError{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	root, ok := decodeObject(data, "settings", report)
	if !ok {
		return Settings{}, errors.Join(errs...)
	}
	settings := Settings{Chi: ChiSettings{Patterns: Patterns{Defaults: true}}}
	for _, key := range sortedKeys(root) {
		switch key {
		case "chi":
			settings.Chi = parseChi(root[key], report)
		case "_ktml":
			if err := json.Unmarshal(root[key], &settings.Host); err != nil {
				report("_ktml", "invalid host settings: %v", err)
			}
		default:
			report(key, "unknown setting; the HTMX plugin accepts \"chi\"")
		}
	}
	if _, ok := root["chi"]; !ok {
		report("chi.dir", "required: set it to the Go directory that registers the Chi routes")
	}
	if len(errs) > 0 {
		return Settings{}, errors.Join(errs...)
	}
	settings.Chi.Dir = resolveMountedDir(settings.Chi.Dir, settings.Host.PathMap)
	return settings, nil
}

func parseChi(data json.RawMessage, report func(path, format string, args ...any)) ChiSettings {
	chi := ChiSettings{Patterns: Patterns{Defaults: true}}
	object, ok := decodeObject(data, "chi", report)
	if !ok {
		return chi
	}
	for _, key := range sortedKeys(object) {
		switch key {
		case "dir":
			var dir string
			if json.Unmarshal(object[key], &dir) != nil {
				report("chi.dir", "must be a string")
			} else if strings.TrimSpace(dir) == "" {
				report("chi.dir", "must not be empty")
			} else {
				chi.Dir = dir
			}
		case "patterns":
			chi.Patterns = parsePatterns(object[key], report)
		default:
			report("chi."+key, "unknown setting; chi accepts \"dir\" and \"patterns\"")
		}
	}
	if _, ok := object["dir"]; !ok {
		report("chi.dir", "required: set it to the Go directory that registers the Chi routes")
	}
	return chi
}

func parsePatterns(data json.RawMessage, report func(path, format string, args ...any)) Patterns {
	patterns := Patterns{Defaults: true}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return patterns
	}
	object, ok := decodeObject(data, "chi.patterns", report)
	if !ok {
		return patterns
	}
	for _, key := range sortedKeys(object) {
		path := "chi.patterns." + key
		switch key {
		case "defaults":
			if json.Unmarshal(object[key], &patterns.Defaults) != nil {
				report(path, "must be true (extend the built-in Chi matchers) or false (replace them)")
			}
		case "scope":
			patterns.Scope = parsePatternList(object[key], path, scopePattern, report)
		case "mount":
			patterns.Mount = parsePatternList(object[key], path, mountPattern, report)
		case "routes":
			routes, ok := decodeObject(object[key], path, report)
			if !ok {
				continue
			}
			patterns.Routes = make(map[string][]*Pattern)
			for _, method := range sortedKeys(routes) {
				methodPath := path + "." + method
				if !routeMethodKeys[method] {
					report(methodPath, "unknown HTTP method; use an upper-case method such as GET or POST, or \"*\" for every method")
					continue
				}
				list := parsePatternList(routes[method], methodPath, routePattern, report)
				for _, pattern := range list {
					pattern.Method = method
				}
				patterns.Routes[method] = list
			}
		default:
			report(path, "unknown setting; patterns accepts \"defaults\", \"scope\", \"mount\", and \"routes\"")
		}
	}
	if !patterns.Defaults && len(patterns.Scope)+len(patterns.Mount)+len(patterns.Routes) == 0 {
		report("chi.patterns.defaults", "false disables the built-in Chi matchers, so at least one scope, mount, or routes pattern is required")
	}
	return patterns
}

func parsePatternList(data json.RawMessage, path string, kind patternKind, report func(path, format string, args ...any)) []*Pattern {
	var sources []json.RawMessage
	if json.Unmarshal(data, &sources) != nil {
		report(path, "must be an array of pattern strings")
		return nil
	}
	var patterns []*Pattern
	for i, raw := range sources {
		elementPath := fmt.Sprintf("%s[%d]", path, i)
		var source string
		if json.Unmarshal(raw, &source) != nil {
			report(elementPath, "must be a string")
			continue
		}
		pattern, err := CompilePattern(kind, source)
		if err != nil {
			report(elementPath, "%v", err)
			continue
		}
		patterns = append(patterns, pattern)
	}
	return patterns
}

func decodeObject(data json.RawMessage, path string, report func(path, format string, args ...any)) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		report(path, "must be a JSON object")
		return nil, false
	}
	return object, true
}

func sortedKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// resolveMountedDir maps chi.dir onto the host's mount of the allow_read
// directory that contains it, so a dir inside a wider allow_read entry (for
// example "./internal/web" under ".") resolves to its absolute guest path.
func resolveMountedDir(dir string, pathMap map[string]string) string {
	if absolute, ok := pathMap[dir]; ok {
		return absolute
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	clean := filepath.Clean(dir)
	best := ""
	for configured := range pathMap {
		configuredClean := filepath.Clean(configured)
		relative, err := filepath.Rel(configuredClean, clean)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if best == "" || len(configuredClean) > len(filepath.Clean(best)) {
			best = configured
		}
	}
	if best == "" {
		return dir
	}
	relative, _ := filepath.Rel(filepath.Clean(best), clean)
	return filepath.Join(pathMap[best], relative)
}
