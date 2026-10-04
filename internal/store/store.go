package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrEventInboxFull = errors.New("gateway event inbox is full")
)

const maxAuditRecords = 10_000

var buckets = [][]byte{
	llmConfigBucket, llmGroupsBucket, llmSessionsBucket, llmJobsBucket, llmRatesBucket,
	rssSubscriptionsBucket, rssGroupsBucket, rssSeenBucket, rssPendingBucket,
	[]byte("message_dedup"), []byte("event_inbox"), []byte("gateway"), []byte("audit"),
}

type Store struct {
	db                   *bolt.DB
	lastDedupCleanupUnix atomic.Int64
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping() error {
	return s.db.View(func(tx *bolt.Tx) error {
		for _, name := range buckets {
			if tx.Bucket(name) == nil {
				return errors.New("database bucket is missing")
			}
		}
		return nil
	})
}
