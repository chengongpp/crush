package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func init() {
	Register("bing", NewBing)
}

// bingSearchEndpoint is a package var so tests can point the search at a
// local httptest server.
var bingSearchEndpoint = "https://www.bing.com/search"

// Bing scrapes the Bing results page.
type Bing struct {
	client   *http.Client
	market   string
	endpoint string
}

// NewBing builds the Bing provider.
func NewBing(client *http.Client, cfg Config) (Provider, error) {
	market := cfg.BingMarket
	if market == "" {
		market = "zh-CN"
	}
	return &Bing{client: client, market: market, endpoint: bingSearchEndpoint}, nil
}

// Name returns the provider identifier.
func (p *Bing) Name() string { return "bing" }

// Search executes the query against Bing and parses the organic result
// blocks.
func (p *Bing) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if maxResults <= 0 {
		maxResults = 10
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("mkt", p.market)
	searchURL := p.endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	setRandomizedHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search failed with status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return parseBingResults(string(body), maxResults)
}

func parseBingResults(htmlContent string, maxResults int) ([]Result, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var results []Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "li" && hasClass(n, "b_algo") {
			result := parseBingAlgo(n)
			if result.Link != "" {
				result.Position = len(results) + 1
				results = append(results, result)
			}
		}
		for c := n.FirstChild; c != nil && len(results) < maxResults; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results, nil
}

// parseBingAlgo extracts the link, title, and snippet from a single
// <li class="b_algo"> result block.
func parseBingAlgo(li *html.Node) Result {
	var result Result
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		// The first external link is the result URL.
		if result.Link == "" && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.HasPrefix(attr.Val, "http") {
					result.Link = attr.Val
					break
				}
			}
		}
		if result.Title == "" && n.Data == "h2" {
			result.Title = getTextContent(n)
		}
		if result.Snippet == "" && n.Data == "p" {
			result.Snippet = getTextContent(n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(li)
	return result
}
