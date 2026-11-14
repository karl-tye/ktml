// Package htmlcheck is the HTML and accessibility analyzer of KTML
// documents. It reports what is statically clear about the HTML a component
// writes and stays silent about anything a component call, a slot, a
// content outlet, an attribute spread, or a runtime value can change.
package htmlcheck

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/karl-tye/ktml/internal/ast"
	"github.com/karl-tye/ktml/internal/compiler"
)

// Severity is the LSP DiagnosticSeverity.
type Severity int

const (
	Error   Severity = 1
	Warning Severity = 2
)

// Diagnostic is one finding at a half-open byte range of the document.
type Diagnostic struct {
	Start, End int
	Severity   Severity
	Code       string
	Message    string
	Fixes      []Fix
}

// Fix is a code action that resolves a diagnostic.
type Fix struct {
	Title string
	Edits []Edit
	// Preferred marks the fix an editor may apply automatically.
	Preferred bool
}

// Edit replaces source[Start:End] with Text.
type Edit struct {
	Start, End int
	Text       string
}

// Options are the project settings of the analyzer.
type Options struct {
	// ExtensionAttributes are the hx-* attributes of the project's own HTMX
	// extensions, ktml.config.json html.extension_attributes. Each is also
	// known with a data- prefix and with htmx 4 :modifier suffixes.
	ExtensionAttributes []string
}

// Analyze checks the HTML of every component body in file.
func Analyze(snapshot *compiler.Snapshot, file string) []Diagnostic {
	return AnalyzeWith(snapshot, file, Options{})
}

// AnalyzeWith checks the HTML of every component body in file with the
// project's options.
func AnalyzeWith(snapshot *compiler.Snapshot, file string, options Options) []Diagnostic {
	document := snapshot.Document(file)
	source := snapshot.Source(file)
	if document == nil || source == nil {
		return nil
	}
	a := &analyzer{snapshot: snapshot, file: file, source: source, labelFor: labelTargets(snapshot), extensions: options.ExtensionAttributes}
	for _, node := range document.Nodes {
		var name string
		var body []ast.Node
		switch declaration := node.(type) {
		case *ast.ElementDecl:
			name, body = declaration.Name, declaration.Body
		case *ast.RenderClause:
			name, body = declaration.Name, declaration.Body
		default:
			continue
		}
		a.component(name, body)
	}
	sort.SliceStable(a.diagnostics, func(i, j int) bool { return a.diagnostics[i].Start < a.diagnostics[j].Start })
	return a.diagnostics
}

type analyzer struct {
	snapshot    *compiler.Snapshot
	file        string
	source      []byte
	diagnostics []Diagnostic
	// labelFor holds every static label for value in the workspace, and
	// dynamicFor whether any label for is dynamic.
	labelFor map[string]bool
	// extensions are the project's declared hx-* extension attributes.
	extensions []string

	// Per component body.
	componentName  string
	firstLabelable *ast.ElementNode
	content        map[string]bool
	ids            []occurrence
	headings       []occurrence
}

// frameKind classifies an ancestor of a node.
type frameKind int

const (
	elementFrame frameKind = iota
	// callFrame is the body of a component call: its content renders
	// wherever the component places it.
	callFrame
	branchFrame
	repetitionFrame
)

type frame struct {
	kind    frameKind
	element *ast.ElementNode
	name    string // lower-case element name
	group   *ast.ConditionNode
	branch  int
}

type occurrence struct {
	node     *ast.ElementNode
	value    string
	level    int
	branches []frame
	repeated bool
}

func (a *analyzer) report(start, end int, severity Severity, code, message string, fixes ...Fix) {
	if end <= start {
		end = start + 1
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Start: start, End: end, Severity: severity, Code: code, Message: message, Fixes: fixes})
}

func (a *analyzer) component(name string, body []ast.Node) {
	a.content = map[string]bool{compiler.ChildrenInput: true}
	if component, ok := a.snapshot.Resolve(a.file, name); ok {
		for _, input := range component.Inputs {
			if input.IsContent() {
				a.content[input.Name] = true
			}
		}
	}
	a.ids, a.headings = nil, nil
	a.componentName, a.firstLabelable = name, firstLabelable(a, body)
	a.walk(body, nil)
	a.reportDuplicateIDs()
	a.reportHeadingOrder()
}

