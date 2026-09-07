// Package store implements the jario storage engine.
// Blobs live on disk, content-addressed by sha256.
// Metadata lives in memory, protected by a RWMutex,
// and will later be Raft-replicated.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Sentinel errors — matched by callers checking for specific conditions.
var (
	ErrBucketExists = fmt.Errorf("BucketAlreadyExists")
	ErrNoBucket     = fmt.Errorf("NoSuchBucket")
	ErrNoKey        = fmt.Errorf("NoSuchKey")
)

// Store is the storage engine. Owns blob dir and metadata index.
type Store struct {
	dataDir string
	meta    *MetaStore
}

// New creates a Store rooted at dataDir. Creates blobs/ subdirectory.
func New(dataDir string) *Store {
	os.MkdirAll(filepath.Join(dataDir, "blobs"), 0755)
	return &Store{dataDir: dataDir, meta: NewMetaStore()}
}

// --- Bucket operations ---

// CreateBucket adds a bucket. ErrBucketExists on duplicate.
func (s *Store) CreateBucket(name string) error {
	return s.meta.CreateBucket(name)
}

// DeleteBucket removes a bucket and all its object metadata.
func (s *Store) DeleteBucket(name string) error {
	return s.meta.DeleteBucket(name)
}

// ListBuckets returns all buckets, unordered.
func (s *Store) ListBuckets() []Bucket {
	return s.meta.ListBuckets()
}

// --- Object operations ---

// PutObject writes body as a blob, registers metadata.
// Returns hex-encoded sha256 etag.
func (s *Store) PutObject(bucket, key string, body io.Reader) (string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	sha := sha256.Sum256(data)
	shaHex := hex.EncodeToString(sha[:])

	if err := s.writeBlob(shaHex, data); err != nil {
		return "", fmt.Errorf("write blob: %w", err)
	}

	if err := s.meta.PutObject(bucket, key, ObjectMeta{
		Key:       key,
		Size:      int64(len(data)),
		ETag:      shaHex,
		Sha256:    shaHex,
		CreatedAt: time.Now(),
	}); err != nil {
		return "", err
	}
	return shaHex, nil
}

// GetObject returns reader and metadata for key in bucket.
func (s *Store) GetObject(bucket, key string) (io.ReadCloser, *ObjectMeta, error) {
	meta, err := s.meta.GetObject(bucket, key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(s.blobPath(meta.Sha256))
	if err != nil {
		return nil, nil, fmt.Errorf("open blob %s: %w", meta.Sha256[:8], err)
	}
	return f, meta, nil
}

// DeleteObject removes object metadata and its blob.
// Blob deletion is idempotent — safe to call twice.
func (s *Store) DeleteObject(bucket, key string) error {
	meta, err := s.meta.GetObject(bucket, key)
	if err != nil {
		return err
	}
	os.Remove(s.blobPath(meta.Sha256))
	s.meta.DeleteObject(bucket, key)
	return nil
}

// GetMeta returns object metadata without opening the blob.
func (s *Store) GetMeta(bucket, key string) (*ObjectMeta, error) {
	return s.meta.GetObject(bucket, key)
}

// ListObjects returns metadata for all objects in bucket matching prefix.
func (s *Store) ListObjects(bucket, prefix string) []ObjectMeta {
	return s.meta.ListObjects(bucket, prefix)
}

// --- helpers ---

// blobPath returns the shard path: blobs/<sha[0:2]>/<sha>
func (s *Store) blobPath(sha string) string {
	return filepath.Join(s.dataDir, "blobs", sha[:2], sha)
}

func (s *Store) writeBlob(sha string, data []byte) error {
	dir := filepath.Join(s.dataDir, "blobs", sha[:2])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.blobPath(sha), data, 0644)
}
