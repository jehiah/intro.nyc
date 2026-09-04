package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTokenKey(t *testing.T) {
	// The raw token must never be the document id, and the mapping must be
	// stable so a token issued today resolves tomorrow.
	const token = "intro_0123456789abcdef"
	key := tokenKey(token)
	if strings.Contains(key, token) {
		t.Fatalf("token key %q leaks the token", key)
	}
	if len(key) != 64 {
		t.Errorf("got a %d character key, want 64", len(key))
	}
	if key != tokenKey(token) {
		t.Error("token key is not stable")
	}
	if key == tokenKey(token+"0") {
		t.Error("different tokens share a key")
	}
}

func TestNewAPIToken(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := newAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(token, tokenPrefix) {
			t.Fatalf("got %q, want a %q prefix so tokens are recognizable", token, tokenPrefix)
		}
		if seen[token] {
			t.Fatalf("issued %q twice", token)
		}
		seen[token] = true
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"Bearer intro_abc", "intro_abc"},
		{"bearer intro_abc", "intro_abc"}, // schemes are case insensitive
		{"BEARER  intro_abc ", "intro_abc"},
		{"Basic intro_abc", ""},
		{"intro_abc", ""},
	}
	for _, tc := range tests {
		r, err := http.NewRequest("GET", "/api/drafts", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		if got := bearerToken(r); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

// A token that is not ours should be rejected before any lookup, so an
// unauthenticated request never reaches Firestore.
func TestUserForTokenRejectsForeignTokens(t *testing.T) {
	app := &App{tokens: make(map[string]*cachedToken)}
	for _, token := range []string{"", "abc", "sk-live-1234"} {
		if _, err := app.userForToken(context.Background(), token); err != errNoToken {
			t.Errorf("userForToken(%q) = %v, want errNoToken", token, err)
		}
	}
}

func TestCallEditorAPI(t *testing.T) {
	router := http.NewServeMux()
	router.HandleFunc("GET /api/ok", func(w http.ResponseWriter, r *http.Request) {
		// the caller's credentials must reach the API unchanged
		json.NewEncoder(w).Encode(map[string]string{"auth": r.Header.Get("Authorization")})
	})
	router.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(body)
	})
	router.HandleFunc("GET /api/denied", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "403 Permission denied", 403)
	})
	app := &App{editorRouter: router}
	ctx := context.Background()

	out, err := app.callEditorAPI(ctx, "Bearer intro_abc", "GET", "/api/ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.Contains(got, "Bearer intro_abc") {
		t.Errorf("got %s, want the Authorization header forwarded", got)
	}

	out, err = app.callEditorAPI(ctx, "", "POST", "/api/echo", map[string]string{"title": "pools"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.Contains(got, `"title":"pools"`) {
		t.Errorf("got %s, want the request body round tripped", got)
	}

	// An API error must surface to the model as a tool error, not as content.
	if _, err = app.callEditorAPI(ctx, "", "GET", "/api/denied", nil); err == nil {
		t.Error("a 403 did not produce an error")
	} else if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("got %q, want the API's message", err)
	}

	// A route the tools do not know about must fail rather than return HTML.
	if _, err = app.callEditorAPI(ctx, "", "GET", "/api/missing", nil); err == nil {
		t.Error("a 404 did not produce an error")
	}
}

// Every tool must be registered with a description; a tool the model cannot
// interpret is worse than no tool.
func TestMCPToolsAreDescribed(t *testing.T) {
	app := &App{editorRouter: http.NewServeMux()}
	server := app.mcpServer("Bearer intro_abc", "editor.intro.nyc", nil)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"list_drafts", "create_draft", "get_draft", "update_draft",
		"delete_draft", "share_draft", "search_law", "get_law_section",
		"law_datasets",
	}
	got := make(map[string]bool)
	for _, tool := range result.Tools {
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
		got[tool.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("tool %q is not registered", name)
		}
	}
}
