package interp_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
	"github.com/karl-tye/ktml/internal/generator/gogen"
)

const parityModule = "example.com/parity"

// parityFixture is one workspace rendered through both backends: a directory
// tree, or one corpus Document with the corpus Host Schemas.
type parityFixture struct {
	name    string
	source  string
	single  string
	corpus  bool
	schemas []string
}

func parityFixtures(t *testing.T) []parityFixture {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	gogenData := filepath.Join(repository, "internal", "generator", "gogen", "testdata")
	fixtures := []parityFixture{
		{name: "features", source: filepath.Join("testdata", "parity", "features")},
	}
	for _, name := range []string{"apis", "board", "gaps", "hostschema", "payment"} {
		fixtures = append(fixtures, parityFixture{name: name, source: filepath.Join(gogenData, name)})
	}
	corpus := filepath.Join(repository, "testdata", "conformance", "valid")
	documents, err := filepath.Glob(filepath.Join(corpus, "*.ktml"))
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := filepath.Glob(filepath.Join(corpus, "*.ktmlschema"))
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		name := strings.TrimSuffix(filepath.Base(document), ".ktml")
		fixtures = append(fixtures, parityFixture{name: "corpus/" + name, source: corpus, single: document, corpus: true, schemas: schemas})
	}
	return fixtures
}

// TestParity_GIVENEveryLanguageFeatureFixture_WHENRenderedByBothBackends_THENBytesWritesAndErrorsMatch
// is the PLAN "Interpreter parity" gate. Every fixture is generated with gogen
// (production and ktmldev variants) into one temporary module next to
// generated scenarios and a driver that renders each scenario through the
// generated Go and the interpreter.
func TestParity_GIVENEveryLanguageFeatureFixture_WHENRenderedByBothBackends_THENBytesWritesAndErrorsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module with the go command")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	driver, err := os.ReadFile(filepath.Join("testdata", "parity", "driver.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	module := t.TempDir()
	writeFile(t, filepath.Join(module, "go.mod"), "module "+parityModule+"\n\ngo 1.26.3\n\nrequire github.com/karl-tye/ktml v0.0.0\nreplace github.com/karl-tye/ktml => "+filepath.ToSlash(repository)+"\n")
	var included, skipped []string
	for _, fixture := range parityFixtures(t) {
		root := filepath.Join(module, filepath.FromSlash(fixture.name))
		copyFixture(t, fixture, root)
		if reason := generateParityFixture(t, module, root, string(driver)); reason != "" {
			if !fixture.corpus {
				t.Fatalf("fixture %s: %s", fixture.name, reason)
			}
			skipped = append(skipped, fixture.name+": "+reason)
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			continue
		}
		included = append(included, fixture.name)
	}
	t.Logf("fixtures: %s", strings.Join(included, ", "))
	for _, reason := range skipped {
		t.Logf("corpus fixture not semantically valid on its own, covered elsewhere: %s", reason)
	}
	// Release gate: production builds of generated code link neither the
	// development runtime, the interpreter, nor the compiler.
	list := exec.Command("go", "list", "-deps", "./...")
	list.Dir = module
	deps, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, deps)
	}
	for _, forbidden := range []string{"github.com/karl-tye/ktml/dev\n", "github.com/karl-tye/ktml/dev/interp\n", "github.com/karl-tye/ktml/internal/compiler\n"} {
		if strings.Contains(string(deps), forbidden) {
			t.Errorf("production build links %s", strings.TrimSpace(forbidden))
		}
	}
	summary := regexp.MustCompile(`PARITY package=(\S+) development=(\S+) components=(\d+) scenarios=(\d+) comparisons=(\d+) outcomes=(\S+)`)
	for _, tags := range [][]string{nil, {"-tags", "ktmldev"}} {
		args := append([]string{"test", "-count=1", "-v"}, tags...)
		args = append(args, "./...")
		command := exec.Command("go", args...)
		command.Dir = module
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, failures(output))
		}
		for _, match := range summary.FindAllStringSubmatch(string(output), -1) {
			t.Logf("package %s development=%s: %s components, %s scenarios, %s comparisons; baseline outcomes %s", match[1], match[2], match[3], match[4], match[5], match[6])
		}
	}
}

