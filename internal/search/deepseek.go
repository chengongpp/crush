package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func init() {
	Register("deepseek", NewDeepSeek)
}

// deepseekSearchEndpoint is the DeepSeek Anthropic-compatible messages
// endpoint that accepts the server-side web_search tool.
const deepseekSearchEndpoint = "https://api.deepseek.com/anthropic/v1/messages"

// deepseekSearchTimeout bounds a single server-side search call so a
// hung upstream never wedges the agent.
const deepseekSearchTimeout = 20 * time.Second

// DeepSeek searches the web through DeepSeek's server-side web_search
// tool, avoiding a second scraping hop and rate limits.
type DeepSeek struct {
	client   *http.Client
	apiKey   string
	model    string
	endpoint string
}

// NewDeepSeek builds the DeepSeek provider.
func NewDeepSeek(client *http.Client, cfg Config) (Provider, error) {
	model := cfg.DeepSeekModel
	if model == "" {
		model = "deepseek-v4-flash"
	}
	return &DeepSeek{
		client:   client,
		apiKey:   cfg.DeepSeekAPIKey,
		model:    model,
		endpoint: deepseekSearchEndpoint,
	}, nil
}

// Name returns the provider identifier.
func (p *DeepSeek) Name() string { return "deepseek" }

// Search asks DeepSeek to run its server-side web search for the query
// and maps the returned tool results onto [Result].
func (p *DeepSeek) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if p.apiKey == "" {
		return nil, errors.New("DeepSeek search requires DEEPSEEK_API_KEY")
	}
	if maxResults <= 0 {
		maxResults = 10
	}

	payload, err := json.Marshal(deepseekRequest{
		Model:     p.model,
		MaxTokens: 4096,
		Messages: []deepseekMessage{{
			Role: "user",
			Content: []deepseekContent{{
				Type: "text",
				Text: "Perform a web search for the query: " + query,
			}},
		}},
		Tools: []deepseekTool{{
			Type:    "web_search_20250305",
			Name:    "web_search",
			MaxUses: 1,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("authorization", "Bearer "+p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	searchCtx, cancel := context.WithTimeout(ctx, deepseekSearchTimeout)
	defer cancel()

	resp, err := p.client.Do(req.WithContext(searchCtx))
	if err != nil {
		return nil, fmt.Errorf("failed to execute search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("DeepSeek API key is invalid (HTTP 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DeepSeek search failed with status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var dsResp deepseekResponse
	if err := json.Unmarshal(body, &dsResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Citations attached to text blocks carry the snippet text for the
	// cited URLs.
	snippets := map[string]string{}
	for _, block := range dsResp.Content {
		if block.Type != "text" {
			continue
		}
		for _, cite := range block.Citations {
			if cite.URL != "" && cite.CitedText != "" {
				if _, seen := snippets[cite.URL]; !seen {
					snippets[cite.URL] = cite.CitedText
				}
			}
		}
	}

	var results []Result
	seen := map[string]bool{}
	for _, block := range dsResp.Content {
		if block.Type != "web_search_tool_result" {
			continue
		}
		for _, item := range block.Content {
			if item.Type != "web_search_result" || item.URL == "" {
				continue
			}
			if seen[item.URL] {
				continue
			}
			seen[item.URL] = true
			results = append(results, Result{
				Title:   item.Title,
				Link:    item.URL,
				Snippet: snippets[item.URL],
			})
			if len(results) >= maxResults {
				break
			}
		}
		if len(results) >= maxResults {
			break
		}
	}
	for i := range results {
		results[i].Position = i + 1
	}
	return results, nil
}

type deepseekRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Messages  []deepseekMessage `json:"messages"`
	Tools     []deepseekTool    `json:"tools"`
}

type deepseekMessage struct {
	Role    string            `json:"role"`
	Content []deepseekContent `json:"content"`
}

type deepseekContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type deepseekTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses"`
}

type deepseekResponse struct {
	Content []deepseekBlock `json:"content"`
}

type deepseekBlock struct {
	Type      string               `json:"type"`
	Citations []deepseekCitation   `json:"citations,omitempty"`
	Content   []deepseekResultItem `json:"content,omitempty"`
}

type deepseekCitation struct {
	URL       string `json:"url"`
	CitedText string `json:"cited_text"`
}

type deepseekResultItem struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
}
