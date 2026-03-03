package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var webHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

const (
	webFetchMaxBodyBytes  = 2 * 1024 * 1024 // 2 MB
	webFetchDefaultMaxLen = 50000
	webSearchDefaultMax   = 10
	webSearchMaxResults   = 20
)

// Endpoint URLs are variables so tests can override them.
var (
	braveSearchEndpoint    = "https://api.search.brave.com/res/v1/web/search"
	duckDuckGoHTMLEndpoint = "https://html.duckduckgo.com/html/"
)

// handleWebSearch performs a web search using Brave (if API key configured) or DuckDuckGo fallback.
func (e *NativeToolExecutor) handleWebSearch(ctx context.Context, input map[string]any) (map[string]any, error) {
	query, ok := readString(input, "query")
	if !ok || query == "" {
		return map[string]any{"error": "query is required"}, nil
	}
	maxResults := readInt(input, "max_results", webSearchDefaultMax)
	maxResults = clamp(maxResults, 1, webSearchMaxResults)

	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}

	// Check for Brave Search API key via secret resolver.
	var braveKey string
	if e.secrets != nil {
		val, secretErr := e.secrets.Get(ctx, scope.organizationID, "brave-search-api-key")
		if secretErr == nil && strings.TrimSpace(val) != "" {
			braveKey = strings.TrimSpace(val)
		}
	}

	if braveKey != "" {
		results, searchErr := braveWebSearch(ctx, braveKey, query, maxResults)
		if searchErr != nil {
			return map[string]any{"error": fmt.Sprintf("brave search failed: %v", searchErr)}, nil
		}
		return map[string]any{"results": results, "source": "brave"}, nil
	}

	results, searchErr := duckDuckGoHTMLSearch(ctx, query, maxResults)
	if searchErr != nil {
		return map[string]any{"error": fmt.Sprintf("duckduckgo search failed: %v", searchErr)}, nil
	}
	return map[string]any{"results": results, "source": "duckduckgo"}, nil
}

// handleWebFetch fetches a URL and returns the page content as plain text.
func (e *NativeToolExecutor) handleWebFetch(ctx context.Context, input map[string]any) (map[string]any, error) {
	rawURL, ok := readString(input, "url")
	if !ok || rawURL == "" {
		return map[string]any{"error": "url is required"}, nil
	}
	maxLen := readInt(input, "max_length", webFetchDefaultMaxLen)
	if maxLen < 1 {
		maxLen = webFetchDefaultMaxLen
	}
	includeLinks := readBool(input, "include_links", false)

	if err := validateFetchURL(rawURL); err != nil {
		return map[string]any{"error": err.Error()}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("invalid url: %v", err)}, nil
	}
	req.Header.Set("User-Agent", "OtterCamp/1.0 (web-fetch)")
	req.Header.Set("Accept", "text/html, text/plain;q=0.9, */*;q=0.5")

	resp, err := webHTTPClient.Do(req)
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBodyBytes))
	if err != nil {
		return map[string]any{"error": fmt.Sprintf("read body failed: %v", err)}, nil
	}

	title, content, truncated := htmlToText(body, maxLen, includeLinks)
	return map[string]any{
		"url":            rawURL,
		"title":          title,
		"content":        content,
		"content_length": len(content),
		"truncated":      truncated,
	}, nil
}

// validateFetchURL rejects private IPs, localhost, and non-HTTP schemes (SSRF protection).
func validateFetchURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("only http and https schemes are allowed")
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("url must have a hostname")
	}

	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "ip6-localhost" || lower == "ip6-loopback" {
		return fmt.Errorf("requests to localhost are not allowed")
	}

	ip := net.ParseIP(hostname)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("requests to private/internal addresses are not allowed")
		}
	}

	return nil
}

