package storygen

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/karl-tye/ktml/internal/compiler"
)

// Story is one story to write: values for a component's inputs and the text
// that fills its content inputs.
type Story struct {
	Component compiler.Component
	Name      string
	Inputs    []InputValue
	Fills     []Fill
	// Variants lists the Variant symbols the story selects anywhere in its
	// values, for coverage counts.
	Variants []compiler.SymbolID
}

// InputValue is the value a story gives one input.
type InputValue struct {
	Name  string
	Value compiler.Value
}

// Fill is the content of one content input: Slot is empty for children.
type Fill struct {
	Slot string
	Text string
}

// seed fixes the synthesized values: the same field always gets the same
// value, whatever else changed.
const seed = "ktml-stories-v1"

// maxDepth bounds recursive records: below it optional values are None and
// collections are empty.
const maxDepth = 8

// longText is the text of the LongText story's free-text fields.
const longText = "This is a deliberately long piece of text that checks how the component wraps, truncates, or overflows when a value is much longer than usual, such as a detailed note left by a customer about access to the property."

// PlaceholderPrefix starts every URL a story synthesizes:
// /_story/<component>/<field>, and /<n> for the nth item of a collection.
// The Explorer serves no route under it and answers requests to it with 204
// No Content, so a placeholder can never load the Explorer, or an
// application route, into a preview.
const PlaceholderPrefix = "/_story/"

// Placeholder fixtures for trusted values: a safe value of each trusted
// type, so generated stories never invent trusted content.
var placeholderFixtures = map[string]struct{ name, value string }{
	compiler.TrustedURLType:    {"placeholderURL", PlaceholderPrefix + "trusted-url"},
	compiler.JSONValueType:     {"placeholderJSON", "{}"},
	compiler.TrustedScriptType: {"placeholderScript", "{}"},
	compiler.CSSValueType:      {"placeholderCSS", ""},
}

// FixtureDecl is a fixture a story Document declares.
type FixtureDecl struct {
	Name  string
	Type  string
	Value string
}

// synthesizer derives story values from component input types.
type synthesizer struct {
	snapshot *compiler.Snapshot
	// fixture names the fixture a story uses for a trusted type; nil uses
	// the placeholders.
	fixture func(typeName string) string
	// keys lists, by element type, the key paths of the workspace's
	// repetitions over collections of that type; nil until first used.
	keys map[string][][]string
}

// position is a state input the component selects Render Clauses on: each
// of its Variants gets a story.
type position struct {
	key   string
	label string
	state compiler.State
}

// mode is the case a story shows.
type mode struct {
	variants    map[string]string
	empty       bool
	noOptionals bool
	long        bool
}

// Synthesis is the generated stories of one component, or why it has none.
type Synthesis struct {
	Stories []Story
	// Skipped explains a component that cannot have generated stories.
	Skipped string
	// Variants holds the Variants of every state in the component's input
	// types, and Covered those its stories select.
	Variants map[compiler.SymbolID]bool
	Covered  map[compiler.SymbolID]bool
}

// Synthesize derives a component's stories from its input types: one per
// Variant of each state input it selects Render Clauses on (or one Default
// story without), then Empty (its collections empty), NoOptionals (its
// optional values absent), and LongText (long free text, for small
// stories), each when it differs from the others. Empty and NoOptionals
// change values at the inputs and one record field below them, such as
// view.items; deeper ones are rendered by nested components, whose own
// stories vary them. A page, a component a handler renders, gets only its
// Default or Variant stories: its view holds the views of the components it
// renders, which have their own Empty, NoOptionals, and LongText stories.
func Synthesize(snapshot *compiler.Snapshot, component compiler.Component, page bool) Synthesis {
	s := &synthesizer{snapshot: snapshot}
	return s.synthesize(component, page)
}

