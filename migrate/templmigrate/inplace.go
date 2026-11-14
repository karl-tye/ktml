package templmigrate

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// CallMapping is how Go and templ code spell a converted template after the
// conversion: the Hybrid H struct literal of its KTML component, built
// through its presenter. The call-site codemod reads it from the report.
type CallMapping struct {
	// Package and Template name the templ function; Receiver names the
	// receiver's type for a method template.
	Package  string `json:"package"`
	Template string `json:"template"`
	Receiver string `json:"receiver,omitempty"`
	File     string `json:"file"`
	// Component is the Go type of the KTML component.
	Component string `json:"component"`
	Mode      string `json:"mode"` // direct or view
	// ViewField is the component's field holding the view record, and
	// Presenter the function building it. The presenter takes the
	// receiver, then every "presenter" parameter in order; Context puts a
	// context.Context first and Clock a time.Time last.
	ViewField string `json:"viewField,omitempty"`
	Presenter string `json:"presenter,omitempty"`
	Context   bool   `json:"context,omitempty"`
	Clock     bool   `json:"clock,omitempty"`
	// Children is the field of templ's { children... }, when the template
	// renders them.
	Children string      `json:"children,omitempty"`
	Params   []CallParam `json:"params"`
	Variadic bool        `json:"variadic,omitempty"`
	// Bridge is the templ bridge function unconverted templ callers use,
	// when one was emitted.
	Bridge string `json:"bridge,omitempty"`
}

// CallParam is one templ parameter and where its argument goes.
type CallParam struct {
	Name string `json:"name"`
	// Kind is presenter (an argument of the presenter), value (a direct
	// component's scalar field), content, contents (variadic or slice
	// content, joined), or attributes.
	Kind  string `json:"kind"`
	Field string `json:"field,omitempty"`
	// Scalar is a value field's KTML type: string, integer, or boolean.
	Scalar string `json:"scalar,omitempty"`
}

// callMapping describes how a converted template is spelled after the
// conversion.
func (c *conversion) callMapping(sig *tmplSig) CallMapping {
	m := CallMapping{
		Package: sig.fn.Pkg().Path(), Template: sig.name, File: sig.file.rel,
		Component: upperFirst(sig.component), Mode: "direct",
		Variadic: sig.fn.Type().(*types.Signature).Variadic(),
	}
	if sig.receiver != nil {
		m.Receiver = typeBaseName(sig.receiver.t)
	}
	if sig.view {
		m.Mode, m.ViewField, m.Presenter = "view", compiler.GoFieldName(sig.viewInput), sig.presenter
		m.Context, m.Clock = sig.context, sig.clock
	}
	if sig.children {
		m.Children = "Children"
	}
	for _, p := range sig.params {
		cp := CallParam{Name: p.name}
		switch p.kind {
		case "component", "components":
			cp.Kind, cp.Field = "content", compiler.GoFieldName(p.ktml)
			if p.kind == "components" {
				cp.Kind = "contents"
			}
		case "attributes":
			cp.Kind, cp.Field = "attributes", compiler.GoFieldName(p.name)
		default:
			if sig.view {
				cp.Kind = "presenter"
			} else {
				cp.Kind, cp.Field = "value", compiler.GoFieldName(p.name)
				cp.Scalar, _ = ktmlScalar(p.t)
			}
		}
		m.Params = append(m.Params, cp)
	}
	return m
}

