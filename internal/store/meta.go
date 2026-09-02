package store

import (
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

// metaStore holds all bucket and object metadata.
// All mutations require a write lock; reads use RLock.
// structs, pointers, slices that satisfy the invariant
// "maps are never nil after init, mutex is never copied after init".
type metaStore struct {
	mu      sync.RWMutex
	buckets map[string]*Bucket
	objects map[string]map[string]*ObjectMeta // bucket → key → meta
}

func newMetaStore() *metaStore {
	return &metaStore{
		buckets: make(map[string]*Bucket),
		objects: make(map[string]map[string]*ObjectMeta),
	}
}

func (m *metaStore) createBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[name]; ok {
		return ErrBucketExists
	}
	m.buckets[name] = &Bucket{Name: name, CreatedAt: time.Now()}
	m.objects[name] = make(map[string]*ObjectMeta)
	return nil
}

func (m *metaStore) deleteBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[name]; !ok {
		return ErrNoBucket
	}
	delete(m.buckets, name)
	delete(m.objects, name)
	return nil
}

func (m *metaStore) listBuckets() []Bucket {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Bucket, 0, len(m.buckets))
	for _, b := range m.buckets {
		out = append(out, *b)
	}
	return out
}

func (m *metaStore) putObject(bucket, key string, meta ObjectMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[bucket]; !ok {
		m.objects[bucket] = make(map[string]*ObjectMeta)
	}
	m.objects[bucket][key] = &meta
}

func (m *metaStore) getObject(bucket, key string) (*ObjectMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if objs, ok := m.objects[bucket]; ok {
		if m2, ok := objs[key]; ok {
			return m2, nil
		}
	}
	return nil, ErrNoKey
}

func (m *metaStore) deleteObject(bucket, key string) error {
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

func (m *metaStore) listObjects(bucket, prefix string) []ObjectMeta {
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
