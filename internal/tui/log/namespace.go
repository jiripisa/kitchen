package log

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/recents"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

const namespaceTitle = "Namespaces"

type namespaceModel struct {
	client  *k8s.Client
	recents *recents.Store

	width, height int

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error
}

func newNamespaceModel(client *k8s.Client, store *recents.Store) *namespaceModel {
	delegate := newPickerDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = namespaceTitle
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Filter = substringFilter
	l.Styles.Title = styles.Title

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &namespaceModel{
		client:  client,
		recents: store,
		list:    l,
		spinner: sp,
		loading: true,
	}
}

func (m *namespaceModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m *namespaceModel) SetSize(w, h int) {
	m.width, m.height = w, h
	// Reserve 1 line for title, 1 for status bar, 1 padding.
	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(w, listH)
}

type namespacesLoadedMsg struct {
	names   []string
	recents []string
}

type loadErrMsg struct{ err error }

func (m *namespaceModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		ns, err := m.client.ListNamespaces(ctx)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return namespacesLoadedMsg{
			names:   ns,
			recents: m.recents.Namespaces(m.client.Context()),
		}
	}
}

func (m *namespaceModel) Update(msg tea.Msg) (*namespaceModel, tea.Cmd) {
	switch msg := msg.(type) {
	case namespacesLoadedMsg:
		items := buildItemsWithRecents(msg.names, msg.recents, func(string) string { return "" })
		m.list.SetItems(items)
		m.loading = false
		return m, nil

	case loadErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		// Type-to-filter: any printable key in the unfiltered state jumps
		// the user straight into the list's filter input.
		if shouldStartFiltering(msg, m.list.FilterState()) {
			return m, startFilteringWith(&m.list, msg)
		}
		switch msg.String() {
		case "esc":
			// While filtering, let the list clear the filter on esc.
			// Otherwise esc takes the user back to the previous screen.
			if m.list.FilterState() == list.Filtering || m.list.FilterState() == list.FilterApplied {
				break
			}
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(simpleItem); ok && it.title != "" {
				selected := it.title
				_ = m.recents.RecordNamespace(m.client.Context(), selected)
				return m, func() tea.Msg { return namespaceSelectedMsg(selected) }
			}
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	prevIdx := m.list.Index()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	skipSeparator(&m.list, prevIdx)
	syncListTitle(&m.list, namespaceTitle)
	return m, cmd
}

// shouldStartFiltering reports whether a key press should jump the list
// straight into its filter input. A printable rune typed while the list is
// unfiltered is the trigger.
func shouldStartFiltering(msg tea.KeyMsg, state list.FilterState) bool {
	return state == list.Unfiltered &&
		msg.Type == tea.KeyRunes &&
		len(msg.Runes) > 0
}

// startFilteringWith opens the list's filter and types `msg` into it. We do
// this by first feeding the list a synthetic "/" (its default filter trigger)
// and then the original key event.
func startFilteringWith(l *list.Model, msg tea.KeyMsg) tea.Cmd {
	openFilter := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
	*l, _ = l.Update(openFilter)
	var cmd tea.Cmd
	*l, cmd = l.Update(msg)
	return cmd
}

// skipSeparator nudges the list cursor past a separatorItem if it landed on
// one after the last update. Direction is inferred from how the cursor moved.
func skipSeparator(l *list.Model, prevIdx int) {
	items := l.Items()
	if len(items) == 0 {
		return
	}
	idx := l.Index()
	if _, ok := items[idx].(separatorItem); !ok {
		return
	}
	if idx > prevIdx {
		l.CursorDown()
	} else if idx < prevIdx {
		l.CursorUp()
	} else {
		// Same index — happens at startup. Push down (out of recents zone).
		l.CursorDown()
	}
}

func (m *namespaceModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "namespace picker"))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s loading namespaces…", m.spinner.View())
		b.WriteByte('\n')
	default:
		b.WriteString(m.list.View())
	}

	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		pickerHint(m.list.FilterState()),
	))
	return b.String()
}
