package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// uiTestModel builds a model backed by a throw-away config with a few scripts
// and groups, sized w×h.
func uiTestModel(t *testing.T, w, h int) *model {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`scripts:
  - name_alias: backup_db
    description: Nightly database backup with a rather long description that must be truncated
    command: ./scripts/backup.sh --full
    output_folder_path: %[1]s/backup
    cron: "0 2 * * *"
    input:
      DB_NAME: production
      DB_PASSWORD: hunter2
  - name_alias: deploy
    description: Deploy to production over SSH
    command: ./deploy.sh
    output_folder_path: %[1]s/deploy
    host: ops@10.0.0.5
    timeout: 10m
  - name_alias: lint
    command: golangci-lint run ./...
    output_folder_path: %[1]s/lint
  - name_alias: 日本語のスクリプト
    description: wide characters must not break the layout ✓✓✓
    command: echo "こんにちは"
    output_folder_path: %[1]s/jp
groups:
  - name: release
    description: Full release pipeline
    pipeline: true
    scripts: [lint, backup_db, deploy, ghost]
  - name: checks
    pipeline: false
    scripts: [lint, deploy]
`, dir)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCTL_CONFIG", path)
	m := initialModel()
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

// assertFrame checks the geometry invariant: the frame is exactly w×h cells.
func assertFrame(t *testing.T, m *model, label string) string {
	t.Helper()
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) != m.height {
		t.Fatalf("%s: %d lines, want %d", label, len(lines), m.height)
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != m.width {
			t.Fatalf("%s: line %d is %d cells wide, want %d:\n%q", label, i, got, m.width, stripANSI(l))
		}
	}
	return v
}

func feedLogs(m *model, alias string, lines ...string) {
	for i, l := range lines {
		m.Update(TaskUpdateMsg{ScriptNameAlias: alias, State: "Running", Progress: i * 100 / max(1, len(lines)), LogLine: l})
	}
	m.flushLogs()
}

func TestFramesKeepExactGeometry(t *testing.T) {
	sizes := [][2]int{{60, 17}, {72, 20}, {80, 24}, {99, 30}, {100, 30}, {120, 40}, {200, 60}}
	long := strings.Repeat("a very long log line that must wrap or clip without breaking the frame ", 6)
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			m := uiTestModel(t, w, h)
			assertFrame(t, m, "initial")

			// Running script with hostile output: colours, cursor moves, tabs, CR, wide runes.
			m.scripts[0].State = "Running"
			m.scripts[0].StartedAt = time.Now().Add(-75 * time.Second)
			m.scripts[0].CPUHistory = []float64{5, 20, 80, 40, 10, 99}
			m.scripts[0].MemHistory = []float64{100, 120, 150, 180}
			feedLogs(m, "backup_db",
				"\x1b[32mgreen\x1b[0m ok", "\x1b[2J\x1b[Hcursor games", "tab\tseparated", "progress 10%\rprogress 90%",
				long, "ERROR: something broke", "✓ 日本語 wide 🚀", "WARN: careful")
			assertFrame(t, m, "running logs")

			press(m, "tab")
			for _, k := range []string{"2", "3", "1"} {
				press(m, k)
				assertFrame(t, m, "tab "+k)
			}
			press(m, "tab", "/")
			assertFrame(t, m, "filter")
			press(m, "d", "e", "p")
			assertFrame(t, m, "filter typed")
			press(m, "esc")
			press(m, "esc")

			for _, k := range []string{"a", "e", "enter"} {
				press(m, k)
				assertFrame(t, m, "dialog "+k)
				for i := 0; i < 30; i++ {
					press(m, "tab")
					assertFrame(t, m, fmt.Sprintf("dialog %s tab %d", k, i))
				}
				press(m, "esc")
				if m.activeView != "main" {
					t.Fatalf("esc should close dialog %q, view=%q", k, m.activeView)
				}
			}
			for _, k := range []string{"d", "?", "q", ",", "x"} {
				press(m, k)
				assertFrame(t, m, "dialog "+k)
				press(m, "esc")
			}
			if m.cleanupMode || m.settingsMode || m.activeView != "main" {
				t.Fatalf("dialogs should all be closed: view=%q cleanup=%v settings=%v", m.activeView, m.cleanupMode, m.settingsMode)
			}

			press(m, "g")
			assertFrame(t, m, "groups")
			press(m, "tab")
			assertFrame(t, m, "groups right")
			press(m, "down", "down", "down", "down")
			assertFrame(t, m, "groups steps")
			press(m, "tab", "enter")
			assertFrame(t, m, "members")
			press(m, "esc", "a")
			assertFrame(t, m, "group form")
			press(m, "esc", "d")
			assertFrame(t, m, "group delete")
			press(m, "esc", "?")
			assertFrame(t, m, "groups help")
			press(m, "esc", "esc")
			if m.activeView != "main" {
				t.Fatalf("expected main view, got %q", m.activeView)
			}
		})
	}
}

func TestTooSmallTerminal(t *testing.T) {
	m := uiTestModel(t, 50, 12)
	v := m.View()
	if !strings.Contains(stripANSI(v), "Terminal too small") {
		t.Fatalf("expected a too-small notice, got:\n%s", stripANSI(v))
	}
	if n := len(strings.Split(v, "\n")); n != 12 {
		t.Fatalf("too-small screen should still fill the terminal, got %d lines", n)
	}
	// Keys must not panic while too small.
	press(m, "j", "r", "a", "esc", "?", "esc")
}

