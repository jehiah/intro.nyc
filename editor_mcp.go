package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP endpoint is a thin wrapper over the editor's own JSON API. Every tool
// dispatches an in-process HTTP request through the same router the browser
// uses, carrying the caller's Authorization header, so access control and
// behaviour cannot drift between the two.

const mcpInstructions = `Draft New York City Council legislation.

A bill is a ProseMirror document with a fixed shape, following the NYC Bill
Drafting Manual (see https://editor.intro.nyc/drafting-manual):

  doc
    bill_title        attrs {code, subject}   code is one of:
                                              "administrative code", "charter",
                                              "both", "unconsolidated"
    enacting_clause   (fixed, no content)
    bill_section+     attrs {kind, cite, code}
      section_lead    the unconsolidated lead-in sentence (Rule 3)
      law_block*      attrs {level, designator, label}; the consolidated text

Inline content is text, plus a hard_break node for a line break within one
block. In a bill_section of kind "add" the designator and label attrs are
derived from document order and rewritten by the editor; set them to "" and
let the level attr carry the structure.

bill_section.kind is one of:
  amend       existing law reproduced and marked up
  add         wholly new text; all of it carries the "ins" mark
  repeal      lead-in only, ending "is REPEALED." (Rules 3.1.10, 11.1.4)
  effective   the closing "This local law takes effect ..." (Rule 6, required)

Marks on text (Rule 11.1) are the heart of the format:
  ins   text this bill ADDS      - rendered underlined
  del   text this bill REMOVES   - rendered in [brackets], and KEPT in the
                                   document rather than deleted
A deletion always precedes the addition that replaces it, separated by one
unmarked space: [old] new.

Workflow:
  1. search_law to find a provision, then get_law_section for its full text and
     amendment history.
  2. create_draft, then update_draft with bill sections built from that text.
  3. The lead-in must recite the last law that amended the provision
     (Rule 3.1); get_law_section returns the history needed to write it.

Always read a draft with get_draft before updating it: update_draft replaces
the whole document.`

// mcpHandler serves /mcp. A server is built per request so tool handlers can
// close over the caller's credentials.
func (a *App) mcpHandler() http.Handler {
	cache := mcp.NewSchemaCache()
	return mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server {
			return a.mcpServer(r.Header.Get("Authorization"), r.Host, cache)
		},
		&mcp.StreamableHTTPOptions{
			Stateless:                  true,
			JSONResponse:               true,
			DisableLocalhostProtection: true,
		},
	)
}