func (a *analyzer) walk(nodes []ast.Node, stack []frame) {
	for _, node := range nodes {
		switch value := node.(type) {
		case *ast.TextNode:
			a.text(value, stack)
		case *ast.ConditionNode:
			a.walk(value.Then, push(stack, frame{kind: branchFrame, group: value, branch: 0}))
			a.walk(value.Else, push(stack, frame{kind: branchFrame, group: value, branch: 1}))
		case *ast.RepetitionNode:
			a.walk(value.Body, push(stack, frame{kind: repetitionFrame}))
		case *ast.SlotNode:
			a.walk(value.Body, push(stack, frame{kind: callFrame}))
		case *ast.ElementNode:
			a.element(value, stack)
		}
	}
}

func push(stack []frame, f frame) []frame {
	return append(append([]frame(nil), stack...), f)
}

// parent returns the nearest HTML element ancestor through conditions and
// repetitions, or false when the parent is unknown: the component root, or
// content passed to a component call.
func parent(stack []frame) (frame, bool) {
	for index := len(stack) - 1; index >= 0; index-- {
		switch stack[index].kind {
		case elementFrame:
			if stack[index].name == "template" {
				// Alpine stamps x-for and x-if template content into the
				// template's parent, so the template is transparent.
				continue
			}
			return stack[index], true
		case callFrame:
			return frame{}, false
		}
	}
	return frame{}, false
}

// ancestors returns the HTML element ancestors inside the component body,
// nearest first, up to the first component call.
func ancestors(stack []frame) []frame {
	var list []frame
	for index := len(stack) - 1; index >= 0; index-- {
		switch stack[index].kind {
		case elementFrame:
			list = append(list, stack[index])
		case callFrame:
			return list
		}
	}
	return list
}

func inCall(stack []frame) bool {
	for _, f := range stack {
		if f.kind == callFrame {
			return true
		}
	}
	return false
}

func (a *analyzer) text(text *ast.TextNode, stack []frame) {
	if text.Quoted == nil {
		return
	}
	blank := true
	for _, segment := range text.Quoted.Segments {
		if segment.Kind == ast.PathSegment || strings.TrimSpace(segment.Literal) != "" {
			blank = false
		}
	}
	if blank {
		return
	}
	if p, ok := parent(stack); ok && tableFamily[p.name] {
		a.report(text.P, text.E, Warning, "html-invalid-nesting", fmt.Sprintf("text cannot be a child of %s; the browser moves it out of the table", p.name))
	}
}