// Literal spells the component's struct literal for a call with the given
// argument expressions. qualifier prefixes package-level names ("pkg." or
// ""); receiver is the receiver expression of a method template; ctx and
// now spell the context and clock a presenter takes. join wraps several
// content arguments into one ktml.Component, and attributes converts a
// templ.Attributes argument; an empty function refuses the argument. It
// returns an error for arguments it cannot place safely.
func (m CallMapping) Literal(qualifier, receiver string, args []string, spread bool, ctx, now string, join func([]string) string, attributes func(string) (string, error)) (string, error) {
	var fields []string
	var presenterArgs []string
	if m.Context {
		if ctx == "" {
			return "", fmt.Errorf("the presenter %s takes the request context, and none is in scope", m.Presenter)
		}
		presenterArgs = append(presenterArgs, ctx)
	}
	if m.Receiver != "" {
		presenterArgs = append(presenterArgs, receiver)
	}
	if spread && (len(m.Params) == 0 || m.Params[len(m.Params)-1].Kind != "presenter") {
		return "", fmt.Errorf("a spread argument (...) fills %s, which is not a presenter parameter", m.lastParam())
	}
	clocked := false
	clock := func() {
		if m.Mode == "view" && m.Clock && !clocked {
			presenterArgs = append(presenterArgs, now)
			clocked = true
		}
	}
	for i, p := range m.Params {
		var values []string
		switch {
		case m.Variadic && i == len(m.Params)-1:
			if i < len(args) {
				values = args[i:]
			}
		case i < len(args):
			values = []string{args[i]}
		default:
			return "", fmt.Errorf("argument %s is missing", p.Name)
		}
		switch p.Kind {
		case "presenter":
			if m.Variadic && i == len(m.Params)-1 {
				// A variadic parameter stays last, after the clock.
				clock()
				if spread {
					values[len(values)-1] += "..."
				}
			}
			presenterArgs = append(presenterArgs, values...)
		case "value":
			if len(values) != 1 {
				return "", fmt.Errorf("argument %s is not one value", p.Name)
			}
			value := values[0]
			if p.Scalar == "integer" && !integerLiteral.MatchString(value) {
				value = "int64(" + value + ")"
			}
			fields = append(fields, p.Field+": "+value)
		case "content", "contents":
			var parts []string
			for _, value := range values {
				if value != "nil" {
					parts = append(parts, value)
				}
			}
			switch {
			case len(parts) == 0:
			case p.Kind == "content" || len(parts) == 1 && !spread && m.Variadic:
				if p.Kind == "contents" && !m.Variadic {
					return "", fmt.Errorf("content slice %s cannot be passed as one ktml.Component; join it by hand", p.Name)
				}
				fields = append(fields, p.Field+": "+parts[0])
			default:
				if spread || join == nil {
					return "", fmt.Errorf("content list %s cannot be joined automatically", p.Name)
				}
				fields = append(fields, p.Field+": "+join(parts))
			}
		case "attributes":
			if len(values) != 1 {
				return "", fmt.Errorf("argument %s is not one value", p.Name)
			}
			if values[0] == "nil" && attributes == nil {
				continue
			}
			if attributes == nil {
				return "", fmt.Errorf("the templ.Attributes argument %s needs a ktml.Attributes built with the context-checked constructors", p.Name)
			}
			value, err := attributes(values[0])
			if err != nil {
				return "", err
			}
			fields = append(fields, p.Field+": "+value)
		}
	}
	if m.Mode == "view" {
		clock()
		view := m.ViewField + ": " + qualifier + m.Presenter + "(" + strings.Join(presenterArgs, ", ") + ")"
		fields = append([]string{view}, fields...)
	}
	return qualifier + m.Component + "{" + strings.Join(fields, ", ") + "}", nil
}

func (m CallMapping) lastParam() string {
	if len(m.Params) == 0 {
		return "no parameter"
	}
	return m.Params[len(m.Params)-1].Name
}

// --- in-place outputs ---

// sourceMapPath is where a file's source map goes: next to the KTML for
// --out, and under .ktml/migrate in place, which keeps converter metadata
// out of the source packages.
func (c *conversion) sourceMapPath(fe *fileEmitter) string {
	if c.opts.InPlace {
		return filepath.Join(c.out, ".ktml", "migrate", filepath.FromSlash(strings.TrimSuffix(fe.file.rel, ".templ"))+".ktmlmap.json")
	}
	return fe.base + ".ktmlmap.json"
}

// keep reports whether an in-place output already exists and must be kept.
func (c *conversion) keep(abs, kind string) bool {
	if !c.opts.InPlace || c.opts.Force || kind == "sourcemap" {
		return false
	}
	if _, err := os.Stat(abs); err != nil {
		return false
	}
	c.summary.Kept = append(c.summary.Kept, c.rel(abs))
	return true
}

// metaPath is where converter metadata about an in-place output goes:
// .ktml/migrate/kept/<output>.<area>. The suffix keeps the copies out of
// every tool that reads .ktml, .ktmlschema, or .go files.
func (c *conversion) metaPath(area, abs string) string {
	return filepath.Join(c.out, ".ktml", "migrate", "kept", filepath.FromSlash(c.rel(abs))) + "." + area
}

