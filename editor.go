package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxDraftBytes = 1 << 20

// renderEditor executes an editor template against the shared editor chrome.
func (a *App) renderEditor(w http.ResponseWriter, r *http.Request, name string, body map[string]any) {
	a.renderEditorStatus(w, r, 200, name, body)
}

func (a *App) renderEditorStatus(w http.ResponseWriter, r *http.Request, status int, name string, body map[string]any) {
	if _, ok := body["User"]; !ok {
		body["User"] = a.User(r)
	}
	if user, ok := body["User"].(*SessionUser); ok && user.SignedIn() {
		profile, err := a.profileFor(r.Context(), user)
		if err != nil {
			log.Printf("editor profile: %s", err)
		}
		body["Profile"] = profile
	}
	body["AuthDomain"] = a.authDomain(r)

	t := newTemplateWithBase(a.templateFS, "editor_base.html", name)
	var rendered bytes.Buffer
	if err := t.ExecuteTemplate(&rendered, name, body); err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("content-type", "text/html")
	w.WriteHeader(status)
	w.Write(rendered.Bytes())
}

// EditorIndex is the editor home: sign-in when signed out, the document list
// when signed in.
func (a *App) EditorIndex(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		a.EditorSignIn(w, r)
		return
	}

	documents, err := a.listDocuments(r.Context(), user)
	if err != nil {
		log.Printf("editor list: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	type row struct {
		Document
		Shared bool
	}
	rows := make([]row, 0, len(documents))
	for _, d := range documents {
		rows = append(rows, row{Document: d, Shared: d.UID != user.UID})
	}

	a.renderEditor(w, r, "editor_documents.html", map[string]any{
		"Title":     "Drafts",
		"User":      user,
		"Documents": rows,
	})
}

// EditorDraftingManual is a public reference for the rules this editor applies.
func (a *App) EditorDraftingManual(w http.ResponseWriter, r *http.Request) {
	a.renderEditor(w, r, "drafting_manual.html", map[string]any{
		"Title": "Bill Drafting Manual",
	})
}

// EditorProfile shows the drafter's account settings.
func (a *App) EditorProfile(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Redirect(w, r, "/", 302)
		return
	}
	a.renderEditor(w, r, "editor_profile.html", map[string]any{
		"Title": "Profile",
		"User":  user,
		"Saved": r.URL.Query().Has("saved"),
	})
}

