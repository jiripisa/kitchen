package webtop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jiripisa/kitchen/internal/github"
	"github.com/jiripisa/kitchen/internal/k8s"
	"github.com/jiripisa/kitchen/internal/tui/components"
	"github.com/jiripisa/kitchen/internal/tui/styles"
)

// deployStep is the wizard's current page.
type deployStep int

const (
	stepBranch   deployStep = iota // pick a branch / image tag
	stepCoreo                      // pick a coreo backend
	stepName                       // name the new deployment (suffix)
	stepConfirm                    // review summary + confirm
	stepApplying                   // spinner while creating resources
	stepDone                       // success / error result
)

// --- choices the wizard collects -------------------------------------------

type branchOption struct {
	// Label is what we show in the picker — branch name for PRs, "main"
	// for the canonical build.
	Label string
	// Tag is the ghcr.io tag for the rendered image (EFFECTIVE_SLUG of the
	// label).
	Tag string
	// HeadRef is the exact ref (with slashes etc.) used in annotations.
	HeadRef string
	PR      *github.PR
	Mine    bool // author matches current gh user
	IsMain  bool
}

type coreoOption struct {
	Namespace string
	Name      string
	URL       string
	Tag       string
	LastLog   time.Time
	IsMain    bool // the canonical no-suffix `mafin-coreo` staging
}

// --- list-item adapters ----------------------------------------------------

type branchItem struct{ b branchOption }

func (i branchItem) FilterValue() string {
	return i.b.Label + " " + i.b.Tag
}

type coreoItem struct{ c coreoOption }

func (i coreoItem) FilterValue() string {
	return i.c.Name + " " + i.c.URL
}

// --- model ----------------------------------------------------------------

type deployModel struct {
	ctx    context.Context
	client *k8s.Client

	width, height int

	step deployStep

	// Step 1 — branches
	branchList    list.Model
	branchLoading bool
	branchErr     error

	// Step 2 — coreos
	coreoList    list.Model
	coreoLoading bool
	coreoErr     error

	// Step 3 — name
	nameInput textinput.Model
	nameErr   string

	// Step 4 — template fetched lazily from upstream `main`
	template      []byte
	templateErr   error
	templateReady bool

	// Step 5 — apply
	applySpinner spinner.Model
	applied      string // deployed name on success
	applyErr     error

	// Spinner shared across steps where we wait on something.
	spinner spinner.Model

	// Cached identity for branch sorting + annotation.
	currentUser string

	// Locked-in choices.
	chosenBranch *branchOption
	chosenCoreo  *coreoOption
	chosenName   string
}

func newDeployModel(ctx context.Context, client *k8s.Client) *deployModel {
	bl := list.New(nil, branchDelegate{}, 0, 0)
	bl.Title = "Pick a webtop build (branch)"
	bl.SetShowHelp(false)
	bl.SetShowStatusBar(false)
	bl.SetFilteringEnabled(true)
	bl.Filter = substringFilter
	bl.Styles.Title = styles.Title

	cl := list.New(nil, coreoDelegate{}, 0, 0)
	cl.Title = "Pick a coreo backend"
	cl.SetShowHelp(false)
	cl.SetShowStatusBar(false)
	cl.SetFilteringEnabled(true)
	cl.Filter = substringFilter
	cl.Styles.Title = styles.Title

	ni := textinput.New()
	ni.Placeholder = "deployment suffix (e.g. feat-foo)"
	ni.Prompt = "name › "
	ni.CharLimit = 45
	ni.Width = 40

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return &deployModel{
		ctx:           ctx,
		client:        client,
		step:          stepBranch,
		branchList:    bl,
		coreoList:     cl,
		nameInput:     ni,
		spinner:       sp,
		applySpinner:  sp,
		branchLoading: true,
	}
}

func (m *deployModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadBranchesCmd())
}

