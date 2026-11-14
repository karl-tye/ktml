package templmigrate

import (
	"strings"

	parser "github.com/a-h/templ/parser/v2"
)

// KTML output tree. The emitter builds it per component, then prints it
// with the source-map lines of each node.

type kNode interface{ isKNode() }

type kElement struct {
	name     string
	attrs    []kAttr
	children []kNode
	src      *Node
}

// kText is one quoted text node: literal and path segments.
type kText struct {
	segments []segment
	src      []*Node
}

// kRaw is a raw-text literal (``` … ```), the body of script or style.
type kRaw struct{ text string }

type kIf struct {
	branches []kBranch
	elseBody []kNode
	hasElse  bool
	src      *Node
}

type kBranch struct {
	cond string
	body []kNode
}

type kFor struct {
	item, coll, key string
	body            []kNode
	src             *Node
}

// kCall is a component call; children and slot fills form its body.
type kCall struct {
	name     string
	attrs    []kAttr
	children []kNode
	slots    []kSlot
	src      *Node
}

type kSlot struct {
	name string
	body []kNode
	// forwards names the caller's optional content input when the fill
	// only passes it on. templ passes nil for absent content, while a KTML
	// fill is always present, so the call is printed once with the fill
	// and once without, chosen by the input's presence.
	forwards string
}

// kOutlet renders a content input (children or a named slot).
type kOutlet struct {
	name string
	src  *Node
}

type kComment struct {
	text string
	src  *Node
}

type kDoctype struct{ src *Node }

// kNote is a // source comment: review markers and TODOs.
type kNote struct{ text string }

func (*kElement) isKNode() {}
func (*kText) isKNode()    {}
func (*kRaw) isKNode()     {}
func (*kIf) isKNode()      {}
func (*kFor) isKNode()     {}
func (*kCall) isKNode()    {}
func (*kOutlet) isKNode()  {}
func (*kComment) isKNode() {}
func (*kDoctype) isKNode() {}
func (*kNote) isKNode()    {}

type kAttrKind int

const (
	attrBool kAttrKind = iota
	attrValue
	attrGroup
	attrSpread
	attrBag
	// attrNone writes nothing; it carries the source of an attribute that
	// renders nothing.
	attrNone
)

// kAttr is one attribute-list entry.
type kAttr struct {
	kind  kAttrKind
	name  string
	value string // a quoted KTML value, for attrValue
	// attrGroup: if cond { then } else { els }; elseIf continues a chain.
	cond   string
	then   []kAttr
	els    []kAttr
	elseIf *kAttr
	path   string  // attrSpread
	bag    []kAttr // attrBag
	note   string  // a review marker printed above the element
	src    *Node   // the templ attribute
}

func (a kAttr) needsGroup() bool {
	return a.kind == attrGroup || a.kind == attrBag && len(writtenAttrs(a.bag)) > 0
}

// ktmlPrinter writes KTML source with tab indentation, recording the output
// lines of the templ nodes it prints.
type ktmlPrinter struct {
	b     strings.Builder
	line  int
	spans map[*Node][2]int
}

func newKTMLPrinter() *ktmlPrinter {
	return &ktmlPrinter{line: 1, spans: map[*Node][2]int{}}
}

func (p *ktmlPrinter) writeLine(indent int, text string) {
	p.b.WriteString(strings.Repeat("\t", indent))
	p.b.WriteString(text)
	p.b.WriteByte('\n')
	p.line += 1 + strings.Count(text, "\n")
}

func (p *ktmlPrinter) mark(n *Node, from int) {
	if n == nil {
		return
	}
	if _, ok := p.spans[n]; !ok {
		p.spans[n] = [2]int{from, p.line - 1}
	}
}

func (p *ktmlPrinter) nodes(indent int, nodes []kNode) {
	for _, node := range nodes {
		p.node(indent, node)
	}
}

func (p *ktmlPrinter) node(indent int, node kNode) {
	from := p.line
	switch n := node.(type) {
	case *kElement:
		p.notes(indent, n.attrs)
		p.element(indent, n.name, n.attrs, n.children, nil)
		p.mark(n.src, from)
		p.markAttrs(n.attrs, from)
	case *kCall:
		for i, slot := range n.slots {
			if slot.forwards == "" {
				continue
			}
			with, without := *n, *n
			with.slots = append([]kSlot(nil), n.slots...)
			with.slots[i].forwards = ""
			without.slots = append(append([]kSlot(nil), n.slots[:i]...), n.slots[i+1:]...)
			p.node(indent, &kIf{branches: []kBranch{{cond: slot.forwards + "?", body: []kNode{&with}}}, elseBody: []kNode{&without}, hasElse: true})
			return
		}
		p.notes(indent, n.attrs)
		p.element(indent, n.name, n.attrs, n.children, n.slots)
		p.mark(n.src, from)
		p.markAttrs(n.attrs, from)
	case *kText:
		p.writeLine(indent, ktmlValue(n.segments))
		for _, src := range n.src {
			p.mark(src, from)
		}
	case *kRaw:
		p.writeLine(indent, "```"+n.text+"```")
	case *kIf:
		for i, branch := range n.branches {
			if i == 0 {
				p.writeLine(indent, "if "+branch.cond+" {")
			} else {
				p.writeLine(indent, "} else if "+branch.cond+" {")
			}
			p.nodes(indent+1, branch.body)
		}
		if n.hasElse {
			p.writeLine(indent, "} else {")
			p.nodes(indent+1, n.elseBody)
		}
		p.writeLine(indent, "}")
		p.mark(n.src, from)
	case *kFor:
		p.writeLine(indent, "for "+n.item+" in "+n.coll+" key "+n.key+" {")
		p.nodes(indent+1, n.body)
		p.writeLine(indent, "}")
		p.mark(n.src, from)
	case *kOutlet:
		p.writeLine(indent, n.name)
		p.mark(n.src, from)
	case *kComment:
		p.writeLine(indent, "<!--"+n.text+"-->")
		p.mark(n.src, from)
	case *kDoctype:
		p.writeLine(indent, "doctype html")
		p.mark(n.src, from)
	case *kNote:
		for _, line := range strings.Split(n.text, "\n") {
			p.writeLine(indent, "// "+line)
		}
	}
}

