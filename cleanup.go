package main

// cleanup.go — removing saved run records (task_<id>.yaml) from the output
// folders of scripts. Only those exact files are ever deleted: HTML reports
// and anything else a script writes into its output folder are left alone.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CleanupMode says which run records a cleanup removes.
type CleanupMode int

const (
	CleanupKeepLast  CleanupMode = iota // keep the newest N records (default, N=1)
	CleanupOlderThan                    // remove records older than N days
	CleanupAll                          // remove every record
)

const (
	defaultKeepLast  = 1
	defaultOlderDays = 30
)

// CleanupOptions is a cleanup request. Value is the number of records to keep
// (CleanupKeepLast) or the age in days (CleanupOlderThan); CleanupAll ignores it.
type CleanupOptions struct {
	Mode  CleanupMode
	Value int
	Now   time.Time // zero means time.Now(); set by tests
}

func defaultCleanupOptions() CleanupOptions {
	return CleanupOptions{Mode: CleanupKeepLast, Value: defaultKeepLast}
}

func (o CleanupOptions) validate() error {
	if o.Mode != CleanupAll && o.Value < 0 {
		return fmt.Errorf("value must be 0 or more")
	}
	return nil
}

func (o CleanupOptions) describe() string {
	switch o.Mode {
	case CleanupOlderThan:
		return fmt.Sprintf("older than %d day(s)", o.Value)
	case CleanupAll:
		return "all records"
	}
	return fmt.Sprintf("all but the last %d record(s)", o.Value)
}

// cleanupTarget is the outcome of planning a cleanup for one output folder.
type cleanupTarget struct {
	Alias     string   // script alias(es) using this folder
	Dir       string   // output folder
	Paths     []string // task_<id>.yaml files that would be deleted
	Kept      int      // records that stay
	Protected int      // records kept only because their task is still running
}

var taskFileRe = regexp.MustCompile(`^task_(\d+)\.yaml$`)

// planCleanup works out what a cleanup would delete without deleting anything.
// running maps an output folder to the task IDs that are running right now;
// those records are never planned for deletion. Scripts that share an output
// folder are merged into one target.
func planCleanup(scripts []ScriptConfig, opts CleanupOptions, running map[string]map[int]bool) []cleanupTarget {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.AddDate(0, 0, -opts.Value)

	var plan []cleanupTarget
	byDir := map[string]int{}
	for _, s := range scripts {
		if strings.TrimSpace(s.OutputFolderPath) == "" {
			continue
		}
		dir := filepath.Clean(s.OutputFolderPath)
		if idx, ok := byDir[dir]; ok {
			plan[idx].Alias += ", " + s.NameAlias
			continue
		}
		byDir[dir] = len(plan)
		plan = append(plan, planFolder(s.NameAlias, dir, opts, cutoff, running[dir]))
	}
	return plan
}

func planFolder(alias, dir string, opts CleanupOptions, cutoff time.Time, running map[int]bool) cleanupTarget {
	t := cleanupTarget{Alias: alias, Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return t
	}
	type rec struct {
		id   int
		path string
		mod  time.Time
	}
	var recs []rec
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := taskFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		recs = append(recs, rec{id: id, path: filepath.Join(dir, e.Name()), mod: info.ModTime()})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].id > recs[j].id }) // newest first
	for i, r := range recs {
		del := false
		switch opts.Mode {
		case CleanupKeepLast:
			del = i >= opts.Value
		case CleanupOlderThan:
			del = r.mod.Before(cutoff)
		case CleanupAll:
			del = true
		}
		if del && running[r.id] {
			del = false
			t.Protected++
		}
		if del {
			t.Paths = append(t.Paths, r.path)
		} else {
			t.Kept++
		}
	}
	return t
}

// cleanupTotals returns how many files a plan deletes and from how many folders.
func cleanupTotals(plan []cleanupTarget) (files, folders int) {
	for _, t := range plan {
		if len(t.Paths) > 0 {
			files += len(t.Paths)
			folders++
		}
	}
	return files, folders
}

// applyCleanup deletes the files in a plan. It keeps going after a failure and
// returns how many files were deleted together with the first error.
func applyCleanup(plan []cleanupTarget) (int, error) {
	deleted := 0
	var first error
	for _, t := range plan {
		for _, p := range t.Paths {
			if err := os.Remove(p); err != nil {
				if !os.IsNotExist(err) && first == nil {
					first = err
				}
				continue
			}
			deleted++
		}
	}
	return deleted, first
}

// selectScripts picks scripts by alias, or all of them. Unknown aliases are an error.
func selectScripts(cfg *Config, aliases []string, all bool) ([]ScriptConfig, error) {
	if all {
		return cfg.Scripts, nil
	}
	var out []ScriptConfig
	var unknown []string
	for _, a := range aliases {
		found := false
		for _, s := range cfg.Scripts {
			if s.NameAlias == a {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			unknown = append(unknown, a)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown script alias: %s", strings.Join(unknown, ", "))
	}
	return out, nil
}

// cleanupArgs is the parsed form of `sctl cleanup ...`.
type cleanupArgs struct {
	Aliases []string
	All     bool
	Opts    CleanupOptions
	Yes     bool
}

// parseCleanupArgs parses the command line of `sctl cleanup`.
func parseCleanupArgs(args []string) (cleanupArgs, error) {
	ca := cleanupArgs{Opts: defaultCleanupOptions()}
	modeSet := false
	setMode := func(m CleanupMode, v int) error {
		if modeSet {
			return fmt.Errorf("use only one of --keep-last, --older-than, --remove-all")
		}
		modeSet = true
		ca.Opts.Mode, ca.Opts.Value = m, v
		return nil
	}
	for i := 0; i < len(args); i++ {
		flag, val, hasVal := strings.Cut(args[i], "=")
		next := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			return args[i], nil
		}
		num := func() (int, error) {
			v, err := next()
			if err != nil {
				return 0, err
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 0 {
				return 0, fmt.Errorf("%s needs a whole number, 0 or more", flag)
			}
			return n, nil
		}
		switch flag {
		case "--all":
			ca.All = true
		case "--alias", "--scripts":
			v, err := next()
			if err != nil {
				return ca, err
			}
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); a != "" {
					ca.Aliases = append(ca.Aliases, a)
				}
			}
		case "--keep-last":
			n, err := num()
			if err != nil {
				return ca, err
			}
			if err := setMode(CleanupKeepLast, n); err != nil {
				return ca, err
			}
		case "--older-than":
			n, err := num()
			if err != nil {
				return ca, err
			}
			if err := setMode(CleanupOlderThan, n); err != nil {
				return ca, err
			}
		case "--remove-all":
			if err := setMode(CleanupAll, 0); err != nil {
				return ca, err
			}
		case "--yes", "-y", "--confirm":
			ca.Yes = true
		default:
			return ca, fmt.Errorf("unknown option %q", args[i])
		}
	}
	if !ca.All && len(ca.Aliases) == 0 {
		return ca, fmt.Errorf("choose scripts with --alias <a,b> or --all")
	}
	return ca, nil
}