// htmlToText converts raw HTML bytes to plain text, stripping scripts/styles
// and preserving block structure. Returns title, content, and whether truncated.
func htmlToText(body []byte, maxLen int, includeLinks bool) (title string, content string, truncated bool) {
	tokenizer := html.NewTokenizer(strings.NewReader(string(body)))
	var (
		buf       strings.Builder
		inTitle   bool
		titleBuf  strings.Builder
		skipDepth int
		lastWasNL bool
	)

	blockElements := map[string]bool{
		"p": true, "div": true, "br": true, "h1": true, "h2": true, "h3": true,
		"h4": true, "h5": true, "h6": true, "li": true, "tr": true, "blockquote": true,
		"pre": true, "hr": true, "section": true, "article": true, "header": true,
		"footer": true, "nav": true, "main": true, "aside": true, "dt": true, "dd": true,
	}
	skipElements := map[string]bool{
		"script": true, "style": true, "noscript": true, "svg": true,
	}

	writeNewline := func() {
		if !lastWasNL && buf.Len() > 0 {
			buf.WriteByte('\n')
			lastWasNL = true
		}
	}

	for {
		if maxLen > 0 && buf.Len() >= maxLen {
			truncated = true
			break
		}
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			goto done
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			tag := strings.ToLower(string(tn))
			if tag == "title" {
				inTitle = true
			}
			if skipElements[tag] {
				skipDepth++
				continue
			}
			if skipDepth > 0 {
				continue
			}
			if blockElements[tag] {
				writeNewline()
			}
			_ = includeLinks // reserved for future link extraction
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := strings.ToLower(string(tn))
			if tag == "title" {
				inTitle = false
			}
			if skipElements[tag] && skipDepth > 0 {
				skipDepth--
				continue
			}
			if skipDepth > 0 {
				continue
			}
			if blockElements[tag] {
				writeNewline()
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			text := strings.TrimSpace(string(tokenizer.Text()))
			if text == "" {
				continue
			}
			if inTitle {
				titleBuf.WriteString(text)
			}
			buf.WriteString(text)
			buf.WriteByte(' ')
			lastWasNL = false
		}
	}
done:

	title = strings.TrimSpace(titleBuf.String())
	raw := buf.String()
	if maxLen > 0 && len(raw) > maxLen {
		raw = raw[:maxLen]
		truncated = true
	}

	lines := strings.Split(raw, "\n")
	var cleaned []string
	prevEmpty := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevEmpty {
				cleaned = append(cleaned, "")
			}
			prevEmpty = true
		} else {
			cleaned = append(cleaned, trimmed)
			prevEmpty = false
		}
	}
	content = strings.TrimSpace(strings.Join(cleaned, "\n"))
	return title, content, truncated
}

// braveWebSearch calls the Brave Search API and returns parsed results.
func braveWebSearch(ctx context.Context, apiKey, query string, maxResults int) ([]map[string]any, error) {
	reqURL := fmt.Sprintf("%s?q=%s&count=%d", braveSearchEndpoint, url.QueryEscape(query), maxResults)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := webHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("brave api returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBodyBytes))
	if err != nil {
		return nil, err
	}

	return parseBraveResponse(body, maxResults)
}

// parseBraveResponse extracts search results from the Brave API JSON response.
func parseBraveResponse(body []byte, maxResults int) ([]map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse brave response: %w", err)
	}

	webObj, _ := raw["web"].(map[string]any)
	if webObj == nil {
		return []map[string]any{}, nil
	}
	rawResults, _ := webObj["results"].([]any)

	results := make([]map[string]any, 0, len(rawResults))
	for i, item := range rawResults {
		if i >= maxResults {
			break
		}
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result := map[string]any{
			"title":   stringVal(m, "title"),
			"url":     stringVal(m, "url"),
			"snippet": stringVal(m, "description"),
		}
		results = append(results, result)
	}
	return results, nil
}

// duckDuckGoHTMLSearch performs a search via DDG's HTML interface.
func duckDuckGoHTMLSearch(ctx context.Context, query string, maxResults int) ([]map[string]any, error) {
	form := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, duckDuckGoHTMLEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "OtterCamp/1.0 (web-search)")

	resp, err := webHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBodyBytes))
	if err != nil {
		return nil, err
	}

	return parseDuckDuckGoHTML(body, maxResults)
}

// parseDuckDuckGoHTML extracts search results from DDG's HTML response.
// DDG HTML results use <a class="result__a" href="..."> for title+url
// and <a class="result__snippet"> for snippet text.
func parseDuckDuckGoHTML(body []byte, maxResults int) ([]map[string]any, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("parse ddg html: %w", err)
	}

	var results []map[string]any

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			cls := nodeAttr(n, "class")
			if strings.Contains(cls, "result__a") {
				href := nodeAttr(n, "href")
				title := textContent(n)
				if href != "" {
					results = append(results, map[string]any{
						"title":   strings.TrimSpace(title),
						"url":     href,
						"snippet": "",
					})
				}
			}
			if strings.Contains(cls, "result__snippet") && len(results) > 0 {
				results[len(results)-1]["snippet"] = strings.TrimSpace(textContent(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return results, nil
}

// nodeAttr returns the value of a named attribute on an HTML node.
func nodeAttr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// textContent returns the concatenated text content of a node tree.
func textContent(n *html.Node) string {
	var buf strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return buf.String()
}

// stringVal safely reads a string value from a map.
func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// setBraveEndpoint overrides the Brave search URL (for testing).
func setBraveEndpoint(url string) { braveSearchEndpoint = url }

// setDDGEndpoint overrides the DuckDuckGo search URL (for testing).
func setDDGEndpoint(url string) { duckDuckGoHTMLEndpoint = url }
