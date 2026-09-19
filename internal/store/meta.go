package store

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bucket is a named container of objects.
type Bucket struct {
	Name      string
	CreatedAt time.Time
}

// ObjectMeta describes one object version. ETag = sha256 hex for S3 compat.
type ObjectMeta struct {
	Key         string
	Size        int64
	ETag        string
	ContentType string
	Sha256      string
	CreatedAt   time.Time
	VersionID   string
	IsDeleteMarker bool
}

// versionEntry is one version within a versioned key.
type versionEntry struct {
	Meta ObjectMeta `json:"meta"`
}

// versionState holds all versions for a single object key.
type versionState struct {
	Versions []versionEntry `json:"versions"`
	Current  int            `json:"current"`
	NextID   int64          `json:"nextID"`
}

func newVersionState() *versionState {
	return &versionState{NextID: 1}
}

// MetaStore holds bucket metadata (BoltDB) and object metadata (in-memory).
// Bucket CRUD goes through BoltDB for persistence. Object metadata is
// replicated through Raft and kept in-memory for fast access.
// Object versioning is always enabled: every PutObject creates a new version,
// and DELETE without a versionId creates a delete marker.
type MetaStore struct {
	mu      sync.RWMutex
	buckets *BucketDB
	cache   map[string]bool
	objects map[string]map[string]*versionState // bucket → key → versions
	region  string
}

// NewMetaStore creates a MetaStore backed by BoltDB at dataDir.
func NewMetaStore(dataDir string) (*MetaStore, error) {
	bdb, err := OpenBucketDB(dataDir)
	if err != nil {
		return nil, err
	}
	cache := make(map[string]bool)
	buckets, _ := bdb.List()
	for _, b := range buckets {
		cache[b.Name] = true
	}
	return &MetaStore{
		buckets: bdb,
		cache:   cache,
		objects: make(map[string]map[string]*versionState),
	}, nil
}

func (m *MetaStore) Region() string          { return m.region }
func (m *MetaStore) SetRegion(r string)      { m.region = r }

// --- Bucket operations (BoltDB-backed) ---

func (m *MetaStore) CreateBucket(name string) error {
	if err := m.buckets.Create(name, m.region); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[name] = true
	m.objects[name] = make(map[string]*versionState)
	return nil
}

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

func (m *MetaStore) GetBucket(name string) (Bucket, error) {
	return m.buckets.Get(name)
}

func (m *MetaStore) ListBuckets() []Bucket {
	out, _ := m.buckets.List()
	return out
}

// --- Object operations (versioned) ---

// PutObject creates a new version of the given key. Returns the version ID.
func (m *MetaStore) PutObject(bucket, key string, meta ObjectMeta) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cache[bucket] {
		return "", ErrNoBucket
	}
	vs, ok := m.objects[bucket][key]
	if !ok {
		vs = newVersionState()
		m.objects[bucket][key] = vs
	}
	vid := vs.NextID
	vs.NextID++
	meta.VersionID = strconv.FormatInt(vid, 10)
	vs.Versions = append(vs.Versions, versionEntry{Meta: meta})
	vs.Current = len(vs.Versions) - 1
	return meta.VersionID, nil
}

// GetObject returns the current version of key, or a specific version.
// versionID = "" means the current version. If the current version is a
// delete marker, ErrNoKey is returned (S3 GET semantics).
func (m *MetaStore) GetObject(bucket, key, versionID string) (*ObjectMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vs, ok := m.objects[bucket]
	if !ok {
		return nil, ErrNoKey
	}
	vst, ok := vs[key]
	if !ok {
		return nil, ErrNoKey
	}
	if versionID != "" {
		for i := range vst.Versions {
			if vst.Versions[i].Meta.VersionID == versionID {
				return &vst.Versions[i].Meta, nil
			}
		}
		return nil, ErrNoKey
	}
	// Current version: return it if it exists and is not a delete marker.
	if vst.Current < 0 || vst.Current >= len(vst.Versions) {
		return nil, ErrNoKey
	}
	cur := &vst.Versions[vst.Current].Meta
	if cur.IsDeleteMarker {
		return nil, ErrNoKey
	}
	return cur, nil
}

// GetObjectVersion returns any version (including delete markers).
func (m *MetaStore) GetObjectVersion(bucket, key, versionID string) (*ObjectMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	vs, ok := m.objects[bucket]
	if !ok {
		return nil, ErrNoKey
	}
	vst, ok := vs[key]
	if !ok {
		return nil, ErrNoKey
	}
	for i := range vst.Versions {
		if vst.Versions[i].Meta.VersionID == versionID {
			return &vst.Versions[i].Meta, nil
		}
	}
	return nil, ErrNoKey
}

// IsDeleteMarker returns true if the given version is a delete marker.
func (m *MetaStore) IsDeleteMarker(bucket, key, versionID string) bool {
	meta, err := m.GetObjectVersion(bucket, key, versionID)
	if err != nil {
		return false
	}
	return meta.IsDeleteMarker
}

