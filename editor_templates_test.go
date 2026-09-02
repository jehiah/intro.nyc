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
			},
			want: []string{
				`value="Ada Drafter"`,
				"drafter@example.com",
				"Saved.",
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
				"Document": document, "IsOwner": true,
			},
			want: []string{
				"A Draft Local Law",
				`id="share-add"`,
				`id="share-people"`,
				`id="share-public"`,
				"Copy link",
				">Done<",
			},
		},
		{
			name: "bill_readonly.html",
			body: map[string]any{
				"Title": "a bill", "User": user, "Profile": profile, "Document": document,
				"Bill": template.HTML(`<div class="bill-doc"></div>`),
			},
			want: []string{"Read only"},
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
			// A share dialog that offers Save/Cancel has not been migrated to
			// saving as you go.
			if strings.Contains(got, "btn-share-save") {
				t.Error("share dialog still has a save button")
			}
		})
	}
}