// EditorProfilePost saves the display name.
func (a *App) EditorProfilePost(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Redirect(w, r, "/", 302)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	if _, err := a.saveProfileName(r.Context(), user, r.PostForm.Get("name")); err != nil {
		log.Printf("editor profile: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	http.Redirect(w, r, "/profile?saved", 302)
}

// EditorNewForm asks for the title and type of a new bill (Rule 2.1).
func (a *App) EditorNewForm(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Redirect(w, r, "/", 302)
		return
	}
	a.renderEditor(w, r, "editor_new.html", map[string]any{
		"Title": "New draft",
		"User":  user,
	})
}

// EditorNewPost creates a document and opens it.
func (a *App) EditorNewPost(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	if !user.SignedIn() {
		http.Redirect(w, r, "/", 302)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}

	code := r.PostForm.Get("code")
	if _, ok := titlePrefixes[code]; !ok {
		code = "administrative code"
	}
	title := strings.TrimSpace(r.PostForm.Get("title"))

	document := newDocument(uuid.NewString(), user, title, code)
	if err := a.putDocument(r.Context(), document); err != nil {
		log.Printf("editor create: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	http.Redirect(w, r, "/d/"+document.ID, 302)
}

// EditorDocument renders a document: the editor for those who may change it,
// and the read-only bill for everyone else who may see it.
func (a *App) EditorDocument(w http.ResponseWriter, r *http.Request) {
	user := a.User(r)
	document, err := a.getDocument(r.Context(), r.PathValue("id"))
	if err == errDocumentNotFound {
		a.renderEditorStatus(w, r, 404, "editor_error.html", map[string]any{
			"Title":   "Not found",
			"Code":    404,
			"Message": "There is no draft at this address.",
		})
		return
	}
	if err != nil {
		log.Printf("editor get: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	access := document.AccessFor(user)
	if !access.CanView() {
		a.editorForbidden(w, r)
		return
	}

	w.Header().Set("cache-control", "no-store")

	if !access.CanEdit() {
		var doc pmNode
		if err := json.Unmarshal([]byte(document.Doc), &doc); err != nil {
			log.Printf("editor render %s: %s", document.ID, err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		a.renderEditor(w, r, "bill_readonly.html", map[string]any{
			"Title":    document.DisplayTitle(),
			"User":     user,
			"Document": document,
			"Bill":     renderBill(&doc),
		})
		return
	}

	a.renderEditor(w, r, "editor.html", map[string]any{
		"Title":    document.DisplayTitle(),
		"User":     user,
		"Document": document,
		"IsOwner":  access == AccessOwner,
	})
}

// EditorGetDraft returns the stored document for the editor to load.
func (a *App) EditorGetDraft(w http.ResponseWriter, r *http.Request) {
	document, access, ok := a.documentFor(w, r)
	if !ok {
		return
	}
	body := map[string]any{
		"id":      document.ID,
		"title":   document.Title,
		"code":    document.Code,
		"owner":   document.Owner,
		"doc":     json.RawMessage(document.Doc),
		"canEdit": access.CanEdit(),
		"updated": document.LastModified,
	}
	// Who a bill is shared with is the owner's business.
	if access == AccessOwner {
		body["editors"] = document.Editors
		body["viewers"] = document.Viewers
		body["public"] = document.Public
		body["names"] = a.namesFor(r.Context(), document.People())
	}
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	json.NewEncoder(w).Encode(body)
}

// EditorSaveDraft stores a change from the editor.
func (a *App) EditorSaveDraft(w http.ResponseWriter, r *http.Request) {
	document, access, ok := a.documentFor(w, r)
	if !ok {
		return
	}
	if !access.CanEdit() {
		http.Error(w, "Forbidden", 403)
		return
	}

	var req struct {
		Title string          `json:"title"`
		Code  string          `json:"code"`
		Doc   json.RawMessage `json:"doc"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDraftBytes)).Decode(&req); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}
	if len(req.Doc) == 0 || !json.Valid(req.Doc) {
		http.Error(w, "Bad Request", 400)
		return
	}

	document.Title = req.Title
	if _, ok := titlePrefixes[req.Code]; ok {
		document.Code = req.Code
	}
	document.Doc = string(req.Doc)
	document.LastModified = time.Now().UTC()

	if err := a.putDocument(r.Context(), document); err != nil {
		log.Printf("editor save: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"updated": document.LastModified})
}

// EditorShare updates who may read or change a document. Only the owner may
// change sharing.
func (a *App) EditorShare(w http.ResponseWriter, r *http.Request) {
	document, access, ok := a.documentFor(w, r)
	if !ok {
		return
	}
	if access != AccessOwner {
		http.Error(w, "Forbidden", 403)
		return
	}

	var req struct {
		Editors []string `json:"editors"`
		Viewers []string `json:"viewers"`
		Public  bool     `json:"public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "Bad Request", 400)
		return
	}

	document.Editors = withoutOwner(normalizeEmails(req.Editors), document.Owner)
	// An address with edit access does not also need view access.
	var viewers []string
	for _, email := range withoutOwner(normalizeEmails(req.Viewers), document.Owner) {
		if !contains(document.Editors, email) {
			viewers = append(viewers, email)
		}
	}
	document.Viewers = viewers
	document.Public = req.Public
	document.LastModified = time.Now().UTC()

	if err := a.putDocument(r.Context(), document); err != nil {
		log.Printf("editor share: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"editors": document.Editors,
		"viewers": document.Viewers,
		"public":  document.Public,
		"names":   a.namesFor(r.Context(), document.People()),
	})
}

// EditorDeleteDocument removes a draft. Only the owner may delete.
func (a *App) EditorDeleteDocument(w http.ResponseWriter, r *http.Request) {
	document, access, ok := a.documentFor(w, r)
	if !ok {
		return
	}
	if access != AccessOwner {
		a.editorForbidden(w, r)
		return
	}
	if err := a.deleteDocument(r.Context(), document.ID); err != nil {
		log.Printf("editor delete: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	// A bodyless 204 is reported as a failed request by Chromium's network log.
	w.Header().Set("content-type", "application/json")
	w.Write([]byte(`{"deleted": true}`))
}

// documentFor loads the document named in the path and checks read access.
func (a *App) documentFor(w http.ResponseWriter, r *http.Request) (*Document, Access, bool) {
	document, err := a.getDocument(r.Context(), r.PathValue("id"))
	if err == errDocumentNotFound {
		http.Error(w, "Not Found", 404)
		return nil, AccessNone, false
	}
	if err != nil {
		log.Printf("editor document: %s", err)
		http.Error(w, "Internal Server Error", 500)
		return nil, AccessNone, false
	}
	access := document.AccessFor(a.User(r))
	if !access.CanView() {
		a.editorForbidden(w, r)
		return nil, AccessNone, false
	}
	return document, access, true
}

// editorForbidden reports that the reader may not see this document. Signed-out
// readers are offered sign-in, since they may well have access once they do.
func (a *App) editorForbidden(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, "403 Permission denied", 403)
		return
	}
	a.renderEditorStatus(w, r, 403, "editor_error.html", map[string]any{
		"Title":   "Permission denied",
		"Code":    403,
		"Message": "You do not have access to this draft.",
		"SignIn":  !a.User(r).SignedIn(),
		"Next":    r.PathValue("id"),
	})
}

/* ------------------------------------------------------- document rendering */

// pmNode mirrors the ProseMirror document JSON produced by
// static/editor/js/schema.js.
type pmNode struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs"`
	Content []pmNode       `json:"content"`
	Text    string         `json:"text"`
	Marks   []struct {
		Type string `json:"type"`
	} `json:"marks"`
}

func (n pmNode) attr(name string) string {
	if s, ok := n.Attrs[name].(string); ok {
		return s
	}
	return ""
}

func (n pmNode) hasMark(name string) bool {
	for _, m := range n.Marks {
		if m.Type == name {
			return true
		}
	}
	return false
}

// titlePrefixes mirrors TITLE_PREFIXES in static/editor/js/schema.js.
var titlePrefixes = map[string]string{
	"administrative code": "To amend the administrative code of the city of New York, in relation to",
	"charter":             "To amend the New York city charter, in relation to",
	"both":                "To amend the New York city charter and the administrative code of the city of New York, in relation to",
	"unconsolidated":      "In relation to",
}

func billTitle(n pmNode) string {
	prefix, ok := titlePrefixes[n.attr("code")]
	if !ok {
		prefix = titlePrefixes["administrative code"]
	}
	return strings.TrimSpace(prefix + " " + n.attr("subject"))
}

// renderBill produces the same markup the editor renders, so the read-only view
// and the editor share static/editor/editor.css.
func renderBill(doc *pmNode) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="bill-doc">`)
	for _, node := range doc.Content {
		renderNode(&b, node)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

func renderNode(b *strings.Builder, n pmNode) {
	switch n.Type {
	case "bill_title":
		fmt.Fprintf(b, `<p class="bill-title">%s</p>`, html.EscapeString(billTitle(n)))
	case "enacting_clause":
		b.WriteString(`<p class="enacting-clause">Be it enacted by the Council as follows:</p>`)
	case "bill_section":
		fmt.Fprintf(b, `<section class="bill-section kind-%s">`, html.EscapeString(n.attr("kind")))
		for _, child := range n.Content {
			renderNode(b, child)
		}
		b.WriteString(`</section>`)
	case "section_lead":
		b.WriteString(`<p class="section-lead">`)
		renderInline(b, n.Content)
		b.WriteString(`</p>`)
	case "law_block":
		fmt.Fprintf(b,
			`<p class="law-block level-%s" data-label="%s">`,
			html.EscapeString(n.attr("level")),
			html.EscapeString(n.attr("label")),
		)
		renderInline(b, n.Content)
		b.WriteString(`</p>`)
	}
}

func renderInline(b *strings.Builder, content []pmNode) {
	for _, n := range content {
		text := html.EscapeString(n.Text)
		switch {
		case n.hasMark("del"):
			fmt.Fprintf(b, `<span class="del">%s</span>`, text)
		case n.hasMark("ins"):
			fmt.Fprintf(b, `<u class="ins">%s</u>`, text)
		default:
			b.WriteString(text)
		}
	}
}
