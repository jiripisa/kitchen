package webtop

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jiripisa/kitchen/internal/k8s"
)

type screen int

const (
	screenList screen = iota
	screenManifest
)

// rootModel routes between the list and manifest screens and holds shared state.
type rootModel struct {
	ctx    context.Context
	client *k8s.Client

	current screen

	list     *listModel
	manifest *manifestModel

	width, height int
}

func newRootModel(ctx context.Context, client *k8s.Client) rootModel {
	return rootModel{
		ctx:     ctx,
		client:  client,
		current: screenList,
		list:    newListModel(client),
	}
}

func (m rootModel) Init() tea.Cmd {
	return m.list.Init()
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.list != nil {
			m.list.SetSize(msg.Width, msg.Height)
		}
		if m.manifest != nil {
			m.manifest.SetSize(msg.Width, msg.Height)
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
			m.current = screenList
			m.manifest = nil
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.current {
	case screenList:
		lm, cmd := m.list.Update(msg)
		m.list = lm
		return m, cmd
	case screenManifest:
		mm, cmd := m.manifest.Update(msg)
		m.manifest = mm
		return m, cmd
	}
	return m, nil
}

func (m rootModel) View() string {
	switch m.current {
	case screenList:
		return m.list.View()
	case screenManifest:
		return m.manifest.View()
	}
	return ""
}

// entrySelectedMsg is fired when the user picks a webtop deployment from the
// list; the root model uses it to construct the manifest screen.
type entrySelectedMsg struct {
	namespace, name string
}

// backMsg asks the root to navigate to the previous screen (or quit when at
// the list screen).
type backMsg struct{}