// recordPristine keeps a copy of an in-place output as the converter
// wrote it, so a later run can tell a converter change from a hand edit.
func (c *conversion) recordPristine(output emittedFile) error {
	if !c.opts.InPlace || output.kind == "sourcemap" || output.kind == "ktmlschema" {
		return nil
	}
	path := c.metaPath("pristine", output.abs)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(c.metaPath("fresh", output.abs))
	return os.WriteFile(path, output.content, 0o644)
}

// checkKept compares the converter's current emission of a kept output
// with the one it was written from. When they differ, the kept output
// misses a converter change: the current emission is written under
// .ktml/migrate/kept/<output>.fresh, and the batch's templ files stay until it is
// merged. An output without a pristine copy predates this check.
func (c *conversion) checkKept(output emittedFile) error {
	pristine, err := os.ReadFile(c.metaPath("pristine", output.abs))
	fresh := c.metaPath("fresh", output.abs)
	if err != nil || bytes.Equal(pristine, output.content) {
		_ = os.Remove(fresh) // merged, or never outdated
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(fresh), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(fresh, output.content, 0o644); err != nil {
		return err
	}
	rel := c.rel(output.abs)
	c.summary.Outdated = append(c.summary.Outdated, rel)
	c.summary.Diagnostics = append(c.summary.Diagnostics, Diagnostic{File: rel, Severity: "review", Code: "kept-output-outdated", Message: fmt.Sprintf("the converter now emits %s differently than when it was kept; merge the change with git merge-file %s %s %s, then move the fresh copy over the pristine one", rel, rel, c.rel(c.metaPath("pristine", output.abs)), c.rel(fresh))})
	return nil
}

// existing returns an in-place output that an earlier run wrote, unless
// --force overwrites it.
func (c *conversion) existing(abs string) ([]byte, bool) {
	if !c.opts.InPlace || c.opts.Force {
		return nil, false
	}
	data, err := os.ReadFile(abs)
	return data, err == nil
}

var (
	schemaImport = regexp.MustCompile(`(?m)^import \{ ([^}]*) \} from ("[^"]*")\n`)
	schemaDecl   = regexp.MustCompile(`(?m)^(?:record|state) ([A-Za-z_][A-Za-z0-9_]*)`)
	schemaSource = regexp.MustCompile(`([^()\s]+\.templ):[0-9]+`)
)

// mergeSchema adds an emitted package schema's imports and declarations
// to the schema an earlier batch wrote, which is kept as it is
// (presenters and hand edits rely on it). A declaration the existing
// schema already has is not repeated. changed is false when nothing is
// new.
func mergeSchema(existing, emitted []byte) ([]byte, bool) {
	text, out := string(existing), string(emitted)
	declared := map[string]bool{}
	for _, m := range schemaDecl.FindAllStringSubmatch(text, -1) {
		declared[m[1]] = true
	}
	header, body, _ := strings.Cut(out, "\n")
	if loc := schemaImport.FindAllStringIndex(out, -1); len(loc) > 0 {
		body = out[loc[len(loc)-1][1]:]
	}
	// A declaration is new when the existing schema neither declares it
	// nor already covers its templ file: records an earlier run of the
	// same batch wrote and that were since edited or removed by hand stay
	// as they are.
	var blocks []string
	for _, block := range strings.Split(strings.Trim(body, "\n"), "\n\n") {
		m := schemaDecl.FindStringSubmatch(block)
		if m == nil || declared[m[1]] {
			continue
		}
		if source := schemaSource.FindStringSubmatch(block); source != nil && strings.Contains(text, source[1]) {
			continue
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return existing, false
	}
	for _, m := range schemaImport.FindAllStringSubmatch(out, -1) {
		names := strings.Split(m[1], ", ")
		var line []int
		for _, have := range schemaImport.FindAllStringSubmatchIndex(text, -1) {
			if text[have[4]:have[5]] == m[2] {
				line = have
				break
			}
		}
		if line == nil {
			at := 0
			if last := schemaImport.FindAllStringIndex(text, -1); len(last) > 0 {
				at = last[len(last)-1][1]
			} else {
				for strings.HasPrefix(text[at:], "//") {
					at += strings.Index(text[at:], "\n") + 1
				}
			}
			text = text[:at] + m[0] + text[at:]
			continue
		}
		set := map[string]bool{}
		for _, name := range strings.Split(text[line[2]:line[3]], ", ") {
			set[name] = true
		}
		for _, name := range names {
			set[name] = true
		}
		text = text[:line[0]] + "import { " + strings.Join(sortedSet(set), ", ") + " } from " + m[2] + "\n" + text[line[1]:]
	}
	text = strings.TrimRight(text, "\n") + "\n\n" + strings.Replace(header, "// Host Schema converted", "// Records converted", 1) + "\n"
	for _, block := range blocks {
		text += "\n" + block + "\n"
	}
	return []byte(text), true
}

// helperFuncs names the function that defines each presenter helper.
var helperFuncs = map[string]string{"classes": "migrateClasses", "jsonscript": "migrateJSONScript", "style": "migrateStyle", "join": "migrateJoin", "attributes": "migrateAttributes"}

// definedHelpers lists the helpers an existing helper file defines.
func definedHelpers(source []byte) map[string]bool {
	defined := map[string]bool{}
	for name, fn := range helperFuncs {
		if regexp.MustCompile(`(?m)^func ` + fn + `\b`).Match(source) {
			defined[name] = true
		}
	}
	return defined
}

// converterOutput reports whether a Go file is written by the converter or
// generated from its KTML, so that loading a package for conversion or the
// codemod can hide it: a package mixing them with its templ files does not
// type-check.
func converterOutput(path string, head []byte) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, "_ktml.go"), strings.HasSuffix(base, "_ktml_dev.go"), strings.HasSuffix(base, "_ktmlschema.go"):
		return bytes.Contains(head, []byte("Code generated by KTML"))
	case strings.HasSuffix(base, "_present.go"), base == "migrate_helpers.go", strings.HasSuffix(base, "_bridge.go"):
		return bytes.Contains(head, []byte("ktml migrate templ"))
	}
	return false
}

