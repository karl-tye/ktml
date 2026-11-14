package gohtml

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

var componentDecls map[string][]*ast.ElementDecl
var generateMu sync.Mutex

func Generate(doc *ast.Document, devMode bool, filePath string, writeFile func(string, []byte)) string {
	content, err := GenerateChecked(doc, devMode, filePath, writeFile)
	if err != nil {
		slog.Error("WASM gohtml.Generate failed", "error", err)
		return ""
	}
	return content
}

// GenerateChecked rejects invalid or unreadable imported KTML before emitting
// any output. Generate remains as a compatibility wrapper for direct callers.
func GenerateChecked(doc *ast.Document, devMode bool, filePath string, writeFile func(string, []byte)) (string, error) {
	if err := validateCanonicalInputs(doc); err != nil {
		return "", err
	}
	if err := validateImports(doc, filePath, make(map[string]bool)); err != nil {
		return "", err
	}
	generateMu.Lock()
	defer generateMu.Unlock()
	componentDecls = make(map[string][]*ast.ElementDecl)
	slog.Debug("WASM gohtml.Generate started", "filePath", filePath)
	var sb strings.Builder

	localComponentDecls := make(map[string]bool)

	dir := filepath.Dir(filePath)
	slog.Debug("Scanning doc.Nodes for imports", "dir", dir, "nodeCount", len(doc.Nodes))
	for _, node := range doc.Nodes {
		if imp, ok := node.(*ast.ImportStmt); ok {
			importPath := imp.Path
			if unquoted, err := strconv.Unquote(importPath); err == nil {
				importPath = unquoted
			}
			if !strings.HasSuffix(importPath, ".ktml") {
				importPath += ".ktml"
			}
			fullPath := filepath.Join(dir, importPath)
			slog.Debug("Found import, attempting read", "fullPath", fullPath)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				slog.Debug("Failed absolute read, attempting relative read", "importPath", filepath.Base(importPath))
				content, err = os.ReadFile(filepath.Base(importPath))
			}
			if err == nil {
				slog.Debug("Successfully read imported file", "len", len(content))
				result := compiler.CompileSyntax(fullPath, content)
				if result.HasErrors() {
					return "", fmt.Errorf("invalid import %s: %s", fullPath, result.Diagnostics[0].Message)
				}
				if err := validateCanonicalInputs(result.Document); err != nil {
					return "", fmt.Errorf("invalid import %s: %w", fullPath, err)
				}
				compDoc := result.Document
				for _, compNode := range compDoc.Nodes {
					if decl, ok := compNode.(*ast.ElementDecl); ok {
						slog.Debug("Loaded imported component", "name", decl.Name)
						componentDecls[decl.Name] = append(componentDecls[decl.Name], decl)
					}
				}
			} else {
				return "", fmt.Errorf("read import %s: %w", fullPath, err)
			}
		} else if decl, ok := node.(*ast.ElementDecl); ok {
			slog.Debug("Loaded local component", "name", decl.Name)
			componentDecls[decl.Name] = append(componentDecls[decl.Name], decl)
			localComponentDecls[decl.Name] = true
		}
	}

	if writeFile != nil {
		slog.Debug("Generating Go structs")
		localDecls := make(map[string][]*ast.ElementDecl)
		for name, decls := range componentDecls {
			if localComponentDecls[name] {
				localDecls[name] = decls
			}
		}
		goCode := generateGoStructs(filePath, localDecls)
		if goCode != "" {
			outPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".ktml.go"
			slog.Debug("Writing auxiliary Go file", "outPath", outPath)
			writeFile(outPath, []byte(goCode))
		} else {
			slog.Debug("No structs generated (no attributes)")
		}
	} else {
		slog.Debug("writeFile callback is nil, skipping Go struct generation")
	}

	slog.Debug("Generating HTML templates")
	processedDecls := make(map[string]bool)
	for _, node := range doc.Nodes {
		switch n := node.(type) {
		case *ast.ElementDecl:
			if !processedDecls[n.Name] {
				sb.WriteString(generateTemplate(componentDecls[n.Name], devMode, filePath))
				processedDecls[n.Name] = true
			}
		}
	}
	slog.Debug("WASM gohtml.Generate finished")
	return sb.String(), nil
}

