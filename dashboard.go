package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

//go:embed web/dashboard.html
var dashboardHTML embed.FS

func startDashboard(p *tea.Program) {
	if p == nil {
		return
	}
	port := os.Getenv("SCTL_DASHBOARD_PORT")
	if port == "" {
		port = "3122"
	}
	addr := "127.0.0.1:" + port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("dashboard disabled: %v", err)
		return
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := dashboardHTML.ReadFile("web/dashboard.html")
		if err != nil {
			http.Error(w, "dashboard template unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/scripts", func(w http.ResponseWriter, r *http.Request) {
		reply := make(chan DashboardSnapshot, 1)
		p.Send(DashboardQueryMsg{Reply: reply})
		select {
		case snap := <-reply:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snap)
		case <-time.After(2 * time.Second):
			http.Error(w, "dashboard query timeout", http.StatusGatewayTimeout)
		}
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-dashboardUpdateSignal:
			}
			reply := make(chan DashboardSnapshot, 1)
			p.Send(DashboardQueryMsg{Reply: reply})
			select {
			case snap := <-reply:
				_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", mustJSON(snap))
				flusher.Flush()
			case <-time.After(2 * time.Second):
				_, _ = fmt.Fprint(w, "event: ping\ndata: keepalive\n\n")
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("POST /api/scripts/{alias}/run", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		p.Send(DashboardRunMsg{Alias: alias})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/scripts/{alias}/stop", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		p.Send(DashboardStopMsg{Alias: alias})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/run-batch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Aliases []string `json:"aliases"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p.Send(DashboardRunBatchMsg{Aliases: body.Aliases})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/scripts", func(w http.ResponseWriter, r *http.Request) {
		var cfg ScriptConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid script payload", http.StatusBadRequest)
			return
		}
		p.Send(DashboardCreateMsg{Config: cfg})
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("PUT /api/scripts/{alias}", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		var cfg ScriptConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid script payload", http.StatusBadRequest)
			return
		}
		p.Send(DashboardEditMsg{Alias: alias, Config: cfg})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("PUT /api/scripts/{alias}/input", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		var body struct {
			Cron   string                 `json:"cron"`
			Notify bool                   `json:"notify"`
			Input  map[string]interface{} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid input payload", http.StatusBadRequest)
			return
		}
		p.Send(DashboardEnvUpdateMsg{Alias: alias, Cron: body.Cron, Notify: body.Notify, Input: body.Input})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("DELETE /api/scripts/{alias}", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		p.Send(DashboardDeleteMsg{Alias: alias})
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/scripts/{alias}/history", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		cfg, err := LoadConfig()
		if err != nil {
			http.Error(w, "config load failed", http.StatusInternalServerError)
			return
		}
		for _, sc := range cfg.Scripts {
			if sc.NameAlias == alias {
				history, err := getTaskHistory(sc.OutputFolderPath)
				if err != nil {
					http.Error(w, "history unavailable", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(history)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /api/scripts/{alias}/report", func(w http.ResponseWriter, r *http.Request) {
		alias := strings.TrimSpace(r.PathValue("alias"))
		cfg, err := LoadConfig()
		if err != nil {
			http.Error(w, "config load failed", http.StatusInternalServerError)
			return
		}
		for _, sc := range cfg.Scripts {
			if sc.NameAlias == alias {
				folder := sc.OutputFolderPath
				files, err := filepath.Glob(filepath.Join(folder, "*.html"))
				if err != nil || len(files) == 0 {
					http.NotFound(w, r)
					return
				}
				latest := files[0]
				for _, f := range files[1:] {
					fi1, _ := os.Stat(latest)
					fi2, _ := os.Stat(f)
					if fi2 != nil && (fi1 == nil || fi2.ModTime().After(fi1.ModTime())) {
						latest = f
					}
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeFile(w, r, latest)
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("PUT /api/theme", func(w http.ResponseWriter, r *http.Request) {
		var theme ThemeConfig
		if err := json.NewDecoder(r.Body).Decode(&theme); err != nil {
			http.Error(w, "invalid theme payload", http.StatusBadRequest)
			return
		}
		p.Send(DashboardThemeMsg{Theme: theme})
		w.WriteHeader(http.StatusAccepted)
	})

	// Pipeline endpoints
	mux.HandleFunc("POST /api/pipelines", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Steps       []string `json:"steps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p.Send(DashboardCreatePipelineMsg{Name: body.Name, Description: body.Description, Steps: body.Steps})
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("POST /api/pipelines/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		pipelineID := strings.TrimSpace(r.PathValue("id"))
		p.Send(DashboardRunPipelineMsg{PipelineID: pipelineID})
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("POST /api/pipelines/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		pipelineID := strings.TrimSpace(r.PathValue("id"))
		p.Send(DashboardStopPipelineMsg{PipelineID: pipelineID})
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("PUT /api/pipelines/{id}", func(w http.ResponseWriter, r *http.Request) {
		pipelineID := strings.TrimSpace(r.PathValue("id"))
		var body struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Steps       []string `json:"steps"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p.Send(DashboardUpdatePipelineMsg{ID: pipelineID, Name: body.Name, Description: body.Description, Steps: body.Steps})
		w.WriteHeader(http.StatusAccepted)
	})

	mux.HandleFunc("DELETE /api/pipelines/{id}", func(w http.ResponseWriter, r *http.Request) {
		pipelineID := strings.TrimSpace(r.PathValue("id"))
		p.Send(DashboardDeletePipelineMsg{PipelineID: pipelineID})
		w.WriteHeader(http.StatusAccepted)
	})

	log.Printf("dashboard listening on %s", addr)
	if err := http.Serve(ln, mux); err != nil {
		log.Printf("dashboard server stopped: %v", err)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"json marshal failed"}`
	}
	return string(b)
}
