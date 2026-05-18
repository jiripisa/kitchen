package webtop

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

// screen identifies which sub-model currently owns the screen.
type screen int

const (
	screenMenu screen = iota
	screenList
	screenManifest
	screenDeploy
	screenUndeploy
)

// rootModel routes between every webtop sub-screen.
//
// The startScreen field lets each cobra subcommand land directly on the
// screen it wants (`kitchen webtop list` → list, `… deploy` → deploy, etc.)
// while keeping a single state machine for back-navigation. Whatever screen
// the user landed on first is what backMsg quits on; backMsg from any other
// screen returns to that starting screen (or to the menu when it's the
// start).
type rootModel struct {
	ctx    context.Context
	client *k8s.Client

	startScreen screen
	current     screen

	menu     *menuModel
	list     *listModel
	manifest *manifestModel
	deploy   *deployModel
	undeploy *undeployModel

	width, height int
}

func newRootModel(ctx context.Context, client *k8s.Client, start screen) rootModel {
	m := rootModel{
		ctx:         ctx,
		client:      client,
		startScreen: start,
		current:     start,
	}
	switch start {
	case screenMenu:
		m.menu = newMenuModel(client)
	case screenList:
		m.list = newListModel(client)
	case screenDeploy:
		m.deploy = newDeployModel(ctx, client)
	case screenUndeploy:
		m.undeploy = newUndeployModel(client)
	}
	return m
}

func (m rootModel) Init() tea.Cmd {
	switch m.current {
	case screenMenu:
		return m.menu.Init()
	case screenList:
		return m.list.Init()
	case screenDeploy:
		return m.deploy.Init()
	case screenUndeploy:
		return m.undeploy.Init()
	}
	return nil
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.menu != nil {
			m.menu.SetSize(msg.Width, msg.Height)
		}
		if m.list != nil {
			m.list.SetSize(msg.Width, msg.Height)
		}
		if m.manifest != nil {
			m.manifest.SetSize(msg.Width, msg.Height)
		}
		if m.deploy != nil {
			m.deploy.SetSize(msg.Width, msg.Height)
		}
		if m.undeploy != nil {
			m.undeploy.SetSize(msg.Width, msg.Height)
		}
		return m, nil

	case menuSelectedMsg:
		switch msg.choice {
		case menuList:
			m.list = newListModel(m.client)
			m.list.SetSize(m.width, m.height)
			m.current = screenList
			return m, m.list.Init()
		case menuDeploy:
			m.deploy = newDeployModel(m.ctx, m.client)
			m.deploy.SetSize(m.width, m.height)
			m.current = screenDeploy
			return m, m.deploy.Init()
		case menuUndeploy:
			m.undeploy = newUndeployModel(m.client)
			m.undeploy.SetSize(m.width, m.height)
			m.current = screenUndeploy
			return m, m.undeploy.Init()
		}
		return m, nil

	case entrySelectedMsg:
		m.manifest = newManifestModel(m.client, msg.namespace, msg.name)
		m.manifest.SetSize(m.width, m.height)
		m.current = screenManifest
		return m, m.manifest.Init()

	case backMsg:
		switch m.current {
		case screenManifest:
			// Manifest viewer is always reached from the list.
			m.current = screenList
			m.manifest = nil
			return m, nil
		case screenList, screenDeploy, screenUndeploy:
			// Top-level screens: bounce to menu if we came in via the menu,
			// otherwise quit (this is how `kitchen webtop list/deploy/undeploy`
			// keep their "esc quits" expectation).
			if m.startScreen == screenMenu {
				m.current = screenMenu
				m.list = nil
				m.deploy = nil
				m.undeploy = nil
				return m, nil
			}
			return m, tea.Quit
		}
		return m, tea.Quit
	}

	switch m.current {
	case screenMenu:
		mm, cmd := m.menu.Update(msg)
		m.menu = mm
		return m, cmd
	case screenList:
		lm, cmd := m.list.Update(msg)
		m.list = lm
		return m, cmd
	case screenManifest:
		mm, cmd := m.manifest.Update(msg)
		m.manifest = mm
		return m, cmd
	case screenDeploy:
		dm, cmd := m.deploy.Update(msg)
		m.deploy = dm
		return m, cmd
	case screenUndeploy:
		um, cmd := m.undeploy.Update(msg)
		m.undeploy = um
		return m, cmd
	}
	return m, nil
}

func (m rootModel) View() string {
	switch m.current {
	case screenMenu:
		return m.menu.View()
	case screenList:
		return m.list.View()
	case screenManifest:
		return m.manifest.View()
	case screenDeploy:
		return m.deploy.View()
	case screenUndeploy:
		return m.undeploy.View()
	}
	return ""
}

// Shared messages between sub-models and the root.

// entrySelectedMsg is fired by the list when the user picks a webtop entry
// to view its manifest.
type entrySelectedMsg struct {
	namespace, name string
}

// backMsg asks the root to navigate to the previous screen (or quit if it's
// the starting screen). Sub-models emit this on esc.
type backMsg struct{}
