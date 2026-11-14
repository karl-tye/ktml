package alpine

import (
	"sort"
	"strings"
)

// Directive is a parsed Alpine attribute name: x-on:click.prevent is
// {Name: "on", Argument: "click", Modifiers: ["prevent"]}.
type Directive struct {
	Name      string
	Argument  string
	Modifiers []string
	// Shorthand reports @ or : spelling.
	Shorthand bool
}

// ParseDirective parses an Alpine attribute name, or returns false.
func ParseDirective(attribute string) (Directive, bool) {
	var directive Directive
	var rest string
	switch {
	case strings.HasPrefix(attribute, "@"):
		directive.Name, directive.Shorthand, rest = "on", true, attribute[1:]
	case strings.HasPrefix(attribute, ":") && len(attribute) > 1:
		directive.Name, directive.Shorthand, rest = "bind", true, attribute[1:]
	case strings.HasPrefix(attribute, "x-"):
		name := attribute[2:]
		end := strings.IndexAny(name, ":.")
		if end < 0 {
			return Directive{Name: name}, name != ""
		}
		directive.Name, rest = name[:end], name[end:]
		if rest[0] == '.' {
			directive.Modifiers = strings.Split(rest[1:], ".")
			return directive, directive.Name != ""
		}
		rest = rest[1:]
	default:
		return Directive{}, false
	}
	argument, modifiers, _ := strings.Cut(rest, ".")
	directive.Argument = argument
	if modifiers != "" || strings.HasSuffix(rest, ".") {
		directive.Modifiers = strings.Split(modifiers, ".")
	}
	return directive, true
}

// valueKind is how Alpine reads a directive's value.
type valueKind int

const (
	// expressionValue is JavaScript evaluated in the element's scope.
	expressionValue valueKind = iota
	// handlerValue is JavaScript run on an event, with $event in scope.
	handlerValue
	// plainValue is not JavaScript: classes, a selector, a name.
	plainValue
	// noValue takes no value.
	noValue
	forValue
	dataValue
)

type directiveInfo struct {
	value       valueKind
	description string
	plugin      string
	// template requires a template element.
	template bool
	// argument is whether the directive takes :argument.
	argument  bool
	modifiers []string
}

var directives = map[string]directiveInfo{
	"data":      {value: dataValue, description: "Declares a component and its reactive data. Its properties are in scope for the element and its descendants; methods reach them through this."},
	"init":      {value: handlerValue, description: "Runs code when the element is initialized."},
	"show":      {value: expressionValue, description: "Toggles display: none by the truthiness of the expression.", modifiers: []string{"important"}},
	"bind":      {value: expressionValue, argument: true, description: "Sets an attribute to the result of the expression; :class and :style merge with the static value.", modifiers: []string{"camel", "prop"}},
	"on":        {value: handlerValue, argument: true, description: "Runs code when the event fires; $event is the event."},
	"text":      {value: expressionValue, description: "Sets the text content to the result of the expression."},
	"html":      {value: expressionValue, description: "Sets the inner HTML to the result of the expression. Never use it with user content."},
	"model":     {value: expressionValue, description: "Binds the value of an input, select, or textarea to a data property.", modifiers: []string{"lazy", "change", "blur", "enter", "number", "boolean", "debounce", "throttle", "fill"}},
	"modelable": {value: expressionValue, description: "Exposes a data property so an outer x-model can bind it."},
	"for":       {value: forValue, template: true, description: "Repeats the template's single root element for each item: item in items or (item, index) in items."},
	"transition": {value: plainValue, argument: true, description: "Transitions the element when x-show toggles it.",
		modifiers: []string{"in", "out", "opacity", "scale", "duration", "delay", "origin", "top", "bottom", "left", "right"}},
	"effect":   {value: expressionValue, description: "Re-runs the expression whenever a dependency changes."},
	"ignore":   {value: noValue, description: "Alpine does not initialize the element or its descendants.", modifiers: []string{"self"}},
	"ref":      {value: plainValue, description: "Names the element in $refs of its component."},
	"cloak":    {value: noValue, description: "Removed when Alpine initializes; hide [x-cloak] in CSS to avoid a flash."},
	"teleport": {value: plainValue, template: true, description: "Moves the template's content to the element matching the selector."},
	"if":       {value: expressionValue, template: true, description: "Adds or removes the template's single root element by the truthiness of the expression."},
	"id":       {value: expressionValue, description: "Declares id scopes for $id."},
	// Official plugins.
	"intersect": {value: handlerValue, plugin: "intersect", argument: true, description: "Runs code when the element enters or leaves the viewport.", modifiers: []string{"once", "half", "full", "threshold", "margin"}},
	"mask":      {value: plainValue, plugin: "mask", argument: true, description: "Formats input as it is typed; x-mask:dynamic takes an expression."},
	"trap":      {value: expressionValue, plugin: "focus", description: "Traps focus inside the element while the expression is true.", modifiers: []string{"inert", "noscroll", "noreturn", "noautofocus"}},
	"collapse":  {value: noValue, plugin: "collapse", description: "Animates height when x-show toggles the element.", modifiers: []string{"duration", "min"}},
	"anchor":    {value: expressionValue, plugin: "anchor", description: "Positions the element relative to a reference element.", modifiers: []string{"top", "bottom", "left", "right", "start", "end", "offset", "no-style", "fixed"}},
	"sort":      {value: handlerValue, plugin: "sort", argument: true, description: "Makes the children sortable by drag and drop.", modifiers: []string{"ghost", "config"}},
	"resize":    {value: handlerValue, plugin: "resize", description: "Runs code when the element is resized.", modifiers: []string{"document"}},
}

