package store

import (
	"io"
	"strings"
	"testing"
)

func TestCreateBucket(t *testing.T) {
	s := New(t.TempDir())
	err := s.CreateBucket("test-bucket")
	if err != nil {
		t.Fatalf("CreateBucket failed: %v", err)
	}
	buckets := s.ListBuckets()
	if len(buckets) != 1 || buckets[0].Name != "test-bucket" {
		t.Fatalf("expected 1 bucket 'test-bucket', got %v", buckets)
	}
}

func TestCreateBucketDuplicate(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	err := s.CreateBucket("test-bucket")
	if err == nil {
		t.Fatal("expected error on duplicate bucket")
	}
}

func TestDeleteBucket(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	err := s.DeleteBucket("test-bucket")
	if err != nil {
		t.Fatalf("DeleteBucket failed: %v", err)
	}
	if len(s.ListBuckets()) != 0 {
		t.Fatal("expected 0 buckets after delete")
	}
}

func TestPutGetObject(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	etag, err := s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}
	if etag == "" {
		t.Fatal("expected non-empty etag")
	}
	rc, meta, err := s.GetObject("test-bucket", "hello.txt")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello world" {
		t.Fatalf("expected 'hello world', got '%s'", string(data))
	}
	if meta.Size != 11 {
		t.Fatalf("expected size 11, got %d", meta.Size)
	}
}

func TestDeleteObject(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	err := s.DeleteObject("test-bucket", "hello.txt")
	if err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
	_, _, err = s.GetObject("test-bucket", "hello.txt")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestListObjects(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "a/1.txt", strings.NewReader("1"))
	s.PutObject("test-bucket", "a/2.txt", strings.NewReader("2"))
	s.PutObject("test-bucket", "b/1.txt", strings.NewReader("3"))
	objs := s.ListObjects("test-bucket", "a/")
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects with prefix, got %d", len(objs))
	}
	objs = s.ListObjects("test-bucket", "")
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objs))
	}
}