func (a *App) mcpServer(auth, host string, cache *mcp.SchemaCache) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:       "intro.nyc-editor",
			Title:      "NYC legislation editor",
			Version:    "1.0.0",
			WebsiteURL: "https://" + host + "/",
		},
		&mcp.ServerOptions{
			Instructions: mcpInstructions,
			SchemaCache:  cache,
		},
	)

	call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		return a.callEditorAPI(ctx, auth, method, path, body)
	}

	type empty struct{}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_drafts",
		Description: "List the bill drafts you own or that have been shared with you.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "GET", "/api/drafts", nil)
		return nil, out, err
	})

	type createArgs struct {
		Title string `json:"title" jsonschema:"the subject of the bill, completing \"in relation to ...\" (Rule 2.1). Short and general."`
		Code  string `json:"code,omitempty" jsonschema:"which bodies of law it amends: administrative code (default), charter, both, or unconsolidated"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "create_draft",
		Description: "Start a new bill. Returns its id and the starting document, " +
			"which already contains the title, enacting clause and an effective " +
			"date section.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in createArgs) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "POST", "/api/drafts", in)
		return nil, out, err
	})

	type draftArgs struct {
		ID string `json:"id" jsonschema:"the draft's uuid"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_draft",
		Description: "Fetch a draft: its title, which bodies of law it amends, and " +
			"the full ProseMirror document. Read this before updating.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in draftArgs) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "GET", "/api/draft/"+url.PathEscape(in.ID), nil)
		return nil, out, err
	})

	type updateArgs struct {
		ID    string `json:"id" jsonschema:"the draft's uuid"`
		Title string `json:"title" jsonschema:"the bill subject; must match bill_title.attrs.subject in the document"`
		Code  string `json:"code,omitempty" jsonschema:"administrative code, charter, both, or unconsolidated; must match bill_title.attrs.code"`
		// Doc is the document's JSON text, not a structured object: several
		// MCP clients deliver a "{...}"-shaped argument as a JSON string
		// rather than parsing it into the declared object/array schema (a
		// json.RawMessage or map[string]any field, which both produce such a
		// schema, saw the whole document arrive double-encoded as a result).
		// A plain string field matches what those clients actually send, and
		// is forwarded as json.RawMessage below so it lands unescaped.
		Doc string `json:"doc" jsonschema:"the complete ProseMirror document as JSON text, replacing what is stored"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "update_draft",
		Description: "Replace a draft's document. Send the whole document, not a " +
			"patch. Keep the effective date as the last bill section (Rule 6), and " +
			"mark amended law with ins and del rather than editing it in place " +
			"(Rule 11.1).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateArgs) (*mcp.CallToolResult, any, error) {
		if !json.Valid([]byte(in.Doc)) {
			return nil, nil, fmt.Errorf("doc is not valid JSON")
		}
		out, err := call(ctx, "POST", "/api/draft/"+url.PathEscape(in.ID), map[string]any{
			"title": in.Title, "code": in.Code, "doc": json.RawMessage(in.Doc),
		})
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_draft",
		Description: "Delete a draft you own. This cannot be undone.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in draftArgs) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "DELETE", "/api/draft/"+url.PathEscape(in.ID), nil)
		return nil, out, err
	})

	type shareArgs struct {
		ID      string   `json:"id" jsonschema:"the draft's uuid"`
		Editors []string `json:"editors,omitempty" jsonschema:"email addresses that may edit"`
		Viewers []string `json:"viewers,omitempty" jsonschema:"email addresses that may read"`
		Public  bool     `json:"public,omitempty" jsonschema:"whether anyone with the link may read"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "share_draft",
		Description: "Set who may read or edit a draft, by email address. Replaces " +
			"the current lists. Only the owner may change sharing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in shareArgs) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "POST", "/api/share/"+url.PathEscape(in.ID), map[string]any{
			"editors": in.Editors, "viewers": in.Viewers, "public": in.Public,
		})
		return nil, out, err
	})

	type searchArgs struct {
		Query   string   `json:"query" jsonschema:"a citation such as 16-497, or words from the section heading"`
		Dataset []string `json:"dataset,omitempty" jsonschema:"administrative-code and charter by default; rules for the RCNY, which a local law does not amend"`
		Limit   int      `json:"limit,omitempty" jsonschema:"maximum results, default 25"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_law",
		Description: "Find a provision of the Charter or Administrative Code. Returns " +
			"citation, heading and the file path to pass to get_law_section.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, any, error) {
		params := url.Values{"q": {in.Query}}
		for _, d := range in.Dataset {
			params.Add("dataset", d)
		}
		if in.Limit > 0 {
			params.Set("limit", fmt.Sprint(in.Limit))
		}
		out, err := call(ctx, "GET", "/api/law/search?"+params.Encode(), nil)
		return nil, out, err
	})

	type sectionArgs struct {
		Dataset string `json:"dataset" jsonschema:"administrative-code, charter or rules"`
		File    string `json:"file" jsonschema:"the file path from search_law, e.g. title-16/chapter-4-g/16-497.json"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_law_section",
		Description: "Fetch one provision in full: its heading, text blocks with " +
			"their levels and designators, and the amendment history a bill-section " +
			"lead-in must recite (Rule 3.1).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sectionArgs) (*mcp.CallToolResult, any, error) {
		path := "/api/law/section/" + url.PathEscape(in.Dataset) + "/" + strings.TrimPrefix(in.File, "/")
		out, err := call(ctx, "GET", path, nil)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "law_datasets",
		Description: "List the bodies of law available and how current each is, " +
			"as published by the Council's codifier.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, "GET", "/api/law/datasets", nil)
		return nil, out, err
	})

	return server
}

func boolPtr(b bool) *bool { return &b }

// callEditorAPI dispatches a request through the editor's router in-process.
func (a *App) callEditorAPI(ctx context.Context, auth, method, path string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://editor.intro.nyc"+path, reader)
	if err != nil {
		return nil, err
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")

	rec := &responseRecorder{header: make(http.Header), status: 200}
	a.editorRouter.ServeHTTP(rec, req)

	if rec.status >= 400 {
		message := strings.TrimSpace(rec.body.String())
		if message == "" {
			message = http.StatusText(rec.status)
		}
		return nil, fmt.Errorf("%s", message)
	}
	if !json.Valid(rec.body.Bytes()) {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(rec.body.Bytes()), nil
}

// responseRecorder captures an in-process API response.
type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.body.Write(b)
}

func (r *responseRecorder) WriteHeader(status int) {
	if !r.wrote {
		r.status = status
		r.wrote = true
	}
}
