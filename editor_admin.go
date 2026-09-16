package main

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"

	"google.golang.org/api/iterator"
)

// EditorAdminIndex lists the dev-only admin utilities. It only responds when
// the server is run with -dev-mode, matching EditorTestingAuth.
func (a *App) EditorAdminIndex(w http.ResponseWriter, r *http.Request) {
	if !a.devMode {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	a.renderEditor(w, r, "editor_admin.html", map[string]any{
		"Title":    "Admin",
		"Purged":   q.Has("purged"),
		"Accounts": q.Get("purged"),
		"Drafts":   q.Get("drafts"),
	})
}

// adminUserRow is one row of the /_admin/users listing.
type adminUserRow struct {
	*Profile
	Plan   string
	Drafts int
}

// EditorAdminUsers lists every editor profile with its plan and draft count,
// so a paying account never has to be found by digging through Firestore
// directly.
func (a *App) EditorAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !a.devMode {
		http.NotFound(w, r)
		return
	}
	profiles, err := a.allProfiles(r.Context())
	if err != nil {
		log.Printf("admin users: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	draftCounts, err := a.draftCountsByUID(r.Context())
	if err != nil {
		log.Printf("admin users: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	rows := make([]adminUserRow, 0, len(profiles))
	for _, p := range profiles {
		sub, err := a.subscriptionForUID(r.Context(), p.UID)
		if err != nil {
			log.Printf("admin users: %s", err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		rows = append(rows, adminUserRow{
			Profile: p,
			Plan:    planOf(sub),
			Drafts:  draftCounts[p.UID],
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Created.After(rows[j].Created)
	})

	a.renderEditor(w, r, "editor_admin_users.html", map[string]any{
		"Title": "Users",
		"Rows":  rows,
	})
}

// allProfiles returns every editor profile.
func (a *App) allProfiles(ctx context.Context) ([]*Profile, error) {
	iter := a.firestore.Collection(profileCollection).Documents(ctx)
	defer iter.Stop()

	var profiles []*Profile
	for {
		snapshot, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var p Profile
		if err := snapshot.DataTo(&p); err != nil {
			return nil, err
		}
		profiles = append(profiles, &p)
	}
	return profiles, nil
}

// draftCountsByUID tallies every document's owner in one pass, rather than
// running a separate query per profile. Select("UID") keeps the scan from
// pulling each draft's full ProseMirror body across the wire.
func (a *App) draftCountsByUID(ctx context.Context) (map[UID]int, error) {
	iter := a.firestore.Collection(documentCollection).Select("UID").Documents(ctx)
	defer iter.Stop()

	counts := make(map[UID]int)
	for {
		snapshot, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var d Document
		if err := snapshot.DataTo(&d); err != nil {
			return nil, err
		}
		counts[d.UID]++
	}
	return counts, nil
}

// EditorAdminPurgeTestAccounts deletes every account minted by
// EditorTestingAuth (identified by its "test-" UID prefix) along with its
// drafts and any other Firestore data, so a dev environment's test accounts
// don't accumulate indefinitely. It only runs in dev mode, since that is the
// only place such accounts can exist.
func (a *App) EditorAdminPurgeTestAccounts(w http.ResponseWriter, r *http.Request) {
	if !a.devMode {
		http.NotFound(w, r)
		return
	}
	profiles, err := a.testAccountProfiles(r.Context())
	if err != nil {
		log.Printf("admin purge: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	var draftsDeleted int
	for _, p := range profiles {
		n, err := a.purgeTestAccount(r.Context(), p)
		if err != nil {
			log.Printf("admin purge %s: %s", p.UID, err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		draftsDeleted += n
	}

	http.Redirect(w, r, "/_admin/?purged="+strconv.Itoa(len(profiles))+"&drafts="+strconv.Itoa(draftsDeleted), 302)
}

// testAccountProfiles returns every profile whose UID has the "test-" prefix
// EditorTestingAuth mints.
func (a *App) testAccountProfiles(ctx context.Context) ([]*Profile, error) {
	iter := a.firestore.Collection(profileCollection).
		Where("UID", ">=", "test-").
		Where("UID", "<", "test-").
		Documents(ctx)
	defer iter.Stop()

	var profiles []*Profile
	for {
		snapshot, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var p Profile
		if err := snapshot.DataTo(&p); err != nil {
			return nil, err
		}
		profiles = append(profiles, &p)
	}
	return profiles, nil
}

// purgeTestAccount deletes one test account's drafts, then its API token
// index entry, subscription and profile. Drafts go first so a crash midway
// leaves an orphaned profile rather than drafts nothing can reach.
func (a *App) purgeTestAccount(ctx context.Context, p *Profile) (draftsDeleted int, err error) {
	iter := a.firestore.Collection(documentCollection).
		Where("UID", "==", string(p.UID)).Documents(ctx)
	var ids []string
	for {
		snapshot, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			iter.Stop()
			return 0, err
		}
		ids = append(ids, snapshot.Ref.ID)
	}
	iter.Stop()

	for _, id := range ids {
		if err := a.deleteDocument(ctx, id); err != nil {
			return draftsDeleted, err
		}
		draftsDeleted++
	}

	if p.APIToken != "" {
		if _, err := a.firestore.Collection(tokenCollection).Doc(tokenKey(p.APIToken)).Delete(ctx); err != nil && !isNotFound(err) {
			return draftsDeleted, err
		}
		a.forgetToken(p.APIToken)
	}

	if _, err := a.firestore.Collection(subscriptionCollection).Doc(string(p.UID)).Delete(ctx); err != nil && !isNotFound(err) {
		return draftsDeleted, err
	}
	a.subscriptionMutex.Lock()
	delete(a.subscriptions, p.UID)
	a.subscriptionMutex.Unlock()

	if _, err := a.firestore.Collection(profileCollection).Doc(string(p.UID)).Delete(ctx); err != nil && !isNotFound(err) {
		return draftsDeleted, err
	}
	a.profileMutex.Lock()
	delete(a.profiles, p.UID)
	a.profileMutex.Unlock()

	return draftsDeleted, nil
}
