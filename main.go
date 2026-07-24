package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

func runHeadless(alias string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %v", err)
	}

	var target *ScriptConfig
	for _, s := range cfg.Scripts {
		if s.NameAlias == alias {
			sc := s
			target = &sc
			break
		}
	}
	if target == nil {
		return fmt.Errorf("script alias %q not found", alias)
	}

	cmd, taskID, err := StartTask(target.NameAlias, target.Command, target.OutputFolderPath, target.Input)
	if err != nil {
		return fmt.Errorf("failed to start task: %v", err)
	}

	taskFilePath := filepath.Join(target.OutputFolderPath, fmt.Sprintf("task_%d.yaml", taskID))

	success := false
	var lastState string
	printedOffset := 0

	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		data, err := os.ReadFile(taskFilePath)
		if err != nil {
			continue
		}

		var current TaskYAML
		err = yaml.Unmarshal(data, &current)
		if err != nil {
			continue
		}

		lastState = current.Task.State

		logsVal := current.Task.Logs.Value
		if len(logsVal) > printedOffset {
			fmt.Print(logsVal[printedOffset:])
			printedOffset = len(logsVal)
		}

		if lastState == "Success" {
			success = true
			break
		}
		if lastState == "Failed" || lastState == "Stopped" {
			break
		}
	}
	if time.Now().After(deadline) {
		return fmt.Errorf("timed out waiting for task '%s' to complete", alias)
	}

	_ = cmd

	if !success {
		return fmt.Errorf("task finished with state: %s", lastState)
	}
	return nil
}

func printHelp() {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff007f")).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffd7")).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ae81ff")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))
	boldStyle := lipgloss.NewStyle().Bold(true)

	versionBadge := lipgloss.NewStyle().
		Background(lipgloss.Color("#00ffd7")).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 1).
		Render(Version)

	authorLabel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#75715e")).
		Render("by github/codewithyagnesh")

	fmt.Println()
	fmt.Print(lipgloss.NewStyle().Foreground(lipgloss.Color("#6366f1")).Bold(true).Render("  SCTL — Script Controller") + "\n")
	fmt.Println()
	fmt.Printf("  %s  %s  %s\n", titleStyle.Render("sctl (Script Controller)"), versionBadge, authorLabel)
	fmt.Printf("  %s\n", descStyle.Render("Modern, elegant task automation dashboard & scheduler"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" USAGE:"))
	fmt.Println("   sctl [flags]")
	fmt.Println()

	fmt.Println(headerStyle.Render(" FLAGS:"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--run, -run <alias>"), descStyle.Render("Execute the specified script in headless mode with real-time logging"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--version, -v"), descStyle.Render("Print version and author information"))
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--help, -h"), descStyle.Render("Show this help message and exit"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" TUI KEYBOARD SHORTCUTS:"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("k / ↑"), descStyle.Render("Move selection up"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("j / ↓"), descStyle.Render("Move selection down"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Tab"), descStyle.Render("Switch focus between Left (Scripts) and Right (Logs) panels"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Space"), descStyle.Render("Select / check script for multi-selection execution"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("r"), descStyle.Render("Run the selected (or checked) script(s)"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("s"), descStyle.Render("Force stop the currently running process"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("a"), descStyle.Render("Create a new script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Enter"), descStyle.Render("Edit schedule and environment variables for the selected script"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("d / Delete"), descStyle.Render("Remove the selected script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("h / H"), descStyle.Render("View task execution history and load past logs"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("p"), descStyle.Render("Toggle parallel execution mode (concurrently or sequentially)"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("o"), descStyle.Render("Open the latest HTML report/output in system default browser"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("[ / ] or PgUp/PgDn"), descStyle.Render("Scroll logs viewport up / down"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("q / Ctrl+C"), descStyle.Render("Quit the application"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" ENVIRONMENT VARIABLES:"))
	fmt.Printf("   %-25s %s\n", boldStyle.Render("SCTL_CONFIG"), descStyle.Render("Custom path to config.yaml (defaults to current directory)"))
	fmt.Println()

	fmt.Println(headerStyle.Render(" REAL-TIME PROGRESS INTEGRATION:"))
	fmt.Println(descStyle.Render("   To update the TUI progress bar from your custom script, print a line prefixed with"))
	fmt.Println("   " + boldStyle.Render("__PROGRESS__:<integer_0_to_100>") + descStyle.Render(" to standard output."))
	fmt.Println()
}

func main() {
	if len(os.Args) != 1 {
		if len(os.Args) >= 3 && (os.Args[1] == "--run" || os.Args[1] == "-run") {
			alias := os.Args[2]
			if err := runHeadless(alias); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		if len(os.Args) >= 2 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
			fmt.Printf("sctl version: %s\n", Version)
			fmt.Printf("GitHub: github/codewithyagnesh\n")
			os.Exit(0)
		}
		if len(os.Args) >= 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			printHelp()
			os.Exit(0)
		}
		printHelp()
		os.Exit(0)
	} else {
		p := tea.NewProgram(initialModel(), tea.WithAltScreen())
		program = p
		if _, err := p.Run(); err != nil {
			log.Fatalf("Error running program: %v", err)
		}
	}
}
