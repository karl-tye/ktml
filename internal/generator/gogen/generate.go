// Package gogen generates production Go renderers from validated KTML syntax.
package gogen

import (
	"bytes"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/rt"
)

type Options struct {
	Package string
	// Packages maps a source directory to its Go package. It is required only
	// when generated code references declarations from another directory.
	Packages func(dir string) (GoPackage, error)
	// Development selects the ktmldev variant of GenerateFile, which writes
	// Render Identity boundaries. The default is the production variant.
	Development bool
	// API selects the public Go API. The zero value is the frozen ADR-0003
	// Hybrid H; APIDirect exists only for the benchmark floor.
	API API
}

// API is the public Go shape generated for a component.
type API uint8

const (
	// APIStruct is Hybrid H, the frozen API (ADR-0003): the X struct holds
	// the inputs and is itself the component, with a value-receiver Render
	// method.
	APIStruct API = iota
	// APIDirect is Prototype B, kept only as the benchmark floor: an XProps
	// struct and a direct RenderX(ctx, w, XProps) function. Content fields
	// are ktml.ComponentFunc.
	APIDirect
)

// String returns the ADR-0003 candidate letter.
func (api API) String() string {
	switch api {
	case APIStruct:
		return "H"
	case APIDirect:
		return "B"
	default:
		return fmt.Sprintf("API(%d)", uint8(api))
	}
}

// GoPackage identifies the Go package generated for one source directory.
type GoPackage struct {
	ImportPath string
	Name       string
}

// component is the generator's view of one component signature. Body is set
// only for components declared by the generated document.
type component struct {
	name   string
	file   string
	inputs []input
	body   []ast.Node
	group  *compiler.ClauseGroup
	// source is the declaration's name range.
	source compiler.SourceRange
}

// input is a path root: a component input, or a clause binding whose Go
// expression is expr.
type input struct {
	name         string
	typ          compiler.Type
	mode         ast.InputMode
	defaultValue *compiler.ScalarValue
	expr         string
	// source is the declaration's name range; implicit children have none.
	source compiler.SourceRange
}

func (model component) inputNamed(name string) (input, bool) {
	for _, field := range model.inputs {
		if field.name == name {
			return field, true
		}
	}
	return input{}, false
}

func (info input) isContent() bool {
	return info.typ.Kind == compiler.ContentTypeKind
}

type generator struct {
	output       bytes.Buffer
	options      Options
	snapshot     *compiler.Snapshot
	file         string
	dir          string
	components   map[string]component
	imports      map[string]string
	usesRuntime  bool
	usesKTML     bool
	usesDev      bool
	bindSequence int

	// current is the component being generated; identity is the Go variable
	// holding the innermost repetition item's Render Identity in the
	// development variant.
	current            string
	identity           string
	repetitionSequence int

	// narrowed holds the path spellings a presence condition proves present
	// where code is being generated.
	narrowed []string
	// contentSequence numbers fill closures.
	contentSequence int
	// literals declares the package variables that hold the current
	// component's literal optional and defaulted call inputs, by expression.
	literals     []string
	literalNames map[string]string

	// pending holds escaped static markup not yet written, so adjacent
	// static runs become one write. Every other emission flushes it first.
	pending strings.Builder
	// currentID is the quoted canonical ID of the component being generated,
	// for render errors.
	currentID string
	// instanceRoot is the root element of the single-root body being
	// generated, which carries the instance attribute in the ktmldev variant.
	instanceRoot *ast.ElementNode
	// owned holds the lower-cased names the element being generated writes
	// itself, which its spread drops.
	owned []string
	// bagSequence numbers the variables of conditional attribute bag
	// entries.
	bagSequence int

	// target is the generated file, which line directives are relative to;
	// marks holds the source range of each placeholder written so far
	// (sourcemap.go). Generation without a target maps nothing.
	target string
	marks  []compiler.SourceRange
}

// emitf writes generated Go after flushing pending static markup.
func (g *generator) emitf(format string, args ...any) {
	g.flush()
	fmt.Fprintf(&g.output, format, args...)
}

// emitln writes one line of generated Go after flushing pending static
// markup.
func (g *generator) emitln(line string) {
	g.flush()
	fmt.Fprintln(&g.output, line)
}

// flush writes pending static markup as one write.
func (g *generator) flush() {
	if g.pending.Len() == 0 {
		return
	}
	value := g.pending.String()
	g.pending.Reset()
	g.usesRuntime = true
	fmt.Fprintf(&g.output, "\tif err := rt.Raw(w, %s); err != nil { return rt.WriteError(%s, err) }\n", strconv.Quote(value), g.currentID)
}

// GenerateSnapshot emits one Go package from a validated cross-file workspace
// graph. Imported component declarations are generated alongside their callers.
// It supports scalar inputs only; record-typed documents use GenerateFile.
func GenerateSnapshot(snapshot *compiler.Snapshot, options Options) ([]byte, error) {
	if snapshot == nil || !snapshot.Valid() {
		return nil, fmt.Errorf("generate: workspace snapshot contains errors")
	}
	document := &ast.Document{}
	for _, sourceDocument := range snapshot.Documents() {
		document.Nodes = append(document.Nodes, sourceDocument.Nodes...)
	}
	return Generate(document, options)
}