// markAttrs maps an element's templ attributes to the element's lines.
func (p *ktmlPrinter) markAttrs(attrs []kAttr, from int) {
	for _, a := range attrs {
		p.mark(a.src, from)
		p.markAttrs(a.then, from)
		p.markAttrs(a.els, from)
		p.markAttrs(a.bag, from)
		if a.elseIf != nil {
			p.markAttrs([]kAttr{*a.elseIf}, from)
		}
	}
}

// notes prints the review markers of an element's attributes above it.
func (p *ktmlPrinter) notes(indent int, attrs []kAttr) {
	var walk func([]kAttr)
	walk = func(attrs []kAttr) {
		for _, a := range attrs {
			if a.note != "" {
				p.writeLine(indent, "// "+a.note)
			}
			walk(a.then)
			walk(a.els)
			walk(a.bag)
			if a.elseIf != nil {
				walk([]kAttr{*a.elseIf})
			}
		}
	}
	walk(attrs)
}

const maxHeaderWidth = 110

func (p *ktmlPrinter) element(indent int, name string, attrs []kAttr, children []kNode, slots []kSlot) {
	inline := name
	grouped := false
	attrs = writtenAttrs(attrs)
	for _, a := range attrs {
		if a.needsGroup() {
			grouped = true
		}
		inline += " " + attrText(a)
	}
	if len(inline)+indent*4 > maxHeaderWidth && len(attrs) > 1 {
		grouped = true
	}
	single := len(slots) == 0 && len(children) == 1
	var text string
	if single {
		switch child := children[0].(type) {
		case *kText:
			text = ktmlValue(child.segments)
		case *kRaw:
			text = "```" + child.text + "```"
		default:
			single = false
		}
	}
	// In KTML, a valueless name followed by a string is that attribute's
	// value (name "value"): inline text after one needs the group form.
	if single && !grouped && len(attrs) > 0 {
		if last := attrText(attrs[len(attrs)-1]); !strings.ContainsAny(last, "=#( ") {
			grouped = true
		}
	}
	open := ""
	switch {
	case single:
		open = " " + text
	case len(children) > 0 || len(slots) > 0:
		open = " {"
	}
	if !grouped {
		p.writeLine(indent, inline+open)
	} else {
		p.writeLine(indent, name+" (")
		for _, a := range attrs {
			p.attrLines(indent+1, a)
		}
		p.writeLine(indent, ")"+open)
	}
	if single {
		if child, ok := children[0].(*kText); ok {
			for _, src := range child.src {
				p.mark(src, p.line-1)
			}
		}
		return
	}
	if len(children) == 0 && len(slots) == 0 {
		return
	}
	p.nodes(indent+1, children)
	for _, slot := range slots {
		p.writeLine(indent+1, "slot "+slot.name+" {")
		p.nodes(indent+2, slot.body)
		p.writeLine(indent+1, "}")
	}
	p.writeLine(indent, "}")
}

func (p *ktmlPrinter) attrLines(indent int, a kAttr) {
	switch a.kind {
	case attrGroup:
		p.writeLine(indent, attrText(a))
	case attrBag:
		if len(writtenAttrs(a.bag)) == 0 {
			p.writeLine(indent, a.name+"=()")
			return
		}
		p.writeLine(indent, a.name+"=(")
		for _, entry := range a.bag {
			p.attrLines(indent+1, entry)
		}
		p.writeLine(indent, ")")
	default:
		p.writeLine(indent, attrText(a))
	}
}

// attrText spells one attribute entry on one line.
func attrText(a kAttr) string {
	switch a.kind {
	case attrBool:
		return a.name
	case attrValue:
		return a.name + "=" + a.value
	case attrSpread:
		return "..." + a.path
	case attrBag:
		return a.name + "=(" + attrList(a.bag) + ")"
	case attrGroup:
		text := "if " + a.cond + " { " + attrList(a.then) + " }"
		switch {
		case a.elseIf != nil:
			text += " else " + attrText(*a.elseIf)
		case a.els != nil:
			text += " else { " + attrList(a.els) + " }"
		}
		return text
	}
	return ""
}

func attrList(attrs []kAttr) string {
	attrs = writtenAttrs(attrs)
	parts := make([]string, len(attrs))
	for i, a := range attrs {
		parts[i] = attrText(a)
	}
	return strings.Join(parts, " ")
}

func writtenAttrs(attrs []kAttr) []kAttr {
	var out []kAttr
	for _, a := range attrs {
		if a.kind != attrNone {
			out = append(out, a)
		}
	}
	return out
}

// sourcePosition is a 1-based templ position.
func sourcePosition(p parser.Position) Position {
	return Position{Line: int(p.Line) + 1, Col: int(p.Col) + 1}
}
