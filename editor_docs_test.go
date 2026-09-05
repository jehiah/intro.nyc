package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDocumentAccess(t *testing.T) {
	owner := &SessionUser{UID: "owner-uid", Email: "owner@example.com"}
	editor := &SessionUser{UID: "editor-uid", Email: "Editor@Example.com"}
	viewer := &SessionUser{UID: "viewer-uid", Email: "viewer@example.com"}
	stranger := &SessionUser{UID: "stranger-uid", Email: "stranger@example.com"}

	doc := &Document{
		UID:     "owner-uid",
		Editors: []string{"editor@example.com"},
		Viewers: []string{"viewer@example.com"},
	}

	tests := []struct {
		name string
		doc  *Document
		user *SessionUser
		want Access
	}{
		{"owner", doc, owner, AccessOwner},
		{"editor matches regardless of case", doc, editor, AccessEdit},
		{"viewer", doc, viewer, AccessView},
		{"stranger", doc, stranger, AccessNone},
		{"signed out", doc, nil, AccessNone},
		{"signed out, public", &Document{UID: "owner-uid", Public: true}, nil, AccessView},
		{"stranger, public", &Document{UID: "owner-uid", Public: true}, stranger, AccessView},
		{
			"owner outranks public",
			&Document{UID: "owner-uid", Public: true},
			owner,
			AccessOwner,
		},
		{
			"edit access outranks view access",
			&Document{
				UID:     "owner-uid",
				Editors: []string{"both@example.com"},
				Viewers: []string{"both@example.com"},
			},
			&SessionUser{UID: "both-uid", Email: "both@example.com"},
			AccessEdit,
		},
		{
			"an empty email does not match an empty share list",
			&Document{UID: "owner-uid", Editors: []string{""}},
			&SessionUser{UID: "anon-uid"},
			AccessNone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.doc.AccessFor(tc.user); got != tc.want {
				t.Errorf("AccessFor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAccessLevels(t *testing.T) {
	if AccessNone.CanView() || AccessNone.CanEdit() {
		t.Error("AccessNone grants nothing")
	}
	if !AccessView.CanView() || AccessView.CanEdit() {
		t.Error("AccessView reads only")
	}
	if !AccessEdit.CanView() || !AccessEdit.CanEdit() {
		t.Error("AccessEdit reads and writes")
	}
	if !AccessOwner.CanEdit() {
		t.Error("AccessOwner writes")
	}
}

func TestNormalizeEmails(t *testing.T) {
	got := normalizeEmails([]string{
		"  Second@Example.com ",
		"first@example.com",
		"first@example.com",
		"not-an-email",
		"",
	})
	want := []string{"first@example.com", "second@example.com"}
	if len(got) != len(want) {
		t.Fatalf("normalizeEmails() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("normalizeEmails()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A new document must be renderable by the read-only view without an edit.
func TestNewDocumentRenders(t *testing.T) {
	user := &SessionUser{UID: "uid", Email: "drafter@example.com"}
	d := newDocument("id-1", user, "door alarms in school buildings", "charter")

	var node pmNode
	if err := json.Unmarshal([]byte(d.Doc), &node); err != nil {
		t.Fatalf("stored document is not valid ProseMirror JSON: %s", err)
	}
	html := string(renderBill(&node))

	for _, want := range []string{
		"To amend the New York city charter, in relation to door alarms in school buildings",
		"Be it enacted by the Council as follows:",
		"This local law takes effect immediately.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered bill missing %q\ngot: %s", want, html)
		}
	}
}

// The read-only view renders what the editor stores: a line break within a
// provision, and a cross-reference linked to the publisher's text.
func TestRenderBillInlineNodes(t *testing.T) {
	const doc = `{"type":"doc","content":[
	  {"type":"bill_title","attrs":{"code":"administrative code","subject":"waste zones"}},
	  {"type":"enacting_clause"},
	  {"type":"bill_section","attrs":{"kind":"add","cite":"16-1000","code":"administrative code"},
	   "content":[
	     {"type":"section_lead","content":[{"type":"text","text":"Section 16-1000 is amended by adding a new subdivision c:"}]},
	     {"type":"law_block","attrs":{"level":"subdivision","designator":"c","label":"c."},
	      "content":[
	        {"type":"text","marks":[{"type":"ins"}],"text":"See "},
	        {"type":"text","marks":[{"type":"ins"},{"type":"ref","attrs":{"dataset":"administrative-code","file":"title-16-b/chapter-1/16-1000.json","cite":"16-1000","record":"0-0-0-113745"}}],"text":"section 16-1000"},
	        {"type":"text","marks":[{"type":"ins"}],"text":"."},
	        {"type":"hard_break"},
	        {"type":"text","marks":[{"type":"ins"}],"text":"Second line."}
	      ]}
	   ]}
	]}`

	var node pmNode
	if err := json.Unmarshal([]byte(doc), &node); err != nil {
		t.Fatalf("test document is not valid ProseMirror JSON: %s", err)
	}
	html := string(renderBill(&node))

	for _, want := range []string{
		`<br>`,
		`<a class="law-ref" href="https://codelibrary.amlegal.com/codes/newyorkcity/latest/NYCadmin/0-0-0-113745"`,
		`section 16-1000</a>`,
		`Second line.`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered bill missing %q\ngot: %s", want, html)
		}
	}
}

// A reference the editor could not match to a provision carries no link.
func TestRenderBillUnresolvedReference(t *testing.T) {
	const doc = `{"type":"doc","content":[
	  {"type":"bill_title","attrs":{"code":"charter","subject":"x"}},
	  {"type":"enacting_clause"},
	  {"type":"bill_section","attrs":{"kind":"unconsolidated"},
	   "content":[{"type":"section_lead","content":[
	     {"type":"text","marks":[{"type":"ref","attrs":{"dataset":"","file":"","cite":"99-999","record":""}}],"text":"section 99-999"}
	   ]}]}
	]}`

	var node pmNode
	if err := json.Unmarshal([]byte(doc), &node); err != nil {
		t.Fatalf("test document is not valid ProseMirror JSON: %s", err)
	}
	html := string(renderBill(&node))
	if strings.Contains(html, "<a") {
		t.Errorf("unresolved reference should not be a link\ngot: %s", html)
	}
	if !strings.Contains(html, "section 99-999") {
		t.Errorf("rendered bill missing the reference text\ngot: %s", html)
	}
}