// Generate emits deterministic, direct io.Writer renderers for the supported
// typed KTML subset of one syntax document with scalar inputs.
func Generate(document *ast.Document, options Options) ([]byte, error) {
	if document == nil {
		return nil, fmt.Errorf("generate: nil document")
	}
	if options.Package == "" {
		return nil, fmt.Errorf("generate: package is required")
	}
	g := &generator{options: options, components: make(map[string]component)}
	var order []string
	goNames := make(map[string]string)
	for _, node := range document.Nodes {
		switch value := node.(type) {
		case *ast.StateDecl:
			return nil, fmt.Errorf("state %s requires a workspace snapshot; use GenerateSchema", value.Name)
		case *ast.RenderClause:
			return nil, fmt.Errorf("render clauses of %s require a workspace snapshot; use GenerateFile", value.Name)
		}
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		if declaration.IsSignature() {
			return nil, fmt.Errorf("component signature %s requires a workspace snapshot; use GenerateFile", declaration.Name)
		}
		if _, exists := g.components[declaration.Name]; exists {
			return nil, fmt.Errorf("generate: duplicate component %q is not supported", declaration.Name)
		}
		goName := exported(declaration.Name)
		for _, name := range []string{goName, goName + "Props", "Render" + goName} {
			if first, exists := goNames[name]; exists {
				return nil, fmt.Errorf("generate: components %s and %s both generate Go name %s", first, declaration.Name, name)
			}
			goNames[name] = declaration.Name
		}
		model := component{name: declaration.Name, body: declaration.Body}
		for _, attribute := range declaration.Attributes {
			if attribute.Kind == ast.OverrideDeclaration {
				return nil, fmt.Errorf("component %s uses declaration overrides, which direct Go generation does not support", declaration.Name)
			}
			typ, ok := scalarType(attribute.TypeDecl)
			if attribute.TypeDecl == compiler.ContentTypeName {
				typ, ok = compiler.Type{Kind: compiler.ContentTypeKind}, true
			}
			if !ok {
				return nil, fmt.Errorf("component %s input %s: unsupported type %q without a workspace snapshot", declaration.Name, attribute.Name, attribute.TypeDecl)
			}
			model.inputs = append(model.inputs, input{name: attribute.Name, typ: typ, mode: attribute.Mode, defaultValue: syntaxScalar(attribute.Default, typ)})
		}
		if _, declared := model.inputNamed(compiler.ChildrenInput); !declared && compiler.UsesChildrenOutlet(declaration.Body) {
			model.inputs = append(model.inputs, input{name: compiler.ChildrenInput, typ: compiler.Type{Kind: compiler.ContentTypeKind}, mode: ast.OptionalInput})
		}
		g.components[declaration.Name] = model
		order = append(order, declaration.Name)
	}
	generated, _, err := g.generate(order)
	return generated, err
}

// GenerateFile emits the components declared by one validated component
// document. Components and records from the same directory are referenced by
// name; those from another directory are imported through Options.Packages.
func GenerateFile(snapshot *compiler.Snapshot, file string, options Options) ([]byte, error) {
	generated, _, err := GenerateFileMap(snapshot, file, options)
	return generated, err
}

// GenerateFileMap is GenerateFile that also returns the source map its line
// directives encode, in file order.
func GenerateFileMap(snapshot *compiler.Snapshot, file string, options Options) ([]byte, []SourceMapping, error) {
	if snapshot == nil || !snapshot.Valid() {
		return nil, nil, fmt.Errorf("generate: workspace snapshot contains errors")
	}
	if options.Package == "" {
		return nil, nil, fmt.Errorf("generate: package is required")
	}
	path, err := filepath.Abs(file)
	if err != nil {
		return nil, nil, err
	}
	document := snapshot.Document(path)
	if document == nil || compiler.IsSchemaPath(path) {
		return nil, nil, fmt.Errorf("generate: %s is not a loaded component document", file)
	}
	g := &generator{options: options, snapshot: snapshot, file: path, dir: filepath.Dir(path), components: make(map[string]component), target: OutputFile(path, options.Development)}
	for _, visible := range snapshot.VisibleComponents(path) {
		model := component{name: visible.Name, file: visible.File}
		if visible.File == path {
			model.source = visible.Range
		}
		for _, semantic := range visible.Inputs {
			declared := input{name: semantic.Name, typ: semantic.Type, mode: ast.InputMode(semantic.Mode), defaultValue: semantic.Default}
			if visible.File == path && !semantic.Implicit {
				declared.source = semantic.NameRange
			}
			model.inputs = append(model.inputs, declared)
		}
		g.components[visible.Name] = model
	}
	var order []string
	for _, node := range document.Nodes {
		declaration, ok := node.(*ast.ElementDecl)
		if !ok {
			continue
		}
		for _, attribute := range declaration.Attributes {
			if attribute.Kind == ast.OverrideDeclaration {
				return nil, nil, fmt.Errorf("component %s uses declaration overrides, which direct Go generation does not support", declaration.Name)
			}
		}
		model := g.components[declaration.Name]
		model.body = declaration.Body
		if declaration.IsSignature() {
			id, _ := snapshot.Resolve(path, declaration.Name)
			group, ok := snapshot.ClauseGroup(id.ID)
			if !ok {
				return nil, nil, fmt.Errorf("component signature %s has no validated render clauses", declaration.Name)
			}
			model.group = &group
		}
		g.components[declaration.Name] = model
		order = append(order, declaration.Name)
	}
	return g.generate(order)
}

// OutputFile returns the Go file generated for a .ktml or .ktmlschema
// source: foo.ktml becomes foo_ktml.go, or foo_ktml_dev.go for the ktmldev
// variant, and foo.ktmlschema becomes foo_ktmlschema.go, in the source's
// directory.
func OutputFile(source string, development bool) string {
	if compiler.IsSchemaPath(source) {
		return strings.TrimSuffix(source, ".ktmlschema") + "_ktmlschema.go"
	}
	if development {
		return strings.TrimSuffix(source, ".ktml") + "_ktml_dev.go"
	}
	return strings.TrimSuffix(source, ".ktml") + "_ktml.go"
}

func (g *generator) generate(order []string) ([]byte, []SourceMapping, error) {
	var body bytes.Buffer
	// Hybrid H lifts fill closures into named types (fills.go), which can
	// leave package ktml unreferenced.
	lift := g.options.API == APIStruct
	usesKTML := false
	for _, name := range order {
		g.output.Reset()
		g.literals, g.literalNames = nil, nil
		if err := g.component(g.components[name]); err != nil {
			return nil, nil, err
		}
		if len(g.literals) > 0 {
			g.emitln("// Literal optional and defaulted call inputs. Renderers only read them.")
			g.emitf("var (\n\t%s\n)\n\n", strings.Join(g.literals, "\n\t"))
		}
		g.flush()
		if !lift {
			body.Write(g.output.Bytes())
			continue
		}
		lifted, references, err := liftFills(exported(name), g.output.Bytes())
		if err != nil {
			return nil, nil, err
		}
		usesKTML = usesKTML || references
		body.Write(lifted)
	}
	if lift {
		g.usesKTML = usesKTML
	}
	var file bytes.Buffer
	if g.snapshot != nil {
		// Per-file production and development variants (ADR-0003).
		constraint := "!ktmldev"
		if g.options.Development {
			constraint = "ktmldev"
		}
		fmt.Fprintf(&file, "//go:build %s\n\n", constraint)
	}
	fmt.Fprintf(&file, "// Code generated by KTML. DO NOT EDIT.\npackage %s\n\nimport (\n\t\"context\"\n\t\"io\"\n\n", g.options.Package)
	if g.usesKTML {
		fmt.Fprintln(&file, "\t\"github.com/karl-tye/ktml\"")
	}
	if g.usesDev {
		fmt.Fprintln(&file, "\t\"github.com/karl-tye/ktml/dev\"")
	}
	if g.usesRuntime {
		fmt.Fprintln(&file, "\t\"github.com/karl-tye/ktml/rt\"")
	}
	writeImports(&file, g.imports)
	fmt.Fprint(&file, ")\n\n")
	file.Write(body.Bytes())
	return g.finish(file.Bytes())
}