// failures keeps the failing lines of verbose go test output.
func failures(output []byte) string {
	var kept []string
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "=== RUN") || strings.HasPrefix(strings.TrimSpace(line), "--- PASS") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > 400 {
		kept = append(kept[:400], "…")
	}
	return strings.Join(kept, "\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyFixture(t *testing.T, fixture parityFixture, root string) {
	t.Helper()
	if fixture.single != "" {
		for _, source := range append([]string{fixture.single}, fixture.schemas...) {
			content, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, filepath.Base(source)), string(content))
		}
		return
	}
	err := filepath.WalkDir(fixture.source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name := entry.Name()
		if strings.Contains(filepath.ToSlash(path), "/golden/") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if !strings.HasSuffix(name, ".ktml") && !strings.HasSuffix(name, ".ktmlschema") && !strings.HasSuffix(name, ".go") {
			return nil
		}
		relative, err := filepath.Rel(fixture.source, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".go") {
			// Handwritten Go imports the gogen fixture module's packages.
			content = []byte(strings.ReplaceAll(string(content), `"example.com/fixture/`, `"`+parityModule+"/"+fixture.name+"/"))
		}
		writeFile(t, filepath.Join(root, relative), string(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// packageName is the Go package name the harness gives a fixture directory.
func packageName(dir string) string {
	var name strings.Builder
	for _, current := range strings.ToLower(filepath.Base(dir)) {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' && name.Len() > 0 {
			name.WriteRune(current)
		}
	}
	return name.String()
}

// generateParityFixture writes the generated Go, scenarios, and driver of one
// fixture. It returns why a fixture cannot be rendered, or "".
func generateParityFixture(t *testing.T, module, root, driver string) string {
	t.Helper()
	snapshot, err := compiler.NewWorkspace(root).Index()
	if err != nil {
		t.Fatalf("Index(%s) error = %v", root, err)
	}
	if err := snapshot.Err(); err != nil {
		return err.Error()
	}
	packages := func(dir string) (gogen.GoPackage, error) {
		relative, err := filepath.Rel(module, dir)
		if err != nil {
			return gogen.GoPackage{}, err
		}
		return gogen.GoPackage{ImportPath: parityModule + "/" + filepath.ToSlash(relative), Name: packageName(dir)}, nil
	}
	outputs := make(map[string][]byte)
	var sources []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && (strings.HasSuffix(path, ".ktml") || strings.HasSuffix(path, ".ktmlschema")) {
			sources = append(sources, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(sources)
	for _, source := range sources {
		dir := filepath.Dir(source)
		options := gogen.Options{Package: packageName(dir), Packages: packages}
		base := strings.TrimSuffix(source, filepath.Ext(source))
		if strings.HasSuffix(source, ".ktmlschema") {
			generated, _, err := gogen.GenerateSchema(snapshot, source, options)
			if err != nil {
				return "GenerateSchema: " + err.Error()
			}
			outputs[base+"_ktmlschema.go"] = generated
			continue
		}
		production, err := gogen.GenerateFile(snapshot, source, options)
		if err != nil {
			return "GenerateFile: " + err.Error()
		}
		options.Development = true
		development, err := gogen.GenerateFile(snapshot, source, options)
		if err != nil {
			return "GenerateFile(Development): " + err.Error()
		}
		outputs[base+"_ktml.go"] = production
		outputs[base+"_ktml_dev.go"] = development
	}
	byDir := make(map[string][]compiler.Component)
	for _, component := range snapshot.Components() {
		byDir[filepath.Dir(component.File)] = append(byDir[filepath.Dir(component.File)], component)
	}
	for dir, components := range byDir {
		name := packageName(dir)
		emitter := &valueEmitter{snapshot: snapshot, dir: dir, packages: packages, imports: make(map[string]string)}
		scenarios := emitter.scenarios(components)
		var file strings.Builder
		fmt.Fprintf(&file, "package %s\n\nimport (\n\t\"github.com/karl-tye/ktml\"\n", name)
		paths := make([]string, 0, len(emitter.imports))
		for path := range emitter.imports {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			fmt.Fprintf(&file, "\t%s %q\n", emitter.imports[path], path)
		}
		fmt.Fprintf(&file, ")\n\nvar _ = ktml.Ptr[int]\n\nconst ktmlParityRoot = %q\n\nfunc ktmlParityScenarios() []ktmlParityScenario {\n\treturn []ktmlParityScenario{\n%s\t}\n}\n", root, scenarios)
		outputs[filepath.Join(dir, "ktmlparity_scenarios_test.go")] = []byte(file.String())
		outputs[filepath.Join(dir, "ktmlparity_driver_test.go")] = []byte(strings.ReplaceAll(driver, "PACKAGE", name))
		outputs[filepath.Join(dir, "ktmlparity_prod_test.go")] = []byte("//go:build !ktmldev\n\npackage " + name + "\n\nconst ktmlParityDevelopment = false\n")
		outputs[filepath.Join(dir, "ktmlparity_dev_test.go")] = []byte("//go:build ktmldev\n\npackage " + name + "\n\nconst ktmlParityDevelopment = true\n")
	}
	for path, content := range outputs {
		writeFile(t, path, string(content))
	}
	return ""
}

// valueEmitter writes Go expressions for generated props values. Scalars are
// adversarial for their output contexts and unique per value, so keys are
// unique unless a scenario repeats a collection item on purpose.
type valueEmitter struct {
	snapshot *compiler.Snapshot
	dir      string
	packages func(string) (gogen.GoPackage, error)
	imports  map[string]string
	counter  int
	depth    int
}

// mode selects one scenario's shape.
type mode struct {
	index     int
	present   bool
	empty     bool
	duplicate bool
	// nested repeats the items of collections inside another collection's
	// items only, so outer repetitions write before an inner one fails.
	nested bool
}

func (e *valueEmitter) scenarios(components []compiler.Component) string {
	variants := 3
	for _, component := range components {
		for _, input := range component.Inputs {
			if input.Type.Kind == compiler.StateTypeKind {
				if state, ok := e.snapshot.State(input.Type.State); ok {
					variants = max(variants, len(state.Variants))
				}
			}
		}
	}
	var scenarios strings.Builder
	for _, component := range components {
		typeName := gogen.GoName(component.Name)
		fmt.Fprintf(&scenarios, "\t\t{%q, %q, %s{}},\n", component.Name, "zero", typeName)
		modes := make([]mode, 0, variants+1)
		for index := range variants {
			modes = append(modes, mode{index: index, present: index%2 == 0, empty: index == 2})
		}
		modes = append(modes, mode{present: true, duplicate: true}, mode{present: true, nested: true})
		for _, current := range modes {
			var fields []string
			for _, input := range component.Inputs {
				switch {
				case input.Type.Kind == compiler.ContentTypeKind:
					if input.Mode == compiler.RequiredInput || current.present {
						e.counter++
						fields = append(fields, fmt.Sprintf("%s: ktmlParityContent(%q)", compiler.GeneratedGoField(input.Name), fmt.Sprintf("%s %d", input.Name, e.counter)))
					}
				case input.Mode == compiler.RequiredInput:
					fields = append(fields, compiler.GeneratedGoField(input.Name)+": "+e.value(input.Type, current))
				case current.present:
					fields = append(fields, compiler.GeneratedGoField(input.Name)+": ktml.Ptr("+e.value(input.Type, current)+")")
				}
			}
			name := "mode" + strconv.Itoa(current.index)
			if current.duplicate {
				name = "duplicate"
			} else if current.nested {
				name = "nested-duplicate"
			}
			fmt.Fprintf(&scenarios, "\t\t{%q, %q, %s{%s}},\n", component.Name, name, typeName, strings.Join(fields, ", "))
		}
	}
	return scenarios.String()
}

func (e *valueEmitter) qualifier(file string) string {
	dir := filepath.Dir(file)
	if dir == e.dir {
		return ""
	}
	pkg, err := e.packages(dir)
	if err != nil {
		panic(err)
	}
	if alias, ok := e.imports[pkg.ImportPath]; ok {
		return alias + "."
	}
	alias := fmt.Sprintf("ktmlparity%d", len(e.imports))
	e.imports[pkg.ImportPath] = alias
	return alias + "."
}

func (e *valueEmitter) goType(typ compiler.Type) string {
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		return map[compiler.ValueType]string{compiler.StringType: "string", compiler.BooleanType: "bool", compiler.NumberType: "float64", compiler.IntegerType: "int64"}[typ.Scalar]
	case compiler.RecordTypeKind:
		record, _ := e.snapshot.Record(typ.Record)
		return e.qualifier(record.File) + record.Name
	case compiler.StateTypeKind:
		state, _ := e.snapshot.State(typ.State)
		return e.qualifier(state.File) + state.Name
	case compiler.ContextualTypeKind:
		return "ktml." + typ.Name
	case compiler.CollectionTypeKind:
		return "[]" + e.goType(*typ.Element)
	}
	panic("unsupported type " + typ.String())
}

// value writes one value of typ.
func (e *valueEmitter) value(typ compiler.Type, current mode) string {
	e.counter++
	n := e.counter
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		switch typ.Scalar {
		case compiler.StringType:
			return strconv.Quote(fmt.Sprintf(`<s%d a="b" c='d'>&amp;</s%d>`, n, n))
		case compiler.BooleanType:
			return strconv.FormatBool((n+current.index)%2 == 0)
		case compiler.IntegerType:
			if n%3 == 0 {
				return fmt.Sprintf("int64(-%d)", n)
			}
			return fmt.Sprintf("int64(%d)", n)
		case compiler.NumberType:
			return numberLiteral(n)
		}
	case compiler.ContextualTypeKind:
		switch typ.Name {
		case compiler.URLType:
			urls := []string{"javascript:alert('%d')", "/path/%d?q=<a>&r=\"s\"#'h'", "HTTPS://example.test/%d", "mailto:a%d@example.test", " javascript:x%d", "data:text/html,%d"}
			return "ktml.NewURL(" + strconv.Quote(fmt.Sprintf(urls[n%len(urls)], n)) + ")"
		case compiler.TrustedURLType:
			return "ktml.TrustURL(" + strconv.Quote(fmt.Sprintf("javascript:void(\"%d\")<>&'", n)) + ")"
		case compiler.JSONValueType:
			return "ktml.TrustJSON(" + strconv.Quote(fmt.Sprintf("{\"k\":\"</script><!--%d  &'\\\"\"}", n)) + ")"
		case compiler.TrustedScriptType:
			return "ktml.TrustScript(" + strconv.Quote(fmt.Sprintf("alert(\"%d\") && '<b>' </script> <!-- </SCRIPT", n)) + ")"
		case "TrustedHTML":
			return "ktml.TrustHTML(" + strconv.Quote(fmt.Sprintf("<b>%d</b>", n)) + ")"
		case compiler.AttributesType:
			return fmt.Sprintf("ktml.JoinAttributes(ktml.MustAttr(\"title\", \"<t%d '\\\">\"), ktml.MustBoolAttr(\"required\", %v), ktml.MustScriptAttr(\"@click\", ktml.TrustScript(\"go('%d') && x < 1\")), ktml.MustAttr(\"Type\", \"owned\"))", n, n%2 == 0, n)
		case "CSSValue":
			return "ktml.TrustCSS(" + strconv.Quote(fmt.Sprintf("color: red; content: \"</style>%d\" &amp; 'x'", n)) + ")"
		}
	case compiler.RecordTypeKind:
		record, _ := e.snapshot.Record(typ.Record)
		return e.qualifier(record.File) + record.Name + "{" + e.fields(record.Fields, current) + "}"
	case compiler.StateTypeKind:
		state, _ := e.snapshot.State(typ.State)
		qualifier := e.qualifier(state.File)
		variant := state.Variants[current.index%len(state.Variants)]
		if !variant.Payload {
			return qualifier + "New" + state.Name + variant.Name + "()"
		}
		return qualifier + "New" + state.Name + variant.Name + "(" + qualifier + state.Name + variant.Name + "{" + e.fields(variant.Fields, current) + "})"
	case compiler.CollectionTypeKind:
		if current.empty {
			return e.goType(typ) + "{}"
		}
		repeat := current.duplicate || current.nested && e.depth > 0
		e.depth++
		defer func() { e.depth-- }()
		first := e.value(*typ.Element, current)
		second := first
		if !repeat {
			second = e.value(*typ.Element, current)
		}
		return e.goType(typ) + "{" + first + ", " + second + "}"
	}
	panic("unsupported type " + typ.String())
}

func numberLiteral(n int) string {
	switch n % 4 {
	case 0:
		return fmt.Sprintf("float64(%d.25)", n)
	case 1:
		return fmt.Sprintf("float64(1e21 + %d)", n)
	case 2:
		return fmt.Sprintf("float64(-%de-9)", n)
	default:
		return fmt.Sprintf("float64(0.000001 * %d)", n)
	}
}

func (e *valueEmitter) fields(fields []compiler.Field, current mode) string {
	var values []string
	for _, field := range fields {
		goName := field.GoName
		if goName == "" {
			goName = compiler.GoFieldName(field.Name)
		}
		if field.Optional {
			if current.present {
				values = append(values, goName+": ktml.Ptr("+e.value(field.Type, current)+")")
			}
			continue
		}
		values = append(values, goName+": "+e.value(field.Type, current))
	}
	return strings.Join(values, ", ")
}