var integerLiteral = regexp.MustCompile(`^-?[0-9]+$`)

var packageClause = regexp.MustCompile(`(?m)^package\s+([A-Za-z_][A-Za-z0-9_]*)`)

// shadowsTempl reports whether a converter output belongs to a templ file
// that still exists: its component, presenter, and bridge declarations
// collide with that file's templ Go. Outputs of files already converted
// and removed, the package's Host Schema records, and the helpers do not,
// and the package's remaining templ files may use them.
func shadowsTempl(path string) bool {
	for _, suffix := range []string{"_present.go", "_bridge.go", "_ktml_dev.go", "_ktml.go"} {
		if stem, ok := strings.CutSuffix(path, suffix); ok {
			_, err := os.Stat(stem + ".templ")
			return err == nil
		}
	}
	return false
}

// hideConverterOutputs adds overlays that empty the converter outputs in
// the given directories that shadow a templ file.
func hideConverterOutputs(dirs map[string]bool, overlay map[string][]byte) {
	for dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			head := data[:min(len(data), 512)]
			if !converterOutput(path, head) || !shadowsTempl(path) {
				continue
			}
			if match := packageClause.FindSubmatch(data); match != nil {
				overlay[path] = []byte("package " + string(match[1]) + "\n")
			}
		}
	}
}

// --- bridges ---

// templCallers finds, for every converted template, whether an
// unconverted .templ file under the root calls it (an unexported template
// only from its own package). It matches the name
// textually, so a false positive only emits an unused bridge.
func (c *conversion) templCallers() map[*tmplSig]bool {
	called := map[*tmplSig]bool{}
	batch := map[string]bool{}
	for _, file := range c.run.files {
		batch[file.abs] = true
	}
	_ = filepath.WalkDir(c.run.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != c.run.root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".templ") || batch[path] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		for _, sig := range c.sigs {
			samePackage := filepath.Dir(path) == filepath.Dir(sig.file.abs)
			// An unexported template is callable only from its own package.
			if called[sig] || sig.receiver != nil || !goExported(sig.name) && !samePackage {
				continue
			}
			pattern := sig.fn.Pkg().Name() + "." + sig.name
			if samePackage {
				pattern = sig.name
			}
			// A caller the codemod already renamed calls the bridge.
			if strings.Contains(text, pattern+"(") || strings.Contains(text, pattern+"Templ(") {
				called[sig] = true
			}
		}
		return nil
	})
	return called
}

