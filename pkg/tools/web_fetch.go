package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/sashabaranov/go-openai"
)

const (
	defaultWebFetchFormat      = "text"
	defaultWebFetchTimeout     = 15 * time.Second
	maxWebFetchTimeout         = 120 * time.Second
	maxWebFetchResponseBytes   = int64(5 * 1024 * 1024)
	defaultWebFetchMaxChars    = 12000
	maxAllowedWebFetchMaxChars = 40000
)

type WebFetchTool struct {
	BaseTool
	client *http.Client
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Fetch content from a specific URL after identifying it with web_search. Use this to inspect the contents of relevant web pages directly.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "The URL to fetch. Must start with http:// or https://",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Optional output format: text or html. Defaults to text.",
						"enum":        []string{"text", "html"},
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Optional timeout in seconds. Defaults to 15 and is capped at 120.",
					},
					"max_chars": map[string]interface{}{
						"type":        "integer",
						"description": "Optional character limit for the returned content. Defaults to 12000 and is capped at 40000.",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	var args WebFetchArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	rawURL := strings.TrimSpace(args.URL)
	if rawURL == "" {
		return "", fmt.Errorf("url parameter is required")
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %v", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("url must start with http:// or https://")
	}

	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = defaultWebFetchFormat
	}
	if format != "text" && format != "html" {
		return "", fmt.Errorf("unsupported format %q", format)
	}

	timeout := clampWebFetchTimeout(args.TimeoutSeconds)
	maxChars := clampWebFetchMaxChars(args.MaxChars)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("User-Agent", webToolUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.getHTTPClient(timeout, parsedURL.Hostname()).Do(req)
	if err != nil {
		return "", fmt.Errorf("web fetch request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("web fetch request failed with status %s", resp.Status)
	}

	if contentLength := resp.ContentLength; contentLength > maxWebFetchResponseBytes {
		return "", fmt.Errorf("response too large (exceeds 5MB limit)")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}
	if int64(len(body)) > maxWebFetchResponseBytes {
		return "", fmt.Errorf("response too large (exceeds 5MB limit)")
	}

	contentType := detectContentType(resp.Header.Get("Content-Type"), body)
	if !isSupportedWebFetchContentType(contentType) {
		return "", fmt.Errorf("unsupported content type for web_fetch: %s", contentType)
	}
	title := ""
	if strings.Contains(contentType, "text/html") {
		title = extractHTMLTitle(body)
	}

	var content string
	switch format {
	case "html":
		content = string(body)
	default:
		if strings.Contains(contentType, "text/html") {
			content = extractVisibleHTMLText(body)
		} else {
			content = string(body)
		}
	}

	content = truncateToChars(strings.TrimSpace(content), maxChars)
	if content == "" {
		content = "[empty response body]"
	}

	var builder strings.Builder
	builder.WriteString("Fetched: ")
	builder.WriteString(parsedURL.String())
	builder.WriteString("\n")
	if title != "" {
		builder.WriteString("Title: ")
		builder.WriteString(title)
		builder.WriteString("\n")
	}
	if contentType != "" {
		builder.WriteString("Content-Type: ")
		builder.WriteString(contentType)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(content)

	return builder.String(), nil
}

func (t *WebFetchTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *WebFetchTool) GetDisplayInfo(params map[string]interface{}) string {
	var args WebFetchArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}
	if args.URL == "" {
		return ""
	}
	return fmt.Sprintf(" \"%s\"", args.URL)
}

func (t *WebFetchTool) getHTTPClient(timeout time.Duration, originalHost string) *http.Client {
	var client http.Client
	if t.client != nil {
		client = *t.client
	} else {
		client = http.Client{}
	}

	client.Timeout = timeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after too many redirects")
		}

		if t.isRedirectHostAllowed(originalHost, req.URL.Hostname()) {
			return nil
		}

		return fmt.Errorf("redirected to unapproved host: %s", req.URL.Hostname())
	}

	return &client
}

func (t *WebFetchTool) isRedirectHostAllowed(originalHost, redirectedHost string) bool {
	redirectedHost = normalizeDomain(redirectedHost)
	if redirectedHost == "" {
		return false
	}

	originalHost = normalizeDomain(originalHost)
	if redirectedHost == originalHost {
		return true
	}

	if t.manager == nil || t.manager.agent == nil {
		return false
	}

	for approvedDomain := range t.manager.agent.ApprovedWebDomains {
		if domainMatches(redirectedHost, approvedDomain) {
			return true
		}
	}

	return false
}

func clampWebFetchTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultWebFetchTimeout
	}
	timeout := time.Duration(seconds) * time.Second
	if timeout > maxWebFetchTimeout {
		return maxWebFetchTimeout
	}
	return timeout
}

func clampWebFetchMaxChars(value int) int {
	if value <= 0 {
		return defaultWebFetchMaxChars
	}
	if value > maxAllowedWebFetchMaxChars {
		return maxAllowedWebFetchMaxChars
	}
	return value
}

func extractHTMLTitle(body []byte) string {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var findTitle func(*xhtml.Node) string
	findTitle = func(node *xhtml.Node) string {
		if node == nil {
			return ""
		}
		if node.Type == xhtml.ElementNode && node.Data == "title" {
			return normalizeWhitespace(nodeText(node))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if title := findTitle(child); title != "" {
				return title
			}
		}
		return ""
	}

	return findTitle(doc)
}

func extractVisibleHTMLText(body []byte) string {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}

	var builder strings.Builder
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, skip bool) {
		if node == nil {
			return
		}

		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "iframe", "svg", "canvas":
				skip = true
			case "br", "p", "div", "section", "article", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				builder.WriteString("\n")
			}
		}

		if node.Type == xhtml.TextNode && !skip {
			text := normalizeWhitespace(node.Data)
			if text != "" {
				if builder.Len() > 0 {
					builder.WriteString(" ")
				}
				builder.WriteString(text)
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}

		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "p", "div", "section", "article", "li", "tr":
				builder.WriteString("\n")
			}
		}
	}

	walk(doc, false)
	lines := strings.Split(builder.String(), "\n")
	var cleaned []string
	for _, line := range lines {
		line = normalizeWhitespace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func truncateToChars(value string, maxChars int) string {
	if len(value) <= maxChars {
		return value
	}
	return strings.TrimSpace(value[:maxChars-3]) + "..."
}

func detectContentType(headerValue string, body []byte) string {
	contentType := strings.TrimSpace(strings.ToLower(headerValue))
	if contentType != "" {
		if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
			contentType = mediaType
		}
	}

	if contentType == "" || contentType == "application/octet-stream" {
		sniffLength := 512
		if len(body) < sniffLength {
			sniffLength = len(body)
		}
		if sniffLength > 0 {
			contentType = strings.ToLower(http.DetectContentType(body[:sniffLength]))
		}
	}

	return contentType
}

func isSupportedWebFetchContentType(contentType string) bool {
	if contentType == "" {
		return true
	}

	if strings.HasPrefix(contentType, "text/") {
		return true
	}

	switch {
	case contentType == "application/json":
		return true
	case contentType == "application/xml":
		return true
	case contentType == "application/xhtml+xml":
		return true
	case contentType == "application/javascript":
		return true
	case contentType == "application/x-javascript":
		return true
	case contentType == "image/svg+xml":
		return true
	case strings.HasSuffix(contentType, "+json"):
		return true
	case strings.HasSuffix(contentType, "+xml"):
		return true
	default:
		return false
	}
}
