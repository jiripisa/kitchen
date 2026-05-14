package log

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

type namespaceModel struct {
	client *k8s.Client

	width, height int

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error
}

func newNamespaceModel(client *k8s.Client) *namespaceModel {
	delegate := newPickerDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Namespaces"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = styles.Title

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &namespaceModel{
		client:  client,
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
	names []string
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
		return namespacesLoadedMsg{names: ns}
	}
}

func (m *namespaceModel) Update(msg tea.Msg) (*namespaceModel, tea.Cmd) {
	switch msg := msg.(type) {
	case namespacesLoadedMsg:
		items := make([]list.Item, 0, len(msg.names))
		for _, n := range msg.names {
			items = append(items, simpleItem{title: n})
		}
		m.list.SetItems(items)
		m.loading = false
		return m, nil

	case loadErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, func() tea.Msg { return backMsg{} }
		case "enter":
			if it, ok := m.list.SelectedItem().(simpleItem); ok && it.title != "" {
				selected := it.title
				return m, func() tea.Msg { return namespaceSelectedMsg(selected) }
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
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
		"↑/↓ move · / filter · enter select · q quit",
	))
	return b.String()
}
