package main

import "testing"

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{"Ada Drafter", "ada@example.com", "Ada Drafter"},
		{"  Ada Drafter  ", "ada@example.com", "Ada Drafter"},
		// No name: the local part reads better than a full address.
		{"", "ada.drafter@example.com", "ada.drafter"},
		{"   ", "ada@example.com", "ada"},
		{"", "", ""},
		{"", "not-an-address", "not-an-address"},
	}
	for _, tc := range tests {
		if got := displayName(tc.name, tc.email); got != tc.want {
			t.Errorf("displayName(%q, %q) = %q, want %q", tc.name, tc.email, got, tc.want)
		}
	}
}

func TestProfileDisplayNameNil(t *testing.T) {
	var p *Profile
	if got := p.DisplayName(); got != "" {
		t.Errorf("nil profile DisplayName() = %q, want empty", got)
	}
}

func TestDocumentPeople(t *testing.T) {
	d := &Document{
		Owner:   "owner@example.com",
		Editors: []string{"editor@example.com"},
		Viewers: []string{"viewer@example.com"},
	}
	got := d.People()
	want := []string{"owner@example.com", "editor@example.com", "viewer@example.com"}
	if len(got) != len(want) {
		t.Fatalf("People() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("People()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
