package raft

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/c0ldheat/jario/internal/store"
)

// newTestNode creates a single-node Raft cluster for testing.
func newTestNode(t *testing.T) (*RaftNode, *store.MetaStore) {
	t.Helper()
	meta := store.NewMetaStore()
	node, err := New("test-node", t.TempDir(), "localhost:0", meta)
	if err != nil {
		t.Fatalf("raft.New: %v", err)
	}
	t.Cleanup(func() { node.Close() })

	// Bootstrap single-node cluster.
	node.Bootstrap()

	// Wait for leader election.
	time.Sleep(500 * time.Millisecond)
	if !node.IsLeader() {
		t.Fatal("single-node raft should be leader after bootstrap")
	}
	return node, meta
}

func TestRaftApplyCreateBucket(t *testing.T) {
	node, meta := newTestNode(t)

	if err := node.Apply(Op{Kind: "create_bucket", Bucket: "mybucket"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	buckets := meta.ListBuckets()
	if len(buckets) != 1 || buckets[0].Name != "mybucket" {
		t.Fatalf("expected bucket 'mybucket', got %v", buckets)
	}
}

func TestRaftApplyPutObject(t *testing.T) {
	node, meta := newTestNode(t)

	meta.CreateBucket("images")
	objMeta := store.ObjectMeta{Key: "photo.jpg", Size: 1024, ETag: "abc123", Sha256: "abc123", CreatedAt: time.Now()}
	metaJSON, _ := json.Marshal(objMeta)

	if err := node.Apply(Op{Kind: "put_object", Bucket: "images", Key: "photo.jpg", Meta: metaJSON}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	obj, err := meta.GetObject("images", "photo.jpg")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if obj.Size != 1024 {
		t.Fatalf("expected size 1024, got %d", obj.Size)
	}
}

func TestRaftApplyDeleteObject(t *testing.T) {
	node, meta := newTestNode(t)

	meta.CreateBucket("data")
	meta.PutObject("data", "file.txt", store.ObjectMeta{Key: "file.txt", Sha256: "aaa"})

	if err := node.Apply(Op{Kind: "delete_object", Bucket: "data", Key: "file.txt"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	_, err := meta.GetObject("data", "file.txt")
	if err == nil || !strings.Contains(err.Error(), "NoSuchKey") {
		t.Fatalf("expected NoSuchKey, got %v", err)
	}
}

func TestRaftApplyDeleteBucket(t *testing.T) {
	node, meta := newTestNode(t)

	meta.CreateBucket("temp")
	if err := node.Apply(Op{Kind: "delete_bucket", Bucket: "temp"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(meta.ListBuckets()) != 0 {
		t.Fatal("expected 0 buckets after delete")
	}
}

func TestRaftSnapshotRestore(t *testing.T) {
	// Test MetaStore snapshot/restore directly — this is what the FSM uses.
	meta := store.NewMetaStore()
	meta.CreateBucket("snap-bucket")
	meta.PutObject("snap-bucket", "doc.txt", store.ObjectMeta{
		Key: "doc.txt", Size: 50, ETag: "snap1", Sha256: "snap1", CreatedAt: time.Now(),
	})

	// Snapshot.
	var buf strings.Builder
	if err := meta.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Restore into a fresh MetaStore.
	newMeta := store.NewMetaStore()
	if err := newMeta.Restore(strings.NewReader(buf.String())); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify restored state.
	buckets := newMeta.ListBuckets()
	if len(buckets) != 1 || buckets[0].Name != "snap-bucket" {
		t.Fatalf("restored buckets: got %v", buckets)
	}
	obj, err := newMeta.GetObject("snap-bucket", "doc.txt")
	if err != nil {
		t.Fatalf("restored GetObject: %v", err)
	}
	if obj.Size != 50 {
		t.Fatalf("restored size: got %d", obj.Size)
	}
}
