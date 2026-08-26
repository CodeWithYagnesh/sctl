# SCTL — Script Controller 🚀

> A modern, elegant Terminal User Interface (TUI) task automation dashboard, script runner, and cron scheduler built in Go using the **Bubble Tea** framework. It provides a visual workspace for managing, running, and scheduling tasks, scripts, and notebooks with real-time log monitoring and progress tracking.

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

---

## Table of Contents

- [Features](#features-)
- [Installation](#installation-)
- [Quick Start](#quick-start)
- [Configuration](#configuration-)
- [TUI Usage](#tui-usage-%EF%B8%8F)
- [Groups & Pipelines](#groups--pipelines)
- [Headless Mode](#headless-mode)
- [Progress Protocol](#progress-protocol-)
- [Themes](#themes)
- [Task Outputs & Logging](#task-outputs--logging-)
- [HTML Report Integration](#context-aware-html-report-integration-)
- [Architecture](#architecture)
- [Contributing](#contributing-)
- [License](#license-)

---

## Features ✨

- **Interactive TUI**: Built with Bubble Tea & Lipgloss, featuring full viewport scrolling for logs, beautiful status badges, real-time execution progress bars, and a live execution status strip with spinner and elapsed time.
- **Dual-Mode Execution**:
  - **TUI Dashboard**: Trigger tasks manually, configure parameters, delete scripts, or stop processes interactively.
  - **Headless Mode**: Execute scripts directly via CLI (e.g. `sctl --run <alias>`) with live output streaming to stdout, perfect for background automation.
- **Groups & Pipelines**: Organize related scripts into named groups. Run them in **parallel** or as **sequential pipelines** with visual flow connectors and halt-on-failure.
- **Automated Crontab Sync**: Assign a cron schedule to any task, and `sctl` will automatically synchronize it with your user `crontab` to run headlessly.
- **Cross-Platform Process Management**: Cleanly starts and terminates process trees. Uses Unix process groups on macOS/Linux and `taskkill` on Windows to guarantee child processes are cleaned up upon cancellation.
- **Persistent Log Files**: Saves task results, execution statuses, and incremental log streams to structured `task_<id>.yaml` files in your configured output directories.
- **Open HTML Output**: Automatically detects and opens the most recently generated HTML report or notebook output of a script in your default system browser (cross-platform).
- **Desktop Notifications**: Optional OS notification on script completion.
- **Themes**: Built-in presets: `default`, `monokai`, `catppuccin`, `nord`.

---

## Installation 📦

### 1. Fast Installer (Recommended)

Install the **latest** `sctl` release to `/usr/local/bin` (or `~/.local/bin` if `/usr/local/bin` is not writable) with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/CodeWithYagnesh/sctl/refs/heads/main/install.sh | sh
```

#### Install a Specific Version

You can pin the installer to an exact release using either a CLI argument or the `VERSION` environment variable:

```bash
# Pass the version as an argument (recommended when saving to a file first)
./install.sh v1.0.8

# Pass the version inline via the VERSION environment variable (pipe-friendly)
curl -fsSL https://raw.githubusercontent.com/CodeWithYagnesh/sctl/refs/heads/main/install.sh | VERSION=v1.0.8 sh
```

> [!TIP]
> The `v` prefix is optional — both `v1.0.8` and `1.0.8` are accepted. Browse all available releases at the [GitHub Releases page](https://github.com/CodeWithYagnesh/sctl/releases).

#### Install to a Custom Directory

Set the `BINDIR` environment variable to override the default install location:

```bash
curl -fsSL https://raw.githubusercontent.com/CodeWithYagnesh/sctl/refs/heads/main/install.sh | BINDIR=$HOME/bin sh

# Or for a specific version to a custom directory
curl -fsSL https://raw.githubusercontent.com/CodeWithYagnesh/sctl/refs/heads/main/install.sh | VERSION=v1.0.8 BINDIR=$HOME/bin sh
```

### 2. Install via Go

If you have Go installed on your system, you can build and install the binary directly:

```bash
# Latest version
go install github.com/CodeWithYagnesh/sctl@latest

# Specific version
go install github.com/CodeWithYagnesh/sctl@v1.0.8
```

### 3. Uninstall

To completely remove `sctl` from your system (including its cron configurations):

```bash
# Remove the binary from possible installation directories
rm -f /usr/local/bin/sctl ~/.local/bin/sctl $(go env GOPATH)/bin/sctl 2>/dev/null || sudo rm -f /usr/local/bin/sctl

# Clean up any sctl scheduled jobs from your crontab
crontab -l 2>/dev/null | grep -v -E "SCTL" | crontab -
```

---

## Quick Start

1. **Launch the TUI:**
   ```bash
   sctl
   ```

2. **Add your first script** — press `A` and fill in:
   - Name Alias: `hello_world`
   - Command: `echo "Hello, World!"`
   - Output Folder: `./output/hello_world`

3. **Run it** — select the script and press `R`.

4. **Create a Group** — press `G`, then `A` to add a group. Toggle `Pipeline: y` for sequential execution.

---

## Configuration ⚙️

`sctl` is configured via a simple `config.yaml` file. By default, it looks for `config.yaml` in the current directory, but you can override this path by setting the `SCTL_CONFIG` environment variable:

```bash
export SCTL_CONFIG="$HOME/.config/sctl/config.yaml"
```

### Example `config.yaml`

```yaml
theme:
  name: monokai
  accent: "#66d9ef"
  success: "#a6e22e"
  fail: "#f92672"
  stopped: "#fd971f"
  idle: "#49483e"

scripts:
  - name_alias: system_check
    description: Run automated system health checks
    command: ./scripts/health_check.sh
    output_folder_path: ./output/system_check
    cron: "0 0 * * *" # Runs daily at midnight

  - name_alias: database_backup
    description: Production database backup script
    command: ./scripts/backup.sh
    output_folder_path: ./output/backup
    input:
      DB_NAME: "production"
      COMPRESSION_LEVEL: "9"

  - name_alias: large_log_test
    description: Generates a very large log stream for GUI stress testing
    command: for i in $(seq 1 400); do echo "log line $i / 400"; done
    output_folder_path: ./output/large_log_test

groups:
  - name: deploy_pipeline
    description: Full deployment pipeline
    pipeline: true
    scripts:
      - system_check
      - database_backup

  - name: health_checks
    description: Run all health checks in parallel
    pipeline: false
    scripts:
      - system_check
```

### Configuration Fields

#### `ScriptConfig`

| Field | Required | Description |
|---|---|---|
| `name_alias` | ✅ | A unique, friendly identifier for your script/task. |
| `description` | ❌ | A brief description of what the script does. |
| `command` | ✅ | The shell command to run. |
| `output_folder_path` | ✅ | Directory where run records (`task_<id>.yaml`) will be saved. |
| `input` | ❌ | Map of environment variables supplied to the script execution environment. |
| `cron` | ❌ | *(Optional)* Standard cron expression to schedule automatic headless executions. |
| `notify` | ❌ | Desktop notification on complete (`true`/`false`). |
| `host` | ❌ | SSH host for remote execution. |

#### `GroupConfig`

| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Unique group name |
| `description` | ❌ | Group description |
| `pipeline` | ❌ | `true` = sequential, `false` = parallel (default) |
| `scripts` | ❌ | List of script aliases belonging to the group |

#### `ThemeConfig`

| Field | Description |
|---|---|
| `name` | Built-in preset: `default`, `monokai`, `catppuccin`, `nord` |
| `accent` | Focus states and active UI |
| `success` | Success badge color |
| `fail` | Failure badge color |
| `stopped` | Stopped badge color |
| `idle` | Neutral border and background color |

> [!IMPORTANT]
> **Unique Output Folders Required:** Each script configured in `sctl` **MUST** have a unique `output_folder_path`. Since task files (`task_<id>.yaml`) and HTML reports are scanned and loaded from this directory, using the same output directory across multiple scripts will cause task IDs and log/HTML records to conflict.

---

## TUI Usage 🛠️

### Command Line Flags 🏁

| Flag | Description |
|---|---|
| `--help`, `-h` | Display the styled help banner, listing TUI shortcuts, environment variables, and usage options. |
| `--version`, `-v` | Print the current `sctl` version and GitHub repository information. |
| `--run <script-alias>`, `-run <script-alias>` | Execute the specified script directly in headless mode. |
| `--run-group <group-name>` | Execute all scripts in a group (pipeline or parallel) in headless mode. |

### Environment Variables 🌐

| Variable | Description |
|---|---|
| `SCTL_CONFIG` | Custom path to the `config.yaml` file (defaults to current directory). |
| `SCTL_TASK_ID` | Injected into the execution environment of tasks to indicate the current task run's ID (e.g. `14`). |

### Launching the TUI Dashboard

Simply run `sctl` to launch the interactive interface:

```bash
sctl
```

---

## Groups & Pipelines

Press `G` to enter the **Groups** view.

### Left Pane — Group List
- See all groups with mode badges (`PARALLEL` / `PIPELINE`)
- Live `●` indicator when any script in the group is running
- Navigate with `↑/↓`, switch pane with `Tab`

### Right Pane — Group Detail
- **Pipeline mode**: Scripts rendered vertically with animated connectors (`│`, `┃`, `✕`)
  - Green connector = previous step succeeded
  - Pulsing accent connector = previous step running
  - Red `✕` = previous step failed, pipeline halted
  - Downstream steps show `○ BLOCKED` badge
- **Parallel mode**: Scripts stacked with separator lines
- Each card shows: sequence number, status badge, progress bar, live timer, and last log line
- Failed pipeline steps halt downstream execution with a `⛔ PIPELINE HALTED` banner

---

### Keyboard Shortcuts ⌨️

#### Main View

| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Navigate scripts |
| `Tab` | Switch focus between Left (Scripts) and Right (Logs) panels |
| `Space` | Check/uncheck script for multi-run |
| `R` | Run selected / checked script(s) |
| `S` | Stop running script(s) |
| `P` | Toggle Parallel / Sequential mode |
| `A` | Add new script |
| `E` | Edit selected script |
| `Enter` | Edit env vars & cron for selected script |
| `D` / `Delete` | Delete selected script |
| `/` | Filter scripts |
| `H` | View execution history |
| `O` | Open latest HTML report in browser |
| `T` | Cycle themes |
| `G` | Open Groups view |
| `Ctrl+↑/↓` | Reorder scripts |
| `Q` / `Ctrl+C` | Quit |

#### Groups View

| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Navigate groups (left pane) or scripts (right pane) |
| `Tab` | Switch focus (Left ↔ Right pane) |
| `R` | Run all scripts in selected group |
| `S` | Stop running scripts in selected group |
| `A` | Add new group |
| `E` | Edit selected group |
| `Enter` | Edit group members (add/remove scripts) |
| `D` / `Delete` | Delete selected group |
| `G` / `Esc` | Back to main view |
| `Q` | Quit |

---

## Headless Mode

Run scripts without the TUI — perfect for CI/CD pipelines and cron jobs.

```bash
# Run a single script
sctl --run system_check

# Run an entire group
sctl --run-group deploy_pipeline

# Show version
sctl --version

# Show help
sctl --help
```

Headless mode streams stdout in real-time and exits with a non-zero code on failure.

---

## Progress Protocol 📊

Scripts can drive the TUI progress bar by printing a specially formatted line to stdout:

```bash
echo "__PROGRESS__:30"
```

- Value range: `0` to `100`
- The line is intercepted by `sctl` and filtered from logs
- Example script:
  ```bash
  echo "__PROGRESS__:0"
  sleep 1
  echo "__PROGRESS__:50"
  sleep 1
  echo "__PROGRESS__:100"
  ```

### Python Example
```python
import time
import sys

print("Initializing system update...")
sys.stdout.flush()
time.sleep(1)

# Report 20% progress
print("__PROGRESS__:20")
sys.stdout.flush()

print("Downloading dependencies...")
time.sleep(2)

# Report 60% progress
print("__PROGRESS__:60")
sys.stdout.flush()

print("Applying changes...")
time.sleep(2)

# Report 100% progress
print("__PROGRESS__:100")
sys.stdout.flush()
```

### Bash Example
```bash
#!/bin/bash

echo "Starting deployment..."
sleep 1

echo "__PROGRESS__:10"
echo "Cleaning up temp files..."
sleep 1

echo "__PROGRESS__:50"
echo "Copying assets..."
sleep 2

echo "__PROGRESS__:90"
echo "Restarting services..."
sleep 1

echo "__PROGRESS__:100"
echo "Deployment successful!"
```

---

## Themes

Cycle themes in-app with `T`. Available presets:

| Theme | Accent | Success | Fail | Stopped | Idle |
|-------|--------|---------|------|---------|------|
| `default` | `#6366f1` | `#059669` | `#f43f5e` | `#f59e0b` | `#334155` |
| `monokai` | `#66d9ef` | `#a6e22e` | `#f92672` | `#fd971f` | `#75715e` |
| `catppuccin` | `#89b4fa` | `#a6e3a1` | `#f38ba8` | `#f9e2af` | `#6c7086` |
| `nord` | `#88c0d0` | `#a3be8c` | `#bf616a` | `#ebcb8b` | `#4c566a` |

Customize colors in `config.yaml` under the `theme` key.

---

## Task Outputs & Logging 🗃️

Every run of a task is assigned an incremental `task_id` and saved in `output_folder_path` as `task_<id>.yaml`. The task file captures the configuration state, execution progress, and exact logs:

```yaml
task:
  task_id: 15
  script_name_alias: system_check
  state: Success
  progress: 100
  logs: |
    [15:30:00] Starting automated system health checks...
    [15:30:04] Checked system metrics. All parameters normal.
    [15:30:05] Finished successfully.
```

---

## Context-Aware HTML Report Integration 📄

`sctl` automatically matches task execution histories to their respective HTML reports if your scripts dynamically name the HTML output files.

### How it works:
1. When starting a task, `sctl` injects an environment variable `SCTL_TASK_ID` containing the current incremental task ID (e.g., `14`, `15`).
2. Have your script output its HTML report using this ID, such as naming the file `report_task_${SCTL_TASK_ID}.html` or `report_${SCTL_TASK_ID}.html`.
3. When you browse past runs in the **History Viewer** and press **`O`**, `sctl` looks for any HTML file in the output folder containing `task_<id>` or `_<id>` and opens the correct historical HTML report in your browser. If none is found, it defaults to opening the newest HTML file in the output directory.

---

## Architecture

```
sctl/
├── main.go          # Entry point, headless runner, help
├── model.go         # Bubble Tea model, state management, Update loop
├── ui.go            # All view rendering (panels, forms, groups, pipeline viz)
├── types.go         # Structs, messages, theme definitions
├── tasks.go         # Task execution engine (StartTask, StopTask, progress parser)
├── config.go        # YAML config load/save with defaults
├── theme.go         # Theme application and cycling
├── cron.go          # Crontab synchronization
├── sys_unix.go      # Unix process group termination
└── sys_windows.go   # Windows taskkill termination
```

### Task Lifecycle

1. User triggers run → `StartTask()` spawns command
2. Task ID assigned incrementally per output folder
3. `task_{id}.yaml` written continuously with state, progress, and logs
4. `__PROGRESS__:N` lines parsed and stripped from logs
5. On completion, state set to `Success`, `Failed`, or `Stopped`
6. Desktop notification fired if `notify: true`

---

## Contributing 🤝

Contributions are welcome! Please open an issue or submit a pull request if you want to propose improvements or bug fixes.

## License 📄

This project is licensed under the MIT License.

---

## Author

**Yagnesh** — [github.com/CodeWithYagnesh](https://github.com/CodeWithYagnesh)

> Built with ❤️ using [Charm](https://charm.sh) libraries.
