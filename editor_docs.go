package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const documentCollection = "editor_documents"

// Access is what a user may do with a document.
type Access int

const (
	AccessNone Access = iota
	AccessView
	AccessEdit
	AccessOwner
)

func (a Access) CanView() bool { return a >= AccessView }
func (a Access) CanEdit() bool { return a >= AccessEdit }

// Document is a bill being drafted.
//
// Sharing is by email address rather than UID because a drafter shares with
// colleagues before knowing whether they have signed in yet.
type Document struct {
	ID    string `firestore:"ID"`
	UID   UID    `firestore:"UID"`
	Owner string `firestore:"Owner"` // owner email, for display

	Title string `firestore:"Title"` // the "in relation to" subject
	Code  string `firestore:"Code"`  // which bodies of law the bill amends

	// The ProseMirror document, stored as JSON text. Firestore's field-name
	// rules do not survive an arbitrary document tree.
	Doc string `firestore:"Doc"`

	Editors []string `firestore:"Editors"`
	Viewers []string `firestore:"Viewers"`
	Public  bool     `firestore:"Public"`

	Created      time.Time `firestore:"Created"`
	LastModified time.Time `firestore:"LastModified"`
}

func (d *Document) AccessFor(u *SessionUser) Access {
	if d == nil {
		return AccessNone
	}
	if u.SignedIn() {
		if d.UID == u.UID {
			return AccessOwner
		}
		if contains(d.Editors, u.Email) {
			return AccessEdit
		}
		if contains(d.Viewers, u.Email) {
			return AccessView
		}
	}
	if d.Public {
		return AccessView
	}
	return AccessNone
}

// DisplayTitle is what the document list shows.
func (d *Document) DisplayTitle() string {
	if strings.TrimSpace(d.Title) != "" {
		return d.Title
	}
	return "Untitled bill"
}

func contains(list []string, want string) bool {
	if want == "" {
		return false
	}
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// normalizeEmails lowercases, trims and de-duplicates a list of addresses.
func normalizeEmails(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || !strings.Contains(s, "@") || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

var errDocumentNotFound = errors.New("document not found")

func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

/* -------------------------------------------------------------- datastore */

func (a *App) getDocument(ctx context.Context, id string) (*Document, error) {
	snapshot, err := a.firestore.Collection(documentCollection).Doc(id).Get(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, errDocumentNotFound
		}
		return nil, err
	}
	var d Document
	if err := snapshot.DataTo(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (a *App) putDocument(ctx context.Context, d *Document) error {
	_, err := a.firestore.Collection(documentCollection).Doc(d.ID).Set(ctx, d)
	return err
}

// listDocuments returns everything the user owns or has been given access to.
func (a *App) listDocuments(ctx context.Context, u *SessionUser) ([]Document, error) {
	collection := a.firestore.Collection(documentCollection)
	queries := []firestore.Query{
		collection.Where("UID", "==", string(u.UID)),
	}
	if u.Email != "" {
		queries = append(queries,
			collection.Where("Editors", "array-contains", u.Email),
			collection.Where("Viewers", "array-contains", u.Email),
		)
	}

	seen := make(map[string]bool)
	var out []Document
	for _, q := range queries {
		iter := q.Limit(200).Documents(ctx)
		for {
			snapshot, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				iter.Stop()
				return nil, err
			}
			var d Document
			if err := snapshot.DataTo(&d); err != nil {
				iter.Stop()
				return nil, err
			}
			if !seen[d.ID] {
				seen[d.ID] = true
				out = append(out, d)
			}
		}
		iter.Stop()
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastModified.After(out[j].LastModified)
	})
	return out, nil
}

// newDocument builds the starting document for a bill of the given type. It
// mirrors emptyBill() in static/editor/js/corpus.js.
func newDocument(id string, u *SessionUser, title, code string) *Document {
	now := time.Now().UTC()
	doc, _ := json.Marshal(map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":  "bill_title",
				"attrs": map[string]any{"code": code, "subject": title},
			},
			map[string]any{"type": "enacting_clause"},
			map[string]any{
				"type":  "bill_section",
				"attrs": map[string]any{"kind": "effective", "cite": "", "code": ""},
				"content": []any{
					map[string]any{
						"type": "section_lead",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "This local law takes effect immediately.",
							},
						},
					},
				},
			},
		},
	})

	return &Document{
		ID:           id,
		UID:          u.UID,
		Owner:        u.Email,
		Title:        title,
		Code:         code,
		Doc:          string(doc),
		Created:      now,
		LastModified: now,
	}
}
