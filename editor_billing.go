package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/iterator"
)

// Plus subscriptions are managed through PayPal Subscriptions: a drafter
// approves one of two plans (monthly or annual) with the PayPal JS SDK, the
// browser hands us the resulting subscription id, and we confirm it directly
// with PayPal before granting access. From then on PayPal's webhook keeps the
// subscription's status current as it renews, lapses or is cancelled.

const subscriptionCollection = "editor_subscriptions"
const subscriptionCacheTTL = time.Minute * 5

const (
	PlanFree = "free"
	PlanPlus = "plus"
)

// Free-plan limits. Rule references don't apply here — these are product
// limits, not drafting-manual rules.
const (
	maxFreeDrafts        = 5
	maxFreeCollaborators = 1 // editors + viewers combined; public sharing is Plus-only
)

// The Plus plans, as created in the PayPal dashboard. Sandbox and live are
// separate PayPal accounts, so the plans have separate ids; --dev-mode runs
// against the sandbox.
const (
	planMonthlyLive    = "P-6WS71638342969041NKM3MGQ"
	planAnnualLive     = "P-6UN27905MG812970UNKM3RSY"
	planMonthlySandbox = "P-08J5312066268080RNKNCDUI"
	planAnnualSandbox  = "P-5R223784WU9606019NKNCECI"
)

// PlusPlan is how an interval is described to a drafter: on the upgrade offer
// before they subscribe, and on the billing status afterwards. Both read from
// the same table so the two can never quote different prices.
type PlusPlan struct {
	Interval string // "monthly" or "annual", matching Subscription.Interval
	Label    string
	Price    string // what PayPal charges each cycle
	Per      string // the cycle that price covers
	Note     string // the comparison against monthly, where there is one
}

var plusPlans = []PlusPlan{
	{Interval: "monthly", Label: "Monthly", Price: "$6.99", Per: "month"},
	{Interval: "annual", Label: "Annual", Price: "$70", Per: "year",
		Note: "$5.83/month — a 20% discount over monthly"},
}

func plusPlanFor(interval string) *PlusPlan {
	for i, p := range plusPlans {
		if p.Interval == interval {
			return &plusPlans[i]
		}
	}
	return nil
}

// What Plus buys, listed both on the offer and on the status so a subscriber
// can see what they are paying for without going back to the sales copy.
var plusFeatures = []string{
	"Unlimited drafts",
	"Share a draft with any number of people, or publish a public link",
	"Every export: Word, plain text, markdown, HTML, as adopted, and JSON",
}

// Subscription is a drafter's PayPal subscription record. Only Plus drafters
// have one; a free account simply has none.
type Subscription struct {
	UID   UID    `firestore:"UID"`
	Email string `firestore:"Email"`

	Interval string `firestore:"Interval"` // "monthly" or "annual"

	PayPalPlanID         string `firestore:"PayPalPlanID"`
	PayPalSubscriptionID string `firestore:"PayPalSubscriptionID"`
	Status               string `firestore:"Status"` // a PayPal subscription status, e.g. ACTIVE, CANCELLED

	// Billing detail as PayPal last reported it. Mirrored here so the billing
	// page can still answer "when was I charged, and when does it renew"
	// while PayPal is unreachable.
	LastPayment       time.Time `firestore:"LastPayment"`
	LastPaymentAmount string    `firestore:"LastPaymentAmount"` // e.g. "70.00 USD"
	NextBilling       time.Time `firestore:"NextBilling"`

	// AccessUntil keeps a canceled subscription working through the cycle it
	// has already been charged for. PayPal does not refund that cycle, so
	// cutting Plus off the moment someone cancels would take back something
	// they paid for.
	AccessUntil time.Time `firestore:"AccessUntil"`

	Created      time.Time `firestore:"Created"`
	LastModified time.Time `firestore:"LastModified"`
}

// Active reports whether this subscription currently grants Plus access.
func (s *Subscription) Active() bool {
	if s == nil {
		return false
	}
	return s.Status == "ACTIVE" || s.Canceling()
}

