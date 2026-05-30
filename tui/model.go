package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	upStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	downStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

type tab int

const (
	tabWatchlist tab = iota
	tabPositions
	tabOrder
	tabNews
)

type Model struct {
	activeTab tab
	symbols   []string
	cursor    int
	status    string
	width     int
	height    int
}

func New() Model {
	return Model{
		symbols: []string{"AAPL", "MSFT", "NVDA", "SPY"},
		status:  "q quit · tab switch · ↑↓ navigate",
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % 4
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.symbols)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	header := titleStyle.Render("TRAIO") + " " + dimStyle.Render(m.tabName())
	body := m.renderBody()
	footer := dimStyle.Render(m.status)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) tabName() string {
	switch m.activeTab {
	case tabWatchlist:
		return "自选"
	case tabPositions:
		return "持仓"
	case tabOrder:
		return "下单"
	case tabNews:
		return "资讯"
	default:
		return ""
	}
}

func (m Model) renderBody() string {
	switch m.activeTab {
	case tabWatchlist:
		return m.renderWatchlist()
	case tabPositions:
		return dimStyle.Render("持仓 — 连接后端 /api/v1/positions（待实现）")
	case tabOrder:
		return dimStyle.Render("快速下单 — 待实现")
	case tabNews:
		return dimStyle.Render("资讯摘要 — 待实现")
	default:
		return ""
	}
}

func (m Model) renderWatchlist() string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%-8s %12s %10s", "SYMBOL", "LAST", "CHG%"))
	for i, sym := range m.symbols {
		chg := "+1.24%"
		line := fmt.Sprintf("%-8s %12s %10s", sym, "—", upStyle.Render(chg))
		if i == m.cursor {
			line = "> " + line
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
