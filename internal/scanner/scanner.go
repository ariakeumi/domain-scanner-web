package scanner

import (
	"context"
	"fmt"
	"sort"
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

// Manager tracks in-memory scan jobs.
type Manager struct {
	mu    sync.RWMutex
	scans map[string]*Scan
	seq   uint64
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

	registeredCount int64
	errorCount      int64
	available       []Result
	registered      []Result
	errors          []Result
}

// NewManager creates an empty scan manager.
func NewManager() *Manager {
	return &Manager{
		scans: make(map[string]*Scan),
	}
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
		status:      StatusRunning,
		cancel:      cancel,
		collector:   stats.NewCollector(int64(domainGen.TotalCount), options.Workers),
		generated:   domainGen.Generated,
		startedAt:   time.Now(),
		resultLimit: options.ResultLimit,
		available:   make([]Result, 0),
		registered:  make([]Result, 0),
		errors:      make([]Result, 0),
	}

	m.mu.Lock()
	m.scans[id] = scan
	m.mu.Unlock()

	go scan.run(ctx, domainGen)

	return scan, nil
}

// Get returns a scan by ID.
func (m *Manager) Get(id string) (*Scan, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scan, ok := m.scans[id]
	return scan, ok
}

// Cancel cancels a running scan.
func (m *Manager) Cancel(id string) bool {
	scan, ok := m.Get(id)
	if !ok {
		return false
	}
	return scan.Cancel()
}

// List returns scan snapshots ordered newest first.
func (m *Manager) List() []Snapshot {
	m.mu.RLock()
	scans := make([]*Scan, 0, len(m.scans))
	for _, scan := range m.scans {
		scans = append(scans, scan)
	}
	m.mu.RUnlock()

	sort.Slice(scans, func(i, j int) bool {
		return scans[i].startedAt.After(scans[j].startedAt)
	})

	snapshots := make([]Snapshot, 0, len(scans))
	for _, scan := range scans {
		snapshots = append(snapshots, scan.Snapshot())
	}
	return snapshots
}

func (m *Manager) nextID() string {
	seq := atomic.AddUint64(&m.seq, 1)
	return fmt.Sprintf("%s-%04d", time.Now().Format("20060102"), seq)
}

func (s *Scan) run(ctx context.Context, domainGen *generator.DomainGenerator) {
	jobs := make(chan string, 1000)
	results := make(chan types.DomainResult, 1000)

	var workerWg sync.WaitGroup
	for w := 1; w <= s.options.Workers; w++ {
		workerWg.Add(1)
		go func(id int) {
			defer workerWg.Done()
			worker.WorkerWithContext(ctx, id, jobs, results, time.Duration(s.options.DelayMS)*time.Millisecond, s.collector)
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

	s.finish(ctx.Err())
}

// Cancel asks the scan to stop.
func (s *Scan) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != StatusRunning {
		return false
	}
	s.status = StatusCanceling
	s.cancel()
	return true
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
		if processed > 0 || s.collector.GetTotalDomains() > 0 {
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
		s.mu.Lock()
		s.errorCount++
		s.appendResultLocked(&s.errors, Result{
			Domain:    result.Domain,
			Available: result.Available,
			Error:     result.Error.Error(),
			CheckedAt: checkedAt,
		})
		s.mu.Unlock()
		return
	}

	if result.Available {
		s.collector.IncrementAvailable()
		s.mu.Lock()
		s.appendResultLocked(&s.available, Result{
			Domain:    result.Domain,
			Available: true,
			CheckedAt: checkedAt,
		})
		s.mu.Unlock()
		return
	}

	if s.options.ShowRegistered {
		s.mu.Lock()
		s.registeredCount++
		s.appendResultLocked(&s.registered, Result{
			Domain:     result.Domain,
			Available:  false,
			Signatures: result.Signatures,
			CheckedAt:  checkedAt,
		})
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