func (m *deployModel) SetSize(w, h int) {
	m.width, m.height = w, h
	listH := h - 5
	if listH < 3 {
		listH = 3
	}
	m.branchList.SetSize(w, listH)
	m.coreoList.SetSize(w, listH)
	m.nameInput.Width = max(20, w/2)
}

// --- messages -------------------------------------------------------------

type branchesLoadedMsg struct {
	branches []branchOption
}

type branchesErrMsg struct{ err error }

type coreosLoadedMsg struct {
	coreos []coreoOption
}

type coreosErrMsg struct{ err error }

type templateLoadedMsg struct {
	body []byte
	err  error
}

type appliedMsg struct {
	name string
	err  error
}

// --- commands -------------------------------------------------------------

func (m *deployModel) loadBranchesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()

		user, _ := github.CurrentUser(ctx)
		m.currentUser = user

		prs, err := fetchOpenPRs(ctx, webtopRepoOwner, webtopRepoName)
		if err != nil {
			return branchesErrMsg{err: err}
		}

		out := []branchOption{
			{Label: "main", Tag: "main", HeadRef: "main", IsMain: true},
		}
		for _, p := range prs {
			b := branchOption{
				Label:   p.HeadRefName,
				Tag:     github.EffectiveSlug(p.HeadRefName),
				HeadRef: p.HeadRefName,
				PR: &github.PR{
					Number:  p.Number,
					URL:     p.URL,
					HeadRef: p.HeadRefName,
				},
				Mine: user != "" && strings.EqualFold(p.AuthorLogin, user),
			}
			out = append(out, b)
		}
		// main first, mine before others, otherwise by PR number desc.
		sort.SliceStable(out[1:], func(i, j int) bool {
			a, b := out[i+1], out[j+1]
			if a.Mine != b.Mine {
				return a.Mine
			}
			if a.PR != nil && b.PR != nil {
				return a.PR.Number > b.PR.Number
			}
			return a.Label < b.Label
		})
		return branchesLoadedMsg{branches: out}
	}
}

func (m *deployModel) loadCoreosCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()

		deps, err := m.client.ListAllDeployments(ctx)
		if err != nil {
			return coreosErrMsg{err: err}
		}
		ingresses, _ := m.client.ListAllIngresses(ctx)
		urls := buildIngressURLIndex(ingresses)
		logs := fetchLastLogTimes(ctx, m.client, deps)

		out := []coreoOption{}
		for _, d := range deps {
			if !isCoreoDeployment(d) {
				continue
			}
			key := d.Namespace + "/" + d.Name
			c := coreoOption{
				Namespace: d.Namespace,
				Name:      d.Name,
				URL:       urls[key],
				Tag:       coreoImageTag(d),
				LastLog:   logs[key],
				IsMain:    d.Name == "mafin-coreo",
			}
			out = append(out, c)
		}
		// Sort: main (no-suffix staging) first, then by last-log desc.
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if a.IsMain != b.IsMain {
				return a.IsMain
			}
			if a.LastLog.IsZero() != b.LastLog.IsZero() {
				return !a.LastLog.IsZero()
			}
			if !a.LastLog.Equal(b.LastLog) {
				return a.LastLog.After(b.LastLog)
			}
			return a.Name < b.Name
		})
		return coreosLoadedMsg{coreos: out}
	}
}

func loadTemplateCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		body, err := github.FetchRawFile(ctx, webtopRepoOwner, webtopRepoName, "main", "k8s.yml")
		return templateLoadedMsg{body: body, err: err}
	}
}

func (m *deployModel) applyCmd() tea.Cmd {
	spec := k8s.WebtopDeploySpec{
		Suffix:    m.chosenName,
		ImageTag:  m.chosenBranch.Tag,
		CoreoURL:  m.chosenCoreo.URL,
		Branch:    m.chosenBranch.HeadRef,
		CreatedBy: m.currentUser,
		Template:  m.template,
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultAPITimeout)
		defer cancel()
		name, err := client.DeployWebtop(ctx, spec)
		return appliedMsg{name: name, err: err}
	}
}

