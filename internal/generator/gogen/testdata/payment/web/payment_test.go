package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"example.com/fixture/billing"
	"github.com/karl-tye/ktml"
)

func render(t *testing.T, component ktml.Component) string {
	t.Helper()
	var output strings.Builder
	if err := component.Render(context.Background(), &output); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return output.String()
}

func TestStaffPanel_GIVENEveryPresentedState_WHENRendered_THENEachVariantHasItsOwnShape(t *testing.T) {
	order := ktml.NewURL("/orders/o1")
	tests := []struct {
		name     string
		duePence int
		payment  *Payment
		shipment *Shipment
		want     []string
		absent   []string
	}{
		{name: "nothing due", want: []string{"No upfront payment is due."}, absent: []string{"phone-card-form"}},
		{name: "payable", duePence: 4999, want: []string{`action="/orders/o1/payment/card"`, `<p class="text-3xl font-bold text-green">£49.99</p>`, `href="/orders/o1/checkout-link"`, "Take £49.99"}},
		{name: "pending", duePence: 4999, payment: &Payment{ID: "p<1>", Status: "pending", Metadata: map[string]string{"provider_outcome": "challenged"}},
			want: []string{`hx-get="/orders/o1/payment/status"`, `hx-trigger="every 5s"`, `value="p&lt;1&gt;"`, "Customer action required"}, absent: []string{"Retry", "Open Shipment"}},
		{name: "declined is retryable", duePence: 4999, payment: &Payment{ID: "p1", Status: "failed", Metadata: map[string]string{"provider_outcome": "declined"}},
			want: []string{"Card declined", `hx-get="/orders/o1/payment"`, "Try another card"}},
		{name: "fraud is blocked without retry", duePence: 4999, payment: &Payment{ID: "p1", Status: "failed", Metadata: map[string]string{"provider_outcome": "highRisk"}},
			want: []string{"Payment blocked by Tessera Pay"}, absent: []string{`hx-get="/orders/o1/payment"`}},
		{name: "succeeded", duePence: 4999, payment: &Payment{ID: "p1", Status: "succeeded"}, shipment: &Shipment{ID: "s 1", Reference: "SHP-7"},
			want: []string{"Shipment SHP-7 created", `href="/shipments/s%201"`, `data-confetti="true">`, `hx-push-url="true"`, "£49.99"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := StaffPaymentState("o1", tt.duePence, tt.payment, tt.shipment)
			if err != nil {
				t.Fatal(err)
			}
			html := render(t, StaffPaymentPanel{Payment: state, OrderLink: order})
			for _, want := range tt.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q in\n%s", want, html)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(html, absent) {
					t.Errorf("unexpected %q in\n%s", absent, html)
				}
			}
		})
	}
}

func TestStaffPanel_GIVENImpossibleInputs_WHENPresented_THENGoRejectsBeforeRendering(t *testing.T) {
	if _, err := StaffPaymentState("o1", 100, &Payment{ID: "p1", Status: "succeeded"}, nil); err == nil {
		t.Error("succeeded payment without a Shipment was presented")
	}
	if _, err := StaffPaymentState("o1", 100, &Payment{ID: "p1", Status: "refunded"}, nil); err == nil {
		t.Error("unknown status was presented")
	}
}

type countingWriter struct{ bytes int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return len(p), nil
}

func TestStaffPanel_GIVENNilState_WHENRendered_THENReturnsTypedErrorBeforeWriting(t *testing.T) {
	var output countingWriter
	err := (StaffPaymentPanel{OrderLink: ktml.NewURL("/o")}).Render(context.Background(), &output)
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrInvalidState || renderError.Component != "example.com/fixture/web.StaffPaymentPanel" || renderError.Input != "payment" {
		t.Fatalf("Render() error = %#v", err)
	}
	if output.bytes != 0 {
		t.Errorf("wrote %d bytes before the invalid-state error", output.bytes)
	}
	err = (PaymentBadge{}).Render(context.Background(), &output)
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrInvalidState || output.bytes != 0 {
		t.Fatalf("default body selected a nil state: %v, %d bytes", err, output.bytes)
	}
}