func writeImports(output *bytes.Buffer, imports map[string]string) {
	paths := make([]string, 0, len(imports))
	for path := range imports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if alias := imports[path]; alias != pathpkg.Base(path) {
			fmt.Fprintf(output, "\t%s %s\n", alias, strconv.Quote(path))
		} else {
			fmt.Fprintf(output, "\t%s\n", strconv.Quote(path))
		}
	}
}

// qualifier returns the package qualifier for a declaration in dir, adding a
// deterministic import when it belongs to another Go package.
func (g *generator) qualifier(dir string) (string, error) {
	if g.snapshot == nil || dir == g.dir {
		return "", nil
	}
	if g.options.Packages == nil {
		return "", fmt.Errorf("declarations in %s need Options.Packages to import their Go package", dir)
	}
	pkg, err := g.options.Packages(dir)
	if err != nil {
		return "", fmt.Errorf("resolve Go package for %s: %w", dir, err)
	}
	if g.imports == nil {
		g.imports = make(map[string]string)
	}
	if alias, ok := g.imports[pkg.ImportPath]; ok {
		return alias + ".", nil
	}
	alias := pkg.Name
	taken := func(candidate string) bool {
		if candidate == "ktml" || candidate == "rt" || candidate == "context" || candidate == "io" {
			return true
		}
		for _, existing := range g.imports {
			if existing == candidate {
				return true
			}
		}
		return false
	}
	for suffix := 2; taken(alias); suffix++ {
		alias = pkg.Name + strconv.Itoa(suffix)
	}
	g.imports[pkg.ImportPath] = alias
	return alias + ".", nil
}

func (g *generator) goType(value compiler.Type) (string, error) {
	switch value.Kind {
	case compiler.ScalarTypeKind:
		switch value.Scalar {
		case compiler.StringType:
			return "string", nil
		case compiler.BooleanType:
			return "bool", nil
		case compiler.NumberType:
			return "float64", nil
		case compiler.IntegerType:
			return "int64", nil
		}
	case compiler.RecordTypeKind:
		if g.snapshot == nil {
			return "", fmt.Errorf("record type %s requires a workspace snapshot", value.Name)
		}
		record, ok := g.snapshot.Record(value.Record)
		if !ok {
			return "", fmt.Errorf("unknown record %s", value.Name)
		}
		qualifier, err := g.qualifier(filepath.Dir(record.File))
		if err != nil {
			return "", err
		}
		return qualifier + record.Name, nil
	case compiler.StateTypeKind:
		if g.snapshot == nil {
			return "", fmt.Errorf("state type %s requires a workspace snapshot", value.Name)
		}
		state, ok := g.snapshot.State(value.State)
		if !ok {
			return "", fmt.Errorf("unknown state %s", value.Name)
		}
		qualifier, err := g.qualifier(filepath.Dir(state.File))
		if err != nil {
			return "", err
		}
		return qualifier + state.Name, nil
	case compiler.ContextualTypeKind:
		g.usesKTML = true
		return "ktml." + value.Name, nil
	case compiler.ContentTypeKind:
		g.usesKTML = true
		if g.options.API == APIDirect {
			return "ktml.ComponentFunc", nil
		}
		return "ktml.Component", nil
	case compiler.CollectionTypeKind:
		element, err := g.goType(*value.Element)
		if err != nil {
			return "", err
		}
		return "[]" + element, nil
	}
	return "", fmt.Errorf("unsupported type %q", value.String())
}

func (g *generator) component(model component) error {
	g.current, g.identity = model.name, ""
	g.currentID = strconv.Quote(g.componentID(model.name))
	componentName := exported(model.name)
	typeName := g.propsType(componentName)
	generatedFields := make(map[string]string, len(model.inputs))
	fieldTypes := make(map[string]input, len(model.inputs))
	g.emitf("type %s%s struct {\n", g.mark(model.source), typeName)
	for _, field := range model.inputs {
		fieldTypes[field.name] = field
		fieldName := exported(field.name)
		if !exportedGoIdentifier(fieldName) {
			return fmt.Errorf("component %s input %s does not normalize to an exported Go field", model.name, field.name)
		}
		if first, exists := generatedFields[fieldName]; exists {
			return fmt.Errorf("component %s inputs %s and %s normalize to the same Go field %s", model.name, first, field.name, fieldName)
		}
		if g.options.API == APIStruct && fieldName == "Render" {
			return fmt.Errorf("component %s input %s normalizes to the Go field Render, which is the component's Render method", model.name, field.name)
		}
		generatedFields[fieldName] = field.name
		publicType, err := g.goType(field.typ)
		if err != nil {
			return fmt.Errorf("component %s input %s: %w", model.name, field.name, err)
		}
		if (field.mode == ast.OptionalInput || field.mode == ast.DefaultedInput) && !field.isContent() {
			publicType = "*" + publicType
		}
		g.emitf("\t%s%s %s\n", g.mark(field.source), fieldName, publicType)
	}
	g.emitln("}")
	renderName := "ktmlRender" + componentName
	g.usesRuntime = true
	switch {
	case g.options.API == APIStruct && g.development():
		// The ktmldev public Render first offers the component to the
		// delegation hook (ADR-0004), which renders it through the current
		// interpreter program once one is installed.
		g.usesDev = true
		g.emitf("func (component %s) %sRender(ctx context.Context, w io.Writer) error {\n\treturn dev.Render(ctx, w, component, component.ktmlGenerated)\n}\n", typeName, g.mark(model.source))
		g.emitf("func (component %s) ktmlGenerated(ctx context.Context, w io.Writer) error {\n\tktmlW, ktmlOwned := rt.Wrap(w)\n\treturn rt.Finish(ktmlW, ktmlOwned, %s(ctx, ktmlW, &component))\n}\n", typeName, renderName)
	case g.options.API == APIStruct:
		g.emitf("func (component %s) %sRender(ctx context.Context, w io.Writer) error {\n\tktmlW, ktmlOwned := rt.Wrap(w)\n\treturn rt.Finish(ktmlW, ktmlOwned, %s(ctx, ktmlW, &component))\n}\n", typeName, g.mark(model.source), renderName)
	case g.options.API == APIDirect:
		g.emitf("func Render%s(ctx context.Context, w io.Writer, props %s) error {\n\tktmlW, ktmlOwned := rt.Wrap(w)\n\treturn rt.Finish(ktmlW, ktmlOwned, %s(ctx, ktmlW, &props))\n}\n", componentName, typeName, renderName)
	default:
		return fmt.Errorf("unknown API %s", g.options.API)
	}
	g.emitf("func %s%s(ctx context.Context, w *rt.Writer, props *%s) error {\n", g.mark(model.source), renderName, typeName)
	g.emitf("\tif err := ctx.Err(); err != nil { return rt.Canceled(%s, \"\", err) }\n", g.currentID)
	if g.development() {
		// The instance's Render Identity: the caller's call or outlet frame,
		// or the registry root.
		g.usesDev = true
		g.emitln("\tktmlInstance := dev.Scope(ctx)\n\t_ = ktmlInstance")
	}
	// Required content is checked before the component writes (ADR-0003).
	for _, field := range model.inputs {
		if field.isContent() && field.mode == ast.RequiredInput {
			g.emitf("\tif props.%s == nil { return rt.MissingContent(%s, %s) }\n", exported(field.name), g.currentID, strconv.Quote(field.name))
		}
	}
	for _, field := range model.inputs {
		if field.mode != ast.DefaultedInput || field.defaultValue == nil {
			continue
		}
		expression, err := scalarExpression(field.defaultValue, field.typ)
		if err != nil {
			return fmt.Errorf("component %s input %s: %w", model.name, field.name, err)
		}
		name := exported(field.name)
		g.emitf("\tinput%s := %s%s\n", name, g.mark(field.source), expression)
		g.emitf("\tif %sprops.%s != nil { input%s = %s*props.%s }\n", g.mark(field.source), name, name, g.mark(field.source), name)
		g.emitf("\t_ = input%s\n", name)
	}
	if model.group != nil {
		if err := g.dispatch(model, fieldTypes); err != nil {
			return fmt.Errorf("component %s: %w", model.name, err)
		}
	} else if err := g.componentBody(model.body, fieldTypes); err != nil {
		return fmt.Errorf("component %s: %w", model.name, err)
	}
	g.emitln("\treturn nil")
	g.emitln("}")
	return nil
}

