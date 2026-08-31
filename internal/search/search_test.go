package search

import (
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

func newProviderMap(id, apiKey string) *csync.Map[string, config.ProviderConfig] {
	return csync.NewMapFrom(map[string]config.ProviderConfig{
		id: {APIKey: apiKey},
	})
}

// TestRegisteredProviders verifies all built-in providers are registered.
func TestRegisteredProviders(t *testing.T) {
	names := Names()
	require.Contains(t, names, "duckduckgo")
	require.Contains(t, names, "deepseek")
	require.Contains(t, names, "bing")
}

// TestNewDefaultsToDuckDuckGo verifies an empty config picks the default
// provider and an unknown name falls back to it too.
func TestNewDefaultsToDuckDuckGo(t *testing.T) {
	p, err := New(http.DefaultClient, Config{})
	require.NoError(t, err)
	require.Equal(t, "duckduckgo", p.Name())

	p, err = New(http.DefaultClient, Config{Provider: "does-not-exist"})
	require.NoError(t, err)
	require.Equal(t, "duckduckgo", p.Name())
}

// TestNewSelectsProvider verifies explicit selection works.
func TestNewSelectsProvider(t *testing.T) {
	p, err := New(http.DefaultClient, Config{Provider: "deepseek"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", p.Name())

	p, err = New(http.DefaultClient, Config{Provider: "bing"})
	require.NoError(t, err)
	require.Equal(t, "bing", p.Name())
}

// TestFromConfig verifies the provider and DeepSeek key derivation.
func TestFromConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg := &config.Config{}
	require.Equal(t, DefaultProvider, FromConfig(cfg).Provider)

	cfg.Options = &config.Options{SearchProvider: "deepseek"}
	c := FromConfig(cfg)
	require.Equal(t, "deepseek", c.Provider)
	require.Empty(t, c.DeepSeekAPIKey)

	// Fall back to the configured deepseek provider's key.
	cfg.Providers = newProviderMap("deepseek", "sk-from-provider")
	c = FromConfig(cfg)
	require.Equal(t, "sk-from-provider", c.DeepSeekAPIKey)

	// Environment wins over the provider config.
	t.Setenv("DEEPSEEK_API_KEY", "sk-from-env")
	require.Equal(t, "sk-from-env", FromConfig(cfg).DeepSeekAPIKey)
}

// TestFormatResults verifies the rendered output shape.
func TestFormatResults(t *testing.T) {
	out := FormatResults(nil)
	require.Contains(t, out, "No results found")

	out = FormatResults([]Result{{Title: "T", Link: "https://example.com", Snippet: "S", Position: 1}})
	require.Contains(t, out, "T")
	require.Contains(t, out, "https://example.com")
	require.Contains(t, out, "S")
}
