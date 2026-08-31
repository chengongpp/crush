package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/search"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

const (
	// SearchProviderID is the identifier for the search provider dialog.
	SearchProviderID              = "search_provider"
	searchProviderDialogMaxWidth  = 50
	searchProviderDialogMinHeight = 8
	searchProviderDialogMaxHeight = 16
)

// searchProviderLabels maps provider identifiers to their display names.
var searchProviderLabels = map[string]string{
	"duckduckgo": "DuckDuckGo",
	"deepseek":   "DeepSeek",
	"bing":       "Bing",
}

// SearchProviderLabel returns the display label for a search provider
// identifier.
func SearchProviderLabel(name string) (string, bool) {
	label, ok := searchProviderLabels[name]
	return label, ok
}

// SearchProvider represents a dialog for selecting the web search provider.
type SearchProvider struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// SearchProviderItem represents a search provider list item.
type SearchProviderItem struct {
	*list.Versioned
	name      string
	title     string
	isCurrent bool
	t         *styles.Styles
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

// Finished implements list.Item. Search provider items are render-stable
// outside of explicit SetFocused / SetMatch.
func (s *SearchProviderItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*SearchProvider)(nil)
	_ ListItem = (*SearchProviderItem)(nil)
)

// NewSearchProvider creates a new search provider dialog.
func NewSearchProvider(com *common.Common) (*SearchProvider, error) {
	s := &SearchProvider{com: com}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()
	s.help = help

	s.list = list.NewFilterableList()
	s.list.Focus()

	s.input = textinput.New()
	s.input.SetVirtualCursor(false)
	s.input.Placeholder = "Type to filter"
	s.input.SetStyles(com.Styles.TextInput)
	s.input.Focus()

	s.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	s.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	s.keyMap.Close = CloseKey

	if err := s.setSearchProviderItems(); err != nil {
		return nil, err
	}

	return s, nil
}

// ID implements Dialog.
func (s *SearchProvider) ID() string {
	return SearchProviderID
}

// HandleMsg implements [Dialog].
func (s *SearchProvider) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.Previous):
			s.list.Focus()
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
				s.list.ScrollToBottom()
				break
			}
			s.list.SelectPrev()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.list.Focus()
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
				s.list.ScrollToTop()
				break
			}
			s.list.SelectNext()
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Select):
			selectedItem := s.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			item, ok := selectedItem.(*SearchProviderItem)
			if !ok {
				break
			}
			return ActionSelectSearchProvider{Provider: item.name}
		default:
			prevValue := s.input.Value()
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			value := s.input.Value()
			if value != prevValue {
				s.list.SetFilter(value)
				s.list.ScrollToTop()
				s.list.SetSelected(0)
			}
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (s *SearchProvider) Cursor() *tea.Cursor {
	return InputCursor(s.com.Styles, s.input.Cursor())
}

// Draw implements [Dialog].
func (s *SearchProvider) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(searchProviderDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	s.input.SetWidth(dialogInputTextWidth(t, s.input, innerWidth))

	// Size the dialog to fit the list content, clamped to min/max bounds.
	listTotalHeight := s.list.TotalHeight()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	desiredHeight := heightOffset + listTotalHeight
	maxAvailable := area.Dy() - t.Dialog.View.GetVerticalBorderSize()
	height := max(searchProviderDialogMinHeight, min(searchProviderDialogMaxHeight, desiredHeight, maxAvailable))

	listHeight, listTotalHeight, _ := sizeDialogList(t, s.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "Select Search Provider"
	inputView := t.Dialog.InputPrompt.Render(s.input.View())
	rc.AddPart(inputView)

	visibleCount := len(s.list.FilteredItems())
	if s.list.Height() >= visibleCount {
		s.list.ScrollToTop()
	} else {
		s.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
	listView = joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, s.list.Offset())
	rc.AddPart(listView)
	rc.Help = renderDialogHelp(t, &s.help, s, innerWidth)

	view := rc.Render()

	cur := s.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (s *SearchProvider) ShortHelp() []key.Binding {
	return []key.Binding{
		s.keyMap.UpDown,
		s.keyMap.Select,
		s.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (s *SearchProvider) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		s.keyMap.Select,
		s.keyMap.Next,
		s.keyMap.Previous,
		s.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

// setSearchProviderItems populates the list with the registered providers.
func (s *SearchProvider) setSearchProviderItems() error {
	cfg := s.com.Config()
	current := search.DefaultProvider
	if cfg != nil && cfg.Options != nil && cfg.Options.SearchProvider != "" {
		current = cfg.Options.SearchProvider
	}

	names := search.Names()
	items := make([]list.FilterableItem, 0, len(names))
	selectedIndex := 0
	for i, name := range names {
		title := searchProviderLabels[name]
		if title == "" {
			title = name
		}
		item := &SearchProviderItem{
			Versioned: list.NewVersioned(),
			name:      name,
			title:     title,
			isCurrent: name == current,
			t:         s.com.Styles,
		}
		items = append(items, item)
		if name == current {
			selectedIndex = i
		}
	}

	s.list.SetItems(items...)
	s.list.SetSelected(selectedIndex)
	s.list.ScrollToSelected()
	return nil
}

// Filter returns the filter value for the search provider item.
func (s *SearchProviderItem) Filter() string {
	return s.title
}

// ID returns the unique identifier for the search provider.
func (s *SearchProviderItem) ID() string {
	return s.name
}

// SetFocused sets the focus state of the search provider item.
func (s *SearchProviderItem) SetFocused(focused bool) {
	if s.focused == focused {
		return
	}
	s.cache = nil
	s.focused = focused
	if s.Versioned != nil {
		s.Bump()
	}
}

// SetMatch sets the fuzzy match for the search provider item.
func (s *SearchProviderItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(s.m, m) {
		return
	}
	s.cache = nil
	s.m = m
	if s.Versioned != nil {
		s.Bump()
	}
}

// Render returns the string representation of the search provider item.
func (s *SearchProviderItem) Render(width int) string {
	info := ""
	if s.isCurrent {
		info = "current"
	}
	styles := ListItemStyles{
		ItemBlurred:     s.t.Dialog.NormalItem,
		ItemFocused:     s.t.Dialog.SelectedItem,
		InfoTextBlurred: s.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: s.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(styles, s.title, info, s.focused, width, s.cache, &s.m)
}
