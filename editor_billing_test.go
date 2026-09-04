package main

import (
	"strings"
	"testing"
	"time"
)

// The API host and the plan ids must move together. A live plan id sent to the
// sandbox is a broken checkout; a sandbox plan id sent to live, or a live plan
// used from a development run, charges someone real money.
func TestPayPalEnvironmentIsSelfConsistent(t *testing.T) {
	tests := []struct {
		sandbox bool
		host    string
		monthly string
		annual  string
	}{
		{false, "https://api-m.paypal.com", planMonthlyLive, planAnnualLive},
		{true, "https://api-m.sandbox.paypal.com", planMonthlySandbox, planAnnualSandbox},
	}
	for _, tc := range tests {
		p := newPayPalClient(paypalConfig{ClientID: "id", Secret: "secret", Sandbox: tc.sandbox})
		if p.apiBase != tc.host {
			t.Errorf("sandbox=%v: apiBase = %q, want %q", tc.sandbox, p.apiBase, tc.host)
		}
		if p.planMonthly != tc.monthly || p.planAnnual != tc.annual {
			t.Errorf("sandbox=%v: plans = %q/%q, want %q/%q",
				tc.sandbox, p.planMonthly, p.planAnnual, tc.monthly, tc.annual)
		}
	}

	// The four ids must be distinct, or the check above proves nothing.
	seen := map[string]bool{}
	for _, id := range []string{planMonthlyLive, planAnnualLive, planMonthlySandbox, planAnnualSandbox} {
		if id == "" {
			t.Error("a plan id is empty")
		}
		if seen[id] {
			t.Errorf("plan id %q is used twice", id)
		}
		seen[id] = true
	}
}

// Billing must not run half-configured, and the startup log must not leak the
// secret.
func TestPayPalConfigured(t *testing.T) {
	var nilClient *paypalClient
	if nilClient.configured() {
		t.Error("a nil client reports itself configured")
	}
	if newPayPalClient(paypalConfig{ClientID: "id"}).configured() {
		t.Error("a client with no secret reports itself configured")
	}
	if !newPayPalClient(paypalConfig{ClientID: "id", Secret: "s"}).configured() {
		t.Error("a client with both credentials reports itself unconfigured")
	}

	// An unset webhook id is called out, because the webhook fails closed.
	got := newPayPalClient(paypalConfig{ClientID: "id", Secret: "shhh"}).String()
	if !strings.Contains(got, "PAYPAL_WEBHOOK_ID") {
		t.Errorf("got %q, want a warning that the webhook id is unset", got)
	}
	if strings.Contains(got, "shhh") {
		t.Errorf("the startup log leaks the secret: %q", got)
	}
}

// Every interval offered must be priced, or a subscriber sees a plan with no
// price on it.
func TestPlusPlansArePriced(t *testing.T) {
	for _, p := range plusPlans {
		if p.Interval == "" || p.Label == "" || p.Price == "" || p.Per == "" {
			t.Errorf("plan %+v is incomplete", p)
		}
		if plusPlanFor(p.Interval) == nil {
			t.Errorf("plusPlanFor(%q) found nothing", p.Interval)
		}
	}
	// The two intervals a Subscription can carry.
	for _, interval := range []string{"monthly", "annual"} {
		if plusPlanFor(interval) == nil {
			t.Errorf("no price for the %q interval", interval)
		}
	}
	if plusPlanFor("weekly") != nil {
		t.Error("plusPlanFor invented a plan")
	}
	if len(plusFeatures) == 0 {
		t.Error("Plus is sold without saying what it includes")
	}
}

