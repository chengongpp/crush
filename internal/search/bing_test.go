package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const bingSamplePage = `<html><body><ol id="b_results">
<li class="b_algo">
	<h2><a href="https://example.com/first">First Result</a></h2>
	<div class="b_caption"><p>A snippet for the first result.</p></div>
</li>
<li class="b_algo">
	<h2><a href="https://example.com/second">Second Result</a></h2>
	<div class="b_caption"><p>A snippet for the second result.</p></div>
</li>
<li class="b_no">Not an organic result.</li>
</ol></body></html>`

// TestBingSearch verifies the b_algo blocks are extracted with title,
// link, and snippet.
func TestBingSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mkt") != "zh-CN" {
			t.Errorf("missing market param: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(bingSamplePage))
	}))
	defer srv.Close()

	p := &Bing{client: srv.Client(), market: "zh-CN", endpoint: srv.URL}
	results, err := p.Search(context.Background(), "example", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Title != "First Result" || results[0].Link != "https://example.com/first" || results[0].Snippet != "A snippet for the first result." {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].Position != 1 || results[1].Position != 2 {
		t.Fatalf("positions not assigned: %+v", results)
	}
}

// TestBingRespectsMaxResults verifies truncation at the requested limit.
func TestBingRespectsMaxResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(bingSamplePage))
	}))
	defer srv.Close()

	p := &Bing{client: srv.Client(), market: "zh-CN", endpoint: srv.URL}
	results, err := p.Search(context.Background(), "example", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

// TestBingSkipsEmptyBlocks verifies blocks without an external link are
// dropped.
func TestBingSkipsEmptyBlocks(t *testing.T) {
	page := `<html><body><ol id="b_results">
<li class="b_algo"><h2><a href="/relative">No scheme</a></h2></li>
<li class="b_algo"><h2><a href="https://example.com/ok">OK</a></h2><p>fine</p></li>
</ol></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	p := &Bing{client: srv.Client(), market: "zh-CN", endpoint: srv.URL}
	results, err := p.Search(context.Background(), "example", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Link != "https://example.com/ok" {
		t.Fatalf("unexpected results: %+v", results)
	}
}