// onModifiers apply to every event.
var onModifiers = []string{"prevent", "stop", "outside", "window", "document", "once", "debounce", "throttle", "self", "camel", "dot", "passive", "capture"}

// keyEvents take key name modifiers, which are open-ended.
var keyEvents = map[string]bool{"keydown": true, "keyup": true, "keypress": true}

var events = strings.Fields(`click dblclick mousedown mouseup mouseenter mouseleave mousemove mouseover mouseout
contextmenu keydown keyup keypress input change submit reset focus blur focusin focusout scroll resize load
error wheel pointerdown pointerup pointermove pointerenter pointerleave touchstart touchend touchmove drag
dragstart dragend dragenter dragleave dragover drop copy cut paste select toggle animationend transitionend
htmx:before-request htmx:after-request htmx:after-swap htmx:after-settle htmx:before-swap htmx:load
htmx:config-request htmx:response-error htmx:send-error`)

// magics are Alpine's $ properties. $event exists only in handlers.
var magics = map[string]string{
	"$el":       "The current DOM element.",
	"$refs":     "Elements marked with x-ref in this component.",
	"$store":    "Global stores registered with Alpine.store.",
	"$watch":    "Watches a property: $watch('name', value => …).",
	"$dispatch": "Dispatches a browser CustomEvent from the element.",
	"$nextTick": "Runs code after Alpine updates the DOM.",
	"$root":     "The root element of this component.",
	"$data":     "The data of the closest components, as one object.",
	"$id":       "Generates an id unique to the closest x-id scope.",
	"$event":    "The event of an x-on handler.",
	"$persist":  "Persists a value in localStorage (persist plugin).",
	"$focus":    "Focus utilities (focus plugin).",
	"$anchor":   "The anchored position (anchor plugin).",
}

// globals are names JavaScript and the browser provide.
var globals = func() map[string]bool {
	table := make(map[string]bool)
	for _, name := range strings.Fields(`window document console Math JSON Date Number String Boolean Array Object
	Promise Symbol Map Set WeakMap WeakSet RegExp Error TypeError Intl BigInt Reflect Proxy fetch setTimeout
	clearTimeout setInterval clearInterval requestAnimationFrame cancelAnimationFrame queueMicrotask
	encodeURIComponent decodeURIComponent encodeURI decodeURI parseInt parseFloat isNaN isFinite alert confirm
	prompt location navigator history localStorage sessionStorage URL URLSearchParams FormData Headers
	Request Response AbortController CustomEvent Event structuredClone crypto performance screen
	getComputedStyle matchMedia innerWidth innerHeight scrollX scrollY scrollTo open close print
	htmx Alpine undefined NaN Infinity globalThis self event arguments navigator Intl atob btoa
	HTMLElement Element Node File Blob FileReader IntersectionObserver ResizeObserver MutationObserver
	Notification Audio Image`) {
		table[name] = true
	}
	return table
}()

// Directives returns every directive name in order.
func Directives() []string {
	names := make([]string, 0, len(directives))
	for name := range directives {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Events returns common event names.
func Events() []string { return events }

// Magics returns every magic property name in order.
func Magics() []string {
	names := make([]string, 0, len(magics))
	for name := range magics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Modifiers returns the modifiers a directive accepts.
func Modifiers(directive Directive) []string {
	if directive.Name == "on" {
		return onModifiers
	}
	return directives[directive.Name].modifiers
}

// DirectiveDescription documents a directive for hover.
func DirectiveDescription(name string) (string, bool) {
	info, ok := directives[name]
	if !ok {
		return "", false
	}
	text := info.description
	if info.plugin != "" {
		text += " (Alpine " + info.plugin + " plugin)"
	}
	return text, true
}

// MagicDescription documents a magic property.
func MagicDescription(name string) (string, bool) {
	text, ok := magics[name]
	return text, ok
}
