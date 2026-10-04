package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"

	"strings"

	"time"

	"github.com/fsykk/qq-bot/internal/model"
	bolt "go.etcd.io/bbolt"
)

type GatewayState struct {
	SessionID string `json:"session_id"`
	Sequence  int64  `json:"sequence"`
}

func (s *Store) EnqueueGatewayEvent(key string, payload []byte, now time.Time, ttl time.Duration, maxPending int) (pending bool, err error) {
	key = strings.TrimSpace(key)
	if key == "" || len(payload) == 0 {
		return false, errors.New("gateway event key and payload are required")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if maxPending <= 0 {
		maxPending = 512
	}
	const cleanupInterval = 5 * time.Minute
	nowUnix := now.Unix()
	cleanup := false
	lastCleanup := s.lastDedupCleanupUnix.Load()
	if lastCleanup == 0 || nowUnix-lastCleanup >= int64(cleanupInterval/time.Second) {
		cleanup = s.lastDedupCleanupUnix.CompareAndSwap(lastCleanup, nowUnix)
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		dedup := tx.Bucket([]byte("message_dedup"))
		inbox := tx.Bucket([]byte("event_inbox"))
		keyBytes := []byte(key)
		if inbox.Get(keyBytes) != nil {
			var encoded [8]byte
			binary.BigEndian.PutUint64(encoded[:], uint64(now.Add(ttl).Unix()))
			if err := dedup.Put(keyBytes, encoded[:]); err != nil {
				return err
			}
			pending = true
			return nil
		}
		if value := dedup.Get(keyBytes); len(value) == 8 && int64(binary.BigEndian.Uint64(value)) > nowUnix {
			return nil
		}
		if inbox.Stats().KeyN >= maxPending {
			return ErrEventInboxFull
		}
		record, err := json.Marshal(model.PendingGatewayEvent{Payload: append([]byte(nil), payload...), CreatedAt: now})
		if err != nil {
			return err
		}
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(now.Add(ttl).Unix()))
		if err := dedup.Put(keyBytes, encoded[:]); err != nil {
			return err
		}
		if err := inbox.Put(keyBytes, record); err != nil {
			return err
		}
		pending = true
		if cleanup {
			cursor := dedup.Cursor()
			for existingKey, value := cursor.First(); existingKey != nil; existingKey, value = cursor.Next() {
				if len(value) == 8 && int64(binary.BigEndian.Uint64(value)) <= nowUnix && inbox.Get(existingKey) == nil {
					if err := cursor.Delete(); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	return pending, err
}

func (s *Store) ListPendingGatewayEvents(limit int) ([]model.PendingGatewayEvent, error) {
	if limit <= 0 {
		limit = 64
	}
	result := make([]model.PendingGatewayEvent, 0, limit)
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket([]byte("event_inbox")).Cursor()
		for key, value := cursor.First(); key != nil && len(result) < limit; key, value = cursor.Next() {
			var item model.PendingGatewayEvent
			if err := json.Unmarshal(value, &item); err != nil {
				return err
			}
			item.Key = string(key)
			result = append(result, item)
		}
		return nil
	})
	return result, err
}

func (s *Store) CompleteGatewayEvent(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("event_inbox")).Delete([]byte(key))
	})
}

func (s *Store) GetGatewayState() (GatewayState, error) {
	var state GatewayState
	err := s.db.View(func(tx *bolt.Tx) error {
		value := tx.Bucket([]byte("gateway")).Get([]byte("state"))
		if value == nil {
			return ErrNotFound
		}
		return json.Unmarshal(value, &state)
	})
	return state, err
}

func (s *Store) PutGatewayState(state GatewayState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("gateway")).Put([]byte("state"), data)
	})
}

func (s *Store) ClearGatewayState() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("gateway")).Delete([]byte("state"))
	})
}

func (s *Store) AddAudit(record model.AuditRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("audit"))
		seq, err := bucket.NextSequence()
		if err != nil {
			return err
		}
		var key [8]byte
		binary.BigEndian.PutUint64(key[:], seq)
		if err := bucket.Put(key[:], data); err != nil {
			return err
		}
		if seq > maxAuditRecords {
			var expired [8]byte
			binary.BigEndian.PutUint64(expired[:], seq-maxAuditRecords)
			_ = bucket.Delete(expired[:])
		}
		return nil
	})
}
