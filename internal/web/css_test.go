package web

import (
	"os"
	"regexp"
	"testing"
)

func TestPublicCSSHasSingleCanonicalDefinitions(t *testing.T) {
	data, err := os.ReadFile("static/pages.css")
	if err != nil {
		t.Fatalf("read pages.css: %v", err)
	}
	css := string(data)
	for _, selector := range []string{
		".public-hero {",
		".public-monitor {",
		".public-monitor-list {",
		".public-uptime {",
		".public-history-bars {",
		".public-powered {",
	} {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(selector))
		if count := len(pattern.FindAllStringIndex(css, -1)); count != 1 {
			t.Fatalf("%s top-level definitions = %d, want 1", selector, count)
		}
	}
}
