package store

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCreateBucket(t *testing.T) {
	s := New(t.TempDir())
	err := s.CreateBucket("test-bucket")
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	buckets := s.ListBuckets()
	if len(buckets) != 1 || buckets[0].Name != "test-bucket" {
		t.Fatalf("expected 1 bucket, got %v", buckets)
	}
}

func TestCreateBucketDuplicate(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	err := s.CreateBucket("test-bucket")
	if !errors.Is(err, ErrBucketExists) {
		t.Fatalf("expected ErrBucketExists, got %v", err)
	}
}

func TestDeleteBucket(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	if err := s.DeleteBucket("test-bucket"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if len(s.ListBuckets()) != 0 {
		t.Fatal("expected 0 buckets after delete")
	}
}

func TestDeleteBucketNonexistent(t *testing.T) {
	s := New(t.TempDir())
	err := s.DeleteBucket("no-bucket")
	if !errors.Is(err, ErrNoBucket) {
		t.Fatalf("expected ErrNoBucket, got %v", err)
	}
}

func TestPutGetObject(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	etag, err := s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if etag == "" {
		t.Fatal("expected non-empty etag")
	}
	rc, meta, err := s.GetObject("test-bucket", "hello.txt")
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
}

func TestPutObjectNoBucket(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.PutObject("no-bucket", "hello.txt", strings.NewReader("hello"))
	if !errors.Is(err, ErrNoBucket) {
		t.Fatalf("expected ErrNoBucket, got %v", err)
	}
}

func TestGetObjectNonexistent(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	_, _, err := s.GetObject("test-bucket", "no-key")
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}
}

func TestDeleteObject(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "hello.txt", strings.NewReader("hello"))
	if err := s.DeleteObject("test-bucket", "hello.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	_, _, err := s.GetObject("test-bucket", "hello.txt")
	if !errors.Is(err, ErrNoKey) {
		t.Fatal("expected ErrNoKey after delete")
	}
}

func TestListObjects(t *testing.T) {
	s := New(t.TempDir())
	s.CreateBucket("test-bucket")
	s.PutObject("test-bucket", "b/1.txt", strings.NewReader("3"))
	s.PutObject("test-bucket", "a/2.txt", strings.NewReader("2"))
	s.PutObject("test-bucket", "a/1.txt", strings.NewReader("1"))
	objs := s.ListObjects("test-bucket", "a/")
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects with prefix, got %d", len(objs))
	}
	if objs[0].Key != "a/1.txt" || objs[1].Key != "a/2.txt" {
		t.Fatalf("expected sorted keys, got %s, %s", objs[0].Key, objs[1].Key)
	}
	objs = s.ListObjects("test-bucket", "")
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objs))
	}
}
