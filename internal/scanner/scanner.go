package scanner

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"domain_scanner/internal/generator"
	"domain_scanner/internal/stats"
	"domain_scanner/internal/types"
	"domain_scanner/internal/worker"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCanceling = "canceling"
	StatusCanceled  = "canceled"
	StatusCompleted = "completed"
	StatusFailed    = "failed"

	defaultLength      = 3
	defaultPattern     = "D"
	defaultSuffix      = ".li"
	defaultWorkers     = 10
	defaultResultLimit = 5000
	maxResultLimit     = 20000
	maxWorkers         = 100
	maxDelayMS         = 60000
	maxWebLength       = 8
	largeScanThreshold = 1000000
)

// Options describes a domain scan request.
type Options struct {
	Length         int      `json:"length"`
	Suffix         string   `json:"suffix"`
	Pattern        string   `json:"pattern"`
	RegexFilter    string   `json:"regexFilter"`
	DictFile       string   `json:"dictFile,omitempty"`
	DictWords      []string `json:"dictWords,omitempty"`
	DelayMS        int      `json:"delayMs"`
	Workers        int      `json:"workers"`
	ShowRegistered bool     `json:"showRegistered"`
	Force          bool     `json:"force"`
	ResultLimit    int      `json:"resultLimit"`
}

