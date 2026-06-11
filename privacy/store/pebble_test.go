package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tmpDB(t *testing.T) (*PebbleStore, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "privacy.db")
	s, err := OpenPebble(dir)
	if err != nil {
		t.Fatalf("open pebble: %v", err)
	}
	return s, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

func TestPebbleStore_SetAndGet(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	if err := s.Set(ctx, "sess-1", "fake-13800138000", "13800138000", "phone"); err != nil {
		t.Fatalf("set: %v", err)
	}

	orig, ok, err := s.GetOriginal(ctx, "sess-1", "fake-13800138000")
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if !ok || orig != "13800138000" {
		t.Fatalf("expected original 13800138000, got %q (ok=%v)", orig, ok)
	}

	fake, ok, err := s.GetFake(ctx, "sess-1", "13800138000")
	if err != nil {
		t.Fatalf("get fake: %v", err)
	}
	if !ok || fake != "fake-13800138000" {
		t.Fatalf("expected fake fake-13800138000, got %q (ok=%v)", fake, ok)
	}
}

func TestPebbleStore_GetNotFound(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	_, ok, err := s.GetOriginal(ctx, "sess-1", "nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestPebbleStore_GetSession(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	s.Set(ctx, "sess-1", "fake-A", "original-A", "person")
	s.Set(ctx, "sess-1", "fake-B", "original-B", "phone")
	s.Set(ctx, "sess-2", "fake-C", "original-C", "email")

	entries, err := s.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	m := make(map[string]string)
	for _, e := range entries {
		m[e.Fake] = e.Original
	}
	if m["fake-A"] != "original-A" || m["fake-B"] != "original-B" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestPebbleStore_DeleteSession(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	s.Set(ctx, "sess-1", "fake-A", "original-A", "person")
	s.Set(ctx, "sess-2", "fake-B", "original-B", "phone")

	if err := s.DeleteSession(ctx, "sess-1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	_, ok, _ := s.GetOriginal(ctx, "sess-1", "fake-A")
	if ok {
		t.Fatal("expected sess-1 entries deleted")
	}

	orig, ok, _ := s.GetOriginal(ctx, "sess-2", "fake-B")
	if !ok || orig != "original-B" {
		t.Fatal("expected sess-2 entries preserved")
	}
}

func TestPebbleStore_Cleanup(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	// Write an entry
	s.Set(ctx, "sess-1", "fake-A", "original-A", "person")

	// Cleanup entries older than 1 hour ago
	before := time.Now().Add(-time.Hour)
	if err := s.Cleanup(ctx, before); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Entry should still exist (just created)
	_, ok, _ := s.GetOriginal(ctx, "sess-1", "fake-A")
	if !ok {
		t.Fatal("expected entry to survive cleanup")
	}

	// Cleanup entries older than 1 second from now (future)
	before = time.Now().Add(time.Second)
	if err := s.Cleanup(ctx, before); err != nil {
		t.Fatalf("cleanup future: %v", err)
	}

	// Entry should be deleted
	_, ok, _ = s.GetOriginal(ctx, "sess-1", "fake-A")
	if ok {
		t.Fatal("expected entry to be cleaned up")
	}
}

func TestPebbleStore_Concurrent(t *testing.T) {
	s, cleanup := tmpDB(t)
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			fake := fmt.Sprintf("fake-%d", idx)
			orig := fmt.Sprintf("orig-%d", idx)
			if err := s.Set(ctx, "concurrent", fake, orig, "person"); err != nil {
				t.Errorf("set %d: %v", idx, err)
			}
			got, ok, err := s.GetOriginal(ctx, "concurrent", fake)
			if err != nil {
				t.Errorf("get %d: %v", idx, err)
			}
			if !ok || got != orig {
				t.Errorf("get %d: expected %q, got %q (ok=%v)", idx, orig, got, ok)
			}
		}(i)
	}
	wg.Wait()

	entries, err := s.GetSession(ctx, "concurrent")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries, got %d", len(entries))
	}
}

func BenchmarkPebbleStore_Set(b *testing.B) {
	s, cleanup := tmpDB(&testing.T{})
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Set(ctx, "bench", "fake", "original", "person")
	}
}

func BenchmarkPebbleStore_Get(b *testing.B) {
	s, cleanup := tmpDB(&testing.T{})
	defer cleanup()
	ctx := context.Background()
	s.Set(ctx, "bench", "fake", "original", "person")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.GetOriginal(ctx, "bench", "fake")
	}
}
