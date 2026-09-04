package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func GetConfigPath() string {
	path := os.Getenv("SCTL_CONFIG")
	if path == "" {
		path = "config.yaml"
	}
	return path
}

func LoadConfig() (*Config, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			defaultConfig := Config{
				Scripts: []ScriptConfig{
					{
						NameAlias:        "hello_world",
						Description:      "Print a friendly greeting",
						Command:          `echo "Hello, World!"`,
						OutputFolderPath: "./output",
					},
				},
			}
			err = SaveConfig(&defaultConfig)
			if err != nil {
				return nil, err
			}
			return &defaultConfig, nil
		}
		return nil, err
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	cfg.Theme = normalizeTheme(cfg.Theme)
	_ = SyncCrontab(&cfg)
	return &cfg, nil
}

func backupConfig(path string) error {
	timestamp := time.Now().Format("20060102-150405")
	backupPath := path + "." + timestamp + ".bak"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return err
	}
	// keep last 5 backups
	entries, err := filepath.Glob(path + ".*.bak")
	if err == nil && len(entries) > 5 {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i] < entries[j]
		})
		for _, e := range entries[:len(entries)-5] {
			os.Remove(e)
		}
	}
	return nil
}

func SaveConfig(cfg *Config) error {
	cfg.Theme = normalizeTheme(cfg.Theme)
	path := GetConfigPath()
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	// backup existing config
	if _, err := os.Stat(path); err == nil {
		if err := backupConfig(path); err != nil {
			// non-fatal
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	_ = SyncCrontab(cfg)
	return nil
}

func SyncCrontab(cfg *Config) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}

	cmd := exec.Command("crontab", "-l")
	var existingLines []string
	out, err := cmd.Output()
	if err == nil {
		rawLines := strings.Split(string(out), "\n")
		for _, line := range rawLines {
			existingLines = append(existingLines, strings.TrimRight(line, "\r\n"))
		}
		if len(existingLines) > 0 && existingLines[len(existingLines)-1] == "" {
			existingLines = existingLines[:len(existingLines)-1]
		}
	}

	var newLines []string
	skipNext := false
	for _, line := range existingLines {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(line, "# SCTL SCHEDULED JOB: ") {
			skipNext = true
			continue
		}
		newLines = append(newLines, line)
	}

	for _, script := range cfg.Scripts {
		cronExpr := strings.TrimSpace(script.Cron)
		if cronExpr != "" {
			newLines = append(newLines, fmt.Sprintf("# SCTL SCHEDULED JOB: %s", script.NameAlias))
			newLines = append(newLines, fmt.Sprintf("%s %s --run %q >/dev/null 2>&1", cronExpr, executable, script.NameAlias))
		}
	}

	if len(newLines) == 0 {
		rmCmd := exec.Command("crontab", "-r")
		_ = rmCmd.Run()
		return nil
	}

	tmpFile, err := os.CreateTemp("", "sctl_cron")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	writer := bufio.NewWriter(tmpFile)
	for _, line := range newLines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	installCmd := exec.Command("crontab", tmpFile.Name())
	return installCmd.Run()
}

func ValidateConfig(cfg *Config) []error {
	var errs []error
	aliasSet := make(map[string]bool)
	outputSet := make(map[string]bool)

	for i, s := range cfg.Scripts {
		if strings.TrimSpace(s.NameAlias) == "" {
			errs = append(errs, fmt.Errorf("scripts[%d]: name_alias is empty", i))
		} else {
			if aliasSet[s.NameAlias] {
				errs = append(errs, fmt.Errorf("scripts[%d]: duplicate name_alias %q", i, s.NameAlias))
			}
			aliasSet[s.NameAlias] = true
		}
		if strings.TrimSpace(s.OutputFolderPath) == "" {
			errs = append(errs, fmt.Errorf("scripts[%d]: output_folder_path is empty", i))
		} else {
			if outputSet[s.OutputFolderPath] {
				errs = append(errs, fmt.Errorf("scripts[%d]: duplicate output_folder_path %q", i, s.OutputFolderPath))
			}
			outputSet[s.OutputFolderPath] = true
		}
		if strings.TrimSpace(s.Command) == "" {
			errs = append(errs, fmt.Errorf("scripts[%d]: command is empty", i))
		}
		if s.Cron != "" && !isValidCron(s.Cron) {
			errs = append(errs, fmt.Errorf("scripts[%d]: invalid cron %q", i, s.Cron))
		}
	}

	for i, g := range cfg.Groups {
		if strings.TrimSpace(g.Name) == "" {
			errs = append(errs, fmt.Errorf("groups[%d]: name is empty", i))
		}
		for _, alias := range g.Scripts {
			if !aliasSet[alias] {
				errs = append(errs, fmt.Errorf("groups[%d]: references unknown script alias %q", i, alias))
			}
		}
	}

	return errs
}

func ExportConfig(outPath string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(outPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(outPath, data, 0644)
}

func ImportConfig(inPath string) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if errs := ValidateConfig(&cfg); len(errs) > 0 {
		return fmt.Errorf("validation failed: %v", errs)
	}
	// backup current
	if err := backupConfig(GetConfigPath()); err != nil {
		// non-fatal
	}
	return SaveConfig(&cfg)
}