// applyTo carries PayPal's billing detail onto the stored record, and — just
// as importantly — leaves stored values alone for fields PayPal omitted.
func TestPayPalSubscriptionApplyTo(t *testing.T) {
	var remote paypalSubscription
	remote.ID = "I-1"
	remote.PlanID = planMonthlyLive
	remote.Status = "ACTIVE"
	remote.BillingInfo.NextBillingTime = "2027-02-11T10:00:00Z"
	remote.BillingInfo.LastPayment.Time = "2026-02-11T10:00:00Z"
	remote.BillingInfo.LastPayment.Amount.Value = "70.00"
	remote.BillingInfo.LastPayment.Amount.CurrencyCode = "USD"

	sub := &Subscription{UID: "uid"}
	remote.applyTo(sub)

	if sub.Status != "ACTIVE" || sub.PayPalSubscriptionID != "I-1" || sub.PayPalPlanID != planMonthlyLive {
		t.Errorf("got %+v, want the remote identity copied", sub)
	}
	if got := sub.NextBilling.Format(time.RFC3339); got != "2027-02-11T10:00:00Z" {
		t.Errorf("NextBilling = %s", got)
	}
	if got := sub.LastPayment.Format(time.RFC3339); got != "2026-02-11T10:00:00Z" {
		t.Errorf("LastPayment = %s", got)
	}
	if sub.LastPaymentAmount != "70.00 USD" {
		t.Errorf("LastPaymentAmount = %q", sub.LastPaymentAmount)
	}

	// A cancellation event carries no next_billing_time. The stored one is
	// what the drafter is paid through, so it must survive — and become the
	// date Plus runs out.
	cancel := paypalSubscription{ID: "I-1", Status: "CANCELLED"}
	cancel.applyTo(sub)
	if got := sub.NextBilling.Format(time.RFC3339); got != "2027-02-11T10:00:00Z" {
		t.Errorf("cancelling cleared the renewal date: %s", got)
	}
	if !sub.AccessUntil.Equal(sub.NextBilling) {
		t.Errorf("AccessUntil = %s, want the renewal that will not happen", sub.AccessUntil)
	}
	if sub.LastPaymentAmount != "70.00 USD" {
		t.Error("cancelling cleared the payment history")
	}

	// An event with nothing in it must not blank a paying drafter's record.
	before := *sub
	(&paypalSubscription{}).applyTo(sub)
	if sub.Status != before.Status || sub.PayPalSubscriptionID != before.PayPalSubscriptionID {
		t.Errorf("an empty event overwrote the record: %+v", sub)
	}
}

// Free-plan access follows the PayPal status, and only ACTIVE is Plus.
func TestPlanOf(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"ACTIVE", PlanPlus},
		{"APPROVED", PlanFree},
		{"APPROVAL_PENDING", PlanFree},
		{"SUSPENDED", PlanFree},
		{"CANCELLED", PlanFree},
		{"EXPIRED", PlanFree},
		{"", PlanFree},
	}
	for _, tc := range tests {
		if got := planOf(&Subscription{Status: tc.status}); got != tc.want {
			t.Errorf("planOf(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
	if got := planOf(nil); got != PlanFree {
		t.Errorf("planOf(nil) = %q, want %q", got, PlanFree)
	}

	// PayPal does not refund the cycle a drafter has already paid for, so
	// cancelling keeps Plus until the renewal that will not happen.
	future := &Subscription{Status: "CANCELLED", AccessUntil: time.Now().Add(24 * time.Hour)}
	if planOf(future) != PlanPlus {
		t.Error("a canceled subscription lost Plus before the cycle it paid for ended")
	}
	if !future.Canceling() {
		t.Error("a canceled subscription still in its cycle does not report as canceling")
	}
	past := &Subscription{Status: "CANCELLED", AccessUntil: time.Now().Add(-time.Second)}
	if planOf(past) != PlanFree {
		t.Error("a canceled subscription kept Plus past the cycle it paid for")
	}
	if past.Canceling() {
		t.Error("an ended subscription still reports as canceling")
	}
	// The grace period is for cancellation only. A failed payment stops access
	// now, whatever date happens to be stored.
	suspended := &Subscription{Status: "SUSPENDED", AccessUntil: time.Now().Add(24 * time.Hour)}
	if planOf(suspended) != PlanFree {
		t.Error("a suspended subscription still grants Plus")
	}

	// Pending is the gap between approval and activation, and nothing else.
	if !(&Subscription{Status: "APPROVED"}).Pending() {
		t.Error("APPROVED is not pending")
	}
	if (&Subscription{Status: "CANCELLED"}).Pending() {
		t.Error("CANCELLED reads as pending, so a canceled drafter waits forever")
	}
	if (*Subscription)(nil).Pending() {
		t.Error("a free account with no subscription reads as pending")
	}
}
