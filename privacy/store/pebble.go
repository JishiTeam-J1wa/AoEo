// pebble.go 基于 Pebble LSM-Tree 引擎实现 MappingStore 接口，
// 使用双向键编码（fake->original 和 original->fake）支持高效的单值和批量查询。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
)

// 键编码前缀常量。
const (
	keyPrefix     = "s:"   // 格式: s:session:f:fake -> original
	keySep        = ":"
	keyFakeDir    = "f"    // fake -> original 方向
	keyOrigDir    = "o"    // original -> fake 方向
	keyUpperBound = ";"    // 用于 DeleteRange 的上界（';' > ':'）
)

// PebbleStore 使用 Pebble LSM-Tree 引擎实现 MappingStore 接口。
// 通过 Batch 写入保证双向映射的原子性。
type PebbleStore struct {
	db *pebble.DB
}

// OpenPebble 在指定路径打开 Pebble 数据库。
// 配置 32 MB 块缓存和 1 MB MemTable 以优化小规模读写性能。
//
// Param:
//   - path: string - 数据库目录路径
//
// Return:
//   - *PebbleStore: 初始化完成的 Pebble 存储实例
//   - error: 数据库打开失败时返回错误
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

// Close 关闭底层 Pebble 数据库，释放文件锁和内存缓存。
func (s *PebbleStore) Close() error {
	return s.db.Close()
}

// keyFake 构造 fake->original 查找方向的数据库键。
func keyFake(sessionID, fake string) []byte {
	return []byte(keyPrefix + sessionID + keySep + keyFakeDir + keySep + fake)
}

// keyOrig 构造 original->fake 查找方向的数据库键。
func keyOrig(sessionID, original string) []byte {
	return []byte(keyPrefix + sessionID + keySep + keyOrigDir + keySep + original)
}

// sessionPrefix 返回指定会话所有键的公共前缀。
func sessionPrefix(sessionID string) []byte {
	return []byte(keyPrefix + sessionID + keySep)
}

// sessionUpperBound 返回 DeleteRange 操作的排他性上界键。
func sessionUpperBound(sessionID string) []byte {
	return []byte(keyPrefix + sessionID + keyUpperBound)
}

// encodeValue 在字符串前附加 8 字节 BigEndian 编码的 UnixNano 时间戳。
// 用于在值中记录映射的创建时间。
func encodeValue(t time.Time, v string) []byte {
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(t.UnixNano()))
	return append(ts, []byte(v)...)
}

// decodeValue 将编码后的值拆分为时间戳和字符串。
// 如果值长度不足 8 字节，返回零值时间戳和原始字节内容。
func decodeValue(b []byte) (time.Time, string) {
	if len(b) < 8 {
		return time.Time{}, string(b)
	}
	ts := binary.BigEndian.Uint64(b[:8])
	return time.Unix(0, int64(ts)), string(b[8:])
}

// Set 通过 Pebble Batch 原子地写入双向映射（fake->original 和 original->fake）。
// Batch 确保两个方向的写入要么全部成功，要么全部不写入，防止出现单向映射的不一致状态。
func (s *PebbleStore) Set(ctx context.Context, sessionID, fake, original string, typ string) error {
	now := time.Now()
	batch := s.db.NewBatch()
	defer batch.Close()

	// fake -> original 方向（附带时间戳）
	if err := batch.Set(keyFake(sessionID, fake), encodeValue(now, original), pebble.NoSync); err != nil {
		return err
	}
	// original -> fake 方向（附带时间戳）
	if err := batch.Set(keyOrig(sessionID, original), encodeValue(now, fake), pebble.NoSync); err != nil {
		return err
	}

	return s.db.Apply(batch, pebble.Sync)
}

// GetOriginal 通过伪造值在 fake->original 方向查找对应的原始值。
// 如果键不存在，返回 ("", false, nil) 而非错误。
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

// GetFake 通过原始值在 original->fake 方向查找对应的伪造值。
// 如果键不存在，返回 ("", false, nil) 而非错误。
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

// GetSession 遍历指定会话的 fake->original 键，返回全部映射记录。
// 仅遍历 fake->original 方向的键以避免返回重复条目。
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
		// 仅处理 fake->original 方向的键（跳过 original->fake 以避免重复）
		if len(key) < len(prefix)+len(keyFakeDir)+1 {
			continue
		}
		dirStart := len(prefix)
		dirEnd := dirStart + len(keyFakeDir)
		if key[dirStart:dirEnd] != keyFakeDir {
			continue
		}

		fake := key[dirEnd+1:] // "f:" 之后的部分
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

// DeleteSession 使用 Pebble DeleteRange 原子删除指定会话的全部映射键。
// 利用前缀范围 [sessionPrefix, sessionUpperBound) 一次性清除所有双向映射。
func (s *PebbleStore) DeleteSession(ctx context.Context, sessionID string) error {
	return s.db.DeleteRange(sessionPrefix(sessionID), sessionUpperBound(sessionID), pebble.Sync)
}

// Cleanup 遍历全部键，删除创建时间早于或等于指定时刻的过期映射。
// 使用 Batch 批量删除以提高写入效率。
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
