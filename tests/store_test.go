// Black-box tests for the storage engine (standalone mode, no Raft).
package tests

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/c0ldheat/jario/internal/store"
)

func TestStoreCreateBucket(t *testing.T) {
	s := mustNewStore(t)
	err := s.CreateBucket("test-bucket")
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	buckets := s.ListBuckets()
	if len(buckets) != 1 || buckets[0].Name != "test-bucket" {
		t.Fatalf("expected 1 bucket, got %v", buckets)
	}
}

func TestStoreCreateBucketDuplicate(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	err := s.CreateBucket("test-bucket")
	if !errors.Is(err, store.ErrBucketExists) {
		t.Fatalf("expected ErrBucketExists, got %v", err)
	}
}

func TestStoreDeleteBucket(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	if err := s.DeleteBucket("test-bucket"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if len(s.ListBuckets()) != 0 {
		t.Fatal("expected 0 buckets after delete")
	}
}

func TestStoreDeleteBucketNonexistent(t *testing.T) {
	s := mustNewStore(t)
	err := s.DeleteBucket("no-bucket")
	if !errors.Is(err, store.ErrNoBucket) {
		t.Fatalf("expected ErrNoBucket, got %v", err)
	}
}

func TestStorePutGetObject(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	etag, vid, err := s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if etag == "" {
		t.Fatal("expected non-empty etag")
	}
	if vid == "" {
		t.Fatal("expected non-empty version ID")
	}
	rc, meta, err := s.GetObject("test-bucket", "hello.txt", "")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello world" {
		t.Fatalf("expected 'hello world', got '%s'", string(data))
	}
	if meta.Size != 11 {
		t.Fatalf("expected size 11, got %d", meta.Size)
	}
	if meta.VersionID != vid {
		t.Fatalf("expected version %s, got %s", vid, meta.VersionID)
	}
}

func TestStorePutObjectNoBucket(t *testing.T) {
	s := mustNewStore(t)
	_, _, err := s.PutObject("no-bucket", "hello.txt", strings.NewReader("hello"))
	if !errors.Is(err, store.ErrNoBucket) {
		t.Fatalf("expected ErrNoBucket, got %v", err)
	}
}

func TestStoreGetObjectNonexistent(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	_, _, err := s.GetObject("test-bucket", "no-key", "")
	if !errors.Is(err, store.ErrNoKey) {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}
}

func TestStoreDeleteObject(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	_, vid, _ := s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	// Delete with specific version to permanently remove the object.
	_, err := s.DeleteObject("test-bucket", "hello.txt", vid)
	if err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	_, _, err = s.GetObject("test-bucket", "hello.txt", "")
	if !errors.Is(err, store.ErrNoKey) {
		t.Fatal("expected ErrNoKey after delete")
	}
}

func TestStoreDeleteObjectCreatesMarker(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	// DELETE without version ID creates a delete marker.
	_, err := s.DeleteObject("test-bucket", "hello.txt", "")
	if err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	// Current version is now a delete marker — GetObject returns ErrNoKey.
	_, _, err = s.GetObject("test-bucket", "hello.txt", "")
	if !errors.Is(err, store.ErrNoKey) {
		t.Fatal("expected ErrNoKey when current version is delete marker")
	}
}

func TestStoreVersioningMultipleVersions(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	_, v1, _ := s.PutObject("test-bucket", "doc.txt", strings.NewReader("v1"))
	_, _, _ = s.PutObject("test-bucket", "doc.txt", strings.NewReader("v2"))
	_, v3, _ := s.PutObject("test-bucket", "doc.txt", strings.NewReader("v3"))
	// Current version is v3.
	_, meta, _ := s.GetObject("test-bucket", "doc.txt", "")
	if meta.VersionID != v3 {
		t.Fatalf("expected current version %s, got %s", v3, meta.VersionID)
	}
	// Read specific versions.
	_, m1, _ := s.GetObject("test-bucket", "doc.txt", v1)
	if m1.VersionID != v1 {
		t.Fatalf("expected version %s, got %s", v1, m1.VersionID)
	}
	// Check content via GetMetaVersion.
	m1, _ = s.GetMetaVersion("test-bucket", "doc.txt", v1)
	if m1.Size != 2 {
		t.Fatalf("expected size 2 for v1, got %d", m1.Size)
	}
	// List all versions — should have 3.
	versions, _ := s.ListObjectVersions("test-bucket", "")
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
}

func TestStoreListObjectsPaged(t *testing.T) {
	s := mustNewStore(t)
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "b/1.txt", strings.NewReader("3"))
	s.PutObject("test-bucket", "a/2.txt", strings.NewReader("2"))
	s.PutObject("test-bucket", "a/1.txt", strings.NewReader("1"))

	// Full listing: prefix filter + sorted by key.
	objs, next := s.ListObjectsPaged("test-bucket", "a/", "", 1000)
	if len(objs) != 2 || next != "" {
		t.Fatalf("expected 2 objs, no next token, got %d, %q", len(objs), next)
	}
	if objs[0].Key != "a/1.txt" || objs[1].Key != "a/2.txt" {
		t.Fatalf("expected sorted keys, got %s, %s", objs[0].Key, objs[1].Key)
	}

	// First page of 2 over all keys.
	page, next := s.ListObjectsPaged("test-bucket", "", "", 2)
	if len(page) != 2 || next == "" {
		t.Fatalf("expected 2 objs + next token, got %d, %q", len(page), next)
	}
	// Second page continues after the token.
	page2, next2 := s.ListObjectsPaged("test-bucket", "", next, 2)
	if len(page2) != 1 || next2 != "" {
		t.Fatalf("expected last page of 1, got %d, %q", len(page2), next2)
	}
	if page2[0].Key != "b/1.txt" {
		t.Fatalf("expected b/1.txt, got %s", page2[0].Key)
	}
}
