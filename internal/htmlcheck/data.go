package htmlcheck

import (
	"sort"
	"strings"
)

// Element categories, from the HTML Living Standard's content models.
const (
	flow = 1 << iota
	phrasing
	heading
	interactive
	void
	transparent
	obsolete
	foreign
)

// Element describes one HTML element.
type Element struct {
	Name        string
	Description string
	flags       int
	// Attributes are the element-specific attributes beyond the global ones.
	Attributes []string
}

// Void reports whether the element can have no content.
func (e Element) Void() bool { return e.flags&void != 0 }

// Obsolete reports whether the element is obsolete in HTML.
func (e Element) Obsolete() bool { return e.flags&obsolete != 0 }

var elementList = []Element{
	{"a", "A hyperlink, or a placeholder for one without href.", flow | phrasing | transparent, []string{"href", "target", "download", "ping", "rel", "hreflang", "type", "referrerpolicy"}},
	{"abbr", "An abbreviation or acronym.", flow | phrasing, nil},
	{"address", "Contact information for the nearest article or body.", flow, nil},
	{"area", "A hyperlink region of an image map.", flow | phrasing | void, []string{"alt", "coords", "shape", "href", "target", "download", "ping", "rel", "referrerpolicy"}},
	{"article", "A self-contained composition, such as a post or a card.", flow, nil},
	{"aside", "Content tangentially related to its surroundings.", flow, nil},
	{"audio", "Sound or an audio stream.", flow | phrasing | transparent, []string{"src", "crossorigin", "preload", "autoplay", "loop", "muted", "controls", "controlslist", "disableremoteplayback"}},
	{"b", "Text set off for attention, without extra importance.", flow | phrasing, nil},
	{"base", "The document base URL and default browsing context.", void, []string{"href", "target"}},
	{"bdi", "Text isolated from its surroundings for bidirectional formatting.", flow | phrasing, nil},
	{"bdo", "Text with an explicit direction override.", flow | phrasing, nil},
	{"blockquote", "A section quoted from another source.", flow, []string{"cite"}},
	{"body", "The document's content.", 0, nil},
	{"br", "A line break.", flow | phrasing | void, nil},
	{"button", "A button.", flow | phrasing | interactive, []string{"command", "commandfor", "disabled", "form", "formaction", "formenctype", "formmethod", "formnovalidate", "formtarget", "name", "popovertarget", "popovertargetaction", "type", "value"}},
	{"canvas", "A scriptable bitmap canvas.", flow | phrasing | transparent, []string{"width", "height"}},
	{"caption", "The title of a table.", 0, nil},
	{"cite", "The title of a work.", flow | phrasing, nil},
	{"code", "A fragment of computer code.", flow | phrasing, nil},
	{"col", "One or more columns of a column group.", void, []string{"span"}},
	{"colgroup", "A group of table columns.", 0, []string{"span"}},
	{"data", "Content with a machine-readable value.", flow | phrasing, []string{"value"}},
	{"datalist", "Predefined options for other controls.", flow | phrasing, nil},
	{"dd", "The description in a description list group.", 0, nil},
	{"del", "A removal from the document.", flow | phrasing | transparent, []string{"cite", "datetime"}},
	{"details", "A disclosure widget.", flow | interactive, []string{"name", "open"}},
	{"dfn", "The defining instance of a term.", flow | phrasing, nil},
	{"dialog", "A dialog box or window.", flow, []string{"open", "closedby"}},
	{"div", "A generic flow container.", flow, nil},
	{"dl", "A description list.", flow, nil},
	{"dt", "The term in a description list group.", 0, nil},
	{"em", "Stress emphasis.", flow | phrasing, nil},
	{"embed", "An external application or interactive content.", flow | phrasing | interactive | void, []string{"src", "type", "width", "height"}},
	{"fieldset", "A group of form controls.", flow, []string{"disabled", "form", "name"}},
	{"figcaption", "The caption of a figure.", 0, nil},
	{"figure", "Self-contained content with an optional caption.", flow, nil},
	{"footer", "The footer of the nearest section or body.", flow, nil},
	{"form", "A form for submitting user input.", flow, []string{"accept-charset", "action", "autocomplete", "enctype", "method", "name", "novalidate", "target", "rel"}},
	{"h1", "A level 1 section heading.", flow | heading, nil},
	{"h2", "A level 2 section heading.", flow | heading, nil},
	{"h3", "A level 3 section heading.", flow | heading, nil},
	{"h4", "A level 4 section heading.", flow | heading, nil},
	{"h5", "A level 5 section heading.", flow | heading, nil},
	{"h6", "A level 6 section heading.", flow | heading, nil},
	{"head", "Document metadata.", 0, nil},
	{"header", "Introductory content of the nearest section or body.", flow, nil},
	{"hgroup", "A heading with related secondary content.", flow | heading, nil},
	{"hr", "A thematic break between paragraphs.", flow | void, nil},
	{"html", "The root of an HTML document.", 0, []string{"manifest", "xmlns"}},
	{"i", "Text in an alternate voice or mood.", flow | phrasing, nil},
	{"iframe", "A nested browsing context.", flow | phrasing | interactive, []string{"src", "srcdoc", "name", "sandbox", "allow", "allowfullscreen", "width", "height", "referrerpolicy", "loading", "credentialless"}},
	{"img", "An image.", flow | phrasing | void, []string{"alt", "src", "srcset", "sizes", "crossorigin", "usemap", "ismap", "width", "height", "referrerpolicy", "decoding", "loading", "fetchpriority"}},
	{"input", "A typed form control.", flow | phrasing | interactive | void, []string{"accept", "alpha", "alt", "autocomplete", "checked", "colorspace", "dirname", "disabled", "form", "formaction", "formenctype", "formmethod", "formnovalidate", "formtarget", "height", "list", "max", "maxlength", "min", "minlength", "multiple", "name", "pattern", "placeholder", "popovertarget", "popovertargetaction", "readonly", "required", "size", "src", "step", "type", "value", "width", "capture"}},
	{"ins", "An addition to the document.", flow | phrasing | transparent, []string{"cite", "datetime"}},
	{"kbd", "User input, such as keys.", flow | phrasing, nil},
	{"label", "A caption for a form control.", flow | phrasing | interactive, []string{"for"}},
	{"legend", "The caption of a fieldset.", 0, nil},
	{"li", "A list item.", 0, []string{"value"}},
	{"link", "A link to an external resource, such as a stylesheet.", phrasing | void, []string{"href", "crossorigin", "rel", "as", "media", "hreflang", "type", "sizes", "imagesrcset", "imagesizes", "referrerpolicy", "integrity", "blocking", "color", "disabled", "fetchpriority"}},
	{"main", "The dominant content of the document.", flow, nil},
	{"map", "An image map.", flow | phrasing | transparent, []string{"name"}},
	{"mark", "Text marked or highlighted for reference.", flow | phrasing, nil},
	{"menu", "A toolbar list of commands.", flow, nil},
	{"meta", "Metadata that other elements cannot express.", phrasing | void, []string{"name", "http-equiv", "content", "charset", "media"}},
	{"meter", "A scalar measurement within a known range.", flow | phrasing, []string{"value", "min", "max", "low", "high", "optimum"}},
	{"nav", "A section of navigation links.", flow, nil},
	{"noscript", "Content for user agents without scripting.", flow | phrasing, nil},
	{"object", "An external resource.", flow | phrasing | transparent, []string{"data", "type", "name", "form", "width", "height"}},
	{"ol", "An ordered list.", flow, []string{"reversed", "start", "type"}},
	{"optgroup", "A group of options in a select.", 0, []string{"disabled", "label"}},
	{"option", "An option in a select or datalist.", 0, []string{"disabled", "label", "selected", "value"}},
	{"output", "The result of a calculation or user action.", flow | phrasing, []string{"for", "form", "name"}},
	{"p", "A paragraph.", flow, nil},
	{"param", "A parameter of an object.", void | obsolete, []string{"name", "value"}},
	{"picture", "Image sources for one img.", flow | phrasing, nil},
	{"pre", "Preformatted text.", flow, nil},
	{"progress", "The completion progress of a task.", flow | phrasing, []string{"value", "max"}},
	{"q", "An inline quotation.", flow | phrasing, []string{"cite"}},
	{"rp", "Parentheses around ruby text.", 0, nil},
	{"rt", "Ruby text.", 0, nil},
	{"ruby", "Ruby annotations.", flow | phrasing, nil},
	{"s", "Content that is no longer accurate.", flow | phrasing, nil},
	{"samp", "Sample output of a program.", flow | phrasing, nil},
	{"script", "An embedded or external script.", phrasing, []string{"src", "type", "nomodule", "async", "defer", "crossorigin", "integrity", "referrerpolicy", "blocking", "fetchpriority"}},
	{"search", "A search or filtering section.", flow, nil},
	{"section", "A generic section of a document.", flow, nil},
	{"select", "A control for selecting among options.", flow | phrasing | interactive, []string{"autocomplete", "disabled", "form", "multiple", "name", "required", "size"}},
	{"slot", "A shadow tree slot.", flow | phrasing | transparent, []string{"name"}},
	{"small", "Side comments and small print.", flow | phrasing, nil},
	{"source", "A media or image source.", void, []string{"type", "media", "src", "srcset", "sizes", "width", "height"}},
	{"span", "A generic phrasing container.", flow | phrasing, nil},
	{"strong", "Strong importance.", flow | phrasing, nil},
	{"style", "Embedded CSS.", 0, []string{"media", "blocking"}},
	{"sub", "Subscript.", flow | phrasing, nil},
	{"summary", "The summary of a details element.", interactive, nil},
	{"sup", "Superscript.", flow | phrasing, nil},
	{"table", "Tabular data.", flow, nil},
	{"tbody", "A block of table rows.", 0, nil},
	{"td", "A table data cell.", 0, []string{"colspan", "rowspan", "headers"}},
	{"template", "An inert content template.", flow | phrasing, []string{"shadowrootmode", "shadowrootdelegatesfocus", "shadowrootclonable", "shadowrootserializable"}},
	{"textarea", "A multi-line plain text control.", flow | phrasing | interactive, []string{"autocomplete", "cols", "dirname", "disabled", "form", "maxlength", "minlength", "name", "placeholder", "readonly", "required", "rows", "wrap"}},
	{"tfoot", "The summary rows of a table.", 0, nil},
	{"th", "A table header cell.", 0, []string{"colspan", "rowspan", "headers", "scope", "abbr"}},
	{"thead", "The header rows of a table.", 0, nil},
	{"time", "A date or time.", flow | phrasing, []string{"datetime"}},
	{"title", "The document title.", 0, nil},
	{"tr", "A table row.", 0, nil},
	{"track", "A timed text track for media.", void, []string{"default", "kind", "label", "src", "srclang"}},
	{"u", "Unarticulated annotation, such as a misspelling.", flow | phrasing, nil},
	{"ul", "An unordered list.", flow, nil},
	{"var", "A variable.", flow | phrasing, nil},
	{"video", "A video.", flow | phrasing | transparent, []string{"src", "crossorigin", "poster", "preload", "autoplay", "playsinline", "loop", "muted", "controls", "controlslist", "width", "height", "disablepictureinpicture", "disableremoteplayback"}},
	{"wbr", "A line break opportunity.", flow | phrasing | void, nil},
	{"svg", "An embedded SVG image.", flow | phrasing | foreign, nil},
	{"math", "Embedded MathML.", flow | phrasing | foreign, nil},
	// Obsolete elements still parse; they are reported as obsolete.
	{"acronym", "Obsolete: use abbr.", flow | phrasing | obsolete, nil},
	{"applet", "Obsolete: use object or embed.", flow | obsolete, nil},
	{"basefont", "Obsolete.", void | obsolete, nil},
	{"big", "Obsolete: use CSS.", flow | phrasing | obsolete, nil},
	{"blink", "Obsolete.", flow | phrasing | obsolete, nil},
	{"center", "Obsolete: use CSS.", flow | obsolete, nil},
	{"dir", "Obsolete: use ul.", flow | obsolete, nil},
	{"font", "Obsolete: use CSS.", flow | phrasing | obsolete, nil},
	{"frame", "Obsolete.", obsolete, nil},
	{"frameset", "Obsolete.", obsolete, nil},
	{"marquee", "Obsolete: use CSS animation.", flow | obsolete, nil},
	{"nobr", "Obsolete: use CSS.", flow | phrasing | obsolete, nil},
	{"noframes", "Obsolete.", obsolete, nil},
	{"strike", "Obsolete: use s or del.", flow | phrasing | obsolete, nil},
	{"tt", "Obsolete: use code or kbd.", flow | phrasing | obsolete, nil},
	{"xmp", "Obsolete: use pre and code.", flow | obsolete, nil},
}

