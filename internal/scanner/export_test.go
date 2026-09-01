package scanner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"domain_scanner/internal/stats"
	"domain_scanner/internal/types"
)

// TestResultExporterWritesBeyondResultLimit verifies that every result is
// appended to the on-disk CSV while the in-memory lists stay capped at
// resultLimit.
func TestResultExporterWritesBeyondResultLimit(t *testing.T) {
	dir := t.TempDir()
	id := "20250901-0001"

	scan := &Scan{
		id:          id,
		options:     Options{ShowRegistered: true},
		resultLimit: 2,
		collector:   stats.NewCollector(100, 1),
		exporter:    newResultExporter(dir, id),
	}

	for i := 0; i < 5; i++ {
		scan.recordResult(types.DomainResult{Domain: fmt.Sprintf("free%d.test", i), Available: true})
	}
	for i := 0; i < 3; i++ {
		scan.recordResult(types.DomainResult{Domain: fmt.Sprintf("reg%d.test", i), Signatures: []string{"parked", `say "hi"`}})
	}
	for i := 0; i < 2; i++ {
		scan.recordResult(types.DomainResult{Domain: fmt.Sprintf("err%d.test", i), Error: errors.New("timeout")})
	}
	scan.exporter.close()

	if len(scan.available) != 2 || len(scan.registered) != 2 || len(scan.errors) != 2 {
		t.Fatalf("in-memory lists not capped: available=%d registered=%d errors=%d, want 2/2/2",
			len(scan.available), len(scan.registered), len(scan.errors))
	}

	avail := readFile(t, filepath.Join(dir, "exports", id+"-available.csv"))
	if got, want := avail[:3], "\xEF\xBB\xBF"; got != want {
		t.Fatalf("available CSV missing UTF-8 BOM, got prefix %q", got)
	}
	if !strings.Contains(avail, `"域名","时间"`) {
		t.Fatalf("available CSV missing header, got %q", avail)
	}
	for i := 0; i < 5; i++ {
		if !strings.Contains(avail, fmt.Sprintf(`"free%d.test"`, i)) {
			t.Fatalf("available CSV missing row free%d.test, got %q", i, avail)
		}
	}

	reg := readFile(t, filepath.Join(dir, "exports", id+"-registered.csv"))
	if !strings.Contains(reg, `"域名","签名","时间"`) {
		t.Fatalf("registered CSV missing header, got %q", reg)
	}
	if !strings.Contains(reg, `"parked; say ""hi"""`) {
		t.Fatalf("registered CSV missing quoted signatures row, got %q", reg)
	}

	errs := readFile(t, filepath.Join(dir, "exports", id+"-errors.csv"))
	if !strings.Contains(errs, `"域名","错误","时间"`) || !strings.Contains(errs, `"timeout"`) {
		t.Fatalf("errors CSV missing header or error row, got %q", errs)
	}
}

// TestExportFilePath validates path construction, ID sanitization and tab
// checks in ExportFilePath.
func TestExportFilePath(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dataDir: dir}

	if path, ok := m.ExportFilePath("20250901-0001", TabAvailable); ok {
		t.Fatalf("expected no file, got %q", path)
	}

	created := filepath.Join(dir, "exports", "20250901-0001-available.csv")
	if err := os.MkdirAll(filepath.Dir(created), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, ok := m.ExportFilePath("20250901-0001", TabAvailable)
	if !ok || path != created {
		t.Fatalf("ExportFilePath = %q, %v; want %q, true", path, ok, created)
	}

	for _, id := range []string{"../evil", ".", "", "x/y"} {
		if path, ok := m.ExportFilePath(id, TabAvailable); ok {
			t.Fatalf("ExportFilePath(%q) accepted, got %q", id, path)
		}
	}
	if path, ok := m.ExportFilePath("20250901-0001", "bogus"); ok {
		t.Fatalf("ExportFilePath with bad tab accepted, got %q", path)
	}
	if path, ok := (&Manager{}).ExportFilePath("20250901-0001", TabAvailable); ok {
		t.Fatalf("ExportFilePath without dataDir accepted, got %q", path)
	}
}

// TestWriteSnapshotCSV checks the fallback format: BOM, header, quoting.
func TestWriteSnapshotCSV(t *testing.T) {
	snap := Snapshot{
		Available: []Result{{
			Domain:    `a"b.test`,
			CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		}},
	}

	var buf strings.Builder
	if err := WriteSnapshotCSV(&buf, snap, TabAvailable); err != nil {
		t.Fatalf("WriteSnapshotCSV: %v", err)
	}

	want := "\xEF\xBB\xBF" + `"域名","时间"` + "\n" + `"a""b.test","2026-01-02T03:04:05Z"` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteSnapshotCSV = %q, want %q", got, want)
	}
}

// TestShutdownPersistsRunningScan verifies that a scan still running when
// Manager.Shutdown is called is persisted as canceled and survives a
// "restart" (fresh Manager over the same data directory).
func TestShutdownPersistsRunningScan(t *testing.T) {
	dir := t.TempDir()

	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	scan, err := m.Start(Options{
		Suffix:      ".test",
		DictWords:   []string{"slowone", "slowtwo", "slowthree"},
		Workers:     1,
		DelayMS:     100,
		ResultLimit: 10,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The WHOIS-backed check takes far longer than this poll window.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if scan.Status() == StatusRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := scan.Status(); got != StatusRunning {
		t.Fatalf("scan status = %q before shutdown, want %q", got, StatusRunning)
	}

	m.Shutdown()

	persisted, ok := m.store.Get(scan.id)
	if !ok {
		t.Fatal("running scan was not persisted by Shutdown")
	}
	if persisted.Status != StatusCanceled {
		t.Fatalf("persisted status = %q, want %q", persisted.Status, StatusCanceled)
	}

	// Simulate a restart: a fresh manager over the same data directory must
	// see the scan in history.
	restarted, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager after restart: %v", err)
	}
	afterRestart, ok := restarted.Get(scan.id)
	if !ok {
		t.Fatal("scan missing from history after simulated restart")
	}
	if got := afterRestart.Status; got != StatusCanceled {
		t.Fatalf("status after restart = %q, want %q", got, StatusCanceled)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
