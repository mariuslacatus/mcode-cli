package tools

import (
	"strings"
	"testing"
)

func TestParseDuckDuckGoHTMLResults(t *testing.T) {
	html := `
<html>
  <body>
    <div class="result">
      <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc">Go Documentation</a>
      <a class="result__snippet">The official Go language documentation and guides.</a>
    </div>
    <div class="result">
      <a class="result__a" href="https://example.com/post">Example Post</a>
      <div class="result__snippet">A shorter snippet.</div>
    </div>
  </body>
</html>`

	results, err := parseDuckDuckGoHTMLResults([]byte(html), 5)
	if err != nil {
		t.Fatalf("parseDuckDuckGoHTMLResults() error = %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "Go Documentation" {
		t.Fatalf("unexpected title: %q", results[0].Title)
	}

	if results[0].URL != "https://go.dev/doc" {
		t.Fatalf("unexpected normalized URL: %q", results[0].URL)
	}

	if !strings.Contains(results[0].Snippet, "official Go language documentation") {
		t.Fatalf("unexpected snippet: %q", results[0].Snippet)
	}
}

func TestInstantAnswerToResults(t *testing.T) {
	payload := duckDuckGoInstantAnswerResponse{
		AbstractText: "Go is an open source programming language.",
		AbstractURL:  "https://go.dev",
		Heading:      "Go",
		RelatedTopics: []duckDuckGoInstantAnswerTopic{
			{
				Text:     "Go by Example - Hands-on examples",
				FirstURL: "https://gobyexample.com",
			},
			{
				Topics: []duckDuckGoInstantAnswerTopic{
					{
						Text:     "The Go Programming Language - Book site",
						FirstURL: "https://www.gopl.io",
					},
				},
			},
		},
	}

	results := instantAnswerToResults(payload, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Title != "Go" {
		t.Fatalf("unexpected abstract title: %q", results[0].Title)
	}

	if results[1].Title != "Go by Example" {
		t.Fatalf("unexpected related topic title: %q", results[1].Title)
	}

	if results[2].URL != "https://www.gopl.io" {
		t.Fatalf("unexpected nested topic URL: %q", results[2].URL)
	}
}

func TestFormatWebSearchResults(t *testing.T) {
	formatted := formatWebSearchResults("golang docs", []string{"go.dev"}, nil, []webSearchResult{
		{
			Title:   "Go Documentation",
			URL:     "https://go.dev/doc",
			Snippet: "Official docs.",
		},
	})

	if !strings.Contains(formatted, `Web results for "golang docs"`) {
		t.Fatalf("missing header: %q", formatted)
	}

	if !strings.Contains(formatted, "1. Go Documentation") {
		t.Fatalf("missing numbered result: %q", formatted)
	}

	if !strings.Contains(formatted, "include=go.dev") {
		t.Fatalf("missing filter summary: %q", formatted)
	}
}

func TestFilterWebSearchResults(t *testing.T) {
	results := []webSearchResult{
		{Title: "Go", URL: "https://go.dev/doc"},
		{Title: "Pkg", URL: "https://pkg.go.dev/fmt"},
		{Title: "Example", URL: "https://example.com"},
	}

	filtered := filterWebSearchResults(results, []string{"go.dev"}, []string{"pkg.go.dev"}, 5)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 result, got %d", len(filtered))
	}
	if filtered[0].URL != "https://go.dev/doc" {
		t.Fatalf("unexpected filtered URL: %q", filtered[0].URL)
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := map[string]string{
		"https://docs.go.dev/path": "docs.go.dev",
		"*.example.com":            "example.com",
		"EXAMPLE.COM:443":          "example.com",
	}

	for input, expected := range tests {
		if got := normalizeDomain(input); got != expected {
			t.Fatalf("normalizeDomain(%q) = %q, want %q", input, got, expected)
		}
	}
}