var elements = func() map[string]Element {
	table := make(map[string]Element, len(elementList))
	for _, element := range elementList {
		table[element.Name] = element
	}
	return table
}()

// LookupElement returns the HTML element of a name, case-insensitively.
func LookupElement(name string) (Element, bool) {
	element, ok := elements[strings.ToLower(name)]
	return element, ok
}

// Elements returns every non-obsolete element in name order.
func Elements() []Element {
	var list []Element
	for _, element := range elementList {
		if !element.Obsolete() {
			list = append(list, element)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// globalAttributes are allowed on every HTML element.
var globalAttributes = map[string]string{
	"accesskey":          "A keyboard shortcut for the element.",
	"autocapitalize":     "How text input is capitalized.",
	"autocorrect":        "Whether text input is autocorrected.",
	"autofocus":          "Focus the element when the page or dialog opens.",
	"class":              "Space-separated CSS classes.",
	"contenteditable":    "Whether the user can edit the content.",
	"dir":                "Text direction: ltr, rtl, or auto.",
	"draggable":          "Whether the element can be dragged: true or false.",
	"enterkeyhint":       "The label of the enter key on virtual keyboards.",
	"hidden":             "The element is not relevant now and is not rendered.",
	"id":                 "A document-unique identifier.",
	"inert":              "The element and its subtree are not interactive.",
	"inputmode":          "The virtual keyboard to show.",
	"is":                 "A customized built-in element name.",
	"itemid":             "Microdata: the global identifier of an item.",
	"itemprop":           "Microdata: a property name.",
	"itemref":            "Microdata: ids of additional properties.",
	"itemscope":          "Microdata: the element creates an item.",
	"itemtype":           "Microdata: the item vocabulary.",
	"lang":               "The language of the content.",
	"nonce":              "A cryptographic nonce for Content Security Policy.",
	"part":               "Shadow part names.",
	"popover":            "Makes the element a popover: auto, manual, or hint.",
	"role":               "The ARIA role that overrides the element's implicit role.",
	"slot":               "The shadow tree slot the element is assigned to.",
	"spellcheck":         "Whether to check spelling: true or false.",
	"style":              "Inline CSS declarations.",
	"tabindex":           "Whether and in which order the element is focusable; use 0 or -1.",
	"title":              "Advisory information, shown as a tooltip.",
	"translate":          "Whether the content is translated: yes or no.",
	"writingsuggestions": "Whether the browser offers writing suggestions.",
	// RDFa and Open Graph attributes are common in real documents.
	"property": "RDFa: a property.", "typeof": "RDFa: a type.", "vocab": "RDFa: a vocabulary.",
	"prefix": "RDFa: prefixes.", "resource": "RDFa: a resource.", "about": "RDFa: the subject.",
}

// tolerated are obsolete presentational attributes that browsers still
// honour. They are not reported, since reporting them is noise.
var tolerated = map[string]bool{
	"align": true, "valign": true, "bgcolor": true, "border": true, "cellpadding": true,
	"cellspacing": true, "width": true, "height": true, "nowrap": true, "frameborder": true,
	"scrolling": true, "marginwidth": true, "marginheight": true, "language": true,
	"charset": true, "name": true, "type": true, "summary": true, "color": true, "face": true,
	"size": true, "clear": true, "hspace": true, "vspace": true, "longdesc": true, "rev": true,
	"shape": true, "coords": true, "axis": true, "char": true, "charoff": true,
}

var eventHandlers = func() map[string]bool {
	names := strings.Fields(`abort auxclick beforeinput beforematch beforetoggle blur cancel canplay
	canplaythrough change click close command contextlost contextmenu contextrestored copy cuechange cut
	dblclick drag dragend dragenter dragleave dragover dragstart drop durationchange emptied ended error
	focus focusin focusout formdata input invalid keydown keypress keyup load loadeddata loadedmetadata
	loadstart mousedown mouseenter mouseleave mousemove mouseout mouseover mouseup paste pause play
	playing progress ratechange reset resize scroll scrollend securitypolicyviolation seeked seeking
	select slotchange stalled submit suspend timeupdate toggle volumechange waiting wheel
	afterprint beforeprint beforeunload hashchange languagechange message messageerror offline online
	pagehide pageshow pagereveal pageswap popstate rejectionhandled storage unhandledrejection unload
	pointerdown pointerup pointermove pointerover pointerout pointerenter pointerleave pointercancel
	gotpointercapture lostpointercapture touchstart touchend touchmove touchcancel
	animationstart animationend animationiteration animationcancel
	transitionstart transitionend transitionrun transitioncancel selectstart selectionchange
	fullscreenchange fullscreenerror search`)
	table := make(map[string]bool, len(names))
	for _, name := range names {
		table["on"+name] = true
	}
	return table
}()

// EventHandlers returns every on* attribute name in order.
func EventHandlers() []string {
	names := make([]string, 0, len(eventHandlers))
	for name := range eventHandlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// frameworkAttribute reports names that frameworks own: Alpine.js, HTMX, data
// attributes, and namespaced names. The HTML analyzer leaves them to their
// analyzers.
func frameworkAttribute(name string) bool {
	switch {
	case strings.HasPrefix(name, "data-"), strings.HasPrefix(name, "x-"), strings.HasPrefix(name, "hx-"),
		strings.HasPrefix(name, "@"), strings.HasPrefix(name, ":"), strings.HasPrefix(name, "sse-"),
		strings.HasPrefix(name, "ws-"), strings.HasPrefix(name, "xmlns"), strings.HasPrefix(name, "xml:"),
		strings.HasPrefix(name, "xlink:"), strings.HasPrefix(name, "v-"):
		return true
	}
	return strings.ContainsAny(name, ".*[]{}")
}

// ElementAttributes returns the attributes an element accepts beyond
// aria-*, data-*, and event handlers: its own, then the global ones.
func ElementAttributes(element string) []string {
	var names []string
	if info, ok := LookupElement(element); ok {
		names = append(names, info.Attributes...)
	}
	global := make([]string, 0, len(globalAttributes))
	for name := range globalAttributes {
		global = append(global, name)
	}
	sort.Strings(global)
	return append(names, global...)
}

// knownAttribute reports whether name is valid on element, which is a
// known HTML element.
func knownAttribute(element, name string) bool {
	name = strings.ToLower(name)
	if _, ok := globalAttributes[name]; ok || tolerated[name] || eventHandlers[name] || frameworkAttribute(name) {
		return true
	}
	if strings.HasPrefix(name, "aria-") {
		return true
	}
	info, _ := LookupElement(element)
	for _, attribute := range info.Attributes {
		if attribute == name {
			return true
		}
	}
	return false
}

// attributeDescriptions documents element-specific attributes for hover.
var attributeDescriptions = map[string]string{
	"href": "The URL of the hyperlink or resource.", "target": "The browsing context for navigation, such as _blank.",
	"rel": "The relationship of the linked resource.", "download": "Download the resource instead of navigating.",
	"src": "The URL of the embedded resource.", "srcset": "Candidate image sources.", "sizes": "Image sizes for srcset.",
	"alt":  "The text alternative of an image. Use alt=\"\" for decorative images.",
	"type": "The type of the control, button, or resource.", "value": "The value of the control.",
	"name": "The name used in form submission or lookups.", "disabled": "The control is disabled.",
	"checked": "The checkbox or radio is checked.", "selected": "The option is selected.",
	"placeholder": "A short hint shown when the control is empty; not a label.",
	"required":    "The control must have a value.", "readonly": "The value cannot be edited.",
	"for": "The id of the labeled control.", "action": "The URL that processes the form.",
	"method": "The HTTP method of the form: get, post, or dialog.", "enctype": "The form encoding.",
	"autocomplete": "Autofill hints for the control.", "loading": "Lazy or eager loading.",
	"decoding": "The image decoding hint.", "crossorigin": "The CORS mode of the request.",
	"referrerpolicy": "The referrer sent with requests.", "fetchpriority": "The fetch priority hint.",
	"colspan": "The number of columns the cell spans.", "rowspan": "The number of rows the cell spans.",
	"scope": "The cells a header cell applies to.", "open": "The details or dialog is open.",
	"multiple": "Allow several values.", "min": "The minimum value.", "max": "The maximum value.",
	"step": "The value granularity.", "pattern": "A pattern the value must match.",
	"maxlength": "The maximum length of the value.", "minlength": "The minimum length of the value.",
	"rows": "The number of visible text lines.", "cols": "The visible width in characters.",
	"wrap": "How the value wraps when submitted.", "form": "The id of the owning form.",
	"novalidate": "Skip form validation on submission.", "datetime": "A machine-readable date or time.",
	"label": "A user-visible label.", "kind": "The kind of text track.", "srclang": "The language of the track.",
	"controls": "Show the user agent's media controls.", "autoplay": "Start playing automatically.",
	"muted": "Mute the media by default.", "loop": "Loop the media.", "poster": "An image shown before playback.",
	"preload": "How much media to preload.", "popovertarget": "The id of the popover to control.",
	"popovertargetaction": "What the button does to its popover.", "content": "The metadata value.",
	"http-equiv": "A pragma directive.", "media": "The media the resource applies to.",
}

// AttributeDescription documents an attribute for hover.
func AttributeDescription(name string) (string, bool) {
	name = strings.ToLower(name)
	if description, ok := globalAttributes[name]; ok {
		return description, true
	}
	if description, ok := attributeDescriptions[name]; ok {
		return description, true
	}
	if eventHandlers[name] {
		return "An inline " + strings.TrimPrefix(name, "on") + " event handler (JavaScript).", true
	}
	if attribute, ok := ariaAttributes[name]; ok {
		return attribute.description, true
	}
	return "", false
}

// enumerated lists the keyword values of attributes, keyed "element attribute"
// or " attribute" for any element. Validated values are reported when a
// literal is not one of them; the rest are offered only as completions.
type enumeration struct {
	values    []string
	validated bool
}

var enumerated = map[string]enumeration{
	"input type":                 {strings.Fields("button checkbox color date datetime-local email file hidden image month number password radio range reset search submit tel text time url week"), true},
	"button type":                {strings.Fields("submit reset button"), true},
	"form method":                {strings.Fields("get post dialog"), true},
	"form enctype":               {[]string{"application/x-www-form-urlencoded", "multipart/form-data", "text/plain"}, true},
	"button formmethod":          {strings.Fields("get post dialog"), true},
	"input formmethod":           {strings.Fields("get post dialog"), true},
	" dir":                       {strings.Fields("ltr rtl auto"), true},
	"img loading":                {strings.Fields("lazy eager"), true},
	"iframe loading":             {strings.Fields("lazy eager"), true},
	"img decoding":               {strings.Fields("sync async auto"), true},
	" crossorigin":               {[]string{"anonymous", "use-credentials", ""}, true},
	" fetchpriority":             {strings.Fields("high low auto"), true},
	" referrerpolicy":            {strings.Fields("no-referrer no-referrer-when-downgrade origin origin-when-cross-origin same-origin strict-origin strict-origin-when-cross-origin unsafe-url"), true},
	"audio preload":              {[]string{"none", "metadata", "auto", ""}, true},
	"video preload":              {[]string{"none", "metadata", "auto", ""}, true},
	"textarea wrap":              {strings.Fields("soft hard"), true},
	"th scope":                   {strings.Fields("row col rowgroup colgroup"), true},
	"ol type":                    {strings.Fields("1 a A i I"), false},
	" draggable":                 {strings.Fields("true false"), true},
	" translate":                 {[]string{"yes", "no", ""}, true},
	" spellcheck":                {[]string{"true", "false", ""}, true},
	" contenteditable":           {[]string{"true", "false", "plaintext-only", ""}, true},
	" autocapitalize":            {strings.Fields("off none on sentences words characters"), true},
	" enterkeyhint":              {strings.Fields("enter done go next previous search send"), true},
	" inputmode":                 {strings.Fields("none text decimal numeric tel search email url"), true},
	" hidden":                    {[]string{"", "hidden", "until-found"}, true},
	" popover":                   {[]string{"", "auto", "manual", "hint"}, true},
	" popovertargetaction":       {strings.Fields("toggle show hide"), true},
	"track kind":                 {strings.Fields("subtitles captions descriptions chapters metadata"), true},
	" target":                    {strings.Fields("_self _blank _parent _top"), false},
	" rel":                       {strings.Fields("alternate author bookmark canonical dns-prefetch external help icon license manifest modulepreload next nofollow noopener noreferrer opener pingback preconnect prefetch preload prev search stylesheet tag"), false},
	"link as":                    {strings.Fields("audio document embed fetch font image object script style track video worker"), false},
	" autocomplete":              {strings.Fields("on off name honorific-prefix given-name additional-name family-name honorific-suffix nickname email username new-password current-password one-time-code organization-title organization street-address address-line1 address-line2 address-line3 address-level1 address-level2 country country-name postal-code cc-name cc-number cc-exp cc-csc cc-type transaction-amount language bday sex tel tel-national url photo"), false},
	"script type":                {strings.Fields("module importmap speculationrules text/javascript application/json application/ld+json"), false},
	"details name":               {nil, false},
	"template shadowrootmode":    {strings.Fields("open closed"), true},
	"area shape":                 {strings.Fields("rect circle poly default"), false},
	"dialog closedby":            {strings.Fields("any closerequest none"), true},
	"input capture":              {strings.Fields("user environment"), false},
	"button popovertargetaction": {strings.Fields("toggle show hide"), true},
}

// AttributeValues returns the keyword values of an attribute and whether a
// literal outside them is invalid.
func AttributeValues(element, attribute string) ([]string, bool) {
	element, attribute = strings.ToLower(element), strings.ToLower(attribute)
	if values, ok := enumerated[element+" "+attribute]; ok {
		return values.values, values.validated
	}
	if values, ok := enumerated[" "+attribute]; ok {
		return values.values, values.validated
	}
	if aria, ok := ariaAttributes[attribute]; ok {
		return aria.values, aria.values != nil
	}
	if attribute == "role" {
		return Roles(), false
	}
	return nil, false
}

type ariaAttribute struct {
	description string
	// values are the keyword values; nil means free text, an id, or a number.
	values []string
	// boolean is set for true/false-valued states, whose value must be
	// written: an empty value is not true.
	boolean bool
	integer bool
}

var (
	ariaBool     = []string{"true", "false"}
	ariaTristate = []string{"true", "false", "mixed"}
	ariaOptional = []string{"true", "false", "undefined"}
)

var ariaAttributes = map[string]ariaAttribute{
	"aria-activedescendant":       {description: "The id of the active descendant of a composite widget."},
	"aria-atomic":                 {description: "Whether assistive technology presents the whole live region on change.", values: ariaBool, boolean: true},
	"aria-autocomplete":           {description: "How suggestions are presented: inline, list, both, or none.", values: strings.Fields("inline list both none")},
	"aria-braillelabel":           {description: "A braille label."},
	"aria-brailleroledescription": {description: "A braille role description."},
	"aria-busy":                   {description: "Whether the element is being updated.", values: ariaBool, boolean: true},
	"aria-checked":                {description: "The checked state of a checkbox, radio, or switch.", values: append(ariaTristate, "undefined"), boolean: true},
	"aria-colcount":               {description: "The number of columns in a table or grid.", integer: true},
	"aria-colindex":               {description: "The column index of a cell.", integer: true},
	"aria-colindextext":           {description: "A human-readable column index."},
	"aria-colspan":                {description: "The number of columns a cell spans.", integer: true},
	"aria-controls":               {description: "The ids of the elements this element controls."},
	"aria-current":                {description: "The current item in a set: page, step, location, date, time, true, or false.", values: strings.Fields("page step location date time true false"), boolean: true},
	"aria-describedby":            {description: "The ids of the elements that describe this element."},
	"aria-description":            {description: "A description of the element."},
	"aria-details":                {description: "The ids of elements with extended details."},
	"aria-disabled":               {description: "Whether the element is perceivable but disabled.", values: ariaBool, boolean: true},
	"aria-dropeffect":             {description: "Deprecated drag-and-drop effects.", values: strings.Fields("copy execute link move none popup")},
	"aria-errormessage":           {description: "The id of the element with this element's error message."},
	"aria-expanded":               {description: "Whether the controlled grouping is expanded.", values: ariaOptional, boolean: true},
	"aria-flowto":                 {description: "The ids of the next elements in an alternate reading order."},
	"aria-grabbed":                {description: "Deprecated drag-and-drop state.", values: ariaOptional, boolean: true},
	"aria-haspopup":               {description: "The kind of popup the element opens.", values: strings.Fields("false true menu listbox tree grid dialog"), boolean: true},
	"aria-hidden":                 {description: "Whether the element is hidden from assistive technology.", values: ariaOptional, boolean: true},
	"aria-invalid":                {description: "Whether the value is invalid: grammar, spelling, true, or false.", values: strings.Fields("grammar false spelling true"), boolean: true},
	"aria-keyshortcuts":           {description: "Keyboard shortcuts that activate the element."},
	"aria-label":                  {description: "A string that labels the element."},
	"aria-labelledby":             {description: "The ids of the elements that label this element."},
	"aria-level":                  {description: "The hierarchical level of the element.", integer: true},
	"aria-live":                   {description: "How updates to a live region are announced.", values: strings.Fields("assertive off polite")},
	"aria-modal":                  {description: "Whether the element is modal when shown.", values: ariaBool, boolean: true},
	"aria-multiline":              {description: "Whether a text box accepts multiple lines.", values: ariaBool, boolean: true},
	"aria-multiselectable":        {description: "Whether several items can be selected.", values: ariaBool, boolean: true},
	"aria-orientation":            {description: "The orientation of the element.", values: strings.Fields("horizontal vertical undefined")},
	"aria-owns":                   {description: "The ids of elements this element owns."},
	"aria-placeholder":            {description: "A hint for an empty control."},
	"aria-posinset":               {description: "The position of the item in its set.", integer: true},
	"aria-pressed":                {description: "The pressed state of a toggle button.", values: append(ariaTristate, "undefined"), boolean: true},
	"aria-readonly":               {description: "Whether the element is read-only.", values: ariaBool, boolean: true},
	"aria-relevant":               {description: "Which changes to a live region are announced."},
	"aria-required":               {description: "Whether input is required.", values: ariaBool, boolean: true},
	"aria-roledescription":        {description: "A human-readable role description."},
	"aria-rowcount":               {description: "The number of rows in a table or grid.", integer: true},
	"aria-rowindex":               {description: "The row index of a row or cell.", integer: true},
	"aria-rowindextext":           {description: "A human-readable row index."},
	"aria-rowspan":                {description: "The number of rows a cell spans.", integer: true},
	"aria-selected":               {description: "The selected state of an item.", values: ariaOptional, boolean: true},
	"aria-setsize":                {description: "The number of items in the set.", integer: true},
	"aria-sort":                   {description: "The sort order of a column: ascending, descending, none, or other.", values: strings.Fields("ascending descending none other")},
	"aria-valuemax":               {description: "The maximum value of a range widget."},
	"aria-valuemin":               {description: "The minimum value of a range widget."},
	"aria-valuenow":               {description: "The current value of a range widget."},
	"aria-valuetext":              {description: "A human-readable value of a range widget."},
}

// AriaAttributes returns every aria-* attribute name in order.
func AriaAttributes() []string {
	names := make([]string, 0, len(ariaAttributes))
	for name := range ariaAttributes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var roles = func() map[string]bool {
	names := strings.Fields(`alert alertdialog application article banner blockquote button caption cell
	checkbox code columnheader combobox comment complementary contentinfo definition deletion dialog
	directory document emphasis feed figure form generic grid gridcell group heading img image insertion
	link list listbox listitem log main mark marquee math menu menubar menuitem menuitemcheckbox
	menuitemradio meter navigation none note option paragraph presentation progressbar radio radiogroup
	region row rowgroup rowheader scrollbar search searchbox separator slider spinbutton status strong
	subscript suggestion superscript switch tab table tablist tabpanel term textbox time timer toolbar
	tooltip tree treegrid treeitem graphics-document graphics-object graphics-symbol
	doc-abstract doc-acknowledgments doc-afterword doc-appendix doc-backlink doc-biblioentry
	doc-bibliography doc-biblioref doc-chapter doc-colophon doc-conclusion doc-cover doc-credit
	doc-credits doc-dedication doc-endnote doc-endnotes doc-epigraph doc-epilogue doc-errata doc-example
	doc-footnote doc-foreword doc-glossary doc-glossref doc-index doc-introduction doc-noteref doc-notice
	doc-pagebreak doc-pagefooter doc-pageheader doc-pagelist doc-part doc-preface doc-prologue
	doc-pullquote doc-qna doc-subtitle doc-tip doc-toc`)
	table := make(map[string]bool, len(names))
	for _, name := range names {
		table[name] = true
	}
	return table
}()

var abstractRoles = map[string]bool{
	"command": true, "composite": true, "input": true, "landmark": true, "range": true, "roletype": true,
	"section": true, "sectionhead": true, "select": true, "structure": true, "widget": true, "window": true,
}

// Roles returns every concrete ARIA role in order.
func Roles() []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// implicitRoles are the roles of elements that take no other role; writing
// the implicit role is redundant but allowed.
var implicitRoles = map[string]string{
	"main": "main", "textarea": "textbox", "option": "option", "progress": "progressbar",
	"meter": "meter", "caption": "caption", "dd": "definition", "optgroup": "group",
	"datalist": "listbox", "summary": "button",
}

// allowedRoles is the subset of ARIA in HTML whose rules are clear: the
// roles an element may take besides its implicit one. A nil list allows no
// role; an element missing from the table allows any role.
func allowedRoles(element string, attributes map[string]string) ([]string, bool) {
	switch element {
	case "base", "col", "colgroup", "datalist", "head", "html", "link", "map", "meta", "noscript",
		"param", "picture", "script", "slot", "source", "style", "template", "title", "track",
		"label", "legend", "main", "option", "progress", "meter", "textarea", "optgroup", "caption",
		"summary", "dd":
		return nil, true
	case "button":
		return strings.Fields("button checkbox combobox link menuitem menuitemcheckbox menuitemradio option radio switch tab"), true
	case "a":
		if _, ok := attributes["href"]; !ok {
			return nil, false
		}
		return strings.Fields("link button checkbox menuitem menuitemcheckbox menuitemradio option radio switch tab treeitem doc-backlink doc-biblioref doc-glossref doc-noteref"), true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return strings.Fields("heading tab none presentation doc-subtitle"), true
	case "ul", "ol":
		return strings.Fields("list directory group listbox menu menubar none presentation radiogroup tablist toolbar tree"), true
	case "form":
		return strings.Fields("form search none presentation"), true
	case "nav":
		return strings.Fields("navigation doc-index doc-pagelist doc-toc menu menubar none presentation tablist"), true
	case "header":
		return strings.Fields("banner group none presentation"), true
	case "footer":
		return strings.Fields("contentinfo group none presentation doc-footnote"), true
	case "fieldset":
		return strings.Fields("group none presentation radiogroup"), true
	case "hr":
		return strings.Fields("separator none presentation doc-pagebreak"), true
	case "dialog":
		return strings.Fields("dialog alertdialog"), true
	case "iframe":
		return strings.Fields("application document img none presentation"), true
	case "img":
		alt, has := attributes["alt"]
		if has && alt == "" {
			return strings.Fields("none presentation"), true
		}
		return nil, false
	case "input":
		switch strings.ToLower(attributes["type"]) {
		case "checkbox":
			return strings.Fields("checkbox button menuitemcheckbox option switch"), true
		case "radio":
			return strings.Fields("radio menuitemradio"), true
		case "hidden":
			return nil, true
		}
	}
	return nil, false
}
