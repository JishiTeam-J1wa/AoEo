package privacy

import (
	"testing"
	"time"
)

func TestMappingStore_CRUD(t *testing.T) {
	store, err := OpenMappingStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	sessionID := "sess-123"

	// Create
	if err := store.Create(sessionID, "192.168.1.1", "10.0.0.1", EntityIP); err != nil {
		t.Fatalf("create: %v", err)
	}

	// FindFake
	fake, ok := store.FindFake(sessionID, "192.168.1.1")
	if !ok {
		t.Fatal("expected to find fake")
	}
	if fake != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", fake)
	}

	// FindOriginal
	orig, ok := store.FindOriginal(sessionID, "10.0.0.1")
	if !ok {
		t.Fatal("expected to find original")
	}
	if orig != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1, got %s", orig)
	}

	// ExistsFake
	if !store.ExistsFake(sessionID, "10.0.0.1") {
		t.Fatal("expected fake to exist")
	}
	if store.ExistsFake(sessionID, "10.0.0.2") {
		t.Fatal("expected fake to not exist")
	}

	// GetSessionMappings
	entries, err := store.GetSessionMappings(sessionID)
	if err != nil {
		t.Fatalf("get mappings: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Original != "192.168.1.1" || entries[0].Fake != "10.0.0.1" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestMappingStore_SessionIsolation(t *testing.T) {
	store, err := OpenMappingStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	store.Create("sess-a", "192.168.1.1", "10.0.0.1", EntityIP)
	store.Create("sess-b", "192.168.1.1", "10.0.0.2", EntityIP)

	fakeA, _ := store.FindFake("sess-a", "192.168.1.1")
	fakeB, _ := store.FindFake("sess-b", "192.168.1.1")

	if fakeA == fakeB {
		t.Fatal("sessions should have independent mappings")
	}
}

func TestMappingStore_Cleanup(t *testing.T) {
	store, err := OpenMappingStore(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	store.Create("sess", "old", "fake-old", EntityIP)
	store.Cleanup(time.Now().Add(time.Hour))

	_, ok := store.FindFake("sess", "old")
	if ok {
		t.Fatal("expected mapping to be cleaned up")
	}
}
