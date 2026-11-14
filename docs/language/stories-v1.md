# Stories and Fixtures v1

- Status: Frozen slice contract (Phase 5, Component Explorer)
- Date: 2026-10-13
- Decision: the user chose option A (top-level `story` and `fixture`
  declarations in story Documents) on 2026-10-13
- Amended 2026-10-22: a story gives an attribute bag as the empty bag `[]`
- Builds on: [Canonical Component Input Declarations v1](input-declarations-v1.md),
  [Host Schema Records v1](host-schema-records-v1.md),
  [Presentation States and Render Clauses v1](host-schema-render-clauses-v1.md),
  [Element Lines, Literal Attributes, Content Inputs, and Conditions v1](language-gaps-v1.md),
  [ADR-0001](../adr/0001-language-boundary.md),
  [ADR-0004](../adr/0004-development-interpreter.md)

A **story** is a named, type-checked set of input values for one component.
Stories live in colocated `.story.ktml` files and drive the
[Component Explorer](../explorer/explorer-v1.md). They are development
fixtures: no Go is generated for them, they are never linked into an
application, and `ktml dev` never renders them. Tools can write them
([below](#generated-and-captured-stories)).

## Documents

A path that ends in `.story.ktml` is a **story Document**. It is a KTML
Document for indexing and tooling, with its own declaration table:

| Declaration | Component Document | Schema Document | Story Document |
| --- | --- | --- | --- |
| `import` (components and types) | yes | types | yes |
| `record`, `state` | `record-outside-schema`, `state-outside-schema` | yes | same as component |
| component, Render Clause | yes | `component-in-schema` | `component-in-story` |
| `fixture`, `story` | `fixture-outside-story-document`, `story-outside-story-document` | same | yes |

Placement is semantic, so the Go parser and Tree-sitter parse `story` and
`fixture` in every Document. A story Document sits next to the component it
exercises (`internal/helpdesk/pipeline.story.ktml` beside
`components.ktml`) and imports it like any other Document.

`ktml generate` skips story Documents, including in `dir/...` patterns, and
`ktml dev` ignores their edits.

## Syntax

```ebnf
fixture_declaration = "fixture", identifier, ":", type, "=", scalar_literal ;
story_declaration   = "story", identifier, ".", identifier,
                      [ "[", [ value_argument, { ",", value_argument }, [","] ], "]" ],
                      [ body ] ;
value_argument      = identifier, "=", value ;
value               = scalar_literal
                    | identifier, [ payload ]
                    | "[", [ value_argument, { ",", value_argument }, [","] ], "]"
                    | "[", [ value, { ",", value }, [","] ], "]" ;
payload             = "[", [ value_argument, { ",", value_argument }, [","] ], "]" ;
```

- `story` and `fixture` are keywords only at the start of a top-level
  declaration, when a name follows them. Elements, attributes, and fields may
  still be named `story` or `fixture`.
- The header names the component and the story: `story Pipeline.Running`.
  The component must be imported by the Document (`unknown-component`). The
  story name is UpperCamel (`invalid-story-name`).
- `=` gives a value, as it gives a default (`count: integer = 3`) and selects
  a Variant in a Render Clause (`progress = Running`). Inside brackets a
  record literal's first entry is `name =`; otherwise the bracket is a
  collection. `[]` is either, by context.
- A Variant is its name; a payload follows the name on the same line,
  `Running [callId = "c-1"]`. `None` is the absent value of an optional.
  `true` and `false` are booleans. Any other lowercase name is a fixture.
- The body is the story's content: unlabelled nodes fill `children`, and
  `slot name { … }` fills a named slot, exactly as in a call body. It has no
  inputs in scope, so a path in it is `unknown-path-root`.
- There are no negative number literals, as elsewhere in KTML; controls can
  set negative numbers.

```ktml
import { Pipeline } from "./components.ktml"
import { Badge } from "../ui/form.ktml"

fixture report: TrustedURL = "/reports/42"
fixture criterion: JSONValue = `{"id": "greeting"}`

story Pipeline.Running [
	progress = Running [callId = "call-42"],
	stages = [recording = Done, transcription = Done, scoring = Active, complete = Waiting],
]

story Badge.WithIcon [
	view = [classBgColorTextColorBorderColor = "border-red text-red", text = "Overdue"],
] {
	slot icon {
		i class="fas fa-clock"
	}
}
```

## AST

`ast.FixtureDecl` (keyword, name, and type ranges, the structured
`TypeExpr`, and the scalar `Value`) and `ast.StoryDecl` (keyword,
component, and name ranges; `Arguments`, with `ArgumentsRange` set when a
bracket was written; `Body`, with `BodyRange` set when a body was written).
`ast.ValueArgument` is `name = value`; `ast.ValueExpr` has a `Kind` of
`scalar`, `fields` (record literal), `list`, `empty`, or `name` (with an
optional payload). All ranges are zero-based, half-open source bytes.

## Semantics

### Fixtures

A fixture gives a **contextual value**: `URL`, `TrustedURL`, `TrustedScript`,
`JSONValue`, or `CSSValue` (`fixture-type` otherwise; attribute bags are not
fixtures). Its value is a string literal (`fixture-value`); a `JSONValue`
fixture must be valid JSON (`invalid-json-fixture`). Names are lowerCamel
(`invalid-fixture-name`) and unique in the Document (`duplicate-fixture`).
Fixtures are local to their story Document.

**Trusted values come only from fixtures.** A string literal for a
`TrustedURL`, `TrustedScript`, `JSONValue`, or `CSSValue` input or field is
`trusted-value-literal`, and the Explorer offers fixtures, never a text
field, for them. `URL` is policy-checked when it renders, so a story may also
write it as a string.

### Stories

Each story is checked against its component's typed inputs:

- Every **required** input must be given (`missing-input`). An omitted
  **optional** input is `None`; an omitted **defaulted** input takes the
  component's default.
- An unknown input is `unknown-input`, a repeated one `duplicate-input`. A
  content input is filled in the body, not given a value
  (`content-as-story-input`).
- An attribute bag (an `Attributes` input or field) is given as the empty
  bag, `attrs = []`, its only value; any other value is
  `unsupported-story-input`. A spread of it writes nothing.
- Values type against the input type: scalar literals by kind, with
  `integer` a whole signed 64-bit number; records by their fields, with every
  required field given (`missing-field`, `unknown-field`, `duplicate-field`)
  and omitted optional fields `None`; collections by their element type;
  states by a Variant of the state (`unknown-variant`), with the Variant's
  payload fields given like a record's (`unexpected-payload` for a Variant
  without one); fixtures by their declared type; attribute bags by `[]`. Anything else is
  `story-type-mismatch`.
- The body is validated as a call body of the component: `missing-content`,
  `unknown-slot`, `duplicate-slot`, `unexpected-children`, and the markup
  rules apply.
- A story name is unique per component in its Document (`duplicate-story`).
- The **first story of a component** in the workspace (by Document path, then
  position) is its **default story**, which the Explorer opens first.

Story headers are `story` references of their component, so definition,
references, rename, and reference counts include them. Fixture uses are
`story` references of the fixture.

### Defaults and controls

Stories declare values only; controls are derived from the component's input
types by the Explorer, with the story's values as their initial state. A
viewer's control changes override only the inputs they changed. Every other
input keeps following the story Document, so editing the story still updates
an open preview. See the [Explorer](../explorer/explorer-v1.md#controls) for
the control of each input kind.

## Rendering

The development interpreter renders a story (`interp.Program.RenderStory`)
by building, from the typed story values, the same reflect shapes it reads
from generated Hybrid H values (ADR-0004 decision 2): records are structs of
their Go field names, optional values pointers, collections slices, contextual
values the `ktml` types built with their trusted constructors, and states a
development-only value read through the same `Variant()`/payload rules. The
body is compiled like a call's content fills. A story is a registry root: the
HMR swap that patches application pages patches story previews, after a story
or component edit, through the same path.

## Tooling

- Formatter: one input per line, values inline, `story` and `fixture` keep
  their order.
- LSP: diagnostics with ranges; completion of components after `story`, of
  the component's remaining inputs and fields in value lists, and of Variants,
  fixtures of the value's type, `None`, booleans, and `[]` for an attribute
  bag at values (from the project index while the story being typed does not
  parse); stories and fixtures as document and workspace symbols; definition
  from the header. The language server loads story Documents lazily
  (`compiler.Workspace.DeferStories`): one joins its index when opened, and a
  rename loads them all, so references and workspace symbols list the
  stories of open story Documents only.
- Workspace index: `Snapshot.Stories`, `StoriesIn`, `Story`, and `Fixtures`.
- `ktml classes` and the Tailwind adapter read classes in story bodies.

## Generated and captured stories

Two tools write stories; both keep every story a person writes or edits.

- **Markers.** A tool writes each import, fixture, and story it owns under a
  `// ktml:generated <hash>` (or `// ktml:captured <hash>`) line, whose hash
  covers the block's text up to the end of its declaration. On a rerun an
  intact block is rewritten or, for a generated story the tool no longer
  writes, removed; a block whose text no longer matches is edited and kept;
  a story without a marker is the author's, and a tool never writes a story
  under the same name. Imports and fixtures are never removed, since a
  hand-written story may use them. A Document that does not parse is left
  alone. The output is deterministic. `ktml fmt` drops comments, so it turns
  marked stories into hand-written ones.
- **`ktml stories generate`** ([CLI](../../cli-spec.md)) writes `x.story.ktml`
  beside each component Document `x.ktml` with a component the application
  renders: reachable from a Chi route through its handler's Go code and the
  KTML calls below. Each component's first story names the routes that reach
  it. Values come from the input types: one story per Variant of each state
  input the component selects Render Clauses on (`Pipeline.Running`), or
  `Default` without one, then `Empty` (its collections empty), `NoOptionals`
  (its optional values `None`), and `LongText` (long text, for small stories),
  each when it differs from the others. A page, which a route's handler
  renders, gets only its `Default` or Variant stories, since its view holds
  the views of components with stories of their own. `Empty` and `NoOptionals` change the
  inputs and one record field below them; a state, collection, or optional
  deeper in a view belongs to the nested component that renders it, whose own
  stories vary it. Scalars are chosen from field names (names, emails, phone
  numbers, postcodes, dates, money, counts, statuses, URLs; classes stay
  empty) under a fixed seed. A synthesized URL is a placeholder naming the
  component and field, `/_story/<component>/<field>` in kebab case (for
  example `/_story/messaging-history/history-url`), with `/<n>` for a
  collection's nth item; the Explorer reserves `/_story/` and answers it
  with 204, so a placeholder never loads a page into a preview (route
  mocks remain a later release). A populated collection varies every scalar by
  item, so Keyed Repetition keys are unique, and cycles through the Variants
  of states its items hold; nested collections hold one item. Trusted values
  use placeholder fixtures (`placeholderURL: TrustedURL = "/_story/trusted-url"`,
  `` placeholderJSON: JSONValue = `{}` ``, `placeholderScript`, `placeholderCSS`)
  and attribute bags the empty bag `[]`. Nothing is written unless every
  generated story type-checks with no diagnostic and renders.
- **`ktml dev --capture-stories <dir>`** writes a story for each new shape of
  props (the Variants selected, the optional values given, the collections
  empty or not) that the application renders as a page root, read back from
  the HMR registry's typed props (`interp.Program.Capture`) in the app
  process, into `<dir>` with the component tree mirrored
  (`<dir>/internal/web/layout.story.ktml`), as `Component.Captured<Variants>`.
  Content is a placeholder, collections keep at most 10 items, trusted
  values become fixtures, and attribute bags the empty bag. The props never leave the development process
  except into those files.

### Capture safety

- **Consent twice.** The project allows capture in `ktml.config.json`
  (`"dev": {"capture": {"allow": true}}`); without it `ktml dev` exits 1. The
  application declares that it reads local development data, in its ktmldev
  wiring: `hmr.Install(handler, hmr.LocalData(local))`; without it the app
  refuses, and `ktml dev` prints the reason. Capture also needs a loopback
  `dev.proxy_target` and a directory inside the project, outside `.git` and
  `.ktml`.
- **Redaction by default.** Strings are redacted by field name and by what
  they hold: names, emails, phone numbers, postcodes, addresses, identifiers,
  tokens and secrets, URLs (hosts become `example.com`, identifying path
  segments fakes, queries dropped), and free text are replaced; classes,
  icons, statuses, counts, amounts, and dates are kept unless their text
  looks like personal data. Integer identifiers, JSON values (by key),
  scripts with literals, and CSS with `url(` are redacted too. A fake is
  chosen by an HMAC of the value under the user's key
  (`ktml/capture.key` in the user's configuration directory, such as
  `~/.config/ktml/capture.key`, created 0600 on first use, outside every
  project), so the same value always gets the same fake and the fake
  reveals nothing without the key.
- **`--capture-raw`** keeps raw values, for local use only, and prints a
  warning; the stories say `raw values`.
- **Production.** Capture lives in the ktmldev-only `dev/hmr` and
  `internal/storygen`; the release gate checks that a production binary links
  neither and holds no capture string.

## Release gates

| Layer | Status |
| --- | --- |
| Canonical contract (this document) | done |
| Go parser and AST | done |
| Tree-sitter grammar, corpus, queries, and the shared `stories` conformance assertion | done (a top-level element or component named `story`/`fixture` not followed by a name parses only in Go, as for `record`/`state`) |
| Formatter | done |
| Semantic model and workspace index | done |
| Interpreter | done (`RenderStory`) |
| Direct Go generator | not applicable: stories are never generated |
| CLI | done (`ktml explore`; `ktml generate` skips) |
| LSP | done (diagnostics, completion, symbols, definition) |
| Explorer | done |
| HMR | done (story roots, story and component swaps) |
| Story generation and capture | done (`ktml stories generate`, `ktml dev --capture-stories`) |