// bridges writes, per converted file, the templ bridges of its templates
// that unconverted templ files still call. A bridge keeps the templ
// signature under the name <Template>Templ, so the codemod renames templ
// call sites and keeps their bodies: it passes templ's context-carried
// children into the component's Children slot.
func (c *conversion) bridges() []emittedFile {
	if !c.opts.InPlace || c.opts.Bridges == "none" {
		return nil
	}
	called := c.templCallers()
	byFile := map[*sourceFile][]*tmplSig{}
	for _, sig := range c.sigs {
		if c.files[sig.file] == nil || sig.receiver != nil {
			continue
		}
		if c.opts.Bridges == "all" && goExported(sig.name) || called[sig] {
			byFile[sig.file] = append(byFile[sig.file], sig)
		}
	}
	var outputs []emittedFile
	files := make([]*sourceFile, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	for _, file := range files {
		fe := c.files[file]
		var b strings.Builder
		fmt.Fprintf(&b, "// Bridges converted from %s by ktml migrate templ. Unconverted templ\n", file.rel)
		b.WriteString("// files call the KTML components through them until their own Phase 7\n")
		b.WriteString("// wave; delete a bridge when nothing calls it.\n\n")
		fmt.Fprintf(&b, "package %s\n\n", fe.pkgName)
		b.WriteString("import (\n\t\"context\"\n\t\"io\"\n\t\"time\"\n\n\t\"github.com/a-h/templ\"\n\t\"github.com/karl-tye/ktml\"\n)\n")
		usesTime, usesJoin, usesAttributes := false, false, false
		// The packages of the parameter types (filter.Date), besides time.
		paramImports := map[string]string{}
		for _, sig := range byFile[file] {
			m := c.callMapping(sig)
			// The bridge passes each parameter as one expression: a content
			// list joined into one component, a variadic presenter argument
			// spread.
			mapping := m
			mapping.Variadic = m.Variadic && sig.variadicPresenter()
			mapping.Params = append([]CallParam(nil), m.Params...)
			var params, args []string
			for i, p := range sig.params {
				typeText := fe.rw.typeText(p.t)
				types.TypeString(p.t, func(pkg *types.Package) string {
					if pkg != fe.t.pkg && pkg.Path() != "time" {
						paramImports[pkg.Path()] = fe.rw.imports.add(pkg.Path(), pkg.Name())
					}
					return pkg.Name()
				})
				last := m.Variadic && i == len(sig.params)-1
				if last {
					typeText = "..." + strings.TrimPrefix(typeText, "[]")
				}
				params = append(params, p.name+" "+typeText)
				arg := p.name
				switch mapping.Params[i].Kind {
				case "contents":
					mapping.Params[i].Kind = "content"
					arg = "migrateJoin(" + arg + ")"
					usesJoin = true
				}
				args = append(args, arg)
			}
			attributes := func(value string) (string, error) {
				usesAttributes = true
				return "migrateAttributes(" + value + ")", nil
			}
			literal, err := mapping.Literal("", "", args, mapping.Variadic, "ctx", "time.Now()", nil, attributes)
			if err != nil {
				fe.diagnose(c.node(file, "HTMLTemplate", sig.tmpl.Range), sig.tmpl.Range, "partial", "bridge", fmt.Sprintf("no templ bridge for %s: %v", sig.name, err))
				continue
			}
			if m.Clock {
				usesTime = true
			}
			if m.Children != "" {
				literal = strings.TrimSuffix(literal, "}")
				if !strings.HasSuffix(literal, "{") {
					literal += ", "
				}
				literal += "Children: templ.GetChildren(ctx)}"
			}
			fmt.Fprintf(&b, "\n// %sTempl renders the KTML component %s for templ callers.\n", sig.name, m.Component)
			fmt.Fprintf(&b, "func %sTempl(%s) templ.Component {\n", sig.name, strings.Join(params, ", "))
			b.WriteString("\treturn templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {\n")
			fmt.Fprintf(&b, "\t\treturn %s.Render(templ.ClearChildren(ctx), w)\n", literal)
			b.WriteString("\t})\n}\n")
			c.bridged[sig] = sig.name + "Templ"
		}
		text := b.String()
		// The clock argument or a parameter type (time.Time) uses time.
		if withoutTime := strings.Replace(text, "\t\"time\"\n", "", 1); !usesTime && !strings.Contains(withoutTime, "time.") {
			text = withoutTime
		}
		// migrateJoin, in the helpers, is the only use of package ktml.
		text = strings.Replace(text, "\t\"github.com/karl-tye/ktml\"\n", "", 1)
		if len(paramImports) > 0 {
			paths := make([]string, 0, len(paramImports))
			for path, name := range paramImports {
				// A moved, unexported type is not qualified.
				if strings.Contains(text, name+".") {
					paths = append(paths, path)
				}
			}
			sort.Strings(paths)
			var std, other strings.Builder
			for _, path := range paths {
				spec := strconv.Quote(path)
				if name := paramImports[path]; name != pathpkg.Base(path) {
					spec = name + " " + spec
				}
				if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
					other.WriteString("\t" + spec + "\n")
				} else {
					std.WriteString("\t" + spec + "\n")
				}
			}
			text = strings.Replace(text, "\t\"io\"\n", "\t\"io\"\n"+std.String(), 1)
			text = strings.Replace(text, "\t\"github.com/a-h/templ\"\n", "\t\"github.com/a-h/templ\"\n"+other.String(), 1)
		}
		if usesAttributes {
			fe.helpers["attributes"] = true
		}
		if usesJoin {
			fe.helpers["join"] = true
		}
		content := []byte(text)
		if formatted, err := format.Source(content); err == nil {
			content = formatted
		} else {
			c.summary.Validation = append(c.summary.Validation, fmt.Sprintf("%s: bridge does not parse: %v", c.rel(fe.base+"_bridge.go"), err))
		}
		outputs = append(outputs, emittedFile{abs: fe.base + "_bridge.go", kind: "bridge", content: content})
	}
	return outputs
}