// Canceling reports a subscription that will not renew but is still paid
// through the end of its cycle.
func (s *Subscription) Canceling() bool {
	return s != nil && s.Status == "CANCELLED" && time.Now().Before(s.AccessUntil)
}

// Plan is the plan this subscription grants, for templates.
func (s *Subscription) Plan() string { return planOf(s) }

// PlusPlan describes what the subscriber is paying for: the interval, its
// price and how that price is billed. It is nil for an interval we no longer
// offer, so callers must check.
func (s *Subscription) PlusPlan() *PlusPlan {
	if s == nil {
		return nil
	}
	return plusPlanFor(s.Interval)
}

// Pending reports that PayPal has taken the approval but has not yet made the
// subscription active. That gap is usually seconds — the BILLING.SUBSCRIPTION
// .ACTIVATED webhook closes it — so the drafter is told to wait rather than
// shown the upgrade offer they just accepted.
func (s *Subscription) Pending() bool {
	return s != nil && (s.Status == "APPROVAL_PENDING" || s.Status == "APPROVED")
}

// planOf is the plan a subscription record implies.
func planOf(s *Subscription) string {
	if s.Active() {
		return PlanPlus
	}
	return PlanFree
}

type cachedSubscription struct {
	subscription *Subscription
	loaded       time.Time
}

// subscriptionFor returns the signed-in user's subscription, or nil if they
// have none (a free account).
func (a *App) subscriptionFor(ctx context.Context, u *SessionUser) (*Subscription, error) {
	if !u.SignedIn() {
		return nil, nil
	}
	return a.subscriptionForUID(ctx, u.UID)
}

func (a *App) subscriptionForUID(ctx context.Context, uid UID) (*Subscription, error) {
	a.subscriptionMutex.RLock()
	cached, ok := a.subscriptions[uid]
	a.subscriptionMutex.RUnlock()
	if ok && time.Since(cached.loaded) < subscriptionCacheTTL {
		return cached.subscription, nil
	}

	snapshot, err := a.firestore.Collection(subscriptionCollection).Doc(string(uid)).Get(ctx)
	if err != nil && !isNotFound(err) {
		return nil, err
	}

	var sub *Subscription
	if err == nil && snapshot.Exists() {
		sub = &Subscription{}
		if err := snapshot.DataTo(sub); err != nil {
			return nil, err
		}
	}
	a.cacheSubscription(uid, sub)
	return sub, nil
}

func (a *App) cacheSubscription(uid UID, s *Subscription) {
	a.subscriptionMutex.Lock()
	a.subscriptions[uid] = &cachedSubscription{subscription: s, loaded: time.Now()}
	a.subscriptionMutex.Unlock()
}

func (a *App) putSubscription(ctx context.Context, s *Subscription) error {
	if _, err := a.firestore.Collection(subscriptionCollection).Doc(string(s.UID)).Set(ctx, s); err != nil {
		return err
	}
	a.cacheSubscription(s.UID, s)
	return nil
}

// refreshSubscription re-reads a subscription from PayPal so the billing page
// quotes the dates PayPal actually holds rather than whatever the last webhook
// left behind. PayPal being slow or down must not take the billing page with
// it, so a failure logs and falls back to the stored record.
func (a *App) refreshSubscription(ctx context.Context, sub *Subscription) *Subscription {
	if sub == nil || sub.PayPalSubscriptionID == "" || !a.paypal.configured() {
		return sub
	}
	remote, err := a.paypal.getSubscription(ctx, sub.PayPalSubscriptionID)
	if err != nil {
		log.Printf("paypal refresh %s: %s", sub.PayPalSubscriptionID, err)
		return sub
	}
	updated := *sub
	remote.applyTo(&updated)
	if updated.Status == sub.Status &&
		updated.LastPaymentAmount == sub.LastPaymentAmount &&
		updated.LastPayment.Equal(sub.LastPayment) &&
		updated.NextBilling.Equal(sub.NextBilling) &&
		updated.AccessUntil.Equal(sub.AccessUntil) {
		return sub // nothing moved; do not spend a write on every page view
	}
	updated.LastModified = time.Now().UTC()
	if err := a.putSubscription(ctx, &updated); err != nil {
		log.Printf("editor billing save: %s", err)
	}
	return &updated
}