func validateImports(doc *ast.Document, filePath string, visited map[string]bool) error {
	for _, node := range doc.Nodes {
		imp, ok := node.(*ast.ImportStmt)
		if !ok {
			continue
		}
		importPath := imp.Path
		if unquoted, err := strconv.Unquote(importPath); err == nil {
			importPath = unquoted
		}
		if !strings.HasSuffix(importPath, ".ktml") {
			importPath += ".ktml"
		}
		resolvedPath := filepath.Join(filepath.Dir(filePath), importPath)
		content, err := os.ReadFile(resolvedPath)
		if err != nil {
			resolvedPath = filepath.Base(importPath)
			content, err = os.ReadFile(resolvedPath)
		}
		if err != nil {
			return fmt.Errorf("read import %s: %w", resolvedPath, err)
		}
		absolutePath, err := filepath.Abs(resolvedPath)
		if err != nil {
			return fmt.Errorf("resolve import %s: %w", resolvedPath, err)
		}
		if visited[absolutePath] {
			continue
		}
		visited[absolutePath] = true
		result := compiler.CompileSyntax(resolvedPath, content)
		if result.HasErrors() {
			return fmt.Errorf("invalid import %s: %s", resolvedPath, result.Diagnostics[0].Message)
		}
		if err := validateCanonicalInputs(result.Document); err != nil {
			return fmt.Errorf("invalid import %s: %w", resolvedPath, err)
		}
		if err := validateImports(result.Document, resolvedPath, visited); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalInputs(doc *ast.Document) error {
	if doc == nil {
		return fmt.Errorf("generate: nil document")
	}
	for _, node := range doc.Nodes {
		switch declaration := node.(type) {
		case *ast.RecordDecl:
			return fmt.Errorf("record declaration %s: Host Schema records are unsupported in gohtml generator", declaration.Name)
		case *ast.StateDecl:
			return fmt.Errorf("state declaration %s: Presentation States are unsupported in gohtml generator", declaration.Name)
		case *ast.RenderClause:
			return fmt.Errorf("render clause for %s: Render Clauses are unsupported in gohtml generator", declaration.Name)
		case *ast.ImportStmt:
			if err := validateSupportedImport(declaration); err != nil {
				return err
			}
		}
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		if declaration.IsSignature() {
			return fmt.Errorf("component signature %s: Render Clause signatures are unsupported in gohtml generator", declaration.Name)
		}
		if err := validateUndottedPaths(declaration.Body); err != nil {
			return fmt.Errorf("component %s: %w", declaration.Name, err)
		}
		fields := make(map[string]string)
		for _, input := range declaration.Attributes {
			if input.Kind == ast.OverrideDeclaration {
				continue
			}
			if input.TypeDecl == compiler.ContentTypeName {
				return fmt.Errorf("component %s content input %s: declared content inputs are unsupported in gohtml generator", declaration.Name, input.Name)
			}
			if _, scalar := gohtmlInputTypes[input.TypeDecl]; !scalar {
				return fmt.Errorf("component %s input %s: unsupported input type %s in gohtml generator", declaration.Name, input.Name, input.TypeDecl)
			}
			if input.Mode == ast.OptionalInput || input.Mode == ast.DefaultedInput {
				return fmt.Errorf("component %s input %s: unsupported input mode %s in gohtml generator", declaration.Name, input.Name, input.Mode)
			}
			field := capitalize(input.Name)
			if !exportedGoIdentifier(field) {
				return fmt.Errorf("component %s input %s does not normalize to an exported Go field", declaration.Name, input.Name)
			}
			if first, exists := fields[field]; exists {
				return fmt.Errorf("component %s inputs %s and %s normalize to the same Go field %s", declaration.Name, first, input.Name, field)
			}
			fields[field] = input.Name
		}
	}
	return nil
}

// gohtmlInputTypes are the scalar input types the experimental gohtml backend
// supports; Host Schema types and integer require the direct generator.
var gohtmlInputTypes = map[string]bool{"string": true, "boolean": true, "number": true}

func validateSupportedImport(imp *ast.ImportStmt) error {
	path := imp.Path
	if unquoted, err := strconv.Unquote(path); err == nil {
		path = unquoted
	}
	if strings.HasSuffix(path, ".ktmlschema") {
		return fmt.Errorf("schema import %s: Host Schema imports are unsupported in gohtml generator", path)
	}
	for _, specifier := range imp.Specifiers {
		if specifier.Alias != "" {
			return fmt.Errorf("import alias %s for %s is unsupported in gohtml generator", specifier.Alias, specifier.Name)
		}
	}
	return nil
}

// validateUndottedPaths rejects typed field paths, Keyed Repetitions, slot
// fills, conditions, doctypes, HTML comments (html/template strips them),
// and interpolated script attributes, which gohtml cannot lower.
func validateUndottedPaths(nodes []ast.Node) error {
	check := func(quoted *ast.QuotedValue) error {
		if quoted == nil {
			return nil
		}
		for _, segment := range quoted.Segments {
			if segment.Path != nil && len(segment.Path.Parts) > 1 {
				return fmt.Errorf("dotted path %s is unsupported in gohtml generator", segment.Path.String())
			}
		}
		return nil
	}
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.RepetitionNode:
			return fmt.Errorf("keyed repetition over %s: Keyed Repetition is unsupported in gohtml generator", value.Collection.String())
		case *ast.SlotNode:
			return fmt.Errorf("slot fill %s: named slots are unsupported in gohtml generator", value.Name)
		case *ast.ConditionNode:
			return fmt.Errorf("condition %s: conditions are unsupported in gohtml generator", value.Condition.String())
		case *ast.DoctypeNode:
			return fmt.Errorf("doctype %s is unsupported in gohtml generator", value.Name)
		case *ast.HTMLCommentNode:
			return fmt.Errorf("HTML comments are unsupported in gohtml generator")
		case *ast.RawTextNode:
			return fmt.Errorf("raw-text literals are unsupported in gohtml generator")
		case *ast.TextNode:
			if err := check(value.Quoted); err != nil {
				return err
			}
		case *ast.ElementNode:
			if compiler.RawTextElement(value.Name) && len(value.Children) > 0 {
				return fmt.Errorf("%s content is raw text, which is unsupported in gohtml generator", value.Name)
			}
			for _, attribute := range value.Attributes {
				if attribute.Condition != nil {
					return fmt.Errorf("conditional attributes are unsupported in gohtml generator")
				}
				if attribute.Spread != nil {
					return fmt.Errorf("attribute spreads are unsupported in gohtml generator")
				}
				if attribute.BagRange != nil {
					return fmt.Errorf("attribute bags are unsupported in gohtml generator")
				}
				if err := check(attribute.Quoted); err != nil {
					return err
				}
				if compiler.StyleAttribute(attribute.Name) && hasPathSegment(attribute.Quoted) {
					return fmt.Errorf("style attribute %s: interpolated style attributes are unsupported in gohtml generator", attribute.Name)
				}
				if compiler.ScriptAttribute(attribute.Name) && hasPathSegment(attribute.Quoted) {
					return fmt.Errorf("script attribute %s: interpolated script attributes are unsupported in gohtml generator", attribute.Name)
				}
			}
			if err := validateUndottedPaths(value.Children); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasPathSegment(quoted *ast.QuotedValue) bool {
	if quoted == nil {
		return false
	}
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return true
		}
	}
	return false
}

func mapKTMLTypeToGo(ktmlType string) string {
	switch ktmlType {
	case "string":
		return "string"
	case "number":
		return "Number"
	case "boolean":
		return "bool"
	default:
		return "any"
	}
}

func capitalize(s string) string {
	var result strings.Builder
	capitalizeNext := true
	for _, r := range s {
		if r == '-' {
			capitalizeNext = true
			continue
		}
		if capitalizeNext {
			r = unicode.ToUpper(r)
			capitalizeNext = false
		}
		result.WriteRune(r)
	}
	return result.String()
}

func exportedGoIdentifier(name string) bool {
	for _, current := range name {
		return unicode.IsUpper(current)
	}
	return false
}

func generateGoStructs(filePath string, decls map[string][]*ast.ElementDecl) string {
	var buf bytes.Buffer
	pkgName := filepath.Base(filepath.Dir(filePath))
	if pkgName == "." || pkgName == "/" {
		pkgName = "templates"
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filePath), "main.go")); err == nil {
		pkgName = "main"
	}
	pkgName = strings.ReplaceAll(pkgName, "-", "_")

	buf.WriteString(fmt.Sprintf("// Code generated by KTML. DO NOT EDIT.\npackage %s\n\n", pkgName))

	hasNumber := false
	for _, declList := range decls {
		for _, decl := range declList {
			for _, attr := range decl.Attributes {
				if attr.TypeDecl == "number" {
					hasNumber = true
				}
			}
		}
	}

	if hasNumber {
		buf.WriteString("type Number interface {\n\tint | float64 | float32\n}\n\n")
	}

	hasStructs := false
	names := make([]string, 0, len(decls))
	for name := range decls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		declList := decls[name]
		var baseDecl *ast.ElementDecl
		for _, d := range declList {
			isOverride := declarationIsOverride(d)
			if !isOverride {
				baseDecl = d
				break
			}
		}
		if baseDecl == nil || len(baseDecl.Attributes) == 0 {
			continue
		}
		hasStructs = true

		needsNumber := false
		for _, attr := range baseDecl.Attributes {
			if attr.TypeDecl == "number" {
				needsNumber = true
			}
		}

		typeParams := ""
		if needsNumber {
			typeParams = "[N Number]"
		}

		buf.WriteString(fmt.Sprintf("type %sInput%s struct {\n", capitalize(name), typeParams))
		for _, attr := range baseDecl.Attributes {
			goType := mapKTMLTypeToGo(attr.TypeDecl)
			if goType == "Number" {
				goType = "N"
			}
			buf.WriteString(fmt.Sprintf("\t%s %s\n", capitalize(attr.Name), goType))
		}
		buf.WriteString("}\n\n")
	}

	if !hasStructs {
		return ""
	}
	return buf.String()
}