// propsType is the Go type holding a component's inputs: the component type
// itself in Hybrid H, and XProps otherwise.
func (g *generator) propsType(componentName string) string {
	if g.options.API == APIStruct {
		return componentName
	}
	return componentName + "Props"
}

func (g *generator) node(node ast.Node, fieldTypes map[string]input) error {
	switch value := node.(type) {
	case *ast.TextNode:
		return g.interpolated(value.Quoted, value.Value, fieldTypes, false)
	case *ast.RepetitionNode:
		return g.repetition(value, fieldTypes)
	case *ast.ConditionNode:
		return g.condition(value, fieldTypes)
	case *ast.SlotNode:
		return fmt.Errorf("slot %s must be a direct body node of a component call", value.Name)
	case *ast.DoctypeNode:
		if value.Name != "html" {
			return fmt.Errorf("doctype %s is not supported", value.Name)
		}
		g.writeLiteral("<!doctype html>")
		return nil
	case *ast.RawTextNode:
		return fmt.Errorf("a raw-text literal is only the content of a script or style element")
	case *ast.HTMLCommentNode:
		// The compiler validated the text as HTML comment text.
		g.writeLiteral("<!--" + value.Text + "-->")
		return nil
	case *ast.ElementNode:
		if info, outlet := fieldTypes[value.Name]; outlet && info.isContent() {
			return g.outlet(value, info)
		}
		if _, isComponent := g.components[value.Name]; isComponent {
			return g.componentCall(value, fieldTypes)
		}
		return g.element(value, fieldTypes)
	default:
		return fmt.Errorf("unsupported node %T", node)
	}
}

// pathExpression lowers a typed dotted path to a Go selector expression.
// Optional parts are readable only where a presence condition narrowed them,
// and are dereferenced there.
func (g *generator) pathExpression(path *ast.PathExpression, fieldTypes map[string]input) (string, compiler.Type, error) {
	return g.pathValue(path, fieldTypes, false)
}

// pathValue lowers a path. With presence set, the last part is not read: the
// result is its pointer (or content value) for a nil test.
func (g *generator) pathValue(path *ast.PathExpression, fieldTypes map[string]input, presence bool) (string, compiler.Type, error) {
	root := path.Parts[0].Name
	info, exists := fieldTypes[root]
	if !exists {
		return "", compiler.Type{}, fmt.Errorf("references unknown input %s", root)
	}
	last := len(path.Parts) == 1
	var expression string
	switch {
	case info.isContent():
		if !presence || !last {
			return "", compiler.Type{}, fmt.Errorf("content input %s renders only as an outlet", root)
		}
		expression = "props." + exported(root)
	case info.mode == ast.OptionalInput && presence && last:
		expression = optionalBase(root, info)
	case info.mode == ast.OptionalInput && g.isNarrowed(root):
		expression = "(*" + optionalBase(root, info) + ")"
	default:
		var err error
		expression, err = inputExpression(root, info)
		if err != nil {
			return "", compiler.Type{}, err
		}
	}
	current := info.typ
	for index, part := range path.Parts[1:] {
		if current.Kind != compiler.RecordTypeKind || g.snapshot == nil {
			return "", compiler.Type{}, fmt.Errorf("path %s: %s is %s and has no field %s", path.String(), expression, current, part.Name)
		}
		record, _ := g.snapshot.Record(current.Record)
		field, ok := record.Field(part.Name)
		if !ok {
			return "", compiler.Type{}, fmt.Errorf("path %s: record %s has no field %s", path.String(), record.Name, part.Name)
		}
		expression += "." + field.GoName
		if field.Optional && !(presence && index == len(path.Parts)-2) {
			if !g.isNarrowed(spellingOf(path, index+2)) {
				return "", compiler.Type{}, fmt.Errorf("path %s: optional field %s must be narrowed before value access", path.String(), field.Name)
			}
			expression = "(*" + expression + ")"
		}
		current = field.Type
	}
	return g.markIn(path.P, path.E) + expression, current, nil
}

// optionalBase is the pointer expression of an optional input or binding.
func optionalBase(name string, info input) string {
	if info.expr != "" {
		return info.expr
	}
	return "props." + exported(name)
}

func spellingOf(path *ast.PathExpression, count int) string {
	names := make([]string, count)
	for i := range names {
		names[i] = path.Parts[i].Name
	}
	return strings.Join(names, ".")
}

func (g *generator) isNarrowed(spelling string) bool {
	for _, narrowed := range g.narrowed {
		if narrowed == spelling {
			return true
		}
	}
	return false
}