func (s *synthesizer) synthesize(component compiler.Component, page bool) Synthesis {
	result := Synthesis{Variants: make(map[compiler.SymbolID]bool), Covered: make(map[compiler.SymbolID]bool)}
	var positions []position
	// A component branches on a state only by selecting Render Clauses on
	// one of its inputs; a state deeper in its values is passed to a nested
	// component, whose own stories show its Variants.
	selectors := make(map[string]bool)
	for _, group := range s.snapshot.Groups(component.File) {
		if group.Component == component.ID && group.Kind == compiler.StateSelector {
			selectors[group.Selector.Name] = true
		}
	}
	for _, input := range component.Inputs {
		if input.IsContent() || input.Mode == compiler.DefaultedInput {
			continue
		}
		s.variants(input.Type, 0, result.Variants)
		if selectors[input.Name] && input.Type.Kind == compiler.StateTypeKind {
			if state, ok := s.snapshot.State(input.Type.State); ok {
				positions = append(positions, position{key: input.Name, label: upperCamel(input.Name), state: state})
			}
		}
	}

	var cases []struct {
		name string
		mode mode
	}
	if len(positions) == 0 {
		cases = append(cases, struct {
			name string
			mode mode
		}{"Default", mode{}})
	}
	for index, p := range positions {
		for variantIndex, variant := range p.state.Variants {
			if index > 0 && variantIndex == 0 {
				continue
			}
			name := variant.Name
			if len(positions) > 1 {
				name = p.label + variant.Name
			}
			cases = append(cases, struct {
				name string
				mode mode
			}{name, mode{variants: map[string]string{p.key: variant.Name}}})
		}
	}
	if !page {
		cases = append(cases,
			struct {
				name string
				mode mode
			}{"Empty", mode{empty: true}},
			struct {
				name string
				mode mode
			}{"NoOptionals", mode{noOptionals: true}},
			struct {
				name string
				mode mode
			}{"LongText", mode{long: true}},
		)
	}

	seen := make(map[string]bool)
	names := make(map[string]bool)
	for _, c := range cases {
		story := s.story(component, c.mode)
		key := storyKey(story)
		if c.mode.long && len(key) > longTextBudget {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		story.Name = uniqueName(c.name, names)
		for _, id := range story.Variants {
			result.Covered[id] = true
		}
		result.Stories = append(result.Stories, story)
	}
	return result
}

// storyKey identifies a story by what it renders, so a case that changes
// nothing (Empty without collections) is left out.
func storyKey(story Story) string {
	var b strings.Builder
	for _, input := range story.Inputs {
		b.WriteString(input.Name)
		b.WriteByte('=')
		b.WriteString(ValueText(input.Value))
		b.WriteByte(';')
	}
	for _, fill := range story.Fills {
		b.WriteString(fill.Slot)
		b.WriteByte(':')
		b.WriteString(fill.Text)
		b.WriteByte(';')
	}
	return b.String()
}

func uniqueName(name string, used map[string]bool) string {
	candidate := name
	for suffix := 2; used[candidate]; suffix++ {
		candidate = fmt.Sprintf("%s%d", name, suffix)
	}
	used[candidate] = true
	return candidate
}

// variants collects the Variants of every state in a type, for coverage.
func (s *synthesizer) variants(typ compiler.Type, depth int, all map[compiler.SymbolID]bool) {
	if depth > maxDepth {
		return
	}
	switch typ.Kind {
	case compiler.StateTypeKind:
		state, ok := s.snapshot.State(typ.State)
		if !ok {
			return
		}
		for _, variant := range state.Variants {
			if all[variant.ID] {
				continue
			}
			all[variant.ID] = true
			for _, field := range variant.Fields {
				s.variants(field.Type, depth+1, all)
			}
		}
	case compiler.RecordTypeKind:
		if record, ok := s.snapshot.Record(typ.Record); ok {
			for _, field := range record.Fields {
				s.variants(field.Type, depth+1, all)
			}
		}
	case compiler.CollectionTypeKind:
		if typ.Element != nil {
			s.variants(*typ.Element, depth+1, all)
		}
	}
}

// itemLimit bounds the items of a populated collection; collections of
// states show up to this many Variants.
const itemLimit = 6

// longTextBudget bounds the stories that get a LongText case: in a deep
// view, every string made long would make a story of many kilobytes.
const longTextBudget = 2048

func (s *synthesizer) story(component compiler.Component, m mode) Story {
	story := Story{Component: component}
	for _, input := range component.Inputs {
		switch {
		case input.IsContent():
			if input.Mode == compiler.RequiredInput || !m.noOptionals {
				story.Fills = append(story.Fills, contentFill(input))
			}
			continue
		case input.Mode == compiler.DefaultedInput:
			continue
		}
		value, ok := s.value(input.Type, input.Name, input.Name, input.Mode == compiler.OptionalInput, 0, m, 0, 0, &story)
		if ok {
			story.Inputs = append(story.Inputs, InputValue{Name: input.Name, Value: value})
		}
	}
	return story
}

func contentFill(input compiler.Input) Fill {
	if input.Name == compiler.ChildrenInput {
		return Fill{Text: "Sample content"}
	}
	return Fill{Slot: input.Name, Text: "Sample " + strings.ToLower(humanize(input.Name))}
}

// value synthesizes one value. ok is false for an absent optional, which a
// story leaves out.
//
// fields counts the record fields between the input and the value: Empty
// and NoOptionals change only values at the input or one field below it,
// as state positions do; deeper ones belong to nested components.
func (s *synthesizer) value(typ compiler.Type, name, key string, optional bool, index int, m mode, depth, fields int, story *Story) (compiler.Value, bool) {
	if optional && (m.noOptionals && fields <= 1 || depth > maxDepth) {
		return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, false
	}
	switch typ.Kind {
	case compiler.ScalarTypeKind:
		return synthScalar(typ.Scalar, story.Component.Name, name, index, m.long), true
	case compiler.ContextualTypeKind:
		if typ.Name == compiler.AttributesType {
			// The empty bag is the only one a story gives.
			return compiler.Value{Kind: compiler.AttributesValueKind, Type: typ}, true
		}
		if typ.Name == compiler.URLType {
			return compiler.Value{Kind: compiler.ScalarValueKind, Type: typ, String: synthURL(story.Component.Name, name, index)}, true
		}
		placeholder := placeholderFixtures[typ.Name]
		fixture := placeholder.name
		if s.fixture != nil {
			fixture = s.fixture(typ.Name)
		}
		return compiler.Value{Kind: compiler.FixtureValueKind, Type: typ, Fixture: fixture, String: placeholder.value}, true
	case compiler.RecordTypeKind:
		record, _ := s.snapshot.Record(typ.Record)
		value := compiler.Value{Kind: compiler.RecordValueKind, Type: typ, Fields: []compiler.FieldValue{}}
		for _, field := range record.Fields {
			if fieldValue, ok := s.value(field.Type, field.Name, key+"."+field.Name, field.Optional, index, m, depth+1, fields+1, story); ok {
				value.Fields = append(value.Fields, compiler.FieldValue{Name: field.Name, Value: fieldValue})
			}
		}
		return value, true
	case compiler.CollectionTypeKind:
		value := compiler.Value{Kind: compiler.CollectionValueKind, Type: typ, Items: []compiler.Value{}}
		if m.empty && fields <= 1 || depth > maxDepth || typ.Element == nil {
			return value, true
		}
		// Only collections outside collections have several items: nested
		// ones would multiply a deep view's size.
		count := 1
		if !strings.Contains(key, "[]") {
			count = s.itemCount(*typ.Element, 0)
		}
		for item := range count {
			element, _ := s.value(*typ.Element, name, key+"[]", false, item, m, depth+1, fields, story)
			value.Items = append(value.Items, element)
		}
		for _, path := range s.keyPaths(*typ.Element) {
			uniqueKeys(value.Items, path, name)
		}
		return value, true
	case compiler.StateTypeKind:
		state, ok := s.snapshot.State(typ.State)
		if !ok || len(state.Variants) == 0 {
			return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, false
		}
		variant := state.Variants[0]
		if strings.Contains(key, "[]") {
			variant = state.Variants[index%len(state.Variants)]
		} else if chosen, ok := m.variants[key]; ok {
			variant, _ = state.Variant(chosen)
		}
		story.Variants = append(story.Variants, variant.ID)
		value := compiler.Value{Kind: compiler.VariantValueKind, Type: typ, Variant: variant.Name}
		if variant.Payload {
			value.Fields = []compiler.FieldValue{}
			for _, field := range variant.Fields {
				if fieldValue, ok := s.value(field.Type, field.Name, key+"."+variant.Name+"."+field.Name, field.Optional, index, m, depth+1, fields, story); ok {
					value.Fields = append(value.Fields, compiler.FieldValue{Name: field.Name, Value: fieldValue})
				}
			}
		}
		return value, true
	}
	return compiler.Value{Kind: compiler.NoneValueKind, Type: typ}, false
}

// itemCount is how many items a populated collection of element has: two,
// or enough to show each Variant of a state its items hold, up to
// itemLimit.
func (s *synthesizer) itemCount(element compiler.Type, depth int) int {
	count := 2
	var visit func(typ compiler.Type, depth int)
	visit = func(typ compiler.Type, depth int) {
		if depth > maxDepth {
			return
		}
		switch typ.Kind {
		case compiler.StateTypeKind:
			if state, ok := s.snapshot.State(typ.State); ok {
				count = max(count, min(len(state.Variants), itemLimit))
				for _, variant := range state.Variants {
					for _, field := range variant.Fields {
						visit(field.Type, depth+1)
					}
				}
			}
		case compiler.RecordTypeKind:
			if record, ok := s.snapshot.Record(typ.Record); ok {
				for _, field := range record.Fields {
					visit(field.Type, depth+1)
				}
			}
		}
	}
	visit(element, depth)
	return count
}

func upperCamel(name string) string {
	list := words(name)
	var b strings.Builder
	for _, word := range list {
		if word == "" {
			continue
		}
		b.WriteString(strings.ToUpper(word[:1]))
		b.WriteString(word[1:])
	}
	if b.Len() == 0 {
		return "Value"
	}
	return b.String()
}

// pick chooses one of options for a field, fixed by its name and varied by
// the item index, so items of a collection differ.
func pick(options []string, name string, index int) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed + "/" + name))
	return options[(int(h.Sum32()%uint32(len(options)))+index)%len(options)]
}

