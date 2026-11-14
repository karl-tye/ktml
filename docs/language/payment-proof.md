# Payment Lifecycle Proof

- Status: Gate result for Delivery Slice 2 (PLAN "Host Schema and Business
  Derivation"; Mandatory Release Gate "the payment proof")
- Date: 2026-10-12
- Fixture: `internal/generator/gogen/testdata/payment`
- Baseline: an equivalent templ/Go implementation of the same lifecycle

## What was built

The fixture models the payment lifecycle of Fernhill Teas, a fictional online
tea shop that sells boxes and subscriptions. Staff take a payment for an order
by card over the phone or by emailing the customer a checkout link; customers
pay on a public checkout page through a fictional provider, Tessera Pay. Its
payment statuses are `pending | succeeded | failed`, and failure and 3DS
details live in untyped `Metadata` strings.

| Part | File | Lines |
| --- | --- | --- |
| Host Schema: `Money`, `StaffPayment` (6 Variants), `PublicPayment` (5 Variants) | `billing/payment.ktmlschema` | 68 (57 non-comment) |
| Components: `StaffPaymentPanel` and `PublicPaymentPage` (exhaustive), `PaymentBadge` (`default`), `TestTerms` (boolean), `PaymentNote` (optional) | `web/payment.ktml` | 149 |
| Handwritten presenters: `StaffPaymentState`, `PublicPaymentState`, `tesseraBrowserURL` | `web/presenter.go` | 136 (114 non-comment) |
| Behavioural tests: every Variant, nil state, URL policy, trusted 3DS host | `web/payment_test.go` | 155 |
| Generated: states | `golden/billing/payment_ktmlschema.go.golden` | 334 |
| Generated: renderers | `golden/web/payment_ktml.go.golden` | 1498 |

Line counts are those of the first run; the
[addendum](#addendum-rerun-with-slots-and-conditions) gives the current ones.

`go test ./internal/generator/gogen -run Payment` generates both packages and
checks them against the goldens. It then compiles them with the presenters in
a temporary module, runs `go vet`, and runs the fixture tests.

The fixture has these SSR shapes:

| Baseline template | KTML clause |
| --- | --- |
| `OrderPaymentModal` (`payable` branch) | `StaffPaymentPanel` `Payable` |
| `OrderPaymentModal` (`else`) | `StaffPaymentPanel` `NotDue` |
| `PaymentPending` | `StaffPaymentPanel` `Confirming` |
| `PaymentFailed` with `CanRetry` true | `StaffPaymentPanel` `Retryable` |
| `PaymentFailed` with `CanRetry` false | `StaffPaymentPanel` `Blocked` |
| `ShipmentModal` | `StaffPaymentPanel` `Confirmed` |
| `CheckoutPaymentPending`, `PaymentFailed`, `DeviceData`, `Challenge`, `Confirmation` | the five `PublicPaymentPage` clauses |

The markup is abbreviated: the modal header and footer chrome and the
`<head>` of the public pages are left out. Template line counts below are
therefore not compared one to one.

## Presenter glue

| Concern | Baseline templ/Go | KTML fixture |
| --- | --- | --- |
| Status to presentation | `NewStaffPaymentStatus`, 29 lines, returning a 6-field struct | `StaffPaymentState`, 57 lines, returning one typed Variant |
| Amount due or not | Computed inside the template, and the error from loading the order total is discarded | In the presenter (`duePence <= 0`), where errors can be returned |
| Choosing which component renders | Two handler `switch payment.Status` blocks, each with a 409 `default` | Render Clauses. The handler renders one component, and the presenter returns an error for an unknown status |
| URLs | 6+ `fmt.Sprintf`/concatenations inside templates, one of them a raw `href={ "/checkout?token=" + … }` | Built once in Go as `ktml.URL`, with the policy enforced at render |
| 3DS action URL | A 12-line host check, then a plain string in `action=` | `tesseraBrowserURL` (11 lines) returns `ktml.TrustedURL`; a clause can only render a `TrustedURL` field there |
| Public result | 3-way handler switch plus template parameters | `PublicPaymentState`, 20 lines |
| New authored artifact | none | 57 schema lines |

**Verdict on glue: it moves into one place but does not shrink.**

- The staff presenter roughly doubles, from 29 to 57 lines. It absorbs the
  `payable` computation and the URL building that the baseline does inside
  templates, so templates hold no Go at all.
- The status-to-component switch leaves the handlers. Handlers still fetch the
  Shipment and set HX headers, so they shrink by only about 10 lines each.
- The schema is new code (57 lines) that a templ codebase does not have.
- Constructors stutter, for example
  `billing.NewStaffPaymentPayable(billing.StaffPaymentPayable{…})`.

Net handwritten lines are higher, but there are fewer places that decide
presentation, and each one is typed.

One tension surfaced. The contract keeps literal copy in KTML, but the
fixture copies the baseline view model 1:1, so titles and details stay in Go.
Idiomatic KTML would model each of the 10 outcome presentations as its own
Variant. The copy would then live in clauses and be HMR-editable, at the cost
of a larger state and more clauses. Either choice is expressible; the
migration tool should prefer moving copy into clauses.

## Invalid states eliminated

The following are representable in the baseline and are impossible, or fail
with a typed error before output, in the fixture:

1. **A pending panel showing a settled payment.** `PaymentPending` has no
   status check and hardcodes yellow, so it renders any payment it is given.
   In KTML, `Confirming` is its own Variant with its own payload, and only the
   pending branch of the presenter constructs it.
2. **A retry label without a retry action, and the reverse.** The baseline
   sets `ActionLabel` for Blocked and Needs-investigation while `CanRetry` is
   false, so the label is dead. `Retryable` requires `actionLabel` and
   `retryLink`; `Blocked` has neither field.
3. **Unused presentation data.** The baseline's `Tone` is computed for every
   status and read by no template. It has no Variant field, so it cannot drift.
4. **A confirmed panel without its Shipment.** A `Confirmed` Variant cannot be
   built without `shipmentReference` and `shipmentLink`. The "succeeded but no
   Shipment" case is still a runtime check, but it moves from a handler 500
   into the one presenter as a returned error.
5. **Unknown or nil status reaching a template.** The baseline handles this
   with a 409 `default` in each of two handlers, and the templates would
   render anyway. KTML renderers return
   `*ktml.RenderError{Kind: ErrInvalidState}` before writing a byte; the test
   asserts zero bytes. A `default` body never receives a nil state.
6. **An unvalidated 3DS form action.** The baseline checks
   `Outcome == DeviceDataNeeded && DeviceData != nil` and then passes a string.
   `DeviceData.action` is a required `TrustedURL`, and Go can only obtain one
   through the host allowlist (`verify.tesserapay.example` and its sandbox).
   A plain `string` or `URL` field in that position is a compile-time
   `input-type-mismatch` or `contextual-type-mismatch`.
7. **Unsafe URL schemes.** A `ktml.URL` with `javascript:` renders
   `about:invalid#ktml-unsafe-url` (tested). The baseline's raw concatenated
   `href={ "/checkout?token=" + … }` relies on templ's attribute sanitising
   instead of a type.
8. **Adding a status without updating the templates.** Adding a Variant
   (for example `Refunded`) makes every bodyless clause group a fatal
   `non-exhaustive-clauses` error at generation. In the baseline it falls
   through to a runtime 409. Groups with a `default` warn
   (`default-fallthrough`) in the CLI and the LSP.

**Not eliminated.** The domain is still stringly typed: `Payment.Status` and
`Metadata["provider_outcome"]`. Presenters still switch on those strings.
Host Schema types the presentation boundary, not the domain.

## Tooling benefits observed

- **Exhaustiveness and fallthrough.** In the fixture, `PaymentBadge` reports
  `default-fallthrough` for `NotDue, Payable, Confirming, Retryable` at its
  `default` keyword. The CLI and the LSP report this from one analyzer.
- **Symbol rename** of a Variant, a state, or a payload field covers the schema
  declaration, Patterns and qualifiers, binding names, and unaliased binding
  uses in bodies. Compiler tests prove this; LSP wiring is in the slice's LSP
  layer.
- **Completion** of Variants after `selector =`, of payload fields in binding
  position, and of bindings as path roots.
- **Generated constructors** turn a missing or misspelled payload field into a
  Go compile error in the presenter.
- **Render tests become cheap.** Each Variant is a value; the fixture renders
  all 11. The baseline has no render test for `PaymentFailed`,
  `CheckoutPaymentFailed`, `DeviceData`, or `Challenge`.

## Defects and gaps found by the proof

1. **Childless siblings merge.** Consecutive childless elements on separate
   lines merge, because a following identifier parses as an attribute
   (`div id="a"` then `div id="b"`). The fixture appends `{}` to each one.
   This predates the slice, but migration must not hit it silently. The
   grammar should end an element's attributes at a newline, or the formatter
   should make `{}` canonical.
2. **Literal `"true"` attributes lose their value.** `gogen` lowers a literal
   `"true"` attribute value to a bare boolean attribute, so
   `hx-push-url="true"` renders `hx-push-url`. That changes htmx behaviour.
   It predates the slice and must be fixed before migrating HTMX templates.
3. **No children in clause groups.** Clause groups cannot render children until
   content inputs exist. The baseline wraps these panels in modal header and
   footer slot components, so the slots were inlined here.
4. **No conditions.** The public checkout page has four orthogonal conditions,
   such as `MembershipRequired` and acceptance. They need the conditions
   slice, or more Variants.

## Recommendation

**Proceed with the Host Schema rollout for Presentation States, scoped and
gated.**

- The schema buys concrete safety. Eight invalid or unchecked situations from
  realistic payment code become unrepresentable or fail typed before output.
  Exhaustiveness turns "new status" from a runtime 409 into a generation error.
- The cost is real but bounded. Presentation glue concentrates in one presenter
  per state and grows modestly, and the schema adds about 60 lines per domain
  area.

Before wider migration:

- fix defects 1 and 2;
- land content inputs (defect 3);
- land conditions (defect 4).

Prefer Variant-per-presentation with copy in clauses, so copy edits stay
HMR-compatible. Re-measure on a second domain (for example order returns or
stock alerts), using records and states without slots, before converting the
modal-heavy templates.

## Decision

The user accepted this recommendation on 2026-10-12. The Host Schema rollout
for Presentation States proceeds, with re-measurement on a second domain
before converting the modal-heavy templates. The four prerequisites are
scheduled after Keyed Repetition and before the item-9 rendering-API
prototypes. For defect 1, the user chose this grammar rule: a newline ends an
element's attribute list. Multi-line attribute lists need an explicit
continuation form, which the spec defines.

## Addendum: rerun with slots and conditions

- Date: 2026-10-12
- Slice: [Element Lines, Literal Attributes, Content Inputs, and Conditions v1](language-gaps-v1.md)

The four prerequisites landed, and the fixture was rerun without its
workarounds.

### The four defects

1. **Childless siblings.** A newline now ends an element's attribute list.
   Every `{}` that kept siblings apart is gone from the fixture (and from the
   board and hostschema fixtures). The goldens were byte-identical after the
   removal, before any other change. Long attribute lists use the
   parenthesized continuation form.
2. **Literal `"true"`.** `hx-push-url="true"` and `data-confetti="true"` now
   render with their values. The fixture test asserts both.
3. **Content inputs.** These are now components with content inputs:
   - `ModalHeader` and `ModalFooter`, the staff modal chrome, each with
     `children: content`;
   - the public pages' shared `<head>`/`<body>`, as `PublicShell` with
     `children: content` and `scripts?: content`.

   Every `StaffPaymentPanel` clause and every `PublicPaymentPage` clause calls
   them instead of inlining the chrome, so clause bodies now render slot
   components. `ModalHeader` called without children returns
   `ErrMissingContent` before writing, and the test asserts zero bytes.
4. **Conditions.** `CheckoutPage` renders the public subscription checkout
   with its four orthogonal conditions:
   - the Tessera Pay checkout scripts, inside a `slot scripts` fill;
   - the tea club membership notice;
   - accepted-and-waiting, which reads `order.acceptance.acceptedAt` only
     inside `order.membershipRequired && order.acceptance?`;
   - accept-terms versus card form (`else if !order.cardCheckout`).

   Six tests cover the combinations.

### Glue and size

| Part | Before | After |
| --- | --- | --- |
| Host Schema | 68 lines | 90 lines (`TermsAcceptance`, `OrderSummary`) |
| `payment.ktml` | 149 lines | 344 lines |
| Handwritten presenters | 136 lines | 169 lines (`CheckoutView`, 20 lines plus the `Basket` input type) |
| Behavioural tests | 155 lines | 225 lines |
| Generated renderers (golden) | 1498 lines | 971 lines |

Most of the KTML growth is new markup: the checkout page (about 80 lines), the
three shared components (about 45 lines), and attribute lists split one per
line by the continuation form. Clause bodies got shorter where they now call
`ModalHeader`, `ModalFooter`, and `PublicShell`. The "After" column gives
the fixture's current sizes; the generated renderer count reflects the
current generator, not the one used for the first run.

`CheckoutView` is the only new glue. It turns the two inline comparisons a
template would make (`Subscription && DueNow > 0` and its negation) into one
`cardCheckout` boolean. It also turns the acceptance into an optional record.

### Invalid states added to the list

9. **Checkout scripts disagreeing with the card form.** A templ page loads the
   Tessera Pay scripts and chooses the card form with two separate inline
   expressions that must stay each other's negation. Both now read one
   Go-derived `cardCheckout`, so the page cannot load the checkout without
   the form, or the reverse.
10. **Reading an absent acceptance.** `order.acceptance.acceptedAt` outside a
    branch where `order.acceptance?` holds is a compile-time
    `optional-value-access`. A templ page guards with `!= nil` by convention
    only.

### Verdict

The recommendation stands: proceed with the scoped Host Schema rollout. The
modal-heavy templates are no longer blocked by missing language features,
because slots, clause bodies that compose slot components, and conditions
now all work end to end in `gogen`. The planned re-measurement on a second
domain still comes first. The interpreter (Phase 3) and the item-9 API
choice remain open.
