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

// MetaStore holds bucket metadata (BoltDB) and object metadata (in-memory).
// Bucket CRUD goes through BoltDB for persistence. Object metadata is
// replicated through Raft and kept in-memory for fast access.
type MetaStore struct {
	mu       sync.RWMutex
	buckets  *BucketDB      // persistent bucket store
	cache    map[string]bool // fast existence check, rebuilt from BoltDB
	objects  map[string]map[string]*ObjectMeta // bucket → key → meta
	region   string         // default region for new buckets
}

// NewMetaStore creates a MetaStore backed by BoltDB at dataDir.
func NewMetaStore(dataDir string) (*MetaStore, error) {
	bdb, err := OpenBucketDB(dataDir)
	if err != nil {
		return nil, err
	}
	// Build in-memory cache from BoltDB.
	cache := make(map[string]bool)
	buckets, _ := bdb.List()
	for _, b := range buckets {
		cache[b.Name] = true
	}
	return &MetaStore{
		buckets: bdb,
		cache:   cache,
		objects: make(map[string]map[string]*ObjectMeta),
	}, nil
}

// Region returns the default region for new buckets.
func (m *MetaStore) Region() string {
	return m.region
}

// SetRegion sets the default region for new buckets.
func (m *MetaStore) SetRegion(r string) {
	m.region = r
}

// CreateBucket adds a bucket. ErrBucketExists on duplicate.
func (m *MetaStore) CreateBucket(name string) error {
	if err := m.buckets.Create(name, m.region); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[name] = true
	m.objects[name] = make(map[string]*ObjectMeta)
	return nil
}

// DeleteBucket removes a bucket and all its object metadata.
func (m *MetaStore) DeleteBucket(name string) error {
	if err := m.buckets.Delete(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, name)
	delete(m.objects, name)
	return nil
}

// GetBucket returns the named bucket, or ErrNoBucket if it doesn't exist.
func (m *MetaStore) GetBucket(name string) (Bucket, error) {
	return m.buckets.Get(name)
}

// ListBuckets returns all buckets, unordered.
func (m *MetaStore) ListBuckets() []Bucket {
	out, _ := m.buckets.List()
	return out
}

// PutObject registers object metadata. ErrNoBucket if the bucket
// doesn't exist — objects can never outlive their bucket.
func (m *MetaStore) PutObject(bucket, key string, meta ObjectMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cache[bucket] {
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

// ListObjectsPaged returns a page of objects matching prefix, sorted by key.
// startAfter is exclusive (results begin after this key). maxKeys limits results.
func (m *MetaStore) ListObjectsPaged(bucket, prefix, startAfter string, maxKeys int) (objs []ObjectMeta, nextToken string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	if bucketObjs, ok := m.objects[bucket]; ok {
		for k, v := range bucketObjs {
			if (prefix == "" || strings.HasPrefix(k, prefix)) && k > startAfter {
				objs = append(objs, *v)
			}
		}
		sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
		if len(objs) > maxKeys {
			objs = objs[:maxKeys]
			nextToken = objs[len(objs)-1].Key
		}
	}
	return objs, nextToken
}

// --- Raft snapshot support ---

// Snapshot serializes object metadata to w for Raft persistence.
// Bucket metadata lives in BoltDB and is not included in snapshots.
func (m *MetaStore) Snapshot(w io.Writer) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.NewEncoder(w).Encode(map[string]interface{}{
		"objects": m.objects,
	})
}

// Restore loads object metadata from a Raft snapshot.
// Bucket metadata is re-read from BoltDB into the in-memory cache.
func (m *MetaStore) Restore(r io.Reader) error {
	var v struct {
		Objects map[string]map[string]*ObjectMeta `json:"objects"`
	}
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		return err
	}

	// Rebuild bucket cache from BoltDB.
	buckets, _ := m.buckets.List()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = make(map[string]bool, len(buckets))
	for _, b := range buckets {
		m.cache[b.Name] = true
	}

	// Merge objects: add keys not in snapshot, keep snapshot's keys.
	if v.Objects != nil {
		for b, objs := range v.Objects {
			if m.objects[b] == nil {
				m.objects[b] = make(map[string]*ObjectMeta)
			}
			for k, meta := range objs {
				m.objects[b][k] = meta
			}
		}
	}
	return nil
}

// MarshalJSON implements json.Marshaler for snapshot serialization.
func (m *MetaStore) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.Marshal(map[string]interface{}{
		"objects": m.objects,
	})
}
