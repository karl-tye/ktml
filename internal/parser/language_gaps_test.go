package parser

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/karl-tye/ktml/internal/ast"
)

func parseBody(t *testing.T, body string) (string, []ast.Node) {
	t.Helper()
	source := "Panel [] {\n" + body + "\n}\n"
	parser := New([]byte(source))
	document := parser.ParseDocument()
	if err := parser.Err(); err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	return source, document.Nodes[0].(*ast.ElementDecl).Body
}

func elementShape(nodes []ast.Node) string {
	var parts []string
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.ElementNode:
			names := make([]string, len(value.Attributes))
			for i, attribute := range value.Attributes {
				names[i] = attribute.Name
			}
			shape := value.Name + "(" + strings.Join(names, " ") + ")"
			if len(value.Children) > 0 {
				shape += "{" + elementShape(value.Children) + "}"
			}
			parts = append(parts, shape)
		case *ast.TextNode:
			parts = append(parts, value.Value)
		case *ast.SlotNode:
			parts = append(parts, "slot:"+value.Name+"{"+elementShape(value.Body)+"}")
		case *ast.ConditionNode:
			parts = append(parts, "if:"+value.Condition.String()+"{"+elementShape(value.Then)+"}")
		default:
			parts = append(parts, value.Type())
		}
	}
	return strings.Join(parts, ", ")
}

