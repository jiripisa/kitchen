package log

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// screen identifies which sub-model owns the screen.
type screen int

const (
	screenNamespace screen = iota
	screenDeployment
	screenLogs
)

// rootModel routes between the three sub-screens and holds shared state.
type rootModel struct {
	ctx    context.Context
	client *k8s.Client

	current screen

	namespace  *namespaceModel
	deployment *deploymentModel
	logs       *logsModel

	width, height int
}

func newRootModel(ctx context.Context, client *k8s.Client) rootModel {
	return rootModel{
		ctx:       ctx,
		client:    client,
		current:   screenNamespace,
		namespace: newNamespaceModel(client),
	}
}

func (m rootModel) Init() tea.Cmd {
	return m.namespace.Init()
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.namespace != nil {
			m.namespace.SetSize(msg.Width, msg.Height)
		}
		if m.deployment != nil {
			m.deployment.SetSize(msg.Width, msg.Height)
		}
		if m.logs != nil {
			m.logs.SetSize(msg.Width, msg.Height)
		}
		return m, nil

	case namespaceSelectedMsg:
		m.deployment = newDeploymentModel(m.client, string(msg))
		m.deployment.SetSize(m.width, m.height)
		m.current = screenDeployment
		return m, m.deployment.Init()

	case deploymentSelectedMsg:
		m.logs = newLogsModel(m.client, msg.namespace, msg.deployment)
		m.logs.SetSize(m.width, m.height)
		m.current = screenLogs
		return m, m.logs.Init()

	case backMsg:
		switch m.current {
		case screenDeployment:
			m.current = screenNamespace
			m.deployment = nil
			return m, nil
		case screenLogs:
			if m.logs != nil {
				m.logs.Stop()
			}
			m.current = screenDeployment
			m.logs = nil
			return m, nil
		}
		return m, tea.Quit
	}

	// Route everything else to the active screen.
	switch m.current {
	case screenNamespace:
		nm, cmd := m.namespace.Update(msg)
		m.namespace = nm
		return m, cmd
	case screenDeployment:
		dm, cmd := m.deployment.Update(msg)
		m.deployment = dm
		return m, cmd
	case screenLogs:
		lm, cmd := m.logs.Update(msg)
		m.logs = lm
		return m, cmd
	}
	return m, nil
}

func (m rootModel) View() string {
	switch m.current {
	case screenNamespace:
		return m.namespace.View()
	case screenDeployment:
		return m.deployment.View()
	case screenLogs:
		return m.logs.View()
	}
	return ""
}

// Shared messages between sub-models and the root.

type namespaceSelectedMsg string

type deploymentSelectedMsg struct {
	namespace, deployment string
}

// backMsg asks the root to navigate to the previous screen (or quit if at the
// start).
type backMsg struct{}