func generateTemplate(decls []*ast.ElementDecl, devMode bool, filePath string) string {
	if len(decls) == 0 {
		return ""
	}
	name := decls[0].Name
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("{{define %q}}\n", name))

	var baseDecl *ast.ElementDecl
	var overrides []*ast.ElementDecl

	for _, d := range decls {
		isOverride := declarationIsOverride(d)
		if isOverride {
			overrides = append(overrides, d)
		} else {
			baseDecl = d
		}
	}

	if baseDecl == nil && len(overrides) > 0 {
		baseDecl = overrides[0]
		overrides = overrides[1:]
	}

	for i, override := range overrides {
		if i > 0 {
			sb.WriteString("{{else if ")
		} else {
			sb.WriteString("{{if ")
		}

		var conditions []string
		for _, a := range override.Attributes {
			if a.Value != "" {
				checkVal := a.Value
				if unq, err := strconv.Unquote(checkVal); err == nil {
					checkVal = unq
				}
				conditions = append(conditions, fmt.Sprintf("(eq .%s %q)", capitalize(a.Name), checkVal))
			}
		}
		if len(conditions) == 1 {
			sb.WriteString(strings.TrimSuffix(strings.TrimPrefix(conditions[0], "("), ")"))
		} else {
			sb.WriteString("and " + strings.Join(conditions, " "))
		}
		sb.WriteString("}}\n")

		for j, child := range override.Body {
			isRoot := j == 0
			sb.WriteString(generateNode(child, 1, devMode, isRoot, filePath, nil, nil))
		}
	}

	if len(overrides) > 0 {
		sb.WriteString("{{else}}\n")
	}

	if baseDecl != nil {
		for i, child := range baseDecl.Body {
			isRoot := i == 0
			sb.WriteString(generateNode(child, 1, devMode, isRoot, filePath, nil, nil))
		}
	}

	if len(overrides) > 0 {
		sb.WriteString("{{end}}\n")
	}

	sb.WriteString("{{end}}\n")
	return sb.String()
}