// planFor is the plan ("free" or "plus") that gates a signed-in user's access.
func (a *App) planFor(ctx context.Context, u *SessionUser) (string, error) {
	if u != nil && u.Plan != "" {
		return u.Plan, nil
	}
	sub, err := a.subscriptionFor(ctx, u)
	if err != nil {
		return PlanFree, err
	}
	return planOf(sub), nil
}

// canExport reports whether the reader may use the download menu.
//
// Export is a Plus feature, but it follows the document as well as the reader:
// a bill an owner pays to keep can be exported by everyone they shared it
// with, and a Plus reader can export anything they are allowed to read. Either
// side of the share paying is enough.
func (a *App) canExport(ctx context.Context, d *Document, u *SessionUser) (bool, error) {
	if u.SignedIn() {
		plan, err := a.planFor(ctx, u)
		if err != nil {
			return false, err
		}
		if plan == PlanPlus {
			return true, nil
		}
		if d.UID == u.UID {
			return false, nil // the reader is the owner, already asked about
		}
	}
	sub, err := a.subscriptionForUID(ctx, d.UID)
	if err != nil {
		return false, err
	}
	return planOf(sub) == PlanPlus, nil
}

/* --------------------------------------------------------- free-plan limits */

// draftCountAtLeast reports whether the drafter owns at least n drafts,
// without paying for a full count when the answer is already clear at n.
func (a *App) draftCountAtLeast(ctx context.Context, uid UID, n int) (bool, error) {
	iter := a.firestore.Collection(documentCollection).
		Where("UID", "==", string(uid)).Limit(n).Documents(ctx)
	defer iter.Stop()
	count := 0
	for {
		_, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return false, err
		}
		count++
	}
	return count >= n, nil
}

// requireDraftCapacity enforces the free-plan draft limit, replying itself
// (as JSON for the API, a redirect for the HTML form) when it is exceeded.
func (a *App) requireDraftCapacity(w http.ResponseWriter, r *http.Request, user *SessionUser) bool {
	plan, err := a.planFor(r.Context(), user)
	if err != nil {
		log.Printf("editor billing: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return false
	}
	if plan == PlanPlus {
		return true
	}
	atLimit, err := a.draftCountAtLeast(r.Context(), user.UID, maxFreeDrafts)
	if err != nil {
		log.Printf("editor billing: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return false
	}
	if !atLimit {
		return true
	}
	message := fmt.Sprintf("Free accounts are limited to %d drafts. Upgrade at /billing to add more.", maxFreeDrafts)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, message, 402)
	} else {
		http.Redirect(w, r, "/billing?limit=drafts", 302)
	}
	return false
}

