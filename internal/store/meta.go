package store

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Bucket struct {
	Name      string
	CreatedAt time.Time
}

type ObjectMeta struct {
	Key         string
	Size        int64
	ETag        string // sha256 hex
	ContentType string
	Sha256      string
	CreatedAt   time.Time
	VersionID   string
}

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
		return fmt.Errorf("BucketAlreadyExists")
	}
	m.buckets[name] = &Bucket{Name: name, CreatedAt: time.Now()}
	m.objects[name] = make(map[string]*ObjectMeta)
	return nil
}

func (m *metaStore) deleteBucket(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.buckets[name]; !ok {
		return fmt.Errorf("NoSuchBucket")
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
	return nil, fmt.Errorf("NoSuchKey")
}

func (m *metaStore) deleteObject(bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[bucket]; !ok {
		return fmt.Errorf("NoSuchKey")
	}
	if _, ok := m.objects[bucket][key]; !ok {
		return fmt.Errorf("NoSuchKey")
	}
	delete(m.objects[bucket], key)
	return nil
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
	}
	return out
}
