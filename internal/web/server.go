package web

import (
	"bufio"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
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

// NewServer creates a web server with an in-memory scan manager.
func NewServer() (*Server, error) {
	staticFS, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		return nil, err
	}

	return &Server{
		manager: scanner.NewManager(),
		static:  http.FileServer(http.FS(staticFS)),
	}, nil
}

// ListenAndServe starts the web UI on addr.
func ListenAndServe(addr string) error {
	server, err := NewServer()
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return httpServer.ListenAndServe()
}

// Handler returns the HTTP handler for the web UI and API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/scans", s.handleScans)
	mux.HandleFunc("/api/scans/", s.handleScan)
	mux.Handle("/", s.static)
	return mux
}

func (s *Server) handleScans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.manager.List())
	case http.MethodPost:
		s.startScan(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
		scan, ok := s.manager.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "scan not found")
			return
		}
		writeJSON(w, http.StatusOK, scan.Snapshot())
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
		scan, _ := s.manager.Get(id)
		writeJSON(w, http.StatusOK, scan.Snapshot())
		return
	}

	writeError(w, http.StatusNotFound, "scan not found")
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
