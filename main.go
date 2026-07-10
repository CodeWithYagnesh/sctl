package main

import (
	"fmt"
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	// Define the styling for our container
	outerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")). // Purple outer border
			Padding(1)

	// Inner containers style
	innerLeftStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")). // Cyan border
			Padding(0, 1)

	innerRightStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("214")). // Orange border
			Padding(0, 1)
)

type model struct {
	count int
	width int
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.(tea.WindowSizeMsg).Width
	case tea.KeyMsg:
		key := msg.(tea.KeyMsg).String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if key == "q" {
			return m, tea.Quit
		}
		if key == "up" {
			m.count++
		}
		if key == "down" {
			m.count--
		}
		if key == "r" {
			m.count = 0
		}
	}
	return m, nil
}

func (m model) View() string {
	s := fmt.Sprintf("Count: %d", m.count)
	netWidth := m.width - 4
	halfWidth := netWidth / 2
	leftBox := innerLeftStyle.Width(halfWidth - 4).Render(s)
	rightBox := innerRightStyle.Width(halfWidth - 4).Render("ctrl+c - Quit • q - Quit • up/down - Increment/Decrement • r - Reset")

	innerRow := lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)

	return outerStyle.Width(netWidth).Render(innerRow)
}

func main() {
	p := tea.NewProgram(model{})
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