// --- Update ---------------------------------------------------------------

func (m *deployModel) Update(msg tea.Msg) (*deployModel, tea.Cmd) {
	switch msg := msg.(type) {
	case branchesLoadedMsg:
		m.branchLoading = false
		items := make([]list.Item, 0, len(msg.branches))
		for _, b := range msg.branches {
			items = append(items, branchItem{b: b})
		}
		return m, m.branchList.SetItems(items)

	case branchesErrMsg:
		m.branchLoading = false
		m.branchErr = msg.err
		return m, nil

	case coreosLoadedMsg:
		m.coreoLoading = false
		items := make([]list.Item, 0, len(msg.coreos))
		for _, c := range msg.coreos {
			items = append(items, coreoItem{c: c})
		}
		return m, m.coreoList.SetItems(items)

	case coreosErrMsg:
		m.coreoLoading = false
		m.coreoErr = msg.err
		return m, nil

	case templateLoadedMsg:
		m.template = msg.body
		m.templateErr = msg.err
		m.templateReady = true
		return m, nil

	case appliedMsg:
		m.step = stepDone
		m.applied = msg.name
		m.applyErr = msg.err
		return m, nil

	case spinner.TickMsg:
		var c1, c2 tea.Cmd
		m.spinner, c1 = m.spinner.Update(msg)
		m.applySpinner, c2 = m.applySpinner.Update(msg)
		return m, tea.Batch(c1, c2)
	}

	// Per-step handling.
	switch m.step {
	case stepBranch:
		return m.updateBranchStep(msg)
	case stepCoreo:
		return m.updateCoreoStep(msg)
	case stepName:
		return m.updateNameStep(msg)
	case stepConfirm:
		return m.updateConfirmStep(msg)
	case stepDone:
		return m.updateDoneStep(msg)
	}
	return m, nil
}