func TestBadge_GIVENDefaultBody_WHENResidualAndSpecializedVariants_THENSelectsDeclaratively(t *testing.T) {
	tests := []struct {
		state billing.StaffPayment
		want  string
	}{
		{billing.NewStaffPaymentNotDue(), "In progress"},
		{billing.NewStaffPaymentConfirming(billing.StaffPaymentConfirming{}), "In progress"},
		{billing.NewStaffPaymentConfirmed(billing.StaffPaymentConfirmed{}), "Paid"},
		{billing.NewStaffPaymentBlocked(billing.StaffPaymentBlocked{}), "Blocked"},
	}
	for _, tt := range tests {
		if html := render(t, PaymentBadge{Payment: tt.state}); !strings.Contains(html, ">"+tt.want+"<") {
			t.Errorf("%s badge = %s, want %s", tt.state.Variant(), html, tt.want)
		}
	}
}

func TestPublicPage_GIVENGatewayResults_WHENRendered_THENEnforcesURLBoundaries(t *testing.T) {
	pending, err := PublicPaymentState("ORD-1", "t&k", Payment{ID: "p 1", Status: "pending"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if html := render(t, PublicPaymentPage{Payment: pending}); !strings.Contains(html, `data-status-url="/checkout/payment/status?token=t%26k&amp;paymentId=p+1"`) {
		t.Errorf("pending page = %s", html)
	}
	device, err := PublicPaymentState("ORD-1", "t", Payment{ID: "p1", Status: "pending"}, &DeviceData{URL: "https://verify.tesserapay.example/v1/collect", Bin: "4444", JWT: "a.b"})
	if err != nil {
		t.Fatal(err)
	}
	html := render(t, PublicPaymentPage{Payment: device})
	if !strings.Contains(html, `action="https://verify.tesserapay.example/v1/collect"`) || !strings.Contains(html, `value="4444"`) {
		t.Errorf("device-data page = %s", html)
	}
	if _, err := PublicPaymentState("ORD-1", "t", Payment{ID: "p1", Status: "pending"}, &DeviceData{URL: "https://evil.example/collect"}); err == nil {
		t.Error("untrusted 3DS host became a trusted action")
	}
	failed, _ := PublicPaymentState("ORD-1", "t k", Payment{ID: "p1", Status: "failed"}, nil)
	if html := render(t, PublicPaymentPage{Payment: failed}); !strings.Contains(html, `href="/checkout?token=t+k"`) {
		t.Errorf("failed page = %s", html)
	}
	unsafe := billing.NewPublicPaymentNotCompleted(billing.PublicPaymentNotCompleted{Reference: "x", ReturnLink: ktml.NewURL("javascript:alert(1)")})
	if html := render(t, PublicPaymentPage{Payment: unsafe}); !strings.Contains(html, `href="about:invalid#ktml-unsafe-url"`) || strings.Contains(html, "javascript") {
		t.Errorf("unsafe URL rendered: %s", html)
	}
}

func TestBooleanAndOptionalGroups_GIVENDefaultsAndAbsence_WHENRendered_THENSelectCases(t *testing.T) {
	if html := render(t, TestTerms{}); html != "" {
		t.Errorf("defaulted false = %q", html)
	}
	if html := render(t, TestTerms{TestMode: ktml.Ptr(true)}); !strings.Contains(html, "Test terms") {
		t.Errorf("true = %q", html)
	}
	if html := render(t, PaymentNote{}); html != "" {
		t.Errorf("None = %q", html)
	}
	if html := render(t, PaymentNote{Note: ktml.Ptr("<b>")}); html != `<p class="mt-2 text-xs text-subtext0">&lt;b&gt;</p>` {
		t.Errorf("Some = %q", html)
	}
}

func TestStaffPanel_GIVENModalSlots_WHENRendered_THENHeaderAndFooterWrapEachVariant(t *testing.T) {
	state, err := StaffPaymentState("o1", 4999, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	html := render(t, StaffPaymentPanel{Payment: state, OrderLink: ktml.NewURL("/orders/o1")})
	for _, want := range []string{
		`<div class="flex min-w-0 flex-1 items-center gap-3"><div><h2 class="text-xl font-bold text-text">Take Payment</h2></div></div>`,
		`aria-label="Close dialog" onclick="this.closest(&#39;dialog&#39;).close()"`,
		`<div class="p-4 border-t border-surface0 flex justify-between items-center bg-surface0/30 shrink-0"><div class="flex justify-end gap-3 w-full">`,
		`<div id="phone-card-pan" class="h-10 border border-surface1"></div><div id="phone-card-expiry"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
}

func TestModalHeader_GIVENNoChildren_WHENRendered_THENReturnsMissingContentBeforeWriting(t *testing.T) {
	var output countingWriter
	err := (ModalHeader{}).Render(context.Background(), &output)
	var renderError *ktml.RenderError
	if !errors.As(err, &renderError) || renderError.Kind != ktml.ErrMissingContent || renderError.Input != "children" || output.bytes != 0 {
		t.Fatalf("Render() = %#v after %d bytes", err, output.bytes)
	}
}

func TestCheckoutPage_GIVENOrthogonalFacts_WHENRendered_THENConditionsSelectTheAction(t *testing.T) {
	base := Basket{Reference: "ORD-9", Blend: "Assam", Postcode: "EH1", DueNowPence: 4999, FirstBoxPence: 9000, MonthlyPence: 4500, Version: 3, Token: "t<k>", CheckoutID: "co-1"}
	tests := []struct {
		name   string
		basket func(Basket) Basket
		want   []string
		absent []string
	}{
		{name: "one-off order accepts terms", basket: func(b Basket) Basket { return b },
			want:   []string{`action="/checkout/accept"`, `value="t&lt;k&gt;"`, "I accept order revision 3, its stated conditions", "Test terms", "<title>ORD-9 checkout</title>"},
			absent: []string{"tessera-card-form", "checkout.js", "Membership required"}},
		{name: "subscription pays by card", basket: func(b Basket) Basket { b.Subscription = true; return b },
			want:   []string{`id="tessera-card-form" data-checkout-id="co-1"`, `<script src="https://js.tesserapay.example/checkout/v2/checkout.js"></script><script src="/static/js/tessera-checkout.js" defer></script><title>`, "£49.99"},
			absent: []string{`action="/checkout/accept"`}},
		{name: "nothing due now accepts terms", basket: func(b Basket) Basket { b.Subscription, b.DueNowPence = true, 0; return b },
			want: []string{`action="/checkout/accept"`}, absent: []string{"checkout.js"}},
		{name: "membership without acceptance", basket: func(b Basket) Basket { b.MembershipRequired = true; return b },
			want: []string{"Membership required", `action="/checkout/accept"`}, absent: []string{"waiting for membership"}},
		{name: "membership accepted", basket: func(b Basket) Basket {
			b.MembershipRequired, b.Subscription, b.AcceptedAt = true, true, "12 Oct"
			return b
		},
			want: []string{"Membership required", "waiting for membership confirmation", "Accepted 12 Oct."}, absent: []string{"<form"}},
		{name: "acceptance without membership pays", basket: func(b Basket) Basket { b.Subscription, b.AcceptedAt = true, "12 Oct"; return b },
			want: []string{"tessera-card-form"}, absent: []string{"waiting for membership", "Membership required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := render(t, CheckoutPage{Order: CheckoutView(tt.basket(base))})
			for _, want := range tt.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q in\n%s", want, html)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(html, absent) {
					t.Errorf("unexpected %q in\n%s", absent, html)
				}
			}
		})
	}
}
