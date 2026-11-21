package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// seedRuns writes task_1..n.yaml plus a few files that must never be deleted.
func seedRuns(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "task_9"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		write(t, filepath.Join(dir, fmt.Sprintf("task_%d.yaml", i)))
	}
	for _, f := range []string{"report_task_1.html", "index.html", "task_x.yaml", "task_1.yaml.bak", "notes.txt"} {
		write(t, filepath.Join(dir, f))
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func records(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for i := 1; i <= 20; i++ {
		if exists(filepath.Join(dir, fmt.Sprintf("task_%d.yaml", i))) {
			out = append(out, fmt.Sprint(i))
		}
	}
	return out
}

func assertSafeFiles(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"report_task_1.html", "index.html", "task_x.yaml", "task_1.yaml.bak", "notes.txt", "task_9"} {
		if !exists(filepath.Join(dir, f)) {
			t.Fatalf("%s must not be deleted", f)
		}
	}
}

func TestCleanupKeepLastDefault(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 5)
	scripts := []ScriptConfig{{NameAlias: "a", OutputFolderPath: dir}}

	plan := planCleanup(scripts, defaultCleanupOptions(), nil)
	if files, _ := cleanupTotals(plan); files != 4 || plan[0].Kept != 1 {
		t.Fatalf("want 4 deletions, 1 kept; got %+v", plan[0])
	}
	if got := records(t, dir); len(got) != 5 {
		t.Fatalf("planning must not delete anything, left %v", got)
	}
	n, err := applyCleanup(plan)
	if err != nil || n != 4 {
		t.Fatalf("applyCleanup = %d, %v", n, err)
	}
	if got := records(t, dir); !reflect.DeepEqual(got, []string{"5"}) {
		t.Fatalf("only the newest record should stay, got %v", got)
	}
	assertSafeFiles(t, dir)
}

func TestCleanupKeepLastN(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 5)
	plan := planCleanup([]ScriptConfig{{NameAlias: "a", OutputFolderPath: dir}}, CleanupOptions{Mode: CleanupKeepLast, Value: 3}, nil)
	applyCleanup(plan)
	if got := records(t, dir); !reflect.DeepEqual(got, []string{"3", "4", "5"}) {
		t.Fatalf("got %v", got)
	}
}

func TestCleanupOlderThan(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 4)
	old := time.Now().AddDate(0, 0, -10)
	for _, i := range []int{1, 2} {
		os.Chtimes(filepath.Join(dir, fmt.Sprintf("task_%d.yaml", i)), old, old)
	}
	plan := planCleanup([]ScriptConfig{{NameAlias: "a", OutputFolderPath: dir}}, CleanupOptions{Mode: CleanupOlderThan, Value: 7}, nil)
	applyCleanup(plan)
	if got := records(t, dir); !reflect.DeepEqual(got, []string{"3", "4"}) {
		t.Fatalf("got %v", got)
	}
	assertSafeFiles(t, dir)
}

func TestCleanupAllKeepsNonRecords(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 3)
	plan := planCleanup([]ScriptConfig{{NameAlias: "a", OutputFolderPath: dir}}, CleanupOptions{Mode: CleanupAll}, nil)
	applyCleanup(plan)
	if got := records(t, dir); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	assertSafeFiles(t, dir)
}

func TestCleanupSkipsRunningTask(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 3)
	running := map[string]map[int]bool{filepath.Clean(dir): {2: true}}
	plan := planCleanup([]ScriptConfig{{NameAlias: "a", OutputFolderPath: dir}}, CleanupOptions{Mode: CleanupAll}, running)
	applyCleanup(plan)
	if got := records(t, dir); !reflect.DeepEqual(got, []string{"2"}) {
		t.Fatalf("running task 2 must stay, got %v", got)
	}
	if plan[0].Protected != 1 {
		t.Fatalf("Protected = %d", plan[0].Protected)
	}
}

func TestCleanupSharedFolderPlannedOnce(t *testing.T) {
	dir := t.TempDir()
	seedRuns(t, dir, 3)
	plan := planCleanup([]ScriptConfig{
		{NameAlias: "a", OutputFolderPath: dir},
		{NameAlias: "b", OutputFolderPath: dir + "/"},
		{NameAlias: "c"},
	}, defaultCleanupOptions(), nil)
	if len(plan) != 1 || len(plan[0].Paths) != 2 || plan[0].Alias != "a, b" {
		t.Fatalf("got %+v", plan)
	}
}

func TestCleanupMissingFolder(t *testing.T) {
	plan := planCleanup([]ScriptConfig{{NameAlias: "a", OutputFolderPath: filepath.Join(t.TempDir(), "nope")}}, defaultCleanupOptions(), nil)
	if files, _ := cleanupTotals(plan); files != 0 {
		t.Fatalf("got %+v", plan)
	}
}

