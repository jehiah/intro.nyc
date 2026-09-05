package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEditorTestingAuthDisabledOutsideDevMode(t *testing.T) {
	a := &App{devMode: false}
	req := httptest.NewRequest("GET", "/_admin/testing/auth?email=drafter@example.com", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("cookies set outside dev mode: %v", w.Result().Cookies())
	}
}

func TestEditorTestingAuthRequiresEmail(t *testing.T) {
	a := &App{devMode: true}
	req := httptest.NewRequest("GET", "/_admin/testing/auth", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEditorTestingAuthMintsSession(t *testing.T) {
	a := &App{devMode: true}
	req := httptest.NewRequest("GET", "/_admin/testing/auth?email=Drafter@Example.com", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/" {
		t.Fatalf("redirect = %q, want /", got)
	}

	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("cookies = %v, want one %q cookie", cookies, sessionCookie)
	}

	// The minted cookie must round-trip through User() without touching
	// Firebase, since a.firebaseAuth is nil in this test.
	verify := httptest.NewRequest("GET", "/", nil)
	verify.AddCookie(cookies[0])
	user := a.User(verify)
	if !user.SignedIn() {
		t.Fatal("User() did not recognize the minted session")
	}
	if user.Email != "drafter@example.com" {
		t.Errorf("Email = %q, want lowercased drafter@example.com", user.Email)
	}
	if user.UID == "" {
		t.Error("UID is empty")
	}
}

func TestEditorTestingAuthStableUID(t *testing.T) {
	one := devTestUser("same@example.com", "")
	two := devTestUser("same@example.com", "")
	if one.UID != two.UID {
		t.Errorf("devTestUser is not stable: %q != %q", one.UID, two.UID)
	}
}

func TestEditorTestingAuthPlanOverride(t *testing.T) {
	a := &App{devMode: true}
	req := httptest.NewRequest("GET", "/_admin/testing/auth?email=drafter@example.com&plan=complimentary", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	verify := httptest.NewRequest("GET", "/", nil)
	verify.AddCookie(w.Result().Cookies()[0])
	user := a.User(verify)
	if user.Plan != PlanPlus {
		t.Errorf("Plan = %q, want %q", user.Plan, PlanPlus)
	}
	plan, err := a.planFor(req.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if plan != PlanPlus {
		t.Errorf("planFor() = %q, want %q", plan, PlanPlus)
	}
}

func TestEditorTestingAuthRejectsUnknownPlan(t *testing.T) {
	a := &App{devMode: true}
	req := httptest.NewRequest("GET", "/_admin/testing/auth?email=drafter@example.com&plan=gold", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestEditorTestingAuthDefaultPlanIsUnset(t *testing.T) {
	// No ?plan= means planFor should fall through to its normal (Firestore)
	// lookup rather than being overridden, so ordinary dev-mode sign-ins are
	// unaffected by this test-only escape hatch.
	user := devTestUser("drafter@example.com", "")
	if user.Plan != "" {
		t.Errorf("Plan = %q, want empty", user.Plan)
	}
}

func TestEditorTestingAuthRedirectParam(t *testing.T) {
	a := &App{devMode: true}
	req := httptest.NewRequest("GET", "/_admin/testing/auth?email=a@example.com&redirect=/d/abc", nil)
	w := httptest.NewRecorder()
	a.EditorTestingAuth(w, req)
	if got := w.Header().Get("Location"); got != "/d/abc" {
		t.Fatalf("redirect = %q, want /d/abc", got)
	}
}

func TestDecodeDevSessionRejectsGarbage(t *testing.T) {
	if _, ok := decodeDevSession("not-a-dev-session"); ok {
		t.Error("decodeDevSession accepted a non-dev-session value")
	}
	if _, ok := decodeDevSession(""); ok {
		t.Error("decodeDevSession accepted an empty value")
	}
}

func TestUserIgnoresDevSessionOutsideDevMode(t *testing.T) {
	a := &App{devMode: true}
	value, err := encodeDevSession(devTestUser("drafter@example.com", ""))
	if err != nil {
		t.Fatal(err)
	}
	a.devMode = false

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
	// firebaseAuth is nil, so a real verification attempt would panic/fail;
	// User() must return nil rather than trying it against a dev cookie.
	if user := a.User(req); user.SignedIn() {
		t.Fatal("User() honored a dev session cookie outside dev mode")
	}
}
