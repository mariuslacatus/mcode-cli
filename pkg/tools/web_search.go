package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"

	"github.com/sashabaranov/go-openai"
)

const (
	defaultWebSearchEndpoint     = "https://html.duckduckgo.com/html/"
	defaultInstantAnswerEndpoint = "https://api.duckduckgo.com/"
	defaultWebSearchTimeout      = 12 * time.Second
	defaultWebSearchMaxResults   = 5
	maxAllowedWebSearchResults   = 10
	maxSearchResponseBodyBytes   = int64(1 << 20)
	webToolUserAgent             = "Mozilla/5.0 (compatible; mcode/1.0; +https://github.com)"
)

type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type duckDuckGoInstantAnswerResponse struct {
	AbstractText  string                         `json:"AbstractText"`
	AbstractURL   string                         `json:"AbstractURL"`
	Heading       string                         `json:"Heading"`
	RelatedTopics []duckDuckGoInstantAnswerTopic `json:"RelatedTopics"`
}

type duckDuckGoInstantAnswerTopic struct {
	Text     string                         `json:"Text"`
	FirstURL string                         `json:"FirstURL"`
	Topics   []duckDuckGoInstantAnswerTopic `json:"Topics"`
}

type WebSearchTool struct {
	BaseTool
	client                *http.Client
	searchEndpoint        string
	instantAnswerEndpoint string
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name(),
			Description: "Search the web for current external information. Supports include/exclude domain filters for narrowing results.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query to send to the web search engine",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: maximum number of results to return (default 5, max 10)",
					},
					"include_domains": map[string]interface{}{
						"type":        "array",
						"description": "Optional: only include results from these domains",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
					"exclude_domains": map[string]interface{}{
						"type":        "array",
						"description": "Optional: exclude results from these domains",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	var args WebSearchArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return "", err
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	maxResults := clampWebSearchResults(args.MaxResults)
	includeDomains := normalizeDomainFilters(args.IncludeDomains)
	excludeDomains := normalizeDomainFilters(args.ExcludeDomains)

	results, err := t.fetchHTMLResults(ctx, query, maxResults, includeDomains, excludeDomains)
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		results, err = t.fetchInstantAnswerResults(ctx, query, maxResults, includeDomains, excludeDomains)
		if err != nil {
			return "", err
		}
	}

	if len(results) == 0 {
		return formatNoWebSearchResults(query, includeDomains, excludeDomains), nil
	}

	return formatWebSearchResults(query, includeDomains, excludeDomains, results), nil
}

func (t *WebSearchTool) Preview(params map[string]interface{}) (string, error) {
	return "", nil
}

func (t *WebSearchTool) GetDisplayInfo(params map[string]interface{}) string {
	var args WebSearchArgs
	if err := t.Unmarshal(params, &args); err != nil {
		return ""
	}
	if args.Query == "" {
		return ""
	}
	return fmt.Sprintf(" \"%s\"", args.Query)
}