var varRegex = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func generateNode(node ast.Node, depth int, devMode bool, isRoot bool, filePath string, props map[string]string, injectedChildren []ast.Node) string {
	var sb strings.Builder
	indent := strings.Repeat("  ", depth)
	switch n := node.(type) {
	case *ast.ElementNode:
		if declList, ok := componentDecls[n.Name]; ok {
			newProps := make(map[string]string)
			var styleBuilder strings.Builder
			for _, attr := range n.Attributes {
				val := attr.Value
				if val == "true" {
					val = ""
				} else if unquoted, err := strconv.Unquote(val); err == nil {
					val = unquoted
				}
				if attr.Name == "style" {
					styleBuilder.WriteString(val)
					if !strings.HasSuffix(val, ";") {
						styleBuilder.WriteString(";")
					}
				} else {
					newProps[attr.Name] = val
				}
			}
			newProps["style"] = styleBuilder.String()

			if devMode {
				newProps["data-ktml-component"] = n.Name
				if newProps["data-ktml-id"] == "" {
					hashInput := n.Name
					for _, attr := range n.Attributes {
						hashInput += attr.Name + "=" + attr.Value
					}
					hash := sha256.Sum256([]byte(hashInput))
					newProps["data-ktml-id"] = fmt.Sprintf("ktml-%x", hash[:8])
				}
			}

			var baseDecl *ast.ElementDecl
			var overrides []*ast.ElementDecl
			for _, d := range declList {
				isOverride := declarationIsOverride(d)
				if isOverride {
					overrides = append(overrides, d)
				} else {
					baseDecl = d
				}
			}
			if baseDecl == nil && len(overrides) > 0 {
				baseDecl = overrides[0]
				overrides = overrides[1:]
			}

			renderBody := func(body []ast.Node) {
				propsClone := make(map[string]string, len(newProps))
				for k, v := range newProps {
					propsClone[k] = v
				}
				for j, child := range body {
					sb.WriteString(generateNode(child, depth, devMode, j == 0, filePath, propsClone, n.Children))
				}
			}

			for i, override := range overrides {
				if i > 0 {
					sb.WriteString(fmt.Sprintf("%s{{else if ", indent))
				} else {
					sb.WriteString(fmt.Sprintf("%s{{if ", indent))
				}
				var conditions []string
				for _, a := range override.Attributes {
					if a.Value != "" {
						checkVal := a.Value
						if unq, err := strconv.Unquote(checkVal); err == nil {
							checkVal = unq
						}
						passedVal := newProps[a.Name]
						if passedVal != "" {
							if strings.HasPrefix(passedVal, "{") && strings.HasSuffix(passedVal, "}") {
								varName := passedVal[1 : len(passedVal)-1]
								conditions = append(conditions, fmt.Sprintf("(eq .%s %q)", capitalize(varName), checkVal))
							} else {
								conditions = append(conditions, fmt.Sprintf("(eq %q %q)", passedVal, checkVal))
							}
						} else {
							conditions = append(conditions, fmt.Sprintf("(eq .%s %q)", capitalize(a.Name), checkVal))
						}
					}
				}
				if len(conditions) == 0 {
					conditions = append(conditions, "true")
				}
				if len(conditions) == 1 {
					sb.WriteString(strings.TrimSuffix(strings.TrimPrefix(conditions[0], "("), ")"))
				} else {
					sb.WriteString("and " + strings.Join(conditions, " "))
				}
				sb.WriteString("}}\n")

				renderBody(override.Body)
			}

			if len(overrides) > 0 {
				sb.WriteString(fmt.Sprintf("%s{{else}}\n", indent))
			}

			if baseDecl != nil {
				renderBody(baseDecl.Body)
			}

			if len(overrides) > 0 {
				sb.WriteString(fmt.Sprintf("%s{{end}}\n", indent))
			}

			return sb.String()
		}

		if len(n.Children) == 0 {
			if props != nil {
				if val, ok := props[n.Name]; ok {
					if val != "" {
						val = varRegex.ReplaceAllStringFunc(val, func(m string) string {
							vName := m[1 : len(m)-1]
							return fmt.Sprintf("{{.%s}}", capitalize(vName))
						})
						return fmt.Sprintf("%s%s\n", indent, val)
					}
				}
			}
			if n.Name == "children" && injectedChildren != nil {
				for _, child := range injectedChildren {
					sb.WriteString(generateNode(child, depth, devMode, false, filePath, props, nil))
				}
				return sb.String()
			}
		}

		sb.WriteString(fmt.Sprintf("%s<%s", indent, n.Name))

		hasKtmlId := false
		var nodeStyle string
		var nodeStyleQuoted *ast.QuotedValue
		var attrList []string

		for _, attr := range n.Attributes {
			val := attr.Value
			if unquoted, err := strconv.Unquote(val); err == nil {
				val = unquoted
			}
			if attr.Name == "style" {
				nodeStyleQuoted = attr.Quoted
				nodeStyle = quotedTemplate(attr.Quoted, val, props, false)
				continue
			}
			if attr.Name == "data-ktml-id" {
				hasKtmlId = true
			}
			// Only a valueless attribute is bare; a literal "true" keeps its value.
			if attr.Value == "" {
				attrList = append(attrList, fmt.Sprintf(" %s", attr.Name))
			} else {
				val = quotedTemplate(attr.Quoted, val, props, false)
				attrList = append(attrList, fmt.Sprintf(" %s=%q", attr.Name, val))
			}
		}

		if props != nil {
			if props["style"] != "" {
				if nodeStyle != "" {
					if !strings.HasSuffix(nodeStyle, ";") {
						nodeStyle += ";"
					}
					nodeStyle += " " + props["style"]
				} else {
					nodeStyle = props["style"]
				}
				props["style"] = ""
			}
			for k, v := range props {
				if strings.HasPrefix(k, "data-ktml-") || k == "id" {
					attrList = append(attrList, fmt.Sprintf(" %s=%q", k, v))
					delete(props, k)
					if k == "data-ktml-id" {
						hasKtmlId = true
					}
				}
			}
		}

		for _, a := range attrList {
			sb.WriteString(a)
		}

		if nodeStyle != "" {
			if nodeStyleQuoted == nil {
				nodeStyle = quotedTemplate(nil, nodeStyle, nil, false)
			}
			sb.WriteString(fmt.Sprintf(" style=%q", nodeStyle))
		}

		if isRoot && devMode {
			sb.WriteString(fmt.Sprintf(" data-ktml-file=%q", filePath))
			if !hasKtmlId {
				hash := sha256.Sum256([]byte(filePath))
				sb.WriteString(fmt.Sprintf(" data-ktml-id=\"ktml-%x\"", hash[:8]))
			}
		}

		if len(n.Children) == 0 {
			sb.WriteString("></" + n.Name + ">\n")
		} else {
			sb.WriteString(">\n")
			for _, child := range n.Children {
				sb.WriteString(generateNode(child, depth+1, devMode, false, filePath, props, injectedChildren))
			}
			sb.WriteString(fmt.Sprintf("%s</%s>\n", indent, n.Name))
		}
	case *ast.TextNode:
		val := n.Value
		if unquoted, err := strconv.Unquote(val); err == nil {
			val = unquoted
		}

		val = quotedTemplate(n.Quoted, val, props, true)

		sb.WriteString(fmt.Sprintf("%s%s\n", indent, val))
	}
	return sb.String()
}

