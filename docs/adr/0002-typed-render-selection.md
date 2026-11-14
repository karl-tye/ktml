# ADR-0002: Typed Render Selection

- Status: Accepted
- Date: 2026-09-10
- Deciders: KTML maintainers
- Scope: Host data, component state selection, repetition, and the Go boundary
- Amends: [ADR-0001](0001-language-boundary.md)

## Context

ADR-0001 kept business work in Go but left nominal host types, state selection,
and collections open. A production templ application demonstrates that simple scalar conditions are
not enough: payment, analysis, attachment, and invalid-form rendering have
mutually exclusive shapes with state-specific data. It also has many nested
lists whose browser identity must survive insertion, removal, and reordering.

The design must remain a small SSR rendering language. It must not recreate Go
expressions, a general pattern language, custom iterators, or framework dialects.

## Decision

KTML will use an authoritative language-neutral **Host Schema** in dedicated
`.ktmlschema` documents. Host Schemas declare nominal records and closed
**Presentation State** types. Generated Go packages expose record values and
sealed state interfaces with private variants, typed constructors,
discriminators, and payload accessors. Page- or fragment-level presenters perform
**Business Derivation** from application data into those generated values.

A component has one public **Component Signature** and may have contiguous,
same-document **Render Clauses** selected by one finite signature input. Clauses
are order-independent and non-overlapping. A bodyless signature requires
exhaustive clauses. A residual body is permitted only with the explicit
`default` marker; Variant, boolean, or option cases handled only by that body
produce a warning.

Dynamic repetition has one blessed form: a collection path, one immutable item
binding, and a mandatory string or signed-integer key path rooted in that item.
The key defines **Render Identity**, not HTML output. Generated renderers
preflight sibling uniqueness before streaming a second pass. Development output
uses keyed fragment boundaries; production output contains no identity markers.

The target grammar and full semantic rules are specified in
[Host Schema, Render Clauses, and Keyed Repetition v1](../language/host-schema-render-clauses-keyed-repetition-v1.md).
These are accepted target semantics, not a description of current implementation.

## Boundary

Go owns request and service access, validation, permissions, state selection,
comparison, formatting, grouping, sorting, filtering, URL construction, and
trusted-value construction. KTML may read typed paths, select Render Clauses,
evaluate boolean/presence conditions, compose components and slots, repeat
prepared collections, and render contextual output.

Library plugins may add schemas, output-context classification, routes,
completion, diagnostics, and embedded-language analysis through generic core
contracts. They cannot add syntax, control flow, types, render transforms, or
change rendered bytes. Official HTMX, Alpine.js, and Tailwind integrations use
the same contracts as third-party plugins and are enabled through a pinned
project manifest.

## Consequences

- Host applications gain impossible-state protection for state-specific data without a
  general discriminated-union or pattern-matching language.
- A second authored schema language and generated presenter types add migration
  glue. The payment lifecycle proof must demonstrate that this cost buys useful
  state safety before Host Schema use expands.
- A keyed loop performs an O(n) uniqueness preflight and allocates one key set.
  Benchmarks against equivalent templ and handwritten Go are a release gate.
- Host Schema changes regenerate Go, restart the application, and reload the
  browser. Valid clause/body changes with unchanged signatures and schemas may
  use HMR.
- ADR-0001's scalar comparison operators are removed from the target condition
  language. Conditions are limited to boolean paths, optional presence, `!`,
  `&&`, `||`, and parentheses.
- ADR-0001's rejection of prototype declaration overrides remains. Render
  Clauses are a canonical typed construct with one shared signature, finite
  patterns, exhaustiveness, and backend-independent semantics; they do not
  preserve gohtml override behavior.

## Rejected

The following are explicit non-goals and require a new ADR to reconsider:
guards, wildcards, scalar literal patterns, multiple selectors, open states,
anonymous/general unions, nested patterns, cross-file clauses, recursive schemas
or rendering, component-valued data inputs, arbitrary calls or methods, hidden
request context, unkeyed loops, loop indices or `else`, collection transforms,
composite key syntax, and plugin-defined language dialects.

## Design Check

This decision was tested against Odin's stated goals of simplicity,
readability, one idiomatic way, orthogonality, visible cost, and data
transformation. The design deliberately blesses the common SSR cases instead of
generalizing them. The explicit `default` marker was chosen during that review
to avoid hidden partial matching. The Host Schema and loop allocation costs
remain empirical proof gates rather than assumed successes.

References:

- [Odin guiding principles](https://odin-lang.org/docs/faq/#what-are-the-guiding-principles-behind-the-design-of-odin)
- [Pragmatism in Programming Proverbs](https://www.gingerbill.org/article/2020/05/31/programming-pragmatist-proverbs/)
- [Blessed Syntax and Ergonomics](https://www.gingerbill.org/article/2026/04/29/blessed-syntax-and-ergonomics/)
- [Why People are Angry over Go 1.23 Iterators](https://www.gingerbill.org/article/2024/06/17/go-iterator-design/)
- [The Metaprogramming Dilemma](https://www.gingerbill.org/article/2016/12/01/the-metaprogramming-dilemma/)
