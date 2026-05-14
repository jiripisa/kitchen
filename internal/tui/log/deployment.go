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

type deploymentModel struct {
	client    *k8s.Client
	namespace string

	width, height int

	list    list.Model
	spinner spinner.Model

	loading bool
	err     error
}

func newDeploymentModel(client *k8s.Client, namespace string) *deploymentModel {
	delegate := newPickerDelegate()
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Deployments in " + namespace
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Styles.Title = styles.Title

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &deploymentModel{
		client:    client,
		namespace: namespace,
		list:      l,
		spinner:   sp,
		loading:   true,
	}
}

func (m *deploymentModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m *deploymentModel) SetSize(w, h int) {
	m.width, m.height = w, h
	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(w, listH)
}

type deploymentsLoadedMsg struct {
	items []k8s.Deployment
}

func (m *deploymentModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		ds, err := m.client.ListDeployments(ctx, m.namespace)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return deploymentsLoadedMsg{items: ds}
	}
}

func (m *deploymentModel) Update(msg tea.Msg) (*deploymentModel, tea.Cmd) {
	switch msg := msg.(type) {
	case deploymentsLoadedMsg:
		items := make([]list.Item, 0, len(msg.items))
		for _, d := range msg.items {
			items = append(items, simpleItem{
				title: d.Name,
				desc:  fmt.Sprintf("%d/%d ready", d.Ready, d.Replicas),
			})
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
		case "esc":
			return m, func() tea.Msg { return backMsg{} }
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(simpleItem); ok && it.title != "" {
				ns := m.namespace
				name := it.title
				return m, func() tea.Msg {
					return deploymentSelectedMsg{namespace: ns, deployment: name}
				}
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

func (m *deploymentModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "deployment picker"))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		b.WriteString(fmt.Sprintf("  %s loading deployments in %q…", m.spinner.View(), m.namespace))
		b.WriteByte('\n')
	default:
		if len(m.list.Items()) == 0 {
			b.WriteString(styles.Hint.Render(fmt.Sprintf("  no deployments in %q.", m.namespace)))
			b.WriteByte('\n')
		} else {
			b.WriteString(m.list.View())
		}
	}

	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
			{Key: "ns", Value: m.namespace},
		},
		"↑/↓ move · / filter · enter select · esc back · q quit",
	))
	return b.String()
}
