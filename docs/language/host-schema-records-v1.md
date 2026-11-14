# Host Schema Records, Imports, and Typed Paths v1

- Status: Frozen slice contract (Delivery Slice 1)
- Date: 2026-10-12
- Parent contract: [Host Schema, Render Clauses, and Keyed Repetition v1](host-schema-render-clauses-keyed-repetition-v1.md)
- Builds on: [Canonical Component Input Declarations v1](input-declarations-v1.md),
  [ADR-0002](../adr/0002-typed-render-selection.md),
  [ADR-0003](../adr/0003-rendering-api.md)

This document freezes the parts of the parent contract that Delivery Slice 1
needs: Host Schema records, explicit imports with aliases, scalar, optional, and
collection types, generated Go records, and typed dotted field paths. It records
the smaller decisions the slice made where the parent contract was silent.
Presentation States, Render Clauses, conditions, and Keyed Repetition remain
target behavior and are not implemented by this slice.

Its interpreter release-gate layer is done: `dev/interp` renders it with
byte, write, and error parity against `gogen`
([ADR-0004](../adr/0004-development-interpreter.md)). The slice stays
unadvertised until `ktml dev` renders live instances through the interpreter
(PLAN Immediate Backlog items 10 and 11).

## Documents

A `.ktmlschema` path is a **schema Document**; any other KTML path is a
**component Document**. Both are parsed by one concrete grammar. Which
declarations a Document may contain is a semantic rule, so the Go parser and
Tree-sitter agree on syntax for every fixture:

| Declaration | Component Document | Schema Document |
| --- | --- | --- |
| `import` from a `.ktml` path | components | `schema-imports-component` |
| `import` from a `.ktmlschema` path | records | records |
| `record` | `record-outside-schema` | yes |
| component declaration | yes | `component-in-schema` |

An import path without a `.ktml` or `.ktmlschema` suffix keeps the existing rule
and resolves to `.ktml`.

## Syntax

```ebnf
document           = { import_declaration | record_declaration | component } ;
import_declaration = "import", "{", [ import_name, { ",", import_name }, [","] ],
                     "}", "from", string_literal ;
import_name        = identifier, [ "as", identifier ] ;
record_declaration = "record", identifier, "[",
                     [ field_declaration, { ",", field_declaration }, [","] ], "]" ;
field_declaration  = identifier, ["?"], ":", type ;
type               = identifier | "[", type, "]" ;
input_declaration  = identifier, ["?"], ":", type, ["=", scalar_literal] ;
```

- `record` is a keyword only at the start of a top-level declaration. A
  top-level declaration that begins with `record` is always a record
  declaration, in either Document kind. Element names and paths may still be
  spelled `record`.
- `as` is a keyword only inside import braces.
- The concrete grammar accepts any identifier in name positions. The parent
  contract's shapes are semantic rules with exact ranges: record names must
  match `[A-Z][A-Za-z0-9]*` (`invalid-type-name`) and field names must match
  `[a-z][A-Za-z0-9]*` (`invalid-field-name`).
- `type` names a scalar (`string`, `boolean`, `number`, `integer`), a contextual
  type (`URL`, `TrustedURL`, `CSSValue`, `JSONValue`, `TrustedHTML`,
  `TrustedScript`), or a record in scope. A collection is `[T]`; whitespace
  inside the brackets is allowed and the canonical spelling has none.
- Input declarations accept the same `type` form. A default remains a scalar
  literal for a scalar type.
- Comments use the existing `//` line form. The formatter does not preserve
  comments in either Document kind.

## Syntax AST

All ranges are zero-based, half-open source byte offsets.

- `RecordDecl`: whole declaration, keyword, and name ranges, plus its fields in
  source order. Its `E` is the end of the closing `]`.
- `FieldDecl`: whole field, name, optional `?`, and type ranges; the canonical
  type spelling; and a structured `TypeExpr`.
- `TypeExpr`: `kind` is `named` (with `name` and the name range) or
  `collection` (with the element `TypeExpr`), plus the full type range.
