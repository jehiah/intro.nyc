package main

import (
	"encoding/json"
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
// shared by email address rather than by UID.
type SessionUser struct {
	UID   UID
	Email string
}

func (u *SessionUser) SignedIn() bool { return u != nil && u.UID != "" }

// User returns the signed-in user, or nil.
func (a *App) User(r *http.Request) *SessionUser {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
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
	return &SessionUser{UID: UID(decoded.UID), Email: strings.ToLower(email)}
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

// authDomain is the domain Firebase redirects through. Requests to /__/auth/
// are proxied to the Firebase-hosted helpers so the flow stays first-party,
// which Safari requires.
func (a *App) authDomain(r *http.Request) string {
	if a.devMode {
		return "editor.dev.intro.nyc"
	}
	return "editor.intro.nyc"
}
