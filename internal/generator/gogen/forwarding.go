package gogen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// ownedNames returns the lower-cased names an element writes itself, in any
// branch; a forwarded entry with one of them is dropped.
func ownedNames(entries []*ast.AttributeNode) []string {
	seen := make(map[string]bool)
	var names []string
	for _, attribute := range ast.NamedAttributes(entries) {
		name := strings.ToLower(attribute.Name)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// spread writes ...path through rt.Attributes, dropping the names the
// element owns.
func (g *generator) spread(spread *ast.AttributeSpread, fieldTypes map[string]input) error {
	expression, pathType, err := g.pathExpression(spread.Path, fieldTypes)
	if err != nil {
		return fmt.Errorf("spread ...%s: %w", spread.Path.String(), err)
	}
	if !isContextual(pathType, compiler.AttributesType) {
		return fmt.Errorf("spread ...%s is %s, not Attributes", spread.Path.String(), pathType)
	}
	owned := "nil"
	if len(g.owned) > 0 {
		quoted := make([]string, len(g.owned))
		for i, name := range g.owned {
			quoted[i] = strconv.Quote(name)
		}
		owned = g.literalVariable("[]string{" + strings.Join(quoted, ", ") + "}")
	}
	g.writeValue("rt.Attributes", expression+", "+owned)
	return nil
}

func isContextual(typ compiler.Type, name string) bool {
	return typ.Kind == compiler.ContextualTypeKind && typ.Name == name
}

// bagExpression lowers an attribute bag literal to a ktml.Attributes value
// built at the call site. Names and contexts were checked by the compiler,
// so the Must constructors cannot fail.
func (g *generator) bagExpression(entries []*ast.AttributeNode, fieldTypes map[string]input) (string, error) {
	g.usesKTML = true
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		part, err := g.bagEntry(entry, fieldTypes)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	switch len(parts) {
	case 0:
		return "ktml.Attributes{}", nil
	case 1:
		return parts[0], nil
	}
	return "ktml.JoinAttributes(" + strings.Join(parts, ", ") + ")", nil
}

func (g *generator) bagEntry(entry *ast.AttributeNode, fieldTypes map[string]input) (string, error) {
	switch {
	case entry.Condition != nil:
		// The branches are built lazily: a branch may read an optional that
		// only its condition proves present.
		group := entry.Condition
		condition, err := g.conditionExpression(group.Condition, fieldTypes)
		if err != nil {
			return "", err
		}
		whenTrue, whenFalse := compiler.ConditionNarrowing(group.Condition)
		g.bagSequence++
		variable := fmt.Sprintf("ktmlBag%d", g.bagSequence)
		g.usesKTML = true
		g.emitf("\tvar %s ktml.Attributes\n\tif %s {\n", variable, condition)
		if err := g.withNarrowed(whenTrue, func() error {
			then, err := g.bagExpression(group.Then, fieldTypes)
			if err == nil {
				g.emitf("\t%s = %s\n", variable, then)
			}
			return err
		}); err != nil {
			return "", err
		}
		g.emitln("\t} else {")
		if err := g.withNarrowed(whenFalse, func() error {
			otherwise, err := g.bagExpression(group.Else, fieldTypes)
			if err == nil {
				g.emitf("\t%s = %s\n", variable, otherwise)
			}
			return err
		}); err != nil {
			return "", err
		}
		g.emitln("\t}")
		return variable, nil
	case entry.Spread != nil:
		expression, pathType, err := g.pathExpression(entry.Spread.Path, fieldTypes)
		if err != nil {
			return "", err
		}
		if !isContextual(pathType, compiler.AttributesType) {
			return "", fmt.Errorf("spread ...%s is %s, not Attributes", entry.Spread.Path.String(), pathType)
		}
		return expression, nil
	case entry.BagRange != nil:
		return "", fmt.Errorf("attribute %s: an attribute bag is only the value of an Attributes input", entry.Name)
	}
	name := strconv.Quote(entry.Name)
	script, style := compiler.ScriptAttribute(entry.Name), compiler.StyleAttribute(entry.Name)
	url := !script && !style && compiler.URLAttribute(entry.Name)
	if entry.Quoted == nil && entry.Value == "" {
		return "ktml.MustBoolAttr(" + name + ", true)", nil
	}
	if path, ok := exactPath(entry.Quoted); ok {
		expression, pathType, err := g.pathExpression(path, fieldTypes)
		if err != nil {
			return "", fmt.Errorf("attribute %s: %w", entry.Name, err)
		}
		switch {
		case script && pathType.IsScalar(compiler.BooleanType):
			return "ktml.MustScriptBoolAttr(" + name + ", " + expression + ")", nil
		case script && isContextual(pathType, compiler.TrustedScriptType):
			return "ktml.MustScriptAttr(" + name + ", " + expression + ")", nil
		case (script || !style) && isContextual(pathType, compiler.JSONValueType):
			return "ktml.MustJSONAttr(" + name + ", " + expression + ")", nil
		case style && isContextual(pathType, compiler.CSSValueType):
			return "ktml.MustCSSAttr(" + name + ", " + expression + ")", nil
		case script || style:
		case url && !isContextual(pathType, compiler.URLType) && !isContextual(pathType, compiler.TrustedURLType):
		case pathType.IsScalar(compiler.BooleanType) && compiler.ARIAAttribute(entry.Name):
			g.usesRuntime = true
			return "ktml.MustAttr(" + name + ", rt.BoolText(" + expression + "))", nil
		case pathType.IsScalar(compiler.BooleanType):
			return "ktml.MustBoolAttr(" + name + ", " + expression + ")", nil
		case isContextual(pathType, compiler.URLType):
			return "ktml.MustURLAttr(" + name + ", " + expression + ")", nil
		case isContextual(pathType, compiler.TrustedURLType):
			return "ktml.MustTrustedURLAttr(" + name + ", " + expression + ")", nil
		case pathType.Kind == compiler.ScalarTypeKind:
			text, err := g.bagString(entry.Quoted, fieldTypes)
			if err != nil {
				return "", err
			}
			return "ktml.MustAttr(" + name + ", " + text + ")", nil
		}
		return "", fmt.Errorf("attribute %s cannot carry %s of type %s", entry.Name, path.String(), pathType)
	}
	if hasPath(entry.Quoted) {
		if script || style {
			return "", fmt.Errorf("attribute %s in an attribute bag takes a literal or one whole value", entry.Name)
		}
		if url && !compiler.SafeURLPrefix(entry.Quoted) {
			return "", fmt.Errorf("URL attribute %s: literal text before the first path must fix the scheme and host", entry.Name)
		}
		text, err := g.bagString(entry.Quoted, fieldTypes)
		if err != nil {
			return "", err
		}
		if url {
			// The literal text fixes the scheme and host, as for an element.
			return "ktml.MustTrustedURLAttr(" + name + ", ktml.TrustURL(" + text + "))", nil
		}
		return "ktml.MustAttr(" + name + ", " + text + ")", nil
	}
	// A literal is template source, so it is trusted in its context.
	literal := strconv.Quote(quotedLiteral(entry.Quoted, entry.Value))
	switch {
	case script:
		return "ktml.MustScriptAttr(" + name + ", ktml.TrustScript(" + literal + "))", nil
	case style:
		return "ktml.MustCSSAttr(" + name + ", ktml.TrustCSS(" + literal + "))", nil
	case url:
		return "ktml.MustTrustedURLAttr(" + name + ", ktml.TrustURL(" + literal + "))", nil
	}
	return "ktml.MustAttr(" + name + ", " + literal + ")", nil
}

// bagString lowers a quoted value of string, integer, and number paths to a
// Go string expression, formatted as the attribute escapers write them.
func (g *generator) bagString(quoted *ast.QuotedValue, fieldTypes map[string]input) (string, error) {
	parts := make([]string, 0, len(quoted.Segments))
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			if segment.Literal != "" {
				parts = append(parts, strconv.Quote(segment.Literal))
			}
			continue
		}
		expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
		if err != nil {
			return "", err
		}
		switch {
		case pathType.IsScalar(compiler.StringType):
			parts = append(parts, expression)
		case pathType.IsScalar(compiler.IntegerType):
			g.usesRuntime = true
			parts = append(parts, "rt.FormatInt("+expression+")")
		case pathType.IsScalar(compiler.NumberType):
			g.usesRuntime = true
			parts = append(parts, "rt.FormatNumber("+expression+")")
		default:
			return "", fmt.Errorf("path %s of type %s cannot be part of a text value", segment.Path.String(), pathType)
		}
	}
	if len(parts) == 0 {
		return strconv.Quote(""), nil
	}
	return strings.Join(parts, " + "), nil
}
