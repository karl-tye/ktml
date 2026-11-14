package htmlcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
)

// htmxAttributes are the attribute names of htmx 2 and htmx 4, with the
// first-party extensions KTML projects use. A name may also be written with
// a data- prefix and, in htmx 4, with :modifier suffixes such as
// hx-indicator:inherited or hx-ext:append.
var htmxAttributes = map[string]bool{
	// Requests.
	"hx-get": true, "hx-post": true, "hx-put": true, "hx-patch": true, "hx-delete": true,
	"hx-action": true, "hx-method": true,
	// Core.
	"hx-trigger": true, "hx-target": true, "hx-swap": true, "hx-swap-oob": true, "hx-select": true,
	"hx-select-oob": true, "hx-include": true, "hx-indicator": true, "hx-params": true,
	"hx-encoding": true, "hx-sync": true, "hx-boost": true, "hx-push-url": true, "hx-replace-url": true,
	"hx-confirm": true, "hx-prompt": true, "hx-history": true, "hx-history-elt": true,
	"hx-request": true, "hx-validate": true, "hx-headers": true, "hx-vals": true, "hx-vars": true,
	"hx-ext": true, "hx-inherit": true, "hx-disinherit": true, "hx-disable": true,
	"hx-disabled-elt": true, "hx-preserve": true, "hx-config": true, "hx-optimistic": true,
	"hx-partial": true, "hx-ignore": true, "hx-on": true, "hx-status": true,
	// Extensions.
	"hx-preload": true, "hx-alpine-compat": true, "hx-targets": true, "hx-sse": true, "hx-ws": true,
}

// htmxFamilies are the name prefixes whose suffix is open: event names
// (hx-on-click), response codes (hx-target-404, hx-target-4*, hx-status-*),
// and the subnames of the SSE and WebSocket extensions.
var htmxFamilies = []string{"hx-on-", "hx-target-", "hx-status", "hx-sse-", "hx-ws-"}

// htmxName splits an HTMX attribute name into its data- prefix, its stem,
// and its :modifier suffix; false when the name is not an HTMX attribute.
func htmxName(name string) (prefix, stem, suffix string, ok bool) {
	rest := name
	if strings.HasPrefix(rest, "data-hx-") {
		prefix, rest = "data-", rest[len("data-"):]
	}
	if !strings.HasPrefix(rest, "hx-") {
		return "", "", "", false
	}
	stem = rest
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		stem, suffix = rest[:colon], rest[colon:]
	}
	return prefix, stem, suffix, true
}

// knownHTMX reports whether stem is an HTMX attribute or one of the
// project's extension attributes. Names whose suffix is open, and names a
// framework builds from parts, are known: the analyzer prefers silence to a
// false positive.
func knownHTMX(stem string, extensions []string) bool {
	if htmxAttributes[stem] || strings.ContainsAny(stem, "{[") {
		return true
	}
	for _, family := range htmxFamilies {
		if strings.HasPrefix(stem, family) {
			return true
		}
	}
	for _, extension := range extensions {
		if stem == extension {
			return true
		}
	}
	return false
}

// HTMXAttributes returns the HTMX attribute names in order.
func HTMXAttributes() []string {
	names := make([]string, 0, len(htmxAttributes))
	for name := range htmxAttributes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// checkHTMX reports an hx-* or data-hx-* name that is neither an HTMX
// attribute nor a declared extension attribute.
func (a *analyzer) checkHTMX(entry *ast.AttributeNode, prefix, stem, suffix string, start, end int) {
	if knownHTMX(stem, a.extensions) {
		return
	}
	message := fmt.Sprintf("%s is not an HTMX attribute", entry.Name)
	var fixes []Fix
	if suggestion := closest(stem, append(HTMXAttributes(), a.extensions...)); suggestion != "" {
		replacement := prefix + suggestion + suffix
		message += fmt.Sprintf("; did you mean %s?", replacement)
		fixes = append(fixes, Fix{Title: "Change to " + replacement, Edits: []Edit{{start, end, replacement}}, Preferred: true})
	} else {
		message += fmt.Sprintf(`; declare a project HTMX extension attribute in ktml.config.json "html": {"extension_attributes": [%q]}`, stem)
	}
	a.report(start, end, Warning, "html-unknown-attribute", message, fixes...)
}
