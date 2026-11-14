# Core Language

The KTML templating language: an ergonomic HTML templating language for server-side-rendered web development. It stays close to HTML primitives while providing clean syntax for component encapsulation.

The accepted language boundary is [ADR-0001](../../adr/0001-language-boundary.md),
as amended by [ADR-0002](../../adr/0002-typed-render-selection.md).
Terms below describe the prototype domain model; where syntax or semantics
conflict, the ADRs and canonical language contracts are normative.

## Language

**Document**:
A single `.ktml` source file. Contains Element Declarations and Import Statements at the top level.
_Avoid_: File, module, template

**Element**:
Any HTML-like node — either a built-in HTML element (`div`, `button`, `span`) or a user-defined Component. This is the universal term for both built-in and custom nodes.
_Avoid_: Tag, node, widget

**Component**:
A named, user-defined Element with one Component Signature and one or more ways to render it.
_Avoid_: Element definition, macro, partial, widget

**Component Signature**:
The single public contract of a Component, containing its typed value inputs and content slots. Every Render Clause of that Component shares it.
_Avoid_: Props interface, overload signature, clause signature

**Render Clause**:
A Component body selected by matching one Pattern against the Component Signature's finite selector. Render Clauses are declarative and do not have source-order priority.
_Avoid_: Override, overload, case body

**Pattern**:
A finite structural selector for one Variant, boolean value, or optional presence case. A Pattern does not evaluate arbitrary expressions or guards.
_Avoid_: Condition, predicate, guard

**Host Schema**:
The language-neutral contract for nominal records, Presentation States, collections, and contextual values that a host application may expose to KTML.
_Avoid_: Go schema, view-model reflection, database schema

**Presentation State**:
A closed nominal set of mutually exclusive Variants prepared by the host application for rendering different data or markup shapes.
_Avoid_: Domain status, UI flag collection, open union

**Variant**:
One named case of a Presentation State, optionally carrying a typed record payload.
_Avoid_: Override, subclass, mode string

**Business Derivation**:
The host application's transformation of domain and request data into Host Schema values and semantic rendering facts.
_Avoid_: Template helper, view logic call, implicit context

**Element Instance**:
A usage of an Element within a body, with concrete attribute values. Can be a built-in element (`button disabled "Click"`) or a Component instance (`Button label="Click"`).
_Avoid_: Element node, element reference, invocation, call

**Attribute**:
A named value bound to an Element Instance. Written as a key-value pair: `style "padding: 2rem"`, `disabled` (valueless), or `href="/home"`. HTML attributes map directly to their HTML equivalents. Component attributes become template variables.
_Avoid_: Prop, parameter, option

**Input Declaration**:
A formal input of a Component. It has one explicit mode: required `name: T`, optional `name?: T`, or defaulted `name: T = scalar-literal`. Optional absence and default omission are distinct from zero values. The frozen scalar contract is documented in [Canonical Component Input Declarations v1](../../language/input-declarations-v1.md).
_Avoid_: Prop type, parameter declaration, argument

**Type**:
The expected kind of a value. Core types are scalars, contextual values, Host Schema records and Presentation States, collections, options, and content slots. Component-valued data and anonymous unions are not Types.
_Avoid_: Data type, prop type

**Children**:
The unlabelled content passed beneath an Element Instance and rendered through a `children` outlet. Ordinary Components may infer it from that outlet; Render Clause groups declare it explicitly in their shared Component Signature.
_Avoid_: Slot, inner content, body content

**Keyed Repetition**:
A structural mapping from a prepared collection to repeated render fragments, where each sibling item has an explicit stable key.
_Avoid_: General loop, iterator, map operation

**Render Identity**:
The stable development identity of rendered component and repetition fragments across source edits and collection reordering.
_Avoid_: DOM ID, database ID, HTML key attribute

**Identifier**:
A `#`-prefixed label on an Element Instance within a Component body (e.g., `label#text-input-label`). Causes the rendered HTML element to receive a unique DOM ID on each render. Identifiers are also exposed as Virtual Attributes to callers.
_Avoid_: ID, anchor, fragment identifier

**Virtual Attribute**:
An attribute on a Component Instance that targets a named Identifier inside the Component. `TextInput text-input-label:class="text-red"` sets the `class` attribute on the `#text-input-label` element within `TextInput`. Shares the same namespace as declared Attributes — a Component cannot have both a declared attribute `[label: string]` and an identifier `#label`.
_Avoid_: External attribute, forwarded attribute, prop passthrough

**Element Injection**:
When an Element Instance is passed as an Attribute value, the receiving Component can declare that attribute as a node in its body. The compiler merges any additional attributes from the injection site into the passed Element Instance.
_Avoid_: Slot injection, child injection, element passthrough

**Override (prototype only)**:
A legacy gohtml-only declaration specialization tagged separately from canonical input declarations. It is not a Render Clause or Variant and is rejected by direct Go generation.
_Avoid_: Render Clause, overload, specialization

**Attribute Modifier**:
An operator controlling how injected attributes merge with existing ones on a target Element Instance:
- `=` (overwrite): completely replaces the existing attribute value
- `+=` (append): concatenates with a space delimiter; acts as `=` if the attribute doesn't exist
_Avoid_: Merge operator, attribute strategy

**Package**:
A directory of `.ktml` files that can be imported from other files. The root Package Directory is configured via `package_dir` in `ktml.config.json`.
_Avoid_: Module, library, namespace

**Import**:
An `import { X } from "path"` statement at the top of a Document that makes Components from another file or Package available locally. Paths are relative to the importing file unless aliased by the Package Directory configuration.
_Avoid_: Include, require, use