func (m *deployModel) updateBranchStep(msg tea.Msg) (*deployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if shouldStartFiltering(key, m.branchList.FilterState()) {
			return m, startFilteringWith(&m.branchList, key)
		}
		switch key.String() {
		case "esc":
			if m.branchList.FilterState() == list.Filtering || m.branchList.FilterState() == list.FilterApplied {
				break
			}
			return m, func() tea.Msg { return backMsg{} }
		case "ctrl+c", "q":
			if m.branchList.FilterState() == list.Unfiltered {
				return m, tea.Quit
			}
		case "enter":
			if it, ok := m.branchList.SelectedItem().(branchItem); ok {
				bv := it.b
				m.chosenBranch = &bv
				m.step = stepCoreo
				m.coreoLoading = true
				return m, tea.Batch(m.spinner.Tick, m.loadCoreosCmd())
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.branchList, cmd = m.branchList.Update(msg)
	syncListTitle(&m.branchList, "Pick a webtop build (branch)")
	return m, cmd
}

func (m *deployModel) updateCoreoStep(msg tea.Msg) (*deployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if shouldStartFiltering(key, m.coreoList.FilterState()) {
			return m, startFilteringWith(&m.coreoList, key)
		}
		switch key.String() {
		case "esc":
			if m.coreoList.FilterState() == list.Filtering || m.coreoList.FilterState() == list.FilterApplied {
				break
			}
			m.step = stepBranch
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if it, ok := m.coreoList.SelectedItem().(coreoItem); ok {
				cv := it.c
				if cv.URL == "" {
					// Can't deploy against a coreo with no ingress yet.
					return m, nil
				}
				m.chosenCoreo = &cv
				// Prefill the suggested name with the branch slug.
				suggested := m.chosenBranch.Tag
				m.nameInput.SetValue(suggested)
				m.nameInput.CursorEnd()
				m.nameErr = ""
				m.step = stepName
				return m, m.nameInput.Focus()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.coreoList, cmd = m.coreoList.Update(msg)
	syncListTitle(&m.coreoList, "Pick a coreo backend")
	return m, cmd
}

func (m *deployModel) updateNameStep(msg tea.Msg) (*deployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.step = stepCoreo
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			name := strings.TrimSpace(m.nameInput.Value())
			if err := validateSuffixUI(name); err != nil {
				m.nameErr = err.Error()
				return m, nil
			}
			// Collision check against the cluster.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			exists, err := m.client.WebtopNameExists(ctx, name)
			if err != nil {
				m.nameErr = fmt.Sprintf("could not check collisions: %v", err)
				return m, nil
			}
			if exists {
				m.nameErr = fmt.Sprintf("mafin-coreo-app-%s already exists — pick a different name", name)
				return m, nil
			}
			m.chosenName = name
			m.step = stepConfirm
			if !m.templateReady {
				return m, tea.Batch(m.spinner.Tick, loadTemplateCmd())
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	// Live-validate on each keystroke so the user sees the rule before
	// hitting Enter.
	if v := strings.TrimSpace(m.nameInput.Value()); v != "" {
		if err := validateSuffixUI(v); err != nil {
			m.nameErr = err.Error()
		} else {
			m.nameErr = ""
		}
	} else {
		m.nameErr = ""
	}
	return m, cmd
}

func (m *deployModel) updateConfirmStep(msg tea.Msg) (*deployModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.step = stepName
			return m, nil
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if !m.templateReady {
				// Template still loading; ignore for now.
				return m, nil
			}
			if m.templateErr != nil {
				return m, nil
			}
			m.step = stepApplying
			return m, tea.Batch(m.applySpinner.Tick, m.applyCmd())
		}
	}
	return m, nil
}

func (m *deployModel) updateDoneStep(msg tea.Msg) (*deployModel, tea.Cmd) {
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

// --- View -----------------------------------------------------------------

func (m *deployModel) View() string {
	switch m.step {
	case stepBranch:
		return m.viewBranchStep()
	case stepCoreo:
		return m.viewCoreoStep()
	case stepName:
		return m.viewNameStep()
	case stepConfirm:
		return m.viewConfirmStep()
	case stepApplying:
		return m.viewApplyingStep()
	case stepDone:
		return m.viewDoneStep()
	}
	return ""
}

func (m *deployModel) chrome(title, hint string) (header, footer string) {
	header = components.TitleBar(m.width, title) + "\n"
	footer = components.StatusBar(m.width,
		[]components.StatusItem{
			{Key: "context", Value: m.client.Context()},
			{Key: "step", Value: m.stepLabel()},
		},
		hint,
	)
	return
}

func (m *deployModel) stepLabel() string {
	switch m.step {
	case stepBranch:
		return "1/4 build"
	case stepCoreo:
		return "2/4 coreo"
	case stepName:
		return "3/4 name"
	case stepConfirm:
		return "4/4 confirm"
	case stepApplying:
		return "applying"
	case stepDone:
		return "done"
	}
	return ""
}

func (m *deployModel) viewBranchStep() string {
	header, footer := m.chrome("deploy · pick build",
		pickerHint(m.branchList.FilterState()))
	var b strings.Builder
	b.WriteString(header)
	switch {
	case m.branchErr != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.branchErr)))
		b.WriteByte('\n')
	case m.branchLoading:
		fmt.Fprintf(&b, "  %s loading branches & open PRs…\n", m.spinner.View())
	default:
		b.WriteString(m.branchList.View())
	}
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (m *deployModel) viewCoreoStep() string {
	header, footer := m.chrome("deploy · pick coreo",
		pickerHint(m.coreoList.FilterState()))
	var b strings.Builder
	b.WriteString(header)
	switch {
	case m.coreoErr != nil:
		b.WriteString(styles.Error.Render(fmt.Sprintf("error: %v", m.coreoErr)))
		b.WriteByte('\n')
	case m.coreoLoading:
		fmt.Fprintf(&b, "  %s loading running coreos…\n", m.spinner.View())
	default:
		if len(m.coreoList.Items()) == 0 {
			b.WriteString(styles.Hint.Render("  no coreo deployments in this context.\n"))
		} else {
			b.WriteString(m.coreoList.View())
		}
	}
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (m *deployModel) viewNameStep() string {
	header, footer := m.chrome("deploy · name", "type · enter confirm · esc back · ^c quit")
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString("  ")
	b.WriteString(styles.Hint.Render(
		"The name becomes the deployment SUFFIX. URL will be:"))
	b.WriteString("\n  ")
	b.WriteString(styles.StatusValue.Render(
		"https://webtop-" + m.nameInput.Value() + ".mafin.finforce.dev"))
	b.WriteString("\n\n  ")
	b.WriteString(m.nameInput.View())
	b.WriteString("\n\n  ")
	if m.nameErr != "" {
		b.WriteString(styles.Error.Render(m.nameErr))
	} else {
		b.WriteString(styles.Hint.Render("rule: [a-z0-9][a-z0-9-]{0,44}"))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (m *deployModel) viewConfirmStep() string {
	header, footer := m.chrome("deploy · confirm",
		"enter deploy · esc back · ^c quit")
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')

	row := func(k, v string) {
		fmt.Fprintf(&b, "  %s  %s\n",
			lipgloss.NewStyle().Foreground(styles.ColorAccent).Width(10).Render(k),
			styles.StatusValue.Render(v))
	}

	row("name", "mafin-coreo-app-"+m.chosenName)
	row("url", "https://webtop-"+m.chosenName+".mafin.finforce.dev")
	row("image", "ghcr.io/finforce/mafin-coreo-app:"+m.chosenBranch.Tag)
	row("branch", m.chosenBranch.HeadRef)
	row("coreo", m.chosenCoreo.URL)
	if m.currentUser != "" {
		row("by", m.currentUser)
	}
	b.WriteByte('\n')
	switch {
	case m.templateErr != nil:
		b.WriteString("  " + styles.Error.Render(fmt.Sprintf("could not fetch upstream k8s.yml: %v", m.templateErr)))
		b.WriteByte('\n')
	case !m.templateReady:
		fmt.Fprintf(&b, "  %s fetching upstream k8s.yml from main…\n", m.spinner.View())
	default:
		b.WriteString(styles.Hint.Render("  Press enter to apply, esc to go back."))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (m *deployModel) viewApplyingStep() string {
	header, footer := m.chrome("deploy · applying", "")
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  %s creating Deployment, Service and Ingress…\n", m.applySpinner.View())
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

func (m *deployModel) viewDoneStep() string {
	header, footer := m.chrome("deploy · done", "enter / esc back · ^c quit")
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	if m.applyErr != nil {
		fmt.Fprintf(&b, "  %s\n",
			styles.Error.Render(fmt.Sprintf("deploy failed: %v", m.applyErr)))
	} else {
		fmt.Fprintf(&b, "  %s\n",
			lipgloss.NewStyle().Foreground(styles.ColorOK).Bold(true).Render(
				"✓ deployed "+m.applied))
		fmt.Fprintf(&b, "  %s\n",
			styles.Hint.Render("https://webtop-"+m.chosenName+".mafin.finforce.dev"))
		fmt.Fprintf(&b, "  %s\n",
			styles.Hint.Render("(give the pod ~30s to come up before opening the URL)"))
	}
	b.WriteByte('\n')
	b.WriteString(footer)
	return b.String()
}

// --- delegates ------------------------------------------------------------

type branchDelegate struct{}

func (d branchDelegate) Height() int                         { return 2 }
func (d branchDelegate) Spacing() int                        { return 0 }
func (d branchDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

var (
	branchTitleStyle = lipgloss.NewStyle().Foreground(styles.ColorText)
	branchSelStyle   = lipgloss.NewStyle().Foreground(styles.ColorAccent).Bold(true)
	branchMineStyle  = lipgloss.NewStyle().Foreground(styles.ColorAccent2)
	branchMetaStyle  = lipgloss.NewStyle().Foreground(styles.ColorDim)
)

func (d branchDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(branchItem)
	if !ok {
		return
	}
	selected := index == m.Index()

	cursor := "  "
	titleStyle := branchTitleStyle
	if it.b.Mine {
		titleStyle = branchMineStyle
	}
	if selected {
		cursor = "▸ "
		titleStyle = branchSelStyle
	}

	title := it.b.Label
	if it.b.IsMain {
		title = "main"
	}
	var b strings.Builder
	b.WriteString(cursor)
	b.WriteString(titleStyle.Render(title))
	if it.b.Mine {
		b.WriteString(branchMetaStyle.Render("  · yours"))
	}
	b.WriteByte('\n')
	if it.b.PR != nil {
		fmt.Fprintf(&b, "    %s  %s",
			branchMetaStyle.Render(fmt.Sprintf("PR #%d", it.b.PR.Number)),
			branchMetaStyle.Render("tag "+it.b.Tag))
	} else if it.b.IsMain {
		fmt.Fprintf(&b, "    %s",
			branchMetaStyle.Render("canonical build — image tag `main`"))
	}
	fmt.Fprint(w, b.String())
}

type coreoDelegate struct{}

func (d coreoDelegate) Height() int                         { return 2 }
func (d coreoDelegate) Spacing() int                        { return 0 }
func (d coreoDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d coreoDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(coreoItem)
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

	title := it.c.Name
	if it.c.IsMain {
		title = it.c.Name + " · staging"
	}
	var b strings.Builder
	b.WriteString(cursor)
	b.WriteString(titleStyle.Render(title))
	b.WriteByte('\n')
	url := it.c.URL
	if url == "" {
		url = branchMetaStyle.Render("(no ingress yet)")
	} else {
		url = branchMetaStyle.Render(url)
	}
	fmt.Fprintf(&b, "    %s", url)
	if !it.c.LastLog.IsZero() {
		fmt.Fprintf(&b, "  %s", branchMetaStyle.Render("last log "+humanDuration(time.Since(it.c.LastLog))))
	}
	fmt.Fprint(w, b.String())
}

// --- helpers --------------------------------------------------------------

type rawPR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	URL         string `json:"url"`
	AuthorLogin string `json:"-"` // populated below
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
}

// fetchOpenPRs lists open PRs for a repo (number, head-ref, URL, author login).
// The github.FetchIndex helper doesn't expose author info, so we shell out
// to `gh` here directly.
func fetchOpenPRs(ctx context.Context, owner, repo string) ([]rawPR, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", owner+"/"+repo,
		"--state", "open",
		"--limit", "200",
		"--json", "number,headRefName,url,author",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list %s/%s: %w", owner, repo, err)
	}
	var rows []rawPR
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	for i := range rows {
		rows[i].AuthorLogin = rows[i].Author.Login
	}
	return rows, nil
}

// coreoImageTag returns the tag of the first coreo container in d, or "".
func coreoImageTag(d k8s.Deployment) string {
	for _, c := range d.Containers {
		if isCoreoImage(c.Image) {
			return imageTag(c.Image)
		}
	}
	return ""
}

// validateSuffixUI is a UI-friendly wrapper around the k8s package's
// validateSuffix — same regex, but returns concise messages.
func validateSuffixUI(s string) error {
	if s == "" {
		return fmt.Errorf("name is required")
	}
	if len(s) > 45 {
		return fmt.Errorf("max 45 chars (got %d)", len(s))
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9':
			// ok
		case r == '-':
			if i == 0 {
				return fmt.Errorf("must not start with a dash")
			}
		default:
			return fmt.Errorf("only [a-z0-9-] allowed (offending %q at %d)", string(r), i)
		}
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
