package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	cmd, taskID, err := StartTask(target.NameAlias, target.Command, target.OutputFolderPath, target.Input, target.Timeout)
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

func runHeadlessGroup(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %v", err)
	}

	var target *GroupConfig
	for _, g := range cfg.Groups {
		if g.Name == name {
			gc := g
			target = &gc
			break
		}
	}
	if target == nil {
		return fmt.Errorf("group %q not found", name)
	}

	// Build alias -> ScriptConfig map
	scriptMap := make(map[string]ScriptConfig)
	for _, s := range cfg.Scripts {
		scriptMap[s.NameAlias] = s
	}

	var scripts []ScriptConfig
	for _, alias := range target.Scripts {
		sc, ok := scriptMap[alias]
		if !ok {
			return fmt.Errorf("group %q references unknown script %q", name, alias)
		}
		scripts = append(scripts, sc)
	}

	if len(scripts) == 0 {
		return fmt.Errorf("group %q has no scripts", name)
	}

	mode := "parallel"
	if target.Pipeline {
		mode = "pipeline"
	}
	fmt.Printf("[sctl] Running group '%s' (%s, %d script(s))...\n", name, mode, len(scripts))

	type result struct {
		alias  string
		err    error
		state  string
		taskID int
	}

	if target.Pipeline {
		// Sequential execution
		for _, sc := range scripts {
			fmt.Printf("\n--- [%s] ---\n", sc.NameAlias)
			err := runHeadless(sc.NameAlias)
			if err != nil {
				return fmt.Errorf("group '%s' pipeline failed at script '%s': %v", name, sc.NameAlias, err)
			}
		}
		fmt.Printf("\n[sctl] Group '%s' completed successfully.\n", name)
		return nil
	}

	// Parallel execution
	results := make(chan result, len(scripts))
	for _, sc := range scripts {
		go func(s ScriptConfig) {
			err := runHeadless(s.NameAlias)
			var state string
			if err != nil {
				state = "Failed"
			} else {
				state = "Success"
			}
			results <- result{alias: s.NameAlias, err: err, state: state}
		}(sc)
	}

	allOK := true
	for i := 0; i < len(scripts); i++ {
		res := <-results
		if res.err != nil {
			allOK = false
			fmt.Fprintf(os.Stderr, "[sctl] Script '%s' finished with error: %v\n", res.alias, res.err)
		} else {
			fmt.Printf("[sctl] Script '%s' finished: %s\n", res.alias, res.state)
		}
	}

	if !allOK {
		return fmt.Errorf("group '%s' finished with errors", name)
	}
	fmt.Printf("\n[sctl] Group '%s' completed successfully.\n", name)
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
	fmt.Printf("   %-30s %s\n", keyStyle.Render("--run-group, -run-group <name>"), descStyle.Render("Execute all scripts in a group (pipeline or parallel) in headless mode"))
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
	fmt.Printf("   %-25s %s\n", keyStyle.Render("e"), descStyle.Render("Edit the selected script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("Enter"), descStyle.Render("Edit schedule and environment variables for the selected script"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("d / Delete"), descStyle.Render("Remove the selected script configuration"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("h / H"), descStyle.Render("View task execution history and load past logs"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("p"), descStyle.Render("Toggle parallel execution mode (concurrently or sequentially)"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("o"), descStyle.Render("Open the latest HTML report/output in system default browser"))
	fmt.Printf("   %-25s %s\n", keyStyle.Render("g"), descStyle.Render("Open the Groups management view"))
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

func handleCleanupCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: sctl cleanup [--all] [--alias <name>] [--older-than <days>] [--confirm]")
		fmt.Println("  --all          Delete task_*.yaml files for all scripts")
		fmt.Println("  --alias <name> Clean only specified script")
		fmt.Println("  --older-than N Delete files older than N days")
		os.Exit(1)
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	alias := ""
	var olderThan int = -1
	confirm := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			// all scripts will be processed by default
		case "--alias":
			if i+1 < len(args) {
				alias = args[i+1]
				i++
			}
		case "--older-than":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &olderThan)
				i++
			}
		case "--confirm":
			confirm = true
		}
	}
	if !confirm {
		fmt.Println("This will delete task files. Use --confirm to proceed.")
		os.Exit(1)
	}
	deleted := 0
	scripts := cfg.Scripts
	if alias != "" {
		var filtered []ScriptConfig
		for _, s := range cfg.Scripts {
			if s.NameAlias == alias {
				filtered = append(filtered, s)
			}
		}
		scripts = filtered
	}
	if len(scripts) == 0 {
		fmt.Println("No scripts matched")
		os.Exit(0)
	}
	cutoff := time.Time{}
	if olderThan >= 0 {
		cutoff = time.Now().AddDate(0, 0, -olderThan)
	}
	for _, s := range scripts {
		dir := s.OutputFolderPath
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "task_") || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if olderThan >= 0 && info.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(path); err == nil {
				deleted++
				fmt.Printf("Deleted %s\n", path)
			}
		}
	}
	fmt.Printf("Cleanup complete. Deleted %d task file(s).\n", deleted)
}

func handleConfigCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: sctl config <validate|list|edit|set|export|import> [...]")
		os.Exit(1)
	}
	cmd := args[0]
	switch cmd {
	case "validate":
		cfg, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		errs := ValidateConfig(cfg)
		if len(errs) == 0 {
			fmt.Println("Config validation PASSED")
			os.Exit(0)
		}
		fmt.Println("Config validation FAILED:")
		for _, e := range errs {
			fmt.Printf(" - %v\n", e)
		}
		os.Exit(1)
	case "list":
		cfg, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Scripts:")
		for _, s := range cfg.Scripts {
			fmt.Printf(" - %s: %s -> %s\n", s.NameAlias, s.Command, s.OutputFolderPath)
		}
	case "edit":
		if len(args) < 2 {
			fmt.Println("Usage: sctl config edit <alias>")
			os.Exit(1)
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		path := GetConfigPath()
		cmd := exec.Command(editor, path)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Editor error: %v\n", err)
			os.Exit(1)
		}
	case "set":
		if len(args) < 4 {
			fmt.Println("Usage: sctl config set <alias> <field> <value>")
			os.Exit(1)
		}
		alias := args[1]
		field := args[2]
		value := args[3]
		cfg, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
			os.Exit(1)
		}
		found := false
		for i, s := range cfg.Scripts {
			if s.NameAlias == alias {
				found = true
				switch field {
				case "description":
					cfg.Scripts[i].Description = value
				case "command":
					cfg.Scripts[i].Command = value
				case "output_folder_path":
					cfg.Scripts[i].OutputFolderPath = value
				case "cron":
					cfg.Scripts[i].Cron = value
				case "notify":
					cfg.Scripts[i].Notify = value == "true"
				default:
					fmt.Fprintf(os.Stderr, "Unknown field %s\n", field)
					os.Exit(1)
				}
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Script %s not found\n", alias)
			os.Exit(1)
		}
		if errs := ValidateConfig(cfg); len(errs) > 0 {
			fmt.Println("Validation failed after set:")
			for _, e := range errs {
				fmt.Printf(" - %v\n", e)
			}
			os.Exit(1)
		}
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Config updated")
	case "export":
		if len(args) < 2 {
			fmt.Println("Usage: sctl config export <file>")
			os.Exit(1)
		}
		if err := ExportConfig(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Export error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config exported to %s\n", args[1])
	case "import":
		if len(args) < 2 {
			fmt.Println("Usage: sctl config import <file>")
			os.Exit(1)
		}
		if err := ImportConfig(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Import error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config imported from %s\n", args[1])
	default:
		fmt.Printf("Unknown config command: %s\n", cmd)
		os.Exit(1)
	}
}


func main() {
	if len(os.Args) != 1 {
		// cleanup subcommand
		if len(os.Args) >= 2 && os.Args[1] == "cleanup" {
			handleCleanupCmd(os.Args[2:])
			os.Exit(0)
		}
		// config subcommands
		if len(os.Args) >= 2 && os.Args[1] == "config" {
			handleConfigCmd(os.Args[2:])
			os.Exit(0)
		}
		// --run <alias>
		if len(os.Args) >= 3 && (os.Args[1] == "--run" || os.Args[1] == "-run") {
			alias := os.Args[2]
			if err := runHeadless(alias); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		// --run-group <name>
		if len(os.Args) >= 3 && (os.Args[1] == "--run-group" || os.Args[1] == "-run-group") {
			name := os.Args[2]
			if err := runHeadlessGroup(name); err != nil {
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