var (
	firstNames = []string{"Avery", "Morgan", "Jordan", "Riley", "Casey", "Taylor", "Quinn", "Rowan"}
	lastNames  = []string{"Clarke", "Reyes", "Patel", "Okafor", "Nguyen", "Hughes", "Kowalski", "Brennan"}
	companies  = []string{"Acme Books Ltd", "Northside Stationers", "Brightwell Printing", "Harbour Press", "Oakfield Bindery", "Summit Paper Co"}
	streets    = []string{"12 Example Street", "4 Sample Road", "27 Test Avenue", "9 Placeholder Lane", "51 Demo Close", "3 Fixture Way"}
	cities     = []string{"London", "Leeds", "Bristol", "Manchester", "Glasgow", "Cardiff"}
	postcodes  = []string{"SW1A 1AA", "LS1 4AP", "BS1 5TR", "M1 1AE", "G1 1XQ", "CF10 1EP"}
	statuses   = []string{"Active", "Pending", "Completed", "Scheduled", "On hold", "Cancelled"}
	titles     = []string{"Spring catalogue", "Signed first edition", "Book club order", "Gift wrapping", "Author evening", "Shelf restock"}
	sentences  = []string{"Ordered from the publisher on Tuesday.", "Customer asked for a call when it arrives.", "Two copies reserved for collection next week.", "Gift note added at the till.", "Quote approved by the customer.", "Courier confirmed the delivery slot."}
	dates      = []string{"14 Mar 2026", "15 Mar 2026", "16 Mar 2026", "17 Mar 2026", "18 Mar 2026", "19 Mar 2026"}
	times      = []string{"09:15", "10:30", "11:45", "13:00", "14:15", "15:30"}
)

