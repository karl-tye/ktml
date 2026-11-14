# HTML, accessibility, and Alpine.js analysis

Two analyzers of the federated language server (PLAN "Federated LSP and IDE
Support") check the markup KTML components write. Both follow one rule:
**report only what is statically clear, and prefer silence to a false
positive.** A component call, slot content, a content outlet, an attribute
spread, a framework binding, an interpolated value, or an HTMX swap can
change what the browser sees, and anything such a construct makes unknown is
not reported.

## HTML and accessibility

`internal/htmlcheck` is native Go. The KTML semantic tooling (the gohtml
LSP) serves it, so it needs no plugin of its own; the optional `html`
section of `ktml.config.json` declares HTMX extension attributes (see
"HTMX attribute names"). Its diagnostics are warnings
(`source: ktml`). Content in a void element is the compiler error
`void-content` (ADR-0001), so the analyzer does not report it.

| Code | Reports |
| --- | --- |
| `html-unknown-element`, `html-obsolete-element` | a lower-case name that is not an HTML element, a custom element (`a-b`), a component, or a content outlet; obsolete elements such as `center` |
| `html-unknown-attribute` | an attribute that is neither global nor the element's own. `data-*`, `aria-*`, event handlers, Alpine (`x-*`, `@`, `:`), RDFa, and obsolete presentational attributes browsers still honour are known. `hx-*` and `data-hx-*` names must be HTMX attributes (below) or declared extension attributes |
| `html-invalid-attribute-value` | a literal keyword value outside its set, such as `input type="txt"` |
| `html-duplicate-attribute` | two unconditional attributes of one name (the compiler reports conditional ones) |
| `html-duplicate-id` | one literal id twice in a component body, outside exclusive `if` branches and repetitions |
| `html-invalid-nesting` | required parents (`li`, `tr`, `td`, `option`, `legend`, …), table and list children (the browser moves stray table content out of the table), blocks that close an open `p`, and a form inside a form |
| `a11y-missing-alt` | `img` or `input type="image"` with no text alternative |
| `a11y-missing-label` | `input`, `select`, or `textarea` with no label (see below) |
| `a11y-missing-name` | `button`, `a href`, or `input type="button"` with no accessible name |
| `a11y-unknown-aria`, `a11y-invalid-aria-value` | unknown `aria-*` names, literal values outside their type, and a boolean path in an `aria-*` attribute that is not a true/false state (`aria-label="{open}"`) |
| `html-boolean-presence` | an exact boolean path in `draggable`, whose value must be the text `true` or `false`: KTML writes it as a bare attribute or nothing. `aria-*` booleans render `true`/`false` (ADR-0001) |
| `a11y-invalid-role`, `a11y-role-not-allowed` | unknown or abstract roles, and roles ARIA in HTML forbids on an element (a clear subset; elements outside it accept any role) |
| `a11y-heading-order` | a heading level that skips one, within a component body |
| `a11y-positive-tabindex` | `tabindex` above 0 |
| `a11y-nested-interactive` | interactive content inside `a` or `button` |

Silence rules worth knowing:

- Element names are checked against the parent only when the parent is an
  HTML element of the same component. At the component root, and in content
  passed to a component, the parent is unknown. A `template` is transparent:
  Alpine stamps x-for and x-if content into the template's parent.
- A form control is labelled by `aria-label`, `aria-labelledby`, `title`,
  `placeholder`, a bound name (`:aria-label`), an enclosing `label` in the
  component, or a `label for` anywhere in the workspace (any dynamic `for`
  counts). The first labelable control of a component may also be labelled
  by a caller that wraps the component in a label, lexically or through slot
  content; the call graph decides. A component nothing calls is rendered
  from Go and is checked.
- `hidden`, `aria-hidden="true"`, and Tailwind's `hidden` and `invisible`
  classes remove an element from the accessibility tree.
- Accessible names come from text, interpolations, `img alt`, `svg title`,
  and descendant `aria-label` or `title`. Components, outlets, slot content,
  `x-text`, and `x-html` make a name unknown.

### HTMX attribute names

The analyzer knows the attribute names of htmx 2 and htmx 4, so a misspelt
`hx-tigger` is reported with a fix to `hx-trigger`. The HTMX plugin is not
needed. A name may carry a `data-` prefix and htmx 4 `:modifier` suffixes
(`hx-indicator:inherited`, `hx-ext:append`, `hx-sse:connect`,
`hx-status:404`); the stem before the first `:` is checked, so a known stem
accepts any modifier. Known stems:

- requests: `hx-get`, `hx-post`, `hx-put`, `hx-patch`, `hx-delete`,
  `hx-action`, `hx-method`
- core: `hx-trigger`, `hx-target`, `hx-swap`, `hx-swap-oob`, `hx-select`,
  `hx-select-oob`, `hx-include`, `hx-indicator`, `hx-params`, `hx-encoding`,
  `hx-sync`, `hx-boost`, `hx-push-url`, `hx-replace-url`, `hx-confirm`,
  `hx-prompt`, `hx-history`, `hx-history-elt`, `hx-request`, `hx-validate`,
  `hx-headers`, `hx-vals`, `hx-vars`, `hx-ext`, `hx-inherit`,
  `hx-disinherit`, `hx-disable`, `hx-disabled-elt`, `hx-preserve`,
  `hx-config`, `hx-optimistic`, `hx-partial`, `hx-ignore`, `hx-on`,
  `hx-status`
- extensions: `hx-preload`, `hx-alpine-compat`, `hx-targets`, `hx-sse`,
  `hx-ws`
- open families, whose suffix is not checked: `hx-on:…`, `hx-on::…`,
  `hx-on-…`, `hx-target-…` (response-targets: `hx-target-404`,
  `hx-target-4*`, `hx-target-error`), `hx-status…`, `hx-sse-…`, `hx-ws-…`

A project's own HTMX extension attributes are declared in
`ktml.config.json`; each is then known with and without `data-` and with
any `:modifier`:

```json
{
  "html": { "extension_attributes": ["hx-require"] }
}
```

Only `hx-*` names can be declared (anything else is a configuration error
on `ktml.config.json`). There is no general allowlist for custom hook
attributes: those belong in `data-*`, which is always accepted. The setting
takes effect when `ktml lsp` starts, like the rest of the configuration.

Completion covers element names, the attributes of HTML elements (their
own, global, ARIA, and event handlers), and keyword values. Hover documents
elements and attributes. Quick fixes rename typos (elements, attributes,
`aria-*`), set `tabindex="0"`, remove a duplicate attribute, and mark an
image decorative with `alt=""`.

## Alpine.js

`internal/alpine` is native Go, served by the first-party `alpine` plugin
(`cmd/plugins/alpine`), like the HTMX integration. Enable it in
`ktml.config.json`:

```json
{
  "plugin": {
    "alpine": { "path": "./plugins/alpine.wasm", "allow_read": ["."] }
  },
  "resolution": { "lsp": ["gohtml", "alpine"] }
}
```

Directive values are parsed as JavaScript: expressions and the statement
forms Alpine accepts, arrow functions, template literals, and destructuring.
KTML interpolations in a value are holes typed from KTML: a `boolean` input
in `x-data="{ open: {open} }"` gives `open` the type `boolean`.
Inline TypeScript inference is not attempted; complex JavaScript belongs in
modules.

Scopes:

- A literal object in `x-data` declares properties, methods, and getters for
  the element and its descendants. A value that is not a literal object (a
  call such as `dropdown()`, or one built in Go) is an opaque scope.
- Method bodies reach the data only through `this`; bare names in a method
  resolve in the enclosing scopes, as Alpine evaluates them.
- `x-for` on a template declares its item and index for the template's
  content and its `:key`.
- `x-ref` names belong to the closest `x-data`, which `$refs` reads.

Diagnostics (`source: alpine`):

| Code | Reports |
| --- | --- |
| `alpine-syntax` | a value that is not valid JavaScript |
| `alpine-directive-placement` | `x-for`, `x-if`, or `x-teleport` off a `template`; `x-on` with no event |
| `alpine-directive-combination` | `x-for` and `x-if` on one template; `:key` off an `x-for` template |
| `alpine-template-root` | an `x-for` or `x-if` template whose content is not exactly one element, when the count is known |
| `alpine-unknown-directive`, `alpine-unknown-modifier` | near misses of known directives and modifiers (custom directives are not reported) |
| `alpine-scope-reference` | `$event` outside an `x-on` handler |
| `alpine-missing-this` | a bare data property inside a method of the same `x-data` |
| `alpine-unknown-ref` | `$refs.name` when no `x-ref` of the component declares it and nothing unknown renders inside it |
| `alpine-out-of-scope` | a name declared only by an `x-data` or `x-for` of the same component that does not enclose the use |
| `alpine-swap-resets-state` | an `x-data` element that its own HTMX request replaces (`hx-swap="outerHTML"` without another target), while something in it writes its data |

A name that reaches the component root unresolved is never reported: a
caller, slot content, or an HTMX swap can place the component inside any
`x-data`. The plugin also provides completion (directives, events,
modifiers, names in scope, `this.`, `$refs.`, and fields of literal
objects), hover, definitions, references, scoped rename, and Alpine scopes
in workspace symbols. Rename is refused, with the reason, when a name it
cannot bind may mean the binding: slot content in the scope, a component
rendered in the scope that uses the name, or a caller's content rendered at
an outlet in the scope.

## Alpine lifecycle boundaries

Alpine state lives in the DOM element that carries `x-data`. Two things
replace elements underneath it.

**HTMX swaps.** HTMX inserts new elements, and Alpine initializes them as
new components: their `x-data` runs again, so their state starts over. An
element that swaps itself (`hx-swap="outerHTML"`) loses the state of its own
`x-data` on every response, which `alpine-swap-resets-state` reports. An
`innerHTML` swap keeps the target's `x-data` and its state, and Alpine
initializes the new children, which see that state. To keep state across a
replacement, put it on an element outside the swap, or swap with the
`alpine-morph` extension (`hx-ext="alpine-morph"`), which the diagnostic
respects. Swapped-in content is evaluated in the scope it lands in, so a
fragment rendered from Go may read names its own component does not
declare, which is why the analyzer never reports them.

**KTML HMR.** `ktml dev` morphs the page by Render Identity instead of
replacing it. Surviving elements keep their Alpine state. A scope whose
bound markup changed is destroyed and initialized again, and its saved
state is restored when its `x-data` text is unchanged. A changed `x-data`
starts fresh, so editing an `x-data` literal, or a Go-built `x-data` whose
value changes, resets that component. Removed scopes are destroyed and new
ones initialized.

**Script context.** ADR-0001 classifies Alpine attributes as script
attributes: an interpolation in one must be `JSONValue`, `TrustedScript`,
or `boolean`, in an expression position. The analyzer reads those values
and never relaxes the rule.
