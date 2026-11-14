package templmigrate

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	parser "github.com/a-h/templ/parser/v2"
	"github.com/a-h/templ/parser/v2/visitor"
)

const fixtureDir = "testdata/typed/fixture"

func inventory(t *testing.T, options Options, paths ...string) *Report {
	t.Helper()
	files, err := ExpandPaths(paths)
	if err != nil {
		t.Fatalf("ExpandPaths: %v", err)
	}
	report, err := Inventory(files, options)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	return report
}

func findNode(t *testing.T, report *Report, template, kind, code string) Node {
	t.Helper()
	for _, node := range report.Nodes {
		if node.Template == template && node.Kind == kind && strings.Contains(node.Code, code) {
			return node
		}
	}
	t.Fatalf("no %s node in %s containing %q", kind, template, code)
	return Node{}
}

func TestInventory_GIVENTypedFixture_WHENClassifying_THENEachConstructGetsItsPlanStatus(t *testing.T) {
	report := inventory(t, Options{Root: "testdata/typed"}, fixtureDir)
	if !report.Files[0].Typed || len(report.TypeProblems) > 0 {
		t.Fatalf("fixture was not type-checked: typed=%v problems=%v", report.Files[0].Typed, report.TypeProblems)
	}
	tests := []struct {
		name, template, kind, code string
		status                     Status
		reason, gap                string
	}{
		{"static element", "Static", "Element", "<div>", Converted, "", ""},
		{"doctype", "Static", "DocType", "html", Converted, "", ""},
		{"html comment", "Static", "HTMLComment", "note", Converted, "", ""},
		{"scalar inputs", "Static", "HTMLTemplate", "Static()", Converted, "", ""},
		{"record input", "Paths", "HTMLTemplate", "Paths(", PresentationScaffolded, "host-schema-record-input", ""},
		{"field path text", "Paths", "StringExpression", "page.Title", Converted, "field-path", ""},
		{"formatted text", "Paths", "StringExpression", "fmt.Sprintf", PresentationScaffolded, "formatting", ""},
		{"boolean condition", "Paths", "IfExpression", "open", Converted, "boolean-path", ""},
		{"presence condition", "Paths", "ElseIfExpression", "note != nil", Converted, "presence", ""},
		{"comparison condition", "Paths", "IfExpression", "page.Count > 0", PresentationScaffolded, "comparison", ""},
		{"exact boolean attribute", "Paths", "BoolExpressionAttribute", "open", Converted, "boolean-path", ""},
		{"negated boolean attribute", "Paths", "BoolExpressionAttribute", "!open", PresentationScaffolded, "derived-boolean", ""},
		{"string href keeps URL policy", "Paths", "ExpressionAttribute", "href={ page.Link }", PresentationScaffolded, "url-policy", ""},
		{"SafeURL is a trust boundary", "Paths", "ExpressionAttribute", "templ.SafeURL", ReviewRequired, "trusted-url", ""},
		{"templ.URL", "Paths", "ExpressionAttribute", "templ.URL", PresentationScaffolded, "url-construction", ""},
		{"string hx-get takes the URL policy", "Paths", "ExpressionAttribute", "hx-get={ page.Link }", PresentationScaffolded, "url-policy", ""},
		{"safe prefix hx-post keeps its text", "Paths", "ExpressionAttribute", `hx-post={ "/pages/" + page.Title }`, PresentationScaffolded, "concatenation", ""},
		{"host from a value takes the URL policy", "Paths", "ExpressionAttribute", `hx-put={ "/" + page.Title }`, PresentationScaffolded, "url-policy", ""},
		{"constant class", "Paths", "ExpressionAttribute", "BoxClass", Converted, "inlined-go-constant", ""},
		{"class list of paths", "Paths", "ExpressionAttribute", `"a", title`, Converted, "class-list-interpolation", ""},
		{"computed class list", "Paths", "ExpressionAttribute", "templ.KV", PresentationScaffolded, "computed-classes", ""},
		{"dynamic style", "Paths", "ExpressionAttribute", "style=", PresentationScaffolded, "css-value", ""},
		{"JSON script attribute", "Paths", "ExpressionAttribute", "x-data", PresentationScaffolded, "json-value", ""},
		{"string script attribute", "Paths", "ExpressionAttribute", "@click", PresentationScaffolded, "script-attribute-value", ""},
		{"boolean script attribute", "Paths", "ExpressionAttribute", ":disabled", Converted, "script-boolean", ""},
		{"boolean ordinary attribute renders true/false in templ", "Paths", "ExpressionAttribute", "data-open", PresentationScaffolded, "boolean-attribute-value", ""},
		{"attribute spread of a Go value", "Paths", "SpreadAttributes", "templ.Attributes", PresentationScaffolded, "derived-attributes", ""},
		{"conditional valued attribute", "Paths", "ConditionalAttribute", "open", Converted, "boolean-path", ""},
		{"keyed loop by ID", "Loops", "ForExpression", "page.Items", Converted, "key:ID", ""},
		{"keyed loop by TypeID", "Loops", "ForExpression", "page.Orders", Converted, "key:OrderID", ""},
		{"unkeyed loop", "Loops", "ForExpression", "page.Plain", ReviewRequired, "unkeyed-loop", ""},
		{"scalar items", "Loops", "ForExpression", "page.Tags", ReviewRequired, "scalar-item-key-uniqueness", ""},
		{"loop index", "Loops", "ForExpression", "i, item", PresentationScaffolded, "loop-index", ""},
		{"counted loop", "Loops", "ForExpression", "i := 0", PresentationScaffolded, "counted-loop", ""},
		{"function call", "Loops", "StringExpression", "label(item)", PresentationScaffolded, "function-call", ""},
		{"component input", "Calls", "HTMLTemplate", "Calls(", ReviewRequired, "component-valued-input", ""},
		{"static call", "Calls", "TemplElementExpression", "Static()", Converted, "", ""},
		{"call with nil optional", "Calls", "TemplElementExpression", "Paths(page", Converted, "", ""},
		{"call with children", "Calls", "TemplElementExpression", "Wrapper()", Converted, "", ""},
		{"component input outlet", "Calls", "TemplElementExpression", "content", Converted, "component-input-as-slot", ""},
		{"raw html", "Calls", "TemplElementExpression", "templ.Raw", Blocked, "trusted-html", "trusted-html"},
		{"json script", "Calls", "TemplElementExpression", "templ.JSONScript", PresentationScaffolded, "json-value", ""},
		{"go function returning a component", "Calls", "TemplElementExpression", "helper()", ReviewRequired, "function-returning-component", ""},
		{"local initialization", "Calls", "GoCode", "total", PresentationScaffolded, "local-initialization", ""},
		{"switch", "Calls", "SwitchExpression", "role", PresentationScaffolded, "closed-presentation-state", ""},
		{"switch case", "Calls", "CaseExpression", `case "a"`, PresentationScaffolded, "state-variant", ""},
		{"script with src", "Calls", "ScriptElement", "<script>", Converted, "", ""},
		{"children outlet", "Wrapper", "ChildrenExpression", "children", Converted, "", ""},
		{"go function", "", "TemplateFileGoExpression", "func helper", PresentationScaffolded, "go-func-moves-to-presenter", ""},
		{"attributes input", "Forwarding", "HTMLTemplate", "Forwarding(", Converted, "attributes-input", ""},
		{"spread of an input", "Forwarding", "SpreadAttributes", "attrs", Converted, "attribute-spread", ""},
		{"script call with literal arguments", "Forwarding", "TemplElementExpression", `"chart-canvas"`, Converted, "static-script-call", ""},
		{"script call with data", "ScriptData", "TemplElementExpression", "data)", PresentationScaffolded, "json-value", ""},
		{"once handle", "Forwarding", "TemplElementExpression", "scriptHandle.Once()", ReviewRequired, "once-handle-static-bundle", ""},
		{"static inline script", "Forwarding", "ScriptElement", "<script>", Converted, "raw-text-literal", ""},
		{"star attribute name", "Forwarding", "ConstantAttribute", "hx-target-4*", Converted, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := findNode(t, report, tt.template, tt.kind, tt.code)
			if node.Status != tt.status {
				t.Errorf("status = %s, want %s (reasons %v)", node.Status, tt.status, node.Reasons)
			}
			if tt.reason != "" && !contains(node.Reasons, tt.reason) {
				t.Errorf("reasons = %v, want %q", node.Reasons, tt.reason)
			}
			if node.Gap != tt.gap {
				t.Errorf("gap = %q, want %q", node.Gap, tt.gap)
			}
		})
	}
	var inline, style int
	for _, node := range report.Nodes {
		if contains(node.Reasons, "raw-text-literal") && node.Status == Converted {
			switch node.Kind {
			case "ScriptElement":
				inline++
			case "RawElement":
				style++
			}
		}
	}
	if inline != 2 || style != 2 {
		t.Errorf("raw-text literals converted %d inline scripts and %d styles, want 2 and 2", inline, style)
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// visitorCounts counts nodes per type with templ's own visitor, which walks
// every node the parser produces, independently of the inventory walker.
func visitorCounts(t *testing.T, file string) map[string]int {
	t.Helper()
	parsed, err := parser.Parse(file)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	counts := make(map[string]int)
	v := visitor.New()
	value := reflect.ValueOf(v).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		original := field.Interface()
		originalValue := reflect.ValueOf(original)
		kind := field.Type().In(0).Elem().Name()
		field.Set(reflect.MakeFunc(field.Type(), func(args []reflect.Value) []reflect.Value {
			counts[kind]++
			return originalValue.Call(args)
		}))
	}
	if err := v.VisitTemplateFile(parsed); err != nil {
		t.Fatalf("visit %s: %v", file, err)
	}
	delete(counts, "TemplateFile")
	return counts
}