// requireShareCapacity enforces the free-plan sharing limit against the
// sharing a request is about to set. A draft that is already shared more
// widely than the free plan allows — because the owner let a subscription
// lapse — may still be narrowed; only widening it is refused. Otherwise a
// lapsed drafter could not un-share their own work.
func (a *App) requireShareCapacity(w http.ResponseWriter, r *http.Request, user *SessionUser, current *Document, editors, viewers []string, public bool) bool {
	plan, err := a.planFor(r.Context(), user)
	if err != nil {
		log.Printf("editor billing: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return false
	}
	if plan == PlanPlus {
		return true
	}
	was := len(current.Editors) + len(current.Viewers)
	now := len(editors) + len(viewers)
	if now > maxFreeCollaborators && now >= was {
		http.Error(w, fmt.Sprintf(
			"Free accounts can share a draft with %d person. Upgrade at /billing to share more widely.",
			maxFreeCollaborators,
		), 402)
		return false
	}
	if public && !current.Public {
		http.Error(w, "A public link requires Plus. Upgrade at /billing.", 402)
		return false
	}
	return true
}

/* --------------------------------------------------------------- handlers */

// EditorBilling shows the drafter's plan and, for a free account, the PayPal
// buttons to become Plus.
func (a *App) EditorBilling(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Redirect(w, r, "/", 302)
		return
	}
	sub, err := a.subscriptionFor(r.Context(), user)
	if err != nil {
		log.Printf("editor billing: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	sub = a.refreshSubscription(r.Context(), sub)

	plan := planOf(sub)
	a.renderEditor(w, r, "editor_billing.html", map[string]any{
		"Title":        "Billing",
		"User":         user,
		"Subscription": sub,
		"Plan":         plan,
		"Pending":      sub.Pending(),
		"Canceling":    sub.Canceling(),
		"Suspended":    sub != nil && sub.Status == "SUSPENDED",
		"PlusPlan":     sub.PlusPlan(),
		"Features":     plusFeatures,
		"Plans":        plusPlans,
		// The PayPal buttons are only rendered when there is something to
		// subscribe to; a half-finished subscription is waited on instead.
		"ShowButtons":    plan != PlanPlus && !sub.Pending() && a.paypal.configured(),
		"PayPalClientID": a.paypal.clientID,
		"PlanIDMonthly":  a.paypal.planMonthly,
		"PlanIDAnnual":   a.paypal.planAnnual,
		"Limit":          r.URL.Query().Get("limit"),
	})
}

// EditorBillingSubscribe records a subscription the drafter just approved
// through the PayPal button. It confirms the subscription with PayPal itself
// rather than trusting the browser: the plan id must be one of ours and the
// subscription's custom_id (set to the drafter's uid when the button created
// it) must match the signed-in drafter.
func (a *App) EditorBillingSubscribe(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Error(w, "401 Sign in required", 401)
		return
	}
	if !a.paypal.configured() {
		http.Error(w, "Billing is not configured", 500)
		return
	}
	var req struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil || req.SubscriptionID == "" {
		http.Error(w, "Bad Request", 400)
		return
	}

	remote, err := a.paypal.getSubscription(r.Context(), req.SubscriptionID)
	if err != nil {
		log.Printf("paypal get subscription: %s", err)
		http.Error(w, "Could not verify the subscription with PayPal", 502)
		return
	}
	if remote.CustomID != string(user.UID) {
		http.Error(w, "Forbidden", 403)
		return
	}
	var interval string
	switch remote.PlanID {
	case a.paypal.planMonthly:
		interval = "monthly"
	case a.paypal.planAnnual:
		interval = "annual"
	default:
		http.Error(w, "Bad Request", 400)
		return
	}
	switch remote.Status {
	case "ACTIVE", "APPROVAL_PENDING", "APPROVED":
	default:
		http.Error(w, "That subscription is not active", 400)
		return
	}

	// A fresh record: subscribing again after cancelling must not inherit the
	// old subscription's paid-through date or payment history.
	now := time.Now().UTC()
	record := &Subscription{
		UID: user.UID, Email: user.Email,
		Interval:     interval,
		Created:      now,
		LastModified: now,
	}
	remote.applyTo(record)
	if existing, err := a.subscriptionFor(r.Context(), user); err == nil && existing != nil {
		record.Created = existing.Created
	}
	if err := a.putSubscription(r.Context(), record); err != nil {
		log.Printf("editor billing save: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"plan": planOf(record), "status": record.Status})
}

// EditorBillingCancel cancels the drafter's Plus subscription with PayPal.
func (a *App) EditorBillingCancel(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Error(w, "401 Sign in required", 401)
		return
	}
	sub, err := a.subscriptionFor(r.Context(), user)
	if err != nil {
		log.Printf("editor billing: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	if sub == nil || sub.PayPalSubscriptionID == "" {
		http.Error(w, "No subscription to cancel", 400)
		return
	}
	// Read the renewal date before cancelling: once cancelled PayPal stops
	// reporting one, and it is the date the drafter stays on Plus through.
	sub = a.refreshSubscription(r.Context(), sub)

	if err := a.paypal.cancelSubscription(r.Context(), sub.PayPalSubscriptionID, "canceled by drafter"); err != nil {
		log.Printf("paypal cancel: %s", err)
		http.Error(w, "Could not cancel with PayPal", 502)
		return
	}
	sub.Status = "CANCELLED"
	if sub.NextBilling.After(time.Now()) {
		sub.AccessUntil = sub.NextBilling
	}
	sub.LastModified = time.Now().UTC()
	if err := a.putSubscription(r.Context(), sub); err != nil {
		log.Printf("editor billing save: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"plan": planOf(sub)})
}

// EditorPayPalWebhook keeps subscription status current as PayPal reports it:
// renewals, cancellations, suspensions (a failed payment) and expirations.
// The bill drafted while a subscription lapses is untouched; only the Plus
// features stop being offered.
func (a *App) EditorPayPalWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	if !a.paypal.configured() || a.paypal.webhookID == "" {
		log.Printf("paypal webhook: not configured")
		http.Error(w, "Not configured", 500)
		return
	}
	ok, err := a.paypal.verifyWebhookSignature(r.Context(), r.Header, body)
	if err != nil {
		log.Printf("paypal webhook verify: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	if !ok {
		http.Error(w, "Forbidden", 403)
		return
	}

	var event struct {
		EventType string             `json:"event_type"`
		Resource  paypalSubscription `json:"resource"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}

	switch event.EventType {
	case "BILLING.SUBSCRIPTION.ACTIVATED", "BILLING.SUBSCRIPTION.UPDATED",
		"BILLING.SUBSCRIPTION.CANCELLED", "BILLING.SUBSCRIPTION.SUSPENDED",
		"BILLING.SUBSCRIPTION.EXPIRED":
		if event.Resource.CustomID == "" {
			log.Printf("paypal webhook: %s for subscription %s has no custom_id", event.EventType, event.Resource.ID)
			break
		}
		// Never record an empty status: that would read as "not Plus" and
		// silently revoke a paying drafter's access.
		if event.Resource.Status == "" {
			log.Printf("paypal webhook: %s for subscription %s has no status", event.EventType, event.Resource.ID)
			break
		}
		uid := UID(event.Resource.CustomID)
		existing, err := a.subscriptionForUID(r.Context(), uid)
		if err != nil {
			log.Printf("paypal webhook: %s", err)
			break
		}
		now := time.Now().UTC()
		if existing == nil {
			existing = &Subscription{UID: uid, Created: now}
		}
		event.Resource.applyTo(existing)
		if existing.Interval == "" {
			switch existing.PayPalPlanID {
			case a.paypal.planMonthly:
				existing.Interval = "monthly"
			case a.paypal.planAnnual:
				existing.Interval = "annual"
			}
		}
		existing.LastModified = now
		if err := a.putSubscription(r.Context(), existing); err != nil {
			log.Printf("paypal webhook save: %s", err)
		}
	}

	w.WriteHeader(200)
}

/* ------------------------------------------------------------ PayPal API */

// paypalClient talks to the PayPal REST API with the app's own credentials
// (client-credentials grant), not on behalf of any one drafter.
type paypalClient struct {
	clientID  string
	secret    string
	webhookID string
	apiBase   string

	planMonthly string
	planAnnual  string

	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

type paypalConfig struct {
	ClientID  string
	Secret    string
	WebhookID string
	Sandbox   bool
}

// newPayPalClient picks the API host and the plan ids together, so a sandbox
// run can never bill against a live plan.
func newPayPalClient(cfg paypalConfig) *paypalClient {
	apiBase, monthly, annual := "https://api-m.paypal.com", planMonthlyLive, planAnnualLive
	if cfg.Sandbox {
		apiBase, monthly, annual = "https://api-m.sandbox.paypal.com", planMonthlySandbox, planAnnualSandbox
	}
	return &paypalClient{
		clientID:    cfg.ClientID,
		secret:      cfg.Secret,
		webhookID:   cfg.WebhookID,
		apiBase:     apiBase,
		planMonthly: monthly,
		planAnnual:  annual,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *paypalClient) configured() bool {
	return p != nil && p.clientID != "" && p.secret != ""
}

// String describes the billing environment at startup without logging the
// secret.
func (p *paypalClient) String() string {
	if !p.configured() {
		return "not configured (set PAYPAL_CLIENT_ID and PAYPAL_SECRET)"
	}
	s := fmt.Sprintf("%s monthly:%s annual:%s", p.apiBase, p.planMonthly, p.planAnnual)
	if p.webhookID == "" {
		s += " (no PAYPAL_WEBHOOK_ID; webhook deliveries will be rejected)"
	}
	return s
}

// token returns a cached OAuth access token, refreshing it shortly before it
// expires.
func (p *paypalClient) token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.accessToken, nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(p.clientID, p.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("paypal oauth: %s: %s", resp.Status, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	p.accessToken = out.AccessToken
	p.tokenExpiry = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	return p.accessToken, nil
}

// do makes an authenticated call against the PayPal REST API.
func (p *paypalClient) do(ctx context.Context, method, path string, body, out any) error {
	token, err := p.token(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.apiBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("paypal %s %s: %s: %s", method, path, resp.Status, respBody)
	}
	if out != nil && len(respBody) > 0 {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

type paypalSubscription struct {
	ID          string `json:"id"`
	PlanID      string `json:"plan_id"`
	Status      string `json:"status"`
	CustomID    string `json:"custom_id"`
	BillingInfo struct {
		NextBillingTime string `json:"next_billing_time"`
		LastPayment     struct {
			Time   string `json:"time"`
			Amount struct {
				CurrencyCode string `json:"currency_code"`
				Value        string `json:"value"`
			} `json:"amount"`
		} `json:"last_payment"`
	} `json:"billing_info"`
}

// applyTo copies what PayPal reports onto a stored record. Every field is
// copied only when PayPal actually sent it: a cancellation event carries no
// next_billing_time, and blanking the stored one would lose the date the
// drafter is paid through.
func (r *paypalSubscription) applyTo(s *Subscription) {
	if r.ID != "" {
		s.PayPalSubscriptionID = r.ID
	}
	if r.PlanID != "" {
		s.PayPalPlanID = r.PlanID
	}
	if r.Status != "" {
		s.Status = r.Status
	}
	if t, err := time.Parse(time.RFC3339, r.BillingInfo.NextBillingTime); err == nil {
		s.NextBilling = t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, r.BillingInfo.LastPayment.Time); err == nil {
		s.LastPayment = t.UTC()
	}
	if v := r.BillingInfo.LastPayment.Amount.Value; v != "" {
		s.LastPaymentAmount = strings.TrimSpace(v + " " + r.BillingInfo.LastPayment.Amount.CurrencyCode)
	}
	// A subscription that is no longer renewing is paid through the renewal
	// that will now never happen.
	if s.Status == "CANCELLED" && s.AccessUntil.IsZero() {
		s.AccessUntil = s.NextBilling
	}
}

func (p *paypalClient) getSubscription(ctx context.Context, id string) (*paypalSubscription, error) {
	var out paypalSubscription
	if err := p.do(ctx, "GET", "/v1/billing/subscriptions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *paypalClient) cancelSubscription(ctx context.Context, id, reason string) error {
	return p.do(ctx, "POST", "/v1/billing/subscriptions/"+url.PathEscape(id)+"/cancel", map[string]string{"reason": reason}, nil)
}

// verifyWebhookSignature asks PayPal to validate a webhook delivery, rather
// than reimplementing certificate-chain verification of the signature
// ourselves.
func (p *paypalClient) verifyWebhookSignature(ctx context.Context, headers http.Header, body []byte) (bool, error) {
	payload := map[string]any{
		"auth_algo":         headers.Get("Paypal-Auth-Algo"),
		"cert_url":          headers.Get("Paypal-Cert-Url"),
		"transmission_id":   headers.Get("Paypal-Transmission-Id"),
		"transmission_sig":  headers.Get("Paypal-Transmission-Sig"),
		"transmission_time": headers.Get("Paypal-Transmission-Time"),
		"webhook_id":        p.webhookID,
		"webhook_event":     json.RawMessage(body),
	}
	var out struct {
		VerificationStatus string `json:"verification_status"`
	}
	if err := p.do(ctx, "POST", "/v1/notifications/verify-webhook-signature", payload, &out); err != nil {
		return false, err
	}
	return out.VerificationStatus == "SUCCESS", nil
}