var tableFamily = map[string]bool{"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true}

func (a *analyzer) element(element *ast.ElementNode, stack []frame) {
	if _, ok := a.snapshot.Resolve(a.file, element.Name); ok {
		a.walk(element.Children, push(stack, frame{kind: callFrame}))
		return
	}
	if a.content[element.Name] && len(element.Attributes) == 0 && len(element.Children) == 0 {
		return
	}
	nameEnd := element.P + len(element.Name)
	name := strings.ToLower(element.Name)
	info, known := LookupElement(name)
	if !known {
		if !strings.Contains(name, "-") && !startsUpper(element.Name) {
			message := fmt.Sprintf("%s is not an HTML element", element.Name)
			var fixes []Fix
			if suggestion := closest(name, elementNames()); suggestion != "" {
				message += fmt.Sprintf("; did you mean %s?", suggestion)
				fixes = append(fixes, Fix{Title: "Change to " + suggestion, Edits: []Edit{{element.P, nameEnd, suggestion}}, Preferred: true})
			}
			a.report(element.P, nameEnd, Warning, "html-unknown-element", message, fixes...)
		}
		// Custom elements and unknown names have no content model to check.
		a.walk(element.Children, push(stack, frame{kind: elementFrame, element: element, name: name}))
		return
	}
	if info.flags&foreign != 0 {
		// SVG and MathML have their own vocabularies.
		return
	}
	if info.Obsolete() {
		a.report(element.P, nameEnd, Warning, "html-obsolete-element", fmt.Sprintf("%s is obsolete in HTML. %s", name, info.Description))
	}
	attributes := collectAttributes(element)
	a.checkAttributes(element, name, attributes)
	a.checkNesting(element, name, stack)
	a.checkAccessibility(element, name, attributes, stack)
	if level := headingLevel(name); level > 0 && !inCall(stack) {
		a.headings = append(a.headings, occurrence{node: element, level: level, branches: branchesOf(stack)})
	}
	if id, ok := attributes.static("id"); ok && id != "" && !inCall(stack) {
		a.ids = append(a.ids, occurrence{node: element, value: id, branches: branchesOf(stack), repeated: repeated(stack)})
	}
	a.walk(element.Children, push(stack, frame{kind: elementFrame, element: element, name: name}))
}

func startsUpper(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

func elementNames() []string {
	var names []string
	for _, element := range elementList {
		if !element.Obsolete() {
			names = append(names, element.Name)
		}
	}
	return names
}

func headingLevel(name string) int {
	if len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6' {
		return int(name[1] - '0')
	}
	return 0
}

func branchesOf(stack []frame) []frame {
	var branches []frame
	for _, f := range stack {
		if f.kind == branchFrame {
			branches = append(branches, f)
		}
	}
	return branches
}

func repeated(stack []frame) bool {
	for _, f := range stack {
		if f.kind == repetitionFrame {
			return true
		}
	}
	return false
}

// exclusive reports whether two nodes lie in different branches of one if.
func exclusive(a, b []frame) bool {
	for _, left := range a {
		for _, right := range b {
			if left.group == right.group && left.branch != right.branch {
				return true
			}
		}
	}
	return false
}

// attributeSet is the named attributes of an element, conditional
// branches included, and whether a spread can add more.
type attributeSet struct {
	entries []*ast.AttributeNode
	spread  bool
}

func collectAttributes(element *ast.ElementNode) attributeSet {
	set := attributeSet{entries: ast.NamedAttributes(element.Attributes)}
	var walk func([]*ast.AttributeNode)
	walk = func(entries []*ast.AttributeNode) {
		for _, entry := range entries {
			if entry.Spread != nil {
				set.spread = true
			}
			if entry.Condition != nil {
				walk(entry.Condition.Then)
				walk(entry.Condition.Else)
			}
		}
	}
	walk(element.Attributes)
	return set
}

func (s attributeSet) get(name string) (*ast.AttributeNode, bool) {
	for _, entry := range s.entries {
		if strings.EqualFold(entry.Name, name) {
			return entry, true
		}
	}
	return nil, false
}

func (s attributeSet) has(names ...string) bool {
	for _, name := range names {
		if _, ok := s.get(name); ok {
			return true
		}
	}
	return false
}

// static returns the literal value of an attribute; false when it is
// missing or interpolated.
func (s attributeSet) static(name string) (string, bool) {
	entry, ok := s.get(name)
	if !ok {
		return "", false
	}
	return literalValue(entry)
}

// bound reports whether a framework binds the attribute at runtime, as
// :alt, x-bind:alt, or a spread.
func (s attributeSet) bound(name string) bool {
	return s.spread || s.has(":"+name, "x-bind:"+name)
}

func literalValue(entry *ast.AttributeNode) (string, bool) {
	if entry.Quoted == nil {
		return entry.Value, entry.BagRange == nil
	}
	var value strings.Builder
	for _, segment := range entry.Quoted.Segments {
		if segment.Kind == ast.PathSegment {
			return "", false
		}
		value.WriteString(segment.Literal)
	}
	return value.String(), true
}

// exactPath returns the path of a value that is exactly one interpolation.
func exactPath(entry *ast.AttributeNode) (*ast.PathExpression, bool) {
	if entry.Quoted == nil || len(entry.Quoted.Segments) != 1 || entry.Quoted.Segments[0].Kind != ast.PathSegment {
		return nil, false
	}
	return entry.Quoted.Segments[0].Path, true
}

func (a *analyzer) nameRange(entry *ast.AttributeNode) (int, int) {
	end := entry.P + len(entry.Name)
	if end > entry.E || end > len(a.source) || !strings.EqualFold(string(a.source[entry.P:end]), entry.Name) {
		return entry.P, entry.E
	}
	return entry.P, end
}

func (a *analyzer) checkAttributes(element *ast.ElementNode, name string, attributes attributeSet) {
	// Duplicates the compiler leaves alone: two unconditional attributes of
	// one name. The browser keeps the first.
	seen := make(map[string]bool)
	for _, entry := range element.Attributes {
		if !entry.IsNamed() || entry.Name == "" {
			continue
		}
		key := strings.ToLower(entry.Name)
		if seen[key] {
			start, end := a.nameRange(entry)
			a.report(start, end, Warning, "html-duplicate-attribute", fmt.Sprintf("attribute %s is written twice; the browser ignores this one", entry.Name),
				Fix{Title: "Remove the duplicate " + entry.Name, Edits: []Edit{a.removal(entry)}})
		}
		seen[key] = true
	}
	for _, entry := range attributes.entries {
		attribute := strings.ToLower(entry.Name)
		if entry.Name == "" {
			continue
		}
		start, end := a.nameRange(entry)
		switch {
		case strings.HasPrefix(attribute, "aria-"):
			a.checkAria(entry, attribute, start, end)
			continue
		case attribute == "role":
			a.checkRole(entry, name, attributes)
			continue
		case strings.HasPrefix(attribute, "hx-") || strings.HasPrefix(attribute, "data-hx-"):
			prefix, stem, suffix, _ := htmxName(attribute)
			a.checkHTMX(entry, prefix, stem, suffix, start, end)
			continue
		case !knownAttribute(name, attribute):
			message := fmt.Sprintf("%s is not an attribute of %s", entry.Name, name)
			var fixes []Fix
			if suggestion := closest(attribute, ElementAttributes(name)); suggestion != "" {
				message += fmt.Sprintf("; did you mean %s?", suggestion)
				fixes = append(fixes, Fix{Title: "Change to " + suggestion, Edits: []Edit{{start, end, suggestion}}, Preferred: true})
			}
			a.report(start, end, Warning, "html-unknown-attribute", message, fixes...)
			continue
		}
		if values, validated := AttributeValues(name, attribute); validated {
			if value, ok := literalValue(entry); ok && entry.Quoted != nil && !containsFold(values, value) {
				a.report(entry.P, entry.E, Warning, "html-invalid-attribute-value", fmt.Sprintf("%q is not a valid %s value of %s; use one of %s", value, attribute, name, quoteList(values)))
			}
		}
		if attribute == "tabindex" {
			if value, ok := literalValue(entry); ok {
				if number, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && number > 0 {
					a.report(entry.P, entry.E, Warning, "a11y-positive-tabindex", fmt.Sprintf("tabindex=%q moves the element ahead of the page's focus order; use 0 to follow the document order or -1 to focus it only from script", value),
						Fix{Title: "Use tabindex=\"0\"", Edits: []Edit{{entry.Quoted.P, entry.Quoted.E, `"0"`}}, Preferred: true})
				}
			}
		}
		if attribute == "draggable" {
			a.checkBooleanPresence(entry)
		}
	}
}

// removal deletes an attribute and the space before it.
func (a *analyzer) removal(entry *ast.AttributeNode) Edit {
	start := entry.P
	for start > 0 && (a.source[start-1] == ' ' || a.source[start-1] == '\t') {
		start--
	}
	return Edit{start, entry.E, ""}
}

func containsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func quoteList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return strings.Join(quoted, ", ")
}

