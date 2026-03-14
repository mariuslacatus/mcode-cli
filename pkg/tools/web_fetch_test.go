package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"coding-agent/pkg/types"
)

func TestWebFetchToolTextExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Example</title></head><body><h1>Hello</h1><p>World</p><script>ignored()</script></body></html>`))
	}))
	defer server.Close()

	tool := &WebFetchTool{}
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(result, "Title: Example") {
		t.Fatalf("expected title in result, got %q", result)
	}
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Fatalf("expected visible text in result, got %q", result)
	}
	if strings.Contains(result, "ignored()") {
		t.Fatalf("unexpected script content in result, got %q", result)
	}
}

func TestWebFetchToolRejectsLargeResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "6000000")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tool := &WebFetchTool{}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("expected response-too-large error, got %v", err)
	}
}

func TestWebFetchToolRejectsBinaryContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x00, 0x01, 0x02, 0x03})
	}))
	defer server.Close()

	tool := &WebFetchTool{}
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("expected unsupported-content-type error, got %v", err)
	}
}

func TestWebFetchToolRejectsRedirectToUnapprovedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com", http.StatusFound)
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse server URL: %v", err)
	}

	tool := &WebFetchTool{
		BaseTool: BaseTool{
			manager: &Manager{
				agent: &types.Agent{
					ApprovedWebDomains: map[string]bool{
						normalizeDomain(parsedURL.Hostname()): true,
					},
				},
			},
		},
	}

	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"url": server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "redirected to unapproved host") {
		t.Fatalf("expected redirect-host error, got %v", err)
	}
}

func TestWebFetchToolAllowsRedirectToApprovedHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("failed to parse target URL: %v", err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	sourceURL, err := url.Parse(source.URL)
	if err != nil {
		t.Fatalf("failed to parse source URL: %v", err)
	}

	approved := map[string]bool{
		normalizeDomain(sourceURL.Hostname()): true,
	}
	if sourceURL.Hostname() != targetURL.Hostname() {
		approved[normalizeDomain(targetURL.Hostname())] = true
	}

	tool := &WebFetchTool{
		BaseTool: BaseTool{
			manager: &Manager{
				agent: &types.Agent{
					ApprovedWebDomains: approved,
				},
			},
		},
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": source.URL,
	})
	if err != nil {
		t.Fatalf("expected redirect to approved host to succeed, got %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Fatalf("expected fetched body after redirect, got %q", result)
	}
}

func TestDetectContentTypeUsesSniffing(t *testing.T) {
	body := []byte("<html><body>Hello</body></html>")
	contentType := detectContentType("", body)
	if contentType == "" {
		t.Fatal("expected sniffed content type")
	}
	if !isSupportedWebFetchContentType(contentType) {
		t.Fatalf("expected sniffed content type %q to be supported", contentType)
	}
}
