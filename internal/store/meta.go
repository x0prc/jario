package store

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bucket is a named container of objects.
type Bucket struct {
	Name      string
	CreatedAt time.Time
}

// ObjectMeta describes one object. ETag = sha256 hex for S3 compat.
type ObjectMeta struct {
	Key         string
	Size        int64
	ETag        string
	ContentType string
	Sha256      string
	CreatedAt   time.Time
	VersionID   string
}

// MetaStore holds all bucket and object metadata.
// All mutations require a write lock; reads use RLock.
type MetaStore struct {
	mu      sync.RWMutex
	buckets map[string]*Bucket
	objects map[string]map[string]*ObjectMeta // bucket → key → meta
}

func NewMetaStore() *MetaStore {
	return &MetaStore{
		buckets: make(map[string]*Bucket),
		objects: make(map[string]map[string]*ObjectMeta),
	}
}

// CreateBucket adds a bucket. ErrBucketExists on duplicate.
func (m *MetaStore) CreateBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[name]; ok {
		return ErrBucketExists
	}
	m.buckets[name] = &Bucket{Name: name, CreatedAt: time.Now()}
	m.objects[name] = make(map[string]*ObjectMeta)
	return nil
}

// DeleteBucket removes a bucket and all its object metadata.
func (m *MetaStore) DeleteBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[name]; !ok {
		return ErrNoBucket
	}
	delete(m.buckets, name)
	delete(m.objects, name)
	return nil
}

// ListBuckets returns all buckets, unordered.
func (m *MetaStore) ListBuckets() []Bucket {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Bucket, 0, len(m.buckets))
	for _, b := range m.buckets {
		out = append(out, *b)
	}
	return out
}

// PutObject registers object metadata. ErrNoBucket if the bucket
// doesn't exist — objects can never outlive their bucket.
func (m *MetaStore) PutObject(bucket, key string, meta ObjectMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[bucket]; !ok {
		return ErrNoBucket
	}
	m.objects[bucket][key] = &meta
	return nil
}

// GetObject returns object metadata for key in bucket.
func (m *MetaStore) GetObject(bucket, key string) (*ObjectMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if objs, ok := m.objects[bucket]; ok {
		if m2, ok := objs[key]; ok {
			return m2, nil
		}
	}
	return nil, ErrNoKey
}

// DeleteObject removes object metadata. ErrNoKey if not found.
func (m *MetaStore) DeleteObject(bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if objs, ok := m.objects[bucket]; ok {
		if _, ok := objs[key]; ok {
			delete(objs, key)
			return nil
		}
	}
	return ErrNoKey
}

// ListObjects returns metadata for all objects in bucket matching prefix.
// Sorted by key for S3 compatibility.
func (m *MetaStore) ListObjects(bucket, prefix string) []ObjectMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ObjectMeta
	if objs, ok := m.objects[bucket]; ok {
		for k, v := range objs {
			if prefix == "" || strings.HasPrefix(k, prefix) {
				out = append(out, *v)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	}
	return out
}

// --- Raft snapshot support ---

// Snapshot serializes the store to w for Raft persistence.
func (m *MetaStore) Snapshot(w io.Writer) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.NewEncoder(w).Encode(m)
}

// Restore loads state from a Raft snapshot.
func (m *MetaStore) Restore(r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.NewDecoder(r).Decode(m)
}

// MarshalJSON implements json.Marshaler for snapshot serialization.
func (m *MetaStore) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(map[string]interface{}{
		"buckets": m.buckets,
		"objects": m.objects,
	})
}

// UnmarshalJSON implements json.Unmarshaler for snapshot restoration.
func (m *MetaStore) UnmarshalJSON(data []byte) error {
	var v struct {
		Buckets map[string]*Bucket                 `json:"buckets"`
		Objects map[string]map[string]*ObjectMeta `json:"objects"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buckets = v.Buckets
	m.objects = v.Objects
	return nil
}