- `AttributeDecl` (inputs) keeps `type` as the canonical type spelling and
  `type_range` as the full type range, and adds the structured `type_expr`.
- `ImportStmt` keeps `items` (source names, in order) and `path` (raw string
  spelling), and adds `path_range` and `specifiers`. Each specifier records
  the source `name` and its range, and the optional `alias` with its range.

## Semantic Model

### Symbols and scope

- Records are nominal. A record's identity is its Document path and name.
- Components and types have separate namespaces in each Document. Visible types
  are local records, plus imported records under their local name (the alias
  when one is given).
- A schema Document exports the records it declares. It does not re-export
  imported records.
- Duplicate local names in one namespace are errors: `duplicate-record` for two
  local records, and `import-collision` for an import that clashes with a local
  or earlier imported name.
- A scalar or contextual type name cannot be declared as a record
  (`reserved-type-name`).
- Contextual types are reserved but are not lowered by this slice. Using one is
  the fatal `unsupported-contextual-type` diagnostic until the trust slice
  defines their Go constructors.
- Component import aliases (`import { Card as Tile } from "./card.ktml"`) parse
  but are the fatal `unsupported-component-alias` diagnostic. Calls are lowered
  by component name, and direct generation does not yet have per-file package
  output for renamed calls.

### Types

| KTML | Kind | Go |
| --- | --- | --- |
| `string` | scalar | `string` |
| `boolean` | scalar | `bool` |
| `number` | scalar | `float64` |
| `integer` | scalar | `int64` |
| `R` | record | `R`, or `pkg.R` across Go packages |
| `[T]` | collection | `[]T` |
| `name?: T` (field or input) | optional | `*T` (including `*[]T`) |

- Type equality is structural over collections and nominal over records.
- `integer` defaults and call literals use the numeric literal token. The value
  must be a whole number with no fraction or exponent, and it must fit in a
  signed 64-bit integer. Otherwise the diagnostic is `default-type-mismatch` or
  `input-type-mismatch`.
- Defaults are allowed only for scalar types. A defaulted record or collection
  input is `default-type-mismatch`.
- Unknown type names are `unknown-type`.
- Record dependencies are acyclic. A field whose type reaches its own record,
  directly or through other records, collections, or optionals, is
  `recursive-record`. The diagnostic is reported at the field type range that
  closes the cycle.
- Two fields of one record that generate the same Go name are
  `generated-field-collision`. Duplicate source names are `duplicate-field`.

### Typed dotted paths

A quoted-interpolation path `{a.b.c}` is resolved from left to right:

1. The root must be an input of the enclosing component (`unknown-path-root`).
2. Every later part must name a field of the record type reached so far.
   - A part after a scalar is `scalar-field-access`.
   - A part after a collection is `collection-field-access`.
   - An unknown name in a record is `unknown-field`, at that part's range.
   - A part after an optional value (an optional input or optional field) is
     `optional-value-access`, at the optional part's range.
3. The resolved value has a type and an optionality.

Where a path is used:

- **Text and attribute interpolation** render `string`, `integer`, and `number`
  values. A boolean exact attribute value keeps its presence lowering
  (`true`/`false` text in `aria-*` attributes). An
  optional value is `optional-value-access`. A record or collection is
  `path-type-mismatch`. Boolean text is `boolean-text` (ADR-0001
  amendment of 2026-10-13).
- **Component input arguments.** An exact path (`item="{state.item}"`) must
  match the target input type (`input-type-mismatch`). As in the input
  contract, an optional value cannot be read before narrowing, even when the
  target input is optional (`optional-value-access`). Mixed literal and path
  arguments build a `string` value and follow the interpolation rule.
- Record and collection inputs accept only exact paths. There are no record or
  collection literals.

`untyped-field-path` is retired. Dotted paths are typed or rejected.

### Dependency graph

