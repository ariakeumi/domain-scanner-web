package scanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store persists finished scan snapshots to a JSON file.
type Store struct {
	mu   sync.Mutex
	path string
	data map[string]Snapshot
}

// NewStore creates a store backed by <dataDir>/scans.json. If dataDir is
// empty, it returns nil, nil (persistence disabled).
func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, nil
	}
	path := filepath.Join(dataDir, "scans.json")
	s := &Store{path: path, data: make(map[string]Snapshot)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []Snapshot
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	for _, snap := range list {
		s.data[snap.ID] = snap
	}
	return nil
}

// Upsert stores or replaces a snapshot, then rewrites the file atomically.
func (s *Store) Upsert(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[snapshot.ID] = snapshot
	return s.writeLocked()
}

func (s *Store) writeLocked() error {
	list := make([]Snapshot, 0, len(s.data))
	for _, snap := range s.data {
		list = append(list, snap)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns all persisted snapshots.
func (s *Store) List() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Snapshot, 0, len(s.data))
	for _, snap := range s.data {
		list = append(list, snap)
	}
	return list
}

// Get returns a persisted snapshot by ID.
func (s *Store) Get(id string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.data[id]
	return snap, ok
}
