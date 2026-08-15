package main

import "time"

// Dashboard messages are routed through the Bubble Tea model so all writes stay
// serialized on the same Update() event loop as the TUI.
type DashboardRunMsg struct {
	Alias string
}

type DashboardStopMsg struct {
	Alias string
}

type DashboardDeleteMsg struct {
	Alias string
}

type DashboardCreateMsg struct {
	Config ScriptConfig
}

type DashboardEditMsg struct {
	Alias  string
	Config ScriptConfig
}

type DashboardEnvUpdateMsg struct {
	Alias  string
	Input  map[string]interface{}
	Cron   string
	Notify bool
}

type DashboardThemeMsg struct {
	Theme ThemeConfig
}

type DashboardRunBatchMsg struct {
	Aliases []string
}

type DashboardQueryMsg struct {
	Reply chan DashboardSnapshot
}

type DashboardSnapshot struct {
	Scripts []DashboardScriptView `json:"scripts"`
	Theme   ThemeConfig           `json:"theme"`
}

type DashboardRunPipelineMsg struct {
	PipelineID string
	Steps      []string
}

type DashboardStopPipelineMsg struct {
	PipelineID string
}

type PipelineScriptView struct {
	Step       int    `json:"step"`
	Alias      string `json:"alias"`
	State      string `json:"state"`
	Progress   int    `json:"progress"`
	Logs       string `json:"logs"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

type PipelineExecution struct {
	ID          string
	Name        string
	Description string
	Steps       []string
	StepStates  []string
	StepLogs    []string
	Status      string
	StartedAt   time.Time
	FinishedAt  time.Time
}
type DashboardCreatePipelineMsg struct {
	Name        string
	Description string
	Steps       []string
}

type DashboardUpdatePipelineMsg struct {
	ID          string
	Name        string
	Description string
	Steps       []string
}

type DashboardDeletePipelineMsg struct {
	PipelineID string
}
type DashboardScriptView struct {
	Alias            string    `json:"alias"`
	Description      string    `json:"description"`
	Command          string    `json:"command"`
	OutputFolderPath string    `json:"outputFolderPath"`
	Cron             string    `json:"cron,omitempty"`
	Host             string    `json:"host,omitempty"`
	State            string    `json:"state"`
	Progress         int       `json:"progress"`
	Logs             string    `json:"logs"`
	TaskID           int       `json:"taskId"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	FinishedAt       time.Time `json:"finishedAt,omitempty"`
}
