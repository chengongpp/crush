package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeepSeekSearch verifies the server-side web_search tool request
// shape and the mapping of tool results onto Result.
func TestDeepSeekSearch(t *testing.T) {
	var gotReq deepseekRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [
				{"type": "text", "citations": [{"url": "https://example.com/a", "cited_text": "Snippet A"}]},
				{"type": "web_search_tool_result", "content": [
					{"type": "web_search_result", "url": "https://example.com/a", "title": "Title A"},
					{"type": "web_search_result", "url": "https://example.com/b", "title": "Title B"}
				]}
			]
		}`))
	}))
	defer srv.Close()

	p := &DeepSeek{client: srv.Client(), apiKey: "test-key", model: "deepseek-v4-flash", endpoint: srv.URL}
	results, err := p.Search(context.Background(), "hello world", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gotReq.Tools) != 1 || gotReq.Tools[0].Type != "web_search_20250305" || gotReq.Tools[0].Name != "web_search" {
		t.Fatalf("unexpected tools in request: %+v", gotReq.Tools)
	}
	if gotReq.Model != "deepseek-v4-flash" {
		t.Fatalf("unexpected model: %s", gotReq.Model)
	}
	msgText := gotReq.Messages[0].Content[0].Text
	if !strings.Contains(msgText, "hello world") {
		t.Fatalf("query missing from prompt: %q", msgText)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Title != "Title A" || results[0].Link != "https://example.com/a" || results[0].Snippet != "Snippet A" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].Position != 1 || results[1].Position != 2 {
		t.Fatalf("positions not assigned: %+v", results)
	}
}

// TestDeepSeekRequiresKey verifies a clear error when no API key is set.
func TestDeepSeekRequiresKey(t *testing.T) {
	p := &DeepSeek{client: http.DefaultClient, model: "deepseek-v4-flash", endpoint: "https://example.com"}
	_, err := p.Search(context.Background(), "hello", 10)
	if err == nil || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

// TestDeepSeekUnauthorized verifies HTTP 401 is reported as an invalid key.
func TestDeepSeekUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := &DeepSeek{client: srv.Client(), apiKey: "bad-key", model: "deepseek-v4-flash", endpoint: srv.URL}
	_, err := p.Search(context.Background(), "hello", 10)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

// TestDeepSeekDeduplicates verifies duplicate URLs collapse to one result.
func TestDeepSeekDeduplicates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [{"type": "web_search_tool_result", "content": [
				{"type": "web_search_result", "url": "https://example.com/a", "title": "A"},
				{"type": "web_search_result", "url": "https://example.com/a", "title": "A again"}
			]}]
		}`))
	}))
	defer srv.Close()

	p := &DeepSeek{client: srv.Client(), apiKey: "test-key", model: "deepseek-v4-flash", endpoint: srv.URL}
	results, err := p.Search(context.Background(), "hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}
