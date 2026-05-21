// Package tui renders the three interactive pickers (account, role, cluster)
// on top of Bubble Tea's `list` component. Filtering with `/` is built in.
package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dutraph/awsso-tui/internal/ssoauth"
)

var (
	titleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Bold(true).Padding(0, 0, 1, 0)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true)
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C7C7C"))
)

// row is the common shape of every list item across the three pickers.
type row struct {
	primary   string // shown on the left
	secondary string // smaller subtitle (e.g. account id, email)
	value     string // returned to the caller on selection
}

func (r row) FilterValue() string { return r.primary + " " + r.secondary }

// delegate renders one row.
type delegate struct{}

func (d delegate) Height() int                             { return 1 }
func (d delegate) Spacing() int                            { return 0 }
func (d delegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d delegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	r, ok := item.(row)
	if !ok {
		return
	}
	cursor := "  "
	line := r.primary
	if r.secondary != "" {
		line = r.primary + "  " + subtleStyle.Render(r.secondary)
	}
	if index == m.Index() {
		cursor = "> "
		line = selectedStyle.Render(line)
	}
	fmt.Fprint(w, cursor+line)
}

// model is the shared Bubble Tea model for every picker.
type model struct {
	list   list.Model
	chosen *row
	quit   bool
}

func newModel(title string, items []list.Item) model {
	l := list.New(items, delegate{}, 0, 0)
	l.Title = title
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.Styles.Title = titleStyle
	l.KeyMap.Quit = key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit"))
	return model{list: l}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Leave a couple lines for title and help text.
		m.list.SetSize(msg.Width, msg.Height-2)
	case tea.KeyMsg:
		// Don't intercept Enter/quit while the filter input is focused —
		// the list component is already handling those keys internally.
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "enter":
			if it, ok := m.list.SelectedItem().(row); ok {
				m.chosen = &it
				return m, tea.Quit
			}
		case "q", "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return m.list.View()
}

func run(title string, items []list.Item) (*row, bool, error) {
	m := newModel(title, items)
	p := tea.NewProgram(m, tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return nil, false, err
	}
	final, ok := out.(model)
	if !ok || final.quit || final.chosen == nil {
		return nil, false, nil
	}
	return final.chosen, true, nil
}

// PickAccount shows the account picker. Returns (account, ok=false) when
// the user quit without choosing.
func PickAccount(accounts []ssoauth.Account) (ssoauth.Account, bool, error) {
	items := make([]list.Item, 0, len(accounts))
	for _, a := range accounts {
		secondary := a.ID
		if a.Email != "" {
			secondary = a.ID + "  " + a.Email
		}
		items = append(items, row{primary: a.Name, secondary: secondary, value: a.ID})
	}
	r, ok, err := run("Select an AWS account", items)
	if err != nil || !ok {
		return ssoauth.Account{}, false, err
	}
	// Re-resolve to the source Account (so we keep email etc).
	for _, a := range accounts {
		if a.ID == r.value {
			return a, true, nil
		}
	}
	return ssoauth.Account{}, false, nil
}

// PickRole shows the role picker for the chosen account.
func PickRole(account ssoauth.Account, roles []string) (string, bool, error) {
	items := make([]list.Item, 0, len(roles))
	for _, role := range roles {
		items = append(items, row{primary: role, secondary: "", value: role})
	}
	title := fmt.Sprintf("Select a role in %s (%s)", account.Name, account.ID)
	r, ok, err := run(title, items)
	if err != nil || !ok {
		return "", false, err
	}
	return r.value, true, nil
}

// PickCluster shows the EKS cluster picker for the chosen profile/region.
func PickCluster(profile, region string, clusters []string) (string, bool, error) {
	items := make([]list.Item, 0, len(clusters))
	for _, c := range clusters {
		items = append(items, row{primary: c, secondary: "", value: c})
	}
	title := fmt.Sprintf("Select an EKS cluster (profile %s, region %s)", profile, region)
	r, ok, err := run(title, items)
	if err != nil || !ok {
		return "", false, err
	}
	return r.value, true, nil
}