// synthURL is the placeholder URL of a component's field, such as
// /_story/messaging-history/history-url, and /_story/job-card/link/2 for a
// collection's second item.
func synthURL(component, name string, index int) string {
	url := PlaceholderPrefix + segment(component) + "/" + segment(name)
	if index > 0 {
		url += fmt.Sprintf("/%d", index+1)
	}
	return url
}

// segment is a name's words in kebab case, reduced to [a-z0-9-].
func segment(name string) string {
	var parts []string
	for _, word := range words(name) {
		word = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, word)
		if word != "" {
			parts = append(parts, word)
		}
	}
	if len(parts) == 0 {
		return "value"
	}
	return strings.Join(parts, "-")
}

// synthScalar picks a plausible value for a scalar field from its name.
func synthScalar(scalar compiler.ValueType, component, name string, index int, long bool) compiler.Value {
	value := compiler.Value{Kind: compiler.ScalarValueKind, Type: compiler.ScalarTypeOf(scalar)}
	category := classify(name)
	switch scalar {
	case compiler.BooleanType:
		value.Boolean = !negative(name)
	case compiler.IntegerType:
		value.Integer = synthInteger(category, index)
		value.Number = float64(value.Integer)
	case compiler.NumberType:
		switch category {
		case categoryMoney:
			value.Number = 120.5 + float64(index)*15
		case categoryPercent:
			value.Number = 42.5 + float64(index)
		default:
			value.Number = float64(synthInteger(category, index)) + 0.5
		}
	default:
		value.String = synthString(category, component, name, index, long)
	}
	return value
}