func TestParseCleanupArgs(t *testing.T) {
	ok := []struct {
		args []string
		want cleanupArgs
	}{
		{[]string{"--all"}, cleanupArgs{All: true, Opts: CleanupOptions{Mode: CleanupKeepLast, Value: 1}}},
		{[]string{"--alias", "a,b", "--alias=c", "--older-than", "7", "-y"},
			cleanupArgs{Aliases: []string{"a", "b", "c"}, Opts: CleanupOptions{Mode: CleanupOlderThan, Value: 7}, Yes: true}},
		{[]string{"--all", "--keep-last", "3", "--confirm"}, cleanupArgs{All: true, Opts: CleanupOptions{Mode: CleanupKeepLast, Value: 3}, Yes: true}},
		{[]string{"--alias", "a", "--remove-all"}, cleanupArgs{Aliases: []string{"a"}, Opts: CleanupOptions{Mode: CleanupAll}}},
	}
	for _, c := range ok {
		got, err := parseCleanupArgs(c.args)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{
		{}, {"--keep-last", "2"}, {"--all", "--older-than"}, {"--all", "--older-than", "x"}, {"--all", "--older-than", "-1"},
		{"--all", "--remove-all", "--older-than", "3"}, {"--all", "--bogus"},
	} {
		if _, err := parseCleanupArgs(bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
	if got := normalizeKeepLast([]string{"--all", "--keep-last"}); !reflect.DeepEqual(got, []string{"--all", "--keep-last", "1"}) {
		t.Errorf("normalizeKeepLast: %v", got)
	}
}

func TestCleanupDialogFlow(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 18}} {
		m := uiTestModel(t, sz[0], sz[1])
		dirs := map[string]string{}
		for _, s := range m.scripts {
			dirs[s.Config.NameAlias] = s.Config.OutputFolderPath
			seedRuns(t, s.Config.OutputFolderPath, 4)
		}
		first := m.scripts[m.cursor].Config.NameAlias

		press(m, "x")
		assertFrame(t, m, "cleanup open")
		if !m.cleanupMode || !m.ui.cleanup.checked[first] {
			t.Fatalf("dialog should open with the selected script ticked")
		}
		// Enter only reviews: nothing is deleted yet.
		press(m, "enter")
		assertFrame(t, m, "cleanup review")
		if !m.ui.cleanup.confirm || len(records(t, dirs[first])) != 4 {
			t.Fatalf("review step expected, nothing deleted")
		}
		// Esc goes back one step, not out of the dialog.
		press(m, "esc")
		if !m.cleanupMode || m.ui.cleanup.confirm {
			t.Fatalf("esc in review should return to the form")
		}
		// Tick a second script, switch to "Older than", then back to keep-last.
		press(m, "down", " ")
		press(m, "tab", "right", "left")
		assertFrame(t, m, "cleanup mode")
		press(m, "tab")
		press(m, "esc")
		if m.cleanupMode {
			t.Fatalf("esc should close the form")
		}

		// Full flow: two scripts, default keep-last.
		press(m, "x", "a") // tick all scripts
		press(m, "enter", "enter")
		if m.cleanupMode {
			t.Fatalf("dialog should close after deleting")
		}
		for alias, dir := range dirs {
			if got := records(t, dir); !reflect.DeepEqual(got, []string{"4"}) {
				t.Fatalf("%s: want only newest record, got %v", alias, got)
			}
			assertSafeFiles(t, dir)
		}
		assertFrame(t, m, "after cleanup")
	}
}

func TestCleanupDialogValidation(t *testing.T) {
	m := uiTestModel(t, 100, 30)
	seedRuns(t, m.scripts[m.cursor].Config.OutputFolderPath, 3)
	press(m, "x", " ") // untick the only script
	press(m, "enter")
	if m.ui.cleanup.confirm {
		t.Fatalf("no script ticked must not reach review")
	}
	press(m, " ", "tab", "tab")
	for i := 0; i < 3; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	press(m, "enter")
	if m.ui.cleanup.confirm {
		t.Fatalf("empty number must not reach review")
	}
	press(m, "z") // letters are ignored
	if m.cleanupInput.Value() != "" {
		t.Fatalf("letters must be ignored, got %q", m.cleanupInput.Value())
	}
	press(m, "0")
	press(m, "enter")
	if !m.ui.cleanup.confirm {
		t.Fatalf("keep 0 should reach review")
	}
}

func TestCleanupDialogNothingToDelete(t *testing.T) {
	m := uiTestModel(t, 100, 30)
	seedRuns(t, m.scripts[m.cursor].Config.OutputFolderPath, 1)
	press(m, "x", "enter")
	if m.ui.cleanup.confirm || m.ui.cleanup.notice == "" {
		t.Fatalf("a single record with keep-last 1 has nothing to delete")
	}
	assertFrame(t, m, "nothing to delete")
}
