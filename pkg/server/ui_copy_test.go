package server

import (
	"bytes"
	"strings"
	"testing"
)

// TestUICopyOmitsReferenceImplementation renders the pages that used to
// describe DevPulse as a "reference implementation" and asserts the phrase
// is absent from the rendered output. Asserting on rendered output (rather
// than grepping template source) means the phrase still fails this test if
// it moves into a different template that these pages render.
func TestUICopyOmitsReferenceImplementation(t *testing.T) {
	const forbidden = "reference implementation"

	tests := []struct {
		page string
		data map[string]any
	}{
		{page: "tos.html", data: map[string]any{"Title": "Terms"}},
		{page: "settings.html", data: map[string]any{"Title": "Settings"}},
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			tmpl, ok := pageTemplates[tt.page]
			if !ok {
				t.Fatalf("%s template not registered", tt.page)
			}
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "layout.html", tt.data); err != nil {
				t.Fatalf("render %s: %v", tt.page, err)
			}
			if body := buf.String(); strings.Contains(body, forbidden) {
				t.Errorf("rendered %s still contains %q", tt.page, forbidden)
			}
		})
	}
}
