package server

import (
	"bytes"
	"strings"
	"testing"
)

func renderFooterPage(t *testing.T, opts Options) string {
	t.Helper()
	serverOpts = opts
	t.Cleanup(func() { serverOpts = Options{} })

	tmpl, ok := pageTemplates["tos.html"]
	if !ok {
		t.Fatal("tos.html template not registered")
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", map[string]any{"Title": "Terms"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestFooterLinks(t *testing.T) {
	body := renderFooterPage(t, Options{Version: "v1.2.3", Commit: "abc1234"})

	for _, want := range []string{
		`href="/help">HELP<`,
		`href="/tos">TERMS<`,
		`href="https://github.com/thingzio/devpulse"`,
		"VERSION v1.2.3",
		"(abc1234)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("footer missing %q", want)
		}
	}
	if strings.Contains(body, "CHANGELOG") {
		t.Error("footer still links the changelog")
	}
}

func TestFooterOmitsVersionWhenUnset(t *testing.T) {
	body := renderFooterPage(t, Options{})

	if strings.Contains(body, "VERSION") {
		t.Error("footer rendered a version label with no version")
	}
	if !strings.Contains(body, `href="/help">HELP<`) {
		t.Error("footer lost its links when the version was absent")
	}
}
