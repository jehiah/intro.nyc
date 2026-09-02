package main

import (
	"context"
	"strings"
	"time"

	"google.golang.org/api/iterator"
)

const profileCollection = "editor_profiles"
const profileCacheTTL = time.Minute * 5

// Profile is the account-level information a drafter can edit. It is separate
// from the identity provider's record because the display name shown to
// colleagues should be the drafter's choice.
type Profile struct {
	UID   UID    `firestore:"UID"`
	Email string `firestore:"Email"`
	Name  string `firestore:"Name"`

	Created      time.Time `firestore:"Created"`
	LastModified time.Time `firestore:"LastModified"`
}

// DisplayName is what to show wherever a person is named.
func (p *Profile) DisplayName() string {
	if p == nil {
		return ""
	}
	return displayName(p.Name, p.Email)
}

// displayName falls back to the local part of the address, which reads better
// than a full address in a nav bar.
func displayName(name, email string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
		return local
	}
	return email
}

type cachedProfile struct {
	profile *Profile
	loaded  time.Time
}

// profileFor returns the signed-in user's profile, creating it on first sight
// seeded from the name the identity provider supplied.
func (a *App) profileFor(ctx context.Context, u *SessionUser) (*Profile, error) {
	a.profileMutex.RLock()
	cached, ok := a.profiles[u.UID]
	a.profileMutex.RUnlock()
	if ok && time.Since(cached.loaded) < profileCacheTTL {
		return cached.profile, nil
	}

	doc := a.firestore.Collection(profileCollection).Doc(string(u.UID))
	snapshot, err := doc.Get(ctx)

	var profile Profile
	switch {
	case err != nil && !isNotFound(err):
		return nil, err
	case err != nil || !snapshot.Exists():
		now := time.Now().UTC()
		profile = Profile{
			UID:          u.UID,
			Email:        u.Email,
			Name:         displayName(u.Name, u.Email),
			Created:      now,
			LastModified: now,
		}
		if _, err := doc.Set(ctx, profile); err != nil {
			return nil, err
		}
	default:
		if err := snapshot.DataTo(&profile); err != nil {
			return nil, err
		}
		// The address can change at the provider; the profile follows it.
		if profile.Email != u.Email && u.Email != "" {
			profile.Email = u.Email
			profile.LastModified = time.Now().UTC()
			if _, err := doc.Set(ctx, profile); err != nil {
				return nil, err
			}
		}
	}

	a.cacheProfile(&profile)
	return &profile, nil
}

func (a *App) cacheProfile(p *Profile) {
	a.profileMutex.Lock()
	a.profiles[p.UID] = &cachedProfile{profile: p, loaded: time.Now()}
	a.profileMutex.Unlock()
}

func (a *App) saveProfileName(ctx context.Context, u *SessionUser, name string) (*Profile, error) {
	profile, err := a.profileFor(ctx, u)
	if err != nil {
		return nil, err
	}
	updated := *profile
	updated.Name = strings.TrimSpace(name)
	updated.LastModified = time.Now().UTC()

	if _, err := a.firestore.Collection(profileCollection).Doc(string(u.UID)).Set(ctx, updated); err != nil {
		return nil, err
	}
	a.cacheProfile(&updated)
	return &updated, nil
}

// namesFor resolves display names for addresses that have a profile. Addresses
// without one are simply absent, and are shown as the bare address.
func (a *App) namesFor(ctx context.Context, emails []string) map[string]string {
	names := make(map[string]string)
	// Firestore allows at most 30 values in an "in" filter.
	const batch = 30
	for start := 0; start < len(emails); start += batch {
		values := make([]any, 0, batch)
		for _, email := range emails[start:min(start+batch, len(emails))] {
			values = append(values, email)
		}
		iter := a.firestore.Collection(profileCollection).
			Where("Email", "in", values).Documents(ctx)
		for {
			snapshot, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				iter.Stop()
				return names
			}
			var p Profile
			if err := snapshot.DataTo(&p); err == nil && p.Name != "" {
				names[p.Email] = p.Name
			}
		}
		iter.Stop()
	}
	return names
}