func (a *analyzer) checkAria(entry *ast.AttributeNode, attribute string, start, end int) {
	info, ok := ariaAttributes[attribute]
	if !ok {
		message := fmt.Sprintf("%s is not an ARIA attribute", entry.Name)
		var fixes []Fix
		if suggestion := closest(attribute, AriaAttributes()); suggestion != "" {
			message += fmt.Sprintf("; did you mean %s?", suggestion)
			fixes = append(fixes, Fix{Title: "Change to " + suggestion, Edits: []Edit{{start, end, suggestion}}, Preferred: true})
		}
		a.report(start, end, Warning, "a11y-unknown-aria", message, fixes...)
		return
	}
	if !info.boolean && a.booleanPath(entry) {
		a.report(entry.P, entry.E, Warning, "a11y-invalid-aria-value", fmt.Sprintf("%s is not a true/false state; a boolean path writes the text \"true\" or \"false\"", attribute))
		return
	}
	value, static := literalValue(entry)
	if !static {
		return
	}
	switch {
	case info.values != nil && !containsFold(info.values, value):
		a.report(entry.P, entry.E, Warning, "a11y-invalid-aria-value", fmt.Sprintf("%q is not a valid %s value; use one of %s", value, attribute, quoteList(info.values)))
	case info.integer:
		if _, err := strconv.Atoi(strings.TrimSpace(value)); err != nil {
			a.report(entry.P, entry.E, Warning, "a11y-invalid-aria-value", fmt.Sprintf("%s takes an integer, not %q", attribute, value))
		}
	}
}