func assertEveryNodeClassified(t *testing.T, report *Report, files []string) {
	t.Helper()
	if report.Dropped != 0 {
		t.Errorf("dropped %d nodes", report.Dropped)
	}
	got := make(map[string]map[string]int)
	for _, node := range report.Nodes {
		if got[node.File] == nil {
			got[node.File] = make(map[string]int)
		}
		got[node.File][node.Kind]++
		if node.Construct == "" || node.Kind == "" {
			t.Errorf("%s:%d: node without kind or construct: %+v", node.File, node.From.Line, node)
		}
	}
	for i, file := range files {
		want := visitorCounts(t, file)
		path := report.Files[i].Path
		for kind, count := range want {
			if got[path][kind] != count {
				t.Errorf("%s: %s nodes classified %d, templ visitor counts %d", path, kind, got[path][kind], count)
			}
		}
		for kind, count := range got[path] {
			if _, ok := want[kind]; !ok && kind != "ElseIfExpression" && kind != "CaseExpression" {
				t.Errorf("%s: classified %d %s nodes the templ visitor does not visit", path, count, kind)
			}
		}
	}
}

func TestInventory_GIVENFixture_WHENComparedWithTheTemplVisitor_THENEveryNodeIsClassifiedOnce(t *testing.T) {
	for _, noTypes := range []bool{false, true} {
		files, err := ExpandPaths([]string{fixtureDir})
		if err != nil {
			t.Fatal(err)
		}
		report, err := Inventory(files, Options{Root: "testdata/typed", NoTypes: noTypes})
		if err != nil {
			t.Fatal(err)
		}
		assertEveryNodeClassified(t, report, files)
	}
}

