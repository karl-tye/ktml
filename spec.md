# KTML - Core Language Specification

> **Prototype document:** [ADR-0001](docs/adr/0001-language-boundary.md) is the
> accepted Phase 0 language boundary and is normative where this document
> conflicts with it. This document is retained as prototype syntax and product
> context until the canonical grammar and semantic specification replace it.

## 1. Introduction

KTML is an ergonomic HTML templating language designed for modern SSR web development. It stays close to HTML primitives while providing clean syntax for component encapsulation, while allowing for simple extensions to introduce custom rules to the language.

## 2. Elements

### Canonical component input declaration slice

Required `name: T`, optional `name?: T`, and defaulted
`name: T = scalar-literal` inputs follow
[Canonical Component Input Declarations v1](docs/language/input-declarations-v1.md).
That document supersedes this prototype's string-valued declaration and override
inference. Legacy overrides remain an explicitly tagged gohtml-only prototype
path; they are not defaults or canonical component inputs.

### Canonical quoted interpolation slice

Quoted text and quoted attribute values are syntax values composed of ordered
literal and path segments. A path has one or more identifier parts separated by
`.`; its expression range includes `{` and `}`, and every identifier part has
its own zero-based, half-open byte range. For example, `"Hi {user.Name}"`
normalizes to literal `Hi ` followed by path `user.Name`.

Interpolation is recognized in double-quoted values and starts with `{`
immediately followed by an identifier; backtick values remain literal. A paired
brace group whose opening brace is followed by horizontal whitespace remains
literal content so embedded object values such as `{ filtersOpen: false }`
retain their existing meaning. Only paths are valid inside recognized interpolation. Empty
paths, missing braces, empty path parts, unmatched braces, and call-like forms
such as `{title()}` are fatal `invalid-interpolation` diagnostics. The semantic
analyzer resolves the root part against the enclosing component's declared
inputs and reports
`unknown-path-root` on the root identifier. Dotted paths are parsed and
root-resolved, but field typing remains deferred until host view-model schemas
exist; direct Go generation therefore does not yet accept dotted paths.

The Go syntax model represents this slice as `QuotedValue` containing
`ValueSegment` entries of kind `literal` or `path`, with `PathExpression` and
`PathPart` ranges. `TextNode.value` and `AttributeNode.value` remain in the
plugin/WASM JSON payload because the current formatter and plugin process still
consume the original quoted spelling; generators use the structural value for
parser-produced nodes.

Elements are defined at the root of a document like so:

```ktml
Button {
  button
    "Press me!"
}
```

HTML strings require to have quotes wrapped around them. The above component maps to:

```html
<button>Press me!</button>
```

Backticks (\`) can be used to create strings that allow for the " character to not be escaped.

Element attributes and tags may be easily nested with the `:` operator:

```ktml
Row [data:string] {
  tr:td:data
}
```

when rendered with 
```ktml
...
  Row:data="1234.5678"
```
maps to:

```html
<tr><td>1234.5678</td></tr>
```

### 2.1. Attributes

Attributes can be defined alongside elements:

```ktml
Button [label: string] ...
```

Attributes are passed to elements with the following syntax:

```ktml
Button [label: string] {
  button
    disabled
    style    "padding: 2rem"
    label
}
```

Note that any number of spaces can be used on the same line.

Rendering the above element with the code

```ktml
...
  Button
    label "Learn more"
```

maps to the following HTML:

```html
<button disabled style="padding: 2rem">Learn more</button>
```

The `=` syntax can also be used alongside spaces if it creates a more readable element:

```ktml
a:href="/home":Back
```

#### 2.1.1 Children

Elements can use children to refer to anything passed underneath an element. For example:

```ktml
Page {
  div:style="margin-x: auto; max-width: 1280px"
    children
}
```

when rendered with 

```ktml
...
  Page:a:href="/signup":"Sign up!"
```

maps to the following HTML:

```html
<div style="margin-x: auto; max-width: 1280px">
  <a href="/signup">
    Sign up!
  </a>
</div>
```

#### 2.1.2 Types

The native types available for attributes are: `boolean`, `string`, `number`, and constants of any of these types. Elements can also be used as types to signify that a particular element is required.

The implicit attribute `children` can be further scoped to a type if required.

Types can be unioned with the `|` operator.


```ktml
User [name:string, img: string] { ... }

Profile [user: User] { ... }

// long-hand
...
  Profile
    user=
      User:name="John Doe":img="/john.png"

// short-hand
...
   Profile:user=User:name="John Doe":img="/john.png"
```

### 2.1.3 Element Injection

When an element is passed as an attribute, the variable can be declared as a standard node within the component to inject additional attributes into it.

```ktml
MyDialog [trigger: button] {
  div.dialog
    trigger
      aria-expanded="true"
      class="trigger-btn"
}
```

The compiler intercepts the `trigger` reference and merges the new attributes into the passed AST node.

### 2.2. Identifiers

Unique identifiers can be assigned to elements within defined elements by using a `#`:

```ktml
TextInput {
  label#text-input-label
    "My Input"
    input
      type="text"
}
```

Each time the `TextInput` element is rendered, the `label` HTML is given a unique ID.
This id can be referenced within a scope of an element the same way as custom attributes.

Furthermore, identifiers defined with `#` automatically become exposed as virtual attributes to the caller. This allows external modification of nested elements:

```ktml
...
  TextInput
    text-input-label:class="text-red"
```

The compiler maps the external `text-input-label` reference to the internal AST node. Because virtual attributes share the same API namespace as standard attributes, it is a compiler error to define an attribute `[label: string]` if an identifier `#label` is also defined within the same element. Elements can also be given standard identifiers by setting the `id` attribute.

### 2.3. Overrides

Elements can define their own overrides within a package (see Packages) to allow for the
ease of clearly representing separate states:

```ktml
Button [label: string] {
  button
    style="background-color: blue"
    label
}

Button [disabled="true"] {
  button
    style="background-color: gray"
    disabled
    label
}
```

This will change the rendered HTML depending on what is passed to the `Button` element's
`disabled` attribute. Notice that the defined `disabled` attribute cannot be referenced as
a variable but the `label` attribute can.

Overrides require a base case to define all possible attributes and their types. These also
can only be defined in the same package as the base case.

### 2.4. Attribute Modifiers

When attributes are injected into an existing element (either internally via Element Injection or externally via Identifiers), the compiler must resolve collisions if the attribute already exists on the target node. KTML uses explicit modifier operators for this:

*   **Overwrite Operator (`=`)**: Completely replaces the internal attribute.
*   **Append Operator (`+=`)**: Concatenates the internal attribute with the new value, separated by a single space (`" "`). If the attribute does not exist on the internal node, it acts identically to `=`.

```ktml
TextInput
  // Appends space + "text-red" to any existing classes
  text-input-label:class+="text-red" 
  
  // Completely overwrites any existing aria-hidden value
  text-input-label:aria-hidden="true"
```

*Note: Because `+=` strictly uses a space delimiter, inline styles require explicit semicolons within the string (e.g., `style+="; color: red"`).*

## 3. Packages

KTML elements are scoped to directories by default. To reference elements that are located
in KTML files in another directory, they can be referenced by the `import` statement.

```ktml
// ./ktml/ui/button.ktml
Button { ... }

// ./ktml/pages/index.ktml
import { Button } from "../ui"
```

KTML configuration can allow for aliasing of directories so that relative locations
need not be used:

```json
// ./ktml.config.json
{
   ...
   package-dir: "./ktml"
}
```

```ktml
// ./ktml/pages/index.ktml
import { Button } from "ui"
```
