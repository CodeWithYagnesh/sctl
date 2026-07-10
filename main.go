package main

import (
	"fmt"
	"log"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	count int
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
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
	return fmt.Sprintf("Count: %d\n\nctrl+c - Quit • q - Quit • up/down - Increment/Decrement • r - Reset", m.count)
}
func main() {
	p := tea.NewProgram(model{0})
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}

}
