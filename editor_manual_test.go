package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every rule a style check cites must exist on /drafting-manual, because the
// check links to it.
func TestStyleCheckRulesHaveManualAnchors(t *testing.T) {
	manual, err := os.ReadFile("templates/drafting_manual.html")
	if err != nil {
		t.Fatal(err)
	}
	lint, err := os.ReadFile("static/editor/js/lint.js")
	if err != nil {
		t.Fatal(err)
	}

	anchors := make(map[string]bool)
	for _, m := range regexp.MustCompile(`id="(rule-[a-z0-9-]+)"`).FindAllSubmatch(manual, -1) {
		anchors[string(m[1])] = true
	}
	if len(anchors) == 0 {
		t.Fatal("no rule anchors found in the manual")
	}

	cited := make(map[string]bool)
	for _, m := range regexp.MustCompile(`rule:\s*"([^"]+)"`).FindAllSubmatch(lint, -1) {
		cited[string(m[1])] = true
	}
	if len(cited) == 0 {
		t.Fatal("no rules cited in lint.js")
	}

	for rule := range cited {
		// Mirrors ruleHref() in main.js.
		anchor := "rule-" + strings.NewReplacer(" ", "-", ".", "-").Replace(strings.ToLower(rule))
		if !anchors[anchor] {
			t.Errorf("style check cites %q but the manual has no #%s", rule, anchor)
		}
	}
}
