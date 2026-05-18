package webtop

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// undeployStep is the current sub-screen.
type undeployStep int

const (
	undeployList    undeployStep = iota // pick a deployment
	undeployConfirm                     // confirm the delete
	undeployRunning                     // spinner while deleting
	undeployDone                        // result
)

type undeployItem struct{ w k8s.KitchenWebtop }

func (i undeployItem) FilterValue() string {
	return i.w.Name + " " + i.w.Branch + " " + i.w.CoreoURL + " " + i.w.CreatedBy
}

type undeployModel struct {
	client *k8s.Client

	width, height int

	step undeployStep

	list    list.Model
	spinner spinner.Model
	loading bool
	err     error

	// Locked-in target.
	target *k8s.KitchenWebtop

	deleted   bool
	deleteErr error
}

func newUndeployModel(client *k8s.Client) *undeployModel {
	l := list.New(nil, undeployDelegate{}, 0, 0)
	l.Title = "Pick a kitchen-managed webtop to undeploy"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.Filter = substringFilter
	l.Styles.Title = styles.Title

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &undeployModel{
		client:  client,
		list:    l,
		spinner: sp,
		loading: true,
	}
}

func (m *undeployModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadCmd())
}

func (m *undeployModel) SetSize(w, h int) {
	m.width, m.height = w, h
	listH := h - 3
	if listH < 3 {
		listH = 3
	}
	m.list.SetSize(w, listH)
}

type kitchenWebtopsLoadedMsg struct {
	items []k8s.KitchenWebtop
}

type kitchenWebtopsErrMsg struct{ err error }

type undeployDoneMsg struct{ err error }

func (m *undeployModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		items, err := m.client.ListKitchenWebtops(ctx)
		if err != nil {
			return kitchenWebtopsErrMsg{err: err}
		}
		return kitchenWebtopsLoadedMsg{items: items}
	}
}

func (m *undeployModel) deleteCmd() tea.Cmd {
	target := *m.target
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		err := client.DeleteWebtop(ctx, target.Namespace, target.Name)
		return undeployDoneMsg{err: err}
	}
}

func (m *undeployModel) Update(msg tea.Msg) (*undeployModel, tea.Cmd) {
	switch msg := msg.(type) {
	case kitchenWebtopsLoadedMsg:
		m.loading = false
		items := make([]list.Item, 0, len(msg.items))
		for _, w := range msg.items {
			items = append(items, undeployItem{w: w})
		}
		return m, m.list.SetItems(items)

	case kitchenWebtopsErrMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case undeployDoneMsg:
		m.step = undeployDone
		m.deleted = msg.err == nil
		m.deleteErr = msg.err
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	switch m.step {
	case undeployList:
		return m.updateListStep(msg)
	case undeployConfirm:
		return m.updateConfirmStep(msg)
	case undeployDone:
		return m.updateDoneStep(msg)
	}
	return m, nil
}

func (m *undeployModel) updateListStep(msg tea.Msg) (*undeployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if shouldStartFiltering(key, m.list.FilterState()) {
			return m, startFilteringWith(&m.list, key)
		}
		switch key.String() {
		case "esc":
			if m.list.FilterState() == list.Filtering || m.list.FilterState() == list.FilterApplied {
				break
			}
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c", "q":
			if m.list.FilterState() == list.Unfiltered {
				return m, tea.Quit
			}
		case "enter":
			if it, ok := m.list.SelectedItem().(undeployItem); ok {
				w := it.w
				m.target = &w
				m.step = undeployConfirm
				return m, nil
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	syncListTitle(&m.list, "Pick a kitchen-managed webtop to undeploy")
	return m, cmd
}

func (m *undeployModel) updateConfirmStep(msg tea.Msg) (*undeployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "n":
			m.step = undeployList
			m.target = nil
			return m, nil
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter", "y":
			m.step = undeployRunning
			return m, tea.Batch(m.spinner.Tick, m.deleteCmd())
		}
	}
	return m, nil
}

func (m *undeployModel) updateDoneStep(msg tea.Msg) (*undeployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter", "esc", "q":
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *undeployModel) View() string {
	switch m.step {
	case undeployList:
		return m.viewList()
	case undeployConfirm:
		return m.viewConfirm()
	case undeployRunning:
		return m.viewRunning()
	case undeployDone:
		return m.viewDone()
	}
	return ""
}

func (m *undeployModel) viewList() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "undeploy · pick"))
	b.WriteByte('\n')
	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s finding kitchen-managed webtops…\n", m.spinner.View())
	default:
		if len(m.list.Items()) == 0 {
			b.WriteString(styles.Hint.Render(
				"  no kitchen-managed webtops in this context. " +
					"only deployments created via `kitchen webtop deploy` are listed."))
			b.WriteByte('\n')
		} else {
			b.WriteString(m.list.View())
		}
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

