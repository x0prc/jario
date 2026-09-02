package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Store is the storage engine. Content-addressed blobs on disk,
// in-memory metadata map.
type Store struct {
	dataDir string
	meta    *metaStore
}

func New(dataDir string) *Store {
	os.MkdirAll(filepath.Join(dataDir, "blobs"), 0755)
	return &Store{dataDir: dataDir, meta: newMetaStore()}
}

// --- Bucket operations ---

func (s *Store) CreateBucket(name string) error {
	return s.meta.createBucket(name)
}

func (s *Store) DeleteBucket(name string) error {
	return s.meta.deleteBucket(name)
}

func (s *Store) ListBuckets() []Bucket {
	return s.meta.listBuckets()
}

// --- Object operations ---

func (s *Store) PutObject(bucket, key string, body io.Reader) (etag string, err error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	sha := sha256.Sum256(data)
	shaHex := hex.EncodeToString(sha[:])

	err = writeBlob(s.dataDir, shaHex, data)
	if err != nil {
		return "", err
	}

	s.meta.putObject(bucket, key, ObjectMeta{
		Key:       key,
		Size:      int64(len(data)),
		ETag:      shaHex,
		Sha256:    shaHex,
		CreatedAt: time.Now(),
	})
	return shaHex, nil
}

func (s *Store) GetObject(bucket, key string) (io.ReadCloser, *ObjectMeta, error) {
	meta, err := s.meta.getObject(bucket, key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(blobPath(s.dataDir, meta.Sha256))
	if err != nil {
		return nil, nil, err
	}
	return f, meta, nil
}

func (s *Store) DeleteObject(bucket, key string) error {
	meta, err := s.meta.getObject(bucket, key)
	if err != nil {
		return err
	}
	os.Remove(blobPath(s.dataDir, meta.Sha256))
	s.meta.deleteObject(bucket, key)
	return nil
}

func (s *Store) GetMeta(bucket, key string) (*ObjectMeta, error) {
	return s.meta.getObject(bucket, key)
}

func (s *Store) ListObjects(bucket, prefix string) []ObjectMeta {
	return s.meta.listObjects(bucket, prefix)
}

// --- helpers ---

func blobPath(dataDir, sha string) string {
	return filepath.Join(dataDir, "blobs", sha[:2], sha)
}

func writeBlob(dataDir, sha string, data []byte) error {
	dir := filepath.Join(dataDir, "blobs", sha[:2])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sha), data, 0644)
}