func TestEmptyConfigRenders(t *testing.T) {
	m := uiTestModel(t, 100, 30)
	m.scripts, m.config.Scripts, m.config.Groups, m.groups = nil, nil, nil, nil
	assertFrame(t, m, "no scripts")
	for _, k := range []string{"j", "k", "r", "s", "e", "d", "enter", "h", "o", "c", " ", "tab", "1", "2", "3", "f", "v", "g"} {
		press(m, k) // none of these may panic on an empty list
		assertFrame(t, m, "empty "+k)
	}
	press(m, "esc")
}

func TestSecretsAreMasked(t *testing.T) {
	m := uiTestModel(t, 120, 40)
	press(m, "3")
	v := stripANSI(assertFrame(t, m, "config"))
	if strings.Contains(v, "hunter2") {
		t.Fatal("secret value leaked into the Config tab")
	}
	if !strings.Contains(v, "DB_NAME") || !strings.Contains(v, "production") {
		t.Fatal("regular variables should be visible")
	}
	press(m, "v")
	if !strings.Contains(stripANSI(m.View()), "hunter2") {
		t.Fatal("V should reveal secrets")
	}
}

func TestQuitAsksWhenRunning(t *testing.T) {
	m := uiTestModel(t, 100, 30)
	if _, cmd := m.Update(key("q")); cmd == nil {
		t.Fatal("q on an idle app should quit")
	}
	m = uiTestModel(t, 100, 30)
	m.scripts[0].State = "Running"
	m.Update(key("q"))
	if m.activeView != "quit_confirm" {
		t.Fatalf("q with a running script must ask first (view=%q)", m.activeView)
	}
	assertFrame(t, m, "quit confirm")
	press(m, "esc")
	if m.activeView != "main" {
		t.Fatalf("esc should return to main, got %q", m.activeView)
	}
}

func TestSelectionAndFilter(t *testing.T) {
	m := uiTestModel(t, 100, 30)
	press(m, "j")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	press(m, "/", "l", "i", "n", "enter")
	if m.cursor != 2 || m.activeView != "main" {
		t.Fatalf("filter should select 'lint' (cursor=%d view=%q)", m.cursor, m.activeView)
	}
	press(m, "esc")
	if m.filterQuery != "" {
		t.Fatal("esc should clear the filter")
	}
	press(m, "p")
	if !m.parallelMode {
		t.Fatal("p should toggle parallel mode")
	}
}

func TestRunsTabLoadsPastRunWithoutTouchingLiveState(t *testing.T) {
	m := uiTestModel(t, 120, 40)
	dir := m.scripts[0].Config.OutputFolderPath
	if err := writeTaskFile(dir, 1, "backup_db", "Failed", 40, "old run output\nERROR: disk full"); err != nil {
		t.Fatal(err)
	}
	msg := historyLoadedMsg{folder: dir}
	msg.items, _ = getTaskHistory(dir)
	m.Update(msg)

	m.scripts[0].State = "Success"
	m.scripts[0].TaskID = 9
	m.scripts[0].Logs = "live output"
	press(m, "h")
	if m.ui.tab != tabRuns || m.activePanel != panelRight {
		t.Fatalf("h should open the Runs tab (tab=%d)", m.ui.tab)
	}
	assertFrame(t, m, "runs")
	press(m, "enter")
	if m.ui.viewing == nil || !strings.Contains(m.ui.viewing.logs, "disk full") {
		t.Fatal("enter should load the selected run")
	}
	if m.scripts[0].Logs != "live output" || m.scripts[0].TaskID != 9 || m.scripts[0].State != "Success" {
		t.Fatal("viewing an old run must not modify the live script state")
	}
	v := stripANSI(assertFrame(t, m, "viewing"))
	if !strings.Contains(v, "disk full") || !strings.Contains(v, "RUN #1") {
		t.Fatalf("expected the old log and a banner:\n%s", v)
	}
	press(m, "esc")
	if m.ui.viewing != nil {
		t.Fatal("esc should return to live output")
	}
}

func TestStatusClassification(t *testing.T) {
	cases := map[string]statusLevel{
		"Error: Name is required":                  levelError,
		"Script 'x' failed: exit status 2":         levelError,
		"Successfully added script 'x'.":           levelSuccess,
		"Log copied to clipboard":                  levelSuccess,
		"Multi-run mode: PARALLEL — all at once":   levelInfo,
		"Group 'g' membership updated (2 scripts)": levelSuccess,
	}
	for msg, want := range cases {
		if got := classifyStatus(msg); got != want {
			t.Errorf("classifyStatus(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestKitGeometry(t *testing.T) {
	for _, w := range []int{4, 10, 33} {
		for _, h := range []int{2, 5, 9} {
			p := renderPanel(w, h, panelSpec{title: paintB("A very long title indeed", pal.accent), right: "right"}, []string{"x", strings.Repeat("y", 100), "\x1b[31mred\x1b[0m"})
			if len(p) != h {
				t.Fatalf("panel %dx%d has %d lines", w, h, len(p))
			}
			for _, l := range p {
				if lipgloss.Width(l) != w {
					t.Fatalf("panel %dx%d line is %d wide: %q", w, h, lipgloss.Width(l), stripANSI(l))
				}
			}
		}
	}
	if got := truncate("日本語日本語", 7); lipgloss.Width(got) > 7 {
		t.Fatalf("truncate overflowed: %q", got)
	}
	if got := stripANSI("\x1b[1;31mred\x1b[0m"); got != "red" {
		t.Fatalf("stripANSI = %q", got)
	}
	if got := sanitizeLog("a\x1b[2Jb\x1b[32mc\x1b[0m\x07"); !strings.Contains(got, "\x1b[32m") || strings.Contains(got, "[2J") || strings.Contains(got, "\x07") {
		t.Fatalf("sanitizeLog = %q", got)
	}
}