func TestParseElement_GIVENElementLines_WHENParsed_THENANewlineEndsTheAttributeList(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "childless siblings", body: "\tdiv id=\"a\"\n\tdiv id=\"b\"", want: "div(id), div(id)"},
		{name: "component siblings", body: "\tFeeAmount fee=\"{fee}\"\n\tPaymentNote\n\tdiv", want: "FeeAmount(fee), PaymentNote(), div()"},
		{name: "valueless sibling attributes", body: "\tinput required disabled\n\thr", want: "input(required disabled), hr()"},
		{name: "comment ends the line", body: "\tdiv id=\"a\" // note\n\tspan", want: "div(id), span()"},
		{name: "text on the next line is a sibling", body: "\tp class=\"x\"\n\t\"after\"", want: "p(class), \"after\""},
		{name: "same-line text child", body: "\tp class=\"x\" \"inside\"", want: "p(class){\"inside\"}"},
		{name: "shorthand value stays on its line", body: "\tp class\n\t\"text\"", want: "p(class), \"text\""},
		{name: "continuation group", body: "\tform (\n\t\tid=\"f\"\n\t\t// the action\n\t\taction=\"{a}\"\n\t\tdisabled\n\t\t#main\n\t) {\n\t\th2 \"x\"\n\t}\n\tdiv", want: "form(id action disabled id){h2(){\"x\"}}, div()"},
		{name: "continuation group with text child", body: "\tbutton (\n\t\ttype=\"button\"\n\t) \"Back\"", want: "button(type){\"Back\"}"},
		{name: "empty continuation group", body: "\tdiv ()\n\tspan", want: "div(), span()"},
		{name: "attribute named if", body: "\tdiv if=\"x\"", want: "div(if)"},
		{name: "same-line if starts a sibling", body: "\tdiv class=\"x\" if a {}", want: "div(class), if:a{}"},
		{name: "attribute named for and slot", body: "\tlabel for=\"email\" slot=\"title\"", want: "label(for slot)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, body := parseBody(t, tt.body)
			if got := elementShape(body); got != tt.want {
				t.Errorf("shape = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseElement_GIVENAContinuationGroup_WHENParsed_THENRecordsExactRanges(t *testing.T) {
	source, body := parseBody(t, "\tform (\n\t\tid=\"f\"\n\t\tdisabled\n\t) {}\n\tdiv id=\"a\" \n")
	form := body[0].(*ast.ElementNode)
	if !form.Multiline || form.AttributeGroup == nil || source[form.AttributeGroup.Start:form.AttributeGroup.End] != "(\n\t\tid=\"f\"\n\t\tdisabled\n\t)" {
		t.Errorf("group = %v %+v", form.Multiline, form.AttributeGroup)
	}
	if got := source[form.P:form.E]; got != "form (\n\t\tid=\"f\"\n\t\tdisabled\n\t) {}" {
		t.Errorf("form range = %q", got)
	}
	if got := source[form.Attributes[1].P:form.Attributes[1].E]; got != "disabled" {
		t.Errorf("attribute range = %q", got)
	}
	div := body[1].(*ast.ElementNode)
	if div.Multiline || source[div.P:div.E] != `div id="a"` || source[div.Attributes[0].P:div.Attributes[0].E] != `id="a"` {
		t.Errorf("div range = %q attribute %q", source[div.P:div.E], source[div.Attributes[0].P:div.Attributes[0].E])
	}
}

func TestParseElement_GIVENInvalidElementLines_WHENParsed_THENReportsCodes(t *testing.T) {
	tests := []struct {
		name, body, code, marker string
	}{
		{name: "detached body", body: "\tdiv class=\"x\"\n\t{\n\t\tspan\n\t}", code: "detached-element-body", marker: "{\n\t\tspan"},
		{name: "mixed attribute list", body: "\tdiv class=\"x\" (id=\"y\")", code: "mixed-attribute-list", marker: "(id"},
		{name: "unclosed group", body: "\tdiv (\n\t\tid=\"y\"\n\t{}", code: "syntax", marker: "(\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "Panel [] {\n" + tt.body + "\n}\n"
			parser := New([]byte(source))
			parser.ParseDocument()
			errors := parser.Errors()
			if len(errors) == 0 || errors[0].Code != tt.code {
				t.Fatalf("errors = %#v, want first %s", errors, tt.code)
			}
			if start := strings.Index(source, tt.marker); errors[0].Pos != start {
				t.Errorf("error at %d, want %d", errors[0].Pos, start)
			}
		})
	}
}

func TestParseSlotFill_GIVENACallBody_WHENParsed_THENRecordsFillsAndRanges(t *testing.T) {
	source, body := parseBody(t, "\tModal title=\"Pay\" {\n\t\tp \"Choose\"\n\t\tslot footer {\n\t\t\tbutton \"Pay\"\n\t\t}\n\t\tslot actions {}\n\t\tslot name=\"html\"\n\t}")
	call := body[0].(*ast.ElementNode)
	if got := elementShape(call.Children); got != `p(){"Choose"}, slot:footer{button(){"Pay"}}, slot:actions{}, slot(name)` {
		t.Fatalf("shape = %s", got)
	}
	footer := call.Children[1].(*ast.SlotNode)
	if source[footer.KeywordRange.Start:footer.KeywordRange.End] != "slot" || source[footer.NameRange.Start:footer.NameRange.End] != "footer" {
		t.Errorf("ranges = %+v %+v", footer.KeywordRange, footer.NameRange)
	}
	if got := source[footer.P:footer.E]; got != "slot footer {\n\t\t\tbutton \"Pay\"\n\t\t}" {
		t.Errorf("whole range = %q", got)
	}
	encoded, err := json.Marshal(&ast.Document{Nodes: []ast.Node{&ast.ElementDecl{Name: "Panel", Body: body}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ast.Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	roundTripped := decoded.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ElementNode)
	if got := elementShape(roundTripped.Children); got != elementShape(call.Children) {
		t.Errorf("round trip shape = %s", got)
	}
}

func TestParseSlotFill_GIVENAFillWithoutBody_WHENParsed_THENReportsSlotFillBody(t *testing.T) {
	parser := New([]byte("Panel [] {\n\tModal {\n\t\tslot footer\n\t}\n}\n"))
	parser.ParseDocument()
	if errors := parser.Errors(); len(errors) == 0 || errors[0].Code != "slot-fill-body" {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestParseCondition_GIVENTheClosedGrammar_WHENParsed_THENBuildsTheConditionTree(t *testing.T) {
	tests := []struct {
		condition, want string
	}{
		{condition: "a", want: "path(a)"},
		{condition: "job.urgent", want: "path(job.urgent)"},
		{condition: "note?", want: "presence(note)"},
		{condition: "!a", want: "not(path(a))"},
		{condition: "!user.address?", want: "not(presence(user.address))"},
		{condition: "a && b && c", want: "and(path(a) path(b) path(c))"},
		{condition: "a || b && c", want: "or(path(a) and(path(b) path(c)))"},
		{condition: "(a || b) && !(c?)", want: "and(group(or(path(a) path(b))) not(group(presence(c))))"},
	}
	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			source, body := parseBody(t, "\tif "+tt.condition+" {\n\t\tspan\n\t}")
			node := body[0].(*ast.ConditionNode)
			if got := conditionShape(node.Condition); got != tt.want {
				t.Errorf("tree = %s, want %s", got, tt.want)
			}
			if got := source[node.Condition.P:node.Condition.E]; got != tt.condition {
				t.Errorf("condition range = %q", got)
			}
			if node.Condition.String() != tt.condition {
				t.Errorf("String() = %q", node.Condition.String())
			}
		})
	}
}

func conditionShape(condition *ast.ConditionExpr) string {
	switch condition.Kind {
	case ast.PathCondition, ast.PresenceCondition:
		return string(condition.Kind) + "(" + condition.Path.String() + ")"
	}
	parts := make([]string, len(condition.Operands))
	for i, operand := range condition.Operands {
		parts[i] = conditionShape(operand)
	}
	return string(condition.Kind) + "(" + strings.Join(parts, " ") + ")"
}

func TestParseCondition_GIVENElseBranches_WHENParsed_THENRecordsChainsAndRanges(t *testing.T) {
	source, body := parseBody(t, "\tif a {\n\t\tspan\n\t}\n\telse if b? {\n\t} else {\n\t\tp\n\t}\n\tdiv")
	if len(body) != 2 {
		t.Fatalf("body = %s", elementShape(body))
	}
	node := body[0].(*ast.ConditionNode)
	if source[node.KeywordRange.Start:node.KeywordRange.End] != "if" || node.ElseKeywordRange == nil || source[node.ElseKeywordRange.Start:node.ElseKeywordRange.End] != "else" || !node.ElseIf {
		t.Fatalf("keywords = %+v %+v %v", node.KeywordRange, node.ElseKeywordRange, node.ElseIf)
	}
	nested := node.Else[0].(*ast.ConditionNode)
	if nested.Condition.String() != "b?" || len(nested.Then) != 0 || len(nested.Else) != 1 || nested.ElseIf {
		t.Errorf("nested = %+v", nested)
	}
	if got := source[node.P:node.E]; !strings.HasSuffix(got, "} else {\n\t\tp\n\t}") || !strings.HasPrefix(got, "if a {") {
		t.Errorf("whole range = %q", got)
	}
	encoded, err := json.Marshal(&ast.Document{Nodes: []ast.Node{&ast.ElementDecl{Name: "Panel", Body: body}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ast.Document
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	roundTripped := decoded.Nodes[0].(*ast.ElementDecl).Body[0].(*ast.ConditionNode)
	if conditionShape(roundTripped.Condition) != "path(a)" || roundTripped.Else[0].(*ast.ConditionNode).Condition.String() != "b?" {
		t.Errorf("round trip = %+v", roundTripped)
	}
}

func TestParseCondition_GIVENRejectedForms_WHENParsed_THENReportsPreciseCodes(t *testing.T) {
	tests := []struct {
		condition, code, marker string
	}{
		{condition: "a == b", code: "condition-comparison", marker: "=="},
		{condition: "a != b", code: "condition-comparison", marker: "!="},
		{condition: "count > 1", code: "condition-comparison", marker: ">"},
		{condition: "count <= 1", code: "condition-comparison", marker: "<="},
		{condition: "true", code: "condition-literal", marker: "true"},
		{condition: "!false", code: "condition-literal", marker: "false"},
		{condition: `"x"`, code: "condition-literal", marker: `"x"`},
		{condition: "1", code: "condition-literal", marker: "1"},
		{condition: "user.isAdmin()", code: "condition-call", marker: "("},
		{condition: "items[0]", code: "condition-index", marker: "["},
		{condition: "a = b", code: "condition-assignment", marker: "="},
		{condition: "a & b", code: "condition-operator", marker: "&"},
		{condition: "a | b", code: "condition-operator", marker: "|"},
		{condition: "!!a", code: "condition-double-negation", marker: "!!"},
		{condition: "", code: "condition-expected", marker: "{"},
		{condition: "a &&", code: "condition-expected", marker: "{"},
		{condition: "a ? b : c", code: "condition-unexpected", marker: "?"},
		{condition: "a b", code: "condition-unexpected", marker: "b {"},
		{condition: "(a", code: "condition-unexpected", marker: "{"},
	}
	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			source := "Panel [a: boolean] {\n\tif " + tt.condition + " {\n\t\tspan\n\t}\n}\n"
			parser := New([]byte(source))
			parser.ParseDocument()
			errors := parser.Errors()
			if len(errors) == 0 || errors[0].Code != tt.code {
				t.Fatalf("errors = %#v, want %s", errors, tt.code)
			}
			if start := strings.Index(source[len("Panel [a: boolean] {\n\tif"):], tt.marker) + len("Panel [a: boolean] {\n\tif"); errors[0].Pos != start {
				t.Errorf("error at %d (%q), want %d", errors[0].Pos, source[errors[0].Pos:], start)
			}
		})
	}
}

func TestParseCondition_GIVENElseWithoutIf_WHENParsed_THENReportsElseWithoutIf(t *testing.T) {
	parser := New([]byte("Panel [] {\n\tdiv\n\telse {}\n}\n"))
	parser.ParseDocument()
	if errors := parser.Errors(); len(errors) == 0 || errors[0].Code != "else-without-if" {
		t.Fatalf("errors = %#v", errors)
	}
}

func TestParse_GIVENMalformedLanguageGapSources_WHENParsed_THENTerminatesWithErrors(t *testing.T) {
	sources := []string{
		"Panel [] {\n\tdiv (\n\t\t\"stray\"\n\t\tid=\"x\"\n\t)\n}\n",
		"Panel [] {\n\tdiv (\n\t\t= = =\n\t)\n}\n",
		"Panel [] {\n\telse else else\n}\n",
		"Panel [] {\n\tif a == == b { }\n}\n",
		"Panel [] {\n\tslot\n\tslot x\n\tslot x y\n}\n",
		"Panel [] {\n\tdiv (\n",
		"Panel [] {\n\tif (a || {\n}\n",
		"Panel [] {\n\tif a {} else\n}\n",
	}
	for _, source := range sources {
		parser := New([]byte(source))
		parser.ParseDocument()
		if parser.Err() == nil {
			t.Errorf("%q parsed without errors", source)
		}
	}
}