// checkBooleanPresence reports an exact boolean path in a non-ARIA attribute
// whose value must be the text true or false: KTML writes a boolean exact
// value as the bare attribute or nothing, and the bare attribute is not
// "true". aria-* attributes render a boolean as true or false (ADR-0001).
func (a *analyzer) checkBooleanPresence(entry *ast.AttributeNode) {
	if !a.booleanPath(entry) {
		return
	}
	path, _ := exactPath(entry)
	spelling := path.String()
	a.report(entry.P, entry.E, Warning, "html-boolean-presence", fmt.Sprintf("%s=\"{%s}\" writes a bare %s when %s is true, which is not the value \"true\", and omits it when false; write (if %s { %s=\"true\" } else { %s=\"false\" })", entry.Name, spelling, entry.Name, spelling, spelling, entry.Name, entry.Name))
}

// booleanPath reports whether an attribute value is exactly one boolean
// path.
func (a *analyzer) booleanPath(entry *ast.AttributeNode) bool {
	path, ok := exactPath(entry)
	if !ok {
		return false
	}
	for _, resolved := range a.snapshot.Paths(a.file) {
		if resolved.Range.Start == path.P && resolved.Range.End == path.E {
			return resolved.Type.IsScalar(compiler.BooleanType)
		}
	}
	return false
}

func (a *analyzer) checkRole(entry *ast.AttributeNode, element string, attributes attributeSet) {
	value, static := literalValue(entry)
	if !static {
		return
	}
	tokens := strings.Fields(strings.ToLower(value))
	for _, token := range tokens {
		switch {
		case abstractRoles[token]:
			a.report(entry.P, entry.E, Warning, "a11y-invalid-role", fmt.Sprintf("%s is an abstract ARIA role and cannot be used on an element", token))
			return
		case !roles[token]:
			message := fmt.Sprintf("%s is not an ARIA role", token)
			if suggestion := closest(token, Roles()); suggestion != "" {
				message += fmt.Sprintf("; did you mean %s?", suggestion)
			}
			a.report(entry.P, entry.E, Warning, "a11y-invalid-role", message)
			return
		}
	}
	if len(tokens) == 0 {
		return
	}
	statics := make(map[string]string)
	for _, other := range attributes.entries {
		if literal, ok := literalValue(other); ok {
			statics[strings.ToLower(other.Name)] = literal
		} else if _, seen := statics[strings.ToLower(other.Name)]; !seen {
			statics[strings.ToLower(other.Name)] = "\x00dynamic"
		}
	}
	if strings.HasPrefix(statics["type"], "\x00") {
		return
	}
	allowed, rule := allowedRoles(element, statics)
	if !rule || tokens[0] == implicitRoles[element] {
		return
	}
	for _, role := range allowed {
		if role == tokens[0] {
			return
		}
	}
	if len(allowed) == 0 {
		a.report(entry.P, entry.E, Warning, "a11y-role-not-allowed", fmt.Sprintf("%s cannot take a role; its native semantics are fixed", element))
		return
	}
	a.report(entry.P, entry.E, Warning, "a11y-role-not-allowed", fmt.Sprintf("role %s is not allowed on %s; allowed roles are %s", tokens[0], element, strings.Join(allowed, ", ")))
}

// requiredParents are the elements that may only be children of certain
// elements. A parent that is a component call, a slot, or the component
// root is unknown and not checked.
var requiredParents = map[string][]string{
	"li": {"ul", "ol", "menu"}, "dt": {"dl", "div"}, "dd": {"dl", "div"},
	"tr": {"table", "thead", "tbody", "tfoot"}, "td": {"tr"}, "th": {"tr"},
	"thead": {"table"}, "tbody": {"table"}, "tfoot": {"table"}, "caption": {"table"},
	"colgroup": {"table"}, "col": {"colgroup", "table"}, "option": {"select", "datalist", "optgroup"},
	"optgroup": {"select"}, "legend": {"fieldset"}, "figcaption": {"figure"}, "summary": {"details"},
	"track": {"audio", "video"}, "rt": {"ruby"}, "rp": {"ruby"}, "source": {"picture", "audio", "video"},
}

// allowedChildren are the elements whose children are restricted; the
// browser moves other children of table elements out of the table.
var allowedChildren = map[string][]string{
	"table": {"caption", "colgroup", "thead", "tbody", "tfoot", "tr", "script", "template", "style"},
	"thead": {"tr", "script", "template"}, "tbody": {"tr", "script", "template"}, "tfoot": {"tr", "script", "template"},
	"tr": {"td", "th", "script", "template"}, "ul": {"li", "script", "template"}, "ol": {"li", "script", "template"},
	"menu": {"li", "script", "template"}, "dl": {"dt", "dd", "div", "script", "template"},
}