Imports are edges from the importing Document to the imported Document,
whatever their kinds. Workspace indexing discovers both Document kinds. Build
cache keys hash the source of every transitive dependency, so changing a schema
changes the key of every dependent Document. Development batches regenerate
every transitive importer of a changed schema in the same atomic batch.

## Generated Go

### Records

`foo.ktmlschema` generates `foo_ktmlschema.go` in the Go package of its
directory (ADR-0003). For each record, in source order:

```go
// SearchItem is generated from search.ktmlschema:1:8.
type SearchItem struct {
	Id    string
	Title string
	Score *int64
	Tags  []string
}
```

- Field names uppercase only the first ASCII letter (`id` becomes `Id`, and
  `displayName` becomes `DisplayName`). Fields keep source order.
- Records are plain exported value types with no methods. A required record
  field embeds the value, which acyclicity makes finite.
- Records from another directory are referenced through that directory's Go
  import path. The caller supplies the directory-to-package mapping. The CLI
  derives it from the nearest `go.mod` and the directory's existing package
  clause, falling back to the directory name.
- Each record and field has a source-map entry from its generated Go name
  (`SearchItem`, `SearchItem.Id`) to the declaration's name range. Generated-Go
  navigation in the LSP is deferred.

### Collisions

Every generated exported name in one Go package must be unique. Before any
output, generation fails on:

- two declarations in the same directory whose generated names meet: a
  component's `<Component>`, `<Component>Props`, and `Render<Component>`
  (after the exported spelling, so `Row` and `row` meet), a record name, and
  a state's generated names (`generated-name-collision`, which is also a
  semantic diagnostic for loaded Documents, reported at every declaration
  involved); and
- a record name that collides with a top-level declaration in a handwritten
  `.go` file of the package (CLI schema generation).

### Components

The direct generator (`gogen`) lowers record, collection, optional, and
`integer` inputs to the ADR-0003 props table, and lowers typed field paths to Go
selector expressions. `integer` text uses canonical base-10 output.

Generated files declare no package-level helpers. Writes and escaping go
through `github.com/karl-tye/ktml/rt`, and optional wrapping uses
`ktml.Ptr`. Several generated files can therefore share one package.

### gohtml

The experimental gohtml generator supports none of this slice. Before any
output, it fails on record, collection, or `integer` input types, on dotted
paths, on schema imports, and on import aliases.

## Formatting

The canonical form is:

```ktml
import { SearchItem, SearchState as State } from "./search.ktmlschema"

record SearchItem [
	id: string,
	note?: string,
	tags: [string],
]
```

- Records always use one field per line, with a trailing comma. An empty record
  is `record Name []`.
- Types use their canonical spelling.
- Imports keep specifier order and aliases.
- Formatting is idempotent, and parse/format/parse preserves the normalized
  conformance shape.

## Tooling

- The LSP serves diagnostics; completion (fields after `.` in paths, types in
  type positions, and exported names inside import braces); hover;
  definition; references; rename; and semantic tokens for records, fields,
  type references, import names, and path parts. These work across
  `.ktmlschema` and `.ktml`.
- Rename binds to symbols. Renaming a record updates its declaration, every
  type reference that uses its name, and import specifiers (the source name
  only, never an alias). Renaming a field updates its declaration and every
  path part that resolves to it.
- Every Host Schema change is classified as regenerate, rebuild, restart, and
  reload. It is never an HMR patch. Development `watch` patterns must include
  `*.ktmlschema` for schema edits to be observed.

## Conformance

Manifest cases may declare `records` and `imports`, as well as the existing
`declarations`, `interpolations`, and `segments`. Both parsers assert record
names, fields, optional markers, canonical type spelling, and every range. They
also assert import specifiers, aliases, and path ranges.

## Deferred

- Presentation States, Render Clauses, conditions, and narrowing (slice 2).
- Keyed Repetition (slice 3).
- Contextual types and their trusted constructors.
- Component import aliases.
- Per-file component generation across Go packages through the CLI. `gogen`
  has no CLI caller until ADR-0003 item 9.
- Generated-Go source-map navigation in the LSP, and handwritten-Go rename.
