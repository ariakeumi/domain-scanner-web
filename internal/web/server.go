package web

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"domain_scanner/internal/scanner"
)

//go:embed static/*
var embeddedStatic embed.FS

// Server exposes the domain scanner web UI and JSON API.
type Server struct {
	manager *scanner.Manager
	static  http.Handler
}

type startScanRequest struct {
	Length         int    `json:"length"`
	Suffix         string `json:"suffix"`
	Pattern        string `json:"pattern"`
	RegexFilter    string `json:"regexFilter"`
	Dictionary     string `json:"dictionary"`
	DelayMS        int    `json:"delayMs"`
	Workers        int    `json:"workers"`
	ShowRegistered bool   `json:"showRegistered"`
	Force          bool   `json:"force"`
	ResultLimit    int    `json:"resultLimit"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// NewServer creates a web server. If dataDir is non-empty, finished scan
// history is persisted to <dataDir>/scans.json.
func NewServer(dataDir string) (*Server, error) {
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		return nil, err
	}

	manager, err := scanner.NewManager(dataDir)
	if err != nil {
		return nil, err
	}

	return &Server{
		manager: manager,
		static:  http.FileServer(http.FS(staticFS)),
	}, nil
}

// ListenAndServe starts the web UI on addr, persisting scan history to dataDir
// (pass "" to disable persistence). On SIGINT/SIGTERM it cancels in-flight
// scans, flushes their state to disk, and returns nil.
func ListenAndServe(addr, dataDir string) error {
	server, err := NewServer(dataDir)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		return err
	case sig := <-sigCh:
		fmt.Printf("received %v, persisting scan history before exit\n", sig)
		server.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return nil
	}
}

// Handler returns the HTTP handler for the web UI and API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/scans", s.handleScans)
	mux.HandleFunc("/api/scans/", s.handleScan)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleScans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("summary") == "1" {
			writeJSON(w, http.StatusOK, s.manager.ListSummary())
			return
		}
		writeJSON(w, http.StatusOK, s.manager.List())
	case http.MethodPost:
		s.startScan(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, s.manager.Config())
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var request startScanRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "request body is required")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	options := scanner.Options{
		Length:         request.Length,
		Suffix:         request.Suffix,
		Pattern:        request.Pattern,
		RegexFilter:    request.RegexFilter,
		DictWords:      splitDictionary(request.Dictionary),
		DelayMS:        request.DelayMS,
		Workers:        request.Workers,
		ShowRegistered: request.ShowRegistered,
		Force:          request.Force,
		ResultLimit:    request.ResultLimit,
	}

	scan, err := s.manager.Start(options)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, scan.Snapshot())
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/scans/"), "/")
	if path == "" {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		snap, ok := s.manager.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "scan not found")
			return
		}
		writeJSON(w, http.StatusOK, snap)
		return
	}

	if len(parts) == 2 && parts[1] == "export" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleExport(w, r, id)
		return
	}

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if ok := s.manager.Cancel(id); !ok {
			writeError(w, http.StatusNotFound, "running scan not found")
			return
		}
		snap, _ := s.manager.Get(id)
		writeJSON(w, http.StatusOK, snap)
		return
	}

	writeError(w, http.StatusNotFound, "scan not found")
}

// Shutdown cancels in-flight scans and persists their final state to disk.
func (s *Server) Shutdown() {
	s.manager.Shutdown()
}

// handleExport streams a scan's results as a CSV download. Results recorded
// while the exporter was active are streamed straight from the on-disk CSV
// file (no resultLimit cap); otherwise the snapshot's rows are written as a
// fallback, so older scans still export.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, id string) {
	tab := r.URL.Query().Get("tab")
	switch tab {
	case scanner.TabAvailable, scanner.TabRegistered, scanner.TabErrors:
	default:
		writeError(w, http.StatusBadRequest, "invalid tab, use available, registered, or errors")
		return
	}

	snap, ok := s.manager.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}

	filename := fmt.Sprintf("domain-scanner-%s-%s.csv", id, tab)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if path, ok := s.manager.ExportFilePath(id, tab); ok {
		f, err := os.Open(path)
		if err != nil {
			// Header is already sent; fall back to the snapshot inline.
			snap, _ = s.manager.Get(id)
			_ = scanner.WriteSnapshotCSV(w, snap, tab)
			return
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
		return
	}

	_ = scanner.WriteSnapshotCSV(w, snap, tab)
}

func splitDictionary(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	var words []string
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word != "" {
			words = append(words, word)
		}
	}
	return words
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
