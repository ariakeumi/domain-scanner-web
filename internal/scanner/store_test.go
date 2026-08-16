package scanner

import (
	"testing"
	"time"
)

func TestStorePersistsAndReloads(t *testing.T) {
	dir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{ID: "20260816-0001", Status: StatusCompleted, StartedAt: time.Now()}
	if err := store.Upsert(snap); err != nil {
		t.Fatal(err)
	}

	// Reload from disk in a fresh store.
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := store2.Get("20260816-0001")
	if !ok || got.ID != snap.ID || got.Status != StatusCompleted {
		t.Fatalf("expected persisted snapshot, got %+v ok=%v", got, ok)
	}
}

func TestNewManagerSeedsSequenceFromHistory(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("20060102")

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Snapshot{ID: today + "-0003", Status: StatusCompleted}); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.nextID(); got != today+"-0004" {
		t.Fatalf("expected %s-0004, got %s", today, got)
	}
}

func TestListIncludesPersistedHistory(t *testing.T) {
	dir := t.TempDir()

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snap := Snapshot{ID: "20260101-0001", Status: StatusCompleted, StartedAt: time.Now().Add(-time.Hour)}
	if err := store.Upsert(snap); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := m.List()
	if len(list) != 1 || list[0].ID != snap.ID {
		t.Fatalf("expected 1 persisted scan in List, got %+v", list)
	}
}