// withNarrowed runs generate with additional narrowed paths.
func (g *generator) withNarrowed(paths []string, generate func() error) error {
	saved := len(g.narrowed)
	g.narrowed = append(g.narrowed, paths...)
	err := generate()
	g.narrowed = g.narrowed[:saved]
	return err
}

func (g *generator) componentCall(call *ast.ElementNode, callerFields map[string]input) error {
	target := g.components[call.Name]
	targetFields := make(map[string]input, len(target.inputs))
	for _, field := range target.inputs {
		targetFields[field.name] = field
	}
	supplied := make(map[string]bool, len(call.Attributes))
	var assignments []string
	for _, attribute := range call.Attributes {
		if !attribute.IsNamed() {
			return fmt.Errorf("component %s inputs cannot be conditional or spread", call.Name)
		}
		targetField, ok := targetFields[attribute.Name]
		if !ok {
			return fmt.Errorf("component %s has no input %s", call.Name, attribute.Name)
		}
		if targetField.isContent() {
			return fmt.Errorf("component %s content input %s cannot be supplied as an attribute", call.Name, attribute.Name)
		}
		if supplied[attribute.Name] {
			return fmt.Errorf("component %s input %s is supplied more than once", call.Name, attribute.Name)
		}
		supplied[attribute.Name] = true
		value := quotedLiteral(attribute.Quoted, attribute.Value)
		var expression string
		literal := false
		if attribute.BagRange != nil {
			if !isContextual(targetField.typ, compiler.AttributesType) {
				return fmt.Errorf("component %s input %s requires %s, not an attribute bag", call.Name, attribute.Name, targetField.typ)
			}
			var err error
			if expression, err = g.bagExpression(attribute.Bag, callerFields); err != nil {
				return fmt.Errorf("component %s input %s: %w", call.Name, attribute.Name, err)
			}
		} else if path, ok := exactPath(attribute.Quoted); ok {
			pathValue, pathType, err := g.pathExpression(path, callerFields)
			if err != nil {
				return fmt.Errorf("component %s input %s: %w", call.Name, attribute.Name, err)
			}
			if !pathType.Equal(targetField.typ) {
				return fmt.Errorf("component %s input %s requires %s, got %s", call.Name, attribute.Name, targetField.typ, pathType)
			}
			expression = pathValue
		} else if targetField.typ.IsScalar(compiler.StringType) && hasPath(attribute.Quoted) {
			var err error
			expression, err = g.goStringExpression(attribute.Quoted, callerFields)
			if err != nil {
				return fmt.Errorf("component %s input %s: %w", call.Name, attribute.Name, err)
			}
		} else {
			if targetField.typ.Kind != compiler.ScalarTypeKind {
				return fmt.Errorf("component %s input %s requires %s", call.Name, attribute.Name, targetField.typ)
			}
			literal = true
			switch targetField.typ.Scalar {
			case compiler.StringType:
				expression = strconv.Quote(value)
			case compiler.BooleanType:
				if value != "true" && value != "false" {
					return fmt.Errorf("component %s input %s requires a boolean", call.Name, attribute.Name)
				}
				expression = value
			case compiler.NumberType:
				number, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return fmt.Errorf("component %s input %s requires a number", call.Name, attribute.Name)
				}
				expression = "float64(" + strconv.FormatFloat(number, 'g', -1, 64) + ")"
			case compiler.IntegerType:
				integer, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return fmt.Errorf("component %s input %s requires an integer", call.Name, attribute.Name)
				}
				expression = "int64(" + strconv.FormatInt(integer, 10) + ")"
			}
		}
		if targetField.mode == ast.OptionalInput || targetField.mode == ast.DefaultedInput {
			if literal {
				// A pointer to a package variable does not allocate; a
				// ktml.Ptr result escapes with the props it is stored in.
				expression = "&" + g.literalVariable(expression)
			} else {
				g.usesKTML = true
				expression = "ktml.Ptr(" + expression + ")"
			}
		}
		assignments = append(assignments, g.markIn(attribute.P, attribute.P+len(attribute.Name))+exported(attribute.Name)+": "+expression)
	}
	contents, err := g.callContent(call, target, callerFields)
	if err != nil {
		return err
	}
	assignments = append(assignments, contents...)
	for _, field := range target.inputs {
		if !supplied[field.name] && field.mode == ast.RequiredInput && !field.isContent() {
			return fmt.Errorf("component %s requires input %s", call.Name, field.name)
		}
	}
	qualifier := ""
	if target.file != "" {
		var err error
		qualifier, err = g.qualifier(filepath.Dir(target.file))
		if err != nil {
			return fmt.Errorf("component %s: %w", call.Name, err)
		}
	}

	site, _ := g.snapshot.CallSource(call)
	contextExpression := g.contextExpression(site)
	name := exported(call.Name)
	callee := g.markIn(call.P, call.P+len(call.Name))
	literal := qualifier + g.propsType(name) + "{" + strings.Join(assignments, ", ") + "}"
	var render string
	switch {
	case qualifier == "":
		// Same-package calls are static calls of the internal renderer; a
		// pointer to a non-escaping literal does not allocate.
		render = fmt.Sprintf("%sktmlRender%s(%s, w, &%s)", callee, name, contextExpression, literal)
	case g.options.API == APIStruct:
		// The literal is parenthesized: an if header cannot start with a
		// composite literal.
		render = fmt.Sprintf("%s(%s).Render(%s, w)", callee, literal, contextExpression)
	default:
		render = fmt.Sprintf("%s%sRender%s(%s, w, %s)", callee, qualifier, name, contextExpression, literal)
	}
	g.emitf("\tif err := %s; err != nil { return err }\n", render)
	return nil
}

// literalVariable returns the package variable holding a literal call input
// of the current component, declaring it on first use. Names are unique per
// Go package because component names are.
func (g *generator) literalVariable(expression string) string {
	if name, ok := g.literalNames[expression]; ok {
		return name
	}
	if g.literalNames == nil {
		g.literalNames = make(map[string]string)
	}
	name := fmt.Sprintf("ktmlLiteral%s%d", exported(g.current), len(g.literals))
	g.literalNames[expression] = name
	g.literals = append(g.literals, name+" = "+expression)
	return name
}

// contextExpression is the context passed to the component call or content
// outlet whose c<n> or o<n> source is site. In the ktmldev variant it carries
// the Render Identity of the instance rendered there: the innermost
// repetition item, or the scope of ctx, extended by the site's frame.
func (g *generator) contextExpression(site string) string {
	if !g.development() {
		return "ctx"
	}
	g.usesDev = true
	scope := g.identity
	if scope == "" {
		scope = "dev.Scope(ctx)"
	}
	return fmt.Sprintf("dev.WithScope(ctx, dev.CallFrame(%s, %s, %s))", scope, strconv.Quote(g.componentID(g.current)), strconv.Quote(site))
}

