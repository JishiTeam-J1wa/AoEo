package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
)

// prefix constants for key encoding.
const (
	keyPrefix     = "s:"   // s:session:f:fake → original
	keySep        = ":"
	keyFakeDir    = "f"    // fake → original
	keyOrigDir    = "o"    // original → fake
	keyUpperBound = ";"    // used for DeleteRange upper bound (';' > ':')
)

// PebbleStore implements MappingStore using Pebble LSM-Tree.
type PebbleStore struct {
	db *pebble.DB
}

// OpenPebble opens a Pebble database at the given path.
func OpenPebble(path string) (*PebbleStore, error) {
	cache := pebble.NewCache(32 << 20) // 32 MB block cache
	defer cache.Unref()

	opts := &pebble.Options{
		Cache:                       cache,
		MemTableSize:                1 << 20, // 1 MB
		MemTableStopWritesThreshold: 2,
		BytesPerSync:                256 << 10,
	}

	db, err := pebble.Open(path, opts)
	if err != nil {
		return nil, fmt.Errorf("open pebble: %w", err)
	}
	return &PebbleStore{db: db}, nil
}

// Close implements MappingStore.
func (s *PebbleStore) Close() error {
	return s.db.Close()
}

// keyFake returns the DB key for fake→original lookup.
func keyFake(sessionID, fake string) []byte {
	return []byte(keyPrefix + sessionID + keySep + keyFakeDir + keySep + fake)
}

// keyOrig returns the DB key for original→fake lookup.
func keyOrig(sessionID, original string) []byte {
	return []byte(keyPrefix + sessionID + keySep + keyOrigDir + keySep + original)
}

// sessionPrefix returns the prefix for all keys belonging to a session.
func sessionPrefix(sessionID string) []byte {
	return []byte(keyPrefix + sessionID + keySep)
}

// sessionUpperBound returns the exclusive upper bound for DeleteRange.
func sessionUpperBound(sessionID string) []byte {
	return []byte(keyPrefix + sessionID + keyUpperBound)
}

// encodeValue prepends an 8-byte BigEndian UnixNano timestamp to the string.
func encodeValue(t time.Time, v string) []byte {
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(t.UnixNano()))
	return append(ts, []byte(v)...)
}

// decodeValue splits the value into timestamp and string.
func decodeValue(b []byte) (time.Time, string) {
	if len(b) < 8 {
		return time.Time{}, string(b)
	}
	ts := binary.BigEndian.Uint64(b[:8])
	return time.Unix(0, int64(ts)), string(b[8:])
}

// Set implements MappingStore. Writes both directions atomically via Batch.
func (s *PebbleStore) Set(ctx context.Context, sessionID, fake, original string, typ string) error {
	now := time.Now()
	batch := s.db.NewBatch()
	defer batch.Close()

	// fake → original (with timestamp)
	if err := batch.Set(keyFake(sessionID, fake), encodeValue(now, original), pebble.NoSync); err != nil {
		return err
	}
	// original → fake (with timestamp)
	if err := batch.Set(keyOrig(sessionID, original), encodeValue(now, fake), pebble.NoSync); err != nil {
		return err
	}

	return s.db.Apply(batch, pebble.Sync)
}

// GetOriginal implements MappingStore.
func (s *PebbleStore) GetOriginal(ctx context.Context, sessionID, fake string) (string, bool, error) {
	val, closer, err := s.db.Get(keyFake(sessionID, fake))
	if err == pebble.ErrNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer closer.Close()
	_, original := decodeValue(val)
	return original, true, nil
}

// GetFake implements MappingStore.
func (s *PebbleStore) GetFake(ctx context.Context, sessionID, original string) (string, bool, error) {
	val, closer, err := s.db.Get(keyOrig(sessionID, original))
	if err == pebble.ErrNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer closer.Close()
	_, fake := decodeValue(val)
	return fake, true, nil
}

// GetSession implements MappingStore. Iterates fake→original keys only.
func (s *PebbleStore) GetSession(ctx context.Context, sessionID string) ([]Entry, error) {
	prefix := sessionPrefix(sessionID)
	upper := sessionUpperBound(sessionID)

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var entries []Entry
	for iter.First(); iter.Valid(); iter.Next() {
		key := string(iter.Key())
		// Only process fake→original keys (skip original→fake to avoid duplicates)
		if len(key) < len(prefix)+len(keyFakeDir)+1 {
			continue
		}
		dirStart := len(prefix)
		dirEnd := dirStart + len(keyFakeDir)
		if key[dirStart:dirEnd] != keyFakeDir {
			continue
		}

		fake := key[dirEnd+1:] // after "f:"
		createdAt, original := decodeValue(iter.Value())
		entries = append(entries, Entry{
			SessionID: sessionID,
			Original:  original,
			Fake:      fake,
			CreatedAt: createdAt,
		})
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return entries, nil
}

// DeleteSession implements MappingStore.
func (s *PebbleStore) DeleteSession(ctx context.Context, sessionID string) error {
	return s.db.DeleteRange(sessionPrefix(sessionID), sessionUpperBound(sessionID), pebble.Sync)
}

// Cleanup implements MappingStore. Iterates all keys and deletes expired ones.
func (s *PebbleStore) Cleanup(ctx context.Context, before time.Time) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	defer batch.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		createdAt, _ := decodeValue(iter.Value())
		if createdAt.Before(before) || createdAt.Equal(before) {
			if err := batch.Delete(iter.Key(), pebble.NoSync); err != nil {
				return err
			}
		}
	}
	if err := iter.Error(); err != nil {
		return err
	}
	return s.db.Apply(batch, pebble.Sync)
}
