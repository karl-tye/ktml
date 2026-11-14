# Testing Conventions

Testing conventions for the KTML repository.

## Frameworks & Assertions
- Use standard `testing` package only.
- No external assertion libraries (e.g., testify, go-cmp). Use `if got != want` for assertions.
- Use `t.Helper()` for setup/teardown helpers.
- Use `t.Fatalf` for critical assertions (e.g., failed setup or parsing errors) and `t.Errorf` for non-critical assertions (allowing multiple failures within a loop/table).

## Structure
- Use table-driven tests (`tests := []struct{...}`) for scanners, parsers, formatters, and compilers.
- Use `t.Run(name, ...)` for nested/subtests.
- Name tests with BDD style (`GIVEN_WHEN_THEN` format) to verify complex behaviors, particularly inside `formatter_test.go`.

## Parser Conformance
- Shared source fixtures and validity/recovery metadata live in `/testdata/conformance`.
- Run `./test-conformance.sh` from the repository root to test the Go parser/compiler and Tree-sitter parser against the same fixtures.
- The script installs Tree-sitter dependencies with `npm ci` from the committed lockfile, regenerates the parser, runs the grammar corpus, and runs the Node conformance harness.
- Version 1 asserts shared concrete-syntax acceptance and recovery ranges for every fixture. Fixtures that declare `interpolations` additionally assert normalized path names, parts, and byte ranges in both parsers. Fixtures that declare `declarations` assert normalized required/optional/defaulted input shape, typed scalar defaults, bodyless/`default` signature markers, and byte ranges. Fixtures that declare `records` assert Host Schema record fields, optional markers, canonical types, and byte ranges; fixtures that declare `imports` assert import specifiers, aliases, and path ranges. Fixtures that declare `states` assert Presentation State names, Variants, payload markers, payload fields, and byte ranges; fixtures that declare `clauses` assert Render Clause names, selectors, Patterns with qualifiers, bindings with aliases, and byte ranges. Fixtures that declare `repetitions` assert every Keyed Repetition in pre-order through every body and nesting level: the whole range, the `for`, `in`, and `key` keyword ranges, the item name and range, and the collection and key path names, parts, and byte ranges. Fixtures that declare `elements` assert every body element in pre-order with its name range, depth, continuation-group marker, and attribute names and ranges, which proves that a newline ends an attribute list. Fixtures that declare `conditions` assert every condition node in pre-order with its `if`/`else` keyword ranges, `else if` marker, and normalized condition tree; fixtures that declare `slots` assert every slot fill's name and keyword, name, and whole ranges; fixtures that declare `markup` assert every doctype, HTML comment, and raw-text literal with its kind, range, and name or text; fixtures that declare `attributeConditions` assert every conditional attribute group with its keyword ranges, condition tree, and branch attributes; fixtures that declare `forwarding` assert every attribute spread and attribute bag literal with its ranges. Fixtures that declare `stories` assert every top-level story and fixture declaration with its component, name, keyword, argument-list, and body ranges, each argument's name and range, and each value's kind (`scalar`, `name`, `fields`, `list`, `empty`), Variant payload range, and nested fields or items. Broader semantic equivalence is not asserted; Go workspace tests cover typed input lowering, call validation, and interpolation input-root resolution.

## Mocking & Fakes
- Prefer real implementations or minimal in-memory structures over complex mocking frameworks.
- For runner control, execute actual platform-independent subprocesses (e.g., `sleep`, `sh`).

## End-to-End (E2E)
- E2E tests live in `/e2e-test`.
- Build the binary dynamically with `go build` inside the test environment.
- Run browser automation using `github.com/go-rod/rod` to verify client-side behavior like Hot Reload.
- Run the local dev server (`ktml dev`) and its backend on ephemeral ports chosen per test run; never assume or signal a fixed port such as 8080.
- Stop the dev server with an interrupt so it stops its own backend. Only terminate processes the test started, verified by PID and start time; never kill by port.
