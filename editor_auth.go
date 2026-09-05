package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Authentication mirrors legislation.support: Firebase issues an ID token in
// the browser, the server exchanges it for a session cookie, and every request
// is identified by verifying that cookie.

const sessionCookie = "session"
const sessionTTL = time.Hour * 24 * 13

type UID string

// SessionUser is the signed-in identity. Email is carried because documents are
// shared by email address rather than by UID; Name seeds a new profile.
type SessionUser struct {
	UID   UID
	Email string
	Name  string
	// Plan overrides planFor's normal Firestore-backed lookup when non-empty.
	// It is only ever set on a dev-test session (EditorTestingAuth's ?plan=),
	// so it has no effect on a real, Firebase-authenticated user.
	Plan string
}

func (u *SessionUser) SignedIn() bool { return u != nil && u.UID != "" }

// User returns the signed-in user, or nil.
//
// A bearer token is accepted as an alternative to the session cookie, so the
// JSON API and the MCP endpoint authenticate the same way as the web app.
func (a *App) User(r *http.Request) *SessionUser {
	if token := bearerToken(r); token != "" {
		user, err := a.userForToken(r.Context(), token)
		if err != nil {
			if err != errNoToken {
				log.Printf("api token: %s", err)
			}
			return nil
		}
		return user
	}

	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	if a.devMode {
		if user, ok := decodeDevSession(cookie.Value); ok {
			return user
		}
	}
	if a.firebaseAuth == nil {
		return nil
	}
	// VerifySessionCookieAndCheckRevoked would make a server side call on every
	// request; revocation is handled by the 13 day expiry instead.
	decoded, err := a.firebaseAuth.VerifySessionCookie(r.Context(), cookie.Value)
	if err != nil {
		return nil
	}
	email, _ := decoded.Claims["email"].(string)
	name, _ := decoded.Claims["name"].(string)
	return &SessionUser{
		UID:   UID(decoded.UID),
		Email: strings.ToLower(email),
		Name:  name,
	}
}

// EditorSignIn renders the sign-in page.
func (a *App) EditorSignIn(w http.ResponseWriter, r *http.Request) {
	a.renderEditor(w, r, "editor_susi.html", map[string]any{
		"Title": "Sign in",
		"User":  a.User(r),
	})
}

// EditorSignOut clears the session cookie.
func (a *App) EditorSignOut(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		MaxAge:   0,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})
	http.Redirect(w, r, "/", 302)
}

// EditorNewSession exchanges a Firebase ID token for a session cookie.
func (a *App) EditorNewSession(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "invalid json", 422)
		return
	}
	if a.firebaseAuth == nil {
		http.Error(w, "authentication is not configured", 500)
		return
	}

	cookie, err := a.firebaseAuth.SessionCookie(r.Context(), body.IDToken, sessionTTL)
	if err != nil {
		log.Printf("session cookie: %s", err)
		http.Error(w, "Failed to create a session cookie", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})
	w.Header().Set("content-type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

// devSessionPrefix marks a session cookie minted by EditorTestingAuth rather
// than by Firebase, so User() can tell the two apart without ambiguity: a
// Firebase session cookie is a JWT and never contains a colon this early.
const devSessionPrefix = "devtest:"

// EditorTestingAuth mints a session for the given email without a real
// Firebase sign-in flow, so browser tests can sign in with one request. It
// only responds when the server is run with -dev-mode; in production it 404s
// regardless of what a request sends, since a.devMode is always false there.
func (a *App) EditorTestingAuth(w http.ResponseWriter, r *http.Request) {
	if !a.devMode {
		http.NotFound(w, r)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	if email == "" {
		http.Error(w, "email is required", 400)
		return
	}
	plan, err := devPlan(r.URL.Query().Get("plan"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	user := devTestUser(email, plan)
	log.Printf("testing auth: signed in as %s (uid=%s, plan=%s)", user.Email, user.UID, user.Plan)

	value, err := encodeDevSession(user)
	if err != nil {
		log.Printf("testing auth: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})
	redirectTo := r.URL.Query().Get("redirect")
	if redirectTo == "" {
		redirectTo = "/"
	}
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

// devPlan maps EditorTestingAuth's ?plan= value to the internal plan constant
// planFor understands. "complimentary" is the name a test uses to ask for
// Plus-gated features (export, sharing beyond one person, a public link)
// without a real PayPal subscription; "plus" is accepted as a synonym.
func devPlan(value string) (string, error) {
	switch value {
	case "":
		return "", nil
	case "plus", "complimentary":
		return PlanPlus, nil
	case "free":
		return PlanFree, nil
	default:
		return "", fmt.Errorf("unrecognized plan %q (want complimentary, plus, or free)", value)
	}
}

// devTestUser derives a stable identity from an email address, so signing in
// as the same test address twice reaches the same drafts.
func devTestUser(email, plan string) *SessionUser {
	sum := sha256.Sum256([]byte(email))
	return &SessionUser{
		UID:   UID("test-" + hex.EncodeToString(sum[:8])),
		Email: email,
		Name:  "Test User",
		Plan:  plan,
	}
}

func encodeDevSession(u *SessionUser) (string, error) {
	b, err := json.Marshal(u)
	if err != nil {
		return "", err
	}
	return devSessionPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeDevSession reverses encodeDevSession. It reports ok=false for any
// cookie value it did not mint, including every real Firebase session cookie.
func decodeDevSession(value string) (u *SessionUser, ok bool) {
	rest, found := strings.CutPrefix(value, devSessionPrefix)
	if !found {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, false
	}
	return u, true
}

// authDomain is the domain Firebase redirects through. Requests to /__/auth/
// are proxied to the Firebase-hosted helpers so the flow stays first-party,
// which Safari requires.
func (a *App) authDomain(r *http.Request) string {
	if a.devMode {
		return "editor.dev.intro.nyc"
	}
	return "editor.intro.nyc"
}