// Result is a JSON-friendly scan result.
type Result struct {
	Domain     string    `json:"domain"`
	Available  bool      `json:"available"`
	Signatures []string  `json:"signatures,omitempty"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
}

// Snapshot is a point-in-time view of a scan.
type Snapshot struct {
	ID                  string     `json:"id"`
	Status              string     `json:"status"`
	Error               string     `json:"error,omitempty"`
	Options             Options    `json:"options"`
	Total               int64      `json:"total"`
	Generated           int64      `json:"generated"`
	Processed           int64      `json:"processed"`
	AvailableCount      int64      `json:"availableCount"`
	RegisteredCount     int64      `json:"registeredCount"`
	ErrorCount          int64      `json:"errorCount"`
	ActiveWorkers       int64      `json:"activeWorkers"`
	QPS                 float64    `json:"qps"`
	Progress            float64    `json:"progress"`
	ETASeconds          int64      `json:"etaSeconds"`
	StartedAt           time.Time  `json:"startedAt"`
	EndedAt             *time.Time `json:"endedAt,omitempty"`
	ElapsedSeconds      int64      `json:"elapsedSeconds"`
	ResultLimit         int        `json:"resultLimit"`
	AvailableTruncated  bool       `json:"availableTruncated"`
	RegisteredTruncated bool       `json:"registeredTruncated"`
	ErrorsTruncated     bool       `json:"errorsTruncated"`
	Available           []Result   `json:"available"`
	Registered          []Result   `json:"registered"`
	Errors              []Result   `json:"errors"`
}

// Config describes global concurrency limits and current load.
type Config struct {
	MaxConcurrentScans int `json:"maxConcurrentScans"`
	MaxTotalWorkers    int `json:"maxTotalWorkers"`
	Running            int `json:"running"`
	Queued             int `json:"queued"`
}

// pendingScan is a scan waiting for a free global scan slot.
type pendingScan struct {
	scan      *Scan
	ctx       context.Context
	domainGen *generator.DomainGenerator
}

// Manager tracks scan jobs, optionally persisting finished scans to disk.
// It applies a global cap on concurrently running scans (queued jobs wait for a
// free slot) and a global cap on total active workers across all scans.
type Manager struct {
	mu                 sync.RWMutex
	scans              map[string]*Scan
	seq                uint64
	store              *Store
	dataDir            string
	maxConcurrentScans int
	maxTotalWorkers    int
	scanSlots          chan struct{}
	workerSlots        chan struct{}
	pending            chan pendingScan
}

// Scan is a running or finished scan job.
type Scan struct {
	mu           sync.RWMutex
	id           string
	options      Options
	status       string
	errorMessage string
	cancel       context.CancelFunc
	collector    *stats.Collector
	generated    *int64
	startedAt    time.Time
	endedAt      *time.Time
	resultLimit  int
	workerSlots  chan struct{}

	registeredCount int64
	errorCount      int64
	available       []Result
	registered      []Result
	errors          []Result
	exporter        *resultExporter
}

// NewManager creates a scan manager. If dataDir is non-empty, finished scans
// are persisted to <dataDir>/scans.json and loaded back on startup.
func NewManager(dataDir string) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}

	maxScans := envInt("MAX_CONCURRENT_SCANS", 3)
	if maxScans < 1 {
		maxScans = 1
	}
	if maxScans > 20 {
		maxScans = 20
	}
	maxTotalWorkers := envInt("MAX_TOTAL_WORKERS", 100)
	if maxTotalWorkers < 1 {
		maxTotalWorkers = 1
	}
	if maxTotalWorkers > 1000 {
		maxTotalWorkers = 1000
	}

	m := &Manager{
		scans:              make(map[string]*Scan),
		store:              store,
		dataDir:            dataDir,
		maxConcurrentScans: maxScans,
		maxTotalWorkers:    maxTotalWorkers,
		scanSlots:          make(chan struct{}, maxScans),
		workerSlots:        make(chan struct{}, maxTotalWorkers),
		pending:            make(chan pendingScan, 256),
	}
	if store != nil {
		m.seq = m.maxSeqForDate(time.Now().Format("20060102"))
	}
	go m.dispatcher()
	return m, nil
}

// envInt reads an integer environment variable, falling back to def.
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// dispatcher starts queued scans as global scan slots free up.
func (m *Manager) dispatcher() {
	for item := range m.pending {
		select {
		case m.scanSlots <- struct{}{}:
		case <-item.ctx.Done():
			m.finishQueued(item)
			continue
		}

		item.scan.mu.Lock()
		if item.scan.status != StatusQueued {
			// Canceled while queued (or otherwise no longer queued).
			item.scan.mu.Unlock()
			<-m.scanSlots
			m.finishQueued(item)
			continue
		}
		item.scan.status = StatusRunning
		item.scan.startedAt = time.Now()
		item.scan.mu.Unlock()

		go func(it pendingScan) {
			defer func() { <-m.scanSlots }()
			it.scan.run(it.ctx, it.domainGen)
			m.persist(it.scan)
		}(item)
	}
}

// finishQueued persists a queued scan that never ran (e.g. canceled while queued).
func (m *Manager) finishQueued(item pendingScan) {
	item.scan.mu.Lock()
	if item.scan.status == StatusQueued {
		item.scan.status = StatusCanceled
		now := time.Now()
		item.scan.endedAt = &now
	}
	item.scan.mu.Unlock()
	m.persist(item.scan)
}

// Start validates options and starts a new scan.
func (m *Manager) Start(options Options) (*Scan, error) {
	options, estimated, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}

	if estimated > largeScanThreshold && !options.Force {
		return nil, fmt.Errorf("scan contains an estimated %d domains; enable force to continue", estimated)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var domainGen *generator.DomainGenerator
	if len(options.DictWords) > 0 {
		domainGen, err = generator.GenerateDomainsFromWordsWithContext(ctx, options.DictWords, options.Suffix, options.RegexFilter)
	} else {
		domainGen, err = generator.GenerateDomainsWithContext(ctx, options.Length, options.Suffix, options.Pattern, options.RegexFilter, options.DictFile)
	}
	if err != nil {
		cancel()
		return nil, err
	}

	id := m.nextID()
	scan := &Scan{
		id:          id,
		options:     options,
		status:      StatusQueued,
		cancel:      cancel,
		collector:   stats.NewCollector(int64(domainGen.TotalCount), options.Workers),
		generated:   domainGen.Generated,
		startedAt:   time.Now(),
		resultLimit: options.ResultLimit,
		workerSlots: m.workerSlots,
		available:   make([]Result, 0),
		registered:  make([]Result, 0),
		errors:      make([]Result, 0),
	}
	if m.dataDir != "" {
		scan.exporter = newResultExporter(m.dataDir, id)
	}

	m.mu.Lock()
	m.scans[id] = scan
	m.mu.Unlock()

	// Start immediately if a global scan slot is free, otherwise queue.
	select {
	case m.scanSlots <- struct{}{}:
		scan.mu.Lock()
		scan.status = StatusRunning
		scan.mu.Unlock()
		go func() {
			defer func() { <-m.scanSlots }()
			scan.run(ctx, domainGen)
			m.persist(scan)
		}()
	default:
		m.pending <- pendingScan{scan: scan, ctx: ctx, domainGen: domainGen}
	}

	return scan, nil
}

// Get returns a snapshot for a scan ID, checking running scans first and then
// persisted history.
func (m *Manager) Get(id string) (Snapshot, bool) {
	if scan, ok := m.getScan(id); ok {
		return scan.Snapshot(), true
	}
	if m.store != nil {
		if snap, ok := m.store.Get(id); ok {
			return snap, true
		}
	}
	return Snapshot{}, false
}

func (m *Manager) getScan(id string) (*Scan, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scan, ok := m.scans[id]
	return scan, ok
}

// Cancel cancels a running scan.
func (m *Manager) Cancel(id string) bool {
	scan, ok := m.getScan(id)
	if !ok {
		return false
	}
	return scan.Cancel()
}

// List returns scan snapshots ordered newest first, merging persisted history
// with in-memory scans (in-memory scans win on ID collisions).
func (m *Manager) List() []Snapshot {
	m.mu.RLock()
	scans := make([]*Scan, 0, len(m.scans))
	for _, scan := range m.scans {
		scans = append(scans, scan)
	}
	m.mu.RUnlock()

	byID := make(map[string]Snapshot)
	if m.store != nil {
		for _, snap := range m.store.List() {
			byID[snap.ID] = snap
		}
	}
	for _, scan := range scans {
		byID[scan.id] = scan.Snapshot()
	}

	list := make([]Snapshot, 0, len(byID))
	for _, snap := range byID {
		list = append(list, snap)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].StartedAt.After(list[j].StartedAt)
	})
	return list
}

// ListSummary returns scan snapshots without result payloads, for cheap
// list rendering in the web UI.
func (m *Manager) ListSummary() []Snapshot {
	snapshots := m.List()
	for i := range snapshots {
		snapshots[i].Available = nil
		snapshots[i].Registered = nil
		snapshots[i].Errors = nil
	}
	return snapshots
}

// Config returns global concurrency limits and current load.
func (m *Manager) Config() Config {
	m.mu.RLock()
	var running, queued int
	for _, s := range m.scans {
		switch s.Status() {
		case StatusRunning, StatusCanceling:
			running++
		case StatusQueued:
			queued++
		}
	}
	m.mu.RUnlock()
	return Config{
		MaxConcurrentScans: m.maxConcurrentScans,
		MaxTotalWorkers:    m.maxTotalWorkers,
		Running:            running,
		Queued:             queued,
	}
}

func (m *Manager) nextID() string {
	seq := atomic.AddUint64(&m.seq, 1)
	return fmt.Sprintf("%s-%04d", time.Now().Format("20060102"), seq)
}

// persist saves a finished scan to the store.
func (m *Manager) persist(scan *Scan) {
	if m.store == nil {
		return
	}
	snapshot := scan.Snapshot()
	switch snapshot.Status {
	case StatusCompleted, StatusCanceled, StatusFailed:
		if err := m.store.Upsert(snapshot); err != nil {
			fmt.Printf("warning: failed to persist scan %s: %v\n", snapshot.ID, err)
		}
	}
}

// maxSeqForDate returns the highest trailing sequence number among persisted
// scans whose ID starts with the given date, so new IDs don't collide.
func (m *Manager) maxSeqForDate(date string) uint64 {
	var max uint64
	if m.store == nil {
		return max
	}
	for _, snap := range m.store.List() {
		rest, ok := strings.CutPrefix(snap.ID, date+"-")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(rest, 10, 64); err == nil && n > max {
			max = n
		}
	}
	return max
}

// Shutdown cancels every queued or running scan and persists its final
// state, so in-flight scans are not silently lost when the process stops
// (docker stop / container restart sends SIGTERM). Finished scans were
// already persisted when they completed.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	scans := make([]*Scan, 0, len(m.scans))
	for _, s := range m.scans {
		scans = append(scans, s)
	}
	m.mu.RUnlock()

	for _, s := range scans {
		if s.abort() {
			m.persist(s)
		}
	}
}

// abort finalizes a queued or running scan as canceled without waiting for
// its goroutines to wind down, returning true when the scan was
// non-terminal and now needs persisting.
func (s *Scan) abort() bool {
	s.mu.Lock()
	var cancel context.CancelFunc
	switch s.status {
	case StatusQueued, StatusRunning, StatusCanceling:
		now := time.Now()
		s.endedAt = &now
		s.status = StatusCanceled
		cancel = s.cancel
		s.mu.Unlock()
		cancel()
		return true
	default:
		s.mu.Unlock()
		return false
	}
}

func (s *Scan) run(ctx context.Context, domainGen *generator.DomainGenerator) {
	jobs := make(chan string, 1000)
	results := make(chan types.DomainResult, 1000)

	var workerWg sync.WaitGroup
	for w := 1; w <= s.options.Workers; w++ {
		workerWg.Add(1)
		go func(id int) {
			defer workerWg.Done()
			worker.WorkerWithContext(ctx, id, jobs, results, time.Duration(s.options.DelayMS)*time.Millisecond, s.collector, s.workerSlots)
		}(w)
	}

	go func() {
		defer close(jobs)
		for domain := range domainGen.Domains {
			select {
			case <-ctx.Done():
				return
			case jobs <- domain:
			}
		}
	}()

	go func() {
		workerWg.Wait()
		close(results)
	}()

	for result := range results {
		s.recordResult(result)
	}

	s.exporter.close()
	s.finish(ctx.Err())
}

// Cancel asks the scan to stop.
func (s *Scan) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.status {
	case StatusRunning:
		s.status = StatusCanceling
		s.cancel()
		return true
	case StatusQueued:
		s.status = StatusCanceled
		now := time.Now()
		s.endedAt = &now
		s.cancel()
		return true
	default:
		return false
	}
}

// Status returns the scan's current status.
func (s *Scan) Status() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// Snapshot returns a JSON-safe copy of the scan state.
func (s *Scan) Snapshot() Snapshot {
	s.mu.RLock()
	status := s.status
	errorMessage := s.errorMessage
	options := s.options
	startedAt := s.startedAt
	endedAt := s.endedAt
	resultLimit := s.resultLimit
	registeredCount := s.registeredCount
	errorCount := s.errorCount
	available := append([]Result(nil), s.available...)
	registered := append([]Result(nil), s.registered...)
	errors := append([]Result(nil), s.errors...)
	s.mu.RUnlock()

	now := time.Now()
	if endedAt != nil {
		now = *endedAt
	}

	processed := s.collector.GetProcessedCount()
	availableCount := s.collector.GetAvailableCount()
	progress := s.collector.GetProgress()
	etaSeconds := int64(s.collector.CalculateETA().Seconds())
	if status == StatusCompleted || status == StatusCanceled || status == StatusFailed {
		etaSeconds = 0
		// Only report 100% for terminal scans that actually did work; a scan
		// canceled while queued never ran and should stay at 0%.
		if processed > 0 {
			progress = 100
		}
	}

	return Snapshot{
		ID:                  s.id,
		Status:              status,
		Error:               errorMessage,
		Options:             options,
		Total:               s.collector.GetTotalDomains(),
		Generated:           atomic.LoadInt64(s.generated),
		Processed:           processed,
		AvailableCount:      availableCount,
		RegisteredCount:     registeredCount,
		ErrorCount:          errorCount,
		ActiveWorkers:       s.collector.GetActiveWorkers(),
		QPS:                 s.collector.CalculateQPS(),
		Progress:            progress,
		ETASeconds:          etaSeconds,
		StartedAt:           startedAt,
		EndedAt:             endedAt,
		ElapsedSeconds:      int64(now.Sub(startedAt).Seconds()),
		ResultLimit:         resultLimit,
		AvailableTruncated:  availableCount > int64(len(available)),
		RegisteredTruncated: registeredCount > int64(len(registered)),
		ErrorsTruncated:     errorCount > int64(len(errors)),
		Available:           available,
		Registered:          registered,
		Errors:              errors,
	}
}

func (s *Scan) recordResult(result types.DomainResult) {
	s.collector.IncrementProcessed()

	checkedAt := time.Now()
	if result.Error != nil {
		row := Result{
			Domain:    result.Domain,
			Available: result.Available,
			Error:     result.Error.Error(),
			CheckedAt: checkedAt,
		}
		s.exporter.append(TabErrors, row)
		s.mu.Lock()
		s.errorCount++
		s.appendResultLocked(&s.errors, row)
		s.mu.Unlock()
		return
	}

	if result.Available {
		s.collector.IncrementAvailable()
		row := Result{
			Domain:    result.Domain,
			Available: true,
			CheckedAt: checkedAt,
		}
		s.exporter.append(TabAvailable, row)
		s.mu.Lock()
		s.appendResultLocked(&s.available, row)
		s.mu.Unlock()
		return
	}

	if s.options.ShowRegistered {
		row := Result{
			Domain:     result.Domain,
			Available:  false,
			Signatures: result.Signatures,
			CheckedAt:  checkedAt,
		}
		s.exporter.append(TabRegistered, row)
		s.mu.Lock()
		s.registeredCount++
		s.appendResultLocked(&s.registered, row)
		s.mu.Unlock()
	}
}

func (s *Scan) appendResultLocked(results *[]Result, result Result) {
	if s.resultLimit <= 0 || len(*results) >= s.resultLimit {
		return
	}
	*results = append(*results, result)
}

func (s *Scan) finish(ctxErr error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.endedAt = &now
	if ctxErr != nil || s.status == StatusCanceling {
		s.status = StatusCanceled
		return
	}
	if s.status != StatusFailed {
		s.status = StatusCompleted
	}
}

func normalizeOptions(options Options) (Options, int, error) {
	if options.Length == 0 {
		options.Length = defaultLength
	}
	if options.Pattern == "" {
		options.Pattern = defaultPattern
	}
	if options.Suffix == "" {
		options.Suffix = defaultSuffix
	}
	if options.Workers == 0 {
		options.Workers = defaultWorkers
	}
	if options.ResultLimit == 0 {
		options.ResultLimit = defaultResultLimit
	}

	options.Suffix = strings.TrimSpace(options.Suffix)
	if !strings.HasPrefix(options.Suffix, ".") {
		options.Suffix = "." + options.Suffix
	}
	options.RegexFilter = strings.TrimSpace(options.RegexFilter)
	options.DictFile = strings.TrimSpace(options.DictFile)
	options.DictWords = cleanDictionaryWords(options.DictWords)

	if options.Workers < 1 || options.Workers > maxWorkers {
		return options, 0, fmt.Errorf("workers must be between 1 and %d", maxWorkers)
	}
	if options.DelayMS < 0 || options.DelayMS > maxDelayMS {
		return options, 0, fmt.Errorf("delay must be between 0 and %d ms", maxDelayMS)
	}
	if options.ResultLimit < 1 || options.ResultLimit > maxResultLimit {
		return options, 0, fmt.Errorf("result limit must be between 1 and %d", maxResultLimit)
	}

	if len(options.DictWords) > 0 || options.DictFile != "" {
		estimated := len(options.DictWords)
		if options.DictFile != "" {
			estimated = 1
		}
		return options, estimated, nil
	}

	if options.Length < 1 || options.Length > maxWebLength {
		return options, 0, fmt.Errorf("domain length must be between 1 and %d", maxWebLength)
	}

	charsetSize := 0
	switch options.Pattern {
	case "d":
		charsetSize = 10
	case "D":
		charsetSize = 26
	case "a":
		charsetSize = 36
	default:
		return options, 0, fmt.Errorf("invalid pattern %q, use d, D, or a", options.Pattern)
	}

	estimated := 1
	for i := 0; i < options.Length; i++ {
		estimated *= charsetSize
	}

	return options, estimated, nil
}

func cleanDictionaryWords(words []string) []string {
	cleaned := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" || strings.Contains(word, " ") {
			continue
		}
		cleaned = append(cleaned, word)
	}
	return cleaned
}
