package interp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/rt"
)

// ownedNames returns the lower-cased names an element writes itself, which
// its spread drops. It matches gogen's list.
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

// spreadOp writes a forwarded Attributes value.
type spreadOp struct {
	path  pathRef
	owned []string
}

func (operation spreadOp) run(s *state) error {
	if err := rt.Attributes(s.w, operation.path.value(s).Interface().(ktml.Attributes), operation.owned); err != nil {
		return rt.WriteError(s.model.goID, err)
	}
	return nil
}

func (u *unit) spread(b *builder, spread *ast.AttributeSpread, owned []string, roots scope) error {
	ref, pathType, err := u.path(spread.Path, roots)
	if err != nil {
		return fmt.Errorf("spread ...%s: %w", spread.Path.String(), err)
	}
	if !isContextual(pathType, compiler.AttributesType) {
		return fmt.Errorf("spread ...%s is %s, not Attributes", spread.Path.String(), pathType)
	}
	b.add(spreadOp{path: ref, owned: owned})
	return nil
}

// bagFunc builds one attribute bag literal value at render time.
type bagFunc func(*state) ktml.Attributes

// bag compiles an attribute bag literal with the constructors gogen calls,
// so both backends build equal values.
func (u *unit) bag(entries []*ast.AttributeNode, roots scope) (bagFunc, error) {
	parts := make([]bagFunc, 0, len(entries))
	for _, entry := range entries {
		part, err := u.bagEntry(entry, roots)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return func(s *state) ktml.Attributes {
		switch len(parts) {
		case 0:
			return ktml.Attributes{}
		case 1:
			return parts[0](s)
		}
		bags := make([]ktml.Attributes, len(parts))
		for i, part := range parts {
			bags[i] = part(s)
		}
		return ktml.JoinAttributes(bags...)
	}, nil
}

func (u *unit) bagEntry(entry *ast.AttributeNode, roots scope) (bagFunc, error) {
	switch {
	case entry.Condition != nil:
		test, err := u.test(entry.Condition.Condition, roots)
		if err != nil {
			return nil, err
		}
		then, err := u.bag(entry.Condition.Then, roots)
		if err != nil {
			return nil, err
		}
		otherwise, err := u.bag(entry.Condition.Else, roots)
		if err != nil {
			return nil, err
		}
		return func(s *state) ktml.Attributes {
			// Lazily, as gogen: a branch may read an optional only its
			// condition proves present.
			if test(s) {
				return then(s)
			}
			return otherwise(s)
		}, nil
	case entry.Spread != nil:
		ref, pathType, err := u.path(entry.Spread.Path, roots)
		if err != nil {
			return nil, err
		}
		if !isContextual(pathType, compiler.AttributesType) {
			return nil, fmt.Errorf("spread ...%s is %s, not Attributes", entry.Spread.Path.String(), pathType)
		}
		return func(s *state) ktml.Attributes { return ref.value(s).Interface().(ktml.Attributes) }, nil
	case entry.BagRange != nil:
		return nil, fmt.Errorf("attribute %s: an attribute bag is only the value of an Attributes input", entry.Name)
	}
	name := entry.Name
	script, style := compiler.ScriptAttribute(name), compiler.StyleAttribute(name)
	url := !script && !style && compiler.URLAttribute(name)
	if entry.Quoted == nil && entry.Value == "" {
		return func(*state) ktml.Attributes { return ktml.MustBoolAttr(name, true) }, nil
	}
	if path, ok := exactPath(entry.Quoted); ok {
		ref, pathType, err := u.path(path, roots)
		if err != nil {
			return nil, fmt.Errorf("attribute %s: %w", name, err)
		}
		switch {
		case script && pathType.IsScalar(compiler.BooleanType):
			return func(s *state) ktml.Attributes { return ktml.MustScriptBoolAttr(name, ref.value(s).Bool()) }, nil
		case script && isContextual(pathType, compiler.TrustedScriptType):
			return func(s *state) ktml.Attributes {
				return ktml.MustScriptAttr(name, ref.value(s).Interface().(ktml.TrustedScript))
			}, nil
		case (script || !style) && isContextual(pathType, compiler.JSONValueType):
			return func(s *state) ktml.Attributes {
				return ktml.MustJSONAttr(name, ref.value(s).Interface().(ktml.JSONValue))
			}, nil
		case style && isContextual(pathType, compiler.CSSValueType):
			return func(s *state) ktml.Attributes {
				return ktml.MustCSSAttr(name, ref.value(s).Interface().(ktml.CSSValue))
			}, nil
		case script || style:
		case url && !isContextual(pathType, compiler.URLType) && !isContextual(pathType, compiler.TrustedURLType):
		case pathType.IsScalar(compiler.BooleanType) && compiler.ARIAAttribute(name):
			return func(s *state) ktml.Attributes { return ktml.MustAttr(name, rt.BoolText(ref.value(s).Bool())) }, nil
		case pathType.IsScalar(compiler.BooleanType):
			return func(s *state) ktml.Attributes { return ktml.MustBoolAttr(name, ref.value(s).Bool()) }, nil
		case isContextual(pathType, compiler.URLType):
			return func(s *state) ktml.Attributes { return ktml.MustURLAttr(name, ref.value(s).Interface().(ktml.URL)) }, nil
		case isContextual(pathType, compiler.TrustedURLType):
			return func(s *state) ktml.Attributes {
				return ktml.MustTrustedURLAttr(name, ref.value(s).Interface().(ktml.TrustedURL))
			}, nil
		case pathType.Kind == compiler.ScalarTypeKind:
			text, err := u.bagString(entry.Quoted, roots)
			if err != nil {
				return nil, err
			}
			return func(s *state) ktml.Attributes { return ktml.MustAttr(name, text(s)) }, nil
		}
		return nil, fmt.Errorf("attribute %s cannot carry %s of type %s", name, path.String(), pathType)
	}
	if hasPath(entry.Quoted) {
		if script || style {
			return nil, fmt.Errorf("attribute %s in an attribute bag takes a literal or one whole value", name)
		}
		if url && !compiler.SafeURLPrefix(entry.Quoted) {
			return nil, fmt.Errorf("URL attribute %s: literal text before the first path must fix the scheme and host", name)
		}
		text, err := u.bagString(entry.Quoted, roots)
		if err != nil {
			return nil, err
		}
		if url {
			// The literal text fixes the scheme and host, as for an element.
			return func(s *state) ktml.Attributes { return ktml.MustTrustedURLAttr(name, ktml.TrustURL(text(s))) }, nil
		}
		return func(s *state) ktml.Attributes { return ktml.MustAttr(name, text(s)) }, nil
	}
	literal := quotedLiteral(entry.Quoted, entry.Value)
	var value ktml.Attributes
	switch {
	case script:
		value = ktml.MustScriptAttr(name, ktml.TrustScript(literal))
	case style:
		value = ktml.MustCSSAttr(name, ktml.TrustCSS(literal))
	case url:
		value = ktml.MustTrustedURLAttr(name, ktml.TrustURL(literal))
	default:
		value = ktml.MustAttr(name, literal)
	}
	return func(*state) ktml.Attributes { return value }, nil
}

// bagString compiles a quoted value of string, integer, and number paths,
// formatted as the attribute escapers write them.
func (u *unit) bagString(quoted *ast.QuotedValue, roots scope) (func(*state) string, error) {
	var parts []func(*state) string
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			literal := segment.Literal
			parts = append(parts, func(*state) string { return literal })
			continue
		}
		ref, pathType, err := u.path(segment.Path, roots)
		if err != nil {
			return nil, err
		}
		switch {
		case pathType.IsScalar(compiler.StringType):
			parts = append(parts, func(s *state) string { return ref.value(s).String() })
		case pathType.IsScalar(compiler.IntegerType):
			parts = append(parts, func(s *state) string { return rt.FormatInt(ref.value(s).Int()) })
		case pathType.IsScalar(compiler.NumberType):
			parts = append(parts, func(s *state) string { return rt.FormatNumber(ref.value(s).Float()) })
		default:
			return nil, fmt.Errorf("path %s of type %s cannot be part of a text value", segment.Path.String(), pathType)
		}
	}
	return func(s *state) string {
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part(s))
		}
		return text.String()
	}, nil
}

// bagArgument compiles an attribute bag literal supplied to an Attributes
// input.
func (u *unit) bagArgument(callee string, attribute *ast.AttributeNode, declared compiler.Input, roots scope) (argument, error) {
	if !isContextual(declared.Type, compiler.AttributesType) {
		return argument{}, fmt.Errorf("component %s input %s requires %s, not an attribute bag", callee, attribute.Name, declared.Type)
	}
	build, err := u.bag(attribute.Bag, roots)
	if err != nil {
		return argument{}, fmt.Errorf("component %s input %s: %w", callee, attribute.Name, err)
	}
	return argument{name: attribute.Name, bag: build}, nil
}
