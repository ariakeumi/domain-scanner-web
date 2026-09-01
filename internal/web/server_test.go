package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"domain_scanner/internal/scanner"
)

// TestExportEndpointValidation checks routing and parameter validation of
// GET /api/scans/{id}/export.
func TestExportEndpointValidation(t *testing.T) {
	server, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Unknown scan with a valid tab -> 404 JSON error.
	resp, err := http.Get(ts.URL + "/api/scans/20250101-9999/export?tab=available")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown scan export status = %d, want 404", resp.StatusCode)
	}
	var body errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if body.Error == "" {
		t.Fatal("404 body missing error message")
	}

	// Invalid tab -> 400.
	resp2, err := http.Get(ts.URL + "/api/scans/20250101-9999/export?tab=bogus")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad tab status = %d, want 400", resp2.StatusCode)
	}

	// Non-GET -> 405.
	resp3, err := http.Post(ts.URL+"/api/scans/20250101-9999/export?tab=available", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST export status = %d, want 405", resp3.StatusCode)
	}
}

// TestExportFallbackFromSnapshot seeds scans.json with a finished scan and
// verifies the export endpoint serves its rows as CSV even without export
// files on disk.
func TestExportFallbackFromSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	seed := []scanner.Snapshot{{
		ID:      "20250101-0001",
		Status:  "completed",
		Options: scanner.Options{ResultLimit: 5000},
		Available: []scanner.Result{
			{Domain: "seed1.test", Available: true},
			{Domain: "seed2.test", Available: true},
		},
	}}
	b, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "scans.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(dataDir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scans/20250101-0001/export?tab=available")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/csv; charset=utf-8", got)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="domain-scanner-20250101-0001-available.csv"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	csvText := string(body)
	for _, want := range []string{`"域名","时间"`, `"seed1.test"`, `"seed2.test"`} {
		if !strings.Contains(csvText, want) {
			t.Fatalf("export CSV missing %s, got %q", want, csvText)
		}
	}
}