// development reports whether the ktmldev variant of a per-file renderer is
// being generated.
func (g *generator) development() bool {
	return g.options.Development && g.snapshot != nil
}

func (g *generator) element(element *ast.ElementNode, fieldTypes map[string]input) error {
	g.writeLiteral("<" + element.Name)
	if element == g.instanceRoot {
		g.instanceRoot = nil
		g.emitf("\tif err := dev.InstanceAttr(w, ktmlInstance); err != nil { return rt.WriteError(%s, err) }\n", g.currentID)
	}
	saved := g.owned
	g.owned = ownedNames(element.Attributes)
	err := g.attributes(element.Attributes, fieldTypes)
	g.owned = saved
	if err != nil {
		return err
	}
	g.writeLiteral(">")
	if compiler.VoidElement(element.Name) {
		if len(element.Children) > 0 {
			return fmt.Errorf("void element %s cannot have children", element.Name)
		}
		return nil
	}
	if compiler.RawTextElement(element.Name) {
		if err := g.rawTextBody(element, fieldTypes); err != nil {
			return err
		}
	} else {
		for _, child := range element.Children {
			if err := g.node(child, fieldTypes); err != nil {
				return err
			}
		}
	}
	g.writeLiteral("</" + element.Name + ">")
	return nil
}

// scriptBody writes the quoted text of a script element: static
// JavaScript byte for byte, JSONValue through rt.JSONScript, booleans as
// true or false, and TrustedScript through rt.Script.
func (g *generator) scriptBody(element string, text *ast.TextNode, fieldTypes map[string]input) error {
	if text.Quoted == nil {
		return fmt.Errorf("%s content has no quoted value", element)
	}
	for _, segment := range text.Quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			if end := compiler.RawTextEndTag(element, segment.Literal); end != "" {
				return fmt.Errorf("%s content cannot contain %s", element, end)
			}
			g.writeLiteral(segment.Literal)
			continue
		}
		expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
		if err != nil {
			return fmt.Errorf("%s content: %w", element, err)
		}
		switch {
		case pathType.IsScalar(compiler.BooleanType):
			g.writeValue("rt.ScriptBool", expression)
		case isContextual(pathType, compiler.JSONValueType):
			g.writeValue("rt.JSONScript", expression)
		case isContextual(pathType, compiler.TrustedScriptType):
			g.writeValue("rt.Script", expression)
		default:
			return fmt.Errorf("%s content cannot render %s of type %s; build it in Go as ktml.JSONValue or ktml.TrustedScript", element, segment.Path.String(), pathType)
		}
	}
	return nil
}

// rawTextBody writes the content of a raw-text element. Only script and
// style accept content, and only raw-text literals, written byte for byte.
func (g *generator) rawTextBody(element *ast.ElementNode, fieldTypes map[string]input) error {
	for _, child := range element.Children {
		if text, ok := child.(*ast.TextNode); ok && strings.EqualFold(element.Name, "script") {
			if err := g.scriptBody(element.Name, text, fieldTypes); err != nil {
				return err
			}
			continue
		}
		raw, ok := child.(*ast.RawTextNode)
		if !ok || !compiler.RawTextBodyElement(element.Name) {
			return fmt.Errorf("%s content is raw text, which direct Go generation cannot emit from %T", element.Name, child)
		}
		if end := compiler.RawTextEndTag(element.Name, raw.Text); end != "" {
			return fmt.Errorf("%s content cannot contain %s", element.Name, end)
		}
		g.writeLiteral(raw.Text)
	}
	return nil
}

// attributes writes an element's attribute entries in source order. A
// conditional group lowers to a Go if/else whose branches see the paths
// their condition narrows.
func (g *generator) attributes(entries []*ast.AttributeNode, fieldTypes map[string]input) error {
	for _, attribute := range entries {
		if attribute.Condition != nil {
			if err := g.attributeCondition(attribute.Condition, fieldTypes); err != nil {
				return err
			}
			continue
		}
		if attribute.Spread != nil {
			if err := g.spread(attribute.Spread, fieldTypes); err != nil {
				return err
			}
			continue
		}
		name, value := attribute.Name, quotedLiteral(attribute.Quoted, attribute.Value)
		if hasPath(attribute.Quoted) && compiler.ScriptAttribute(name) {
			if err := g.scriptAttribute(name, attribute.Quoted, fieldTypes); err != nil {
				return err
			}
			continue
		}
		if hasPath(attribute.Quoted) && compiler.StyleAttribute(name) {
			if err := g.styleAttribute(name, attribute.Quoted, fieldTypes); err != nil {
				return err
			}
			continue
		}
		if hasPath(attribute.Quoted) && compiler.URLAttribute(name) {
			if err := g.urlAttribute(name, attribute.Quoted, fieldTypes); err != nil {
				return err
			}
			continue
		}
		if path, ok := exactPath(attribute.Quoted); ok {
			expression, pathType, err := g.pathExpression(path, fieldTypes)
			if err != nil {
				return fmt.Errorf("attribute %s: %w", name, err)
			}
			switch {
			case pathType.IsScalar(compiler.BooleanType) && compiler.ARIAAttribute(name):
				g.emitf("\tif %s {\n", expression)
				g.writeLiteral(" " + name + "=\"true\"")
				g.emitln("\t} else {")
				g.writeLiteral(" " + name + "=\"false\"")
				g.emitln("\t}")
			case pathType.IsScalar(compiler.BooleanType):
				g.emitf("\tif %s {\n", expression)
				g.writeLiteral(" " + name)
				g.emitln("\t}")
			case pathType.IsScalar(compiler.StringType):
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.Attr", expression)
				g.writeLiteral("\"")
			case pathType.IsScalar(compiler.IntegerType):
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.AttrInt", expression)
				g.writeLiteral("\"")
			case pathType.IsScalar(compiler.NumberType):
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.AttrNumber", expression)
				g.writeLiteral("\"")
			case pathType.Kind == compiler.ContextualTypeKind && pathType.Name == compiler.URLType:
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.URL", expression)
				g.writeLiteral("\"")
			case pathType.Kind == compiler.ContextualTypeKind && pathType.Name == compiler.TrustedURLType:
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.TrustedURL", expression)
				g.writeLiteral("\"")
			case pathType.Kind == compiler.ContextualTypeKind && pathType.Name == compiler.JSONValueType:
				g.writeLiteral(" " + name + "=\"")
				g.writeValue("rt.JSONAttr", expression)
				g.writeLiteral("\"")
			default:
				return fmt.Errorf("attribute %s cannot render %s of type %s", name, path.String(), pathType)
			}
			continue
		}
		if hasPath(attribute.Quoted) {
			g.writeLiteral(" " + name + "=\"")
			if err := g.interpolated(attribute.Quoted, attribute.Value, fieldTypes, true); err != nil {
				return fmt.Errorf("attribute %s: %w", name, err)
			}
			g.writeLiteral("\"")
			continue
		}
		// Only a valueless attribute is a static boolean attribute. Literal
		// values, including "true" and "false", are strings.
		if attribute.Value == "" {
			g.writeLiteral(" " + name)
			continue
		}
		g.writeLiteral(" " + name + "=\"" + htmlEscape(value) + "\"")
	}
	return nil
}