func declarationIsOverride(declaration *ast.ElementDecl) bool {
	for _, attribute := range declaration.Attributes {
		if attribute.Kind == ast.OverrideDeclaration {
			return true
		}
		// Compatibility for hand-built prototype ASTs only. Parsed source always
		// carries an explicit declaration kind.
		if attribute.Kind == "" && attribute.TypeDecl == "" && attribute.Value != "" {
			return true
		}
	}
	return false
}

func quotedTemplate(quoted *ast.QuotedValue, fallback string, props map[string]string, escapeLiterals bool) string {
	if quoted == nil {
		return varRegex.ReplaceAllStringFunc(fallback, func(match string) string {
			name := match[1 : len(match)-1]
			if props != nil {
				if value, ok := props[name]; ok && value != "" {
					return quotedTemplate(nil, value, nil, escapeLiterals)
				}
			}
			return "{{." + capitalize(name) + "}}"
		})
	}
	var value strings.Builder
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			literal := segment.Literal
			if escapeLiterals {
				literal = html.EscapeString(literal)
			}
			value.WriteString(literal)
			continue
		}
		if segment.Kind != ast.PathSegment || segment.Path == nil {
			continue
		}
		if len(segment.Path.Parts) == 1 && props != nil {
			if mapped, ok := props[segment.Path.Parts[0].Name]; ok && mapped != "" {
				value.WriteString(quotedTemplate(nil, mapped, nil, escapeLiterals))
				continue
			}
		}
		parts := make([]string, len(segment.Path.Parts))
		for i, part := range segment.Path.Parts {
			parts[i] = capitalize(part.Name)
		}
		value.WriteString("{{.")
		value.WriteString(strings.Join(parts, "."))
		value.WriteString("}}")
	}
	return value.String()
}
