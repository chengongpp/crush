// Package search provides a pluggable web search layer for Crush.
//
// Providers are registered by name (see Register) and selected through
// Config.Provider. DuckDuckGo is registered as the default provider so
// search keeps working out of the box, while DeepSeek and Bing are
// available as alternates.
package search

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/config"
)

// Result represents a single web search result.
type Result struct {
	Title    string
	Link     string
	Snippet  string
	Position int
}

// Provider searches the web for a query.
type Provider interface {
	// Name returns the provider identifier (e.g. "duckduckgo").
	Name() string
	// Search executes the query and returns up to maxResults results.
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}

// Config controls which provider the search layer uses and supplies
// provider-specific credentials.
type Config struct {
	// Provider is the registered provider name. Empty means the default.
	Provider string
	// DeepSeekAPIKey authenticates the DeepSeek server-side search tool.
	DeepSeekAPIKey string
	// DeepSeekModel is the model used for the DeepSeek server-side tool.
	DeepSeekModel string
	// BingMarket is the market/locale used by the Bing provider.
	BingMarket string
}

// Factory builds a Provider. Factories are registered by name so the
// search layer can be extended without touching callers.
type Factory func(client *http.Client, cfg Config) (Provider, error)

// DefaultProvider is the provider used when none is configured.
const DefaultProvider = "duckduckgo"

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a provider factory under name. Register panics on
// duplicate names so misconfiguration surfaces at startup.
func Register(name string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("search: provider already registered: " + name)
	}
	registry[name] = factory
}

// Names returns the registered provider names, sorted.
func Names() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// New returns the provider named by cfg.Provider, falling back to the
// default provider when the name is empty or unknown so a typo in config
// never bricks search entirely.
func New(client *http.Client, cfg Config) (Provider, error) {
	name := cfg.Provider
	if name == "" {
		name = DefaultProvider
	}
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()
	if !ok {
		if name != DefaultProvider {
			slog.Warn("Unknown search provider, falling back to default", "provider", name, "default", DefaultProvider)
			return New(client, Config{Provider: DefaultProvider})
		}
		return nil, fmt.Errorf("search: no provider registered for %q", name)
	}
	return factory(client, cfg)
}

// FromConfig derives search configuration from Crush's config. The
// provider comes from options.search_provider; the DeepSeek API key is
// read from the DEEPSEEK_API_KEY environment variable, falling back to
// the configured "deepseek" provider's key.
func FromConfig(cfg *config.Config) Config {
	c := Config{
		Provider:      DefaultProvider,
		DeepSeekModel: "deepseek-v4-flash",
		BingMarket:    "zh-CN",
	}
	if cfg != nil && cfg.Options != nil && cfg.Options.SearchProvider != "" {
		c.Provider = cfg.Options.SearchProvider
	}
	c.DeepSeekAPIKey = resolveDeepSeekAPIKey(cfg)
	return c
}

// resolveDeepSeekAPIKey returns the DeepSeek API key from the
// environment or the configured "deepseek" provider.
func resolveDeepSeekAPIKey(cfg *config.Config) string {
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		return key
	}
	if cfg == nil || cfg.Providers == nil {
		return ""
	}
	pc, ok := cfg.Providers.Get("deepseek")
	if !ok {
		return ""
	}
	return pc.APIKey
}

// FormatResults renders search results for the model.
func FormatResults(results []Result) string {
	if len(results) == 0 {
		return "No results found. Try rephrasing your search."
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d search results:\n\n", len(results))
	for _, result := range results {
		fmt.Fprintf(&sb, "%d. %s\n", result.Position, result.Title)
		fmt.Fprintf(&sb, "   URL: %s\n", result.Link)
		fmt.Fprintf(&sb, "   Summary: %s\n\n", result.Snippet)
	}
	return sb.String()
}

var (
	lastSearchMu   sync.Mutex
	lastSearchTime time.Time
)

// MaybeDelaySearch adds a random delay if the last search was recent.
func MaybeDelaySearch() {
	lastSearchMu.Lock()
	defer lastSearchMu.Unlock()

	minGap := time.Duration(500+rand.IntN(1500)) * time.Millisecond
	elapsed := time.Since(lastSearchTime)
	if elapsed < minGap {
		time.Sleep(minGap - elapsed)
	}
	lastSearchTime = time.Now()
}
