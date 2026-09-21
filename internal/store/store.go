// Package store implements the jario storage engine.
// Blobs live on disk, content-addressed by sha256.
// Metadata lives in memory, protected by a RWMutex.
// When wired to Raft, metadata mutations are replicated.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ErrNotLeader       = fmt.Errorf("not the Raft leader")
	ErrInvalidUploadID = fmt.Errorf("InvalidUploadID")
)

// Rafter is the interface Store uses to replicate metadata through Raft.
// Implemented by raft.RaftNode — avoids circular imports.
type Rafter interface {
	Apply(op RaftOp) error
	IsLeader() bool
}

// RaftOp is the metadata mutation replicated through Raft.
// (raft.Op is an alias of this type.)
type RaftOp struct {
	Kind      string          `json:"kind"`
	Bucket    string          `json:"bucket,omitempty"`
	Key       string          `json:"key,omitempty"`
	VersionID string          `json:"versionID,omitempty"`
	Meta      json.RawMessage `json:"meta,omitempty"`
}

// Store is the storage engine. Owns blob dir and metadata index.
type Store struct {
	dataDir   string
	meta      *MetaStore
	raft      Rafter // nil in standalone mode (no replication)
	multipart *multipartState
	region    string
}

// MaxObjectSize caps in-memory body reads to prevent OOM from malicious requests.
const MaxObjectSize = 5 << 30 // 5 GiB (S3 limit)

// New creates a Store rooted at dataDir with its own MetaStore.
func New(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0755); err != nil {
		return nil, fmt.Errorf("mkdir blobs: %w", err)
	}
	meta, err := NewMetaStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Store{dataDir: dataDir, meta: meta, multipart: newMultipartState()}, nil
}

// NewWithMeta creates a Store sharing the given MetaStore (for Raft wiring).
func NewWithMeta(dataDir string, meta *MetaStore) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0755); err != nil {
		return nil, fmt.Errorf("mkdir blobs: %w", err)
	}
	return &Store{dataDir: dataDir, meta: meta, multipart: newMultipartState()}, nil
}

// SetRaft wires the store to a Raft node for metadata replication.
func (s *Store) SetRaft(r Rafter) {
	s.raft = r
}

// raftApply sends an op through Raft if wired, otherwise applies directly.
func (s *Store) raftApply(op RaftOp) error {
	if s.raft == nil {
		return ApplyOp(s.meta, op)
	}
	if !s.raft.IsLeader() {
		return ErrNotLeader
	}
	return s.raft.Apply(op)
}

// ApplyOp applies a metadata op to m. Single source of truth shared by
// the Raft FSM (replicated writes) and raftApply (standalone mode).
func ApplyOp(m *MetaStore, op RaftOp) error {
	switch op.Kind {
	case "put_object":
		var meta ObjectMeta
		if err := json.Unmarshal(op.Meta, &meta); err != nil {
			return err
		}
		_, err := m.PutObject(op.Bucket, op.Key, meta)
		return err
	case "delete_object":
		_, err := m.DeleteObject(op.Bucket, op.Key, "")
		return err
	case "delete_version":
		_, err := m.DeleteObject(op.Bucket, op.Key, op.VersionID)
		return err
	default:
		return nil
	}
}

// --- Bucket operations ---
// Bucket CRUD is local-only — stored in BoltDB, never replicated through Raft.

// CreateBucket adds a bucket. Replicated through Raft when wired.
func (s *Store) CreateBucket(name string) error {
	return s.meta.CreateBucket(name)
}

// DeleteBucket removes a bucket and all its object metadata.
func (s *Store) DeleteBucket(name string) error {
	return s.meta.DeleteBucket(name)
}

// GetBucket returns the named bucket, or ErrNoBucket if it doesn't exist.
func (s *Store) GetBucket(name string) (Bucket, error) {
	return s.meta.GetBucket(name)
}

// ListBuckets returns all buckets, unordered.
func (s *Store) ListBuckets() []Bucket {
	return s.meta.ListBuckets()
}

// --- Object operations ---

// PutObject writes body as a blob, registers metadata via Raft.
// Returns hex-encoded sha256 etag and the new version ID.
func (s *Store) PutObject(bucket, key string, body io.Reader) (string, string, error) {
	data, err := io.ReadAll(io.LimitReader(body, MaxObjectSize))
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}

	sha := sha256.Sum256(data)
	shaHex := hex.EncodeToString(sha[:])

	if err := s.writeBlob(shaHex, data); err != nil {
		return "", "", fmt.Errorf("write blob: %w", err)
	}

	meta := ObjectMeta{
		Key:       key,
		Size:      int64(len(data)),
		ETag:      shaHex,
		Sha256:    shaHex,
		CreatedAt: time.Now(),
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", "", fmt.Errorf("marshal meta: %w", err)
	}
	if err := s.raftApply(RaftOp{Kind: "put_object", Bucket: bucket, Key: key, Meta: metaJSON}); err != nil {
		return "", "", err
	}
	// Version ID is set by MetaStore.PutObject during ApplyOp.
	// Read it back from the meta store.
	m, _ := s.meta.GetObject(bucket, key, "")
	if m != nil {
		return shaHex, m.VersionID, nil
	}
	return shaHex, "", nil
}

// GetObject returns reader and metadata for key in bucket.
// Pass versionID = "" for the latest version, or a specific version ID.
func (s *Store) GetObject(bucket, key, versionID string) (io.ReadCloser, *ObjectMeta, error) {
	meta, err := s.meta.GetObject(bucket, key, versionID)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(s.blobPath(meta.Sha256))
	if err != nil {
		return nil, nil, fmt.Errorf("open blob %s: %w", meta.Sha256[:8], err)
	}
	return f, meta, nil
}

// DeleteObject creates a delete marker for key (S3 versioning semantics).
// If versionID is specified, that specific version is permanently removed.
func (s *Store) DeleteObject(bucket, key, versionID string) (string, error) {
	if versionID != "" {
		meta, err := s.meta.GetObjectVersion(bucket, key, versionID)
		if err != nil {
			return "", err
		}
		if err := s.raftApply(RaftOp{Kind: "delete_version", Bucket: bucket, Key: key, VersionID: versionID}); err != nil {
			return "", err
		}
		if !meta.IsDeleteMarker {
			os.Remove(s.blobPath(meta.Sha256))
		}
		return "", nil
	}
	return s.meta.DeleteObject(bucket, key, "")
}

// GetMeta returns object metadata without opening the blob.
func (s *Store) GetMeta(bucket, key string) (*ObjectMeta, error) {
	return s.meta.GetObject(bucket, key, "")
}

// GetMetaVersion returns object metadata for a specific version.
func (s *Store) GetMetaVersion(bucket, key, versionID string) (*ObjectMeta, error) {
	return s.meta.GetObjectVersion(bucket, key, versionID)
}

// ListObjectVersions returns all versions for a bucket.
func (s *Store) ListObjectVersions(bucket, prefix string) ([]ObjectVersion, []ObjectVersion) {
	return s.meta.ListObjectVersions(bucket, prefix)
}

// ListObjectsPaged returns a paginated list of objects.
func (s *Store) ListObjectsPaged(bucket, prefix, startAfter string, maxKeys int) ([]ObjectMeta, string) {
	return s.meta.ListObjectsPaged(bucket, prefix, startAfter, maxKeys)
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
