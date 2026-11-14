# Canonical Component Input Declarations v1

This document freezes the input-declaration portion of the syntax AST and typed
semantic schema required by [ADR-0001](../adr/0001-language-boundary.md). It does
not freeze host nominal types, collections, content/slots, conditions, loops, or
general expressions.

[Host Schema Records, Imports, and Typed Paths v1](host-schema-records-v1.md)
extends input types with `integer`, records, and `[T]` collections, and makes
dotted field paths typed.

The remaining omitted areas are specified separately as target behavior by
[ADR-0002](../adr/0002-typed-render-selection.md) and
[Host Schema, Render Clauses, and Keyed Repetition v1](host-schema-render-clauses-keyed-repetition-v1.md).

## Syntax

```ebnf
input_declaration = identifier, ["?"], ":", scalar_type,
                    ["=", scalar_literal] ;
scalar_type       = "string" | "boolean" | "number" ;
scalar_literal    = string_literal | boolean_literal | number_literal ;
```

The accepted modes are:

| Source | Mode | Omission |
| --- | --- | --- |
| `name: T` | `required` | Compile error at a component instance. |
| `name?: T` | `optional` | Preserved as absent; value access requires narrowing. |
| `name: T = literal` | `defaulted` | The typed literal is substituted before rendering. |

`?` and `=` are mutually exclusive. Defaults are scalar literals only and must
have exactly the declared scalar type. In particular, identifiers, paths, calls,
and references to another input are not defaults. Empty string, `false`, and `0`
are present defaults, not absence.

Double-quoted default strings use the shared escape subset `\b`, `\f`, `\n`,
`\r`, `\t`, `\\`, `\"`, and `\u` followed by four hexadecimal digits. Backtick
defaults are single-line raw strings.

## Syntax Shape

Each syntax input records:

- declaration kind (`input` rather than prototype `override`);
- whole declaration, name, and type zero-based half-open byte ranges;
- mode;
- the `?` range for optional inputs;
- the `=` and scalar-literal ranges for defaulted inputs; and
- a scalar literal kind, source spelling, and typed value.

The shared conformance manifest's `declarations` field is the normalized shape
implemented by both the Go parser and Tree-sitter. Invalid exclusivity and
non-scalar-default fixtures record parser-specific recovery ranges.

## Semantic Shape

The compiler `Input` records the same mode and source ranges after lowering.
Its optional `Default` pointer separates no default from typed zero values. Calls
must supply required inputs, may omit optional/defaulted inputs, and cannot read
an optional input as `T` before a future condition slice provides narrowing.

KTML input names must map one-to-one to generated Go field names. A declaration
such as `[first-name: string, firstName: string]` is invalid because both names
normalize to `FirstName`; the diagnostic points at the later KTML name and names
the earlier one.

## Generator Contract

The direct Go generator emits required fields as `T` and optional/defaulted
fields as `*T`. Nil means absent; a non-nil pointer preserves an explicit zero
value. Defaulted values are selected at the render boundary and supplied values
are pointer-wrapped at generated component-instance boundaries.

The experimental gohtml generator supports required scalar inputs only. It emits
a fatal `unsupported input mode` error before output or auxiliary writes for
optional/defaulted inputs. This avoids a second absence/default semantics in
`html/template`.

## Override Compatibility

Existing declarations such as `Button [disabled="true"]` remain an explicitly
tagged prototype path for the current gohtml application fixture. They are not
input declarations, do not enter the canonical component input schema, and are
not advertised as accepted language. The direct Go generator rejects them
fatally. This isolated path exists only until conditions and migration diagnostics
can replace it; no canonical mode is inferred from its value spelling.
