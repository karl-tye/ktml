package htmlcheck

import "strings"

// CursorKind is the markup position of an editor cursor.
type CursorKind int

const (
	// NoMarkup is outside element markup: a declaration, text, or a
	// comment.
	NoMarkup CursorKind = iota
	// ElementName is where an element or component name starts a line or
	// follows a {.
	ElementName
	// AttributeName is an attribute name in an element header.
	AttributeName
	// AttributeValue is inside an attribute's quoted value.
	AttributeValue
)

// Cursor describes the markup around an offset. Prefix is the part of the
// name or value before the cursor, starting at PrefixStart; Element is the
// element whose header holds the attribute; ValueStart is the offset after
// the value's opening quote.
type Cursor struct {
	Kind        CursorKind
	Element     string
	Attribute   string
	Prefix      string
	PrefixStart int
	ValueStart  int
	Quote       byte
	// Bag reports a position inside an attribute bag literal, name=( … ):
	// its attributes reach an element the bag is spread on, so Element is
	// empty. BagElement and BagAttribute are the call and input the bag is
	// given for.
	Bag          bool
	BagElement   string
	BagAttribute string
	// Body is the element whose braced body holds the cursor's line, and
	// Slot reports a cursor on a slot fill's name, after slot.
	Body string
	Slot bool
	// Supplied are the attribute names the header holds before the
	// cursor.
	Supplied []string
}

// frame kinds of the lexical scan.
const (
	scanBody  = iota // a braced element or component body
	scanGroup        // a parenthesized attribute group
	scanDecl         // a [ … ] declaration list, or an import list
	scanAttrIf
)

type scanFrame struct {
	kind    int
	element string
	// bag marks an attribute bag and the conditional groups inside it;
	// outer is the header the bag interrupted and bagName its attribute.
	bag     bool
	outer   string
	bagName string
	// supplied are the interrupted header's attribute names.
	supplied []string
}