// TestInventory_GIVENAHostCheckout_WHENComparedWithTheTemplVisitor_THENEveryNodeIsClassifiedOnce
// runs over a real checkout when KTML_HOST_DIR names one.
func TestInventory_GIVENAHostCheckout_WHENComparedWithTheTemplVisitor_THENEveryNodeIsClassifiedOnce(t *testing.T) {
	dir := os.Getenv("KTML_HOST_DIR")
	if dir == "" {
		t.Skip("KTML_HOST_DIR is not set")
	}
	files, err := ExpandPaths([]string{filepath.Join(dir, "internal") + "/..."})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Inventory(files, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	assertEveryNodeClassified(t, report, files)
	if report.Totals.ParseFailures != 0 || report.Totals.UntypedFiles != 0 || len(report.TypeProblems) != 0 {
		t.Errorf("parse failures %d, untyped files %d, type problems %v", report.Totals.ParseFailures, report.Totals.UntypedFiles, report.TypeProblems)
	}
	for _, node := range report.Nodes {
		if contains(node.Reasons, "unparsed-go-expression") {
			t.Errorf("%s:%d: unparsed Go expression %q", node.File, node.From.Line, node.Code)
		}
	}
}

func TestInventory_GIVENNoTypes_WHENClassifying_THENExpressionsStillParse(t *testing.T) {
	report := inventory(t, Options{Root: "testdata/typed", NoTypes: true}, fixtureDir)
	if report.Files[0].Typed {
		t.Fatal("file reported as typed with NoTypes")
	}
	for _, node := range report.Nodes {
		if contains(node.Reasons, "unparsed-go-expression") {
			t.Errorf("%d: unparsed Go expression %q", node.From.Line, node.Code)
		}
		if node.Typed {
			t.Errorf("%d: node marked typed", node.From.Line)
		}
	}
	if node := findNode(t, report, "Paths", "ExpressionAttribute", "templ.SafeURL"); node.Status != ReviewRequired {
		t.Errorf("syntactic SafeURL status = %s, want review_required", node.Status)
	}
}

func writeTempl(t *testing.T, dir, name, src string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestInventory_GIVENUnparseableTempl_WHENInventoried_THENTheReportFails(t *testing.T) {
	dir := t.TempDir()
	file := writeTempl(t, dir, "bad.templ", "package bad\n\ntempl Bad() {\n\t<div>\n")
	report, err := Inventory([]string{file}, Options{Root: dir, NoTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Files[0].ParseError == "" || report.Totals.ParseFailures != 1 {
		t.Fatalf("parse failure not recorded: %+v", report.Files[0])
	}
	if !report.Failed() {
		t.Error("report with a parse failure did not fail")
	}
}

func TestInventory_GIVENOnlyConvertibleNodes_WHENInventoried_THENTheReportPasses(t *testing.T) {
	dir := t.TempDir()
	file := writeTempl(t, dir, "ok.templ", "package ok\n\ntempl OK(title string) {\n\t<h1 class=\"t\">{ title }</h1>\n}\n")
	report, err := Inventory([]string{file}, Options{Root: dir, NoTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() || report.Totals.Blocked != 0 {
		t.Fatalf("convertible file failed: %+v", report.Totals)
	}
	if report.Totals.Converted != report.Totals.Total {
		t.Errorf("converted %d of %d nodes", report.Totals.Converted, report.Totals.Total)
	}
}

type unknownNode struct{ parser.Text }

func TestWalker_GIVENAnUnknownNodeType_WHENWalked_THENItIsDroppedAndFailsTheReport(t *testing.T) {
	w := &walker{file: &sourceFile{rel: "x.templ"}, scopes: []map[string]string{{}}}
	w.node(&unknownNode{}, "")
	if w.dropped != 1 || len(w.nodes) != 1 || w.nodes[0].Status != Blocked || w.nodes[0].Gap != "unclassified" {
		t.Fatalf("unknown node not recorded as dropped: dropped=%d nodes=%+v", w.dropped, w.nodes)
	}
	report := &Report{Dropped: w.dropped}
	if !report.Failed() {
		t.Error("report with a dropped node did not fail")
	}
}

func TestCategories_GIVENPatterns_WHENAssigning_THENFirstOrderedMatchWinsAndExclusionsApply(t *testing.T) {
	c := &Categories{
		Order:   []string{"leaf", "analytics", "features"},
		Files:   map[string][]string{"leaf": {"internal/ui/data.templ"}, "analytics": {"internal/analytics/*.templ", "internal/ui/*.templ"}},
		Default: "features",
		Exclude: map[string]string{"internal/mobile/**": "hyperview-xml"},
	}
	tests := []struct{ path, category, excluded string }{
		{"internal/ui/data.templ", "leaf", ""},
		{"internal/ui/list.templ", "analytics", ""},
		{"internal/analytics/report.templ", "analytics", ""},
		{"internal/shop/shop.templ", "features", ""},
		{"internal/mobile/ui/header.templ", "features", "hyperview-xml"},
	}
	for _, tt := range tests {
		category, excluded := c.Assign(tt.path)
		if category != tt.category || excluded != tt.excluded {
			t.Errorf("Assign(%s) = %q, %q; want %q, %q", tt.path, category, excluded, tt.category, tt.excluded)
		}
	}
}

func TestInventory_GIVENHyperviewMarkup_WHENInventoried_THENTheFileIsExcludedAndReviewed(t *testing.T) {
	dir := t.TempDir()
	file := writeTempl(t, dir, "screen.templ", "package hv\n\ntempl Screen() {\n\t<doc xmlns=\"https://hyperview.org/hyperview\"><screen></screen></doc>\n}\n")
	report, err := Inventory([]string{file}, Options{Root: dir, NoTypes: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Files[0].Excluded != "hyperview-xml" || report.Totals.ExcludedFiles != 1 || report.Totals.Total != 0 {
		t.Fatalf("hyperview file not excluded: %+v totals %+v", report.Files[0], report.Totals)
	}
	if report.Excluded.ReviewRequired != report.Excluded.Total || report.Excluded.Total == 0 {
		t.Errorf("excluded nodes = %+v, want all review_required", report.Excluded)
	}
}

func TestWriteSummary_GIVENMarkers_WHENWritten_THENOnlyTheGeneratedRegionChanges(t *testing.T) {
	report := inventory(t, Options{Root: "testdata/typed"}, fixtureDir)
	file := filepath.Join(t.TempDir(), "summary.md")
	original := "# Title\n\nHand-written intro.\n\n" + SummaryBegin + "\nstale\n" + SummaryEnd + "\n\nHand-written outro.\n"
	if err := os.WriteFile(file, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSummary(file, report, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "# Title\n\nHand-written intro.\n\n"+SummaryBegin) || !strings.HasSuffix(text, SummaryEnd+"\n\nHand-written outro.\n") {
		t.Errorf("hand-written text not preserved:\n%s", text)
	}
	if strings.Contains(text, "stale") || !strings.Contains(text, "### Top blockers") {
		t.Errorf("generated region not replaced:\n%s", text)
	}
}

func TestInventory_GIVENATemplatePassedAsAnArgument_WHENWavesAreAssigned_THENItsFileIsADependency(t *testing.T) {
	module := copyModule(t, "testdata/inplace")
	source := "package pages\n\nimport \"example.com/inplace/widgets\"\n\ntempl Frame(c templ.Component) {\n\t<div>@c</div>\n}\n\ntempl Wrapped() {\n\t@Frame(widgets.Badge(\"x\", 1))\n}\n"
	if err := os.WriteFile(filepath.Join(module, "pages", "args.templ"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ExpandPaths([]string{filepath.Join(module, "pages", "args.templ"), filepath.Join(module, "widgets", "widgets.templ")})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Inventory(files, Options{Root: module})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range report.Files {
		if file.Path == "pages/args.templ" {
			if !slices.Contains(file.DependsOn, "widgets/widgets.templ") || file.Wave < 2 {
				t.Fatalf("depends on %v, wave %d: widgets.Badge is an argument of the call", file.DependsOn, file.Wave)
			}
			return
		}
	}
	t.Fatal("pages/args.templ not in the report")
}
