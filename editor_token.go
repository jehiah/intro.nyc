package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// API tokens are an alternative to the session cookie for the JSON API and the
// MCP endpoint. They are opt-in: a profile has no token until the drafter
// enables integrations, so existing accounts gain nothing they did not ask for.
//
// The token itself lives on the profile, because the drafter needs to be able
// to read it back. A separate collection indexes the SHA-256 of the token to
// its owner, so a request carrying a bearer token resolves in one lookup
// without scanning profiles.

const tokenCollection = "editor_api_tokens"
const tokenPrefix = "intro_"
const tokenCacheTTL = time.Minute * 5

var errNoToken = errors.New("no such API token")

type tokenIndex struct {
	UID     UID       `firestore:"UID"`
	Created time.Time `firestore:"Created"`
}

type cachedToken struct {
	user   *SessionUser
	loaded time.Time
}

// tokenKey is what the index is keyed by. The raw token is never a document id.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newAPIToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(b), nil
}

// enableAPIToken issues a token for the profile, replacing any existing one.
func (a *App) enableAPIToken(ctx context.Context, u *SessionUser) (*Profile, error) {
	profile, err := a.profileFor(ctx, u)
	if err != nil {
		return nil, err
	}
	token, err := newAPIToken()
	if err != nil {
		return nil, err
	}

	// Index first: a token that resolves but is not yet shown is harmless,
	// whereas a token shown but not indexed would simply fail to authenticate.
	_, err = a.firestore.Collection(tokenCollection).Doc(tokenKey(token)).Set(ctx, tokenIndex{
		UID:     u.UID,
		Created: time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}

	previous := profile.APIToken
	updated := *profile
	updated.APIToken = token
	updated.LastModified = time.Now().UTC()
	if _, err := a.firestore.Collection(profileCollection).Doc(string(u.UID)).Set(ctx, updated); err != nil {
		return nil, err
	}
	a.cacheProfile(&updated)
	a.forgetToken(previous)
	return &updated, nil
}

// revokeAPIToken removes the token from the profile and the index.
func (a *App) revokeAPIToken(ctx context.Context, u *SessionUser) (*Profile, error) {
	profile, err := a.profileFor(ctx, u)
	if err != nil {
		return nil, err
	}
	previous := profile.APIToken

	updated := *profile
	updated.APIToken = ""
	updated.LastModified = time.Now().UTC()
	if _, err := a.firestore.Collection(profileCollection).Doc(string(u.UID)).Set(ctx, updated); err != nil {
		return nil, err
	}
	a.cacheProfile(&updated)

	if previous != "" {
		if _, err := a.firestore.Collection(tokenCollection).Doc(tokenKey(previous)).Delete(ctx); err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
		}
		a.forgetToken(previous)
	}
	return &updated, nil
}

// userForToken resolves a bearer token to the profile that owns it.
func (a *App) userForToken(ctx context.Context, token string) (*SessionUser, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return nil, errNoToken
	}
	key := tokenKey(token)

	a.tokenMutex.RLock()
	cached, ok := a.tokens[key]
	a.tokenMutex.RUnlock()
	if ok && time.Since(cached.loaded) < tokenCacheTTL {
		return cached.user, nil
	}

	snapshot, err := a.firestore.Collection(tokenCollection).Doc(key).Get(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, errNoToken
		}
		return nil, err
	}
	var index tokenIndex
	if err := snapshot.DataTo(&index); err != nil {
		return nil, err
	}

	doc, err := a.firestore.Collection(profileCollection).Doc(string(index.UID)).Get(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, errNoToken
		}
		return nil, err
	}
	var profile Profile
	if err := doc.DataTo(&profile); err != nil {
		return nil, err
	}
	// The profile is authoritative: a revoked token must stop working even if
	// its index entry outlives it.
	if profile.APIToken != token {
		return nil, errNoToken
	}

	user := &SessionUser{UID: profile.UID, Email: profile.Email, Name: profile.Name}
	a.tokenMutex.Lock()
	a.tokens[key] = &cachedToken{user: user, loaded: time.Now()}
	a.tokenMutex.Unlock()
	return user, nil
}

func (a *App) forgetToken(token string) {
	if token == "" {
		return
	}
	a.tokenMutex.Lock()
	delete(a.tokens, tokenKey(token))
	a.tokenMutex.Unlock()
}

// bearerToken returns the token from an Authorization header, if any.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