func (g *generator) attributeCondition(group *ast.AttributeCondition, fieldTypes map[string]input) error {
	expression, err := g.conditionExpression(group.Condition, fieldTypes)
	if err != nil {
		return err
	}
	whenTrue, whenFalse := compiler.ConditionNarrowing(group.Condition)
	g.emitf("\tif %s {\n", expression)
	if err := g.withNarrowed(whenTrue, func() error { return g.attributes(group.Then, fieldTypes) }); err != nil {
		return err
	}
	if len(group.Else) > 0 {
		g.emitln("\t} else {")
		if err := g.withNarrowed(whenFalse, func() error { return g.attributes(group.Else, fieldTypes) }); err != nil {
			return err
		}
	}
	g.emitln("\t}")
	return nil
}

// scriptAttribute writes a script attribute (compiler.ScriptAttribute) with
// interpolated values. Literal text is attribute-escaped; paths must be
// TrustedScript, JSONValue, or boolean, which renders as the JavaScript
// literal true or false rather than controlling presence.
func (g *generator) scriptAttribute(name string, quoted *ast.QuotedValue, fieldTypes map[string]input) error {
	g.writeLiteral(" " + name + "=\"")
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			g.writeLiteral(htmlEscape(segment.Literal))
			continue
		}
		expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		switch {
		case pathType.IsScalar(compiler.BooleanType):
			g.writeValue("rt.ScriptBool", expression)
		case pathType.Kind == compiler.ContextualTypeKind && pathType.Name == compiler.TrustedScriptType:
			g.writeValue("rt.ScriptAttr", expression)
		case pathType.Kind == compiler.ContextualTypeKind && pathType.Name == compiler.JSONValueType:
			g.writeValue("rt.JSONAttr", expression)
		default:
			return fmt.Errorf("script attribute %s cannot render %s of type %s; build it in Go as ktml.JSONValue or ktml.TrustedScript", name, segment.Path.String(), pathType)
		}
	}
	g.writeLiteral("\"")
	return nil
}

// urlAttribute writes a URL attribute (compiler.URLAttribute) with
// interpolated values: one whole URL, written through the URL policy, or
// TrustedURL; or literal text that fixes the scheme and host
// (compiler.SafeURLPrefix) followed by attribute-escaped string, integer,
// and number paths.
func (g *generator) urlAttribute(name string, quoted *ast.QuotedValue, fieldTypes map[string]input) error {
	if path, ok := exactPath(quoted); ok {
		expression, pathType, err := g.pathExpression(path, fieldTypes)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		var write string
		switch {
		case isContextual(pathType, compiler.URLType):
			write = "rt.URL"
		case isContextual(pathType, compiler.TrustedURLType):
			write = "rt.TrustedURL"
		default:
			return fmt.Errorf("URL attribute %s cannot render %s of type %s; build it in Go as ktml.URL or ktml.TrustedURL", name, path.String(), pathType)
		}
		g.writeLiteral(" " + name + "=\"")
		g.writeValue(write, expression)
		g.writeLiteral("\"")
		return nil
	}
	if !compiler.SafeURLPrefix(quoted) {
		return fmt.Errorf("URL attribute %s: literal text before the first path must fix the scheme and host", name)
	}
	for _, segment := range quoted.Segments {
		if segment.Kind != ast.PathSegment {
			continue
		}
		_, pathType, err := g.pathExpression(segment.Path, fieldTypes)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		if pathType.Kind != compiler.ScalarTypeKind || pathType.IsScalar(compiler.BooleanType) {
			return fmt.Errorf("URL attribute %s cannot render %s of type %s after its literal text", name, segment.Path.String(), pathType)
		}
	}
	g.writeLiteral(" " + name + "=\"")
	if err := g.interpolated(quoted, "", fieldTypes, true); err != nil {
		return fmt.Errorf("attribute %s: %w", name, err)
	}
	g.writeLiteral("\"")
	return nil
}

// styleAttribute writes the style attribute with interpolated values:
// literal text is attribute-escaped, and paths must be CSSValue, written
// through rt.CSS.
func (g *generator) styleAttribute(name string, quoted *ast.QuotedValue, fieldTypes map[string]input) error {
	g.writeLiteral(" " + name + "=\"")
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.LiteralSegment {
			g.writeLiteral(htmlEscape(segment.Literal))
			continue
		}
		expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
		if err != nil {
			return fmt.Errorf("attribute %s: %w", name, err)
		}
		if !isContextual(pathType, compiler.CSSValueType) {
			return fmt.Errorf("style attribute %s cannot render %s of type %s; build it in Go as ktml.CSSValue", name, segment.Path.String(), pathType)
		}
		g.writeValue("rt.CSS", expression)
	}
	g.writeLiteral("\"")
	return nil
}

// interpolated writes a quoted value as text, or as the content of a
// double-quoted attribute value when attribute is set.
func (g *generator) interpolated(quoted *ast.QuotedValue, raw string, fieldTypes map[string]input, attribute bool) error {
	if quoted != nil {
		for _, segment := range quoted.Segments {
			switch segment.Kind {
			case ast.LiteralSegment:
				if segment.Literal != "" {
					g.writeLiteral(htmlEscape(segment.Literal))
				}
			case ast.PathSegment:
				if segment.Path == nil || len(segment.Path.Parts) == 0 {
					return fmt.Errorf("empty interpolation path")
				}
				expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
				if err != nil {
					return fmt.Errorf("text %w", err)
				}
				if err := g.writeRendered(segment.Path.String(), expression, pathType, attribute); err != nil {
					return err
				}
			}
		}
		return nil
	}
	value := unquote(raw)
	for {
		start, end, name, ok := nextReference(value, fieldTypes)
		if !ok {
			if value != "" {
				g.writeLiteral(htmlEscape(value))
			}
			return nil
		}
		if start > 0 {
			g.writeLiteral(htmlEscape(value[:start]))
		}
		info := fieldTypes[name]
		expression, err := inputExpression(name, info)
		if err != nil {
			return err
		}
		if err := g.writeRendered(name, expression, info.typ, attribute); err != nil {
			return err
		}
		value = value[end+1:]
	}
}