func (m *undeployModel) viewConfirm() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "undeploy · confirm"))
	b.WriteByte('\n')
	b.WriteByte('\n')
	w := m.target
	row := func(k, v string) {
		fmt.Fprintf(&b, "  %s  %s\n",
			lipgloss.NewStyle().Foreground(styles.ColorAccent).Width(10).Render(k),
			styles.StatusValue.Render(v))
	}
	row("name", w.Namespace+"/"+w.Name)
	row("branch", coalesce(w.Branch, "—"))
	row("coreo", coalesce(w.CoreoURL, "—"))
	row("by", coalesce(w.CreatedBy, "—"))
	if !w.CreatedAt.IsZero() {
		row("age", humanDuration(time.Since(w.CreatedAt))+" ago")
	}
	b.WriteByte('\n')
	b.WriteString("  " + styles.Error.Render("This will delete the Deployment, Service and Ingress.") + "\n")
	b.WriteString("  " + styles.Hint.Render("Press y/enter to confirm, n/esc to cancel.") + "\n")
	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		"y/enter confirm · n/esc cancel · ^c quit",
	))
	return b.String()
}

func (m *undeployModel) viewRunning() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "undeploy · running"))
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  %s deleting Deployment, Service and Ingress…\n", m.spinner.View())
	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		"",
	))
	return b.String()
}

func (m *undeployModel) viewDone() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "undeploy · done"))
	b.WriteByte('\n')
	if m.deleteErr != nil {
		fmt.Fprintf(&b, "  %s\n", styles.Error.Render(fmt.Sprintf("undeploy failed: %v", m.deleteErr)))
	} else {
		fmt.Fprintf(&b, "  %s\n",
			lipgloss.NewStyle().Foreground(styles.ColorOK).Bold(true).Render("✓ removed "+m.target.Name))
	}
	b.WriteByte('\n')
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
		},
		"enter / esc back · ^c quit",
	))
	return b.String()
}

// --- delegate -------------------------------------------------------------

type undeployDelegate struct{}

func (d undeployDelegate) Height() int                         { return 2 }
func (d undeployDelegate) Spacing() int                        { return 0 }
func (d undeployDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d undeployDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(undeployItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	cursor := "  "
	titleStyle := branchTitleStyle
	if selected {
		cursor = "▸ "
		titleStyle = branchSelStyle
	}

	var b strings.Builder
	b.WriteString(cursor)
	b.WriteString(titleStyle.Render(it.w.Name))
	b.WriteByte('\n')

	meta := []string{}
	if it.w.Branch != "" {
		meta = append(meta, "branch "+it.w.Branch)
	}
	if it.w.CoreoURL != "" {
		meta = append(meta, "→ "+it.w.CoreoURL)
	}
	if it.w.CreatedBy != "" {
		meta = append(meta, "by "+it.w.CreatedBy)
	}
	if !it.w.CreatedAt.IsZero() {
		meta = append(meta, humanDuration(time.Since(it.w.CreatedAt))+" ago")
	}
	fmt.Fprintf(&b, "    %s", branchMetaStyle.Render(strings.Join(meta, "  ")))
	fmt.Fprint(w, b.String())
}

func coalesce(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
