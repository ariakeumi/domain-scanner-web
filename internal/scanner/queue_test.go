package scanner

import (
	"testing"
	"time"
)

// TestGlobalScanLimitQueues verifies that when the global concurrent-scan cap is
// reached, new scans queue, can be canceled while queued, and config reports the
// right running/queued counts.
func TestGlobalScanLimitQueues(t *testing.T) {
	t.Setenv("MAX_CONCURRENT_SCANS", "1")
	t.Setenv("MAX_TOTAL_WORKERS", "2")

	m, err := NewManager("")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	opts := Options{
		Suffix:      ".test",
		DictWords:   []string{"alpha"},
		Workers:     1,
		ResultLimit: 10,
	}

	first, err := m.Start(opts)
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if got := first.Status(); got != StatusRunning {
		t.Fatalf("first scan status = %q, want %q", got, StatusRunning)
	}

	second, err := m.Start(opts)
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if got := second.Status(); got != StatusQueued {
		t.Fatalf("second scan status = %q, want %q", got, StatusQueued)
	}

	cfg := m.Config()
	if cfg.Running != 1 || cfg.Queued != 1 {
		t.Fatalf("config after queue = running %d queued %d, want running 1 queued 1", cfg.Running, cfg.Queued)
	}

	// Cancel the queued scan; it should become canceled without ever running.
	if ok := second.Cancel(); !ok {
		t.Fatal("Cancel on queued scan returned false")
	}
	if got := second.Status(); got != StatusCanceled {
		t.Fatalf("queued scan after cancel = %q, want %q", got, StatusCanceled)
	}

	// Still one running scan, so a third scan must queue too.
	third, err := m.Start(opts)
	if err != nil {
		t.Fatalf("Start third: %v", err)
	}
	if got := third.Status(); got != StatusQueued {
		t.Fatalf("third scan status = %q, want %q", got, StatusQueued)
	}

	cfg = m.Config()
	if cfg.Running != 1 || cfg.Queued != 1 {
		t.Fatalf("config after third = running %d queued %d, want running 1 queued 1", cfg.Running, cfg.Queued)
	}

	// Canceling the running scan frees the slot; the queued scan should start.
	if ok := first.Cancel(); !ok {
		t.Fatal("Cancel on running scan returned false")
	}

	// Wait briefly for the dispatcher to promote the queued scan.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if third.Status() == StatusRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("queued scan never promoted to %q (status = %q)", StatusRunning, third.Status())
}