// writeRendered writes a scalar value as escaped text or attribute content.
func (g *generator) writeRendered(name, expression string, value compiler.Type, attribute bool) error {
	switch {
	case value.IsScalar(compiler.StringType) && attribute:
		g.writeValue("rt.Attr", expression)
	case value.IsScalar(compiler.StringType):
		g.writeValue("rt.Text", expression)
	case value.IsScalar(compiler.IntegerType) && attribute:
		g.writeValue("rt.AttrInt", expression)
	case value.IsScalar(compiler.IntegerType):
		g.writeValue("rt.TextInt", expression)
	case value.IsScalar(compiler.NumberType) && attribute:
		g.writeValue("rt.AttrNumber", expression)
	case value.IsScalar(compiler.NumberType):
		g.writeValue("rt.TextNumber", expression)
	default:
		return fmt.Errorf("text input %s has unsupported type %s", name, value)
	}
	return nil
}

func (g *generator) writeValue(writer, expression string) {
	g.usesRuntime = true
	g.emitf("\tif err := %s(w, %s); err != nil { return rt.WriteError(%s, err) }\n", writer, expression, g.currentID)
}

func exactPath(quoted *ast.QuotedValue) (*ast.PathExpression, bool) {
	if quoted == nil || len(quoted.Segments) != 1 {
		return nil, false
	}
	path := quoted.Segments[0].Path
	if quoted.Segments[0].Kind != ast.PathSegment || path == nil || len(path.Parts) == 0 {
		return nil, false
	}
	return path, true
}

func hasPath(quoted *ast.QuotedValue) bool {
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

func quotedLiteral(quoted *ast.QuotedValue, raw string) string {
	if quoted == nil {
		return unquote(raw)
	}
	var value strings.Builder
	for _, segment := range quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return ""
		}
		value.WriteString(segment.Literal)
	}
	return value.String()
}

func (g *generator) goStringExpression(quoted *ast.QuotedValue, fieldTypes map[string]input) (string, error) {
	parts := make([]string, 0, len(quoted.Segments))
	for _, segment := range quoted.Segments {
		switch segment.Kind {
		case ast.LiteralSegment:
			if segment.Literal != "" {
				parts = append(parts, strconv.Quote(segment.Literal))
			}
		case ast.PathSegment:
			expression, pathType, err := g.pathExpression(segment.Path, fieldTypes)
			if err != nil {
				return "", err
			}
			// Integers and numbers are formatted as text writes them.
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
				return "", fmt.Errorf("path %s has unsupported type %s in a string value", segment.Path.String(), pathType)
			}
		}
	}
	if len(parts) == 0 {
		return strconv.Quote(""), nil
	}
	return strings.Join(parts, " + "), nil
}

func nextReference(value string, fieldTypes map[string]input) (start, end int, name string, ok bool) {
	start = len(value)
	for field := range fieldTypes {
		pattern := "{" + field + "}"
		if index := strings.Index(value, pattern); index >= 0 && index < start {
			start = index
			end = index + len(pattern) - 1
			name = field
			ok = true
		}
	}
	return start, end, name, ok
}

func inputExpression(name string, info input) (string, error) {
	if info.expr != "" && info.mode != ast.OptionalInput {
		return info.expr, nil
	}
	switch info.mode {
	case ast.RequiredInput:
		return "props." + exported(name), nil
	case ast.DefaultedInput:
		return "input" + exported(name), nil
	case ast.OptionalInput:
		return "", fmt.Errorf("optional input %s must be narrowed before value access", name)
	default:
		return "", fmt.Errorf("input %s has unknown mode %q", name, info.mode)
	}
}

func scalarType(name string) (compiler.Type, bool) {
	switch compiler.ValueType(name) {
	case compiler.StringType, compiler.BooleanType, compiler.NumberType, compiler.IntegerType:
		return compiler.ScalarTypeOf(compiler.ValueType(name)), true
	default:
		return compiler.Type{}, false
	}
}

// syntaxScalar converts a syntax default for documents generated without a
// workspace snapshot.
func syntaxScalar(literal *ast.ScalarLiteral, typ compiler.Type) *compiler.ScalarValue {
	if literal == nil {
		return nil
	}
	value := &compiler.ScalarValue{Kind: compiler.ScalarLiteralKind(literal.Kind), Raw: literal.Raw, String: literal.String, Boolean: literal.Boolean, Number: literal.Number}
	if typ.IsScalar(compiler.IntegerType) && literal.Kind == ast.NumberLiteral {
		if integer, err := strconv.ParseInt(literal.Raw, 10, 64); err == nil {
			value.Kind, value.Integer = compiler.IntegerLiteral, integer
		}
	}
	return value
}

func scalarExpression(literal *compiler.ScalarValue, target compiler.Type) (string, error) {
	if literal == nil {
		return "", fmt.Errorf("missing scalar default")
	}
	switch {
	case target.IsScalar(compiler.StringType) && literal.Kind == compiler.StringLiteral:
		return strconv.Quote(literal.String), nil
	case target.IsScalar(compiler.BooleanType) && literal.Kind == compiler.BooleanLiteral:
		return strconv.FormatBool(literal.Boolean), nil
	case target.IsScalar(compiler.NumberType) && literal.Kind == compiler.NumberLiteral:
		return "float64(" + strconv.FormatFloat(literal.Number, 'g', -1, 64) + ")", nil
	case target.IsScalar(compiler.IntegerType) && literal.Kind == compiler.IntegerLiteral:
		return "int64(" + strconv.FormatInt(literal.Integer, 10) + ")", nil
	default:
		return "", fmt.Errorf("default requires %s", target)
	}
}

// writeLiteral queues escaped static markup; adjacent runs coalesce into one
// write.
func (g *generator) writeLiteral(value string) {
	g.pending.WriteString(value)
}

func unquote(value string) string {
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	return value
}

// htmlEscape escapes static markup with the runtime's own rules.
func htmlEscape(value string) string {
	return rt.EscapeString(value)
}

// GoName returns the exported Go identifier generated for a KTML component
// or input name.
func GoName(name string) string { return exported(name) }

// exported shares the compiler's Go naming rule so that generated identifiers
// match what the workspace validates and what ktml vet expects.
func exported(name string) string {
	return compiler.GeneratedGoField(name)
}

func exportedGoIdentifier(name string) bool {
	for _, current := range name {
		return unicode.IsUpper(current)
	}
	return false
}
