package coreo

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// manifestModel renders the YAML manifest of one coreo deployment in a
// scrolling viewport. Visually and behaviourally identical to the webtop
// manifest viewer — duplicated rather than abstracted so the two TUIs can
// drift independently as needed.
type manifestModel struct {
	client     *k8s.Client
	namespace  string
	deployment string

	width, height int

	viewport viewport.Model
	spinner  spinner.Model

	loading bool
	err     error
}

func newManifestModel(client *k8s.Client, namespace, deployment string) *manifestModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	vp := viewport.New(0, 0)

	return &manifestModel{
		client:     client,
		namespace:  namespace,
		deployment: deployment,
		viewport:   vp,
		spinner:    sp,
		loading:    true,
	}
}

func (m *manifestModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m *manifestModel) SetSize(w, h int) {
	m.width, m.height = w, h
	vpH := h - 3
	if vpH < 3 {
		vpH = 3
	}
	m.viewport.Width = w
	m.viewport.Height = vpH
}

type manifestLoadedMsg struct{ yaml string }

func (m *manifestModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		y, err := m.client.GetDeploymentYAML(ctx, m.namespace, m.deployment)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return manifestLoadedMsg{yaml: y}
	}
}

func (m *manifestModel) Update(msg tea.Msg) (*manifestModel, tea.Cmd) {
	switch msg := msg.(type) {
	case manifestLoadedMsg:
		m.loading = false
		m.viewport.SetContent(msg.yaml)
		m.viewport.GotoTop()
		return m, nil

	case loadErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			return m, func() tea.Msg { return backMsg{} }
		case "g":
			m.viewport.GotoTop()
			return m, nil
		case "G":
			m.viewport.GotoBottom()
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *manifestModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "manifest · "+m.deployment))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s fetching manifest…", m.spinner.View())
		b.WriteByte('\n')
	default:
		b.WriteString(m.viewport.View())
	}

	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
			{Key: "ns", Value: m.namespace},
			{Key: "dep", Value: m.deployment},
		},
		"j/k ↑/↓ scroll · g/G top/bottom · esc back · q quit",
	))
	return b.String()
}
