package main

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"
	"time"
)

// The editor templates all sit behind authentication, so they are exercised
// here rather than only in a browser.
func TestEditorTemplatesRender(t *testing.T) {
	fs := os.DirFS(".")
	user := &SessionUser{UID: "uid", Email: "drafter@example.com"}
	profile := &Profile{UID: "uid", Email: "drafter@example.com", Name: "Ada Drafter"}
	document := &Document{
		ID:           "11111111-2222-3333-4444-555555555555",
		UID:          "uid",
		Owner:        "drafter@example.com",
		Title:        "door alarms in school buildings",
		Code:         "charter",
		LastModified: time.Now().UTC(),
	}

	type row struct {
		Document
		Shared bool
	}

	cases := []struct {
		name string
		body map[string]any
		want []string
		deny []string
	}{
		{
			name: "editor_susi.html",
			body: map[string]any{"Title": "Sign in", "User": (*SessionUser)(nil)},
			want: []string{"Draft legislation"},
		},
		{
			name: "editor_documents.html",
			body: map[string]any{
				"Title":   "Drafts",
				"User":    user,
				"Profile": profile,
				"Documents": []row{
					{Document: *document},
					{Document: Document{ID: "other", Owner: "someone@example.com"}, Shared: true},
				},
			},
			want: []string{
				"<h1>Drafts</h1>",
				`data-delete="11111111-2222-3333-4444-555555555555"`,
				"bi-trash",
				"modal-delete",
				// the brandbar shows the display name and links to the profile
				"Ada Drafter",
				`href="/profile"`,
			},
		},
		{
			name: "editor_profile.html",
			body: map[string]any{
				"Title": "Profile", "User": user, "Profile": profile, "Saved": true,
				"BaseURL": "https://editor.intro.nyc",
			},
			want: []string{
				`value="Ada Drafter"`,
				"drafter@example.com",
				"Saved.",
				// integrations are opt-in, so an unprovisioned profile offers the
				// button rather than a key
				"Enable API / MCP integrations",
			},
		},
		{
			name: "editor_profile.html",
			body: map[string]any{
				"Title": "Profile", "User": user,
				"Profile": &Profile{
					UID: "uid", Email: "drafter@example.com", Name: "Ada Drafter",
					APIToken: "intro_deadbeef",
				},
				"BaseURL": "https://editor.intro.nyc",
			},
			want: []string{
				"intro_deadbeef",
				"claude mcp add",
				"https://editor.intro.nyc/mcp",
				"Bearer intro_deadbeef",
				"Turn off integrations",
			},
		},
		{
			name: "editor_new.html",
			body: map[string]any{"Title": "New bill", "User": user, "Profile": profile},
			want: []string{"Amend the", "Unconsolidated"},
		},
		{
			name: "editor_error.html",
			body: map[string]any{
				"Title": "Permission denied", "Code": 403,
				"Message": "You do not have access to this draft.",
				"SignIn":  true, "Next": document.ID,
			},
			want: []string{"403 Permission denied", "/sign_in?next="},
		},
		{
			name: "editor.html",
			body: map[string]any{
				"Title": "a bill", "User": user, "Profile": profile,
				"Document": document, "IsOwner": true, "CanExport": true,
			},
			want: []string{
				"A Draft Local Law",
				`id="share-add"`,
				`id="share-people"`,
				`id="share-public"`,
				"Copy link",
				">Done<",
				`data-export="copy-rich"`,
			},
			deny: []string{`data-export="copy-rich" disabled`, "Export requires"},
		},
		{
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "free", "PayPalClientID": "client-id", "ShowButtons": true,
				"PlanIDMonthly": "P-MONTHLY", "PlanIDAnnual": "P-ANNUAL",
				"Plans": plusPlans, "Features": plusFeatures,
			},
			want: []string{
				"Upgrade to Plus",
				"paypal-button-container-monthly",
				"paypal-button-container-annual",
				"P-MONTHLY",
				"P-ANNUAL",
				// the offer quotes the one pricing table
				"$6.99", "$70", "Unlimited drafts",
			},
		},
		{
			// PayPal takes the approval before the subscription is active, so
			// the drafter must not be shown the offer they just accepted.
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "free", "Pending": true,
				"Subscription": &Subscription{Interval: "monthly", Status: "APPROVED"},
			},
			want: []string{"Confirming your subscription"},
			deny: []string{"paypal-button-container-monthly", "Upgrade to Plus"},
		},
		{
			// A subscriber must be able to see what they are paying for, what
			// it costs, when they were last charged and when it renews.
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "plus", "Features": plusFeatures,
				"PlusPlan": plusPlanFor("annual"),
				"Subscription": &Subscription{
					Interval: "annual", Status: "ACTIVE",
					PayPalSubscriptionID: "I-ANNUAL1",
					LastPayment:          time.Date(2026, 2, 11, 0, 0, 0, 0, time.UTC),
					LastPaymentAmount:    "70.00 USD",
					NextBilling:          time.Date(2027, 2, 11, 0, 0, 0, 0, time.UTC),
				},
			},
			want: []string{
				"You're on Plus",
				"$70", "per year",
				"Last billed", "February 11, 2026", "70.00 USD",
				"Renews", "February 11, 2027",
				"I-ANNUAL1",
				"Unlimited drafts",
				"btn-cancel-plan",
			},
		},
		{
			// Canceled but paid through the cycle: the date must be shown as
			// an ending, and there must be nothing left to cancel.
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "plus", "Canceling": true, "Features": plusFeatures,
				"PlusPlan": plusPlanFor("monthly"),
				"Subscription": &Subscription{
					Interval: "monthly", Status: "CANCELLED",
					AccessUntil: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			want: []string{
				"will not renew",
				"Plus ends",
				"October 1, 2026",
			},
			deny: []string{"btn-cancel-plan"},
		},
		{
			// A failed payment drops the drafter to free, so the explanation
			// has to sit above the upgrade offer or they never see it.
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "free", "Suspended": true, "ShowButtons": true,
				"Plans": plusPlans, "Features": plusFeatures,
				"Subscription": &Subscription{
					Interval: "monthly", Status: "SUSPENDED",
				},
			},
			want: []string{"suspended", "Review it at PayPal"},
		},
		{
			// A subscriber PayPal has not yet billed must not be shown a zero
			// time rendered as "January 1, year 1".
			name: "editor_billing.html",
			body: map[string]any{
				"Title": "Billing", "User": user, "Profile": profile,
				"Plan": "plus", "Features": plusFeatures,
				"PlusPlan":     plusPlanFor("monthly"),
				"Subscription": &Subscription{Interval: "monthly", Status: "ACTIVE"},
			},
			want: []string{
				"No payment recorded yet",
				"PayPal has not scheduled the next payment yet",
			},
			deny: []string{"January 1, 1"},
		},
		{
			// The reader is told who owns the draft, not who last touched it —
			// the two are not the same and only the first is known here.
			name: "bill_readonly.html",
			body: map[string]any{
				"Title": "a bill", "User": user, "Profile": profile, "Document": document,
				"OwnerName": "Ada Drafter", "CanExport": true,
				"Bill": template.HTML(`<div class="bill-doc"></div>`),
			},
			want: []string{
				"Read only",
				"owned by Ada Drafter",
				`data-export="download-text"`,
			},
			deny: []string{
				"drafter@example.com",
				`data-export="download-text" disabled`,
				"Export requires",
			},
		},
		{
			// Nobody involved pays, so the exports are shown locked rather
			// than left live to fail, and nothing is said in the header.
			name: "bill_readonly.html",
			body: map[string]any{
				"Title": "a bill", "User": user, "Profile": profile, "Document": document,
				"OwnerName": "Ada Drafter", "CanExport": false,
				"Bill": template.HTML(`<div class="bill-doc"></div>`),
			},
			want: []string{
				`data-export="download-text" disabled`,
				"bi-lock-fill",
				"Export requires",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := newTemplateWithBase(fs, "editor_base.html", tc.name)
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, tc.name, tc.body); err != nil {
				t.Fatalf("render: %s", err)
			}
			got := out.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(got, deny) {
					t.Errorf("unexpectedly present: %q", deny)
				}
			}
			// A share dialog that offers Save/Cancel has not been migrated to
			// saving as you go.
			if strings.Contains(got, "btn-share-save") {
				t.Error("share dialog still has a save button")
			}
		})
	}
}
