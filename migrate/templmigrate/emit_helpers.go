package templmigrate

import (
	"fmt"
	"strings"
)

// helperSources are the presenter helpers the converter writes, once per
// output package, when a presenter uses them. They reproduce templ's
// rendering semantics without importing templ.
var helperSources = map[string]struct {
	imports []string
	source  string
}{
	"classes": {nil, `
// migrateClass is one templ.KV class entry.
type migrateClass struct {
	name string
	on   bool
}

// migrateKV mirrors templ.KV for class lists.
func migrateKV(name string, on bool) migrateClass { return migrateClass{name: name, on: on} }

// migrateClasses joins class names as templ.Classes renders them: in
// order, without disabled or repeated names.
func migrateClasses(items ...any) string {
	enabled := map[string]bool{}
	var order []string
	for _, item := range items {
		switch c := item.(type) {
		case string:
			enabled[c] = true
			order = append(order, c)
		case []string:
			for _, name := range c {
				enabled[name] = true
				order = append(order, name)
			}
		case migrateClass:
			enabled[c.name] = c.on
			order = append(order, c.name)
		}
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(order))
	for _, name := range order {
		if !enabled[name] || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, " ")
}
`},
	"jsonscript": {[]string{"bytes", "encoding/json", "github.com/karl-tye/ktml"}, `
// migrateJSONScript encodes a value as templ.JSONScript did: with
// json.Encoder, so HTML-escaped and newline-terminated. It panics on
// values encoding/json cannot encode.
func migrateJSONScript(value any) ktml.JSONValue {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(value); err != nil {
		panic(err)
	}
	return ktml.TrustJSON(b.String())
}
`},
	"style": {[]string{"github.com/karl-tye/ktml"}, `
// migrateStyle trusts a style string as templ wrote it: trimmed and
// ending with a semicolon. templ also sanitized it; this does not, so
// every caller is a security review (prefer ktml.CSS).
func migrateStyle(value string) ktml.CSSValue {
	value = strings.TrimSpace(value)
	if value != "" && !strings.HasSuffix(value, ";") {
		value += ";"
	}
	return ktml.TrustCSS(value)
}
`},
	"join": {[]string{"github.com/karl-tye/ktml"}, `
// migrateJoin joins a list of components, templ's or KTML's (they share a
// method set), into one content value. An empty list is absent content, as
// templ's len(components) > 0 tests it.
func migrateJoin[C ktml.Component](components []C) ktml.Component {
	if len(components) == 0 {
		return nil
	}
	joined := make([]ktml.Component, len(components))
	for i, component := range components {
		joined[i] = component
	}
	return ktml.Join(joined...)
}
`},
	"attributes": {[]string{"fmt", "sort", "github.com/karl-tye/ktml"}, `
// migrateAttributes converts a templ.Attributes map in templ's order
// (sorted by name). A string that a plain attribute rejects is a URL,
// written through the URL policy, or else is trusted as script, so every
// caller is a security review.
func migrateAttributes(attrs map[string]any) ktml.Attributes {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	bags := make([]ktml.Attributes, 0, len(names))
	for _, name := range names {
		var bag ktml.Attributes
		var err error
		switch value := attrs[name].(type) {
		case bool:
			bag, err = ktml.BoolAttr(name, value)
		case string:
			if bag, err = ktml.Attr(name, value); err != nil {
				if bag, err = ktml.URLAttr(name, ktml.NewURL(value)); err != nil {
					bag, err = ktml.ScriptAttr(name, ktml.TrustScript(value))
				}
			}
		default:
			if bag, err = ktml.Attr(name, fmt.Sprint(value)); err != nil {
				bag, err = ktml.URLAttr(name, ktml.NewURL(fmt.Sprint(value)))
			}
		}
		if err != nil {
			panic(err)
		}
		bags = append(bags, bag)
	}
	return ktml.JoinAttributes(bags...)
}
`},
}

func helperFile(pkg string, used map[string]bool) []byte {
	imports := map[string]bool{}
	var names []string
	for name := range used {
		names = append(names, name)
		for _, imp := range helperSources[name].imports {
			imports[imp] = true
		}
	}
	if used["classes"] || used["style"] {
		imports["strings"] = true
	}
	sortStrings(names)
	var b strings.Builder
	b.WriteString("// Presenter helpers written by ktml migrate templ. They reproduce templ's\n")
	b.WriteString("// rendering semantics for the scaffolds; replace them as presenters mature.\n\n")
	fmt.Fprintf(&b, "package %s\n", pkg)
	if len(imports) > 0 {
		// Standard packages first, then a blank line and the others, as
		// goimports groups them.
		b.WriteString("\nimport (\n")
		var others []string
		for _, imp := range sortedSet(imports) {
			if strings.Contains(strings.SplitN(imp, "/", 2)[0], ".") {
				others = append(others, imp)
				continue
			}
			fmt.Fprintf(&b, "\t%q\n", imp)
		}
		for i, imp := range others {
			if i == 0 && len(others) < len(imports) {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "\t%q\n", imp)
		}
		b.WriteString(")\n")
	}
	for _, name := range names {
		b.WriteString(helperSources[name].source)
	}
	return []byte(b.String())
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
