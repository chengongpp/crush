package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// searchProviderTestWorkspace is a minimal workspace stub exposing a
// fixed config for the search provider dialog.
type searchProviderTestWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *searchProviderTestWorkspace) Config() *config.Config {
	return w.cfg
}

func newTestSearchProvider(t *testing.T, cfg *config.Config) *SearchProvider {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{
		Styles:    &s,
		Workspace: &searchProviderTestWorkspace{cfg: cfg},
	}
	sp, err := NewSearchProvider(com)
	require.NoError(t, err)
	return sp
}

// TestSearchProviderListsProviders verifies all registered providers are
// listed and the default is marked current.
func TestSearchProviderListsProviders(t *testing.T) {
	t.Parallel()

	sp := newTestSearchProvider(t, &config.Config{})
	items := sp.list.FilteredItems()
	require.Len(t, items, 3)

	var duckduckgoItem *SearchProviderItem
	for _, it := range items {
		item, ok := it.(*SearchProviderItem)
		require.True(t, ok)
		if item.name == "duckduckgo" {
			duckduckgoItem = item
		}
	}
	require.NotNil(t, duckduckgoItem)
	require.True(t, duckduckgoItem.isCurrent)
}

// TestSearchProviderMarksCurrent verifies the configured provider is
// marked current.
func TestSearchProviderMarksCurrent(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Options: &config.Options{SearchProvider: "deepseek"}}
	sp := newTestSearchProvider(t, cfg)

	var deepseekItem *SearchProviderItem
	for _, it := range sp.list.FilteredItems() {
		item, ok := it.(*SearchProviderItem)
		require.True(t, ok)
		if item.name == "deepseek" {
			deepseekItem = item
		}
	}
	require.NotNil(t, deepseekItem)
	require.True(t, deepseekItem.isCurrent)
}

// TestSearchProviderSelectEmitsAction verifies enter emits the selection
// action for the highlighted provider.
func TestSearchProviderSelectEmitsAction(t *testing.T) {
	t.Parallel()

	sp := newTestSearchProvider(t, &config.Config{})
	action := sp.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	sel, ok := action.(ActionSelectSearchProvider)
	require.True(t, ok)
	require.Equal(t, "duckduckgo", sel.Provider)
}

// TestSearchProviderNavigationMovesSelection verifies arrow keys move the
// selection before confirming. The current provider (duckduckgo) is
// preselected; pressing up moves to the previous item.
func TestSearchProviderNavigationMovesSelection(t *testing.T) {
	t.Parallel()

	sp := newTestSearchProvider(t, &config.Config{})
	sp.HandleMsg(tea.KeyPressMsg{Code: tea.KeyUp})
	action := sp.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	sel, ok := action.(ActionSelectSearchProvider)
	require.True(t, ok)
	require.Equal(t, "deepseek", sel.Provider)
}