// closesParagraph are the start tags that close an open p, so they cannot
// be written inside one.
var closesParagraph = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "details": true, "dialog": true,
	"div": true, "dl": true, "fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "hr": true, "main": true, "menu": true, "nav": true, "ol": true, "p": true, "pre": true,
	"search": true, "section": true, "table": true, "ul": true, "li": true, "dd": true, "dt": true,
	"center": true, "dir": true, "xmp": true,
}

// buttonScope are the elements that stop a p from being closed by a
// descendant's start tag.
var buttonScope = map[string]bool{
	"button": true, "table": true, "td": true, "th": true, "caption": true, "object": true,
	"template": true, "marquee": true, "applet": true, "html": true,
}

func (a *analyzer) checkNesting(element *ast.ElementNode, name string, stack []frame) {
	nameEnd := element.P + len(element.Name)
	if p, ok := parent(stack); ok {
		if parents, restricted := requiredParents[name]; restricted && !contains(parents, p.name) && !p.custom() {
			a.report(element.P, nameEnd, Warning, "html-invalid-nesting", fmt.Sprintf("%s must be a child of %s, not %s", name, orList(parents), p.name))
		} else if children, restricted := allowedChildren[p.name]; restricted && !contains(children, name) {
			consequence := ""
			if tableFamily[p.name] {
				consequence = "; the browser moves it out of the table"
			}
			a.report(element.P, nameEnd, Warning, "html-invalid-nesting", fmt.Sprintf("%s cannot be a child of %s, which contains only %s%s", name, p.name, orList(children), consequence))
		}
	}
	if closesParagraph[name] {
		for _, ancestor := range ancestors(stack) {
			if buttonScope[ancestor.name] {
				break
			}
			if ancestor.name == "p" {
				a.report(element.P, nameEnd, Warning, "html-invalid-nesting", fmt.Sprintf("p cannot contain %s; the browser closes the p before it, so the %s and what follows it leave the paragraph", name, name))
				break
			}
		}
	}
	if name == "form" {
		for _, ancestor := range ancestors(stack) {
			if ancestor.name == "form" {
				a.report(element.P, nameEnd, Warning, "html-invalid-nesting", "form cannot contain a form; the browser ignores the inner form tag")
				break
			}
		}
	}
}

func (f frame) custom() bool { return strings.Contains(f.name, "-") }

func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}

func orList(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}

func (a *analyzer) reportDuplicateIDs() {
	for later := range a.ids {
		for earlier := range later {
			first, second := a.ids[earlier], a.ids[later]
			// A literal id in a repetition repeats per item, but the item
			// count is not known statically.
			if first.value != second.value || first.repeated || second.repeated || exclusive(first.branches, second.branches) {
				continue
			}
			entry, _ := collectAttributes(second.node).get("id")
			a.report(entry.P, entry.E, Warning, "html-duplicate-id", fmt.Sprintf("id %q is already used by the %s at line %d of this component", second.value, first.node.Name, a.line(first.node.P)))
			break
		}
	}
}

func (a *analyzer) reportHeadingOrder() {
	for index, heading := range a.headings {
		for previous := index - 1; previous >= 0; previous-- {
			before := a.headings[previous]
			if exclusive(before.branches, heading.branches) {
				continue
			}
			if heading.level > before.level+1 {
				a.report(heading.node.P, heading.node.P+len(heading.node.Name), Warning, "a11y-heading-order", fmt.Sprintf("heading level jumps from h%d to h%d; screen reader users navigate by heading level, so use h%d", before.level, heading.level, before.level+1))
			}
			break
		}
	}
}

func (a *analyzer) line(offset int) int {
	return strings.Count(string(a.source[:offset]), "\n") + 1
}

// closest returns the candidate within edit distance two of name, if one
// is unambiguous.
func closest(name string, candidates []string) string {
	best, bestDistance, ties := "", 3, 0
	for _, candidate := range candidates {
		distance := editDistance(name, candidate)
		switch {
		case distance < bestDistance:
			best, bestDistance, ties = candidate, distance, 0
		case distance == bestDistance:
			ties++
		}
	}
	if best == "" || ties > 0 || bestDistance == 0 || len(name) < 3 {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