// DeleteObject creates a delete marker for key (S3 versioning semantics).
// If versionID is specified, that specific version is permanently removed.
// Returns the delete marker's version ID, or "" if a specific version was deleted.
func (m *MetaStore) DeleteObject(bucket, key, versionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cache[bucket] {
		return "", ErrNoBucket
	}
	vs, ok := m.objects[bucket]
	if !ok {
		return "", ErrNoKey
	}
	vst, ok := vs[key]
	if !ok {
		return "", ErrNoKey
	}
	if versionID != "" {
		// Delete a specific version permanently.
		for i := range vst.Versions {
			if vst.Versions[i].Meta.VersionID == versionID {
				// Remove the version from the slice.
				vst.Versions = append(vst.Versions[:i], vst.Versions[i+1:]...)
				// If no versions left, remove the key entirely.
				if len(vst.Versions) == 0 {
					delete(vs, key)
				} else {
					vst.Current = len(vst.Versions) - 1
				}
				return "", nil
			}
		}
		return "", ErrNoKey
	}
	// Create a delete marker (no versionID returned — caller doesn't need it
	// for the S3 response, only for internal tracking).
	marker := ObjectMeta{
		Key:            key,
		IsDeleteMarker: true,
		CreatedAt:      time.Now(),
	}
	vid := vst.NextID
	vst.NextID++
	marker.VersionID = strconv.FormatInt(vid, 10)
	vst.Versions = append(vst.Versions, versionEntry{Meta: marker})
	vst.Current = len(vst.Versions) - 1
	return marker.VersionID, nil
}

// DeleteObjectLegacy is the non-versioned delete for backward compatibility
// with existing code paths that don't pass a version ID.
func (m *MetaStore) DeleteObjectLegacy(bucket, key string) error {
	_, err := m.DeleteObject(bucket, key, "")
	return err
}

// ListObjectVersions returns all versions for a bucket, sorted by key then version (newest first).
func (m *MetaStore) ListObjectVersions(bucket, prefix string) (versions []ObjectVersion, deleteMarkers []ObjectVersion) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bucketObjs, ok := m.objects[bucket]
	if !ok {
		return nil, nil
	}
	for key, vst := range bucketObjs {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		// Walk versions newest-first.
		for i := len(vst.Versions) - 1; i >= 0; i-- {
			v := vst.Versions[i].Meta
			ov := ObjectVersion{
				Key:          v.Key,
				VersionID:    v.VersionID,
				IsLatest:     i == vst.Current,
				LastModified: v.CreatedAt.Format(time.RFC3339),
				ETag:         `"` + v.ETag + `"`,
				Size:         v.Size,
				StorageClass: "STANDARD",
			}
			if v.IsDeleteMarker {
				deleteMarkers = append(deleteMarkers, ov)
			} else {
				versions = append(versions, ov)
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].Key != versions[j].Key {
			return versions[i].Key < versions[j].Key
		}
		return versions[i].VersionID > versions[j].VersionID
	})
	sort.Slice(deleteMarkers, func(i, j int) bool {
		if deleteMarkers[i].Key != deleteMarkers[j].Key {
			return deleteMarkers[i].Key < deleteMarkers[j].Key
		}
		return deleteMarkers[i].VersionID > deleteMarkers[j].VersionID
	})
	return
}

// ObjectVersion is the XML-mappable version entry for ListObjectVersions.
type ObjectVersion struct {
	Key          string
	VersionID    string
	IsLatest     bool
	LastModified string
	ETag         string
	Size         int64
	StorageClass string
}

// ListObjectsPaged returns a page of objects matching prefix, sorted by key.
// Only the current (latest non-delete-marker) version of each key is returned.
func (m *MetaStore) ListObjectsPaged(bucket, prefix, startAfter string, maxKeys int) (objs []ObjectMeta, nextToken string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	if bucketObjs, ok := m.objects[bucket]; ok {
		for k, vst := range bucketObjs {
			if prefix != "" && !strings.HasPrefix(k, prefix) {
				continue
			}
			if k <= startAfter {
				continue
			}
			if vst.Current < 0 || vst.Current >= len(vst.Versions) {
				continue
			}
			cur := &vst.Versions[vst.Current].Meta
			if cur.IsDeleteMarker {
				continue
			}
			objs = append(objs, *cur)
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
func (m *MetaStore) Snapshot(w io.Writer) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.NewEncoder(w).Encode(map[string]interface{}{
		"objects": m.objects,
	})
}

// Restore loads object metadata from a Raft snapshot.
func (m *MetaStore) Restore(r io.Reader) error {
	var v struct {
		Objects map[string]map[string]*versionState `json:"objects"`
	}
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		return err
	}
	buckets, _ := m.buckets.List()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache = make(map[string]bool, len(buckets))
	for _, b := range buckets {
		m.cache[b.Name] = true
	}
	if v.Objects != nil {
		for b, objs := range v.Objects {
			if m.objects[b] == nil {
				m.objects[b] = make(map[string]*versionState)
			}
			for k, vs := range objs {
				m.objects[b][k] = vs
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
