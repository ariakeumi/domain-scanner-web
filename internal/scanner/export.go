package scanner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Result tab names accepted by the CSV export endpoint.
const (
	TabAvailable  = "available"
	TabRegistered = "registered"
	TabErrors     = "errors"
)

// utf8BOM is prepended to CSV output so Excel detects UTF-8.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// scanIDPattern restricts scan IDs used in export file names to the shape
// produced by nextID, keeping requested paths inside the exports directory.
var scanIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{4,}$`)

// resultExporter appends every scan result to per-tab CSV files under
// <dataDir>/exports as results arrive, so exports are not limited by the
// in-memory resultLimit. Files are opened lazily on the first row and stay
// open until the scan finishes. Write failures disable the exporter for the
// rest of the scan; scanning itself is never interrupted by export errors.
type resultExporter struct {
	dir    string
	scanID string

	mu     sync.Mutex
	files  map[string]*os.File
	failed bool
}

func newResultExporter(dataDir, scanID string) *resultExporter {
	return &resultExporter{
		dir:    filepath.Join(dataDir, "exports"),
		scanID: scanID,
		files:  make(map[string]*os.File),
	}
}

// append writes one row. It is nil-safe (persistence disabled).
func (e *resultExporter) append(tab string, result Result) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	f, ok := e.files[tab]
	if !ok {
		if e.failed {
			return
		}
		f = e.openLocked(tab)
		if f == nil {
			return
		}
		e.files[tab] = f
	}
	if _, err := f.WriteString(csvLine(exportFields(tab, result)...)); err != nil {
		e.disableLocked(tab, err)
	}
}

// openLocked opens (creating if needed) the CSV file for tab, writing the
// BOM and header row only when the file is new or empty.
func (e *resultExporter) openLocked(tab string) *os.File {
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		e.failLocked(err)
		return nil
	}
	f, err := os.OpenFile(e.filePath(tab), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		e.failLocked(err)
		return nil
	}
	if info, err := f.Stat(); err == nil && info.Size() == 0 {
		_, _ = f.Write(utf8BOM)
		_, _ = f.WriteString(csvLine(exportHeader(tab)...))
	}
	return f
}

// failLocked logs once and disables all further export writes for this scan.
func (e *resultExporter) failLocked(err error) {
	e.failed = true
	fmt.Printf("warning: CSV export disabled for scan %s: %v\n", e.scanID, err)
}

// disableLocked stops exporting after a mid-scan write error.
func (e *resultExporter) disableLocked(tab string, err error) {
	fmt.Printf("warning: CSV export writes disabled for scan %s (tab %s): %v\n", e.scanID, tab, err)
	for name, f := range e.files {
		_ = f.Close()
		delete(e.files, name)
	}
	e.failed = true
}

// close releases all open file handles. It is nil-safe.
func (e *resultExporter) close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range e.files {
		_ = f.Close()
	}
	e.files = nil
}

func (e *resultExporter) filePath(tab string) string {
	return filepath.Join(e.dir, fmt.Sprintf("%s-%s.csv", e.scanID, tab))
}

// ExportFilePath returns the on-disk CSV file for a scan tab if one exists.
func (m *Manager) ExportFilePath(id, tab string) (string, bool) {
	if m.dataDir == "" || !scanIDPattern.MatchString(id) {
		return "", false
	}
	switch tab {
	case TabAvailable, TabRegistered, TabErrors:
	default:
		return "", false
	}
	path := filepath.Join(m.dataDir, "exports", fmt.Sprintf("%s-%s.csv", id, tab))
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// WriteSnapshotCSV writes the tab's rows from a snapshot as CSV. It backs the
// export endpoint for scans recorded before export files existed (or when
// persistence is disabled); such exports are limited to the snapshot's rows.
func WriteSnapshotCSV(w io.Writer, snap Snapshot, tab string) error {
	var rows []Result
	switch tab {
	case TabRegistered:
		rows = snap.Registered
	case TabErrors:
		rows = snap.Errors
	default:
		tab = TabAvailable
		rows = snap.Available
	}

	bw := bufio.NewWriter(w)
	if _, err := bw.Write(utf8BOM); err != nil {
		return err
	}
	if _, err := bw.WriteString(csvLine(exportHeader(tab)...)); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := bw.WriteString(csvLine(exportFields(tab, row)...)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// exportHeader returns the CSV header row for a result tab, mirroring the
// web UI columns.
func exportHeader(tab string) []string {
	switch tab {
	case TabRegistered:
		return []string{"域名", "签名", "时间"}
	case TabErrors:
		return []string{"域名", "错误", "时间"}
	default:
		return []string{"域名", "时间"}
	}
}

// exportFields returns the CSV row for one result.
func exportFields(tab string, result Result) []string {
	checkedAt := result.CheckedAt.Format(time.RFC3339)
	switch tab {
	case TabRegistered:
		return []string{result.Domain, strings.Join(result.Signatures, "; "), checkedAt}
	case TabErrors:
		return []string{result.Domain, result.Error, checkedAt}
	default:
		return []string{result.Domain, checkedAt}
	}
}

// csvLine formats one fully quoted, comma-separated CSV line.
func csvLine(fields ...string) string {
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(field, `"`, `""`))
		b.WriteByte('"')
	}
	b.WriteByte('\n')
	return b.String()
}