// --- removing the templ sources ---

// removeTempl deletes the batch's templ sources when the batch is complete.
func (c *conversion) removeTempl() {
	var refused []string
	if len(c.summary.Validation) > 0 {
		refused = append(refused, fmt.Sprintf("%d validation errors", len(c.summary.Validation)))
	}
	if !c.opts.HandFinished {
		if c.summary.Partial > 0 {
			refused = append(refused, fmt.Sprintf("%d partial nodes (finish them in the outputs, then rerun with --hand-finished)", c.summary.Partial))
		}
		if c.report.Totals.Blocked > 0 {
			refused = append(refused, fmt.Sprintf("%d blocked nodes", c.report.Totals.Blocked))
		}
	}
	for _, rel := range c.summary.Outdated {
		refused = append(refused, rel+" misses a converter change (kept-output-outdated); merge it first")
	}
	for _, file := range c.run.files {
		if !c.convertible(file) {
			refused = append(refused, file.rel+" was not converted")
		}
	}
	if len(refused) == 0 {
		mappings := c.summary.Calls
		sites, err := ScanCallSites(c.run.root, []string{"./..."}, mappings, CodemodOptions{})
		if err != nil {
			refused = append(refused, "scanning call sites: "+err.Error())
		}
		for _, site := range sites {
			if site.Template == "" {
				continue // not a use of a batch template
			}
			if site.Status == SiteRewritten || site.Status == SiteBridged {
				continue
			}
			if site.Status == SiteRewritable {
				refused = append(refused, fmt.Sprintf("%s:%d:%d still calls %s; run ktml migrate codemod --write first", site.File, site.From.Line, site.From.Col, site.Template))
				continue
			}
			if site.Status == SiteManual {
				refused = append(refused, fmt.Sprintf("%s:%d:%d still uses %s (%s); edit it by hand first", site.File, site.From.Line, site.From.Col, site.Template, site.Reason))
			}
		}
	}
	if len(refused) > 0 {
		c.summary.RemoveRefused = refused
		return
	}
	for _, file := range c.run.files {
		for _, path := range []string{file.abs, strings.TrimSuffix(file.abs, ".templ") + "_templ.go"} {
			if err := os.Remove(path); err == nil {
				c.summary.Removed = append(c.summary.Removed, c.rel(path))
			} else if !os.IsNotExist(err) {
				c.summary.RemoveRefused = append(c.summary.RemoveRefused, err.Error())
			}
		}
	}
}

// templateFile reports whether obj is declared in a templ file's generated
// Go: a templ template.
func (c *conversion) templateFile(obj types.Object) bool {
	if obj.Pkg() == nil {
		return false
	}
	for _, t := range c.run.typed {
		// Every typed file of a module shares the loader's file set,
		// which also positions the dependencies.
		if strings.HasSuffix(t.fset.Position(obj.Pos()).Filename, "_templ.go") {
			return true
		}
	}
	return false
}