// CursorAt classifies the cursor offset in a KTML source. It scans the
// source lexically, so it works on documents that do not parse while they
// are being edited.
func CursorAt(source []byte, offset int) Cursor {
	if offset > len(source) {
		offset = len(source)
	}
	var stack []scanFrame
	// header is the element of the current line's header; headerTokens
	// counts the tokens since the header started.
	header, headerTokens := "", 0
	lastName, lastNameEnd := "", -1
	afterAssign := false
	top := func() int {
		if len(stack) == 0 {
			return -1
		}
		return stack[len(stack)-1].kind
	}
	inBag := func() bool {
		return len(stack) > 0 && stack[len(stack)-1].bag
	}
	// slotLine reports a body line that starts with slot; supplied are the
	// header's attribute names so far.
	slotLine := false
	var supplied []string
	body := func() string {
		for index := len(stack) - 1; index >= 0; index-- {
			if stack[index].kind == scanBody {
				return stack[index].element
			}
		}
		return ""
	}
	finish := func(cursor Cursor) Cursor {
		cursor.Body = body()
		if cursor.Kind == NoMarkup && slotLine && headerTokens == 1 && top() == scanBody {
			cursor.Slot = true
		}
		cursor.Supplied = supplied
		return cursor
	}
	startHeader := func() { header, headerTokens, afterAssign, slotLine, supplied = "", 0, false, false, nil }
	i := 0
	for i < offset {
		c := source[i]
		switch {
		case c == '\n':
			if top() == scanBody {
				startHeader()
			}
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			for i < len(source) && source[i] != '\n' {
				i++
			}
			if i >= offset {
				return Cursor{}
			}
		case c == '`' && i+2 < len(source) && source[i+1] == '`' && source[i+2] == '`':
			end := strings.Index(string(source[i+3:]), "```")
			if end < 0 || i+3+end+3 > offset {
				return Cursor{}
			}
			i += 3 + end + 3
		case c == '"' || c == '`':
			start := i
			i++
			for i < len(source) && source[i] != c {
				if c == '"' && source[i] == '\\' {
					i++
				}
				if c == '"' && source[i] == '\n' {
					break
				}
				i++
			}
			if i >= offset {
				// The cursor is inside this string.
				if afterAssign && lastNameEnd >= 0 && inBag() {
					frame := stack[len(stack)-1]
					return finish(Cursor{Kind: AttributeValue, Attribute: lastName, Prefix: string(source[start+1 : offset]), PrefixStart: start + 1, ValueStart: start + 1, Quote: c, Bag: true, BagElement: frame.outer, BagAttribute: frame.bagName})
				}
				if afterAssign && lastNameEnd >= 0 && header != "" {
					return finish(Cursor{Kind: AttributeValue, Element: header, Attribute: lastName, Prefix: string(source[start+1 : offset]), PrefixStart: start + 1, ValueStart: start + 1, Quote: c})
				}
				return finish(Cursor{})
			}
			i++
			headerTokens++
			afterAssign = false
		case c == '<' && strings.HasPrefix(string(source[i:]), "<!--"):
			end := strings.Index(string(source[i:]), "-->")
			if end < 0 || i+end+3 > offset {
				return Cursor{}
			}
			i += end + 3
		case c == '{':
			if top() == scanGroup || top() == scanAttrIf {
				parent := stack[len(stack)-1]
				stack = append(stack, scanFrame{kind: scanAttrIf, element: parent.element, bag: parent.bag})
			} else if top() == scanDecl {
				stack = append(stack, scanFrame{kind: scanDecl})
			} else {
				stack = append(stack, scanFrame{kind: scanBody, element: header})
				startHeader()
			}
			i++
		case c == '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if top() == scanBody {
				startHeader()
			}
			i++
		case c == '(':
			if afterAssign {
				// name=( opens an attribute bag.
				outer := header
				if inBag() {
					outer = stack[len(stack)-1].outer
				}
				stack = append(stack, scanFrame{kind: scanGroup, bag: true, outer: outer, bagName: lastName, supplied: supplied})
				afterAssign, supplied = false, nil
			} else {
				stack = append(stack, scanFrame{kind: scanGroup, element: header})
			}
			headerTokens++
			i++
		case c == ')':
			if len(stack) > 0 && top() == scanGroup {
				frame := stack[len(stack)-1]
				header = frame.element
				if frame.bag {
					header, supplied = frame.outer, frame.supplied
				}
				stack = stack[:len(stack)-1]
			}
			i++
		case c == '[':
			stack = append(stack, scanFrame{kind: scanDecl})
			i++
		case c == ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			i++
		case c == '=':
			afterAssign = true
			i++
		case nameByte(c):
			start := i
			for i < len(source) && nameByte(source[i]) {
				i++
			}
			if i >= offset {
				// The cursor is at the end of this name.
				return finish(cursorName(stack, header, headerTokens, string(source[start:offset]), start))
			}
			word := string(source[start:i])
			if top() == scanBody && headerTokens == 0 {
				header = word
				if word == "if" || word == "for" || word == "else" || word == "slot" {
					header = ""
				}
				slotLine = word == "slot"
			} else if !afterAssign && headerTokens > 0 && (header != "" || inBag()) {
				supplied = append(supplied, word)
			} else if top() == -1 && word == "import" {
				// An import is one line of declarations.
				for i < len(source) && source[i] != '\n' {
					i++
				}
				if i >= offset {
					return Cursor{}
				}
				continue
			}
			if afterAssign {
				afterAssign = false
			} else {
				lastName, lastNameEnd = word, i
			}
			headerTokens++
		default:
			i++
		}
	}
	// The cursor follows whitespace or punctuation.
	return finish(cursorName(stack, header, headerTokens, "", offset))
}

func cursorName(stack []scanFrame, header string, headerTokens int, prefix string, start int) Cursor {
	if len(stack) == 0 {
		return Cursor{}
	}
	switch top := stack[len(stack)-1]; top.kind {
	case scanBody:
		if headerTokens == 0 {
			return Cursor{Kind: ElementName, Prefix: prefix, PrefixStart: start}
		}
		if header != "" {
			return Cursor{Kind: AttributeName, Element: header, Prefix: prefix, PrefixStart: start}
		}
	case scanGroup, scanAttrIf:
		if top.bag {
			return Cursor{Kind: AttributeName, Prefix: prefix, PrefixStart: start, Bag: true, BagElement: top.outer, BagAttribute: top.bagName}
		}
		if top.element != "" {
			return Cursor{Kind: AttributeName, Element: top.element, Prefix: prefix, PrefixStart: start}
		}
	}
	return Cursor{}
}

func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ':' || c == '.' || c == '@' || c == '#' || c == '*'
}