func (t *WebSearchTool) fetchHTMLResults(ctx context.Context, query string, maxResults int, includeDomains, excludeDomains []string) ([]webSearchResult, error) {
	searchQuery := withIncludeDomainOperators(query, includeDomains)
	rawResultLimit := expandRawWebResultLimit(maxResults, len(includeDomains) > 0 || len(excludeDomains) > 0)

	reqURL, err := buildURL(t.getSearchEndpoint(), url.Values{
		"q": {searchQuery},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build search request: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %v", err)
	}
	req.Header.Set("User-Agent", webToolUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.getHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web search request failed with status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchResponseBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read web search response: %v", err)
	}

	results, err := parseDuckDuckGoHTMLResults(body, rawResultLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to parse web search response: %v", err)
	}

	return filterWebSearchResults(results, includeDomains, excludeDomains, maxResults), nil
}

func (t *WebSearchTool) fetchInstantAnswerResults(ctx context.Context, query string, maxResults int, includeDomains, excludeDomains []string) ([]webSearchResult, error) {
	reqURL, err := buildURL(t.getInstantAnswerEndpoint(), url.Values{
		"q":             {query},
		"format":        {"json"},
		"no_html":       {"1"},
		"no_redirect":   {"1"},
		"skip_disambig": {"0"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build instant-answer request: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create instant-answer request: %v", err)
	}
	req.Header.Set("User-Agent", webToolUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.getHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("instant-answer request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instant-answer request failed with status %s", resp.Status)
	}

	var payload duckDuckGoInstantAnswerResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSearchResponseBodyBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode instant-answer response: %v", err)
	}

	results := instantAnswerToResults(payload, expandRawWebResultLimit(maxResults, true))
	return filterWebSearchResults(results, includeDomains, excludeDomains, maxResults), nil
}

func (t *WebSearchTool) getHTTPClient() *http.Client {
	if t.client != nil {
		return t.client
	}
	return &http.Client{Timeout: defaultWebSearchTimeout}
}

func (t *WebSearchTool) getSearchEndpoint() string {
	if strings.TrimSpace(t.searchEndpoint) != "" {
		return strings.TrimSpace(t.searchEndpoint)
	}
	if endpoint := strings.TrimSpace(os.Getenv("MCODE_WEB_SEARCH_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	return defaultWebSearchEndpoint
}

func (t *WebSearchTool) getInstantAnswerEndpoint() string {
	if strings.TrimSpace(t.instantAnswerEndpoint) != "" {
		return strings.TrimSpace(t.instantAnswerEndpoint)
	}
	if endpoint := strings.TrimSpace(os.Getenv("MCODE_WEB_SEARCH_INSTANT_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	return defaultInstantAnswerEndpoint
}

func clampWebSearchResults(value int) int {
	if value <= 0 {
		return defaultWebSearchMaxResults
	}
	if value > maxAllowedWebSearchResults {
		return maxAllowedWebSearchResults
	}
	return value
}

func expandRawWebResultLimit(maxResults int, filtered bool) int {
	if !filtered {
		return maxResults
	}
	expanded := maxResults * 5
	if expanded < maxResults {
		expanded = maxResults
	}
	if expanded > 50 {
		expanded = 50
	}
	return expanded
}

func buildURL(base string, queryParams url.Values) (string, error) {
	parsedURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	values := parsedURL.Query()
	for key, list := range queryParams {
		for _, item := range list {
			values.Set(key, item)
		}
	}
	parsedURL.RawQuery = values.Encode()
	return parsedURL.String(), nil
}

func parseDuckDuckGoHTMLResults(body []byte, maxResults int) ([]webSearchResult, error) {
	doc, err := xhtml.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	var results []webSearchResult
	seen := make(map[string]bool)

	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || len(results) >= maxResults {
			return
		}

		if node.Type == xhtml.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			title := normalizeWhitespace(nodeText(node))
			href := normalizeDuckDuckGoURL(getAttr(node, "href"))
			key := title + "|" + href
			if title != "" && href != "" && !seen[key] {
				seen[key] = true
				results = append(results, webSearchResult{
					Title:   title,
					URL:     href,
					Snippet: extractSnippet(node),
				})
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
			if len(results) >= maxResults {
				return
			}
		}
	}

	walk(doc)
	return results, nil
}

func extractSnippet(anchor *xhtml.Node) string {
	container := findResultContainer(anchor)
	if container == nil {
		return ""
	}

	if snippetNode := findNodeByClass(container, "result__snippet", "result-snippet"); snippetNode != nil {
		return truncateWebSnippet(normalizeWhitespace(nodeText(snippetNode)))
	}
	return ""
}

func findResultContainer(node *xhtml.Node) *xhtml.Node {
	for current := node.Parent; current != nil; current = current.Parent {
		if current.Type == xhtml.ElementNode && hasClass(current, "result") {
			return current
		}
	}
	return nil
}

func findNodeByClass(node *xhtml.Node, classes ...string) *xhtml.Node {
	if node == nil {
		return nil
	}
	if node.Type == xhtml.ElementNode && hasClass(node, classes...) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findNodeByClass(child, classes...); found != nil {
			return found
		}
	}
	return nil
}

func hasClass(node *xhtml.Node, classes ...string) bool {
	classAttr := getAttr(node, "class")
	if classAttr == "" {
		return false
	}

	classParts := strings.Fields(classAttr)
	for _, className := range classes {
		for _, part := range classParts {
			if part == className {
				return true
			}
		}
	}
	return false
}

func getAttr(node *xhtml.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func nodeText(node *xhtml.Node) string {
	var parts []string
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == nil {
			return
		}
		if current.Type == xhtml.TextNode {
			text := strings.TrimSpace(html.UnescapeString(current.Data))
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}

func normalizeDuckDuckGoURL(rawURL string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}

	if strings.Contains(parsedURL.Host, "duckduckgo.com") {
		if redirectURL := parsedURL.Query().Get("uddg"); redirectURL != "" {
			decodedURL, err := url.QueryUnescape(redirectURL)
			if err == nil {
				return decodedURL
			}
			return redirectURL
		}
	}
	return parsedURL.String()
}

func instantAnswerToResults(payload duckDuckGoInstantAnswerResponse, maxResults int) []webSearchResult {
	results := make([]webSearchResult, 0, maxResults)

	addResult := func(result webSearchResult) {
		if len(results) >= maxResults {
			return
		}
		if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.URL) == "" {
			return
		}
		if result.Snippet == "" {
			result.Snippet = "No summary available."
		}
		results = append(results, result)
	}

	if payload.AbstractText != "" && payload.AbstractURL != "" {
		title := strings.TrimSpace(payload.Heading)
		if title == "" {
			title = payload.AbstractURL
		}
		addResult(webSearchResult{
			Title:   title,
			URL:     strings.TrimSpace(payload.AbstractURL),
			Snippet: truncateWebSnippet(normalizeWhitespace(payload.AbstractText)),
		})
	}

	var walkTopics func([]duckDuckGoInstantAnswerTopic)
	walkTopics = func(topics []duckDuckGoInstantAnswerTopic) {
		for _, topic := range topics {
			if len(results) >= maxResults {
				return
			}
			if len(topic.Topics) > 0 {
				walkTopics(topic.Topics)
				continue
			}
			title := topic.FirstURL
			snippet := normalizeWhitespace(topic.Text)
			if snippet != "" {
				parts := strings.SplitN(snippet, " - ", 2)
				if len(parts) == 2 {
					title = parts[0]
					snippet = parts[1]
				}
			}
			addResult(webSearchResult{
				Title:   strings.TrimSpace(title),
				URL:     strings.TrimSpace(topic.FirstURL),
				Snippet: truncateWebSnippet(snippet),
			})
		}
	}

	walkTopics(payload.RelatedTopics)
	return results
}

func truncateWebSnippet(snippet string) string {
	const maxSnippetLength = 240
	if len(snippet) <= maxSnippetLength {
		return snippet
	}
	return strings.TrimSpace(snippet[:maxSnippetLength-3]) + "..."
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizeDomainFilters(domains []string) []string {
	var normalized []string
	seen := make(map[string]bool)
	for _, domain := range domains {
		host := normalizeDomain(domain)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		normalized = append(normalized, host)
	}
	return normalized
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	value = strings.TrimPrefix(value, "*.")
	value = strings.TrimPrefix(value, ".")

	if strings.Contains(value, "://") {
		parsedURL, err := url.Parse(value)
		if err == nil {
			value = parsedURL.Hostname()
		}
	}

	if idx := strings.Index(value, "/"); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, ":"); idx >= 0 {
		value = value[:idx]
	}

	return strings.TrimSpace(value)
}

func withIncludeDomainOperators(query string, includeDomains []string) string {
	if len(includeDomains) == 0 {
		return query
	}
	var operators []string
	for _, domain := range includeDomains {
		operators = append(operators, "site:"+domain)
	}
	return strings.TrimSpace(query + " " + strings.Join(operators, " OR "))
}

func filterWebSearchResults(results []webSearchResult, includeDomains, excludeDomains []string, maxResults int) []webSearchResult {
	filtered := make([]webSearchResult, 0, maxResults)
	for _, result := range results {
		host := extractURLHostname(result.URL)
		if host == "" {
			continue
		}
		if len(includeDomains) > 0 && !matchesAnyDomain(host, includeDomains) {
			continue
		}
		if matchesAnyDomain(host, excludeDomains) {
			continue
		}
		filtered = append(filtered, result)
		if len(filtered) >= maxResults {
			break
		}
	}
	return filtered
}

func extractURLHostname(rawURL string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
}

func matchesAnyDomain(host string, domains []string) bool {
	for _, domain := range domains {
		if domainMatches(host, domain) {
			return true
		}
	}
	return false
}

func domainMatches(host, domain string) bool {
	host = normalizeDomain(host)
	domain = normalizeDomain(domain)
	if host == "" || domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func formatNoWebSearchResults(query string, includeDomains, excludeDomains []string) string {
	if len(includeDomains) == 0 && len(excludeDomains) == 0 {
		return fmt.Sprintf("No web results found for %q", query)
	}
	return fmt.Sprintf("No web results found for %q with filters: %s", query, formatDomainFilterSummary(includeDomains, excludeDomains))
}

func formatDomainFilterSummary(includeDomains, excludeDomains []string) string {
	var parts []string
	if len(includeDomains) > 0 {
		parts = append(parts, "include="+strings.Join(includeDomains, ", "))
	}
	if len(excludeDomains) > 0 {
		parts = append(parts, "exclude="+strings.Join(excludeDomains, ", "))
	}
	return strings.Join(parts, "; ")
}

func formatWebSearchResults(query string, includeDomains, excludeDomains []string, results []webSearchResult) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Web results for %q", query))
	if summary := formatDomainFilterSummary(includeDomains, excludeDomains); summary != "" {
		builder.WriteString(" [")
		builder.WriteString(summary)
		builder.WriteString("]")
	}
	builder.WriteString(":\n")

	for index, result := range results {
		builder.WriteString(strconv.Itoa(index + 1))
		builder.WriteString(". ")
		builder.WriteString(result.Title)
		builder.WriteString("\n")
		builder.WriteString("   URL: ")
		builder.WriteString(result.URL)
		builder.WriteString("\n")
		if result.Snippet != "" {
			builder.WriteString("   Snippet: ")
			builder.WriteString(result.Snippet)
			builder.WriteString("\n")
		}
	}

	return strings.TrimRight(builder.String(), "\n")
}
