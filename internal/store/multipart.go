// Multipart upload support: tracks in-progress uploads and their parts
// on disk, assembles the final object on CompleteMultipartUpload.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MultipartUpload is one in-progress multipart upload.
type MultipartUpload struct {
	UploadID   string
	Bucket     string
	Key        string
	Initiated  time.Time
	UploadID64 string // human-readable alias — the canonical ID is UploadID
}

// Part describes one uploaded part within a multipart upload.
type Part struct {
	PartNumber int
	Size       int64
	ETag       string
	Sha256     string
}

// CompletedPart is the caller-supplied part清单 for CompleteMultipartUpload.
type CompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// multipartUpload is the internal bookkeeping for one in-flight upload.
type multipartUpload struct {
	id       string
	bucket   string
	key      string
	initiated time.Time
	mu       sync.Mutex
	parts    map[int]Part // partNumber → Part
}

// --- store-level multipart state (in-memory, not replicated) ---

type multipartState struct {
	mu      sync.Mutex
	uploads map[string]*multipartUpload // uploadID → upload
}

func newMultipartState() *multipartState {
	return &multipartState{uploads: make(map[string]*multipartUpload)}
}

// multipartDir returns the temporary part directory for an upload.
func multipartDir(dataDir, uploadID string) string {
	return filepath.Join(dataDir, "multipart", uploadID)
}

// partPath returns the on-disk path for a single part.
func partPath(dataDir, uploadID string, partNumber int) string {
	return filepath.Join(multipartDir(dataDir, uploadID), fmt.Sprintf("part%d", partNumber))
}

// --- public store methods ---

// NewMultipartUpload starts a new multipart upload.
func (s *Store) NewMultipartUpload(bucket, key string) (string, error) {
	if _, err := s.meta.GetBucket(bucket); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomHex(8))
	s.multipart.mu.Lock()
	s.multipart.uploads[id] = &multipartUpload{
		id:       id,
		bucket:   bucket,
		key:      key,
		initiated: time.Now(),
		parts:    make(map[int]Part),
	}
	s.multipart.mu.Unlock()
	return id, nil
}

// UploadPart stores a part for an in-progress multipart upload.
// Returns the part's ETag (sha256 hex).
func (s *Store) UploadPart(bucket, key, uploadID string, partNumber int, body io.Reader) (string, error) {
	if err := s.validateUpload(bucket, key, uploadID); err != nil {
		return "", err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read part: %w", err)
	}
	sha := sha256.Sum256(data)
	etag := hex.EncodeToString(sha[:])

	dir := multipartDir(s.dataDir, uploadID)
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(partPath(s.dataDir, uploadID, partNumber), data, 0644); err != nil {
		return "", fmt.Errorf("write part: %w", err)
	}
	s.multipart.mu.Lock()
	defer s.multipart.mu.Unlock()
	u := s.multipart.uploads[uploadID]
	u.mu.Lock()
	defer u.mu.Unlock()
	u.parts[partNumber] = Part{
		PartNumber: partNumber,
		Size:       int64(len(data)),
		ETag:       etag,
		Sha256:     etag,
	}
	return etag, nil
}

// CompleteMultipartUpload assembles parts into the final object.
// Parts are ordered by partNumber and concatenated; the result is stored
// as a single blob with a sha256 ETag.
func (s *Store) CompleteMultipartUpload(bucket, key, uploadID string, parts []CompletedPart) error {
	if err := s.validateUpload(bucket, key, uploadID); err != nil {
		return err
	}
	s.multipart.mu.Lock()
	u := s.multipart.uploads[uploadID]
	s.multipart.mu.Unlock()
	if u == nil {
		return ErrInvalidUploadID
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	// Sort supplied parts by number and validate they were all uploaded.
	sorted := make([]CompletedPart, len(parts))
	copy(sorted, parts)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PartNumber < sorted[j].PartNumber })
	var totalSize int64
	for _, cp := range sorted {
		p, ok := u.parts[cp.PartNumber]
		if !ok {
			return fmt.Errorf("part %d not uploaded", cp.PartNumber)
		}
		// S3 clients send ETags with surrounding quotes; strip them for comparison.
		requestedETag := strings.Trim(cp.ETag, `"`)
		if requestedETag != p.ETag {
			return fmt.Errorf("part %d ETag mismatch", cp.PartNumber)
		}
		totalSize += p.Size
	}

	// Concatenate parts into final object.
	var combined []byte
	for _, cp := range sorted {
		data, err := os.ReadFile(partPath(s.dataDir, uploadID, cp.PartNumber))
		if err != nil {
			return fmt.Errorf("read part %d: %w", cp.PartNumber, err)
		}
		combined = append(combined, data...)
	}

	// Write blob.
	sha := sha256.Sum256(combined)
	etag := hex.EncodeToString(sha[:])
	if err := s.writeBlob(etag, combined); err != nil {
		return fmt.Errorf("write blob: %w", err)
	}

	// Register metadata.
	meta := ObjectMeta{
		Key:       key,
		Size:      totalSize,
		ETag:      etag,
		Sha256:    etag,
		CreatedAt: time.Now(),
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := s.raftApply(RaftOp{Kind: "put_object", Bucket: bucket, Key: key, Meta: metaJSON}); err != nil {
		return err
	}

	// Cleanup.
	s.cleanupUpload(uploadID)
	return nil
}

// AbortMultipartUpload discards all parts and removes the upload.
func (s *Store) AbortMultipartUpload(bucket, key, uploadID string) error {
	if err := s.validateUpload(bucket, key, uploadID); err != nil {
		return err
	}
	s.cleanupUpload(uploadID)
	return nil
}

// ListParts returns the parts uploaded so far for an in-progress upload.
func (s *Store) ListParts(bucket, key, uploadID string) ([]Part, error) {
	if err := s.validateUpload(bucket, key, uploadID); err != nil {
		return nil, err
	}
	s.multipart.mu.Lock()
	u := s.multipart.uploads[uploadID]
	s.multipart.mu.Unlock()
	if u == nil {
		return nil, ErrInvalidUploadID
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]Part, 0, len(u.parts))
	for _, p := range u.parts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartNumber < out[j].PartNumber })
	return out, nil
}

// ListMultipartUploads returns all in-progress uploads in a bucket.
func (s *Store) ListMultipartUploads(bucket string) ([]MultipartUpload, error) {
	if _, err := s.meta.GetBucket(bucket); err != nil {
		return nil, err
	}
	s.multipart.mu.Lock()
	defer s.multipart.mu.Unlock()
	var out []MultipartUpload
	for _, u := range s.multipart.uploads {
		if u.bucket == bucket {
			out = append(out, MultipartUpload{
				UploadID:  u.id,
				Bucket:    u.bucket,
				Key:       u.key,
				Initiated: u.initiated,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Initiated.Before(out[j].Initiated) })
	return out, nil
}

// --- internal helpers ---

func (s *Store) validateUpload(bucket, key, uploadID string) error {
	s.multipart.mu.Lock()
	defer s.multipart.mu.Unlock()
	u, ok := s.multipart.uploads[uploadID]
	if !ok {
		return ErrInvalidUploadID
	}
	if u.bucket != bucket || u.key != key {
		return ErrInvalidUploadID
	}
	return nil
}

func (s *Store) cleanupUpload(uploadID string) {
	dir := multipartDir(s.dataDir, uploadID)
	os.RemoveAll(dir) // idempotent
	s.multipart.mu.Lock()
	delete(s.multipart.uploads, uploadID)
	s.multipart.mu.Unlock()
}

// randomHex returns n bytes of hex-encoded randomness for upload IDs.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