func synthInteger(category category, index int) int64 {
	switch category {
	case categoryID:
		return 1001 + int64(index)
	case categoryMoney:
		return 120 + int64(index)*15
	case categoryPercent:
		return 42 + int64(index)
	default:
		return 3 + int64(index)
	}
}

func synthString(category category, component, name string, index int, long bool) string {
	switch category {
	case categoryClass, categoryIcon:
		// Classes are markup: an invented class would only add noise.
		return ""
	case categoryEmail:
		first, lastName := pick(firstNames, name, index), pick(lastNames, name, index)
		return strings.ToLower(first+"."+lastName) + "@example.com"
	case categoryPhone:
		h := fnv.New32a()
		_, _ = h.Write([]byte(seed + "/" + name))
		return fmt.Sprintf("07700 900%03d", (int(h.Sum32()%1000)+index)%1000)
	case categoryPostcode:
		return pick(postcodes, name, index)
	case categoryAddress:
		return pick(streets, name, index)
	case categoryCity:
		return pick(cities, name, index)
	case categoryFirstName:
		return pick(firstNames, name, index)
	case categoryLastName:
		return pick(lastNames, name, index)
	case categoryFullName:
		return pick(firstNames, name, index) + " " + pick(lastNames, name+"/last", index)
	case categoryCompany:
		return pick(companies, name, index)
	case categoryID:
		return fmt.Sprintf("%s-%d", strings.ToLower(strings.Join(words(name), "-")), 1001+index)
	case categorySecret:
		return fmt.Sprintf("example-%s-%d", strings.ToLower(strings.Join(words(name), "-")), index+1)
	case categoryURL:
		return synthURL(component, name, index)
	case categoryDateTime:
		return pick(dates, name, index) + " " + pick(times, name, index)
	case categoryDate:
		return pick(dates, name, index)
	case categoryTime:
		return pick(times, name, index)
	case categoryMoney:
		return fmt.Sprintf("£%d.00", 120+index*15)
	case categoryPercent:
		return fmt.Sprintf("%d%%", 42+index)
	case categoryCount:
		return fmt.Sprintf("%d", 3+index)
	case categoryStatus:
		return pick(statuses, name, index)
	case categoryTitle:
		if long {
			return longText
		}
		return pick(titles, name, index)
	case categoryFreeText:
		if long {
			return longText
		}
		return pick(sentences, name, index)
	}
	if long {
		return longText
	}
	text := humanize(name)
	if index > 0 {
		text = fmt.Sprintf("%s %d", text, index+1)
	}
	return text
}
