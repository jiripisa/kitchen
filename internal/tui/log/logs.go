package log

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// logsModel is the third screen: it streams logs from every pod of the
// selected deployment and renders them in a scrolling viewport.
type logsModel struct {
	client     *k8s.Client
	namespace  string
	deployment string

	width, height int

	viewport viewport.Model
	spinner  spinner.Model

	loading bool
	follow  bool
	err     error

	stream  *k8s.LogStream
	pods    []string
	podIdx  map[string]int
	content strings.Builder
}

func newLogsModel(client *k8s.Client, namespace, deployment string) *logsModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	vp := viewport.New(0, 0)

	return &logsModel{
		client:     client,
		namespace:  namespace,
		deployment: deployment,
		viewport:   vp,
		spinner:    sp,
		loading:    true,
		follow:     true,
		podIdx:     map[string]int{},
	}
}

func (m *logsModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startStreamCmd())
}

func (m *logsModel) SetSize(w, h int) {
	m.width, m.height = w, h
	vpH := h - 3
	if vpH < 3 {
		vpH = 3
	}
	m.viewport.Width = w
	m.viewport.Height = vpH
	m.viewport.SetContent(m.content.String())
	if m.follow {
		m.viewport.GotoBottom()
	}
}

type streamStartedMsg struct {
	pods   []string
	stream *k8s.LogStream
}

type logLineMsg k8s.LogLine

type streamEndMsg struct{}

func (m *logsModel) startStreamCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()

		pods, err := m.client.ListPodsForDeployment(ctx, m.namespace, m.deployment)
		if err != nil {
			return loadErrMsg{err: err}
		}
		if len(pods) == 0 {
			return loadErrMsg{err: fmt.Errorf("no running pods for deployment %s/%s", m.namespace, m.deployment)}
		}

		stream, err := m.client.StreamDeploymentLogs(context.Background(), m.namespace, pods, 50)
		if err != nil {
			return loadErrMsg{err: err}
		}
		return streamStartedMsg{pods: pods, stream: stream}
	}
}

func waitForLogCmd(stream *k8s.LogStream) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-stream.Lines
		if !ok {
			return streamEndMsg{}
		}
		return logLineMsg(line)
	}
}

func (m *logsModel) Update(msg tea.Msg) (*logsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartedMsg:
		m.loading = false
		m.pods = msg.pods
		for i, p := range msg.pods {
			m.podIdx[p] = i
		}
		m.stream = msg.stream
		return m, waitForLogCmd(m.stream)

	case logLineMsg:
		m.appendLine(k8s.LogLine(msg))
		var cmd tea.Cmd
		if m.stream != nil {
			cmd = waitForLogCmd(m.stream)
		}
		return m, cmd

	case streamEndMsg:
		// Streams have all closed (pods restarted/deleted). Show a hint but
		// stay on screen so the user can read what's already there.
		m.content.WriteString(styles.Hint.Render("— stream closed —") + "\n")
		m.viewport.SetContent(m.content.String())
		if m.follow {
			m.viewport.GotoBottom()
		}
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
		case "q":
			m.Stop()
			return m, tea.Quit
		case "esc":
			m.Stop()
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c":
			m.Stop()
			return m, tea.Quit
		case "f":
			m.follow = !m.follow
			if m.follow {
				m.viewport.GotoBottom()
			}
			return m, nil
		case "g":
			m.viewport.GotoTop()
			m.follow = false
			return m, nil
		case "G":
			m.viewport.GotoBottom()
			m.follow = true
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *logsModel) appendLine(l k8s.LogLine) {
	idx := m.podIdx[l.Pod]
	prefix := lipgloss.NewStyle().
		Foreground(styles.PodColor(idx)).
		Bold(true).
		Render(shortenPod(l.Pod, m.deployment))
	m.content.WriteString(prefix)
	m.content.WriteString(" │ ")
	m.content.WriteString(l.Line)
	m.content.WriteByte('\n')
	m.viewport.SetContent(m.content.String())
	if m.follow {
		m.viewport.GotoBottom()
	}
}

// shortenPod trims the deployment-name prefix from a pod name to keep the log
// line readable. "api-7c9d8b6c5d-2f9zr" → "7c9d8b6c5d-2f9zr".
func shortenPod(pod, deployment string) string {
	if rest, ok := strings.CutPrefix(pod, deployment+"-"); ok {
		return rest
	}
	return pod
}

// Stop tears down the underlying log stream.
func (m *logsModel) Stop() {
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
}

func (m *logsModel) View() string {
	var b strings.Builder
	b.WriteString(components.TitleBar(m.width, "logs · "+m.deployment))
	b.WriteByte('\n')

	switch {
	case m.err != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.err)))
		b.WriteByte('\n')
	case m.loading:
		fmt.Fprintf(&b, "  %s opening log streams…", m.spinner.View())
		b.WriteByte('\n')
	default:
		b.WriteString(m.viewport.View())
	}

	b.WriteByte('\n')
	followLabel := "follow on"
	if !m.follow {
		followLabel = "follow off"
	}
	b.WriteString(components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
			{Key: "ns", Value: m.namespace},
			{Key: "dep", Value: m.deployment},
			{Key: "pods", Value: fmt.Sprintf("%d", len(m.pods))},
			{Key: "mode", Value: followLabel},
		},
		"f follow · g/G top/bottom · esc back · q quit",
	))
	return b.String()
}
