// Package web is the payment-proof fixture's handwritten Go. Its presenters
// perform the Business Derivation for the Fernhill Teas shop: payment status
// to presentation, URL building, and the checkout page's facts.
package web

import (
	"fmt"
	"net/url"
	"strings"

	"example.com/fixture/billing"
	"github.com/karl-tye/ktml"
)

// Payment is the shop's domain payment record: the status is a string and
// state-specific facts live in untyped metadata.
type Payment struct {
	ID       string
	Status   string
	Metadata map[string]string
}

// Shipment is the Shipment a settled payment created.
type Shipment struct {
	ID        string
	Reference string
}

func money(pence int) billing.Money {
	return billing.Money{Display: fmt.Sprintf("£%.2f", float64(pence)/100)}
}

// StaffPaymentState derives the staff panel state. A nil payment means no
// attempt exists yet; a succeeded payment must have created its Shipment.
func StaffPaymentState(orderID string, duePence int, payment *Payment, shipment *Shipment) (billing.StaffPayment, error) {
	base := "/orders/" + url.PathEscape(orderID)
	if payment == nil {
		if duePence <= 0 {
			return billing.NewStaffPaymentNotDue(), nil
		}
		return billing.NewStaffPaymentPayable(billing.StaffPaymentPayable{
			Amount: money(duePence), CardAction: ktml.NewURL(base + "/payment/card"), CheckoutLink: ktml.NewURL(base + "/checkout-link"),
		}), nil
	}
	outcome := payment.Metadata["provider_outcome"]
	switch payment.Status {
	case "succeeded":
		if shipment == nil {
			return nil, fmt.Errorf("payment %s succeeded without a Shipment", payment.ID)
		}
		return billing.NewStaffPaymentConfirmed(billing.StaffPaymentConfirmed{
			Amount: money(duePence), ShipmentReference: shipment.Reference, ShipmentLink: ktml.NewURL("/shipments/" + url.PathEscape(shipment.ID)),
		}), nil
	case "failed":
		retry := func(label, title, detail, action string) billing.StaffPayment {
			return billing.NewStaffPaymentRetryable(billing.StaffPaymentRetryable{
				Label: label, Title: title, Detail: detail, ActionLabel: action, RetryLink: ktml.NewURL(base + "/payment"),
			})
		}
		switch outcome {
		case "authenticationFailed", "authenticationRejected", "authenticationAbandoned":
			return retry("Customer action failed", "Bank verification failed", "The customer did not complete their bank's identity check.", "Start a new payment"), nil
		case "authenticationUnavailable", "issuerUnavailable", "issuerTimeout", "directoryError", "verificationUnavailable", "verificationError":
			return retry("Provider unavailable", "Bank verification unavailable", "The issuer or authentication service was unavailable.", "Retry payment"), nil
		case "declined":
			return retry("Declined", "Card declined", "No payment was taken. Ask the customer for another card.", "Try another card"), nil
		case "highRisk":
			return billing.NewStaffPaymentBlocked(billing.StaffPaymentBlocked{Label: "Blocked", Title: "Payment blocked by Tessera Pay", Detail: "Do not retry the same card."}), nil
		default:
			return billing.NewStaffPaymentBlocked(billing.StaffPaymentBlocked{Label: "Needs investigation", Title: "Settlement needs investigation", Detail: "Tessera Pay reported a failure after submission."}), nil
		}
	case "pending":
		confirming := func(label, title, detail string) billing.StaffPayment {
			return billing.NewStaffPaymentConfirming(billing.StaffPaymentConfirming{
				PaymentId: payment.ID, Label: label, Title: title, Detail: detail, StatusLink: ktml.NewURL(base + "/payment/status"),
			})
		}
		switch outcome {
		case "deviceDataRequired":
			return confirming("Customer browser", "Customer security check in progress", "Ask them to keep the payment page open."), nil
		case "challenged":
			return confirming("Customer action", "Customer action required", "The customer's bank is waiting for identity verification."), nil
		case "captured":
			return confirming("Accepted", "Payment accepted by Tessera Pay", "The Shipment will be created automatically."), nil
		default:
			return confirming("Checking", "Waiting for Tessera Pay", "Tessera Pay has not confirmed this payment's stage."), nil
		}
	default:
		return nil, fmt.Errorf("payment %s has unknown status %q", payment.ID, payment.Status)
	}
}

// tesseraBrowserURL admits only https URLs on Tessera Pay's verification hosts
// as trusted form actions.
func tesseraBrowserURL(raw string) (ktml.TrustedURL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return ktml.TrustedURL{}, fmt.Errorf("untrusted 3DS URL %q", raw)
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "verify.sandbox.tesserapay.example", "verify.tesserapay.example":
		return ktml.TrustURL(parsed.String()), nil
	}
	return ktml.TrustedURL{}, fmt.Errorf("untrusted 3DS host %q", parsed.Hostname())
}

func checkoutLink(token string) ktml.URL {
	return ktml.NewURL("/checkout?token=" + url.QueryEscape(token))
}

// DeviceData is the 3DS device-data collection step a gateway may request.
type DeviceData struct {
	URL, Bin, JWT string
}

// PublicPaymentState derives the public checkout result page.
func PublicPaymentState(reference, token string, payment Payment, device *DeviceData) (billing.PublicPayment, error) {
	switch payment.Status {
	case "succeeded":
		return billing.NewPublicPaymentConfirmed(billing.PublicPaymentConfirmed{Reference: reference}), nil
	case "failed":
		return billing.NewPublicPaymentNotCompleted(billing.PublicPaymentNotCompleted{Reference: reference, ReturnLink: checkoutLink(token)}), nil
	case "pending":
		if device != nil {
			action, err := tesseraBrowserURL(device.URL)
			if err != nil {
				return nil, err
			}
			return billing.NewPublicPaymentDeviceData(billing.PublicPaymentDeviceData{Action: action, Bin: device.Bin, Jwt: device.JWT}), nil
		}
		status := ktml.NewURL(fmt.Sprintf("/checkout/payment/status?token=%s&paymentId=%s", url.QueryEscape(token), url.QueryEscape(payment.ID)))
		return billing.NewPublicPaymentConfirming(billing.PublicPaymentConfirming{Reference: reference, StatusLink: status}), nil
	default:
		return nil, fmt.Errorf("payment %s has unknown status %q", payment.ID, payment.Status)
	}
}

// Basket is the domain basket the checkout page reads.
type Basket struct {
	Reference, Blend, Postcode string
	Subscription               bool
	DueNowPence, FirstBoxPence int
	MonthlyPence, Version      int
	MembershipRequired         bool
	AcceptedAt                 string
	Token, CheckoutID          string
}

// CheckoutView derives the public checkout page. The inline comparisons a
// template would make (a subscription with a positive amount due now) become
// one boolean, and an acceptance is present only when one was recorded.
func CheckoutView(basket Basket) billing.OrderSummary {
	view := billing.OrderSummary{
		Reference:          basket.Reference,
		Summary:            basket.Reference + " · " + basket.Blend + " · " + basket.Postcode,
		DueNow:             money(basket.DueNowPence),
		FirstBox:           money(basket.FirstBoxPence),
		Monthly:            money(basket.MonthlyPence),
		Version:            int64(basket.Version),
		MembershipRequired: basket.MembershipRequired,
		CardCheckout:       basket.Subscription && basket.DueNowPence > 0,
		CheckoutId:         basket.CheckoutID,
		Token:              basket.Token,
	}
	if basket.AcceptedAt != "" {
		view.Acceptance = &billing.TermsAcceptance{AcceptedAt: basket.AcceptedAt}
	}
	return view
}
