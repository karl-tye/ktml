# Parser Conformance Corpus v1

`manifest.json` is the shared acceptance and recovery contract for the Go
compiler parser and Tree-sitter parser. Every case references a standalone KTML
component (`.ktml`), story (`.story.ktml`), or schema (`.ktmlschema`) source file; neither harness
embeds a second copy of the source.

Version 1 covers only concrete syntax both parsers accept today:

- imports, including aliases (`A as B`) and a trailing comma;
- Host Schema `record` declarations with required and optional fields;
- Host Schema `state` declarations with nullary and payload Variants;
- Component Signatures (bodyless, or with `default` and a body) and Render
  Clauses with plain or qualified Patterns and aliased payload bindings;
- bracketed component declarations;
- named types (scalars including `integer`, and record names), `[T]`
  collection types, and scalar defaults;
- nested elements, IDs, quoted text, whitespace, and line comments.
- quoted text and quoted attribute interpolation containing identifier paths.
- required, optional, and typed scalar-defaulted component inputs, including
  record- and collection-typed inputs.
- Keyed Repetitions (`for item in path key path { … }`) in element, `default`,
  Render Clause, and repetition bodies, with dotted paths and empty bodies.
- element lines: a newline ends an element's attribute list, so childless
  elements and component calls on separate lines are siblings; parenthesized
  continuation groups; and a body or text child on the header's line.
- `content` input declarations, content outlets, and `slot name { … }` fills.
- conditions (`if`, `else`, `else if`) with paths, presence `path?`, `!`,
  `&&`, `||`, and parentheses; comparisons, literals, and `!!` are invalid.
- framework attribute names (`@click.outside`, `:class`, `x-on:keydown.enter`,
  `hx-on::after-request`), `doctype html`, and `<!-- … -->` HTML comments;
  colon chains (`a:href`) and malformed names are invalid.
- raw-text literals (`` ``` … ``` ``), conditional attribute groups
  (`if c { name="v" } else { … }` inside a parenthesized list), attribute
  spreads (`...path`), attribute bag literals (`name=( … )`), and `*` in
  attribute names (`hx-target-4*`); an unterminated raw-text literal, a
  spaced spread (`... attrs`), and a comparison in a conditional group are
  invalid.
- story documents: `fixture name: Type = literal` (string, backtick string,
  number, or boolean) and `story Component.Name` with an optional
  `[input = value, …]` list (trailing comma allowed) and an optional body.
  Values are scalars, names (Variants, `None`, fixture references) with an
  optional same-line Variant payload `Name [field = value, …]`, record
  literals `[field = value, …]`, collections `[value, …]`, and `[]`, nested
  arbitrarily; a missing value is invalid.

`valid` is shared. Invalid cases additionally record each parser's outermost
recovery events as zero-based, half-open byte ranges. The Go contract records
the recovered top-level node count and separately verifies that the compiler
does not expose a recovered document as valid output.

Cases with a `declarations` field assert normalized component names and inputs,
including modes, type spellings, scalar default kinds/spellings, and all
declaration byte ranges, plus the signature markers `bodyless` and, for a
`default` signature, `defaultStart`/`defaultEnd`. Cases with a `records`
field assert record names, fields, optional markers, canonical type spellings, and every range. Cases with
an `imports` field assert raw path spellings and ranges, and each specifier's
name, optional alias, and ranges. Cases with a `states` field assert each state's
name, keyword, name, and whole ranges, and each Variant's name, payload marker,
payload range, and fields (shaped as record fields). Cases with a `clauses`
field assert each Render Clause's name, selector, Pattern (optional qualifier,
name, and full range), bindings (field and optional alias), and whole range
through the closing `}`. Cases with a `repetitions` field assert every Keyed
Repetition in pre-order across the whole document, nested repetitions after
their parent: the item name, the whole range (`for` through the closing `}`),
the `for`, item, `in`, and `key` ranges, and the collection and key paths
(`{ name, start, end, parts: [{ name, start, end }] }`). Type spellings are compared as the source
slice, so fixtures keep canonical types without inner whitespace.
Cases with a `stories` field assert every top-level `story` and `fixture`
declaration in source order with its `kind`, name, and whole, keyword, and name
ranges. A fixture adds its type spelling and range and its scalar `value`; a
story adds its component and component range, `argumentsStart`/`argumentsEnd`
and `bodyStart`/`bodyEnd` when written, and its `arguments`. Each argument or
field records its name, whole and name ranges, and value; each value records
its `kind` (`scalar`, `name`, `fields`, `list`, or `empty`) and range, plus the
scalar `literal` kind and `raw` spelling, the `name`, the Variant payload range
(`payloadStart`/`payloadEnd`), and nested `fields` or `items`. Empty argument,
field, and item lists are omitted. Cases with an `interpolations` field also assert the normalized path name, path
parts, and zero-based half-open byte ranges from the same fixture bytes in both
parsers. Cases with a `segments` field assert normalized quoted literal and path
segments, including their values and ranges. Cases with an `elements` field
assert every body element in pre-order across all body positions (top-level
declarations are not elements): its name and name range, its depth (the number
of enclosing elements), `multiline` for a continuation group, and each
attribute's name (`id` for `#name`) and whole range. Cases with a `conditions`
field assert every condition node in pre-order, an `else if` after its parent:
the whole range through the last `}`, the `if` and optional `else` keyword
ranges, `elseIf`, and the condition tree (`{ kind, start, end, path?,
operands? }` with kinds `path`, `presence`, `not`, `group`, `and`, and `or`;
`and`/`or` chains are flattened into n-ary operand lists, and groups are kept).
Cases with a `slots` field assert every slot fill in pre-order with its name and
its whole, `slot` keyword, and name ranges. Cases with a `markup` field assert
every doctype, HTML comment, and raw-text literal in pre-order with its `kind`
(`doctype`, `comment`, or `raw-text`), its whole range, and the doctype `name`
or the `text` between the delimiters. Cases with an `attributeConditions`
field assert every conditional attribute group in pre-order (elements in body
pre-order, nested groups after their parent): the whole range, the `if` and
optional `else` keyword ranges, `elseIf`, the condition tree as for
`conditions`, and the named attributes of the `then` and `else` branches.
Cases with a `forwarding` field assert every spread (`kind` `spread`, whole
range, and `path`) and attribute bag literal (`kind` `bag`, whole range,
attribute `name`, `bagStart`/`bagEnd` of its parentheses, and its named
`attributes`) in pre-order, bag contents after the bag. The corpus does not assert broader
AST/CST shape or semantic equivalence. Go workspace tests separately assert
interpolation input-root resolution. Attribute values, component-call
resolution, parenthesized parameters, and unions remain outside the shared
contract. Which declarations a document kind may contain is
semantic and is not asserted here.
